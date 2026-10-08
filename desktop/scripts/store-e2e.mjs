// Does the Microsoft Store (MSIX) package actually work as a Store copy?
//
// The .appx builds without complaint from files that would break it (a "--"
// inside an XML comment made makeappx reject the manifest; an Application.Id
// starting with a digit fails at build time) — but most of what can go wrong
// only shows once Windows has INSTALLED the package and runs it with a package
// identity:
//
//   1. the manifest carries the Store version (0.43.1 → 1.0.4301.0), the
//      filex:// link, "Open with" and the startup task with the tray flag;
//   2. a protocol launch hands the link to the app as argv[1] — the same
//      shape the NSIS build's registry command gives it (classifyArgv);
//   3. the running app knows it is a Store copy: updates belong to the Store,
//      the login item to Windows (src/channel.ts);
//   4. AppData really is redirected to the package's LocalCache — the reason
//      the Explorer-visible folders moved to ~/.filex/desktop;
//   5. Windows registered the startup task, switched off.
//   6. a notification from inside the package is shown, and Windows files
//      it under the package (step 3b below).
//   7. none of this puts a filex window on screen: it runs on the desktop of
//      whoever cuts the release, and until 2026-09-29 the Store copy's
//      sign-in window sat in front of them for the whole run. The app is
//      started with --keep-windows-hidden (src/main.ts), the protocol launch
//      is frozen the moment it shows up and then stopped, and a watch over
//      the variant's windows runs from the install to the end. The
//      notification of 6 is the one thing the run still shows.
//
// ⚠ It installs an E2E VARIANT, never the real identity: its own package name,
// its own `filex-e2e://` scheme and its own userData name. On a developer
// machine the real filex is usually installed and running; the variant must
// not answer its sign-in links, take over its "Open with" entry, or read its
// account store (an MSIX package falls back to the real AppData for a file it
// has no private copy of — with the real name it would inherit the real
// accounts and start syncing the real folders). The variant is removed again
// in every outcome, and the real filex:// handler is compared before/after.
//
// Needs Windows with Developer Mode on (loose-file registration, no
// certificate) and `pnpm run dist:store` first (for dist/ and build/bin).
//
//   node scripts/store-e2e.mjs
//   node scripts/store-e2e.mjs --keep     # leave the variant installed to poke at
//
// ⚠ No OS-level input: the checks read processes, the registry and the
// renderer over the DevTools protocol.

import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import { DESKTOP, check, finish, sleep } from './lib/harness.mjs';
import { activationWaitFactor, describeWindow, softActivation, windowVerdicts } from './lib/store-activation.mjs';

if (process.platform !== 'win32') {
  // Not a skip: a Store package can only be installed on Windows, and a gate
  // that "passes" where it cannot run is a gate that never ran.
  console.error('store-e2e runs on Windows only.');
  process.exit(2);
}

const require = createRequire(import.meta.url);
const { storeVersion } = require('./appx-manifest.cjs');

const VARIANT = {
  name: 'filex-store-e2e',
  identity: 'BRFTeknoloji.filexStoreE2E',
  scheme: 'filex-e2e',
  displayName: 'filex Store e2e',
};
const KEEP = process.argv.includes('--keep');
const OUT = path.join(DESKTOP, 'release-e2e');
const pkg = JSON.parse(fs.readFileSync(path.join(DESKTOP, 'package.json'), 'utf8'));
const work = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-store-e2e-'));
const unpacked = path.join(work, 'unpacked');

/** Runs Windows PowerShell (the Appx cmdlets live there, not in pwsh). The
 *  script travels base64-encoded, so no quoting layer can mangle it.
 *  ⚠ windowsHide on every program this run starts (#197): started from a
 *  parent without a console of its own to share, a console program is
 *  otherwise given a visible window on the desktop of whoever cuts the
 *  release - the one thing step 7 says this run never does. */
