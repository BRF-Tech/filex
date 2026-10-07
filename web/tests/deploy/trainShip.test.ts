// filex-ship (scripts/train/ship.mjs + scripts/train/remote/*.sh): once the
// tag run is green, deploy -> everything read back in one command.
//
// ⚠ Why this exists (#178): after 0.51's tag the deploy and everything after
// it took 50-80 minutes of one person's turns in an order that lived in
// memory notes; a step forgotten was a step nobody noticed (the update feeds
// for four releases, 2026-09-05; docs.filex.sh three times). These tests hold
// the parts that decide what runs where, without a server:
//   - the steps and their order, and that the read-back at the end is the
//     release tool's own (`pnpm release X --resume --only deploy`) - no second
//     set of checks;
//   - every value that reaches a server is a plain word, and the server
//     scripts are files sent on stdin, never an inline string (2026-05-10:
//     one apostrophe in an inline script ran `rm` as root on the wrong side);
//   - the one image line of a compose file changes, and only that line;
//   - the settings example names no real host.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { parseEnvFile } from '../../../scripts/lib/settings.mjs';
import { findBash } from '../../../scripts/release/engine.mjs';
import { releaseAssets } from '../../../scripts/release/plan.mjs';
import {
  AFTER,
  DESKTOP_ASSETS,
  SAFE,
  STEPS,
  bashArray,
  buildSteps,
  newestMigration,
  parseArgs,
  personLines,
  posixPath,
  remoteRunner,
  shipSettings,
  unpublishable,
} from '../../../scripts/train/ship.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const TRAIN = path.join(REPO, 'scripts', 'train');
const REMOTE = path.join(TRAIN, 'remote');
const EXPORTER = path.join(REPO, 'scripts', 'export-public.sh');
const BASH = findBash();

