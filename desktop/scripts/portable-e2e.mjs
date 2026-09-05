// Does the PORTABLE build actually work, and is it actually portable?
//
// Three questions, and none of them is answered by the build log.
// electron-builder happily reports "building target=portable file=…" for a
// package that opens to nothing — update-e2e.mjs exists because that happened:
// a missing node_modules entry made the main process throw before it created a
// window, and the build said nothing. And "portable" is a claim about WHERE
// FILES GO, which no packaging step checks at all.
//
//   1. the artifact is named so it neither overwrites the installer nor
//      carries a version (the download links resolve one fixed filename);
//   2. the real .exe starts, loads a page, and puts its data next to itself;
//   3. the app knows it is portable — where its files went, and that it must
//      not pretend to update itself.
//
// (2) drives the actual self-extracting .exe, because that stub is the part
// that hands the app PORTABLE_EXECUTABLE_DIR and it cannot be simulated. The
// stub is not Electron, so Playwright cannot attach to it: the evidence is the
// filesystem plus a process that is still alive. (3) and (4) drive
// release/win-unpacked with the same variable the stub sets, which is where the
// app's own view can be read.
//
// Run (needs `pnpm run dist:win` first):
//   node scripts/portable-e2e.mjs
//   FILEX_EMAIL=… FILEX_PASSWORD=… node scripts/portable-e2e.mjs   # also renders Settings

import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { DESKTOP, check, finish, launchApp, signIn, sleep } from './lib/harness.mjs';

const RELEASE = path.join(DESKTOP, 'release');
const ARCH = process.arch;
const PORTABLE_EXE = path.join(RELEASE, `filex-desktop-portable-${ARCH}.exe`);
const INSTALLER_EXE = path.join(RELEASE, `filex-desktop-${ARCH}.exe`);

