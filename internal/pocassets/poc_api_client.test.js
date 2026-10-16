'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const source = fs.readFileSync(path.join(__dirname, 'poc_api_client.js'), 'utf8');

for (const method of ['finderSearch', 'finderUserPage', 'finderGetCommentDetail', 'finderGetCommentList']) {
  assert(source.includes(method), `missing allowed method ${method}`);
}
for (const forbidden of ['console.', 'fetch(', 'commentLike', 'commentPost', 'finderPost', 'publish', 'download', '?token=']) {
  assert(!source.includes(forbidden), `forbidden bridge capability ${forbidden}`);
}
assert(source.includes("'target_context_mismatch'"));
assert(source.includes("'page_api_failed'"));
assert(source.includes('Number(body.type) === 3 ? 19 : 13'));
assert(source.includes('lastBuff: body.next_marker ||'));
assert(!source.includes('profileURLForUsername'), 'bridge must not manufacture a profile URL from finderUsername');
assert(!source.includes('invokePageAPI'), 'bridge must return page API payloads without enriching account URLs');
assert(source.includes('return WXU.API2.finderSearch'));
assert(source.includes('return WXU.API.finderUserPage'));
assert(!/errMsg\s*:\s*(?:text|error\.message)/.test(source), 'raw error text is sent to the bridge');

async function runBridgeCall(method, body, api) {
  const sockets = [];
  class FakeWebSocket {
    static OPEN = 1;

    constructor(url, protocols) {
      this.url = url;
      this.protocols = protocols;
      this.readyState = FakeWebSocket.OPEN;
      this.sent = [];
      sockets.push(this);
    }

    send(raw) {
      this.sent.push(JSON.parse(raw));
    }
  }

  const window = {
    __WX_CHANNEL_POC_CONFIG__: {port: 2026, token: 'fixture-token'},
    WXU: api,
    __wx_username: 'current-user',
  };
  const document = {hidden: false, addEventListener() {}};
  vm.runInNewContext(source, {window, WXU: api, WebSocket: FakeWebSocket, URL, document, location: {pathname: '/web/pages/feed'}});

  const socket = sockets[0];
  socket.onopen();
  await socket.onmessage({data: JSON.stringify({type: 'api_call', data: {id: 'fixture-call', method, body}})});
  const response = socket.sent.findLast((message) => message.type === 'api_response');
  assert(response, `bridge did not respond: ${JSON.stringify(socket.sent)}`);
  return response;
}

(async () => {
  const result = await runBridgeCall('finderSearch', {keyword: 'fixture', type: 3}, {
    API2: {
      async finderSearch() {
        return {data: {objectList: [{contact: {username: 'fixture-author@finder', nickname: 'Fixture Author'}}]}};
      },
    },
    API: {},
  });
  assert.strictEqual(result.data.errCode, 0);
  assert.strictEqual(
    result.data.data.data.objectList[0].contact.profileUrl,
    undefined,
    'bridge must not infer a public profile URL from an internal username',
  );

  const returnedProfile = await runBridgeCall('finderSearch', {keyword: 'fixture', type: 3}, {
    API2: {
      async finderSearch() {
        return {data: {objectList: [{contact: {
          username: 'fixture-author@finder',
          profileUrl: 'https://channels.weixin.qq.com/web/pages/profile?finderUsername=fixture-author%40finder',
        }}]}};
      },
    },
    API: {},
  });
  assert.strictEqual(
    returnedProfile.data.data.data.objectList[0].contact.profileUrl,
    'https://channels.weixin.qq.com/web/pages/profile?finderUsername=fixture-author%40finder',
    'bridge must preserve a profile URL explicitly returned by WeChat',
  );
})().catch((error) => {
  process.nextTick(() => { throw error; });
});
