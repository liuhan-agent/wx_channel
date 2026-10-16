//go:build windows

package main

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrendRadarRuntimeRejectsIncompleteOrMixedRouterArguments(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	base := []string{
		"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1"),
		"-RequestPath", "missing-request", "-GrantPath", "missing-grant", "-RunRoot", "missing-root",
		"-RuntimeJournalPath", "missing-journal", "-LtaooExePath", "missing-ltaoo", "-BatchExePath", "missing-batch",
	}
	cases := map[string][]string{
		"incomplete generic":       {"-RouterKind", "mihomo"},
		"incomplete legacy":        {"-ClashExePath", "clash.exe"},
		"navigation mode conflict": {"-AutoOpenFirstShareUrl", "-AutoRefreshWechatPage"},
		"mixed groups": {
			"-RouterKind", "mihomo", "-RouterExePath", "mihomo.exe", "-RouterConfigPath", "config.yaml",
			"-RouterCapabilityFingerprint", strings.Repeat("a", 64), "-ClashExePath", "clash.exe", "-ClashConfigPath", "clash.yaml",
		},
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(probePowerShell(t), append(base, arguments...)...)
			output, runErr := command.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 64 {
				t.Fatalf("exit=%v output=%s", runErr, output)
			}
		})
	}
}

func TestTrendRadarRuntimeAcceptsClosedLegacyAndGenericArgumentGroups(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	base := []string{
		"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1"),
		"-RequestPath", "missing-request", "-GrantPath", "missing-grant", "-RunRoot", "missing-root",
		"-RuntimeJournalPath", "missing-journal", "-LtaooExePath", "missing-ltaoo", "-BatchExePath", "missing-batch",
	}
	cases := map[string][]string{
		"legacy": {"-ClashExePath", "clash.exe", "-ClashConfigPath", "clash.yaml"},
		"generic": {
			"-RouterKind", "mihomo", "-RouterExePath", "mihomo.exe", "-RouterConfigPath", "config.yaml",
			"-RouterCapabilityFingerprint", strings.Repeat("a", 64),
		},
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(probePowerShell(t), append(base, arguments...)...)
			output, runErr := command.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 4 {
				t.Fatalf("exit=%v output=%s", runErr, output)
			}
		})
	}
}

func TestTrendRadarRuntimeImportsKeepRuntimeCommandsVisible(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	runtimeModule := filepath.Join(root, "scripts", "LtaooRuntime.psm1")
	routerModule := filepath.Join(root, "scripts", "LtaooRouter.psm1")
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	command := strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"Import-Module " + quote(runtimeModule) + " -Force",
		"Import-Module " + quote(routerModule) + " -Force",
		"Get-Command Assert-LtaooNoReparsePoint -ErrorAction Stop | Out-Null",
		"Get-Command New-LtaooRouterBackend -ErrorAction Stop | Out-Null",
	}, "; ")

	output, runErr := exec.Command(
		probePowerShell(t),
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		command,
	).CombinedOutput()
	if runErr != nil {
		t.Fatalf("runtime commands disappeared after router import: %v\n%s", runErr, output)
	}
}

