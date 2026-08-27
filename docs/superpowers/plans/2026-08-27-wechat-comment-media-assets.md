# WeChat Comment Media Assets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Let WeChat Channels comments carry verified image and sticker files from the authorized wx_channel runtime into TrendRadar's existing media storage and vision-extraction flow without exporting URLs, keys, cookies, or other session material.

**Architecture:** wx_channel keeps remote media locators and sticker decryption secrets in memory, validates media locally, and writes only content-addressed files plus a closed schema-v2 asset manifest. TrendRadar validates v2 and imports local hash-verified files through its existing media object store, repository, vision extraction, reporting, and cleanup flow. Schema v1 remains readable, while v1/v2 fields and artifacts cannot mix.

**Tech Stack:** Go 1.24, Windows PowerShell runtime, Python 3, pytest, SQLite-backed MediaAssetRepository, local MediaAssetStore.

---

## Scope and Contract

This is one coupled protocol change, not two independent projects. wx_channel cannot safely emit v2 until TrendRadar accepts it, and TrendRadar must not accept unverified local files. Keep existing unrelated worktree changes intact.

| Schema | Artifacts | Comment media | Import behavior |
| --- | --- | --- | --- |
| wechat-channels-batch/1 | current five root files | content.media_type | Preserve current verifier/importer; no media ingestion. |
| wechat-channels-batch/2 | v1 JSONL/receipt plus media-assets.jsonl and media/<sha256>.<ext> | content.media_types and opaque content.media_ref | Verify all files and associations, then import the local assets. |

Each v2 media-assets.jsonl row has exactly these keys:

~~~
{
  "comment_ref": "sha256:<64 lowercase hex>",
  "media_type": "image",
  "source_key": "image:<64 lowercase hex>",
  "relative_path": "media/<64 lowercase hex>.png",
  "sha256": "<64 lowercase hex>",
  "bytes": 1234,
  "mime_type": "image/png"
}
~~~

comment_ref appears in the associated comment content.media_ref. source_key derives from non-secret comment association plus verified file digest, never from a URL or key. media_type is image or sticker. relative_path is slash-separated, directly under media/, has no dot or dot-dot path segment, and its file digest must equal sha256.

A media failure may produce exactly this nonfatal issue shape:

~~~
{"stage":"media","code":"media_unavailable","work_id":"work-identifier"}
~~~

No emitted JSON, log, SQLite value, or cleanup receipt may contain remote URLs, signed query strings, AES material, headers/cookies, ciphertext, decrypted temporary filenames, or absolute paths.

## File Map

| Repository | File | Responsibility |
| --- | --- | --- |
| wx_channel | internal/poc/model.go | Safe v2 comment projection and in-memory-only candidate types. |
| wx_channel | internal/poc/comments.go | Parse contentInfo imageInfos and emoticonInfos; calculate media types and opaque comment refs. |
| wx_channel | internal/poc/comment_media.go | New bounded download/decrypt/validate/publish resolver. |
| wx_channel | internal/poc/comment_media_test.go | New candidate/resolver/security tests. |
| wx_channel | internal/poc/ltaoo_batch.go | v2 production and artifact manifest. |
| wx_channel | internal/poc/ltaoo_batch_test.go | v2 batch/failure/cleanup tests. |
| TrendRadar | trendradar/social/media_assets.py | URL-free local asset value object. |
| TrendRadar | trendradar/social/wechat_channels_batch.py | Strict v1/v2 verification. |
| TrendRadar | trendradar/social/wechat_channels_importer.py | V2 multi-type comment import and asset association. |
| TrendRadar | trendradar/social/importer.py | Carry local assets in NormalizedCommentBatch. |
| TrendRadar | trendradar/social/local_media_import.py | New no-follow local asset ingestion service. |
| TrendRadar | trendradar/social/pipeline.py and trendradar/commands/social_comments.py | Ingest after comment upsert and before existing media/vision work; wire the runtime service. |

### Task 1: Parse Safe Comment-Media Descriptors

**Files:**
- Modify: D:\Agent\projects\wechat-channel-comment-poc\internal\poc\model.go
- Modify: D:\Agent\projects\wechat-channel-comment-poc\internal\poc\comments.go
- Modify: D:\Agent\projects\wechat-channel-comment-poc\internal\poc\comments_test.go
- Create: D:\Agent\projects\wechat-channel-comment-poc\internal\poc\comment_media_test.go

