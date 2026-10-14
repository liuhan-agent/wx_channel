# 视频号页面桥接状态格式兼容 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让视频号关键词任务识别旧版 `available` 桥接响应，同时保留新版搜索就绪计数的优先判定。

**Architecture:** 只调整 `Test-LtaooChannelsBridgeAvailableFromBody` 的字段分支。新版 `search_ready_clients` 只要存在就独占判定；完全缺失时才检查布尔型 `available`，其他现有等待和清理流程不动。

**Tech Stack:** Windows PowerShell、Go 1.24.3、TDM-GCC 10.3.0、`scripts` 包 Windows 回归测试。

---

## 范围与约束

- 在 `D:\Agent\projects\wechat-channel-comment-poc` 的本地 `main` 修改；检查分支、提交和脏工作区，保留其余用户改动。
- 源码：`scripts/Invoke-LtaooTrendRadarBatch.ps1` 的状态函数。
- 测试：`scripts/trendradar_runtime_script_test.go` 的状态探针测试。
- 已审阅设计：`docs/superpowers/specs/2026-09-23-legacy-bridge-status-compatibility-design.md`。
- 本地交付：`D:\Agent\services\trendradar-monitor\tools\wechat-channels\Invoke-LtaooTrendRadarBatch.ps1`（独立仓库的忽略目录）；只在源码与副本原始哈希相等、没有运行中的相关进程、且备份可用时，同步单个脚本。此处是本机交付，不将运行脚本提交到中台仓库。
- 不修改证书、Mihomo、ltaoo 可执行文件、入口脚本或评论采集流程；不启动微信采集。

## Task 1：旧格式回归测试

**Files:**
- Modify: `scripts/trendradar_runtime_script_test.go:158-187`
- Test: `scripts/trendradar_runtime_script_test.go`

- [ ] **Step 1: 扩展现有 PowerShell 状态函数探针**

保留当前新版成功断言，追加下列断言；旧版示例来源于保存的真实探针摘要，其 `available=true`、`search_ready_clients` 未出现：

```go
		"$legacy = " + quote(`{"code":0,"data":{"available":true}}`) + " | ConvertFrom-Json",
		"if (-not (Test-LtaooChannelsBridgeAvailableFromBody -Body $legacy)) { throw 'legacy_available_rejected' }",
		"$legacyUnavailable = " + quote(`{"code":0,"data":{"available":false}}`) + " | ConvertFrom-Json",
		"if (Test-LtaooChannelsBridgeAvailableFromBody -Body $legacyUnavailable) { throw 'legacy_unavailable_accepted' }",
		"$newZero = " + quote(`{"code":0,"data":{"available":true,"search_ready_clients":0}}`) + " | ConvertFrom-Json",
		"if (Test-LtaooChannelsBridgeAvailableFromBody -Body $newZero) { throw 'new_zero_overridden' }",
		"$newNull = " + quote(`{"code":0,"data":{"available":true,"search_ready_clients":null}}`) + " | ConvertFrom-Json",
		"if (Test-LtaooChannelsBridgeAvailableFromBody -Body $newNull) { throw 'new_null_overridden' }",
		"$missing = " + quote(`{"code":0,"data":{}}`) + " | ConvertFrom-Json",
		"if (Test-LtaooChannelsBridgeAvailableFromBody -Body $missing) { throw 'missing_fields_accepted' }",
		"$invalidLegacy = " + quote(`{"code":0,"data":{"available":"true"}}`) + " | ConvertFrom-Json",
		"if (Test-LtaooChannelsBridgeAvailableFromBody -Body $invalidLegacy) { throw 'non_boolean_legacy_accepted' }",
		"$badCode = " + quote(`{"code":1,"data":{"available":true}}`) + " | ConvertFrom-Json",
		"if (Test-LtaooChannelsBridgeAvailableFromBody -Body $badCode) { throw 'bad_code_accepted' }",
		"$noData = " + quote(`{"code":0}`) + " | ConvertFrom-Json",
		"if (Test-LtaooChannelsBridgeAvailableFromBody -Body $noData) { throw 'missing_data_accepted' }",
```

把现有 `$connected` 断言保留。追加字段校验代码到 `command` 片段时，仍只允许最后的成功分支输出单行 `ready`。

- [ ] **Step 2: 用批准工具链运行定向测试，确认旧格式拒绝**

