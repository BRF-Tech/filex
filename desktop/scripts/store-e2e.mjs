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

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import { DESKTOP, check, finish, sleep } from './lib/harness.mjs';
import { activationWaitFactor, softActivation } from './lib/store-activation.mjs';

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
 *  script travels base64-encoded, so no quoting layer can mangle it. */
function ps(script) {
  // $ProgressPreference: Windows PowerShell otherwise writes its "Preparing
  // modules for first use" progress records to stderr as CLIXML.
  const enc = Buffer.from(`$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'\n${script}`, 'utf16le').toString('base64');
  return execFileSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-EncodedCommand', enc], {
    encoding: 'utf8',
    maxBuffer: 16 * 1024 * 1024,
  }).trim();
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

let installLocation = '';
let pfn = '';

// The three checks that need Windows to activate the package (see
// lib/store-activation.mjs for when a failure is only a warning).
const SOFT = softActivation(process.env);
const WAIT = activationWaitFactor(process.env);
const softFailed = [];
let activationFailed = false;
function activationCheck(name, ok, detail = '') {
  if (!ok) activationFailed = true;
  if (ok || !SOFT) return check(name, ok, detail);
  softFailed.push(name);
  console.log(`WARN  ${name}${detail ? `  — ${detail}` : ''}  (activation on a CI runner; not a Store upload)`);
  return false;
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

const handlerBefore = realProtocolHandler();

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
  ], { cwd: DESKTOP, stdio: 'inherit', env: { ...process.env, CSC_IDENTITY_AUTO_DISCOVERY: 'false' } });
  const appx = fs.readdirSync(OUT).find((f) => f.endsWith('.appx'));
  if (!appx) throw new Error(`no .appx in ${OUT}`);

  // ── install it ──────────────────────────────────────────────────────
  ps(`Get-AppxPackage -Name ${q(VARIANT.identity)} | Remove-AppxPackage`);
  execFileSync(findMakeAppx(), ['unpack', '/p', path.join(OUT, appx), '/d', unpacked, '/o'], { stdio: 'ignore' });
  ps(`Add-AppxPackage -Register ${q(path.join(unpacked, 'AppxManifest.xml'))}`);
  const info = JSON.parse(ps(`Get-AppxPackage -Name ${q(VARIANT.identity)} | Select-Object PackageFamilyName, InstallLocation, Version | ConvertTo-Json`));
  pfn = info.PackageFamilyName;
  installLocation = info.InstallLocation;

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
  const link = `${VARIANT.scheme}://ping?from=protocol`;
  ps(`Start-Process ${q(link)}`);
  let cmdline = '';
  for (let i = 0; i < 40 * WAIT && !cmdline; i++) {
    await sleep(500);
    cmdline = ps(`Get-CimInstance Win32_Process -Filter "Name='filex.exe'" | Where-Object { $_.ExecutablePath -like (${q(installLocation)} + '*') -and $_.CommandLine -like '*${VARIANT.scheme}://*' } | Select-Object -First 1 -ExpandProperty CommandLine`);
  }
  // Windows canonicalises the URI on the way (…//ping/?from=…): the sign-in
  // parser reads the host, so filex://auth/?… is the same link to it.
  activationCheck('a protocol launch passes the link as argv[1]',
    /filex\.exe"\s+"filex-e2e:\/\/ping\/?\?from=protocol"\s*$/.test(cmdline), cmdline.replace(/^.*filex\.exe"/, 'filex.exe'));
  killVariant();
  await sleep(1500);

  // ── 3. the app knows it is a Store copy ─────────────────────────────
  const port = 9300 + Math.floor(Math.random() * 90);
  ps(`Invoke-CommandInDesktopPackage -PackageFamilyName ${q(pfn)} -AppId 'filex' -Command ${q(path.join(installLocation, 'app', 'filex.exe'))} -Args '--remote-debugging-port=${port}'`);
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
  }

  // ── 4. AppData is redirected ────────────────────────────────────────
  const localCache = path.join(process.env.LOCALAPPDATA, 'Packages', pfn, 'LocalCache', 'Roaming', VARIANT.name);
  check('userData lives in the package LocalCache', fs.existsSync(localCache), localCache);
  check('…and not in the real AppData, where Explorer looks',
    !fs.existsSync(path.join(process.env.APPDATA, VARIANT.name)));

  // ── 5. the startup task ─────────────────────────────────────────────
  const taskState = ps(`$k = 'HKCU:\\Software\\Classes\\Local Settings\\Software\\Microsoft\\Windows\\CurrentVersion\\AppModel\\SystemAppData\\' + ${q(pfn)} + '\\filexStartup'; if (Test-Path $k) { (Get-ItemProperty $k).State } else { 'missing' }`);
  activationCheck('Windows registered the startup task, switched off', taskState === '0', `State=${taskState}`);
  if (activationFailed) diagnoseActivation();
} catch (e) {
  check('store-e2e ran to the end', false, String(e?.message ?? e));
} finally {
  if (!KEEP) {
    killVariant();
    await sleep(1000);
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
  console.log(`::warning title=Store package::${softFailed.length} activation check(s) did not pass on this runner (${softFailed.join('; ')}). Not a Store upload, so the release goes on; run \`pnpm e2e:store\` on a Windows desktop before submitting.`);
}
finish();