- [ ] **Step 1: Write failing contentInfo tests.**

Add table-driven mapComment cases: text-only, direct-image-only, sticker-only with contentType 1, image plus sticker, empty arrays, malformed entries, and legacy no-contentInfo contentType 2. The central case is:

~~~
item := map[string]any{
    "commentId": "comment-image-sticker",
    "content": "look",
    "contentType": 1,
    "contentInfo": map[string]any{
        "imageInfos": []any{map[string]any{"Url": "https://media.example.test/a"}},
        "emoticonInfos": []any{map[string]any{
            "Url": "https://media.example.test/b",
            "EncryptUrl": "ciphertext",
            "AesKey": "key-material",
        }},
    },
}
comment, candidates, _ := mapComment(item, 1, stringPointer("work-1"), nil,
    SourceRef{Method: commentListMethod, Ordinal: 1})
if got, want := comment.Content.MediaTypes, []string{"text", "image", "sticker"};
    !reflect.DeepEqual(got, want) {
    t.Fatalf("media types=%v want=%v", got, want)
}
if len(candidates) != 2 || candidates[0].mediaType != "image" ||
    candidates[1].mediaType != "sticker" {
    t.Fatalf("candidates=%+v", candidates)
}
serialized, err := json.Marshal(comment)
if err != nil || strings.Contains(string(serialized), "ciphertext") ||
    strings.Contains(string(serialized), "key-material") {
    t.Fatalf("comment leaked private media material: %s", serialized)
}
~~~

Assert deterministic de-duplicated ordering: text, image, sticker, unknown_non_text.

- [ ] **Step 2: Verify the tests fail.**

Run: go test ./internal/poc -run 'TestMapComment(Media|ContentInfo)' -count=1

Expected: FAIL. mapComment currently returns one MediaType and no candidate slice.

- [ ] **Step 3: Implement the safe projection.**

Change CommentContent to have Text, MediaTypes, and MediaRef. Add an unexported candidate:

~~~
type commentMediaCandidate struct {
    commentRef string
    mediaType  string
    ordinal    int
    directURL  string
    cipherURL  string
    aesKey     string
}
~~~

Change mapComment to return Comment, candidate slice, field result slice. Generate MediaRef from SHA-256 of the literal domain separator, work ID, and comment identity. Use commentId when present; otherwise use only public comment fields plus source method and ordinal. Do not put a candidate in Comment, Checkpoint, Issue, or Evidence.

Add a private mediaCandidates slice to Collector and a drainCommentMediaCandidates method which returns the accumulated slice and clears it. Every mapComment call in CollectComments appends its returned candidates before the comment is de-duplicated; discard candidates if the corresponding comment is rejected as a duplicate. RunLtaooBatch drains the collector after each work. A resumed checkpoint may omit a previous media candidate, which is a nonfatal media_unavailable outcome and never changes the resumed comment/reply semantics.

Implement this closed classifier:

~~~
types := []string{}
if strings.TrimSpace(text) != "" {
    types = append(types, "text")
}
if nonEmptyInfoArray(item, "imageInfos") {
    types = append(types, "image")
}
if nonEmptyInfoArray(item, "emoticonInfos") {
    types = append(types, "sticker")
}
if len(types) == 0 {
    switch fmt.Sprint(item["contentType"]) {
    case "1":
        types = append(types, "text")
    case "2":
        types = append(types, "image")
    default:
        types = append(types, "unknown_non_text")
    }
}
return uniqueInOrder(types)
~~~

Malformed media descriptors are ignored. FieldResult may carry only the closed path comments[].content.media_types, never source content.

- [ ] **Step 4: Verify parser and existing pagination behavior.**

Run: go test ./internal/poc -run 'TestMapComment|TestCollectComments' -count=1

Expected: PASS.

- [ ] **Step 5: Commit the parser boundary.**

Run:

~~~
git add internal/poc/model.go internal/poc/comments.go internal/poc/comments_test.go internal/poc/comment_media_test.go
git commit -m "feat: parse WeChat comment media descriptors safely"
~~~

