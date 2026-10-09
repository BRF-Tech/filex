// Every release checks that an installed copy of the previous release updates
// to it: on Windows (x64 and arm64), and on Linux (.deb, AppImage, snap; x64
// and arm64), on the machines desktop-arm64-check already installs on.
//
// ⚠ Why (0.55, #68): the desktop packages moved from electron-builder 24 to 26
// and the snap from core20 to core24, and nothing tested an update. A fresh
// install never runs what an update runs:
//
//   - Windows: the new NSIS installer finds the old copy through the uninstall
//     key the old one wrote (a GUID derived from the appId), runs the OLD
//     uninstaller with --updated and /KEEP_APP_DATA, kills what still runs
//     from the install folder, and starts the app again (--force-run). A
//     different key or folder leaves two copies; a wrong uninstall step takes
//     the user's sign-in with it.
//   - .deb: the new package's scripts run over the old package's files (26's
//     own after-install would have taken the setuid bit off chrome-sandbox;
//     desktop/build/linux/after-install.tpl keeps 24's).
//   - snap: snapd copies $SNAP_USER_DATA to the new revision, here onto
//     another base; the app must still find its data there.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts): read from `<repo>/.github/workflows` or
// FILEX_WORKFLOWS_DIR, and skipped when neither exists. The change reaches the
// public checkout as packaging/ci/release-desktop-upgrade.patch.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '../../..');
const DIR = [process.env.FILEX_WORKFLOWS_DIR, path.join(REPO, '.github', 'workflows')].find(
  (d) => d && fs.existsSync(path.join(d, 'release.yml')),
);

/** A file's text without comment-only lines, LF. */
const code = (rel: string) =>
  fs
    .readFileSync(path.join(DIR!, rel), 'utf8')
    .split(/\r?\n/)
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

/** The text of one step, from its `- name:` to the next step or job. */
function step(text: string, name: string): string {
  const at = text.indexOf(`- name: ${name}`);
  expect(at, `step "${name}"`).toBeGreaterThan(0);
  const rest = text.slice(at + 1);
  const next = rest.search(/\n\s+- (name|uses):|\n {2}[A-Za-z0-9_-]+:\s*\n/);
  return next < 0 ? rest : rest.slice(0, next);
}

/** The `if:` of a step's text, or ''. */
const cond = (stepText: string) => /\n\s+if:\s*(.+)/.exec(stepText)?.[1].trim() ?? '';

/** The lines of one job, up to the next job. */
function job(text: string, name: string): string {
  const lines = text.split('\n');
  const start = lines.findIndex((l) => l === `  ${name}:`);
  expect(start, `job "${name}"`).toBeGreaterThanOrEqual(0);
  const next = lines.findIndex((l, i) => i > start && /^ {2}[A-Za-z0-9_-]+:\s*$/.test(l));
  return lines.slice(start, next < 0 ? undefined : next).join('\n');
}

/**
 * Runs `body` (the body of an async function with the helper module as `m`)
 * in a plain Node process and returns what it returns, as JSON. The helpers
 * live in the public checkout, outside this project, where vite's module
 * server will not load from.
 */
function inNode<T>(script: string, body: string): T {
  const url = pathToFileURL(path.join(DIR!, 'scripts', script)).href;
  const src = `const m = await import(${JSON.stringify(url)}); const r = await (async () => { ${body} })(); process.stdout.write(JSON.stringify(r));`;
  const r = spawnSync(process.execPath, ['--input-type=module', '-e', src], { encoding: 'utf8', windowsHide: true });
  if (r.status !== 0) throw new Error(`${script}: ${r.stderr}`);
  return JSON.parse(r.stdout) as T;
}

const STEPS = {
  fetch: "The previous release's desktop packages",
  linux: 'Update from the previous release on Linux',
  windows: 'Update from the previous release on Windows, x64 and arm64',
  freshLinux: '.deb, AppImage and snap on Linux, sandboxed or confined',
  freshWindows: 'NSIS installer and portable .exe on Windows arm64',
};

