# 微信视频号关键词优先采集 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让微信视频号关键词采集只依赖 `work_id + object_nonce_id`，将公开分享链接降级为可选增强，并让 TrendRadar 导入无分享链接的关键词结果。

**Architecture:** `wx_channel` 在 `finderSearch` 后保留具备评论身份的作品，best-effort 解析分享链接并输出批次 v3；TrendRadar 保留 v1/v2 严格兼容，新增 v3 的可空 `public_url` 语义，关键词目标按 `content_id` 导入，显式链接目标继续校验 URL。分享链接配置后端继续兼容，但管理 UI 改为隐藏字段以避免覆盖旧配置。

**Tech Stack:** Go、`go test`、Python 3、pytest、TrendRadar SQLite 社交导入器、Jinja/原生 JavaScript 管理界面。

---

## 工作区约束

- 微信仓库：`D:\Agent\projects\wechat-channel-comment-poc`，直接在本地 `main` 改造。
- TrendRadar 仓库：`D:\Agent\services\trendradar-monitor`，直接在本地 `master` 改造。
- 不创建 worktree，不重置、不清理 TrendRadar 已有修改、输出目录或备份文件。
- 不修改用户未跟踪的 `D:\Agent\projects\wechat-channel-comment-poc\AGENTS.md`。
- 每个阶段只暂存自己修改的文件；验证时记录两个仓库的分支、提交号和工作区状态。

## 文件映射

### 微信仓库

- 修改 `internal/poc/ltaoo_client.go`：关键词结果身份过滤与分享链接 best-effort 逻辑。
- 修改 `internal/poc/ltaoo_batch.go`：批次协议版本提升到 v3，并保留现有显式链接流程。
- 修改 `internal/poc/ltaoo_client_test.go`：搜索结果缺少分享链接时仍保留作品的回归测试。
- 修改 `internal/poc/ltaoo_batch_test.go`：v3 输出和缺失分享链接的批次测试。
- 修改 `docs/trendradar-runtime.md`：记录 v3 关键词批次与可选分享链接语义。

### TrendRadar 仓库

- 修改 `trendradar/social/wechat_channels_batch.py`：识别并验证 v3，允许 v3 的空 `public_url`，修正问题映射。
- 修改 `trendradar/social/wechat_channels_importer.py`：关键词模式按作品 ID 导入，显式 URL 模式继续严格校验。
- 修改 `trendradar/admin/templates/profile_edit.html`：保留已有 `content_urls` 的隐藏字段，移除可见分享链接输入区。
- 修改 `trendradar/admin/static/admin.js`：兼容隐藏字段，不再尝试显示或校验已隐藏的文本框。
- 修改 `tests/test_social_wechat_channels_batch.py`：v3 校验、空 URL 和错误映射回归测试。
- 修改 `tests/test_social_wechat_channels_importer.py`：无 URL 关键词导入和显式 URL 严格校验测试。
- 修改 `tests/test_admin_wechat_channels_ui.py`：UI 隐藏但保留字段的测试。
- 必要时新增 `tests/fixtures/wechat_channels_batch_v3/`，由现有 v2 fixture 派生并只修改 schema 与 `public_url` 为 `null`；不改 v1/v2 fixture。

## Task 1: 先锁定微信采集器的无分享链接行为

**Files:**
- Modify: `D:\Agent\projects\wechat-channel-comment-poc\internal\poc\ltaoo_client_test.go`
- Modify: `D:\Agent\projects\wechat-channel-comment-poc\internal\poc\ltaoo_client.go`

- [ ] **Step 1: 写回归测试，模拟分享链接接口返回不可解析字段**

在 `ltaoo_client_test.go` 增加测试服务器：`finderSearch`/`finderUserPage` 返回一个带
`id=work-without-share`、`objectNonceId=nonce-without-share` 的作品，
`/api/channels/feed/share_url` 返回 `feedH5Url` 缺失或非 `weixin.qq.com/sph` URL。
调用 `collectWorksFromSearch`，断言：