### Task 2: Resolve Media Locally Without Leaking Secrets

**Files:**
- Create: D:\Agent\projects\wechat-channel-comment-poc\internal\poc\comment_media.go
- Modify: D:\Agent\projects\wechat-channel-comment-poc\internal\poc\ltaoo_batch.go
- Modify: D:\Agent\projects\wechat-channel-comment-poc\internal\poc\comment_media_test.go

- [ ] **Step 1: Write failing resolver tests.**

Use httptest.NewServer and a private temporary batch root. Cover direct PNG/JPEG/WebP acceptance, unsupported MIME/signature rejection, redirect-to-unallowed-host rejection, size/time ceilings, duplicate-digest reuse, failed-temp cleanup, unsafe paths, and nonfatal media_unavailable. Search all JSONL files and test log captures for sentinel URL path/query, EncryptUrl, and AesKey and fail if found.

Include this success case:

~~~
resolver := newCommentMediaResolver(httpClient, mediaRoot, CommentMediaLimits{
    PerComment: 4, PerBatch: 100, PerFileBytes: 8 << 20, Timeout: 10 * time.Second,
})
asset, issue := resolver.Resolve(commentMediaCandidate{
    commentRef: "sha256:" + strings.Repeat("a", 64),
    mediaType: "image", directURL: server.URL + "/valid.png",
}, 0)
if issue != nil || asset.RelativePath != "media/"+asset.SHA256+".png" {
    t.Fatalf("asset=%+v issue=%+v", asset, issue)
}
~~~

An encrypted-sticker fixture is added only after a live structural probe establishes exact AES mode, key decoding, IV/nonce source, and ciphertext encoding. It must use locally constructed cipher bytes. Before that fact exists, encrypted stickers return media_unavailable without persisting ciphertext or plaintext; direct images may proceed.

- [ ] **Step 2: Verify resolver tests fail.**

Run: go test ./internal/poc -run 'TestCommentMediaResolver' -count=1

Expected: FAIL. The resolver and CommentMediaLimits do not exist.

- [ ] **Step 3: Implement bounded in-memory resolution.**

Create unexported resolver types. Enforce HTTPS, no user-info, an explicit host allowlist based on the probe, disabled automatic cross-host redirects, timeouts, per-comment/per-batch counts, and byte ceilings. Stream only to 0600 temporary files under draft/media/.tmp; hash while writing; validate content signature and decoded image dimensions; atomically rename to media/<sha256>.<extension>.

The only serializable result is BatchMediaAsset with comment_ref, media_type, source_key, relative_path, sha256, bytes, and mime_type. Generate source_key from media type, comment ref, verified SHA-256, and media ordinal. Never hash a URL or AES key into source_key. Decrypt in memory; remove all temporary files on every path.

- [ ] **Step 4: Verify resolver behavior and race safety.**

Run: go test ./internal/poc -run 'TestCommentMediaResolver' -count=1

Expected: PASS.

Run: go test -race ./internal/poc -run 'TestCommentMediaResolver' -count=1

Expected: PASS with no race report.

- [ ] **Step 5: Commit the resolver.**

Run:

~~~
git add internal/poc/comment_media.go internal/poc/comment_media_test.go internal/poc/ltaoo_batch.go
git commit -m "feat: resolve WeChat comment media locally"
~~~

### Task 3: Publish Closed wx_channel Batch v2

**Files:**
- Modify: D:\Agent\projects\wechat-channel-comment-poc\internal\poc\ltaoo_batch.go
- Modify: D:\Agent\projects\wechat-channel-comment-poc\internal\poc\ltaoo_batch_test.go

- [ ] **Step 1: Write failing v2 finalization tests.**

Extend the fixture server with one direct media response. After RunLtaooBatch and FinalizeLtaooBatch, assert schema v2, manifest.Files contains cleanup-receipt.json, contents.jsonl, comments.jsonl, issues.jsonl, media-assets.jsonl, and media/<digest>.png, and media-assets.jsonl has no URL/key sentinel. Add cases where an invalid asset keeps its comment and collection state intact, and v1-style content.media_type or a missing v2 assets manifest fails finalization.

