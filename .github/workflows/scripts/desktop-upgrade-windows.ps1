# An installed copy of the previous release updates to this build, on Windows.
#
#   pwsh desktop-upgrade-windows.ps1 -Old <previous setup.exe> -New <this setup.exe>
#        -Label <name for the pictures> -Shots <dir> -Scripts <dir of desktop-look.mjs> [-Port 9340]
#
# 1. Installs the previous release's NSIS installer the way a person does
#    (silent, per-user), opens the app and closes it, and leaves a marker
#    file in the app's user data (%APPDATA%\@brftech\filex-desktop).
# 2. Runs this build's installer over it exactly as the app's own updater
#    does (desktop/src/main.ts: autoUpdater.quitAndInstall(true, true), which
#    electron-updater's NsisUpdater turns into `--updated /S --force-run`).
# 3. Checks what is left:
#    - ONE copy: one uninstall entry for the app in HKCU and HKLM (native and
#      WOW6432Node), under the key the previous version registered, saying
#      this version; nothing under Program Files;
#    - this version: the installer's own version, in the registry and on
#      filex.exe, in the folder the previous version was installed to;
#    - the user's data: the marker and the files the previous version wrote;
#    - the app opens.
#    Then it uninstalls, so the steps after it start from a clean machine.
#
# ⚠ Why (0.55, #68): the desktop packages moved from electron-builder 24 to
# 26, and the installer that replaces an installed copy is the one thing a
# fresh install never exercises: the uninstall key the old one wrote (derived
# from the appId), the old uninstaller run with --updated and /KEEP_APP_DATA,
# the "is the app running" check. A new key or a new folder leaves two copies
# behind; a wrong uninstall step deletes the user's sign-in. Nothing tested an
# update before this.
#
# ⚠ The installer started with --force-run brings the app back itself (through
# the shell, as the signed-in user); that relaunch is reported, not required:
# the runner's session may have no shell to launch through.
param(
  [Parameter(Mandatory = $true)] [string] $Old,
  [Parameter(Mandatory = $true)] [string] $New,
  [Parameter(Mandatory = $true)] [string] $Label,
  [Parameter(Mandatory = $true)] [string] $Shots,
  [Parameter(Mandatory = $true)] [string] $Scripts,
  [int] $Port = 9340
)
$ErrorActionPreference = 'Stop'

# electron-builder's NSIS installer writes DisplayName "${productName} ${version}".
$NamePattern = '^filex( |$)'
$Home_ = Join-Path $env:LOCALAPPDATA 'Programs\filex'
$App = Join-Path $Home_ 'filex.exe'
$UserData = Join-Path $env:APPDATA '@brftech\filex-desktop'
$Marker = Join-Path $UserData 'filex-upgrade-check.txt'

function Fail([string] $why) {
  Write-Host "::error title=desktop upgrade ($Label)::$why"
  throw $why
}

# Every uninstall entry of the app, wherever an installer may have put one.
function Get-AppEntries {
  $roots = @(
    'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall',
    'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall',
    'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall'
  )
  foreach ($root in $roots) {
    if (-not (Test-Path $root)) { continue }
    foreach ($k in Get-ChildItem $root) {
      $p = Get-ItemProperty $k.PSPath
      if ($p.DisplayName -and $p.DisplayName -match $NamePattern) {
        [pscustomobject]@{
          Key = $k.PSChildName
          Root = $root
          DisplayName = $p.DisplayName
          DisplayVersion = $p.DisplayVersion
          Publisher = $p.Publisher
          UninstallString = $p.UninstallString
          QuietUninstallString = $p.QuietUninstallString
        }
      }
    }
  }
}

# 1.2.3 out of 1.2.3, 1.2.3.0 or 1.2.3-rc.1 (the numbers a version resource can hold).
function Get-Numbers([string] $v) {
  if ($v -match '^(\d+)\.(\d+)\.(\d+)') { return "$($Matches[1]).$($Matches[2]).$($Matches[3])" }
  return $v
}