const roots: string[] = [];
afterAll(() => {
  for (const r of roots) fs.rmSync(r, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
});
const tmp = () => {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-ship-test-'));
  roots.push(d);
  return d;
};

const ENV: Record<string, string> = {
  SHIP_HOST: 'app-server',
  SHIP_INSTANCES: 'demo prod',
  SHIP_DEMO_DIR: '/srv/filex-demo',
  SHIP_DEMO_PORT: '5212',
  SHIP_PROD_DIR: '/srv/filex',
  SHIP_PROD_PORT: '5213',
  SHIP_MANIFEST_CMD: 'bash /x/publish-manifest.sh',
  SHIP_DESKTOP_CMD: 'bash /x/publish-desktop.sh',
  SHIP_PURGE_CMD: 'bash /srv/purge-urls.sh',
  SHIP_FEED_URL: 'https://downloads.example.com/desktop/',
  SHIP_DOCS_SRC: '/srv/docs-src',
  SHIP_DOCS_REFRESH: 'bash /srv/docs-refresh.sh --force',
  SHIP_REVENDOR_CMD: 'bash /x/revendor.sh --apply --force',
  SHIP_EMBEDS_DEPLOY_CMD: 'bash /x/deploy-embeds.sh',
};

type Step = { id: string; title: string; person?: string[]; gate?: { sh?: string | (() => string); cmd?: string[]; check?: unknown; cwd?: string } };

function steps(env: Record<string, string> = ENV, extra: Record<string, unknown> = {}) {
  const { cfg, problems } = shipSettings(env);
  const runDir = tmp();
  const ctx = {
    repo: REPO,
    tag: 'v0.53.0',
    version: '0.53.0',
    runDir,
    stamp: '20261007-071500',
    want: 86,
    exp: '/x/filex-export',
    preflight: () => ({ ok: true }),
    releases: () => ({ ok: true }),
    npm: () => ({ ok: true }),
    ...extra,
  };
  return { cfg, problems, runDir, list: buildSteps(cfg, ctx) as Step[] };
}
const byId = (list: Step[], id: string) => list.find((s) => s.id === id)!;
const shOf = (s: Step) => (typeof s.gate?.sh === 'function' ? s.gate.sh() : (s.gate?.sh ?? ''));

describe('the settings', () => {
  it('take a complete set without a problem, and spell every instance as name:dir:port:db:service', () => {
    const { cfg, problems } = shipSettings(ENV);
    expect(problems).toEqual([]);
    expect(cfg.instances.map((i: { spec: string }) => i.spec)).toEqual(['demo:/srv/filex-demo:5212:data/instance.sqlite:filex', 'prod:/srv/filex:5213:data/instance.sqlite:filex']);
    expect(cfg.feedUrl).toBe('https://downloads.example.com/desktop');
    expect(cfg.onFail).toBe('rollback');
    expect(cfg.sshOpts).toEqual(['-o', 'BatchMode=yes', '-o', 'ConnectTimeout=20']);
  });

  it('refuse a value that a command could read two ways, and what the server steps cannot do without', () => {
    const { problems } = shipSettings({
      SHIP_INSTANCES: 'prod',
      SHIP_PROD_DIR: '/srv/my filex',
      SHIP_PROD_PORT: 'http',
      SHIP_DOCS_REFRESH: "bash -c 'refresh'",
      SHIP_ON_FAIL: 'retry',
    });
    const all = problems.join('\n');
    expect(all).toContain('SHIP_HOST');
    expect(all).toContain('SHIP_PROD_DIR');
    expect(all).toContain('SHIP_PROD_PORT');
    expect(all).toContain('SHIP_DOCS_REFRESH');
    expect(all).toContain('SHIP_ON_FAIL');
    expect(shipSettings({ SHIP_HOST: 'h' }).problems.join('\n')).toContain('SHIP_INSTANCES');
  });

  it('the example in the repository is valid as it is, and names no real host or name', () => {
    const text = fs.readFileSync(path.join(TRAIN, 'train.env.example'), 'utf8');
    expect(shipSettings(parseEnvFile(text)).problems).toEqual([]);
    expect(text).not.toMatch(/brf\.sh/);
    if (fs.existsSync(EXPORTER)) {
      for (const n of bashArray(fs.readFileSync(EXPORTER, 'utf8'), 'private_names')) expect(text.toLowerCase()).not.toContain(n.toLowerCase());
    }
  });
});

describe('the steps', () => {
  it('are these, in this order, each waiting only for steps that exist', () => {
    const { list } = steps();
    expect(list.map((s) => s.id)).toEqual(STEPS);
    expect(Object.keys(AFTER).sort()).toEqual([...STEPS].sort());
    for (const [id, deps] of Object.entries(AFTER)) for (const d of deps as string[]) expect(STEPS, `${id} waits for ${d}`).toContain(d);
  });

  it('end with the release tool reading the deploy back - its own checks, not a second set', () => {
    const { list } = steps();
    expect(byId(list, 'verify').gate!.cmd).toEqual([process.execPath, path.join(REPO, 'scripts', 'release.mjs'), '0.53.0', '--resume', '--only', 'deploy']);
    expect(AFTER.verify).toEqual(expect.arrayContaining(['deploy', 'manifest', 'desktop', 'purge', 'docs']));
    for (const s of list) if (typeof s.gate?.sh === 'string') expect(s.gate.sh, `${s.id} fetches a URL itself`).not.toMatch(/\bcurl\b/);
  });

  it('send the server scripts on stdin, one line each, every argument a plain word', () => {
    const { list } = steps();
    for (const id of ['backup', 'proxies', 'deploy']) {
      const sh = shOf(byId(list, id));
      expect(sh, id).toMatch(new RegExp(`^set -o pipefail; cat '[^']*common\\.sh' '[^']*${id}\\.sh' \\| 'ssh' '-o' 'BatchMode=yes' '-o' 'ConnectTimeout=20' 'app-server' '`));
      expect(sh, id).not.toContain('\n');
    }
    expect(shOf(byId(list, 'backup'))).toContain('/var/backups/filex/pre-v0.53.0-20261007-071500 demo:/srv/filex-demo:5212:data/instance.sqlite:filex prod:');
    expect(shOf(byId(list, 'deploy'))).toContain('bash "$f" ghcr.io/brf-tech/filex v0.53.0 86 300 rollback /var/backups/filex/pre-v0.53.0-20261007-071500 demo:');
    expect(shOf(byId(steps(ENV, { onFail: 'hold' }).list, 'deploy'))).toContain(' 86 300 hold ');
  });

  it('never hands the server a word it could read two ways', () => {
    for (const bad of ['a b', "it's", '$(id)', 'a;b', 'a|b', '`id`', '']) expect(() => remoteRunner([bad]), bad).toThrow();
    const line = remoteRunner(['/srv/x', 'v0.53.0']);
    expect(line).toBe('f=$(mktemp) && cat > "$f" && bash "$f" /srv/x v0.53.0; rc=$?; rm -f "$f"; exit $rc');
    expect(line).not.toMatch(/[#'\n]/);
    expect(SAFE.test('https://downloads.example.com/desktop/filex-desktop-x64.exe')).toBe(true);
  });

  it('purge every file the desktop step downloaded, by its public URL', () => {
    const { list, runDir } = steps();
    const purge = byId(list, 'purge');
    expect(shOf(purge)).toMatch(/exit 1/);
    fs.mkdirSync(path.join(runDir, 'desktop'));
    for (const f of ['latest.yml', 'filex-desktop-x64.exe']) fs.writeFileSync(path.join(runDir, 'desktop', f), 'x');
    const sh = shOf(purge);
    expect(sh).toContain("'bash /srv/purge-urls.sh ");
    expect(sh).toContain('https://downloads.example.com/desktop/latest.yml');
    expect(sh).toContain('https://downloads.example.com/desktop/filex-desktop-x64.exe');
  });

  it('copy the docs from the public checkout, and the summaries from here', () => {
    const sh = shOf(byId(steps().list, 'docs'));
    expect(sh).toContain(`tar -C '/x/filex-export' -czf - docs README.md CHANGELOG.md`);
    expect(sh).toContain('--exclude=node_modules');
    expect(sh).toContain(`tar -C '${posixPath(REPO)}' -czf - docs-site/data/release-highlights.json`);
    expect(sh).toContain("'bash /srv/docs-refresh.sh --force'");
    expect(sh.indexOf('docs-prepare.sh')).toBeLessThan(sh.indexOf('tar -C'));
  });

  it('leave a step whose command is not set to a person, and say so', () => {
    const env = { ...ENV };
    delete env.SHIP_MANIFEST_CMD;
    delete env.SHIP_EMBEDS_DEPLOY_CMD;
    const { list } = steps(env);
    expect(byId(list, 'manifest').person!.join(' ')).toContain('SHIP_MANIFEST_CMD');
    expect(byId(list, 'manifest').gate).toBeUndefined();
    expect(byId(list, 'embeds-deploy').person!.join(' ')).toContain('SHIP_EMBEDS_DEPLOY_CMD');
  });

  it('download every desktop file the feeds and the download links name, and no snap', () => {
    const toRe = (g: string) => new RegExp(`^${g.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*')}$`);
    const pats = DESKTOP_ASSETS.map(toRe);
    const wanted = releaseAssets('0.53.0').filter((a: string) => (a.startsWith('filex-desktop-') || a.startsWith('latest')) && !a.endsWith('.snap'));
    expect(wanted.length).toBeGreaterThan(10);
    for (const a of wanted) expect(pats.some((p) => p.test(a)), a).toBe(true);
    for (const snap of ['filex-desktop-amd64.snap', 'filex-desktop-arm64.snap']) expect(pats.some((p) => p.test(snap)), snap).toBe(false);
  });

  it("leave a person the replies, the language packs' own release step and the release's --ack deploy", () => {
    const { cfg } = shipSettings(ENV);
    const lines = personLines(cfg, { tag: 'v0.53.0', version: '0.53.0' }).join('\n');
    expect(lines).toContain('pnpm release 0.53.0 --resume --ack deploy');
    expect(lines).toContain('never "Fixes #N"');
    // #177's tool is the one release-day step for the packs; no second recipe.
    expect(lines).toContain('node scripts/langpacks.mjs release 0.53.0');
    expect(lines).not.toContain('pack.mjs sync');
    expect(personLines(cfg, { tag: 'v0.53.0', version: '0.53.0', releasesCommit: 'abcdef0123456789' }).join('\n')).toContain('abcdef0123 "docs(releases): v0.53.0"');
  });
});

describe('the small parts', () => {
  it('read the newest migration a release carries', () => {
    expect(newestMigration(['backend/db/migrations/sqlite/00085_ldap_people.sql', 'backend/db/migrations/sqlite/00086_group_admin.sql', 'backend/db/migrations/sqlite/embed.go', ''])).toBe(86);
    expect(newestMigration([])).toBe(0);
    const here = fs.readdirSync(path.join(REPO, 'backend', 'db', 'migrations', 'sqlite'));
    expect(newestMigration(here)).toBeGreaterThan(80);
  });

  it('spell a Windows path the way Git Bash takes it', () => {
    expect(posixPath('D:\\work\\.git\\filex-ship')).toBe('/d/work/.git/filex-ship');
    expect(posixPath('C:/Users/x')).toBe('/c/Users/x');
    expect(posixPath('/srv/filex')).toBe('/srv/filex');
  });

  it('take a version, with or without its v, and refuse an unknown step', () => {
    expect(parseArgs(['v0.53.0']).version).toBe('0.53.0');
    expect(() => parseArgs([])).toThrow(/X\.Y\.Z/);
    expect(() => parseArgs(['0.53.0', '--only', 'deploy,coffee'])).toThrow(/coffee/);
    expect(() => parseArgs(['0.53.0', '--on-fail', 'retry'])).toThrow(/on-fail/);
    expect(parseArgs(['0.53.0', '--only', 'docs,verify']).only).toEqual(['docs', 'verify']);
  });

  it.skipIf(!fs.existsSync(EXPORTER))('refuse a summary the public docs site must not carry', () => {
    expect(unpublishable('A release that does one thing well.')).toEqual([]);
    expect(unpublishable('See https://fm.example.com/admin for it.').join(' ')).toContain('private host');
  });
});

// ── the server scripts, with bash and no server ─────────────────────────────

const sh = (script: string, ...args: string[]) =>
  spawnSync(BASH ?? 'bash', ['-c', script, 'test', ...args], { encoding: 'utf8', env: { ...process.env, MSYS_NO_PATHCONV: '1' } });
const COMMON = posixPath(path.join(REMOTE, 'common.sh'));

describe.skipIf(!BASH)('the server scripts', () => {
  it('parse, every one of them', () => {
    for (const f of fs.readdirSync(REMOTE).filter((n) => n.endsWith('.sh'))) {
      const r = spawnSync(BASH!, ['-n', posixPath(path.join(REMOTE, f))], { encoding: 'utf8' });
      expect(r.status, `${f}: ${r.stderr}`).toBe(0);
    }
  });

  it('carry no inline ssh and no secret value', () => {
    for (const f of fs.readdirSync(REMOTE)) {
      const text = fs.readFileSync(path.join(REMOTE, f), 'utf8');
      expect(text, f).not.toMatch(/^\s*ssh\s/m);
      expect(text, f).not.toMatch(/(token|password|secret)=\S/i);
    }
  });

  it('change the one image line of a compose file, keep its variant and its quotes, and nothing else', () => {
    const d = tmp();
    const compose = path.join(d, 'docker-compose.yml');
    const pg = '    image: registry.example.com/mirror/postgres:16-alpine\n';
    fs.writeFileSync(compose, `services:\n  filex:\n    image: "ghcr.io/brf-tech/filex:slim-v0.52.0"\n  postgres:\n${pg}`);
    const refs = sh('source "$1"; image_refs "$2" ghcr.io/brf-tech/filex', COMMON, posixPath(compose));
    expect(refs.stdout.trim()).toBe('ghcr.io/brf-tech/filex:slim-v0.52.0');
    expect(sh('source "$1"; variant_of slim-v0.52.0', COMMON).stdout).toBe('slim-');
    const r = sh('source "$1"; set_image "$2" ghcr.io/brf-tech/filex ghcr.io/brf-tech/filex:slim-v0.53.0', COMMON, posixPath(compose));
    expect(r.status, r.stderr).toBe(0);
    expect(fs.readFileSync(compose, 'utf8')).toBe(`services:\n  filex:\n    image: "ghcr.io/brf-tech/filex:slim-v0.53.0"\n  postgres:\n${pg}`);
  });

  it('refuse to guess which of two image lines to change', () => {
    const d = tmp();
    const compose = path.join(d, 'docker-compose.yml');
    const text = 'services:\n  a:\n    image: ghcr.io/brf-tech/filex:v0.52.0\n  b:\n    image: ghcr.io/brf-tech/filex:v0.52.0\n';
    fs.writeFileSync(compose, text);
    const r = sh('source "$1"; set_image "$2" ghcr.io/brf-tech/filex ghcr.io/brf-tech/filex:v0.53.0', COMMON, posixPath(compose));
    expect(r.status).not.toBe(0);
    expect(r.stderr).toContain('exactly one');
    expect(fs.readFileSync(compose, 'utf8')).toBe(text);
  });

  it('refuse an instance whose directory is not an absolute path below /', () => {
    const r = sh('source "$1"; parse_instance "prod:relative/dir:5213:data/instance.sqlite:filex"', COMMON);
    expect(r.status).not.toBe(0);
    expect(r.stderr).toContain('absolute path');
  });

  const py = BASH ? sh('python3 -c "print(1)"').stdout.trim() === '1' : false;
  it.skipIf(!py)('compare a gateway with FILEX_TRUSTED_PROXIES, addresses and ranges', () => {
    expect(sh('source "$1"; trusted_covers "auto, 192.168.96.1" 192.168.96.1', COMMON).status).toBe(0);
    expect(sh('source "$1"; trusted_covers "auto, 192.168.64.0/18" 192.168.96.1', COMMON).status).toBe(0);
    const miss = sh('source "$1"; trusted_covers "auto, 10.0.0.0/8" 192.168.96.1', COMMON);
    expect(miss.status).toBe(1);
    expect(miss.stdout.trim()).toBe('192.168.96.1');
    expect(sh('source "$1"; trusted_covers "" 192.168.96.1', COMMON).status).toBe(1);
  });
});