func TestTrendRadarRuntimeTreatsAlreadyRestoredRouterAsClean(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entry := readRuntimeScript(t, filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1"))
	if !strings.Contains(entry, "if ($action -eq 'already_restored')") || !strings.Contains(entry, "return $true") {
		t.Fatal("already-restored router config must not require another reload")
	}
	if !strings.Contains(strings.ToLower(entry), "test-ltaooprocessidentityorabsent") {
		t.Fatal("cleanup must treat a PID that vanished during identity validation as already stopped")
	}
	if strings.Contains(entry, "if (-not [IO.File]::Exists($configPath) -or -not [IO.File]::Exists($backupPath)) { return $false }") {
		t.Fatal("already-restored router config must not require the deleted backup")
	}
	if !strings.Contains(entry, "$preserveRecoveryArtifacts") ||
		!strings.Contains(entry, "-not $preserveRecoveryArtifacts") {
		t.Fatal("failed journal recovery must preserve prior run artifacts")
	}
}

func TestLtaooRuntimeReadsUTF8JSONWithoutBOM(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(
		requestPath,
		[]byte(`{"schema_version":1,"keyword":"启东音乐节"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	runtimeModule := filepath.Join(root, "scripts", "LtaooRuntime.psm1")
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	command := strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"Import-Module " + quote(runtimeModule) + " -Force",
		"$value = Read-LtaooStrictJson -LiteralPath " + quote(requestPath) + " -AllowedProperties @('schema_version','keyword')",
		"if ($value.keyword -cne '启东音乐节') { throw 'keyword_mismatch' }",
	}, "; ")

	output, runErr := exec.Command(
		probePowerShell(t),
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		command,
	).CombinedOutput()
	if runErr != nil {
		t.Fatalf("UTF-8 JSON without BOM was rejected: %v\n%s", runErr, output)
	}
}

func TestTrendRadarRuntimeRecognizesCurrentBridgeStatus(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1")
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	status := `{"code":0,"data":{"connected":true,"clients":1,"ready_clients":1,"search_ready_clients":1,"comment_ready_clients":1}}`
	command := strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"$script = Get-Content -LiteralPath " + quote(entry) + " -Raw",
		"$start = $script.IndexOf('function Test-LtaooChannelsBridgeAvailable')",
		"$end = $script.IndexOf('function Test-LtaooChannelsBridgeAvailable {')",
		"if ($start -lt 0 -or $end -le $start) { throw 'bridge_function_missing' }",
		"Invoke-Expression $script.Substring($start, $end - $start)",
		"$body = " + quote(status) + " | ConvertFrom-Json",
		"$available = $null -ne $body -and [int]$body.code -eq 0 -and $null -ne $body.data -and [bool]$body.data.search_ready_clients",
		"if (-not $available) { throw 'fixture_not_search_ready' }",
		"if (Test-LtaooChannelsBridgeAvailableFromBody -Body $body) { 'ready' } else { 'unavailable' }",
		"$connected = " + quote(`{"code":0,"data":{"connected":true,"clients":1,"search_ready_clients":0}}`) + " | ConvertFrom-Json",
		"if (Test-LtaooChannelsBridgeAvailableFromBody -Body $connected) { throw 'connected_without_search_must_not_be_ready' }",
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
	}, "; ")
	output, runErr := exec.Command(probePowerShell(t), "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
	if runErr != nil {
		t.Fatalf("bridge status probe failed: %v\n%s", runErr, output)
	}
	if strings.TrimSpace(string(output)) != "ready" {
		t.Fatalf("current status was not recognized as ready: %s", output)
	}
}

func TestTrendRadarRuntimeDoesNotGateCollectionOnLegacyChannelsStatus(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entry := strings.ToLower(readRuntimeScript(t, filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1")))
	if strings.Contains(entry, "wait-ltaoochannelsbridge -apibase $apibase") {
		t.Fatal("collection must not be blocked by the legacy /api/channels/status gate")
	}
	if strings.Contains(entry, "throw 'bridge_unavailable'") {
		t.Fatal("legacy channels status must not produce a pre-collection bridge_unavailable failure")
	}
	collection := strings.Index(entry, "$batchprocess = start-process")
	if collection < 0 {
		t.Fatal("batch collection process is missing")
	}
}

func TestTrendRadarRuntimeWritesSanitizedBridgeFailureDiagnostic(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	entry := filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1")
	module := filepath.Join(root, "scripts", "LtaooRuntime.psm1")
	diagnosticPath := filepath.Join(t.TempDir(), "bridge-status-diagnostic.json")
	command := strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"Set-StrictMode -Version Latest",
		"Import-Module " + quote(module) + " -Force",
		"$script = Get-Content -LiteralPath " + quote(entry) + " -Raw",
		"$start = $script.IndexOf('function Test-LtaooChannelsBridgeAvailableFromBody')",
		"$end = $script.IndexOf('function Stop-JournalLtaooProcess')",
		"if ($start -lt 0 -or $end -le $start) { throw 'bridge_functions_missing' }",
		"Invoke-Expression $script.Substring($start, $end - $start)",
		"$script:payload = " + quote(`{"code":0,"data":{"available":true,"search_ready_clients":0,"private_value":"DO_NOT_RECORD"}}`),
		"function Invoke-WebRequest { [pscustomobject]@{ StatusCode = 200; Content = $script:payload } }",
		"$sample = $null",
		"if (Test-LtaooChannelsBridgeAvailable -ApiBase 'http://127.0.0.1:2022' -Diagnostic ([ref]$sample)) { throw 'zero_count_accepted' }",
		"if ($sample.outcome -ne 'status_not_ready' -or $sample.http_status -ne 200 -or -not $sample.code_ok -or -not $sample.search_ready_clients_present -or $sample.search_ready_clients -ne 0 -or $sample.available -ne $true) { throw 'zero_count_diagnostic_missing' }",
		"$script:payload = " + quote(`{"code":0,"data":{"available":"DO_NOT_RECORD","search_ready_clients":"DO_NOT_RECORD"}}`),
		"if ((Test-LtaooChannelsBridgeAvailable -ApiBase 'http://127.0.0.1:2022' -Diagnostic ([ref]$sample)) -or $sample.available_type -ne 'other' -or $sample.search_ready_clients_type -ne 'other' -or $null -ne $sample.available -or $null -ne $sample.search_ready_clients) { throw 'invalid_values_not_redacted' }",
		"$script:payload = 'INVALID_JSON_DO_NOT_RECORD'",
		"if ((Test-LtaooChannelsBridgeAvailable -ApiBase 'http://127.0.0.1:2022' -Diagnostic ([ref]$sample)) -or $sample.outcome -ne 'invalid_json') { throw 'invalid_json_not_distinguished' }",
		"function Invoke-WebRequest { throw 'SECRET_TRANSPORT_DO_NOT_RECORD' }",
		"if ((Test-LtaooChannelsBridgeAvailable -ApiBase 'http://127.0.0.1:2022' -Diagnostic ([ref]$sample)) -or $sample.outcome -ne 'request_error') { throw 'transport_not_distinguished' }",
		"function Invoke-WebRequest { [pscustomobject]@{ StatusCode = 503; Content = 'HTTP_BODY_DO_NOT_RECORD' } }",
		"if ((Test-LtaooChannelsBridgeAvailable -ApiBase 'http://127.0.0.1:2022' -Diagnostic ([ref]$sample)) -or $sample.outcome -ne 'http_error' -or $sample.http_status -ne 503) { throw 'http_error_not_distinguished' }",
		"$script:payload = " + quote(`{"code":0,"data":{"available":true,"search_ready_clients":0,"private_value":"DO_NOT_RECORD"}}`),
		"function Invoke-WebRequest { [pscustomobject]@{ StatusCode = 200; Content = $script:payload } }",
		"function Start-Sleep { param($Seconds) }",
		"if (Wait-LtaooChannelsBridge -ApiBase 'http://127.0.0.1:2022' -LtaooProcess ([Diagnostics.Process]::GetCurrentProcess()) -DiagnosticPath " + quote(diagnosticPath) + ") { throw 'wait_accepted_zero_count' }",
		"$saved = Get-Content -LiteralPath " + quote(diagnosticPath) + " -Raw",
		"$summary = $saved | ConvertFrom-Json",
		"if ($summary.attempts -ne 60 -or $summary.counts.status_not_ready -ne 60 -or $summary.last.outcome -ne 'status_not_ready' -or $summary.last.search_ready_clients -ne 0) { throw 'summary_incomplete' }",
		"if ($saved -match 'DO_NOT_RECORD|SECRET_TRANSPORT|HTTP_BODY') { throw 'diagnostic_leaked_private_data' }",
		"function Write-LtaooJsonAtomic { throw 'diagnostic_write_failed' }",
		"if (Wait-LtaooChannelsBridge -ApiBase 'http://127.0.0.1:2022' -LtaooProcess ([Diagnostics.Process]::GetCurrentProcess()) -DiagnosticPath " + quote(diagnosticPath) + ") { throw 'write_error_changed_bridge_result' }",
		"'diagnostic_ok'",
	}, "; ")
	output, runErr := exec.Command(probePowerShell(t), "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
	if runErr != nil || strings.TrimSpace(string(output)) != "diagnostic_ok" {
		t.Fatalf("bridge diagnostic probe failed: %v\n%s", runErr, output)
	}
}

func TestTrendRadarRuntimeWaitsForProxyBeforePageNavigation(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entry := strings.ToLower(readRuntimeScript(t, filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1")))
	readyFunction := strings.Index(entry, "function wait-ltaooruntimeready")
	readyCall := strings.Index(entry, "wait-ltaooruntimeready -apibase $apibase -ltaooprocess $ltaooprocess -proxyport $proxyport")
	pageNavigation := strings.Index(entry, "if ($autoopenfirstshareurl)")
	pageRefresh := strings.Index(entry, "elseif ($autorefreshwechatpage)")
	if readyFunction < 0 || readyCall < 0 || pageNavigation < 0 || pageRefresh < 0 {
		t.Fatal("runtime must contain combined API/proxy and page-navigation readiness stages")
	}
	if readyCall >= pageNavigation || readyCall >= pageRefresh {
		t.Fatal("API and proxy readiness must complete before page navigation")
	}
	if !strings.Contains(entry, "data.proxy") || !strings.Contains(entry, "properties['listening']") {
		t.Fatal("readiness must inspect ltaoo's proxy listener status")
	}
}

func TestTrendRadarRuntimeWaitsForProxyStatus(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1")
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	command := strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"$script = Get-Content -LiteralPath " + quote(entry) + " -Raw",
		"$start = $script.IndexOf('function Test-LtaooProxyListenerPresent')",
		"$end = $script.IndexOf('function Test-LtaooChannelsBridgeAvailableFromBody')",
		"if ($start -lt 0 -or $end -le $start) { throw 'runtime_readiness_functions_missing' }",
		"Invoke-Expression $script.Substring($start, $end - $start)",
		"function Start-Sleep { param($Milliseconds) }",
		"$global:proxyProbe = 0",
		"function Invoke-WebRequest { [CmdletBinding()] param([string]$Uri, [switch]$UseBasicParsing, [int]$TimeoutSec); $global:proxyProbe++; if ($global:proxyProbe -lt 3) { [pscustomobject]@{ StatusCode = 200; Content = '{\"code\":0,\"data\":{\"api\":{\"listening\":true},\"proxy\":{\"listening\":false}}}' } } else { [pscustomobject]@{ StatusCode = 200; Content = '{\"code\":0,\"data\":{\"api\":{\"listening\":true},\"proxy\":{\"listening\":true}}}' } } }",
		"$result = Wait-LtaooRuntimeReady -ApiBase 'http://127.0.0.1:2022' -LtaooProcess ([Diagnostics.Process]::GetCurrentProcess()) -ProxyPort 2023",
		"if (-not $result -or $global:proxyProbe -ne 3) { throw 'proxy_status_not_waited' }",
		"function Get-NetTCPConnection { [CmdletBinding()] param([string]$State, [int]$LocalPort); [pscustomobject]@{ LocalPort = $LocalPort; OwningProcess = [int]$PID; State = 'Listen' } }",
		"function Invoke-WebRequest { [CmdletBinding()] param([string]$Uri, [switch]$UseBasicParsing, [int]$TimeoutSec); [pscustomobject]@{ StatusCode = 200; Content = '{\"code\":0,\"data\":{\"version\":\"legacy\"}}' } }",
		"$result = Wait-LtaooRuntimeReady -ApiBase 'http://127.0.0.1:2022' -LtaooProcess ([Diagnostics.Process]::GetCurrentProcess()) -ProxyPort 2023",
		"if (-not $result) { throw 'legacy_listener_fallback_rejected' }",
		"'proxy_status_ok'",
	}, "; ")
	output, runErr := exec.Command(probePowerShell(t), "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
	if runErr != nil || strings.TrimSpace(string(output)) != "proxy_status_ok" {
		t.Fatalf("proxy status readiness probe failed: %v\n%s", runErr, output)
	}
}

func TestTrendRadarRuntimeValidatesLtaooContractBeforeStarting(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entry := strings.ToLower(readRuntimeScript(t, filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1")))
	contract := strings.Index(entry, "function test-ltaooexecutablecontract")
	call := strings.Index(entry, "test-ltaooexecutablecontract -executablepath $resolvedltaoo")
	start := strings.Index(entry, "$ltaooprocess = start-process")
	if contract < 0 || call < 0 || start < 0 {
		t.Fatal("runtime must validate the Ltaoo -c/--config contract")
	}
	if call >= start {
		t.Fatal("Ltaoo contract validation must run before the process starts")
	}
	for _, required := range []string{
		"wx_video_download\\s+\\[flags\\]",
		"^\\s+server\\s+",
		"-c,\\s*--config",
		"--hostname\\s+string",
		"--port\\s+int",
		"default\\s+(\"?2023\"?)",
		"key:channels:contact_list",
		"searchtype === 3 ? 19 : 13",
		"lastbuff",
		"profileurlforusername",
		"addprofileurls",
		"ltaoo_profile_url_contract_invalid",
	} {
		if !strings.Contains(entry, required) {
			t.Fatalf("Ltaoo contract must require %q", required)
		}
	}
}

func TestTrendRadarRuntimeScriptSafety(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entry := readRuntimeScript(t, filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1"))
	refresh := readRuntimeScript(t, filepath.Join(root, "scripts", "Invoke-WeChatPageRefresh.ps1"))
	open := readRuntimeScript(t, filepath.Join(root, "scripts", "Invoke-WeChatKnownShareOpen.ps1"))
	module := readRuntimeScript(t, filepath.Join(root, "scripts", "LtaooRuntime.psm1"))
	routerModule := readRuntimeScript(t, filepath.Join(root, "scripts", "LtaooRouter.psm1"))
	combined := strings.ToLower(entry + "\n" + open + "\n" + module + "\n" + routerModule)

	for _, required := range []string{
		"-literalpath", "convertfrom-json", "allowedproperties", "storelocation]::currentuser", "new('root'", "certutil.exe -user",
		"start-process", "-windowstyle hidden", "try", "finally", "grant", "request_sha256",
		"runtime_paths_sha256", "ltaoo_executable_sha256", "threading.mutex", "runtime-journal.json",
		"batch_executable_sha256", "test-ltaooprocessidentity", "convertfrom-ltaooutf8bytes",
		"wx_channel_ltaoo_batch", "cleanup-receipt", "process-name,wx_video_download.exe,direct", "wait-ltaooprocessstopped",
		"process-name,wechatappex.exe", "process-name,weixin.exe", "process-name,wechat.exe", "external-controller", "/configs?force=true",
		"-datadirectory (split-path -parent $resolvedrouterconfig)",
		"safefailurecode", "runtime_failed",
		"autorefreshwechatpage", "invoke-wechatpagerefresh.ps1",
		"autoopenfirstshareurl", "invoke-wechatknownshareopen.ps1", "known_share_open_failed", "wechat_channel_home_open_failed",
		"collect --request", "--api-base",
	} {
		if !strings.Contains(combined, required) {
			t.Errorf("runtime scripts missing %q", required)
		}
	}
	for _, required := range []string{
		"invoke-wechatknownshareopen.ps1", "entryonly", "refresh", "wechat_page_refresh_sent", "wechat_page_refresh_failed",
	} {
		if !strings.Contains(strings.ToLower(refresh), required) {
			t.Errorf("page refresh helper missing %q", required)
		}
	}
	for _, required := range []string{
		"showwindow", "setforegroundwindow", "activatewindow", "attachthreadinput", "setprocessdpiaware", "mouse_event", "wechat_entry_template", "uiautomationclient",
		"omniboxviewviews", "setvalue", "postmessage", "keybd_event", "get-addressbarmatches", "get-visiblewechatconfirmationbuttons", "select-wechatmainwindow", "wechat_known_share_navigation_verified", "wechat_channel_entry_template_mismatch", "wechat_channel_menu_not_ready", "wechatappex", "wechatembeddedwebview", "wechat_webview_not_ready", "wechat_webview_ambiguous",
	} {
		if !strings.Contains(strings.ToLower(open), required) {
			t.Errorf("known-share helper missing %q", required)
		}
	}
	for _, forbidden := range []string{"sendkeys", "clipboard", "invoke-expression", "cmd /c"} {
		if strings.Contains(strings.ToLower(open), forbidden) {
			t.Errorf("known-share helper contains forbidden %q", forbidden)
		}
	}
	for _, forbidden := range []string{"read-host", "install $runid", "invoke-expression", "cmd /c", "localmachine\\root"} {
		if strings.Contains(combined, forbidden) {
			t.Errorf("runtime scripts contain forbidden %q", forbidden)
		}
	}
	for _, forbidden := range []string{"sendkeys", "clipboard", "weixin.qq.com/sph", "invoke-expression", "cmd /c"} {
		if strings.Contains(strings.ToLower(refresh), forbidden) {
			t.Errorf("page refresh helper contains forbidden %q", forbidden)
		}
	}
	for _, required := range []string{
		"new-ltaoorouterbackend", "router_kind_unsupported", "trendradar.mihomobackend",
		"add-ltaoorouterblock", "remove-ltaoorouterblock", "test-ltaoorouterconfig",
	} {
		if !strings.Contains(strings.ToLower(routerModule), required) {
			t.Errorf("router module missing %q", required)
		}
	}
	for _, required := range []string{
		"$routerkind", "$routerexepath", "$routerconfigpath", "$routercapabilityfingerprint",
		"router_restored", "restore-journalrouterconfig",
		"router_baseline_sha256", "router_temporary_sha256", "router_backup_path",
	} {
		if !strings.Contains(strings.ToLower(entry), required) {
			t.Errorf("runtime entry missing generic contract %q", required)
		}
	}
	if !strings.Contains(combined, "modify_proxy_router") {
		t.Error("runtime modules missing generic router grant action")
	}
}

func TestTrendRadarRuntimeUsesKeywordEntryWhenContentURLsAreEmpty(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entry := strings.ToLower(readRuntimeScript(t, filepath.Join(root, "scripts", "Invoke-LtaooTrendRadarBatch.ps1")))
	for _, required := range []string{
		"$contenturls.count -eq 0",
		"& $openscript -entryonly",
		"wechat_channel_entry_sent",
		"wechat_channel_home_open_failed",
	} {
		if !strings.Contains(entry, required) {
			t.Errorf("keyword startup path missing %q", required)
		}
	}
	if strings.Contains(entry, "if ([string]::isnullorwhitespace($firstshareurl)) { throw 'known_share_url_missing' }\n        $opencode") {
		t.Fatal("keyword startup still requires a first share URL before choosing its entry path")
	}
	open := strings.ToLower(readRuntimeScript(t, filepath.Join(root, "scripts", "Invoke-WeChatKnownShareOpen.ps1")))
	for _, required := range []string{
		"if ($entryonly -and $clickedconfirmation)",
		"$browserhostready = if ($entryonly) {",
		"'wechat_channel_entry_sent'",
	} {
		if !strings.Contains(open, required) {
			t.Errorf("keyword entry flow missing %q", required)
		}
	}
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
}

func TestKnownShareEntryTemplateIsValidBase64(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"wechat-channel-entry-template.b64",
		"wechat-discover-entry-active-template.b64",
		"wechat-discover-entry-inactive-template.b64",
		"wechat-channel-menu-active-template.b64",
		"wechat-channel-menu-inactive-template.b64",
	} {
		raw, err := os.ReadFile(filepath.Join(root, "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatalf("%s is not valid base64: %v", name, err)
		}
		if len(decoded) < 8 || string(decoded[:8]) != "\x89PNG\r\n\x1a\n" {
			t.Fatalf("%s is not a PNG", name)
		}
	}
}

func readRuntimeScript(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