function Stop-App {
  Get-Process filex -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path.StartsWith($Home_, [StringComparison]::OrdinalIgnoreCase) } | Stop-Process -Force
  for ($i = 0; $i -lt 15; $i++) {
    $left = Get-Process filex -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path.StartsWith($Home_, [StringComparison]::OrdinalIgnoreCase) }
    if (-not $left) { return }
    Start-Sleep -Seconds 1
  }
  Fail "the app did not stop: $(($left | ForEach-Object { $_.Id }) -join ', ')"
}

# Runs an installer and waits for IT, not for what it starts: Start-Process
# -Wait waits for the whole process tree, and --force-run starts the app.
function Invoke-Installer([string] $exe, [string[]] $arguments) {
  $p = Start-Process $exe -ArgumentList $arguments -PassThru
  $null = $p.Handle
  if (-not $p.WaitForExit(300000)) { Fail "$(Split-Path $exe -Leaf) $($arguments -join ' ') did not finish in 5 minutes" }
  return $p.ExitCode
}

function Open-App([string] $shot) {
  Stop-App
  node (Join-Path $Scripts 'desktop-look.mjs') --exe $App --out $shot --port $Port --timeout 150
  if ($LASTEXITCODE -ne 0) { Fail "the installed app did not open ($shot)" }
  Stop-App
}

$oldVersion = (Get-Item $Old).VersionInfo.ProductVersion
$newVersion = (Get-Item $New).VersionInfo.ProductVersion
Write-Host "previous release: $oldVersion ($Old)"
Write-Host "this build:       $newVersion ($New)"
if (-not $oldVersion -or -not $newVersion) { Fail 'an installer carries no version' }
if ($oldVersion -eq $newVersion) {
  Write-Host "::warning title=desktop upgrade ($Label)::the previous release and this build are both ${newVersion}: this installs the same version over itself"
}

if (Get-AppEntries) { Fail "the machine is not clean: an uninstall entry for filex exists before the test" }
if (Test-Path $Home_) { Fail "the machine is not clean: $Home_ exists before the test" }

# ── 1. the previous release, as a person installs it ───────────────────────
$code = Invoke-Installer $Old @('/S', '/currentuser')
if ($code -ne 0) { Fail "the previous installer exited $code" }
if (-not (Test-Path $App)) { Fail "the previous installer left no $App" }
$before = @(Get-AppEntries)
if ($before.Count -ne 1) { Fail "the previous release registered $($before.Count) uninstall entries" }
$key = $before[0].Key
$root = $before[0].Root
Write-Host "previous release registered: $root\$key ($($before[0].DisplayName))"
if ($before[0].DisplayVersion -ne $oldVersion) { Fail "the previous release registered version $($before[0].DisplayVersion), its installer says $oldVersion" }

Open-App (Join-Path $Shots "$Label-upgrade-previous.png")
if (-not (Test-Path $UserData)) {
  Get-ChildItem $env:APPDATA -Directory | ForEach-Object { Write-Host "  $env:APPDATA\$($_.Name)" }
  Fail "the previous release wrote no user data to $UserData"
}
$token = [guid]::NewGuid().ToString()
Set-Content -Path $Marker -Value $token -NoNewline
$kept = @(Get-ChildItem $UserData -Force | ForEach-Object { $_.Name } | Where-Object { $_ -in @('Local State', 'Preferences', 'desktop-state.bin', 'logs') })
Write-Host "user data before the update: $((Get-ChildItem $UserData -Force | ForEach-Object { $_.Name }) -join ', ')"

# ── 2. this build, as the app's updater runs it ────────────────────────────
$code = Invoke-Installer $New @('--updated', '/S', '--force-run')
if ($code -ne 0) { Fail "this installer, run as the updater runs it, exited $code" }

