// `pnpm release X.Y.Z` stops at a red gate — proved on a throwaway repository.
//
// ⚠⚠ Why this is tested end to end and not only function by function: the
// point of the release command is ORDER and REFUSAL. A unit test can show that
// `closingKeywords` finds "Fixes #12"; only a run can show that the release
// does not get past `sign` while the public commit says it. Every red case
// below is one that shipped, or nearly shipped, a broken release:
//
//   a Helm chart left behind           23 releases (2026-08-29, lesson #52)
//   a dirty tree                       v0.38.1 shipped a half-finished page
//   an uncommitted workflow change     v0.43.1 (lesson #461)
//   a docs site serving a snapshot     2026-08-29 and 2026-09-25 (#511)
//   a public tag on a private commit   v0.26.0, v0.27.5 (lesson #55)
//   an unsigned / wrong-key tag        releases <= v0.27.5 were unsigned
//   "Fixes #N" in the export commit    v0.41.2 closed an issue (lesson #163)
//
// The fixture is a real git history: a private repository with its bare
// remote, a public checkout with its own, the real release engine
// (scripts/release.mjs + scripts/release/*) and the real version tooling
// (sync-deploy-versions, release-notes) — only the gates are the fixture's
// (web/tests/fixtures/release/plan.mjs), each one steered by an environment
// variable so exactly one can be made red. Tags are signed with a throwaway
// SSH key; nothing touches the network, no port is opened, and the user's own
// git configuration is shut out (GIT_CONFIG_GLOBAL, GIT_CONFIG_NOSYSTEM): the
// maintainer's global `tag.gpgSign` would otherwise pop a pinentry window.

import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');
const FIXTURE_FILES = path.join(REPO, 'web', 'tests', 'fixtures', 'release');
const VERSION = '0.2.0';
const TAG = `v${VERSION}`;
const TIMEOUT = 300_000;

/** The real files the release runs — copied, never reimplemented. */
const COPY = [
  'scripts/release.mjs',
  'scripts/release/engine.mjs',
  'scripts/release/checks.mjs',
  'scripts/release/stages.mjs',
  'scripts/release/verify.mjs',
  'scripts/release-notes.mjs',
  'scripts/sync-deploy-versions.mjs',
  'docs-site/.vitepress/github-slug.mjs',
  'deploy/helm/filex/Chart.yaml',
  'deploy/casaos/docker-compose.yml',
  'deploy/umbrel/filex/docker-compose.yml',
  'deploy/umbrel/filex/umbrel-app.yml',
  'deploy/runtipi/filex/docker-compose.json',
  'deploy/runtipi/filex/config.json',
];

