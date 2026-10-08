// `pnpm release X.Y.Z` stops at a red gate — proved on a throwaway repository.
//
// ⚠⚠ Why this is tested end to end and not only function by function: the
// point of the release command is ORDER and REFUSAL. A unit test can show that
// `closingKeywords` finds "Fixes #12"; only a run can show that the release
// does not get past `land` while the public commit says it. Every red case
// below is one that shipped, or nearly shipped, a broken release:
//
//   a Helm chart left behind           23 releases (2026-08-29, lesson #52)
//   a dirty tree                       v0.38.1 shipped a half-finished page
//   an uncommitted workflow change     v0.43.1 (lesson #461)
//   a docs site serving a snapshot     2026-08-29 and 2026-09-25 (#511)
//   a public tag on a private commit   v0.26.0, v0.27.5 (lesson #55)
//   an unsigned / wrong-key tag        releases <= v0.27.5 were unsigned
//   "Fixes #N" in the export commit    v0.41.2 closed an issue (lesson #163)
//   a tag before GitHub tested it      0.43.0-0.45.0, five numbers (#76)
//   a resume that re-stamps a tag      v0.46.0, v0.47.0 (2026-09-26/27)
//
// GitHub Actions is the fixture plan's stand-in (FIXTURE_GH_*): the gate
// stage asks it for runs and starts the dry run through it, never `gh`.
// CircleCI too (FIXTURE_CC_*, #181): `--gate circleci` asks it instead,
// through the real circleci.mjs's mapping of its workflow states.
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
  'scripts/release/circleci.mjs',
  'scripts/release-notes.mjs',
  'scripts/sync-deploy-versions.mjs',
  'docs-site/.vitepress/github-slug.mjs',
  'docs-site/scripts/markdown-headings.mjs',
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

const STAGE_ORDER = ['preflight', 'audit', 'docs', 'stamp', 'pretag', 'export', 'land', 'gate', 'sign', 'push', 'ci', 'deploy'];
const banner = (stage: string) => `── ${STAGE_ORDER.indexOf(stage) + 1}/${STAGE_ORDER.length} ${stage} `;

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
      env: {
        ...fx.env, TMPDIR: tmp, TEMP: tmp, TMP: tmp, FIXTURE_EXPORT: fx.exp, FIXTURE_SITE: fx.site, FIXTURE_SIGNING_KEY: fx.fingerprint,
        // GitHub: CI passed on whatever the public main is, dry runs pass.
        FIXTURE_GH: path.join(fx.root, 'gh'), FIXTURE_GH_CI: 'success',
        ...extraEnv,
      },
      windowsHide: true,
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

/** The dry runs the gate started on the fixture's GitHub: one entry per dispatch. */
function dispatched(fx: Fixture): Array<{ sha: string; title: string; ref: string; inputs: Record<string, string> }> {
  const f = path.join(fx.root, 'gh', 'dispatched.json');
  return fs.existsSync(f) ? JSON.parse(fs.readFileSync(f, 'utf8')) : [];
}

/** How often the gate asked each CI (the fixture plan counts): { github?: n, circleci?: n }. */
function askedOf(fx: Fixture): { github?: number; circleci?: number } {
  const f = path.join(fx.root, 'gh', 'asked.json');
  return fs.existsSync(f) ? JSON.parse(fs.readFileSync(f, 'utf8')) : {};
}

/** The recorded run of the release (what --resume reads). */
function recordOf(fx: Fixture) {
  return JSON.parse(fs.readFileSync(path.join(fx.src, '.git', 'filex-release', `${TAG}.json`), 'utf8'));
}

/** From the start to WAITING at land: audit confirmed, stamped, tested, exported. */
async function toLand(fx: Fixture) {
  let r = await release(fx, [VERSION]);
  expect(r.code, r.out).toBe(3);
  r = await release(fx, [VERSION, '--resume', '--ack', 'audit']);
  expect(r.out).toContain('WAITING at land');
  expect(r.code, r.out).toBe(3);
  return r;
}

/** A person's land: the export commit, then both mains pushed, no tag. */
function landBoth(fx: Fixture, message = `${TAG} - folders shared with a team (#12)`) {
  gitIn(fx, fx.exp, 'commit', '-q', '-m', message);
  gitIn(fx, fx.src, 'push', '-q', 'origin', 'main');
  gitIn(fx, fx.exp, 'push', '-q', 'origin', 'main');
  return gitIn(fx, fx.exp, 'rev-parse', 'HEAD');
}