# The relaunch --force-run asks for (reported only, see the top).
$back = $false
for ($i = 0; $i -lt 30; $i++) {
  if (Get-Process filex -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path.StartsWith($Home_, [StringComparison]::OrdinalIgnoreCase) }) { $back = $true; break }
  Start-Sleep -Seconds 2
}
if ($back) { Write-Host 'the app came back after the update (--force-run)' }
else { Write-Host "::warning title=desktop upgrade ($Label)::the app did not come back after the update within 60 s (--force-run launches it through the shell; the runner may have none)" }
Stop-App

# ── 3. one copy, this version, the same place, the user's data ─────────────
$after = @(Get-AppEntries)
foreach ($e in $after) { Write-Host "after the update: $($e.Root)\$($e.Key) = $($e.DisplayName) [$($e.DisplayVersion)] $($e.QuietUninstallString)" }
if ($after.Count -ne 1) { Fail "after the update there are $($after.Count) uninstall entries for filex: two copies installed" }
if ($after[0].Key -ne $key -or $after[0].Root -ne $root) { Fail "the update registered $($after[0].Root)\$($after[0].Key), the previous release $root\$key" }
if ($after[0].DisplayVersion -ne $newVersion) { Fail "the uninstall entry says $($after[0].DisplayVersion), this build is $newVersion" }
if ($after[0].QuietUninstallString -notmatch [regex]::Escape($Home_)) { Fail "the uninstall entry points elsewhere: $($after[0].QuietUninstallString)" }
$location = (Get-ItemProperty "HKCU:\Software\$key" -ErrorAction SilentlyContinue).InstallLocation
if ($location -and $location.TrimEnd('\') -ne $Home_.TrimEnd('\')) { Fail "InstallLocation is $location, not $Home_" }
foreach ($pf in @($env:ProgramFiles, ${env:ProgramFiles(x86)})) {
  if ($pf -and (Test-Path (Join-Path $pf 'filex'))) { Fail "a copy under $pf\filex" }
}
$appVersion = (Get-Item $App).VersionInfo.FileVersion
if ((Get-Numbers $appVersion) -ne (Get-Numbers $newVersion)) { Fail "filex.exe is $appVersion after the update, this build is $newVersion" }
$uninstallers = @(Get-ChildItem $Home_ -Filter 'Uninstall*.exe')
if ($uninstallers.Count -ne 1) { Fail "$($uninstallers.Count) uninstallers in $Home_" }

if (-not (Test-Path $Marker)) { Fail "the update deleted the user's data: $Marker is gone" }
if ((Get-Content $Marker -Raw) -ne $token) { Fail "the update rewrote $Marker" }
foreach ($n in $kept) {
  if (-not (Test-Path (Join-Path $UserData $n))) { Fail "the update deleted $n from the user's data" }
}
Write-Host "user data after the update: $((Get-ChildItem $UserData -Force | ForEach-Object { $_.Name }) -join ', ')"

Open-App (Join-Path $Shots "$Label-upgrade-updated.png")
if (-not (Test-Path $Marker)) { Fail "the updated app deleted $Marker" }

# ── clean up for the steps after this one ──────────────────────────────────
$code = Invoke-Installer $uninstallers[0].FullName @('/S', '/currentuser')
# The uninstaller hands over to a copy of itself in %TEMP% and returns.
for ($i = 0; $i -lt 30 -and (Test-Path $App); $i++) { Start-Sleep -Seconds 2 }
if (Test-Path $App) { Fail "uninstall (exit $code) left $App behind" }
Remove-Item -Recurse -Force $UserData -ErrorAction SilentlyContinue
"### Desktop update ($Label): $oldVersion -> $newVersion, one copy, user data kept" | Out-File -Append -Encoding utf8 $env:GITHUB_STEP_SUMMARY
Write-Host "updated $oldVersion -> ${newVersion}: one copy under $Home_, user data kept"
exit 0
