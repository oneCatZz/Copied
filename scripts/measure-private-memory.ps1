param(
    [Parameter(Mandatory = $true)][string]$Executable,
    [Parameter(Mandatory = $true)][string]$DataDirectory,
    [Parameter(Mandatory = $true)][string]$ReportPath,
    [int]$Cycles = 4,
    [switch]$StartupOnly
)

$ErrorActionPreference = 'Stop'
$Executable = [IO.Path]::GetFullPath($Executable)
$DataDirectory = [IO.Path]::GetFullPath($DataDirectory)
if ($DataDirectory.TrimEnd('\') -eq $env:APPDATA.TrimEnd('\')) {
    throw 'Use an isolated data directory, not the current application-data directory.'
}
New-Item -ItemType Directory -Path $DataDirectory -Force | Out-Null
$lockDirectory = Join-Path $env:TEMP ('cp-mem-' + [guid]::NewGuid().ToString('N').Substring(0, 6))
New-Item -ItemType Directory -Path $lockDirectory -Force | Out-Null

Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text;

public static class CopiedMemoryProbe {
    private delegate bool EnumProc(IntPtr window, IntPtr parameter);
    [DllImport("user32.dll")] private static extern bool EnumWindows(EnumProc callback, IntPtr parameter);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window, out uint process);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] private static extern int GetWindowText(IntPtr window, StringBuilder text, int count);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] private static extern uint GetDpiForWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern bool GetClientRect(IntPtr window, out Rectangle rectangle);
    [DllImport("user32.dll")] private static extern bool ClientToScreen(IntPtr window, ref Point point);
    [DllImport("user32.dll")] private static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] private static extern void mouse_event(uint flags, uint x, uint y, uint data, UIntPtr extra);
    [DllImport("user32.dll")] private static extern void keybd_event(byte key, byte scan, uint flags, UIntPtr extra);
    [DllImport("psapi.dll", SetLastError = true)] private static extern bool QueryWorkingSet(IntPtr process, IntPtr buffer, int size);
    [StructLayout(LayoutKind.Sequential)] private struct Rectangle { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] private struct Point { public int X, Y; }

    public static void Focus(IntPtr window) {
        if (GetForegroundWindow() != window && !SetForegroundWindow(window)) {
            throw new InvalidOperationException("Could not focus the isolated window for input.");
        }
    }

    public static IntPtr FindPanel(int process) {
        IntPtr found = IntPtr.Zero;
        EnumWindows(delegate(IntPtr window, IntPtr unused) {
            uint owner;
            GetWindowThreadProcessId(window, out owner);
            if (owner == process && IsWindowVisible(window)) {
                var title = new StringBuilder(256);
                GetWindowText(window, title, title.Capacity);
                if (title.ToString() == "Copied") { found = window; return false; }
            }
            return true;
        }, IntPtr.Zero);
        return found;
    }

    public static double Width(IntPtr window) {
        Rectangle rectangle;
        if (!GetClientRect(window, out rectangle)) throw new Win32Exception();
        return rectangle.Right / (GetDpiForWindow(window) / 96.0);
    }

    public static void Click(IntPtr window, double x, double y) {
        double scale = GetDpiForWindow(window) / 96.0;
        var point = new Point { X = (int)Math.Round(x * scale), Y = (int)Math.Round(y * scale) };
        ClientToScreen(window, ref point);
        Focus(window);
        SetCursorPos(point.X, point.Y);
        mouse_event(2, 0, 0, 0, UIntPtr.Zero);
        mouse_event(4, 0, 0, 0, UIntPtr.Zero);
    }

    public static void Escape(IntPtr window) {
        Focus(window);
        keybd_event(27, 0, 0, UIntPtr.Zero);
        keybd_event(27, 0, 2, UIntPtr.Zero);
    }

    public static long PrivateWorkingSet(Process process) {
        int size = (int)Math.Max(65536, (process.WorkingSet64 / Environment.SystemPageSize + 4096) * IntPtr.Size + IntPtr.Size);
        for (int attempt = 0; attempt < 4; attempt++, size *= 2) {
            IntPtr buffer = Marshal.AllocHGlobal(size);
            try {
                if (!QueryWorkingSet(process.Handle, buffer, size)) {
                    int error = Marshal.GetLastWin32Error();
                    if (error == 24) continue;
                    throw new Win32Exception(error);
                }
                long count = Marshal.ReadIntPtr(buffer).ToInt64(), pages = 0;
                for (long i = 0; i < count; i++) {
                    long block = Marshal.ReadIntPtr(buffer, checked((int)((i + 1) * IntPtr.Size))).ToInt64();
                    if ((block & 0x100) == 0) pages++;
                }
                return pages * Environment.SystemPageSize;
            } finally { Marshal.FreeHGlobal(buffer); }
        }
        throw new InvalidOperationException("Working-set buffer did not converge.");
    }
}
'@