describe('an installed copy of the previous release updates to this one', () => {
  it.runIf(!!DIR)('updates from the previous release on every check machine, before the fresh installs', () => {
    const release = code('release.yml');
    const check = job(release, 'desktop-arm64-check');
    // The update runs on a clean machine and leaves one: the fresh installs
    // after it are still fresh.
    const at = [STEPS.fetch, STEPS.linux, STEPS.windows, STEPS.freshLinux, STEPS.freshWindows].map((n) => check.indexOf(`- name: ${n}`));
    for (const [i, a] of at.entries()) expect(a, Object.values(STEPS)[i]).toBeGreaterThan(0);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    expect(cond(step(check, STEPS.fetch))).toBe('');
    expect(cond(step(check, STEPS.linux))).toBe("startsWith(matrix.label, 'linux')");
    expect(cond(step(check, STEPS.windows))).toBe("matrix.label == 'windows'");
    // Neither may run forever on a hung installer.
    for (const n of [STEPS.linux, STEPS.windows]) expect(step(check, n)).toMatch(/\n\s+timeout-minutes: \d+/);
  });

  it.runIf(!!DIR)("fetches the newest release below this build's version, with the packages of the machine's row", () => {
    const fetch = step(job(code('release.yml'), 'desktop-arm64-check'), STEPS.fetch);
    expect(fetch).toMatch(/VER: \$\{\{ needs\.plan\.outputs\.version \}\}/);
    expect(fetch).toMatch(/node "\$CI_SCRIPTS"\/previous-release\.mjs --repo "\$GITHUB_REPOSITORY" --below "\$VER" --out prev/);
    expect(fetch).toContain('linux-arm64) assets=(filex-desktop-arm64.deb filex-desktop-arm64.AppImage filex-desktop-arm64.snap) ;;');
    expect(fetch).toContain('linux-x64) assets=(filex-desktop-amd64.deb filex-desktop-x86_64.AppImage filex-desktop-amd64.snap) ;;');
    expect(fetch).toContain('windows) assets=(filex-desktop-x64.exe filex-desktop-arm64.exe) ;;');
  });

  it.runIf(!!DIR)('updates the x64 and the arm64 Windows copy, and the check machine gets the x64 installer', () => {
    const release = code('release.yml');
    const win = step(job(release, 'desktop-arm64-check'), STEPS.windows);
    expect(win).toContain("foreach ($arch in @('x64', 'arm64'))");
    expect(win).toMatch(/desktop-upgrade-windows\.ps1" -Old \(Resolve-Path \$old\)\.Path -New \(Resolve-Path \$new\)\.Path/);
    expect(win).toMatch(/if \(\$LASTEXITCODE -ne 0\) \{ throw/);
    // A run of everything must have built both installers; only a partial
    // run may lack one.
    expect(win).toMatch(/if \(\$env:FULL -eq 'true'\) \{ throw "this build has no \$new" \}/);
    const keep = step(job(release, 'desktop'), 'Keep the arm64 packages for the check on arm64 machines');
    expect(keep).toContain('desktop/release/filex-desktop-x64.exe');
    const linux = step(job(release, 'desktop-arm64-check'), STEPS.linux);
    expect(linux).toMatch(/bash "\$CI_SCRIPTS"\/desktop-upgrade-linux\.sh --old prev --new pkg/);
    expect(linux).toContain('linux-arm64) DEB=arm64; IMG=arm64; SNAP=arm64 ;;');
  });

  it.runIf(!!DIR)("runs this build's installer over the old copy as the app's updater does, and wants one copy of this version with the user's data", () => {
    const ps = code('scripts/desktop-upgrade-windows.ps1');
    // The previous release as a person installs it, this one as
    // electron-updater's NsisUpdater runs it for quitAndInstall(true, true).
    const first = ps.indexOf("Invoke-Installer $Old @('/S', '/currentuser')");
    const update = ps.indexOf("Invoke-Installer $New @('--updated', '/S', '--force-run')");
    expect(first).toBeGreaterThan(0);
    expect(update).toBeGreaterThan(first);
    const main = fs.readFileSync(path.join(REPO, 'desktop', 'src', 'main.ts'), 'utf8');
    expect(main, 'the app updates silently and comes back (isSilent, isForceRunAfter)').toMatch(/autoUpdater\.quitAndInstall\(true, true\)/);
    // Start-Process -Wait waits for the whole tree, and --force-run starts the app.
    expect(ps).not.toMatch(/Start-Process[^\n]*-Wait/);
    expect(ps).toMatch(/\$p\.WaitForExit\(\d+\)/);
    // One copy: every uninstall root, the same key, this version, nothing
    // under Program Files, filex.exe of this version.
    for (const root of [
      'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall',
      'HKLM:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall',
      'HKLM:\\Software\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall',
    ]) {
      expect(ps).toContain(`'${root}'`);
    }
    expect(ps).toMatch(/if \(\$after\.Count -ne 1\) \{ Fail/);
    expect(ps).toMatch(/if \(\$after\[0\]\.Key -ne \$key -or \$after\[0\]\.Root -ne \$root\) \{ Fail/);
    expect(ps).toMatch(/if \(\$after\[0\]\.DisplayVersion -ne \$newVersion\) \{ Fail/);
    expect(ps).toMatch(/Test-Path \(Join-Path \$pf 'filex'\)/);
    expect(ps).toMatch(/\(Get-Item \$App\)\.VersionInfo\.FileVersion/);
    // The user's data: a marker written after the old app ran, still there
    // after the update and after the new app ran.
    expect(ps).toContain("$UserData = Join-Path $env:APPDATA '@brftech\\filex-desktop'");
    const marker = ps.indexOf('Set-Content -Path $Marker -Value $token');
    expect(marker).toBeGreaterThan(first);
    expect(marker).toBeLessThan(update);
    expect(ps.slice(update)).toMatch(/if \(\(Get-Content \$Marker -Raw\) -ne \$token\) \{ Fail/);
    // And the machine is left clean for the fresh install after it.
    expect(ps.slice(update)).toMatch(/Invoke-Installer \$uninstallers\[0\]\.FullName @\('\/S', '\/currentuser'\)/);
  });

  it.runIf(!!DIR)('updates the .deb as the updater does and keeps its setuid sandbox, replaces the AppImage in place, refreshes the snap with its data', () => {
    const sh = code('scripts/desktop-upgrade-linux.sh');
    const at = (s: string) => {
      const i = sh.indexOf(s);
      expect(i, s).toBeGreaterThan(0);
      return i;
    };
    // .deb: the old one as a person installs it, then DebUpdater's dpkg -i.
    expect(at('sudo apt-get install -y "$olddeb"')).toBeLessThan(at('sudo dpkg -i "$newdeb"'));
    expect(sh).toContain('sudo apt-get install -f -y');
    expect(sh).toContain(`[ "$(count "$installed")" = 1 ] || fail`);
    expect(sh).toContain('[ -u /opt/filex/chrome-sandbox ]');
    expect(sh).toContain('[ ! -e /etc/apparmor.d/filex-app ] || fail');
    expect(sh).toContain('conf="$HOME/.config/@brftech/filex-desktop"');
    const opens = [...sh.matchAll(/^\s*look --exe .*$/gm)].map((m) => m[0].trim());
    expect(opens.filter((o) => o.startsWith('look --exe /usr/bin/filex-app')).every((o) => o.endsWith('--expect-sandbox'))).toBe(true);
    expect(opens.filter((o) => o.startsWith('look --exe /snap/bin/filex-app')).every((o) => o.endsWith('--expect-snap-confinement'))).toBe(true);
    expect(opens).toHaveLength(4);
    expect(at('printf \'%s\' "$token" > "$conf/$MARK"')).toBeLessThan(at('sudo dpkg -i "$newdeb"'));
    // AppImage: AppImageUpdater moves the new file over the old one.
    expect(sh).toContain('mv -f "$target.new" "$target"');
    expect(sh).toMatch(/X-AppImage-Version=/);
    // snap: old, then new, as a new revision; both markers carried over, and
    // the updated app keeps its data where the previous revision did.
    expect(at('sudo snap install --dangerous "$oldsnap"')).toBeLessThan(at('sudo snap install --dangerous "$newsnap"'));
    expect(sh).toContain('[ "$rev2" != "$rev1" ] || fail');
    expect(sh).toContain('[ "$(cat "$sdir/$rev2/$rel/$MARK" 2>/dev/null)" = "$token" ] || fail');
    expect(sh).toContain('[ "$(cat "$sdir/common/$MARK" 2>/dev/null)" = "$token" ] || fail');
    expect(sh).toContain('[ "$now" = "$sdir/$rev2/$rel" ] || fail');
    // Clean for the fresh installs after it; never --no-sandbox from here.
    expect(sh).toContain('sudo apt-get purge -y filex-app');
    expect(sh).toContain('sudo snap remove --purge filex-app');
    expect(sh).not.toContain('--no-sandbox');
  });

  it.runIf(!!DIR)('previous-release.mjs picks the newest published release below a version', () => {
    const releases = [
      { tag_name: 'v0.55.0', draft: false, prerelease: false },
      { tag_name: 'v0.54.1-rc.1', draft: false, prerelease: true },
      { tag_name: 'v0.54.3', draft: true, prerelease: false },
      { tag_name: 'v0.54.0', draft: false, prerelease: false },
      { tag_name: 'backend/v0.54.0', draft: false, prerelease: false },
      { tag_name: 'v0.53.10', draft: false, prerelease: false },
      { tag_name: 'v0.53.2', draft: false, prerelease: false },
    ];
    const pick = (below: string) =>
      inNode<string | null>('previous-release.mjs', `const r = m.previousRelease(${JSON.stringify(releases)}, ${JSON.stringify(below)}); return r ? r.tag_name : null;`);
    // A tag run: its own Release exists already.
    expect(pick('0.55.0')).toBe('v0.54.0');
    // A dry run still on the last release's version updates from the one before.
    expect(pick('0.54.0')).toBe('v0.53.10');
    expect(pick('0.55.0-rc.1')).toBe('v0.54.0');
    expect(pick('0.1.0')).toBeNull();
    const order = inNode<number[]>(
      'previous-release.mjs',
      `return [['0.53.10', '0.53.2'], ['0.55.0-rc.1', '0.55.0'], ['0.55.0-rc.2', '0.55.0-rc.10'], ['v1.0.0', '0.99.99'], ['0.54.0', 'v0.54.0']].map(([a, b]) => Math.sign(m.compareVersions(a, b)));`,
    );
    expect(order).toEqual([1, -1, -1, 1, 0]);
  });
});
