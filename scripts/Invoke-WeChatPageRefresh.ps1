[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$entryScript = Join-Path $PSScriptRoot 'Invoke-WeChatKnownShareOpen.ps1'
if (-not [IO.File]::Exists($entryScript)) { throw 'known_share_open_helper_missing' }
$result = @(& $entryScript -EntryOnly -Refresh)
if ($result.Count -ne 1 -or [string]$result[0] -cne 'wechat_page_refresh_sent') {
    throw 'wechat_page_refresh_failed'
}
$result[0]