function Start-IsolatedCopied([bool]$AtLogin) {
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $Executable
    $start.WorkingDirectory = Split-Path -Parent $Executable
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    if ($AtLogin) { $start.Arguments = '--mygo-opened-at-login' }
    $start.EnvironmentVariables['APPDATA'] = $DataDirectory
    $start.EnvironmentVariables['XDG_RUNTIME_DIR'] = $lockDirectory
    foreach ($key in @($start.EnvironmentVariables.Keys)) {
        if ($key.StartsWith('COPIED_NATIVE_') -or $key -in @('MYGO_AUTOMATION', 'MYGO_FRAME_STATS', 'MYGO_INSPECTOR')) {
            $start.EnvironmentVariables.Remove($key)
        }
    }
    return [Diagnostics.Process]::Start($start)
}

$samples = New-Object 'Collections.Generic.List[object]'
$process = Start-IsolatedCopied $true
function Add-Sample([string]$Phase) {
    $process.Refresh()
    if ($process.HasExited) { throw 'The isolated Copied process exited unexpectedly.' }
    $samples.Add([pscustomobject]@{
        Phase = $Phase
        At = [DateTimeOffset]::Now.ToString('o')
        ProcessId = $process.Id
        PrivateCommitMiB = [Math]::Round($process.PrivateMemorySize64 / 1MB, 2)
        PrivateWorkingSetMiB = [Math]::Round([CopiedMemoryProbe]::PrivateWorkingSet($process) / 1MB, 2)
        WorkingSetMiB = [Math]::Round($process.WorkingSet64 / 1MB, 2)
        VisiblePanel = [CopiedMemoryProbe]::FindPanel($process.Id) -ne [IntPtr]::Zero
        Modules = @($process.Modules | ForEach-Object { $_.ModuleName })
    })
    if ($Phase -like 'hidden-*' -and $samples[$samples.Count - 1].VisiblePanel) {
        throw 'The panel did not hide; this measurement is invalid.'
    }
    $samples | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $ReportPath
    $samples[$samples.Count - 1] | Select-Object Phase, PrivateCommitMiB, PrivateWorkingSetMiB, WorkingSetMiB, VisiblePanel | Format-Table -AutoSize
}

function Open-Panel {
    $second = Start-IsolatedCopied $false
    try {
        if (!$second.WaitForExit(5000)) { throw 'The second instance did not hand over to the isolated process.' }
    } finally { $second.Dispose() }
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        $panel = [CopiedMemoryProbe]::FindPanel($process.Id)
        if ($panel -ne [IntPtr]::Zero) { Start-Sleep -Milliseconds 700; return $panel }
        Start-Sleep -Milliseconds 50
    }
    throw 'The isolated panel did not open.'
}

try {
    Start-Sleep -Seconds 3
    Add-Sample 'cold-login'
    $panel = Open-Panel
    Add-Sample 'shown'
    if ($StartupOnly) { return }
    $width = [CopiedMemoryProbe]::Width($panel)
    $right = 64
    if ($width -ge 1100) { $right = 126 }
    for ($cycle = 0; $cycle -lt $Cycles; $cycle++) {
        for ($type = 0; $type -lt 6; $type++) {
            [CopiedMemoryProbe]::Click($panel, $width - $right - 59, 35)
            Start-Sleep -Milliseconds 200
            $x = $width - $right - 272 + 71.5 + ($type % 2) * 129
            $y = 120 + [Math]::Floor($type / 2) * 54
            [CopiedMemoryProbe]::Click($panel, $x, $y)
            Start-Sleep -Milliseconds 500
        }
    }
    Add-Sample ('switched-' + ($Cycles * 6))
    [CopiedMemoryProbe]::Escape($panel)
    Start-Sleep -Milliseconds 200
    Add-Sample 'hidden-200ms'
    Start-Sleep -Seconds 3
    Add-Sample 'hidden-3s'
    Start-Sleep -Seconds 5
    Add-Sample 'hidden-8s'
    $panel = Open-Panel
    Add-Sample 'reopened'
    [CopiedMemoryProbe]::Escape($panel)
    Start-Sleep -Seconds 3
    Add-Sample 'hidden-again'
} finally {
    if (!$process.HasExited) { $process.Kill(); $process.WaitForExit() }
    $process.Dispose()
}