/** The published surfaces, as files the fixture plan's fetch serves. */
function publish(fx: Fixture, { docsFresh }: { docsFresh: boolean }) {
  // The server runs the image the TAG built, not whatever the public main is
  // at by now: main may have moved on since the push.
  const exportHead = gitIn(fx, fx.exp, 'rev-parse', `${TAG}^{commit}`);
  write(path.join(fx.site, 'api/capabilities'), JSON.stringify({ version: `${TAG} (${exportHead}, 2026-01-02T00:00:00Z)` }));
  write(path.join(fx.site, 'updates/stable.json'), JSON.stringify({ channel: 'stable', releases: [{ version: TAG }, { version: 'v0.1.0' }] }));
  const app = Buffer.from('the fixture desktop app');
  const sha512 = createHash('sha512').update(app).digest('base64');
  write(path.join(fx.site, 'desktop/app.bin'), app.toString());
  write(path.join(fx.site, 'desktop/latest.yml'), `version: ${VERSION}\nfiles:\n  - url: app.bin\n    sha512: ${sha512}\n    size: ${app.length}\npath: app.bin\nsha512: ${sha512}\n`);
  write(path.join(fx.site, 'docs/RELEASES.html'), `<main><p>Latest - ${TAG}</p></main>`);
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

  // #172: a patch profile tests only what changed since the last release, so
  // it is for X.Y.Z+1 alone; a profile it does not know is refused like an
  // option it does not know.
  it('refuses a patch profile for a minor, and a profile it does not know', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = template;
    const minor = await release(fx, [VERSION, '--profile', 'patch']);
    expect(minor.code).toBe(2);
    expect(minor.out).toContain('--profile patch: 0.2.0 is no patch number');
    const unknown = await release(fx, [VERSION, '--profile', 'quick']);
    expect(unknown.code).toBe(2);
    expect(unknown.out).toContain('the profiles are minor, patch, full');
    const only = await release(fx, [VERSION, '--resume', '--only', 'deploy', '--profile', 'full']);
    expect(only.code).toBe(2);
    expect(only.out).toContain('--only deploy runs no test gate');
  });

  // #181: where the gate reads the export commit's suites is a choice
  // between two named CIs, and nothing else - not a way to skip the gate.
  it('refuses a gate source it does not know, and a gate source with --only deploy', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = template;
    for (const v of ['gitlab', 'none', 'skip']) {
      const r = await release(fx, [VERSION, '--gate', v]);
      expect(r.code, v).toBe(2);
      expect(r.out).toContain(`--gate ${v}: the gate reads github or circleci`);
    }
    const bare = await release(fx, [VERSION, '--gate']);
    expect(bare.code).toBe(2);
    expect(bare.out).toContain('--gate needs a value');
    const only = await release(fx, [VERSION, '--resume', '--only', 'deploy', '--gate', 'circleci']);
    expect(only.code).toBe(2);
    expect(only.out).toContain('--only deploy runs no test gate');
  });
});