const roots: string[] = [];
afterAll(() => {
  for (const r of roots) fs.rmSync(r, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
});

function findSshKeygen(): string | null {
  if (!spawnSync('ssh-keygen', ['-?'], { encoding: 'utf8' }).error) return 'ssh-keygen';
  if (process.platform === 'win32') {
    const execPath = spawnSync('git', ['--exec-path'], { encoding: 'utf8' }).stdout.trim();
    const p = path.resolve(execPath, '..', '..', '..', 'usr', 'bin', 'ssh-keygen.exe');
    if (fs.existsSync(p)) return p;
  }
  return null;
}
const SSH_KEYGEN = findSshKeygen();

interface Fixture {
  root: string;
  src: string;
  exp: string;
  site: string;
  privateRemote: string;
  publicRemote: string;
  env: NodeJS.ProcessEnv;
  fingerprint: string;
}

function isolatedEnv(root: string): NodeJS.ProcessEnv {
  const gitconfig = path.join(root, 'gitconfig');
  fs.writeFileSync(
    gitconfig,
    '[user]\n\tname = Release Test\n\temail = release@test.invalid\n[init]\n\tdefaultBranch = main\n' +
      '[core]\n\tautocrlf = false\n[commit]\n\tgpgSign = false\n[tag]\n\tgpgSign = false\n',
  );
  const env: NodeJS.ProcessEnv = { ...process.env };
  for (const k of Object.keys(env)) if (k.startsWith('GIT_') || k.startsWith('FIXTURE_')) delete env[k];
  return { ...env, GIT_CONFIG_GLOBAL: gitconfig, GIT_CONFIG_NOSYSTEM: '1', GIT_TERMINAL_PROMPT: '0', NO_COLOR: '1' };
}

function run(env: NodeJS.ProcessEnv, bin: string, args: string[], cwd?: string) {
  const r = spawnSync(bin, args, { cwd, env, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  if (r.status !== 0) throw new Error(`${bin} ${args.join(' ')} → ${r.status}\n${r.stdout}\n${r.stderr}`);
  return r.stdout.trim();
}

function write(file: string, text: string) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, text);
}

const CHANGELOG_010 =
  '# Changelog\n\nAll notable changes.\n\n## [Unreleased]\n\n## [0.1.0] - 2026-01-01\n\n### Added\n\n- **The first release.** It exists, and it does one thing.\n';

const FEATURE =
  '### Added\n\n- **Folders can be shared with a whole team at once.** Pick a team instead of\n' +
  '  adding people one by one; everybody who joins the team later gets the folder\n' +
  '  too, and everybody who leaves loses it, without anyone touching the share.\n\n';

const fwd = (p: string) => p.split(path.sep).join('/');

function makeFixture(): Fixture {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-release-test-'));
  roots.push(root);
  const env = isolatedEnv(root);
  const git = (dir: string, ...args: string[]) => run(env, 'git', ['-C', dir, ...args]);
  const privateRemote = fwd(path.join(root, 'private.git'));
  const publicRemote = fwd(path.join(root, 'public.git'));
  git(root, 'init', '-q', '--bare', privateRemote);
  git(root, 'init', '-q', '--bare', publicRemote);

  let fingerprint = '';
  const signing: string[][] = [];
  if (SSH_KEYGEN) {
    for (const k of ['key', 'other-key']) {
      run(env, SSH_KEYGEN, ['-q', '-t', 'ed25519', '-N', '', '-C', 'release@test.invalid', '-f', path.join(root, k)]);
    }
    fs.writeFileSync(
      path.join(root, 'allowed_signers'),
      ['key', 'other-key'].map((k) => `release@test.invalid ${fs.readFileSync(path.join(root, `${k}.pub`), 'utf8').trim()}`).join('\n') + '\n',
    );
    fingerprint = run(env, SSH_KEYGEN, ['-lf', path.join(root, 'key.pub')]).split(/\s+/)[1];
    signing.push(
      ['gpg.format', 'ssh'],
      ['user.signingKey', path.join(root, 'key').split(path.sep).join('/')],
      ['gpg.ssh.allowedSignersFile', path.join(root, 'allowed_signers').split(path.sep).join('/')],
      ['gpg.ssh.program', SSH_KEYGEN.split(path.sep).join('/')],
    );
  }

  // ── the private repository at v0.1.0 ──────────────────────────────────────
  const src = path.join(root, 'src');
  fs.mkdirSync(src);
  git(src, 'init', '-q');
  git(src, 'remote', 'add', 'origin', privateRemote);
  for (const [k, v] of signing) git(src, 'config', k, v);
  for (const f of COPY) write(path.join(src, f), fs.readFileSync(path.join(REPO, f), 'utf8'));
  write(path.join(src, 'scripts/release/plan.mjs'), fs.readFileSync(path.join(FIXTURE_FILES, 'plan.mjs'), 'utf8'));
  write(path.join(src, 'scripts/fake-export.mjs'), fs.readFileSync(path.join(FIXTURE_FILES, 'fake-export.mjs'), 'utf8'));
  write(path.join(src, 'scripts/export-public.sh'), '#!/usr/bin/env bash\nexec node "$(dirname "$0")/fake-export.mjs" "$@"\n');
  write(path.join(src, 'package.json'), '{\n  "name": "fixture",\n  "private": true,\n  "version": "0.0.0"\n}\n');
  write(path.join(src, 'pnpm-workspace.yaml'), 'packages:\n  - "packages/*"\n  - "web"\n');
  write(path.join(src, 'web/package.json'), '{\n  "name": "web",\n  "private": true,\n  "version": "0.1.0"\n}\n');
  write(path.join(src, 'packages/core/package.json'), '{\n  "name": "@fixture/core",\n  "version": "0.1.0",\n  "license": "MIT"\n}\n');
  write(path.join(src, 'CHANGELOG.md'), CHANGELOG_010);
  write(path.join(src, 'README.md'), '# Fixture\n\n![The explorer](docs/shot.png)\n\nReport security problems to security@brf.sh.\n');
  write(path.join(src, 'docs/shot.png'), 'not really a png');
  write(path.join(src, 'docs/GUIDE.md'), '# Guide\n\n## Getting started\n\nOpen it.\n');
  run(env, process.execPath, [path.join(src, 'scripts/sync-deploy-versions.mjs')], src);
  git(src, 'add', '-A');
  git(src, 'commit', '-q', '-m', 'v0.1.0');
  git(src, 'tag', '-a', 'v0.1.0', '-m', 'v0.1.0');

  // ── the public checkout at v0.1.0: its workflows + the export ─────────────
  const exp = path.join(root, 'exp');
  fs.mkdirSync(exp);
  git(exp, 'init', '-q');
  git(exp, 'remote', 'add', 'origin', publicRemote);
  for (const [k, v] of signing) git(exp, 'config', k, v);
  write(path.join(exp, '.github/workflows/release.yml'), 'name: Release\non: push\njobs: {}\n');
  write(path.join(exp, '.github/workflows/ci.yml'), 'name: CI\non: push\njobs: {}\n');
  git(exp, 'add', '-A');
  git(exp, 'commit', '-q', '-m', 'workflows');
  run(env, process.execPath, [path.join(src, 'scripts/fake-export.mjs'), exp]);
  git(exp, 'commit', '-q', '-m', 'v0.1.0 - the first release');
  git(exp, 'tag', '-a', 'v0.1.0', '-m', 'v0.1.0');
  git(exp, 'push', '-q', 'origin', 'main', 'refs/tags/v0.1.0');

  // ── the work of the next release ──────────────────────────────────────────
  write(path.join(src, 'CHANGELOG.md'), CHANGELOG_010.replace('## [Unreleased]\n\n', `## [Unreleased]\n\n${FEATURE}`));
  write(path.join(src, 'docs/GUIDE.md'), '# Guide\n\n## Getting started\n\nOpen it.\n\n## Sharing a folder with a team\n\nPick the team.\n');
  git(src, 'add', '-A');
  git(src, 'commit', '-q', '-m', 'feat: share a folder with a team');
  git(src, 'push', '-q', 'origin', 'main', 'refs/tags/v0.1.0');

  const site = path.join(root, 'site');
  fs.mkdirSync(site);
  return { root, src, exp, site, privateRemote, publicRemote, env, fingerprint };
}

// Built once; every test works on its own copy, so no test sees another's
// commits, tags or state — and a copy costs a directory copy, not forty git
// processes (which on Windows is most of this file's run time).
let template: Fixture;
beforeAll(() => {
  template = makeFixture();
}, TIMEOUT);

function fixture(): Fixture {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-release-test-'));
  roots.push(root);
  fs.cpSync(template.root, root, { recursive: true });
  for (const repo of ['src', 'exp']) {
    const cfg = path.join(root, repo, '.git', 'config');
    fs.writeFileSync(cfg, fs.readFileSync(cfg, 'utf8').split(fwd(template.root)).join(fwd(root)));
  }
  const at = (p: string) => path.join(root, path.relative(template.root, p));
  return {
    ...template,
    root,
    src: at(template.src),
    exp: at(template.exp),
    site: at(template.site),
    privateRemote: fwd(at(template.privateRemote)),
    publicRemote: fwd(at(template.publicRemote)),
  };
}

const gitIn = (fx: Fixture, dir: string, ...args: string[]) => run(fx.env, 'git', ['-C', dir, ...args]);

/** Commits a change in `dir` and pushes it, so preflight's "pushed" gate holds. */
function commitAndPush(fx: Fixture, dir: string, files: Record<string, string>, message: string) {
  for (const [f, text] of Object.entries(files)) write(path.join(dir, f), text);
  gitIn(fx, dir, 'add', '-A');
  gitIn(fx, dir, 'commit', '-q', '-m', message);
  gitIn(fx, dir, 'push', '-q', 'origin', 'main');
}

/** Runs the fixture's real scripts/release.mjs (async, so tests can overlap). */
function release(fx: Fixture, args: string[], extraEnv: Record<string, string> = {}): Promise<{ code: number | null; out: string }> {
  // The dry run's scratch copy and logs go to the OS temp dir; point that into
  // the fixture so afterAll takes them with it.
  const tmp = path.join(fx.root, 'tmp');
  fs.mkdirSync(tmp, { recursive: true });
  return new Promise((resolve) => {
    const child = spawn(process.execPath, [path.join(fx.src, 'scripts', 'release.mjs'), ...args], {
      cwd: fx.src,
      env: { ...fx.env, TMPDIR: tmp, TEMP: tmp, TMP: tmp, FIXTURE_EXPORT: fx.exp, FIXTURE_SITE: fx.site, FIXTURE_SIGNING_KEY: fx.fingerprint, ...extraEnv },
    });
    let out = '';
    child.stdout.on('data', (d) => (out += d));
    child.stderr.on('data', (d) => (out += d));
    child.on('close', (code) => resolve({ code, out }));
  });
}

/** Everything a run could have written: refs, index, working tree, remotes, state. */
function footprint(fx: Fixture) {
  const of = (dir: string) => ({
    refs: gitIn(fx, dir, 'for-each-ref', '--format=%(refname) %(objectname)'),
    status: gitIn(fx, dir, 'status', '--porcelain', '--untracked-files=all', '--ignored'),
    index: createHash('sha1').update(gitIn(fx, dir, 'ls-files', '-s')).digest('hex'),
  });
  return {
    src: of(fx.src),
    exp: of(fx.exp),
    privateRemote: run(fx.env, 'git', [`--git-dir=${fx.privateRemote}`, 'for-each-ref']),
    publicRemote: run(fx.env, 'git', [`--git-dir=${fx.publicRemote}`, 'for-each-ref']),
    state: fs.existsSync(path.join(fx.src, '.git', 'filex-release')),
  };
}

/** The published surfaces, as files the fixture plan's fetch serves. */
function publish(fx: Fixture, { docsFresh }: { docsFresh: boolean }) {
  const exportHead = gitIn(fx, fx.exp, 'rev-parse', 'HEAD');
  write(path.join(fx.site, 'api/capabilities'), JSON.stringify({ version: `${TAG} (${exportHead}, 2026-01-02T00:00:00Z)` }));
  write(path.join(fx.site, 'updates/stable.json'), JSON.stringify({ channel: 'stable', releases: [{ version: TAG }, { version: 'v0.1.0' }] }));
  const app = Buffer.from('the fixture desktop app');
  const sha512 = createHash('sha512').update(app).digest('base64');
  write(path.join(fx.site, 'desktop/app.bin'), app.toString());
  write(path.join(fx.site, 'desktop/latest.yml'), `version: ${VERSION}\nfiles:\n  - url: app.bin\n    sha512: ${sha512}\n    size: ${app.length}\npath: app.bin\nsha512: ${sha512}\n`);
  write(path.join(fx.site, 'docs/RELEASES.html'), `<main><p>Latest — ${TAG}</p></main>`);
  write(
    path.join(fx.site, 'docs/GUIDE.html'),
    `<main><h1>Guide</h1><h2 id="getting-started">Getting started</h2>${docsFresh ? '<h2 id="sharing">Sharing a folder with a team</h2>' : ''}</main>`,
  );
}

describe.concurrent('pnpm release — options', () => {
  it('refuses an option it does not know: there is no way to skip a gate', { timeout: TIMEOUT }, async ({ expect }) => {
    // Refused before anything is read, so the shared template is safe here.
    const fx = template;
    for (const opt of ['--skip-gate', '--force', '--no-verify', '--skip=pretag']) {
      const r = await release(fx, [VERSION, opt]);
      expect(r.code, opt).toBe(2);
      expect(r.out).toContain(`unknown option ${opt}`);
    }
    const ack = await release(fx, [VERSION, '--ack', 'pretag']);
    expect(ack.code).toBe(2);
    expect(ack.out).toContain('a person can confirm only audit, export-withheld, deploy');
    expect((await release(fx, [`v${VERSION}`])).code).toBe(2);
    expect((await release(fx, ['1.0.4'])).out).toContain('there is no 1.0.x');
  });
});

describe.concurrent('pnpm release — preflight stops before anything is written', () => {
  it('(a) a deployment target already behind the release it claims', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    const chart = fs.readFileSync(path.join(fx.src, 'deploy/helm/filex/Chart.yaml'), 'utf8');
    commitAndPush(fx, fx.src, { 'deploy/helm/filex/Chart.yaml': chart.replace(/^appVersion:.*$/m, 'appVersion: "v0.0.9"') }, 'chart drifted');
    const before = footprint(fx);
    const r = await release(fx, [VERSION]);
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  deployment targets name the current release (before the bump)');
    expect(r.out).toContain('helm appVersion: v0.0.9, the release is v0.1.0');
    expect(r.out).toContain('STOPPED at preflight');
    const after = footprint(fx);
    expect(after.src.refs).toBe(before.src.refs);
    expect(after.exp).toEqual(before.exp);
  });

  it('(b) a dirty working tree', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    write(path.join(fx.src, 'stray-wip.txt'), 'half a feature');
    const r = await release(fx, [VERSION]);
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  working tree clean, tracked and untracked');
    expect(r.out).toContain('stray-wip.txt');
    expect(r.out).toContain('STOPPED at preflight');
    expect(r.out).not.toContain('2/10 audit');
  });

  // Every preflight gate runs even after one is red, so one run shows each
  // problem by name — which is also how these cases share a fixture.
  it('an unpushed main and an uncommitted workflow change in the public checkout (lesson #461)', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    write(path.join(fx.src, 'docs/GUIDE.md'), '# Guide\n\nlocal only\n');
    gitIn(fx, fx.src, 'commit', '-q', '-am', 'not pushed');
    fs.appendFileSync(path.join(fx.exp, '.github/workflows/release.yml'), 'env: { SKIP: nothing }\n');
    const r = await release(fx, [VERSION]);
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  main is pushed to origin');
    expect(r.out).toContain('uncommitted change(s) under .github/');
    expect(r.out).toContain('lesson #461');
  });

  it('a number that is not newer, and an [Unreleased] with nothing in it', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    commitAndPush(fx, fx.src, { 'CHANGELOG.md': CHANGELOG_010 }, 'nothing to release yet');
    const r = await release(fx, ['0.1.0']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('0.1.0 is not newer than the last release v0.1.0');
    expect(r.out).toContain('v0.1.0 already exists here and on origin');
    const empty = await release(fx, [VERSION]);
    expect(empty.code).toBe(1);
    expect(empty.out).toContain('the [Unreleased] section is empty');
  });
});