function ps(script) {
  return execFileSync('powershell.exe', psArgs(script), {
    encoding: 'utf8',
    maxBuffer: 16 * 1024 * 1024,
    windowsHide: true,
  }).trim();
}
function psArgs(script) {
  // $ProgressPreference: Windows PowerShell otherwise writes its "Preparing
  // modules for first use" progress records to stderr as CLIXML.
  const enc = Buffer.from(`$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'\n${script}`, 'utf16le').toString('base64');
  return ['-NoProfile', '-NonInteractive', '-EncodedCommand', enc];
}
const q = (s) => `'${String(s).replace(/'/g, "''")}'`; // a PowerShell string literal

function realProtocolHandler() {
  return ps(`$k='HKCU:\\Software\\Classes\\filex\\shell\\open\\command'; if (Test-Path $k) { (Get-ItemProperty $k).'(default)' } else { '' }`);
}

function findMakeAppx() {
  const kits = 'C:\\Program Files (x86)\\Windows Kits\\10\\bin';
  const versions = fs.existsSync(kits)
    ? fs.readdirSync(kits).filter((d) => /^10\.\d/.test(d)).sort((a, b) => a.localeCompare(b, 'en', { numeric: true }))
    : [];
  for (const v of versions.reverse()) {
    const p = path.join(kits, v, 'x64', 'makeappx.exe');
    if (fs.existsSync(p)) return p;
  }
  throw new Error('makeappx.exe not found — install the Windows 10/11 SDK');
}

async function cdpEval(port, expression) {
  const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  const page = list.find((p) => p.type === 'page');
  if (!page) throw new Error('no page to attach to');
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve);
    ws.addEventListener('error', reject);
  });
  try {
    return await new Promise((resolve) => {
      ws.addEventListener('message', (ev) => {
        const m = JSON.parse(ev.data);
        if (m.id === 1) resolve(m.result?.result?.value);
      });
      ws.send(JSON.stringify({ id: 1, method: 'Runtime.evaluate', params: { expression, awaitPromise: true, returnByValue: true } }));
    });
  } finally {
    ws.close();
  }
}

/** How many toasts Action Center holds for `aumid` — the list Windows shows
 *  under the app's name (ToastNotificationManager.History, which Windows
 *  PowerShell reaches through WinRT; it answers for another app's AUMID too).
 *  ⚠ Counted, not matched by text: a condensed toast keeps no Content. The
 *  variant is installed fresh on every run, so whatever it holds is ours. */
function toastsInActionCenter(aumid) {
  try {
    const n = ps(`[void][Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime]
$h = [Windows.UI.Notifications.ToastNotificationManager]::History.GetHistory(${q(aumid)})
@($h | Where-Object { $_ }).Count`);
    return /^\d+$/.test(n) ? Number(n) : n;
  } catch (e) {
    return String(e?.message ?? e).split('\n')[0];
  }
}

let installLocation = '';
let pfn = '';

// The checks that need Windows to activate the package (see
// lib/store-activation.mjs for when a failure is only a warning).
const SOFT = softActivation(process.env);
const WAIT = activationWaitFactor(process.env);
const softFailed = [];
let activationFailed = false;
function softCheck(name, ok, detail, soft, why) {
  if (ok || !soft) return check(name, ok, detail);
  softFailed.push(name);
  console.log(`WARN  ${name}${detail ? `  — ${detail}` : ''}  (${why})`);
  return false;
}
function activationCheck(name, ok, detail = '') {
  if (!ok) activationFailed = true;
  return softCheck(name, ok, detail, SOFT, 'activation on a CI runner; not a Store upload');
}