- [ ] **Step 2: Verify v2 tests fail.**

Run: go test ./internal/poc -run 'Test(RunAndFinalizeLtaooBatch|Finalize.*Media)' -count=1

Expected: FAIL. Production is v1 and has no local asset artifact.

- [ ] **Step 3: Implement v2 production.**

Set the producer schema to wechat-channels-batch/2. Retain candidates only in memory alongside comments, resolve them after each work collection, and write media-assets.jsonl before collection-result.json. Failed candidates append only the closed media_unavailable issue and never alter comment counts, pagination markers, target status, or cleanup semantics.

Finalize by enumerating fixed v2 JSON artifacts and every direct regular non-reparse file beneath media/. Include SHA-256, bytes, and lines: 0 for binary files in manifest.Files. Require every media file to have exactly one asset record. Remove media/.tmp before manifest creation and refuse output if it remains. Preserve targets: [] on failed batches and current cleanup receipt validation.

- [ ] **Step 4: Run wx_channel package validation.**

Run: go test ./internal/poc -count=1

Expected: PASS.

Run: go test ./cmd/... ./internal/poc -count=1

Expected: PASS.

- [ ] **Step 5: Commit v2 production.**

Run:

~~~
git add internal/poc/ltaoo_batch.go internal/poc/ltaoo_batch_test.go
git commit -m "feat: publish verified WeChat media assets in batch v2"
~~~

### Task 4: Verify Closed Batch v2 in TrendRadar

**Files:**
- Modify: D:\Agent\services\trendradar-monitor\trendradar\social\wechat_channels_batch.py
- Modify: D:\Agent\services\trendradar-monitor\tests\test_social_wechat_channels_batch.py
- Add: D:\Agent\services\trendradar-monitor\tests\fixtures\wechat_channels_batch_v2

- [ ] **Step 1: Write failing verifier cases.**

Create a complete v2 fixture with one comment, one 1x1 PNG, one content.media_ref, and one matching asset. Assert verification returns the asset and v1 fixtures return zero assets. Reject: unknown root file; missing/extra manifest record; traversal; symlink/reparse; file/hash/size mismatch; unsupported MIME/type; duplicate source key/path; foreign or missing comment ref; duplicate comment media_ref; v1 field in v2; v2 field/artifact in v1; URL/query, AES, cookie, or absolute-path markers anywhere.

- [ ] **Step 2: Verify failure.**

Run: pytest tests/test_social_wechat_channels_batch.py -q -k 'v2 or media_asset or media_assets'

Expected: FAIL. Verification accepts only v1 layout.

- [ ] **Step 3: Implement a schema switch with no fallback.**

Use exactly:

~~~
if schema_version == "wechat-channels-batch/1":
    return _verify_v1(root, manifest, expected_run_id)
if schema_version == "wechat-channels-batch/2":
    return _verify_v2(root, manifest, expected_run_id)
raise ValueError
~~~

_verify_v2 reuses cleanup/target/count checks but permits only v2 comment keys text, media_types, and media_ref. It opens each asset with a bounded no-follow read, checks declared size/digest and real-root containment, and returns immutable records containing only comment_ref, media_type, source_key, path, digest, byte size, and MIME. It never returns raw manifest text, URLs, or keys.

- [ ] **Step 4: Run all batch tests.**

Run: pytest tests/test_social_wechat_channels_batch.py -q

Expected: PASS.

- [ ] **Step 5: Commit closed verification.**

Run:

~~~
git -C D:\Agent\services\trendradar-monitor add trendradar/social/wechat_channels_batch.py tests/test_social_wechat_channels_batch.py tests/fixtures/wechat_channels_batch_v2
git -C D:\Agent\services\trendradar-monitor commit -m "feat: verify WeChat media batch v2"
~~~

### Task 5: Normalize v2 Comments and Carry Local Assets

**Files:**
- Modify: D:\Agent\services\trendradar-monitor\trendradar\social\media_assets.py
- Modify: D:\Agent\services\trendradar-monitor\trendradar\social\importer.py
- Modify: D:\Agent\services\trendradar-monitor\trendradar\social\wechat_channels_importer.py
- Modify: D:\Agent\services\trendradar-monitor\trendradar\social\platforms\wechat_channels.py
- Modify: D:\Agent\services\trendradar-monitor\tests\test_social_wechat_channels_importer.py
- Modify: D:\Agent\services\trendradar-monitor\tests\test_social_wechat_channels_platform.py