describe.concurrent('pnpm release --dry-run', () => {
  it('runs every stage in order and writes nothing anywhere', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    const before = footprint(fx);
    const r = await release(fx, [VERSION, '--dry-run']);
    expect(r.out).toContain('DRY RUN GREEN');
    expect(r.code).toBe(0);
    const order = ['preflight', 'audit', 'docs', 'stamp', 'pretag', 'export', 'sign', 'push', 'ci', 'deploy'].map((s, i) =>
      r.out.indexOf(`── ${i + 1}/10 ${s} `),
    );
    expect(order.every((at) => at >= 0), `every stage banner is printed: ${order}`).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    for (const f of ['CHANGELOG.md', 'web/package.json', 'packages/core/package.json', 'deploy/helm/filex/Chart.yaml', 'deploy/umbrel/filex/umbrel-app.yml']) {
      expect(r.out).toContain(`            ${f}`);
    }
    expect(r.out).toContain(`tag -s ${TAG} -m "${TAG}"`);
    expect(r.out).toContain(`push origin refs/tags/${TAG}`);
    expect(footprint(fx)).toEqual(before);
  });

  it('stops at a red pretag gate, before the export', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    const before = footprint(fx);
    const r = await release(fx, [VERSION, '--dry-run'], { FIXTURE_PRETAG_FAIL: '1' });
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  the fixture test suite');
    expect(r.out).toContain('STOPPED at pretag');
    expect(r.out).not.toContain('6/10 export');
    expect(footprint(fx)).toEqual(before);
  });

  it('refuses an export that loses a file, withholds one unasked, or names a private host', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    // Built from parts: this file is itself exported, and the export gate
    // must not find the literal here (lesson #95).
    const leak = ['git@gitlab', 'com:brftech/infrastack.git'].join('.');
    commitAndPush(fx, fx.src, { 'private/NOTES.md': 'ops notes\n', 'docs/NOTES.md': `clone ${leak}\n` }, 'notes: one now private, one with an internal remote');
    commitAndPush(fx, fx.exp, { 'LEFT-BEHIND.md': 'only in public\n', 'private/NOTES.md': 'ops notes\n' }, 'published once');
    const r = await release(fx, [VERSION, '--dry-run']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  the export deletes nothing it cannot explain');
    expect(r.out).toContain('LEFT-BEHIND.md');
    expect(r.out).toContain('FAILED  no private host or module path in the public tree');
    expect(r.out).toContain('docs/NOTES.md');
    expect(r.out).toContain('STOPPED at export');

    // With the loss and the leak gone, the withheld file alone still stops the
    // export — until a person confirms exactly that list.
    commitAndPush(fx, fx.src, { 'docs/NOTES.md': 'clone it from the forge\n' }, 'no internal remote');
    gitIn(fx, fx.exp, 'rm', '-q', 'LEFT-BEHIND.md');
    gitIn(fx, fx.exp, 'commit', '-q', '-m', 'gone from public too');
    gitIn(fx, fx.exp, 'push', '-q', 'origin', 'main');
    const withheld = await release(fx, [VERSION, '--dry-run']);
    expect(withheld.code).toBe(1);
    expect(withheld.out).toContain('WITHHOLDS');
    expect(withheld.out).toContain('private/NOTES.md');
    const ok = await release(fx, [VERSION, '--dry-run', '--ack', 'export-withheld']);
    expect(ok.code).toBe(0);
    expect(ok.out).toContain('withheld on purpose');
  });
});

