[CmdletBinding()]
param(
    [string]$ShareUrl = '',
    [switch]$EntryOnly,
    [switch]$Refresh
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not ('TrendRadar.WeChatChannelAutomation' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Text;
using System.Runtime.InteropServices;

namespace TrendRadar {
    public static class WeChatChannelAutomation {
        public const int SW_RESTORE = 9;
        public const uint WM_KEYDOWN = 0x0100;
        public const uint WM_KEYUP = 0x0101;
        public const uint VK_RETURN = 0x0D;
        public const uint VK_CONTROL = 0x11;
        public const uint VK_L = 0x4C;
        public const uint KEYEVENTF_KEYUP = 0x0002;
        public const uint MOUSEEVENTF_LEFTDOWN = 0x0002;
        public const uint MOUSEEVENTF_LEFTUP = 0x0004;

        public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);
        [StructLayout(LayoutKind.Sequential)]
        public struct RECT { public int Left; public int Top; public int Right; public int Bottom; }
        [StructLayout(LayoutKind.Sequential)]
        public struct POINT { public int X; public int Y; }

        [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc callback, IntPtr lParam);
        [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hWnd);
        [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr hWnd);
        [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint processId);
        [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern int GetWindowText(IntPtr hWnd, StringBuilder text, int count);
        [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT rect);
        [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
        [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int command);
        [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hWnd);
        [DllImport("user32.dll")] public static extern bool BringWindowToTop(IntPtr hWnd);
        [DllImport("user32.dll")] public static extern uint GetCurrentThreadId();
        [DllImport("user32.dll")] public static extern bool AttachThreadInput(uint idAttach, uint idAttachTo, bool attach);
        [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
        [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
        [DllImport("user32.dll")] public static extern bool GetCursorPos(out POINT point);
        [DllImport("user32.dll")] public static extern void mouse_event(uint flags, uint dx, uint dy, uint data, UIntPtr extra);
        [DllImport("user32.dll")] public static extern void keybd_event(byte key, byte scan, uint flags, UIntPtr extra);
        [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint message, IntPtr wParam, IntPtr lParam);

        public static bool ActivateWindow(IntPtr hWnd, int command) {
            IntPtr foreground = GetForegroundWindow();
            uint foregroundProcessId = 0;
            uint targetProcessId = 0;
            uint foregroundThread = foreground == IntPtr.Zero ? 0 : GetWindowThreadProcessId(foreground, out foregroundProcessId);
            uint targetThread = GetWindowThreadProcessId(hWnd, out targetProcessId);
            bool attached = foregroundThread != 0 && targetThread != 0 && foregroundThread != targetThread && AttachThreadInput(foregroundThread, targetThread, true);
            try {
                ShowWindow(hWnd, command);
                BringWindowToTop(hWnd);
                return SetForegroundWindow(hWnd);
            } finally {
                if (attached) AttachThreadInput(foregroundThread, targetThread, false);
            }
        }
    }
}
'@ -ErrorAction Stop
}

Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
[void][TrendRadar.WeChatChannelAutomation]::SetProcessDPIAware()

function Get-TopLevelWindowsByProcessName {
    param([Parameter(Mandatory = $true)][string]$ProcessName)
    $windows = [System.Collections.Generic.List[object]]::new()
    [void][TrendRadar.WeChatChannelAutomation]::EnumWindows({
        param($handle, $unused)
        if (-not [TrendRadar.WeChatChannelAutomation]::IsWindowVisible($handle)) { return $true }
        $titleBuilder = [Text.StringBuilder]::new(256)
        [void][TrendRadar.WeChatChannelAutomation]::GetWindowText($handle, $titleBuilder, $titleBuilder.Capacity)
        $title = $titleBuilder.ToString()
        [uint32]$processId = 0
        [void][TrendRadar.WeChatChannelAutomation]::GetWindowThreadProcessId($handle, [ref]$processId)
        $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
        if ($null -eq $process -or $process.ProcessName -cne $ProcessName -or [string]::IsNullOrWhiteSpace($title)) { return $true }
        $rect = [TrendRadar.WeChatChannelAutomation+RECT]::new()
        [void][TrendRadar.WeChatChannelAutomation]::GetWindowRect($handle, [ref]$rect)
        $windows.Add([pscustomobject]@{
            Handle = $handle
            HandleText = ('0x{0:X}' -f $handle.ToInt64())
            Title = $title
            ProcessName = $process.ProcessName
            Minimized = [TrendRadar.WeChatChannelAutomation]::IsIconic($handle)
            Left = $rect.Left
            Top = $rect.Top
            Width = $rect.Right - $rect.Left
            Height = $rect.Bottom - $rect.Top
        })
        return $true
    }, [IntPtr]::Zero)
    return @($windows)
}

function Get-AddressBarMatches {
    param([Parameter(Mandatory = $true)][IntPtr]$HostHandle)
    $root = [System.Windows.Automation.AutomationElement]::FromHandle($HostHandle)
    if ($null -eq $root) { return @() }
    $editCondition = [System.Windows.Automation.PropertyCondition]::new(
        [System.Windows.Automation.AutomationElement]::ControlTypeProperty,
        [System.Windows.Automation.ControlType]::Edit)
    $matches = [System.Collections.Generic.List[object]]::new()
    foreach ($edit in @($root.FindAll([System.Windows.Automation.TreeScope]::Descendants, $editCondition))) {
        $className = [string]$edit.Current.ClassName
        $automationId = [string]$edit.Current.AutomationId
        if ($className -ne 'OmniboxViewViews' -and $automationId -ne 'OmniboxViewViews') { continue }
        $pattern = $null
        if (-not $edit.TryGetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern, [ref]$pattern)) { continue }
        if ($edit.Current.IsEnabled -and -not $edit.Current.IsOffscreen -and -not $pattern.Current.IsReadOnly) {
            $matches.Add([pscustomobject]@{ Element = $edit; Pattern = $pattern })
        }
    }
    return @($matches)
}

function Get-WeChatWindows {
    $windows = [System.Collections.Generic.List[object]]::new()
    foreach ($processName in @('WeChatAppEx', 'Weixin', 'WeChat')) {
        foreach ($window in @(Get-TopLevelWindowsByProcessName -ProcessName $processName)) {
            $windows.Add($window)
        }
    }
    return @($windows | Sort-Object HandleText -Unique)
}

function Select-WeChatMainWindow {
    $windows = @(Get-WeChatWindows)
    if ($windows.Count -eq 0) { throw 'wechat_window_not_found' }

    # The browser host is the only reliable target for address-bar navigation.
    $browserWindows = @($windows | Where-Object { @(Get-AddressBarMatches -HostHandle $_.Handle).Count -eq 1 })
    if ($browserWindows.Count -eq 1) { return $browserWindows[0] }
    if ($browserWindows.Count -gt 1) {
        $browserWindows = @($browserWindows | Sort-Object @{Expression = { $_.Width * $_.Height }; Descending = $true })
        if ($browserWindows.Count -gt 1 -and ($browserWindows[0].Width * $browserWindows[0].Height) -eq ($browserWindows[1].Width * $browserWindows[1].Height)) {
            throw 'wechat_window_ambiguous'
        }
        return $browserWindows[0]
    }

    $legacyWindows = @($windows | Where-Object { $_.ProcessName -in @('Weixin', 'WeChat') })
    if ($legacyWindows.Count -eq 1) { return $legacyWindows[0] }
    if ($legacyWindows.Count -eq 0 -and $windows.Count -eq 1) { return $windows[0] }
    throw 'wechat_window_ambiguous'
}

function Get-EdgeTemplateScore {
    param([Drawing.Bitmap]$Bitmap, [Drawing.Bitmap]$Template, [int]$CenterX, [int]$CenterY)
    $left = $CenterX - [int]($Template.Width / 2)
    $top = $CenterY - [int]($Template.Height / 2)
    if ($left -lt 0 -or $top -lt 0 -or ($left + $Template.Width) -gt $Bitmap.Width -or ($top + $Template.Height) -gt $Bitmap.Height) { return 0.0 }
    $dot = 0.0; $bitmapEnergy = 0.0; $templateEnergy = 0.0
    for ($y = 1; $y -lt ($Template.Height - 1); $y += 2) {
        for ($x = 1; $x -lt ($Template.Width - 1); $x += 2) {
            $bitmapLeft = $Bitmap.GetPixel($left + $x - 1, $top + $y)
            $bitmapRight = $Bitmap.GetPixel($left + $x + 1, $top + $y)
            $bitmapTop = $Bitmap.GetPixel($left + $x, $top + $y - 1)
            $bitmapBottom = $Bitmap.GetPixel($left + $x, $top + $y + 1)
            $templateLeft = $Template.GetPixel($x - 1, $y)
            $templateRight = $Template.GetPixel($x + 1, $y)
            $templateTop = $Template.GetPixel($x, $y - 1)
            $templateBottom = $Template.GetPixel($x, $y + 1)
            $bitmapGradient = [Math]::Abs((0.299 * ($bitmapRight.R - $bitmapLeft.R)) + (0.587 * ($bitmapRight.G - $bitmapLeft.G)) + (0.114 * ($bitmapRight.B - $bitmapLeft.B))) +
                [Math]::Abs((0.299 * ($bitmapBottom.R - $bitmapTop.R)) + (0.587 * ($bitmapBottom.G - $bitmapTop.G)) + (0.114 * ($bitmapBottom.B - $bitmapTop.B)))
            $templateGradient = [Math]::Abs((0.299 * ($templateRight.R - $templateLeft.R)) + (0.587 * ($templateRight.G - $templateLeft.G)) + (0.114 * ($templateRight.B - $templateLeft.B))) +
                [Math]::Abs((0.299 * ($templateBottom.R - $templateTop.R)) + (0.587 * ($templateBottom.G - $templateTop.G)) + (0.114 * ($templateBottom.B - $templateTop.B)))
            $dot += $bitmapGradient * $templateGradient
            $bitmapEnergy += $bitmapGradient * $bitmapGradient
            $templateEnergy += $templateGradient * $templateGradient
        }
    }
    if ($bitmapEnergy -le 0.0 -or $templateEnergy -le 0.0) { return 0.0 }
    return [Math]::Round($dot / [Math]::Sqrt($bitmapEnergy * $templateEnergy), 4)
}

function Read-EntryTemplate {
    param([Parameter(Mandatory = $true)][string]$Name)
    $templatePath = Join-Path $PSScriptRoot $Name
    if (-not [IO.File]::Exists($templatePath)) { throw 'wechat_entry_template_missing' }
    try {
        $bytes = [Convert]::FromBase64String(([IO.File]::ReadAllText($templatePath)).Trim())
        $stream = [IO.MemoryStream]::new($bytes)
        try {
            $loaded = [Drawing.Bitmap]::new($stream)
            try { return [Drawing.Bitmap]::new($loaded) }
            finally { $loaded.Dispose() }
        } finally { $stream.Dispose() }
    } catch { throw 'wechat_entry_template_invalid' }
}

function Get-WindowBitmap {
    param([Parameter(Mandatory = $true)][object]$Window)
    $bitmap = [Drawing.Bitmap]::new($Window.Width, $Window.Height)
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    try { $graphics.CopyFromScreen($Window.Left, $Window.Top, 0, 0, $bitmap.Size) }
    finally { $graphics.Dispose() }
    return $bitmap
}

function Find-ScaledEntryTemplate {
    param(
        [Parameter(Mandatory = $true)][Drawing.Bitmap]$Bitmap,
        [Parameter(Mandatory = $true)][string[]]$TemplateNames,
        [Parameter(Mandatory = $true)][double]$ReferenceX,
        [Parameter(Mandatory = $true)][double]$ReferenceY,
        [Parameter(Mandatory = $true)][double[]]$Scales,
        [ValidateRange(0, 4)][int]$PixelRadius = 2
    )
    $templates = [System.Collections.Generic.List[object]]::new()
    try {
        foreach ($name in $TemplateNames) {
            $sourceTemplate = Read-EntryTemplate -Name $name
            try {
                foreach ($scale in $Scales) {
                    $width = [Math]::Max(8, [int][Math]::Round($sourceTemplate.Width * $scale))
                    $height = [Math]::Max(8, [int][Math]::Round($sourceTemplate.Height * $scale))
                    $scaled = [Drawing.Bitmap]::new($width, $height)
                    $graphics = [Drawing.Graphics]::FromImage($scaled)
                    try {
                        $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
                        $graphics.DrawImage($sourceTemplate, 0, 0, $width, $height)
                    } finally { $graphics.Dispose() }
                    $templates.Add([pscustomobject]@{ Name = $name; Scale = $scale; Bitmap = $scaled })
                }
            } finally { $sourceTemplate.Dispose() }
        }

        $coarseMatches = @(foreach ($template in $templates) {
            $baseX = [int][Math]::Round($ReferenceX * $template.Scale)
            $baseY = [int][Math]::Round($ReferenceY * $template.Scale)
            [pscustomobject]@{
                Template = $template
                X = $baseX
                Y = $baseY
                Score = Get-EdgeTemplateScore -Bitmap $Bitmap -Template $template.Bitmap -CenterX $baseX -CenterY $baseY
            }
        })
        $coarseMatches = @($coarseMatches | Sort-Object Score -Descending)
        $refineCandidates = @($coarseMatches | Select-Object -First ([Math]::Min(2, $coarseMatches.Count)))
        $matches = @(foreach ($candidate in $refineCandidates) {
            $template = $candidate.Template
            $baseX = $candidate.X
            $baseY = $candidate.Y
            for ($offsetX = -$PixelRadius; $offsetX -le $PixelRadius; $offsetX++) {
                for ($offsetY = -$PixelRadius; $offsetY -le $PixelRadius; $offsetY++) {
                    $score = Get-EdgeTemplateScore -Bitmap $Bitmap -Template $template.Bitmap -CenterX ($baseX + $offsetX) -CenterY ($baseY + $offsetY)
                    [pscustomobject]@{
                        X = $baseX + $offsetX; Y = $baseY + $offsetY
                        TemplateName = $template.Name; TemplateScale = $template.Scale; TemplateScore = $score
                        CombinedScore = [Math]::Round((0.98 * $score) + (0.02 * (1.0 - (([Math]::Abs($offsetX) + [Math]::Abs($offsetY)) / [Math]::Max(1.0, 2.0 * $PixelRadius)))), 4)
                    }
                }
            }
        })
        $ranked = @($matches | Sort-Object CombinedScore -Descending)
        $best = $ranked[0]
        $best | Add-Member -NotePropertyName Margin -NotePropertyValue $(if ($ranked.Count -gt 1) { $best.CombinedScore - $ranked[1].CombinedScore } else { 1.0 })
        return $best
    } finally {
        foreach ($template in $templates) { $template.Bitmap.Dispose() }
    }
}

function Invoke-WindowClick {
    param(
        [Parameter(Mandatory = $true)][object]$Window,
        [Parameter(Mandatory = $true)][int]$X,
        [Parameter(Mandatory = $true)][int]$Y
    )
    $cursor = [TrendRadar.WeChatChannelAutomation+POINT]::new()
    [void][TrendRadar.WeChatChannelAutomation]::GetCursorPos([ref]$cursor)
    [void][TrendRadar.WeChatChannelAutomation]::SetCursorPos(($Window.Left + $X), ($Window.Top + $Y))
    Start-Sleep -Milliseconds 100
    [TrendRadar.WeChatChannelAutomation]::mouse_event([TrendRadar.WeChatChannelAutomation]::MOUSEEVENTF_LEFTDOWN, 0, 0, 0, [UIntPtr]::Zero)
    Start-Sleep -Milliseconds 60
    [TrendRadar.WeChatChannelAutomation]::mouse_event([TrendRadar.WeChatChannelAutomation]::MOUSEEVENTF_LEFTUP, 0, 0, 0, [UIntPtr]::Zero)
    Start-Sleep -Milliseconds 250
    [void][TrendRadar.WeChatChannelAutomation]::SetCursorPos($cursor.X, $cursor.Y)
}

function Test-ShareUrl {
    param([string]$Value)
    try { $uri = [Uri]$Value } catch { return $false }
    if ($uri.Scheme -cne 'https' -or $uri.Host -notin @('weixin.qq.com', 'channels.weixin.qq.com')) { return $false }
    return $uri.AbsolutePath -match '^/sph(?:/|$)|^/finder-preview/pages/sph(?:/|$)'
}

if (-not $EntryOnly -and -not (Test-ShareUrl -Value $ShareUrl)) { throw 'wechat_share_url_invalid' }

$main = Select-WeChatMainWindow
$browserHostReady = @(Get-AddressBarMatches -HostHandle $main.Handle).Count -eq 1

if (-not [TrendRadar.WeChatChannelAutomation]::ActivateWindow($main.Handle, [TrendRadar.WeChatChannelAutomation]::SW_RESTORE)) { throw 'wechat_window_activation_failed' }
Start-Sleep -Milliseconds 150
$rect = [TrendRadar.WeChatChannelAutomation+RECT]::new()
[void][TrendRadar.WeChatChannelAutomation]::GetWindowRect($main.Handle, [ref]$rect)
$main.Left = $rect.Left; $main.Top = $rect.Top; $main.Width = $rect.Right - $rect.Left; $main.Height = $rect.Bottom - $rect.Top
if ($main.Width -lt 500 -or $main.Height -lt 400) { throw 'wechat_window_invalid_size' }

if (-not $browserHostReady) {
    $bitmap = Get-WindowBitmap -Window $main
    try {
        $entryScales = @(0.75, 1.0, 1.25, 1.5, 1.75, 2.0)
        $discover = Find-ScaledEntryTemplate -Bitmap $bitmap `
            -TemplateNames @('wechat-discover-entry-active-template.b64', 'wechat-discover-entry-inactive-template.b64') `
            -ReferenceX 59 -ReferenceY 512 -Scales $entryScales -PixelRadius 2
        Write-Verbose ("discover-entry window={0} best={1},{2} template={3} scale={4} score={5} combined={6}" -f $main.HandleText, $discover.X, $discover.Y, $discover.TemplateName, $discover.TemplateScale, $discover.TemplateScore, $discover.CombinedScore)
        if ($discover.TemplateScore -ge 0.84) {
            Invoke-WindowClick -Window $main -X $discover.X -Y $discover.Y

            $channel = $null
            for ($attempt = 0; $attempt -lt 10; $attempt++) {
                Start-Sleep -Milliseconds 250
                $menuBitmap = Get-WindowBitmap -Window $main
                try {
                    $channel = Find-ScaledEntryTemplate -Bitmap $menuBitmap `
                        -TemplateNames @('wechat-channel-menu-active-template.b64', 'wechat-channel-menu-inactive-template.b64') `
                        -ReferenceX 172 -ReferenceY 303 -Scales $entryScales -PixelRadius 2
                } finally { $menuBitmap.Dispose() }
                if ($channel.TemplateScore -ge 0.84) { break }
                $channel = $null
            }
            if ($null -eq $channel) { throw 'wechat_channel_menu_not_ready' }
            Write-Verbose ("channel-menu window={0} best={1},{2} template={3} scale={4} score={5} combined={6}" -f $main.HandleText, $channel.X, $channel.Y, $channel.TemplateName, $channel.TemplateScale, $channel.TemplateScore, $channel.CombinedScore)
            Invoke-WindowClick -Window $main -X $channel.X -Y $channel.Y
        } else {
            $legacy = Find-ScaledEntryTemplate -Bitmap $bitmap -TemplateNames @('wechat-channel-entry-template.b64') `
                -ReferenceX 78 -ReferenceY 617 -Scales $entryScales -PixelRadius 2
            Write-Verbose ("legacy-entry window={0} best={1},{2} template={3} combined={4} margin={5}" -f $main.HandleText, $legacy.X, $legacy.Y, $legacy.TemplateScore, $legacy.CombinedScore, $legacy.Margin)
            if ($legacy.TemplateScore -lt 0.75) { throw 'wechat_channel_entry_template_mismatch' }
            Invoke-WindowClick -Window $main -X $legacy.X -Y $legacy.Y
        }
    } finally {
        $bitmap.Dispose()
    }
}

if ($EntryOnly -and -not $Refresh) {
    'wechat_channel_entry_sent'
    return
}

$webView = $null
$webViewRootHandle = $null
$webViewTopLevel = $false

function Get-EmbeddedWebViewWindows {
    param([Parameter(Mandatory = $true)][IntPtr]$HostHandle)
    $root = [System.Windows.Automation.AutomationElement]::FromHandle($HostHandle)
    if ($null -eq $root) { return @() }
    $windows = [System.Collections.Generic.List[object]]::new()
    $all = $root.FindAll(
        [System.Windows.Automation.TreeScope]::Descendants,
        [System.Windows.Automation.Condition]::TrueCondition)
    foreach ($element in $all) {
        $current = $element.Current
        $handle = [IntPtr]$current.NativeWindowHandle
        if ($handle -eq [IntPtr]::Zero -or $handle -eq $HostHandle) { continue }
        $className = [string]$current.ClassName
        if ($className -notin @('MMUIRenderSubWindowHW', 'Chrome_WidgetWin_0', 'WebView2')) { continue }
        $rect = [TrendRadar.WeChatChannelAutomation+RECT]::new()
        [void][TrendRadar.WeChatChannelAutomation]::GetWindowRect($handle, [ref]$rect)
        if (($rect.Right - $rect.Left) -le 300 -or ($rect.Bottom - $rect.Top) -le 200) { continue }
        $windows.Add([pscustomobject]@{
            Handle = $handle
            HandleText = ('0x{0:X}' -f $handle.ToInt64())
            Title = [string]$current.Name
            ProcessName = 'WeChatEmbeddedWebView'
            Minimized = $false
            Left = $rect.Left
            Top = $rect.Top
            Width = $rect.Right - $rect.Left
            Height = $rect.Bottom - $rect.Top
        })
    }
    return @($windows | Sort-Object HandleText -Unique)
}

for ($attempt = 0; $attempt -lt 30; $attempt++) {
    $webViews = @(Get-TopLevelWindowsByProcessName -ProcessName 'WeChatAppEx' | Where-Object { $_.Width -gt 300 -and $_.Height -gt 200 })
    if ($webViews.Count -eq 1) {
        $webView = $webViews[0]
        $webViewRootHandle = $webView.Handle
        $webViewTopLevel = $true
        break
    }
    if ($webViews.Count -gt 1) { throw 'wechat_webview_ambiguous' }
    $embedded = @(Get-EmbeddedWebViewWindows -HostHandle $main.Handle)
    if ($embedded.Count -eq 1) {
        $webView = $embedded[0]
        $webViewRootHandle = $main.Handle
        break
    }
    if ($embedded.Count -gt 1) { throw 'wechat_webview_ambiguous' }
    Start-Sleep -Milliseconds 500
}
if ($null -eq $webView) { throw 'wechat_webview_not_ready' }
if ($webViewTopLevel) {
    if (-not [TrendRadar.WeChatChannelAutomation]::ActivateWindow($webView.Handle, [TrendRadar.WeChatChannelAutomation]::SW_RESTORE)) { throw 'wechat_webview_activation_failed' }
    Start-Sleep -Milliseconds 200
}

if ($EntryOnly) {
    if (-not [TrendRadar.WeChatChannelAutomation]::PostMessage($webView.Handle, [TrendRadar.WeChatChannelAutomation]::WM_KEYDOWN, [IntPtr]0x74, [IntPtr]::Zero)) { throw 'wechat_page_refresh_failed' }
    [void][TrendRadar.WeChatChannelAutomation]::PostMessage($webView.Handle, [TrendRadar.WeChatChannelAutomation]::WM_KEYUP, [IntPtr]0x74, [IntPtr]::Zero)
    'wechat_page_refresh_sent'
    return
}

$matches = @(Get-AddressBarMatches -HostHandle $webViewRootHandle)
if ($matches.Count -ne 1) { throw 'wechat_share_address_bar_not_ready' }
$keyboardHostHandle = if ($webViewTopLevel) { $webView.Handle } else { $webViewRootHandle }
if (-not [TrendRadar.WeChatChannelAutomation]::ActivateWindow($keyboardHostHandle, [TrendRadar.WeChatChannelAutomation]::SW_RESTORE)) { throw 'wechat_webview_activation_failed' }
Start-Sleep -Milliseconds 80
[TrendRadar.WeChatChannelAutomation]::keybd_event([byte][TrendRadar.WeChatChannelAutomation]::VK_CONTROL, 0, 0, [UIntPtr]::Zero)
[TrendRadar.WeChatChannelAutomation]::keybd_event([byte][TrendRadar.WeChatChannelAutomation]::VK_L, 0, 0, [UIntPtr]::Zero)
[TrendRadar.WeChatChannelAutomation]::keybd_event([byte][TrendRadar.WeChatChannelAutomation]::VK_L, 0, [TrendRadar.WeChatChannelAutomation]::KEYEVENTF_KEYUP, [UIntPtr]::Zero)
[TrendRadar.WeChatChannelAutomation]::keybd_event([byte][TrendRadar.WeChatChannelAutomation]::VK_CONTROL, 0, [TrendRadar.WeChatChannelAutomation]::KEYEVENTF_KEYUP, [UIntPtr]::Zero)
Start-Sleep -Milliseconds 80
$matches[0].Pattern.SetValue($ShareUrl)
$enteredValue = [string]$matches[0].Pattern.Current.Value
if ($enteredValue -cne $ShareUrl) { throw 'wechat_share_address_bar_write_failed' }
[TrendRadar.WeChatChannelAutomation]::keybd_event([byte][TrendRadar.WeChatChannelAutomation]::VK_RETURN, 0, 0, [UIntPtr]::Zero)
[TrendRadar.WeChatChannelAutomation]::keybd_event([byte][TrendRadar.WeChatChannelAutomation]::VK_RETURN, 0, [TrendRadar.WeChatChannelAutomation]::KEYEVENTF_KEYUP, [UIntPtr]::Zero)
Start-Sleep -Milliseconds 1000
$postNavigationMatches = @(Get-AddressBarMatches -HostHandle $webViewRootHandle)
if ($postNavigationMatches.Count -ne 1 -or [string]$postNavigationMatches[0].Pattern.Current.Value -cne $ShareUrl) { throw 'wechat_share_navigation_not_verified' }
'wechat_known_share_navigation_verified'