- [ ] **Step 1: Write failing importer tests.**

Assert media_types=[text, image, sticker] becomes one SocialComment with TEXT, IMAGE, STICKER, and two matching verified assets associate to the final comment_key. Verify v1 remains single-type. Reject an asset whose media_ref is absent from normalized comments.

- [ ] **Step 2: Verify failure.**

Run: pytest tests/test_social_wechat_channels_importer.py tests/test_social_wechat_channels_platform.py -q

Expected: FAIL. The importer reads only content.media_type and NormalizedCommentBatch has no local asset field.

- [ ] **Step 3: Implement URL-free handoff.**

Add immutable VerifiedLocalMediaAsset to media_assets.py with platform, comment_key, media_type, source_key, hidden Path, SHA-256, byte size, and MIME. Validate supported media type, opaque source key, lowercase digest, positive size, approved MIME, and Path; never include URL.

Add local_media_assets with default empty tuple to NormalizedCommentBatch. For v2, normalize closed media_types via normalize_media_types, use media_ref only for the temporary association map, and return local assets with normalized SocialComment.comment_key. v1 returns its default empty tuple.

- [ ] **Step 4: Run importer/platform tests.**

Run: pytest tests/test_social_wechat_channels_importer.py tests/test_social_wechat_channels_platform.py -q

Expected: PASS.

- [ ] **Step 5: Commit normalized imports.**

Run:

~~~
git -C D:\Agent\services\trendradar-monitor add trendradar/social/media_assets.py trendradar/social/importer.py trendradar/social/wechat_channels_importer.py trendradar/social/platforms/wechat_channels.py tests/test_social_wechat_channels_importer.py tests/test_social_wechat_channels_platform.py
git -C D:\Agent\services\trendradar-monitor commit -m "feat: import verified WeChat comment media assets"
~~~

### Task 6: Ingest Local Assets through Existing TrendRadar Storage

**Files:**
- Create: D:\Agent\services\trendradar-monitor\trendradar\social\local_media_import.py
- Modify: D:\Agent\services\trendradar-monitor\trendradar\social\media_repository.py
- Modify: D:\Agent\services\trendradar-monitor\trendradar\social\pipeline.py
- Modify: D:\Agent\services\trendradar-monitor\trendradar\commands\social_comments.py
- Create: D:\Agent\services\trendradar-monitor\tests\test_social_local_media_import.py
- Modify: D:\Agent\services\trendradar-monitor\tests\test_media_fetch.py
- Modify: D:\Agent\services\trendradar-monitor\tests\test_social_pipeline.py

- [ ] **Step 1: Write failing local-ingestion tests.**

Create one SocialStore comment, one verified PNG outside the media store, and one VerifiedLocalMediaAsset. Assert ingestion creates/updates social_comment_media_assets, copies it into MediaAssetStore, completes the attempt with expected hash/MIME/size, and returns downloaded=1 with the fixture byte count. Reject changed source, reparse/symlink, wrong hash/size/MIME, missing comment, duplicate ingest, and source outside batch root. Comments must remain stored and classification continue after per-asset failure. Assert pipeline order: upsert comments, local ingest, media extractor.

- [ ] **Step 2: Verify failure.**

Run: pytest tests/test_social_local_media_import.py tests/test_media_fetch.py tests/test_social_pipeline.py -q

Expected: FAIL. No local ingestion service or pipeline hook exists.

- [ ] **Step 3: Implement the no-URL local ingestion path.**

The service creates a normal MediaAssetCandidate with url_candidates=(), obtains the comment primary key, upserts the candidate, claims/starts an attempt, bounded no-follow copies the verified source into MediaAssetStore.temp_path, re-hashes/re-sniffs it, calls put_verified, then completes the existing repository attempt. It compares opened-file identity, size, digest, and MIME to the verified value. It never invokes the URL downloader.