/** What a failed activation leaves behind — printed so the next red run says why. */
function diagnoseActivation() {
  const tryPs = (label, script) => {
    try {
      console.log(`--- ${label}\n${ps(script) || '(nothing)'}`);
    } catch (e) {
      console.log(`--- ${label}\n(could not read: ${String(e?.message ?? e).split('\n')[0]})`);
    }
  };
  tryPs('session', `"interactive=$([Environment]::UserInteractive) session=$((Get-Process -Id $PID).SessionId) explorer=$((Get-Process explorer -ErrorAction SilentlyContinue | ForEach-Object SessionId) -join ',')"`);
  tryPs('filex.exe processes of the variant', `Get-CimInstance Win32_Process -Filter "Name='filex.exe'" | Where-Object { $_.ExecutablePath -like (${q(installLocation)} + '*') } | ForEach-Object { "$($_.ProcessId) $($_.CommandLine)" }`);
  tryPs('package status', `Get-AppxPackage -Name ${q(VARIANT.identity)} | Select-Object Status, IsDevelopmentMode, SignatureKind | Format-List | Out-String`);
  const logs = path.join(process.env.LOCALAPPDATA, 'Packages', pfn, 'LocalCache', 'Roaming', VARIANT.name, 'logs');
  tryPs('app log (last 40 lines)', `if (Test-Path ${q(logs)}) { Get-ChildItem ${q(logs)} -Filter *.log | Sort-Object LastWriteTime | Select-Object -Last 1 | ForEach-Object { Get-Content $_.FullName -Tail 40 } } else { 'no log directory at ' + ${q(logs)} }`);
}

function killVariant() {
  if (!installLocation) return;
  ps(`Get-CimInstance Win32_Process -Filter "Name='filex.exe'" | Where-Object { $_.ExecutablePath -like (${q(installLocation)} + '*') } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`);
}

/**
 * Watches every top-level window a process of the variant owns — from a
 * PowerShell of its own, every 100 ms, until stopped — and writes down each one
 * that is on screen (there must be none) and each titled one kept off it (the
 * app's own window, which must be there: a watch that sees no window of the
 * app at all has proved nothing). Only processes started from the variant's
 * install folder are looked at; the real filex, if it is running, is not.
 *
 * ⚠ It samples. A window shown and gone again inside one tick would slip
 * through; nothing in this run shows one that briefly.
 */