describe.concurrent('pnpm release — profiles', () => {
  // The patch profile against the last tag: preflight refuses a patch
  // profile on anything but X.Y.Z+1 even when the number itself is one
  // (the fixture's last tag is v0.1.0, so 0.1.1 is a patch step and runs).
  it('a patch profile on a patch step runs every stage, and says what it chose', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    const before = footprint(fx);
    const r = await release(fx, ['0.1.1', '--dry-run', '--profile', 'patch']);
    expect(r.out).toContain('profile patch');
    expect(r.out).toContain('--profile patch: 0.1.1 is a patch step');
    expect(r.out).toContain('DRY RUN GREEN');
    expect(r.code).toBe(0);
    expect(footprint(fx)).toEqual(before);
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
    expect(r.out).not.toContain(banner('audit'));
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
    const order = STAGE_ORDER.map((s) => r.out.indexOf(banner(s)));
    expect(order.every((at) => at >= 0), `every stage banner is printed: ${order}`).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    for (const f of ['CHANGELOG.md', 'web/package.json', 'packages/core/package.json', 'deploy/helm/filex/Chart.yaml', 'deploy/umbrel/filex/umbrel-app.yml']) {
      expect(r.out).toContain(`            ${f}`);
    }
    // #76: both mains go out untagged, GitHub tests that commit, THEN the tags.
    const at = (text: string) => r.out.indexOf(text);
    expect(at('push origin main')).toBeGreaterThan(at(banner('land')));
    expect(at('gh workflow run release.yml --ref main -f publish=false')).toBeGreaterThan(at(banner('gate')));
    expect(r.out).toContain('dry run all <export commit>');
    expect(at(`tag -s ${TAG} -m "${TAG}"`)).toBeGreaterThan(at(banner('sign')));
    expect(at(`push origin refs/tags/${TAG}`)).toBeGreaterThan(at(banner('push')));
    expect(r.out.slice(at(banner('land')), at(banner('gate')))).not.toContain('refs/tags/');
    // nothing was started on GitHub
    expect(dispatched(fx)).toEqual([]);
    expect(footprint(fx)).toEqual(before);
  });

  // #181: a dry run that names CircleCI says what the real gate would read,
  // and asks neither CI anything.
  it('with --gate circleci, says the gate reads CircleCI and starts nothing on GitHub', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    const before = footprint(fx);
    const r = await release(fx, [VERSION, '--dry-run', '--gate', 'circleci']);
    expect(r.out).toContain('DRY RUN GREEN');
    expect(r.code).toBe(0);
    const gate = r.out.slice(r.out.indexOf(banner('gate')), r.out.indexOf(banner('sign')));
    expect(gate).toContain('CircleCI tests the export commit');
    expect(gate).toContain('the workflow "ci" (.circleci/config.yml)');
    expect(gate).toContain('the release dry run does not run');
    expect(gate).not.toContain('gh workflow run release.yml');
    expect(r.out).toContain('CircleCI has tested the export commit');
    expect(dispatched(fx)).toEqual([]);
    expect(askedOf(fx)).toEqual({});
    expect(footprint(fx)).toEqual(before);
  });

  it('stops at a red pretag gate, before the export', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    const before = footprint(fx);
    const r = await release(fx, [VERSION, '--dry-run'], { FIXTURE_PRETAG_FAIL: '1' });
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  the fixture test suite');
    expect(r.out).toContain('STOPPED at pretag');
    expect(r.out).not.toContain(banner('export'));
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

  // Lesson #1108: on Windows the export's `git add -A` staged every new file
  // 100644, and a script the docs tell a reader to run went public without
  // its executable bit. The fixture's export writes files without their bits,
  // as that one did; the release reads the bits back and stops.
  it('refuses an export that drops an executable bit (lesson #1108)', { timeout: TIMEOUT }, async ({ expect }) => {
    const fx = fixture();
    const file = path.join(fx.src, 'scripts', 'run-me.sh');
    write(file, '#!/usr/bin/env bash\necho run\n');
    fs.chmodSync(file, 0o755);
    gitIn(fx, fx.src, 'add', '--', 'scripts/run-me.sh');
    gitIn(fx, fx.src, 'update-index', '--chmod=+x', '--', 'scripts/run-me.sh');
    gitIn(fx, fx.src, 'commit', '-q', '-m', 'a script a reader runs as it is');
    gitIn(fx, fx.src, 'push', '-q', 'origin', 'main');
    const r = await release(fx, [VERSION, '--dry-run']);
    expect(r.code, r.out).toBe(1);
    expect(r.out).toContain('FAILED  the public tree keeps every executable bit the private tree has');
    expect(r.out).toContain('executable here, not in the public tree: scripts/run-me.sh');
    expect(r.out).toContain('STOPPED at export');
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
    expect(r.out).toContain('WAITING at land');
  });

  // v0.53.0: the documented order commits the packaging/ci patches in the
  // public checkout before the pretag and lets the land push them, but the
  // export gate wanted that checkout identical to GitHub and stopped there.
  it('lets the public checkout run ahead of GitHub by workflow commits alone', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    let r = await release(fx, [VERSION]);
    expect(r.code).toBe(3);
    write(path.join(fx.exp, '.github', 'workflows', 'patched.yml'), 'name: patched\n');
    gitIn(fx, fx.exp, 'add', '--', '.github/workflows/patched.yml');
    gitIn(fx, fx.exp, 'commit', '-q', '-m', 'ci: a packaging/ci patch');
    r = await release(fx, [VERSION, '--resume', '--ack', 'audit']);
    expect(r.out).toContain('workflow commit(s) under .github/, pushed by the land');
    expect(r.out).not.toContain('FAILED  export checkout in step');
    expect(r.out).toContain('WAITING at land');
  });

  it('refuses a public checkout that is ahead of GitHub by anything but workflows', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    write(path.join(fx.exp, 'notes.txt'), 'a stray file\n');
    gitIn(fx, fx.exp, 'add', '--', 'notes.txt');
    gitIn(fx, fx.exp, 'commit', '-q', '-m', 'not a workflow');
    const r = await release(fx, [VERSION]);
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  export checkout in step');
    expect(r.out).toMatch(/ahead of \S+ by more than workflow commits: notes\.txt/);
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

    // 2. stamp, pretag, export — then landing the export is a person's
    const baseCommits = commits();
    r = await release(fx, [VERSION, '--resume', '--ack', 'audit']);
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at land');
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

    // 3. land: the export commit must not close an issue, and both mains go out untagged
    git(fx.exp, 'commit', '-q', '-m', `${TAG} - teams (fixes #12)`);
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('CLOSES the issue');
    expect(r.out).toContain('STOPPED at land');
    git(fx.exp, 'commit', '-q', '--amend', '-m', `${TAG} - folders shared with a team (#12)`);
    const exportCommit = git(fx.exp, 'rev-parse', 'HEAD');

    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at land');
    expect(r.out).toContain('Still to do: origin main, origin main');
    git(fx.src, 'push', '-q', 'origin', 'main');
    git(fx.exp, 'push', '-q', 'origin', 'main');
    expect(dispatched(fx)).toEqual([]);

    // 4. gate: GitHub tests the export commit before any tag. A red dry run
    // stops the release, tags nothing, and spends no number.
    r = await release(fx, [VERSION, '--resume'], { FIXTURE_GH_DRY: 'failure' });
    expect(r.code).toBe(1);
    expect(r.out).toContain('STOPPED at gate');
    expect(r.out).toContain(`FAILED  release.yml dry run passed on ${exportCommit.slice(0, 10)}`);
    expect(r.out).toContain(`${TAG} is not spent`);
    expect(r.out).toContain('gh run rerun 100 --failed');
    expect(dispatched(fx)).toEqual([{ workflow: 'release.yml', ref: 'main', inputs: { publish: 'false' }, sha: exportCommit, title: `dry run all ${exportCommit}` }]);
    expect(git(fx.src, 'tag', '--list', TAG)).toBe('');
    expect(git(fx.exp, 'tag', '--list', TAG)).toBe('');

    // …its failed jobs re-run and pass: the same run is read again, no second dry run
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(3);
    expect(r.out).toContain(`ok      ci.yml (push) passed on ${exportCommit.slice(0, 10)}`);
    expect(r.out).toContain(`ok      release.yml dry run passed on ${exportCommit.slice(0, 10)}`);
    expect(r.out).toContain('WAITING at sign');
    expect(r.out).toContain(`tag -s ${TAG} -m "${TAG}" ${releaseCommit}`);
    expect(r.out).toContain(`tag -s ${TAG} -m "${TAG}" ${exportCommit}`);
    expect(dispatched(fx)).toHaveLength(1);

    // 5. wrong moves at sign
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
    // the public tag on a commit GitHub did not test
    git(fx.exp, 'tag', '-s', TAG, '-m', TAG, `${exportCommit}^`);
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('not the export commit GitHub tested');
    git(fx.exp, 'tag', '-d', TAG);

    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(3);
    expect(r.out).toContain('Still to do: the public tag');
    git(fx.exp, 'tag', '-s', TAG, '-m', TAG, exportCommit);

    // 6. push the tags: the lesson #55 leak is refused out loud
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at push');
    expect(r.out).toContain('never --tags');
    git(fx.src, 'push', '-q', fx.publicRemote, `refs/tags/${TAG}`);
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('names the PRIVATE release commit');
    git(fx.exp, 'push', '-q', 'origin', `:refs/tags/${TAG}`);

    git(fx.src, 'push', '-q', 'origin', `refs/tags/${TAG}`);
    git(fx.exp, 'push', '-q', 'origin', `refs/tags/${TAG}`);

    // 7. ci: nothing published yet is red, not "probably fine"
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  the fixture release workflow published');
    expect(r.out).toContain('STOPPED at ci');

    // ⚠ main moves on while the deploy is still to do (v0.47.0: fixes to the
    // Store upload landed on both mains between the push and the deploy). The
    // release is the published tag from here: the next resume re-stamped HEAD
    // as the release commit, re-ran the whole test chain on it and would have
    // asked for the tag to be moved to it. A docs heading written after the
    // tag is not this release's, and the deploy checks must not ask for it.
    fs.writeFileSync(path.join(fx.src, 'AFTER.md'), 'work that is not in the release\n');
    fs.appendFileSync(path.join(fx.src, 'docs/GUIDE.md'), '\n## Written after the tag\n\nNot in this release.\n');
    git(fx.src, 'add', 'AFTER.md', 'docs/GUIDE.md');
    git(fx.src, 'commit', '-q', '-m', 'docs: after the tag');
    git(fx.src, 'push', '-q', 'origin', 'main');
    fs.writeFileSync(path.join(fx.exp, 'AFTER.md'), 'work that is not in the release\n');
    git(fx.exp, 'add', 'AFTER.md');
    git(fx.exp, 'commit', '-q', '-m', 'docs: after the tag');
    git(fx.exp, 'push', '-q', 'origin', 'main');
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain(`published as ${TAG}`);
    expect(r.out).not.toContain('on top of the release commit');
    expect(r.out).not.toContain('WAITING at sign');
    expect(r.out).toContain('STOPPED at ci');

    // --only deploy: the deploy checks alone while ci is held red (lesson
    // #1023), on the TAGGED commits, confirming nothing
    publish(fx, { docsFresh: false });
    r = await release(fx, [VERSION, '--resume', '--only', 'deploy']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('is an OLD snapshot');
    expect(r.out).not.toContain(banner('ci'));
    publish(fx, { docsFresh: true });
    r = await release(fx, [VERSION, '--resume', '--only', 'deploy']);
    expect(r.code, r.out).toBe(0);
    expect(r.out).toContain(`ok      the fixture docs serve ${TAG}`);
    expect(r.out).toContain('the deploy checks are green');
    expect(r.out).not.toContain('is released');
    expect((await release(fx, [VERSION, '--resume', '--only', 'deploy', '--ack', 'deploy'])).code).toBe(2);
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('STOPPED at ci');

    // 8. deploy: (c) a docs site serving the previous snapshot is refused
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

  // ⚠ v0.46.0/v0.47.0: tags pushed, a fix pushed to main, and only then
  // --resume — whose record still said "push: waiting". That resume took HEAD
  // for the release commit and ran the whole chain on it again. Whether a tag
  // is out is asked of the remotes, not of the record.
  it('once a tag is on a remote, a resume never goes back to the stamp or the test chain', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    const git = (dir: string, ...args: string[]) => gitIn(fx, dir, ...args);
    await toLand(fx);
    const releaseCommit = git(fx.src, 'rev-parse', 'HEAD');
    const exportCommit = landBoth(fx);
    let r = await release(fx, [VERSION, '--resume']);
    expect(r.out).toContain('WAITING at sign');
    git(fx.src, 'tag', '-s', TAG, '-m', TAG, releaseCommit);
    git(fx.exp, 'tag', '-s', TAG, '-m', TAG, exportCommit);
    git(fx.src, 'push', '-q', 'origin', `refs/tags/${TAG}`);
    git(fx.exp, 'push', '-q', 'origin', `refs/tags/${TAG}`);
    // …and main moves on before anybody runs --resume
    fs.writeFileSync(path.join(fx.src, 'AFTER.md'), 'a fix after the tags\n');
    git(fx.src, 'add', 'AFTER.md');
    git(fx.src, 'commit', '-q', '-m', 'fix: after the tags');
    git(fx.src, 'push', '-q', 'origin', 'main');
    r = await release(fx, [VERSION, '--resume']);
    expect(r.out).toContain(`published as ${TAG} (${releaseCommit.slice(0, 10)})`);
    expect(r.out).not.toContain('on top of the release commit');
    expect(r.out).not.toContain('FAILED  the fixture test suite');
    expect(r.out).toContain(`ok      origin: ${TAG} is the release commit`);
    expect(r.out).toContain('STOPPED at ci');
    expect(r.code).toBe(1);
    expect(git(fx.src, 'rev-list', '--count', `${releaseCommit}..HEAD`)).toBe('1');
    const state = JSON.parse(fs.readFileSync(path.join(fx.src, '.git', 'filex-release', `${TAG}.json`), 'utf8'));
    expect(state.releaseCommit).toBe(releaseCommit);
    expect(dispatched(fx)).toHaveLength(1);
  });

  it('a red gate spends no number: the fix lands on main, and GitHub tests the new commits', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    const git = (dir: string, ...args: string[]) => gitIn(fx, dir, ...args);
    await toLand(fx);
    const first = landBoth(fx);
    let r = await release(fx, [VERSION, '--resume'], { FIXTURE_GH_DRY: 'failure' });
    expect(r.code).toBe(1);
    expect(r.out).toContain('STOPPED at gate');

    // the fix: a commit on top of the release commit, pushed
    commitAndPush(fx, fx.src, { 'docs/GUIDE.md': '# Guide\n\n## Getting started\n\nOpen it.\n\n## Sharing a folder with a team\n\nPick the team, then share.\n' }, 'fix: what the dry run found');
    const fixed = git(fx.src, 'rev-parse', 'HEAD');
    r = await release(fx, [VERSION, '--resume']);
    expect(r.out).toContain('1 commit(s) on top of the release commit');
    expect(r.out).toContain('ok      the fixture test suite');
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at land');
    expect(git(fx.exp, 'diff', '--cached', '--name-only')).toContain('docs/GUIDE.md');

    const second = landBoth(fx);
    expect(git(fx.exp, 'rev-parse', `${second}^`)).toBe(first);
    r = await release(fx, [VERSION, '--resume']);
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at sign');
    expect(r.out).toContain(`tag -s ${TAG} -m "${TAG}" ${fixed}`);
    expect(r.out).toContain(`tag -s ${TAG} -m "${TAG}" ${second}`);
    expect(dispatched(fx).map((d) => d.sha)).toEqual([first, second]);
    expect(git(fx.src, 'tag', '--list', TAG)).toBe('');
    expect(run(fx.env, 'git', [`--git-dir=${fx.publicRemote}`, 'tag', '--list'])).not.toContain(TAG);
  });

  it('refuses a dry run GitHub does not name as one; fixed in a workflow, the gate tests the new public main', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    const git = (dir: string, ...args: string[]) => gitIn(fx, dir, ...args);
    await toLand(fx);
    landBoth(fx);
    let r = await release(fx, [VERSION, '--resume'], { FIXTURE_GH_NAME: 'old' });
    expect(r.code).toBe(1);
    expect(r.out).toContain('none named "dry run all');
    expect(r.out).toContain('release-verify.patch');

    // the workflow fix is the public checkout's own commit; the export on
    // top of it has nothing to add, and that commit is what gets tested
    commitAndPush(fx, fx.exp, { '.github/workflows/release.yml': 'name: Release\nrun-name: named\non: push\njobs: {}\n' }, 'ci(release): name the dry runs');
    const fixedWorkflows = git(fx.exp, 'rev-parse', 'HEAD');
    r = await release(fx, [VERSION, '--resume']);
    expect(r.out).toContain(`the export added nothing: ${fixedWorkflows.slice(0, 10)} already holds its tree`);
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at sign');
    expect(r.out).toContain(`tag -s ${TAG} -m "${TAG}" ${fixedWorkflows}`);
    expect(dispatched(fx).at(-1)).toMatchObject({ sha: fixedWorkflows, title: `dry run all ${fixedWorkflows}` });
  });

  it('waits for GitHub, and a resume carries on waiting without starting a second dry run', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    await toLand(fx);
    const exportCommit = landBoth(fx);
    // The wait runs out here on purpose: a short limit, nothing else short.
    const brief = { FIXTURE_GATE_TIMEOUT_MS: '500' };
    let r = await release(fx, [VERSION, '--resume'], { FIXTURE_GH_DRY: 'in_progress', ...brief });
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at gate');
    expect(r.out).toContain('GitHub has not finished');
    r = await release(fx, [VERSION, '--resume'], { FIXTURE_GH_CI: '', ...brief });
    expect(r.code).toBe(3);
    expect(r.out).toContain('ci.yml: none');
    r = await release(fx, [VERSION, '--resume'], { FIXTURE_GH_CI: 'failure' });
    expect(r.code).toBe(1);
    expect(r.out).toContain(`FAILED  ci.yml (push) passed on ${exportCommit.slice(0, 10)}`);
    expect(dispatched(fx)).toHaveLength(1);
  });

  // #173/#174: ci.yml is a matrix, a job per part, and its one conclusion
  // said neither which part failed nor that one never ran. The gate reads the
  // run part by part, and asks the dry run for what the tag run promotes.
  it('reads the push run part by part: a red part is named, a missing one is no full matrix, and the dry run must keep what the tag run promotes', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    await toLand(fx);
    const exportCommit = landBoth(fx);
    const M = { FIXTURE_GH_MATRIX: '1' };
    // A red part while the run still goes on: red at once, by its name.
    let r = await release(fx, [VERSION, '--resume'], { ...M, FIXTURE_GH_CI: 'in_progress', FIXTURE_GH_PARTS: 'failure:Go (b)' });
    expect(r.code).toBe(1);
    expect(r.out).toContain(`FAILED  ci.yml (push) passed on ${exportCommit.slice(0, 10)}`);
    expect(r.out).toContain('Go (b) (failure');
    expect(r.out).toContain('once that run has finished, gh run rerun 1 --failed');
    expect(r.out).toContain(`${TAG} is not spent`);
    // A run that passed without one of its parts is no full matrix.
    r = await release(fx, [VERSION, '--resume'], { ...M, FIXTURE_GH_PARTS: 'missing:All tests (full)' });
    expect(r.code).toBe(1);
    expect(r.out).toContain('it is not the full matrix');
    expect(r.out).toContain('never ran - All tests (full)');
    // A dry run that kept no image digests is no release candidate.
    r = await release(fx, [VERSION, '--resume'], { ...M, FIXTURE_GH_ARTIFACTS: 'digests-amd64,release-files-linux' });
    expect(r.code).toBe(1);
    expect(r.out).toContain(`FAILED  the dry run kept what the ${TAG} run promotes`);
    expect(r.out).toContain('kept no digests-arm64');
    // Every part green and everything kept but macOS: on to the tags, saying so.
    r = await release(fx, [VERSION, '--resume'], { ...M, FIXTURE_GH_ARTIFACTS: 'digests-amd64,digests-arm64,release-files-linux' });
    expect(r.code, r.out).toBe(3);
    expect(r.out).toContain(`ok      ci.yml (push) passed on ${exportCommit.slice(0, 10)}`);
    expect(r.out).toContain('4/4 parts green');
    expect(r.out).toContain('the fixture heavy suite: 2/2 parts green');
    expect(r.out).toContain('without release-files-macos');
    expect(r.out).toContain('WAITING at sign');
    expect(dispatched(fx)).toHaveLength(1);
    expect(gitIn(fx, fx.src, 'tag', '--list', TAG)).toBe('');
  });
});