```go
if len(works) != 1 || dereference(works[0].WorkID) != "work-without-share" {
    t.Fatalf("works=%+v", works)
}
if works[0].Locator.PublicURL != nil {
    t.Fatalf("public URL = %v, want nil", works[0].Locator.PublicURL)
}
if len(issues) != 1 || issues[0].Code != "share_url_unavailable" {
    t.Fatalf("issues=%+v", issues)
}
```

- [ ] **Step 2: 运行单测确认当前实现失败**

运行：`go test ./internal/poc -run TestCollectWorksFromSearchKeepsWorkWhenShareURLUnavailable -count=1`

预期：失败，因为当前 `collectWorksFromSearch` 在 `ResolveShareURL` 出错时 `continue` 丢弃作品。

- [ ] **Step 3: 实现关键词身份过滤和 best-effort URL 解析**

调整 `collectWorksFromSearch` 的逐作品循环：

```go
if works[index].WorkID == nil || dereferenceString(works[index].WorkID) == "" ||
    works[index].ObjectNonceID == nil || dereferenceString(works[index].ObjectNonceID) == "" {
    issues = append(issues, Issue{Stage: "content", Code: "work_identity_unavailable", InputIndex: index + 1})
    continue
}
if shareURL, resolveErr := client.ResolveShareURL(ctx, dereferenceString(works[index].WorkID)); resolveErr == nil {
    works[index].Locator.PublicURL = stringPointer(shareURL)
} else {
    issues = append(issues, Issue{Stage: "content", Code: "share_url_unavailable", InputIndex: index + 1})
}
resolved = append(resolved, works[index])
```

保留原有分享链接成功路径；不要改变 `CollectWorksFromURLs`。

- [ ] **Step 4: 运行单测确认回归通过**

运行：`go test ./internal/poc -run 'TestCollectWorksFromSearchKeepsWorkWhenShareURLUnavailable|TestCollectWorksFromSearchUsesReadinessWindow|TestLtaooClientSupportsFinderSearchFeedAndShareURL' -count=1`

预期：全部 PASS。

## Task 2: 将微信批次输出切换到 v3

**Files:**
- Modify: `D:\Agent\projects\wechat-channel-comment-poc\internal\poc\ltaoo_batch.go`
- Modify: `D:\Agent\projects\wechat-channel-comment-poc\internal\poc\ltaoo_batch_test.go`
- Modify: `D:\Agent\projects\wechat-channel-comment-poc\docs\trendradar-runtime.md`

- [ ] **Step 1: 写批次协议测试**

将现有关键词批次测试扩展为：分享 URL 接口返回无效字段时，`RunLtaooBatch` 仍输出一个作品、评论流程继续执行、`contents.jsonl` 的 `public_url` 为 `null`、`issues.jsonl` 含 `share_url_unavailable`，最终状态为 `partial` 而不是 `failed`。

同时把成功输出断言改为 `wechat-channels-batch/3`，并保留 URL 成功时仍写入规范 URL 的断言。

- [ ] **Step 2: 运行批次测试确认版本断言和新场景失败**

运行：`go test ./internal/poc -run 'TestRunAndFinalizeLtaooBatch|TestRunLtaooBatchKeywordDiscovery' -count=1`

预期：新 v3/无 URL 场景在实现前失败，现有 v2 版本断言也会提示待更新。

- [ ] **Step 3: 更新批次版本常量和内部校验**

将 `BatchSchemaVersion` 改为 `wechat-channels-batch/3`。让 `RunLtaooBatch`、`FinalizeLtaooBatch`、`collection-result.json` 和最终 `manifest.json` 使用同一版本；不要改变请求 schema、清理流程、媒体资产路径或显式 URL 分支。

- [ ] **Step 4: 更新运行时文档**

在 `docs/trendradar-runtime.md` 的关键词说明中明确：v3 输出允许 `locator.public_url=null`，评论身份来自 `work_id/object_nonce_id`；分享链接解析失败只产生部分诊断，不代表页面结构变化。

- [ ] **Step 5: 运行微信全量单测**

运行：`go test ./...`