function watchVariantWindows() {
  const dir = path.join(work, 'window-watch');
  fs.mkdirSync(dir, { recursive: true });
  const stopFile = path.join(dir, 'stop');
  const logFile = path.join(dir, 'windows.txt');
  const script = `Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public static class FilexE2eWindows {
  delegate bool EnumProc(IntPtr hwnd, IntPtr lParam);
  [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc cb, IntPtr lParam);
  [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr hwnd);
  [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint pid);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetWindowText(IntPtr hwnd, StringBuilder text, int max);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetClassName(IntPtr hwnd, StringBuilder text, int max);
  [StructLayout(LayoutKind.Sequential)] struct Rect { public int L, T, R, B; }
  [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr hwnd, out Rect r);
  [DllImport("dwmapi.dll")] static extern int DwmGetWindowAttribute(IntPtr hwnd, int attr, out int value, int size);
  public static List<string> Of(uint[] pids) {
    var rows = new List<string>();
    EnumWindows(delegate (IntPtr hwnd, IntPtr lParam) {
      uint pid;
      GetWindowThreadProcessId(hwnd, out pid);
      if (Array.IndexOf(pids, pid) < 0) return true;
      var title = new StringBuilder(256);
      GetWindowText(hwnd, title, 256);
      var cls = new StringBuilder(256);
      GetClassName(hwnd, cls, 256);
      bool visible = IsWindowVisible(hwnd);
      Rect r;
      GetWindowRect(hwnd, out r);
      int cloaked = 0;
      DwmGetWindowAttribute(hwnd, 14, out cloaked, 4);
      if (visible || title.Length > 0) rows.Add((visible ? "visible" : "hidden") + "|" + hwnd.ToInt64() + "|" + pid + "|" + cls + "|" + (r.R - r.L) + "x" + (r.B - r.T) + "|" + cloaked + "|" + title);
      return true;
    }, IntPtr.Zero);
    return rows;
  }
}
'@
$root = ${q(installLocation)}
$log = ${q(logFile)}
$seen = @{}
Add-Content -LiteralPath $log -Value 'armed' -Encoding UTF8
while (-not (Test-Path -LiteralPath ${q(stopFile)})) {
  $pids = [uint32[]]@(Get-Process -Name filex -ErrorAction SilentlyContinue | Where-Object { try { $_.Path -like ($root + '*') } catch { $false } } | ForEach-Object { [uint32]$_.Id })
  if ($pids.Count) {
    foreach ($row in [FilexE2eWindows]::Of($pids)) {
      if (-not $seen.ContainsKey($row)) { $seen[$row] = 1; Add-Content -LiteralPath $log -Value ((Get-Date -Format 'HH:mm:ss.fff') + '|' + $row) -Encoding UTF8 }
    }
  }
  Start-Sleep -Milliseconds 100
}
Add-Content -LiteralPath $log -Value 'stopped' -Encoding UTF8`;
  const child = spawn('powershell.exe', psArgs(script), { stdio: ['ignore', 'ignore', 'pipe'], windowsHide: true });
  let stderr = '';
  child.stderr.on('data', (d) => {
    stderr += d;
  });
  const exited = new Promise((resolve) => child.on('exit', resolve));
  const lines = () => {
    try {
      return fs.readFileSync(logFile, 'utf8').replace(/^\uFEFF/, '').split(/\r?\n/).filter(Boolean);
    } catch {
      return [];
    }
  };
  return {
    /** Resolves once the watch is running (compiling the helper takes a moment). */
    async armed() {
      let gone = false;
      void exited.then(() => {
        gone = true;
      });
      for (let i = 0; i < 120 && !gone; i++) {
        if (lines()[0] === 'armed') return true;
        await sleep(250);
      }
      throw new Error(`the window watch did not start: ${stderr.trim().split('\n')[0] || 'no word from it'}`);
    },
    /** Stops the watch; what it saw, and whether it ran from start to end. */
    async stop() {
      fs.writeFileSync(stopFile, '');
      if ((await Promise.race([exited, sleep(15000).then(() => 'timeout')])) === 'timeout') child.kill();
      const all = lines();
      const rows = all.filter((l) => /^\d/.test(l)).map((l) => {
        const [at, kind, hwnd, pid, cls, size, cloaked, ...title] = l.split('|');
        const [w, h] = size.split('x').map(Number);
        // On screen = shown, with an area, and not cloaked by the compositor
        // (Windows keeps some helper windows "visible" at 0×0 or cloaked).
        const onScreen = kind === 'visible' && w > 0 && h > 0 && cloaked === '0';
        return { at, kind, onScreen, hwnd, pid, cls, size, cloaked, title: title.join('|') };
      });
      return {
        ran: all[0] === 'armed' && all.at(-1) === 'stopped',
        error: stderr.trim().split('\n')[0] ?? '',
        onScreen: rows.filter((r) => r.onScreen),
        hidden: rows.filter((r) => r.kind === 'hidden'),
        rows,
      };
    },
  };
}

const handlerBefore = realProtocolHandler();
let windowWatch = null;