describe.skipIf(!SSH_KEYGEN)('pnpm release — a whole release, a person doing the person\'s steps', () => {
  // v0.45.0: a red export gate left the public checkout holding that export,
  // and every --resume after the fix stopped on "nothing else pending",
  // because the export's tree was recorded only when all its gates passed.
  it('resumes past its own export after a red export gate, discarding that export and nothing else', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    const leak = ['git@gitlab', 'com:brftech/infrastack.git'].join('.');
    commitAndPush(fx, fx.src, { 'docs/NOTES.md': `clone ${leak}\n` }, 'notes with an internal remote');
    let r = await release(fx, [VERSION]);
    expect(r.code).toBe(3);
    r = await release(fx, [VERSION, '--resume', '--ack', 'audit']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  no private host or module path in the public tree');
    expect(gitIn(fx, fx.exp, 'status', '--porcelain')).not.toBe('');

    commitAndPush(fx, fx.src, { 'docs/NOTES.md': 'clone it from the forge\n' }, 'no internal remote');
    r = await release(fx, [VERSION, '--resume']);
    expect(r.out).toContain('discarded the export');
    expect(r.out).not.toContain('FAILED  export checkout');
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at sign');
  });

  it('stamps once, stops at every human step, refuses every wrong move, and verifies what was published', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    const git = (dir: string, ...args: string[]) => gitIn(fx, dir, ...args);
    const commits = () => Number(git(fx.src, 'rev-list', '--count', 'HEAD'));

    // 1. the audit is a person's
    let r = await release(fx, [VERSION]);
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at audit');
    expect(r.out).toContain(`pnpm release ${VERSION} --resume --ack audit`);
    expect(r.out).toContain('touched in this range: docs/*.md');
    expect(r.out).toContain('NOT touched:           README.md');

    // without --resume a second start is refused: one run per release
    expect((await release(fx, [VERSION])).code).toBe(2);

    // 2. stamp, pretag, export — then the tags are a person's
    const baseCommits = commits();
    r = await release(fx, [VERSION, '--resume', '--ack', 'audit']);
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at sign');
    expect(commits()).toBe(baseCommits + 1);
    expect(git(fx.src, 'log', '-1', '--format=%s')).toBe(`chore(release): ${TAG}`);
    const changelog = fs.readFileSync(path.join(fx.src, 'CHANGELOG.md'), 'utf8');
    expect(changelog).toMatch(new RegExp(`## \\[Unreleased\\]\\n\\n## \\[${VERSION}\\] - \\d{4}-\\d{2}-\\d{2}\\n\\n### Added`));
    expect(JSON.parse(fs.readFileSync(path.join(fx.src, 'web/package.json'), 'utf8')).version).toBe(VERSION);
    expect(JSON.parse(fs.readFileSync(path.join(fx.src, 'packages/core/package.json'), 'utf8')).version).toBe(VERSION);
    expect(fs.readFileSync(path.join(fx.src, 'deploy/helm/filex/Chart.yaml'), 'utf8')).toContain(`appVersion: "${TAG}"`);
    expect(git(fx.src, 'status', '--porcelain')).toBe('');
    expect(git(fx.exp, 'diff', '--cached', '--name-only')).toContain('CHANGELOG.md');
    expect(git(fx.src, 'tag', '--list', TAG)).toBe('');
    expect(run(fx.env, 'git', [`--git-dir=${fx.privateRemote}`, 'rev-parse', 'main'])).not.toBe(git(fx.src, 'rev-parse', 'HEAD'));
    const releaseCommit = git(fx.src, 'rev-parse', 'HEAD');

    // resuming without doing the step changes nothing and stamps nothing twice
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(3);
    expect(commits()).toBe(baseCommits + 1);

    // 3. wrong moves at sign
    git(fx.src, 'tag', '-a', TAG, '-m', TAG, releaseCommit);
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('carries no good signature');
    git(fx.src, 'tag', '-d', TAG);

    git(fx.src, '-c', `user.signingKey=${path.join(fx.root, 'other-key').split(path.sep).join('/')}`, 'tag', '-s', TAG, '-m', TAG, releaseCommit);
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('is signed, but not by the release key');
    git(fx.src, 'tag', '-d', TAG);

    git(fx.src, 'tag', '-s', TAG, '-m', TAG, releaseCommit);
    git(fx.exp, 'commit', '-q', '-m', `${TAG} - teams (fixes #12)`);
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('CLOSES the issue');
    git(fx.exp, 'commit', '-q', '--amend', '-m', `${TAG} - folders shared with a team (#12)`);

    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(3);
    expect(r.out).toContain('Still to do: the public tag');
    git(fx.exp, 'tag', '-s', TAG, '-m', TAG);
    const exportCommit = git(fx.exp, 'rev-parse', 'HEAD');

    // 4. push: the lesson #55 leak is refused out loud
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at push');
    expect(r.out).toContain('never --tags');
    git(fx.src, 'push', '-q', fx.publicRemote, `refs/tags/${TAG}`);
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('names the PRIVATE release commit');
    git(fx.exp, 'push', '-q', 'origin', `:refs/tags/${TAG}`);

    git(fx.src, 'push', '-q', 'origin', 'main', `refs/tags/${TAG}`);
    git(fx.exp, 'push', '-q', 'origin', 'main', `refs/tags/${TAG}`);

    // 5. ci: nothing published yet is red, not "probably fine"
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  the fixture release workflow published');
    expect(r.out).toContain('STOPPED at ci');

    // 6. deploy: (c) a docs site serving the previous snapshot is refused
    publish(fx, { docsFresh: false });
    r = await release(fx, [VERSION, '--resume'], { FIXTURE_CI_DONE: '1' });
    expect(r.code).toBe(1);
    expect(r.out).toContain('is an OLD snapshot');
    expect(r.out).toContain('"Sharing a folder with a team"');
    expect(r.out).toContain('ok      the fixture server runs v0.2.0');
    expect(r.out).toContain('ok      the fixture feed offers 0.2.0');

    publish(fx, { docsFresh: true });
    r = await release(fx, [VERSION, '--resume'], { FIXTURE_CI_DONE: '1' });
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at deploy');

    r = await release(fx, [VERSION, '--resume', '--ack', 'deploy'], { FIXTURE_CI_DONE: '1' });
    expect(r.code).toBe(0);
    expect(r.out).toContain(`${TAG} is released, and everything it published was read back.`);

    // the record says so, and the remotes hold exactly the release
    const status = await release(fx, [VERSION, '--status']);
    expect(status.out).toContain('released');
    expect(run(fx.env, 'git', [`--git-dir=${fx.privateRemote}`, 'rev-parse', `${TAG}^{commit}`])).toBe(releaseCommit);
    expect(run(fx.env, 'git', [`--git-dir=${fx.publicRemote}`, 'rev-parse', `${TAG}^{commit}`])).toBe(exportCommit);
    expect((await release(fx, [VERSION, '--resume'])).out).toContain('has already been released');
  });
});
