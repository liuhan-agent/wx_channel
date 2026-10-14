# 微信视频号关键词入口 WebView 刷新 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 使关键词入口只有在定位唯一视频号 WebView 并成功发送 F5 后才报告入口成功。

**Architecture:** 沿用现有的微信协议启动、登录确认、WebView 查找与 F5 发送；移除绕开刷新路径的提前返回。关键词模式跳过仅供链接导航使用的模板/地址栏回退，保留分享链接模式的原行为与上层桥接轮询。

**Tech Stack:** PowerShell 5.1、Go 1.24.3 Windows/amd64、TDM-GCC 10.3.0、Go `scripts` 包回归测试。

---

## 工作区约束与文件映射

- 当前仓库 `D:\Agent\projects\wechat-channel-comment-poc` 直接在 `main` 修改；先记录 `git branch --show-current`、`git rev-parse --short HEAD`、`git status --short`。已有多个用户修改，禁止重置或暂存无关文件。
- 修改 `scripts/trendradar_runtime_script_test.go`：锁定关键词入口返回时机及不依赖链接地址栏。
- 修改 `scripts/Invoke-WeChatKnownShareOpen.ps1`：入口成功要经过现有 WebView 刷新路径。
- 设计依据：`docs/superpowers/specs/2026-09-23-keyword-entry-webview-refresh-design.md`。不修改证书、代理、分享链接和上层桥接轮询代码；不重建或运行采集器，除非实际进行真人联调。

## Task 1：先让关键词入口的回归测试准确表达顺序

**Files:**
- Modify: `scripts/trendradar_runtime_script_test.go:272-307`
- Test: `scripts/trendradar_runtime_script_test.go`

- [x] **Step 1: 将原先“提前返回”的断言改为“定位且刷新后返回”**

保留 `TestTrendRadarRuntimeUsesKeywordEntryWhenContentURLsAreEmpty` 前面的关键词启动断言和登录确认断言；用以下代码替换末尾 `entryOnlyReturn` / `webViewSearch` 检查：

```go
	entryOnlyReturn := strings.Index(open, "'wechat_channel_entry_sent'")
	webViewReady := strings.Index(open, "if ($null -eq $webview) { throw 'wechat_webview_not_ready' }")
	refreshMessage := strings.Index(open, "::postmessage($webview.handle, [trendradar.wechatchannelautomation]::wm_keydown")
	addressBar := strings.Index(open, "$matches = @(get-addressbarmatches -hosthandle $webviewroothandle)")
	if webViewReady < 0 || refreshMessage <= webViewReady || entryOnlyReturn <= refreshMessage || addressBar <= entryOnlyReturn {
		t.Fatal("keyword entry must find its WebView and post F5 before returning, without requiring the share address bar")
	}
	if !strings.Contains(open, "if (-not $entryonly -and -not $browserhostready) {") {
		t.Fatal("keyword entry must bypass the share-navigation template/address-bar fallback")
	}
```

- [x] **Step 2: 用批准的工具链运行单测，确认红灯**

在 PowerShell 中将 `.poc-tools\go1.24.3\go\bin` 与 `.poc-tools\tdm-gcc-10.3.0-2\bin` 加入本次进程 PATH，设置 `GOROOT`、`GOTOOLCHAIN=local`、`CGO_ENABLED=1`、`CC`、`CXX`，分别运行 `go version` 和 `gcc -dumpfullversion`，须得到 Go 1.24.3 和 GCC 10.3.0。运行 `go test ./scripts -run '^TestTrendRadarRuntimeUsesKeywordEntryWhenContentURLsAreEmpty$' -count=1`；预期因为当前提前返回而 FAIL。使用显式批准工具链的 `go.exe`，不要默认系统 Go。

## Task 2：恢复 WebView 刷新后的返回

**Files:**
- Modify: `scripts/Invoke-WeChatKnownShareOpen.ps1:548-566`
- Test: `scripts/trendradar_runtime_script_test.go`

- [x] **Step 1: 删除早退，并限定模板回退只在分享链接模式运行**

删去主窗口尺寸检查后这整个提前返回块：

```powershell
if ($EntryOnly) {
    'wechat_channel_entry_sent'
    return
}
```

将其后的回退判断替换为：

```powershell
if (-not $EntryOnly -and -not $browserHostReady) {
```

不要改下方 `if ($EntryOnly)` 中现有的 `PostMessage(WM_KEYDOWN, 0x74)`、`WM_KEYUP`、`-Refresh` 和普通入口的两个成功码；该块已经位于 WebView 唯一性判断之后。

- [x] **Step 2: 单测变绿，再运行相关脚本回归**

在 Task 1 的同一环境运行 `go test ./scripts -run '^TestTrendRadarRuntimeUsesKeywordEntryWhenContentURLsAreEmpty$' -count=1`，预期 PASS；运行 `go test ./scripts -count=1`，预期 PASS。用 `[System.Management.Automation.Language.Parser]::ParseFile()` 对目标 PowerShell 文件做语法分析，预期错误数为零。

- [x] **Step 3: 校验变更范围**

运行 `git diff --check`、`git diff -- scripts/Invoke-WeChatKnownShareOpen.ps1 scripts/trendradar_runtime_script_test.go`、`git status --short`。对照设计说明核查：启动及登录确认不变；关键词成功码出现在 WebView 定位和 F5 之后；地址栏检查只属于分享链接模式；没有改变证书、代理或上层桥接轮询。

- [x] **Step 4: 报告验证边界**

报告实际测试结果，并明确静态/单元测试不能证明真人微信页面已经桥接。若另行进行宿主机真人联调，记录批准工具链、构建产物、微信运行时、数据库和输出目录，结束时卸载临时 CA 并恢复代理；没有联调则明说未进行。不要将其他工作区改动加入本次提交；是否提交实现以用户后续明确要求为准。