预期：微信仓库所有测试 PASS。

## Task 3: TrendRadar 支持 v3 批次验证

**Files:**
- Modify: `D:\Agent\services\trendradar-monitor\trendradar\social\wechat_channels_batch.py`
- Create/Modify: `D:\Agent\services\trendradar-monitor\tests\fixtures\wechat_channels_batch_v3\*`
- Modify: `D:\Agent\services\trendradar-monitor\tests\test_social_wechat_channels_batch.py`

- [ ] **Step 1: 增加 v3 fixture 和失败测试**

从 v2 fixture 派生 v3 fixture，设置 manifest、文件哈希和 `contents.jsonl` 的
`locator.public_url` 为 `null`，保留评论和媒体资产结构。先增加测试：

```python
verified = verify_wechat_channels_batch(_FIXTURE_V3, expected_run_id="fixture-run-3")
assert verified.status is RunStatus.SUCCEEDED
assert verified.contents[0]["locator"]["public_url"] is None
```

并增加错误映射断言：包含 `share_url_unavailable` 的 v3 批次返回的诊断代码不包含
`page_structure_changed`。

- [ ] **Step 2: 运行单测确认 v3 当前被拒绝**

运行：`pytest -q tests/test_social_wechat_channels_batch.py -k 'v3 or share_url'`

预期：当前实现因不认识 v3 或拒绝空 URL 而失败。

- [ ] **Step 3: 实现 v3 验证分支**

在 `_verify_wechat_channels_batch` 增加 v3 分支；复用 v2 的文件和媒体布局，但传入
`schema_version="wechat-channels-batch/3"`。为 `_validate_contents` 增加
`allow_empty_share_url` 参数：v1/v2 传 `False`，v3 传 `True`；v3 仅接受 `None` 或
合法 `https://weixin.qq.com/sph/...`，不接受空字符串、其他域名或带查询参数 URL。

将 `_validate_comments` 的 v2 媒体字段分支扩展到 v3，将媒体资产校验条件扩展到 v3。
为 `_ISSUE_MAP` 增加 `("content", "work_identity_unavailable")`，并把
`("content", "share_url_unavailable")` 映射成明确的 `share_url_unavailable` 诊断，
不再使用 `page_structure_changed`。

- [ ] **Step 4: 运行批次验证测试**

运行：`pytest -q tests/test_social_wechat_channels_batch.py`

预期：v1/v2 原测试和新增 v3 测试全部 PASS。

## Task 4: TrendRadar 按模式导入可空 URL

**Files:**
- Modify: `D:\Agent\services\trendradar-monitor\trendradar\social\wechat_channels_importer.py`
- Modify: `D:\Agent\services\trendradar-monitor\tests\test_social_wechat_channels_importer.py`

- [ ] **Step 1: 增加关键词无 URL 导入测试**

使用 v3 fixture 和 `replace(_target(), content_urls=())`，断言导入成功、作品
`content_id` 正确、`content_url == ""`、评论数量保持不变。

同时增加显式链接模式测试：使用同一个无 URL v3 fixture 和非空 `content_urls`，断言
导入抛出“缺少或不匹配分享链接”的错误；使用带 URL v3 fixture 时显式模式仍成功。

- [ ] **Step 2: 运行导入测试确认当前失败**

运行：`pytest -q tests/test_social_wechat_channels_importer.py -k 'keyword or url'`

预期：当前 `_content` 强制 `_required_text(locator["public_url"])`，无 URL 测试失败。

- [ ] **Step 3: 实现按目标模式的导入规则**

将 `import_wechat_channels_batch` 的 URL allowlist 分为两种：

```python
explicit_urls = frozenset(target.content_urls)
keyword_mode = not explicit_urls
allowed_urls = explicit_urls
if keyword_mode:
    allowed_urls = frozenset(
        value for row in batch.contents
        if (value := _optional_text(_mapping(row)["locator"]["public_url"]))
    )
```