Add optional local_media_ingester to SocialPipeline. Invoke immediately after comment upsert, merge its MediaFetchSummary with ordinary media-fetch counters, and keep per-asset failure nonfatal. The _build_media_services path in trendradar/commands/social_comments.py constructs LocalMediaImportService and supplies its ingest method; test doubles use None.

- [ ] **Step 4: Run media regressions.**

Run: pytest tests/test_social_local_media_import.py tests/test_media_fetch.py tests/test_social_pipeline.py -q

Expected: PASS.

Run: pytest tests/test_social_query.py tests/test_social_storage.py -q

Expected: PASS.

- [ ] **Step 5: Commit ingestion.**

Run:

~~~
git -C D:\Agent\services\trendradar-monitor add trendradar/social/local_media_import.py trendradar/social/media_repository.py trendradar/social/pipeline.py trendradar/commands/social_comments.py tests/test_social_local_media_import.py tests/test_media_fetch.py tests/test_social_pipeline.py
git -C D:\Agent\services\trendradar-monitor commit -m "feat: ingest verified local comment media"
~~~

### Task 7: Integrate, Test, and Accept Under Controlled Runtime

**Files:**
- Modify: D:\Agent\projects\wechat-channel-comment-poc\docs\superpowers\specs\2026-08-27-wechat-comment-media-assets-design.md with verified non-secret host/cipher findings only

- [ ] **Step 1: Add an end-to-end fixture test.**

Build a finalized v2 fixture in wx_channel producer layout, parse it with WeChatChannelsPlatformPlugin, run a temporary SocialPipeline, and assert the comment retains STICKER/IMAGE, the object digest exists, and a vision extraction task can read it. Search generated artifacts/log captures for a sentinel URL query and AES key; neither may occur.

- [ ] **Step 2: Run the integration suite.**

Run: pytest tests/test_social_wechat_channels_batch.py tests/test_social_wechat_channels_importer.py tests/test_social_wechat_channels_platform.py tests/test_social_local_media_import.py tests/test_social_pipeline.py -q

Expected: PASS after Tasks 1-6. Fix any failure with a narrow contract regression test before proceeding.

- [ ] **Step 3: Run full automated validation.**

Run: go test ./internal/poc ./cmd/... -count=1

Expected: PASS.

Run: pytest tests/test_social_wechat_channels_batch.py tests/test_social_wechat_channels_importer.py tests/test_social_wechat_channels_platform.py tests/test_social_local_media_import.py tests/test_media_fetch.py tests/test_social_pipeline.py -q

Expected: PASS.

Run: ruff check trendradar tests

Expected: no diagnostics.

- [ ] **Step 4: Perform real acceptance.**

With logged-in PC WeChat and a known sticker-comment work, start the authorized runtime, confirm the Windows root-CA warning manually if shown, and use the existing entry/bridge sequence. Verify comment collection succeeds, an asset/hash exists, TrendRadar records asset plus vision outcome, no URL/key occurs in artifacts/logs, and CA absence, router restoration, process stop, port release, and secret deletion are all true. Run image acceptance only after a live response contains nonempty imageInfos.

- [ ] **Step 5: Commit documented evidence and prepare linked PRs.**

Run:

~~~
git -C D:\Agent\projects\wechat-channel-comment-poc add docs/superpowers/specs/2026-08-27-wechat-comment-media-assets-design.md
git -C D:\Agent\projects\wechat-channel-comment-poc commit -m "docs: record verified WeChat media runtime findings"
git -C D:\Agent\services\trendradar-monitor status --short
git -C D:\Agent\projects\wechat-channel-comment-poc status --short
~~~

Create the wx_channel PR first, then the TrendRadar PR referencing its required v2 runtime build. Do not delete user-owned worktrees/branches or reset either main worktree.

## Review Checklist

- v1 keeps its exact fields/files and verifies; v2 data is not accepted under v1.
- v2 has a strict artifact allowlist; every binary file is size- and hash-verified before import.
- URLs, signed parameters, AES keys, session headers/cookies, absolute paths, ciphertext, and decrypted temp files never persist.
- Media failure remains per asset and nonfatal. Comment pagination, markers, partial state, idempotent keys, and cleanup semantics are unchanged.
- Existing TrendRadar retention, vision extraction, media dashboard state, and cleanup own stored assets. The WeChat adapter creates no second store or downloader.