// ── 1. the artifact's name (source guards — no app needed) ───────────
{
  const yml = fs.readFileSync(path.join(DESKTOP, 'electron-builder.yml'), 'utf8');
  const main = fs.readFileSync(path.join(DESKTOP, 'src', 'main.ts'), 'utf8');

  check(
    'the Windows build produces a portable target at all',
    /^win:\s*$[\s\S]*?^\s+-\s*portable\s*$/m.test(yml),
    'win.target must list `portable` — Windows was the only platform with no way to run filex without an installer',
  );

  const shared = /^artifactName:\s*(\S+)/m.exec(yml)?.[1] ?? '';
  const own = /^portable:\s*$[\s\S]*?^\s+artifactName:\s*(\S+)/m.exec(yml)?.[1] ?? '';
  // ⚠⚠ nsis and portable BOTH emit `.exe`. Under one shared template they are
  // the same filename, written twice, and whichever target ran last wins.
  check(
    '…under a name of its own, not the shared template',
    own !== '' && own !== shared,
    `shared=${shared} portable=${own || 'MISSING'}`,
  );
  // ⚠ The web app links to releases/latest/download/<name>, which GitHub only
  // resolves for a fixed filename.
  check('…carrying no version, like every other artifact', !own.includes('${version}'), own);

  // The app builds this filename itself to link a manual download, because the
  // Windows feed does NOT list it — measured: electron-builder writes only the
  // installer into latest.yml, since that is the artifact its updater would
  // apply. A rename has to break here rather than in somebody's browser.
  const literal = /const PORTABLE_ARTIFACT = `([^`]+)`/.exec(main)?.[1] ?? '';
  const rendered = literal.replace('${process.arch}', ARCH);
  const fromTemplate = own
    .replace('${productName}', /^productName:\s*(\S+)/m.exec(yml)?.[1] ?? '')
    .replace('${arch}', ARCH)
    .replace('${ext}', 'exe');
  check(
    '…and the app links to exactly the file the build produces',
    rendered !== '' && rendered === fromTemplate,
    `main.ts=${rendered || 'MISSING'} builder=${fromTemplate}`,
  );

  // ⚠⚠ Nothing may be downloaded that can never be applied. A portable .exe is
  // not an installation: no install directory to differential-patch, no
  // Squirrel to hand the running copy over to.
  check(
    'the updater is never wired on a build that cannot swap itself',
    main.includes('if (manualUpdates) {') &&
      main.indexOf('if (manualUpdates) {') < main.indexOf('autoUpdater.autoDownload = true'),
    'wireAutoUpdate must return before touching autoUpdater when manualUpdates is set',
  );
}

if (process.platform !== 'win32') {
  console.log('\nThe portable target is Windows-only — the rest of this suite needs Windows.');
  finish();
}

if (!fs.existsSync(PORTABLE_EXE)) {
  console.log(`\n${PORTABLE_EXE} yok — build it first:  pnpm run dist:win`);
  process.exit(2);
}

check(
  'the portable artifact did not overwrite the installer',
  fs.existsSync(INSTALLER_EXE) && fs.statSync(PORTABLE_EXE).size !== fs.statSync(INSTALLER_EXE).size,
  `${path.basename(PORTABLE_EXE)} ${(fs.statSync(PORTABLE_EXE).size / 1048576).toFixed(0)} MB · ` +
    `${path.basename(INSTALLER_EXE)} ${
      fs.existsSync(INSTALLER_EXE) ? `${(fs.statSync(INSTALLER_EXE).size / 1048576).toFixed(0)} MB` : 'MISSING'
    }`,
);

// ── 2. the real .exe: does it run, and where do its files land? ──────
{
  const stick = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-stick-'));
  const exe = path.join(stick, 'filex.exe');
  fs.copyFileSync(PORTABLE_EXE, exe);

  const stub = spawn(exe, [], { stdio: 'ignore' });
  // ⚠ Killed by PID, and only this one. `taskkill /IM filex.exe` would take
  // down everything else on the machine with that name — the operator's own
  // app, and any other agent's run. /T because the stub is the parent of the
  // real Electron process, which is where the windows are.
  const kill = () => {
    try {
      execFileSync('taskkill', ['/PID', String(stub.pid), '/T', '/F'], { stdio: 'ignore' });
    } catch {
      /* already gone */
    }
  };
  process.on('exit', kill);

  const dataDir = path.join(stick, 'filex-data');
  // A renderer writing localStorage is the cheapest strong signal that this is
  // more than a running process: a page from the app's own origin loaded.
  const proofOfLife = path.join(dataDir, 'Local Storage', 'leveldb', 'CURRENT');
  const deadline = Date.now() + 90_000;
  while (Date.now() < deadline && !fs.existsSync(proofOfLife)) await sleep(500);

  check(
    'the portable .exe starts and a page actually loads',
    fs.existsSync(proofOfLife),
    fs.existsSync(proofOfLife)
      ? ''
      : fs.existsSync(dataDir)
        ? `only got: ${fs.readdirSync(dataDir).join(', ') || '(empty)'}`
        : 'no data directory appeared at all — the app opened to nothing',
  );
  check(
    '…keeping its files in ONE folder beside the .exe',
    fs.existsSync(dataDir) && fs.readdirSync(stick).sort().join(',') === 'filex-data,filex.exe',
    fs.readdirSync(stick).sort().join(',') === 'filex-data,filex.exe' ? '' : fs.readdirSync(stick).join(', '),
  );

  kill();
  await sleep(2000);
  fs.rmSync(stick, { recursive: true, force: true });
}

// ── 3. the app's own view of being portable ──────────────────────────
{
  const stick = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-stick-'));
  // Exactly what templates/nsis/portable.nsi sets before it runs the app.
  const { app } = await launchApp({
    packagedOverride: path.join(RELEASE, 'win-unpacked', 'filex.exe'),
    env: { PORTABLE_EXECUTABLE_DIR: stick },
  });
  const win = await app.firstWindow();
  await win.waitForLoadState('domcontentloaded');
  const state = await win.evaluate(() => window.filexShell.getState());

  const expected = path.join(stick, 'filex-data');
  check(
    'the app reports its data directory as the folder beside the .exe',
    state.portable?.dataDir === expected,
    `${state.portable?.dataDir} (wanted ${expected})`,
  );
  check('…and knows it managed to put it there', state.portable?.besideExe === true, String(state.portable?.besideExe));
  check('the update UI is told this copy cannot update itself', state.updateManualOnly === true, String(state.updateManualOnly));
  check(
    '…and WHY, so Settings says the portable thing rather than the macOS one',
    state.updateManualReason === 'windows-portable',
    String(state.updateManualReason),
  );

  await app.close();
  fs.rmSync(stick, { recursive: true, force: true });
}

// ── 4. what Settings actually shows (needs a server to sign in to) ───
if (!process.env.FILEX_EMAIL || !process.env.FILEX_PASSWORD) {
  console.log('\n(FILEX_EMAIL/FILEX_PASSWORD yok — Settings rendering atlandı)');
  finish();
}

{
  const stick = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-stick-'));
  const { app } = await launchApp({
    packagedOverride: path.join(RELEASE, 'win-unpacked', 'filex.exe'),
    env: { PORTABLE_EXECUTABLE_DIR: stick },
  });
  const { win } = await signIn(app, { label: 'filex desktop — portable e2e' });

  // ⚠ The log is what proves the redirection happened EARLY enough: log.ts
  // resolves its path on the first line written and caches it forever, so a
  // setPath that ran a moment too late shows up here and nowhere else.
  const logPath = await win.evaluate(() => window.filexApp.logPath());
  check('the log follows the data directory too', logPath.startsWith(path.join(stick, 'filex-data')), logPath);

  // The gear is the last button on the rail — the same handle every other
  // suite in this folder uses.
  await win.evaluate(() => [...document.querySelectorAll('.rail-btn')].pop()?.click());
  await sleep(1500);
  const text = await win.evaluate(() => document.querySelector('#settings')?.innerText ?? '');

  check('Settings shows where this copy keeps its files', text.includes(path.join(stick, 'filex-data')), text.slice(0, 160));
  check(
    '…and says deleting that folder leaves nothing behind',
    /Delete that folder and nothing of yours is left/.test(text),
    'set.portable_sub',
  );
  // ⚠ The failure this replaces: a card that promises a silent self-update, or
  // one stuck at "Checking…" forever because the updater it waits on was never
  // wired.
  check(
    '…and the update card does not promise a self-update it cannot do',
    /does not update itself/.test(text) && !/updates itself in the background/.test(text),
    text
      .split('\n')
      .filter((l) => /update/i.test(l))
      .join(' | ')
      .slice(0, 240),
  );
  check('…nor sits at "Checking…" waiting for an updater that was never wired', !/Checking…/.test(text), 'upd.checking');

  await app.close();
  fs.rmSync(stick, { recursive: true, force: true });
}

finish();