沿用 `scripts/build-poc.ps1` 指定的 `.poc-tools\go1.24.3\go\bin\go.exe` 与 `.poc-tools\tdm-gcc-10.3.0-2\bin\gcc.exe`，确认版本分别是 `go1.24.3 windows/amd64` 与 `10.3.0`。设置 `GOROOT`、`GOTOOLCHAIN=local`、`CGO_ENABLED=1`、`CC`、`CXX` 及本次 shell 的 PATH，运行：

```powershell
& $approvedGo test ./scripts -run '^TestTrendRadarRuntimeRecognizesCurrentBridgeStatus$' -count=1
```

预期失败，消息为 `legacy_available_rejected`。如果在此之前已有其他失败，停止并核对原因。

## Task 2：最小修改状态解析并验证

**Files:**
- Modify: `scripts/Invoke-LtaooTrendRadarBatch.ps1:41-44`
- Test: `scripts/trendradar_runtime_script_test.go`

- [ ] **Step 1: 用属性存在性区分新旧格式**

将 `Test-LtaooChannelsBridgeAvailableFromBody` 函数体替换为：

```powershell
function Test-LtaooChannelsBridgeAvailableFromBody {
    param([Parameter(Mandatory = $true)][object]$Body)
    if ($null -eq $Body -or $null -eq $Body.PSObject.Properties['code'] -or
        [int]$Body.code -ne 0 -or $null -eq $Body.PSObject.Properties['data'] -or $null -eq $Body.data) {
        return $false
    }
    $data = $Body.data
    if ($null -ne $data.PSObject.Properties['search_ready_clients']) {
        return ($data.search_ready_clients -is [int] -or $data.search_ready_clients -is [long]) -and $data.search_ready_clients -gt 0
    }
    return $null -ne $data.PSObject.Properties['available'] -and $data.available -is [bool] -and $data.available
}
```

`[int]` 和 `[long]` 对应 Windows PowerShell/PowerShell 7 对整数 JSON 的两种解析结果；旧版字符串 `"true"` 不可当作布尔成功。

- [ ] **Step 2: 验证定向测试、完整脚本包与 PowerShell 语法**

在 Task 1 相同工具链下运行：

```powershell
& $approvedGo test ./scripts -run '^TestTrendRadarRuntimeRecognizesCurrentBridgeStatus$' -count=1
& $approvedGo test ./scripts -count=1
```

两者预期 `ok wx_channel/scripts`。再用 `[System.Management.Automation.Language.Parser]::ParseFile()` 解析目标脚本，预期零错误；执行 `git diff --check -- scripts/Invoke-LtaooTrendRadarBatch.ps1 scripts/trendradar_runtime_script_test.go`，预期退出码 0。不要把其他脏文件一起暂存、提交或清理。

## Task 3：安全同步本机运行副本

**Files:**
- Deploy: `D:\Agent\services\trendradar-monitor\tools\wechat-channels\Invoke-LtaooTrendRadarBatch.ps1`
- Preserve: 同目录原始脚本的带时间戳备份

- [ ] **Step 1: 交付前只读检查**

确认脚本源码改动前，交付副本哈希与已记录原始哈希
`368EEBB1473BEDEABAC283E493A0DA63A8CB878832F2183D2D1EE38319BAE50B`
一致；脚本以外的交付文件不在操作范围。检查运行日志对应的 `runtime-journal.json`
不存在、`wx_video_download.exe`/PowerShell 采集任务不存在、2022/2023 端口无监听。
若任一条件不满足，停止同步并报告。

- [ ] **Step 2: 备份并同步单个脚本**

使用同一 PowerShell 会话显式路径：

```powershell
$source = 'D:\Agent\projects\wechat-channel-comment-poc\scripts\Invoke-LtaooTrendRadarBatch.ps1'
$delivery = 'D:\Agent\services\trendradar-monitor\tools\wechat-channels\Invoke-LtaooTrendRadarBatch.ps1'
$backup = $delivery + '.backup-20260923-status-' + (Get-Date -Format 'HHmmss')
Copy-Item -LiteralPath $delivery -Destination $backup -ErrorAction Stop
Copy-Item -LiteralPath $source -Destination $delivery -ErrorAction Stop
```

操作前确认该备份路径不存在。操作后同时计算源码、交付副本哈希并确认一致，
备份仍等于原始哈希；检查源仓库与中台仓库 `git status --short` 没有意外新增
受跟踪改动。交付目录是本机忽略目录，不 commit、不 push。

- [ ] **Step 3: 报告严格的验证边界**

汇报红灯与绿灯结果、原始及交付哈希、备份文件、源仓库分支/提交及清理状态；
明确没有安装证书、切换代理、启动真实微信采集，故视频空白的成因仍未验证。