// #181: GitHub Actions down (2026-10-05: the 0.52.0 dry run could not get a
// runner, twice, and the release went out from a PC). CircleCI schedules its
// own jobs, and `--gate circleci` lets the gate read its workflow `ci` on the
// export commit instead of ci.yml and the dry run. A person chooses it; the
// record says which CI passed the commit.
describe('pnpm release — the gate when GitHub Actions is down (--gate circleci)', () => {
  it('passes the gate on CircleCI alone: GitHub is not asked, nothing is started, and the record says CircleCI', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    await toLand(fx);
    const exportCommit = landBoth(fx);
    // GitHub has no run at all on the commit: Actions is down.
    let r = await release(fx, [VERSION, '--resume', '--gate', 'circleci'], { FIXTURE_GH_CI: '', FIXTURE_CC_CI: 'success' });
    expect(r.out).toContain('gate    circleci');
    expect(r.out).toContain('the release dry run does not run');
    expect(r.out).toContain(`ok      CircleCI ci passed on ${exportCommit.slice(0, 10)}`);
    expect(r.out).toContain('WAITING at sign');
    expect(r.out).toContain('CircleCI has tested the export commit');
    expect(r.out).toContain(`tag -s ${TAG} -m "${TAG}" ${exportCommit}`);
    expect(r.code).toBe(3);
    expect(dispatched(fx)).toEqual([]);
    expect(askedOf(fx)).toEqual({ circleci: 1 });
    const rec = recordOf(fx);
    expect(rec.gateSource).toBe('circleci');
    expect(rec.stages.gate).toMatchObject({
      status: 'done',
      source: 'circleci',
      exportHead: exportCommit,
      circleci: 'https://app.circleci.com/pipelines/gh/fixture/fixture/7/workflows/wf-1',
    });

    // a resume keeps the source and does not ask again: the commit passed
    r = await release(fx, [VERSION, '--resume'], { FIXTURE_GH_CI: '', FIXTURE_CC_CI: 'success' });
    expect(r.out).toContain('gate    circleci');
    expect(r.out).toContain('WAITING at sign');
    expect(r.code).toBe(3);
    expect(askedOf(fx)).toEqual({ circleci: 1 });
    const status = await release(fx, [VERSION, '--status']);
    expect(status.out).toContain('gate circleci');
  });

  it('a red CircleCI stops the release and spends no number; with Actions back, --gate github reads GitHub again', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    await toLand(fx);
    const exportCommit = landBoth(fx);
    for (const state of ['failed', 'failing']) {
      const r = await release(fx, [VERSION, '--resume', '--gate', 'circleci'], { FIXTURE_GH_CI: '', FIXTURE_CC_CI: state });
      expect(r.code, state).toBe(1);
      expect(r.out).toContain('STOPPED at gate');
      expect(r.out).toContain(`FAILED  CircleCI ci passed on ${exportCommit.slice(0, 10)}`);
      expect(r.out).toContain(`ended "${state}"`);
      expect(r.out).toContain(`${TAG} is not spent`);
      expect(r.out).toContain('Rerun workflow from failed');
    }
    expect(dispatched(fx)).toEqual([]);
    expect(askedOf(fx).github).toBeUndefined();
    expect(gitIn(fx, fx.src, 'tag', '--list', TAG)).toBe('');

    // Actions is back: the person names GitHub, and the gate is GitHub's again
    const r = await release(fx, [VERSION, '--resume', '--gate', 'github']);
    expect(r.out).toContain('(changed by --gate)');
    expect(r.out).toContain(`ok      ci.yml (push) passed on ${exportCommit.slice(0, 10)}`);
    expect(r.out).toContain(`ok      release.yml dry run passed on ${exportCommit.slice(0, 10)}`);
    expect(r.out).toContain('WAITING at sign');
    expect(r.out).toContain('GitHub has tested the export commit');
    expect(r.code).toBe(3);
    expect(dispatched(fx)).toHaveLength(1);
    expect(recordOf(fx).stages.gate.source).toBe('github');
  });

  it('a CircleCI that cannot be asked, or never ran the workflow, is red; one still running is waited for', { timeout: TIMEOUT }, async () => {
    const fx = fixture();
    await toLand(fx);
    const exportCommit = landBoth(fx);
    let r = await release(fx, [VERSION, '--resume', '--gate', 'circleci'], { FIXTURE_GH_CI: '', FIXTURE_CC_ERROR: '1' });
    expect(r.code).toBe(1);
    expect(r.out).toContain('FAILED  CircleCI answered');
    expect(r.out).toContain('unknown does not pass');

    // the source is kept: this resume asks CircleCI again, and no pipeline
    // ever ran the workflow on the commit
    r = await release(fx, [VERSION, '--resume'], { FIXTURE_GH_CI: '', FIXTURE_GATE_APPEAR_MS: '200' });
    expect(r.code).toBe(1);
    expect(r.out).toContain(`FAILED  CircleCI ran the workflow "ci" on ${exportCommit.slice(0, 10)}`);
    expect(r.out).toContain('Is the project set up');

    r = await release(fx, [VERSION, '--resume'], { FIXTURE_GH_CI: '', FIXTURE_CC_CI: 'running', FIXTURE_GATE_TIMEOUT_MS: '500' });
    expect(r.code).toBe(3);
    expect(r.out).toContain('WAITING at gate');
    expect(r.out).toContain('CircleCI has not finished');
    expect(r.out).toContain(`pnpm release ${VERSION} --resume --gate circleci`);
    expect(dispatched(fx)).toEqual([]);
    expect(askedOf(fx).github).toBeUndefined();
  });
});