try {
  if (ps(`(Get-ItemProperty 'HKLM:\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\AppModelUnlock' -ErrorAction SilentlyContinue).AllowDevelopmentWithoutDevLicense`) !== '1') {
    throw new Error('Developer Mode is off (Settings → System → For developers). It is what lets an unsigned package be registered.');
  }

  // ── build the variant ───────────────────────────────────────────────
  const ext = fs.readFileSync(path.join(DESKTOP, 'build', 'appx-extensions.xml'), 'utf8')
    .replace('<uap3:Protocol Name="filex"', `<uap3:Protocol Name="${VARIANT.scheme}"`);
  if (!ext.includes(`Name="${VARIANT.scheme}"`)) throw new Error('could not re-point the protocol in the e2e variant');
  const extFile = path.join(work, 'appx-extensions.e2e.xml');
  fs.writeFileSync(extFile, ext);
  fs.rmSync(OUT, { recursive: true, force: true });
  execFileSync(process.execPath, [
    require.resolve('electron-builder/cli.js'), '--win', 'appx',
    `-c.extraMetadata.name=${VARIANT.name}`,
    `-c.appx.identityName=${VARIANT.identity}`,
    `-c.appx.displayName=${VARIANT.displayName}`,
    `-c.appx.customExtensionsPath=${extFile}`,
    `-c.directories.output=${OUT}`,
  ], { cwd: DESKTOP, stdio: 'inherit', env: { ...process.env, CSC_IDENTITY_AUTO_DISCOVERY: 'false' }, windowsHide: true });
  const appx = fs.readdirSync(OUT).find((f) => f.endsWith('.appx'));
  if (!appx) throw new Error(`no .appx in ${OUT}`);

  // ── install it ──────────────────────────────────────────────────────
  ps(`Get-AppxPackage -Name ${q(VARIANT.identity)} | Remove-AppxPackage`);
  execFileSync(findMakeAppx(), ['unpack', '/p', path.join(OUT, appx), '/d', unpacked, '/o'], { stdio: 'ignore', windowsHide: true });
  ps(`Add-AppxPackage -Register ${q(path.join(unpacked, 'AppxManifest.xml'))}`);
  const info = JSON.parse(ps(`Get-AppxPackage -Name ${q(VARIANT.identity)} | Select-Object PackageFamilyName, InstallLocation, Version | ConvertTo-Json`));
  pfn = info.PackageFamilyName;
  installLocation = info.InstallLocation;

  // ── 7. no window on screen: watched from here on, checked at the end ─
  windowWatch = watchVariantWindows();
  await windowWatch.armed();

  // ── 1. manifest ─────────────────────────────────────────────────────
  const manifest = fs.readFileSync(path.join(unpacked, 'AppxManifest.xml'), 'utf8');
  check('package version is the Store mapping of package.json',
    info.Version === storeVersion(pkg.version), `${pkg.version} → ${info.Version}`);
  for (const cat of ['windows.protocol', 'windows.fileTypeAssociation', 'windows.startupTask']) {
    check(`manifest declares ${cat}`, manifest.includes(`Category="${cat}"`));
  }
  check('the startup task launches into the tray',
    /windows\.startupTask"[^>]*uap10:Parameters="--hidden"/.test(manifest));
  check('no electron-builder sample logo in the package',
    !fs.readdirSync(path.join(unpacked, 'assets')).some((f) => /^SampleAppx/.test(f)));

  // ── 2. a protocol launch, cold ──────────────────────────────────────
  // ⚠ Windows writes this command line, so this launch cannot be asked to keep
  // its window hidden — a switch added to the variant's protocol entry would
  // change the very line under test. What the check needs is the line, not the
  // app: ONE PowerShell opens the link (from a second thread: Start-Process
  // had still not returned 1.4 s in), spots the process as soon as it reports
  // a path in the variant's install folder (Get-Process; the real filex's
  // PIDs are set aside before the launch and never looked at), FREEZES it,
  // and only then reads its command line and stops it. Frozen, it cannot put
  // anything on screen, however long WMI takes to answer. Measured
  // 2026-09-29: one WMI lookup here takes ~300 ms; without the freeze the app
  // lived 1.5-2.5 s and had built its sign-in window (hidden, about to be
  // shown) before it was stopped; with it, the watch below saw no window of
  // that process at all. Before any of this it took a PowerShell per look
  // and another to stop it, and the window was on screen by then.
  const link = `${VARIANT.scheme}://ping?from=protocol`;
  const [waited, age, ...rest] = ps(`$root = ${q(installLocation)}
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class FilexE2eFreeze {
  [DllImport("ntdll.dll")] public static extern int NtSuspendProcess(IntPtr process);
}
'@
$null = Get-CimInstance Win32_Process -Filter "ProcessId=$PID"
$before = @(Get-Process -Name filex -ErrorAction SilentlyContinue | ForEach-Object { $_.Id })
$rs = [runspacefactory]::CreateRunspace()
$rs.ApartmentState = 'STA'
$rs.Open()
$launch = [PowerShell]::Create()
$launch.Runspace = $rs
$null = $launch.AddCommand('Start-Process').AddArgument(${q(link)})
$clock = [Diagnostics.Stopwatch]::StartNew()
$pending = $launch.BeginInvoke()
$line = ''
$age = -1
$waited = -1
while (-not $line -and $clock.Elapsed.TotalSeconds -lt ${40 * WAIT}) {
  foreach ($h in @(Get-Process -Name filex -ErrorAction SilentlyContinue | Where-Object { $before -notcontains $_.Id })) {
    $ours = try { $h.Path -like ($root + '*') } catch { $false }
    if (-not $ours) { continue }
    try { $null = [FilexE2eFreeze]::NtSuspendProcess($h.Handle) } catch { }
    $frozenAt = $clock.ElapsedMilliseconds
    $frozenAge = try { [int]((Get-Date) - $h.StartTime).TotalMilliseconds } catch { -1 }
    $p = Get-CimInstance Win32_Process -Filter "ProcessId=$($h.Id)"
    if ($p -and $p.CommandLine -like '*${VARIANT.scheme}://*') {
      $line = [string]$p.CommandLine
      $age = $frozenAge
      $waited = $frozenAt
      Stop-Process -Id $h.Id -Force -ErrorAction SilentlyContinue
      break
    }
  }
  if (-not $line) { Start-Sleep -Milliseconds 20 }
}
$null = $launch.EndInvoke($pending)
if ($launch.Streams.Error.Count) { throw $launch.Streams.Error[0] }
$rs.Close()
if (-not $line) { $waited = $clock.ElapsedMilliseconds }
[string]$waited + '|' + $age + '|' + $line`).split('|');
  const cmdline = rest.join('|');
  // Windows canonicalises the URI on the way (…//ping/?from=…): the sign-in
  // parser reads the host, so filex://auth/?… is the same link to it.
  activationCheck('a protocol launch passes the link as argv[1]',
    /filex\.exe"\s+"filex-e2e:\/\/ping\/?\?from=protocol"\s*$/.test(cmdline),
    cmdline
      ? `${cmdline.replace(/^.*filex\.exe"/, 'filex.exe')} (frozen ${age} ms after it started, ${waited} ms after the link was opened, then stopped)`
      : `no process of the variant carried the link in ${waited} ms`);
  killVariant();
  await sleep(1500);

  // ── 3. the app knows it is a Store copy ─────────────────────────────
  // --keep-windows-hidden: the app opens its sign-in window and loads the page
  // the checks below talk to, but never puts it on screen (src/main.ts).
  const port = 9300 + Math.floor(Math.random() * 90);
  ps(`Invoke-CommandInDesktopPackage -PackageFamilyName ${q(pfn)} -AppId 'filex' -Command ${q(path.join(installLocation, 'app', 'filex.exe'))} -Args '--remote-debugging-port=${port} --keep-windows-hidden'`);
  let state = null;
  for (let i = 0; i < 60 * WAIT && !state; i++) {
    await sleep(500);
    try {
      state = await cdpEval(port, '(window.filexShell ?? window.filexApp).getState()');
    } catch {
      /* not listening yet */
    }
  }
  activationCheck('the app came up inside the package', !!state);
  if (state) {
    check('updates belong to the Microsoft Store', state.updateChannel === 'msstore' && state.update?.status === 'store', JSON.stringify(state.update));
    check('the login item is left to Windows Settings', state.launchAtLoginInOsSettings === true);
    check('the app still reports ITS version, not the Store number', state.appVersion === pkg.version, state.appVersion);

    // ── 3b. a notification reaches Windows under the package ──────────
    // Inside a package Windows files a toast by PackageFamilyName!AppId, and
    // Electron has to create its notifier WITHOUT an explicit AUMID there; the
    // NSIS copy's toasts land under "electron.app.filex" instead (measured
    // 2026-09-26). A web Notification goes through the same presenter as the
    // main process's (Electron's Windows toast code), so one from the page
    // stands for the sync, drag-out and "Open with" toasts too.
    const shown = await cdpEval(port, `new Promise((r) => {
      try {
        const n = new Notification('filex Store e2e', { body: 'a toast from inside the package', silent: true });
        n.onshow = () => r('shown');
        n.onerror = () => r('error event');
        setTimeout(() => r('no show event in 15 s'), 15000);
      } catch (e) { r('threw: ' + e); }
    })`);
    activationCheck('a notification from inside the package is shown', shown === 'shown', shown);
    let held = 0;
    for (let i = 0; i < 20 * WAIT && !(held > 0); i++) {
      await sleep(500);
      held = toastsInActionCenter(`${pfn}!filex`);
    }
    activationCheck('Action Center holds it under the package, not under electron.app.filex',
      held > 0, `${pfn}!filex: ${held}`);
  }

  // ── 4. AppData is redirected ────────────────────────────────────────
  const localCache = path.join(process.env.LOCALAPPDATA, 'Packages', pfn, 'LocalCache', 'Roaming', VARIANT.name);
  check('userData lives in the package LocalCache', fs.existsSync(localCache), localCache);
  check('…and not in the real AppData, where Explorer looks',
    !fs.existsSync(path.join(process.env.APPDATA, VARIANT.name)));

  // ── 5. the startup task ─────────────────────────────────────────────
  // ⚠ Registered = the task's key exists. Its State value is NOT written at
  // registration: the key stays empty (= the manifest's Enabled="false") until
  // something enumerates startup apps — Settings, Task Manager, Windows' own
  // StartupAppTask — and then reads State=0. Windows 26200, 2026-09-26: two
  // runs of the same package, one empty, one State=0; the app never writes it
  // (the Store copy leaves the login item to Windows). Switched ON would be
  // State=2, and 1 is "turned off by the user".
  let taskState = 'missing';
  for (let i = 0; i < 20 * WAIT && taskState === 'missing'; i++) {
    if (i) await sleep(500);
    taskState = ps(`$k = 'HKCU:\\Software\\Classes\\Local Settings\\Software\\Microsoft\\Windows\\CurrentVersion\\AppModel\\SystemAppData\\' + ${q(pfn)} + '\\filexStartup'; if (Test-Path $k) { $s = (Get-ItemProperty $k).State; if ($null -eq $s) { 'unset' } else { "$s" } } else { 'missing' }`);
  }
  activationCheck('Windows registered the startup task, switched off',
    taskState === '0' || taskState === 'unset', `State=${taskState}`);
  if (activationFailed) diagnoseActivation();
} catch (e) {
  check('store-e2e ran to the end', false, String(e?.message ?? e));
} finally {
  // Stopped under --keep too: a copy running with its windows held off screen
  // is no copy to poke at. Start it again from the Start menu.
  killVariant();
  await sleep(1000);
  if (windowWatch) {
    const seen = await windowWatch.stop();
    // Both checks guard a person's screen: on CI they only warn (see
    // lib/store-activation.mjs → windowVerdicts); here they fail.
    for (const v of windowVerdicts(seen, process.env)) {
      softCheck(v.name, v.ok, v.detail, v.soft, "a CI runner's screen is nobody's");
    }
    for (const r of seen.rows) console.log(`      seen: ${describeWindow(r)}`);
  }
  if (!KEEP) {
    try {
      ps(`Get-AppxPackage -Name ${q(VARIANT.identity)} | Remove-AppxPackage`);
    } catch (e) {
      check('the e2e variant was removed', false, String(e?.message ?? e));
    }
    fs.rmSync(work, { recursive: true, force: true });
  } else {
    console.log(`\n--keep: ${VARIANT.identity} stays installed from ${unpacked}`);
  }
  check('the real filex:// handler is untouched', realProtocolHandler() === handlerBefore, handlerBefore || '(none)');
}
if (softFailed.length) {
  // GitHub turns this line into an annotation on the run.
  console.log(`::warning title=Store package::${softFailed.length} check(s) only warned on this runner (${softFailed.join('; ')}): activation checks do while nothing goes to the Store, the no-window checks always do on CI. Run \`pnpm e2e:store\` on a Windows desktop before submitting.`);
}
finish();
