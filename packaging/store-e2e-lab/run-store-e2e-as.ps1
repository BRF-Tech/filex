# Runs desktop/scripts/store-e2e.mjs as a SECOND local administrator, in an
# interactive session of its own, on a GitHub-hosted Windows runner (task #248,
# path A).
#
#   pwsh -File run-store-e2e-as.ps1 -Desktop C:\lab\filex\desktop -Cli C:\lab\filex\bin\filex-release-e2e.exe -Out C:\lab\out
#
# Why: the runner's own account (`runneradmin`) is the built-in Administrator
# renamed - RID 500, a full administrator token, no UAC - and an MSIX app's
# Chromium does not start under it (actions/runner-images#9537). No activation
# check of the Store package ever passed on GitHub from 0.44.2 on for that
# reason alone. Under another account, with UAC, in a session that has a
# desktop, the package can activate the way it does on a person's machine.
#
# What it does, in order:
#   1. Developer Mode on (an unsigned package registered from its files);
#   2. a local account `e2e` with a random password (masked in the log, kept
#      nowhere), in Administrators and Remote Desktop Users;
#   3. Remote Desktop on, and an RDP session for `e2e` opened from this
#      machine to itself (127.0.0.2: the client refuses 127.0.0.1) with
#      FreeRDP - so `e2e` has an interactive desktop, with Explorer and the
#      notification service, as a person signed in would;
#   4. store-e2e.mjs started IN that session by a scheduled task that runs as
#      `e2e` interactively (`schtasks /ru e2e /it`), with neither CI nor
#      GITHUB_ACTIONS in its environment: every check is strict, as it is on
#      a developer's desktop - none is only a warning;
#   5. waits for it (StoreE2eMinutes, 25), copies its log to -Out and exits
#      with its exit code.
#
# The checkout and the CLI must be readable by `e2e` (it is given -Desktop's
# repository, -Cli and -Out). Nothing here is kept: the runner is thrown away.
param(
  [Parameter(Mandatory = $true)][string]$Desktop,
  [Parameter(Mandatory = $true)][string]$Cli,
  [Parameter(Mandatory = $true)][string]$Out,
  [int]$StoreE2eMinutes = 25,
  [int]$SessionSeconds = 120
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
New-Item -ItemType Directory -Force -Path $Out | Out-Null
function Say([string]$m) { Write-Host "store-e2e-lab: $m" }

# 1. Developer Mode.
New-Item -Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock' -Force | Out-Null
Set-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock' -Name AllowDevelopmentWithoutDevLicense -Value 1 -Type DWord

# 2. The account. 24 random bytes, base64: letters, digits and + / =, which
# Windows' complexity rule takes; the masked value never reaches the log.
$bytes = New-Object byte[] 24
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
$plain = 'Fx9!' + [Convert]::ToBase64String($bytes)
Write-Host "::add-mask::$plain"
$secure = ConvertTo-SecureString $plain -AsPlainText -Force
if (Get-LocalUser -Name e2e -ErrorAction SilentlyContinue) { Remove-LocalUser -Name e2e }
New-LocalUser -Name e2e -Password $secure -PasswordNeverExpires -AccountNeverExpires -Description 'filex store-e2e lab' | Out-Null
Add-LocalGroupMember -Group Administrators -Member e2e
Add-LocalGroupMember -Group 'Remote Desktop Users' -Member e2e
$sid = (Get-LocalUser -Name e2e).SID.Value
Say "account e2e $sid (not RID 500)"

# The repository, the CLI and the output, readable and writable by e2e.
$repo = Split-Path -Parent $Desktop
foreach ($p in @($repo, (Split-Path -Parent $Cli), $Out)) {
  icacls $p /grant 'e2e:(OI)(CI)F' /T /Q | Out-Null
}

# 3. Remote Desktop, and a session for e2e. NLA off: the loopback client signs
# in with the password on its command line.
Set-ItemProperty -Path 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name fDenyTSConnections -Value 0
Set-ItemProperty -Path 'HKLM:\System\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp' -Name UserAuthentication -Value 0
Enable-NetFirewallRule -DisplayGroup 'Remote Desktop' -ErrorAction SilentlyContinue
Start-Service TermService -ErrorAction SilentlyContinue
$rdp = (Get-Command 'wfreerdp.exe', 'sdl3-freerdp.exe', 'sdl-freerdp.exe' -ErrorAction SilentlyContinue | Select-Object -First 1).Source
if (-not $rdp) { throw 'no FreeRDP client (wfreerdp.exe, sdl3-freerdp.exe or sdl-freerdp.exe) on PATH: install it first' }
$client = Start-Process -FilePath $rdp -ArgumentList @('/v:127.0.0.2', '/u:e2e', "/p:$plain", '/cert:ignore', '/size:1280x800', '+auto-reconnect') -PassThru -WindowStyle Hidden
$session = $null
$clock = [Diagnostics.Stopwatch]::StartNew()
while (-not $session -and $clock.Elapsed.TotalSeconds -lt $SessionSeconds) {
  Start-Sleep -Seconds 2
  $session = (quser 2>$null | Select-String -Pattern '^\s*>?e2e\s' | Select-Object -First 1)
  if ($session -and $session.Line -notmatch '\sActive\s') { $session = $null }
}
if (-not $session) { throw "no active session for e2e after $SessionSeconds s (FreeRDP pid $($client.Id), exited: $($client.HasExited))" }
Say "interactive session: $($session.Line.Trim())"
Start-Sleep -Seconds 15

# 4. store-e2e.mjs in that session, as e2e. A runner of its own, so the
# command line holds no quoting a scheduled task could mangle.
$node = (Get-Command node.exe).Source
$log = Join-Path $Out 'store-e2e.log'
$exit = Join-Path $Out 'store-e2e.exit'
Remove-Item -Force -ErrorAction SilentlyContinue $log, $exit
$runner = Join-Path $Out 'run.cmd'
@(
  '@echo off',
  'set CI=',
  'set GITHUB_ACTIONS=',
  "set FILEX_CLI_BIN=$Cli",
  "cd /d `"$Desktop`"",
  "`"$node`" scripts\store-e2e.mjs > `"$log`" 2>&1",
  "echo %ERRORLEVEL% > `"$exit`""
) | Set-Content -Path $runner -Encoding ASCII
# Not elevated (no HIGHEST run level): the filtered token UAC gives an administrator,
# as on a person's desktop - a per-user package registers without elevation.
schtasks /create /tn filex-store-e2e /tr "`"$runner`"" /sc once /st 23:59 /ru e2e /rp $plain /it /f | Out-Null
schtasks /run /tn filex-store-e2e | Out-Null
Say "store-e2e started in e2e's session; waiting at most $StoreE2eMinutes min"

# 5. Its result.
$clock.Restart()
while (-not (Test-Path $exit) -and $clock.Elapsed.TotalMinutes -lt $StoreE2eMinutes) { Start-Sleep -Seconds 10 }
if (Test-Path $log) { Get-Content $log }
if (-not (Test-Path $exit)) {
  Say "store-e2e did not finish in $StoreE2eMinutes min"
  schtasks /end /tn filex-store-e2e | Out-Null
  exit 1
}
$code = [int]((Get-Content $exit -Raw).Trim())
Say "store-e2e exited $code"
exit $code