将 `_content` 改为接收 `keyword_mode`：关键词模式允许 `public_url is None` 并输出
`content_url=""`；显式模式仍要求合法 URL 且必须在 `target.content_urls` 中。不要以
URL 作为关键词模式的内容身份，评论继续通过 `work_id` 关联。

- [ ] **Step 4: 运行导入和社交平台测试**

运行：`pytest -q tests/test_social_wechat_channels_importer.py tests/test_social_platforms.py -k 'wechat'`

预期：v1/v2 兼容测试、关键词空 URL 测试、显式 URL 严格测试全部 PASS。

## Task 5: 隐藏 UI 分享链接入口但保留旧配置

**Files:**
- Modify: `D:\Agent\services\trendradar-monitor\trendradar\admin\templates\profile_edit.html`
- Modify: `D:\Agent\services\trendradar-monitor\trendradar\admin\static\admin.js`
- Modify: `D:\Agent\services\trendradar-monitor\tests\test_admin_wechat_channels_ui.py`

- [ ] **Step 1: 更新 UI 测试**

保留两个 `data-target-field="content_urls"` 字段断言，但改为断言它们是隐藏
`input`，且不再存在可见 `textarea`；继续断言提交脚本读取 `content_urls`。

- [ ] **Step 2: 运行 UI 测试确认当前实现不符合隐藏约束**

运行：`pytest -q tests/test_admin_wechat_channels_ui.py`

预期：当前模板仍包含可见分享链接 textarea，新增断言失败。

- [ ] **Step 3: 用隐藏 input 保存旧配置**

将两个分享链接 textarea 替换为：

```html
<input type="hidden" data-target-field="content_urls"
       value="{{ target.content_urls | join('\\n') }}">
```

新建目标的模板使用空 value。保留 `data-target-field`，使现有 `buildTargetPayload`
继续提交已保存的 URL，避免用户编辑其他字段时清空旧配置。

- [ ] **Step 4: 简化 JS 对隐藏字段的处理**

`validateWeChatChannelLinks` 遇到非 `textarea` 直接返回 true；
`syncWeChatChannelSettings` 不再尝试显示隐藏 legacy 设置，但保留平台切换时的
校验清理；`buildTargetPayload` 继续从隐藏字段读取数组。不要删除后端 payload 字段。

- [ ] **Step 5: 运行 UI 和 profile 测试**

运行：`pytest -q tests/test_admin_wechat_channels_ui.py tests/test_admin_profile_routes.py`

预期：隐藏入口、旧配置保留和 profile 保存行为全部 PASS。

## Task 6: 跨仓库回归与隔离联调

**Files:**
- No new source files; inspect only the outputs under the existing TrendRadar output root.

- [ ] **Step 1: 运行两个仓库的目标测试和全量测试**

微信仓库：`go test ./...`

TrendRadar 仓库：`pytest -q tests/test_social_wechat_channels_batch.py tests/test_social_wechat_channels_importer.py tests/test_social_platforms.py tests/test_admin_wechat_channels_ui.py`

若全量 TrendRadar 测试受既有工作区服务/输出影响，只报告具体失败，不清理或覆盖用户文件。

- [ ] **Step 2: 重新构建微信实际程序**

使用仓库批准的 Go/CGO 工具链从当前源码构建实际批处理程序，记录构建产物路径、
提交号和构建命令；不得复用旧二进制。

- [ ] **Step 3: 在已授权新版 ltaoo 环境运行关键词批次**

使用空 `content_urls` 和实际平台检索词运行一次，确认：搜索结果进入评论 API、输出
批次 schema v3、无分享链接的作品不会被丢弃、TrendRadar 导入后 `content_url` 可为空。

- [ ] **Step 4: 记录验证证据并检查工作区**

记录微信和 TrendRadar 的分支、HEAD、未提交文件、实际构建产物、ltaoo 路径、运行时、
数据库和输出目录；确认没有修改无关的 TrendRadar 文件。

- [ ] **Step 5: 按用户后续指示提交/推送**

当前计划不自动提交功能代码；验证完成后先报告差异和测试结果，等待用户明确要求提交、
推送或创建 PR。
