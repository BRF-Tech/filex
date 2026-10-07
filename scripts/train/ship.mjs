#!/usr/bin/env node
// filex-ship: once the tag run is green, deploy -> done in one command.
//
//   bash scripts/train/filex-ship.sh X.Y.Z [options]     (node scripts/train/ship.mjs)
//
//   --plan            every step and its command, what is not set up; runs nothing
//   --resume          carry on after a stop: a step that passed is not run again
//                     (backup, proxies and deploy run again together while the
//                     deploy has not passed: a backup is taken for every attempt)
//   --only a,b        just these steps, by id
//   --on-fail hold    a failed instance is left as it failed (default: rollback)
//   --jobs N          steps at once (default 4)
//   --env FILE        settings (default: $FILEX_TRAIN_ENV, else
//                     ~/.config/filex-train.env; the keys and what each one
//                     does: scripts/train/train.env.example)
//
// The steps, by id - each in its own log under <git dir>/filex-ship/<tag>/logs:
//   preflight  the tag on both remotes, the public checkout, the release record
//   backup     every instance: compose file, settings, a checked SQLite copy
//   proxies    every instance's network gateway is in FILEX_TRUSTED_PROXIES
//   deploy     instance by instance: image, /healthz, migrations, gateway;
//              a failed one is rolled back and the rest are not touched
//   manifest   the update manifest servers and the CLI read
//   desktop    the release's desktop packages and feeds onto the feed host
//   purge      those files out of the CDN's cache (the names never change)
//   releases   the Releases page regenerated; committed when it changed
//   docs       docs.filex.sh: the snapshot copied, the public pages in, rebuilt
//   npm        waits until npm serves every package of the release
//   embeds     the vendored explorer moved to the release (and deployed)
//   verify     pnpm release X.Y.Z --resume --only deploy - the release tool's
//              own read-back, the same checks, nothing second
//
// Exit: 0 every step green and read back (what is left to a person - the
// replies, the language packs, `--ack deploy` - is printed at the end); 1 a
// step is red; 2 it could not start.
//
// ⚠ Why this exists (#178, measured in #165): after 0.51's tag the deploy and
// everything after it - a backup by hand, the gateway measured by hand, the
// deploy script, the manifest, the desktop feed, the Releases page, the docs
// snapshot, npm, the embeds - took 50-80 minutes of one person's turns, in an
// order that lived in memory notes, and a step forgotten was a step nobody
// noticed (the feeds, 2026-09-05; docs.filex.sh, three times). The aim is
// deploy -> everything read back in 20 minutes, with a log per step.
//
// ⚠ Nothing here names a server or holds a secret. Hosts, directories, ports
// and the commands that publish come from the settings file; a key is a file
// a command reads. The server steps are files sent over ssh on stdin
// (scripts/train/remote/*.sh), never a script written inline into an ssh
// command (2026-05-10: one apostrophe in an inline script, a root `rm`).

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { neutralize, exportRules } from '../../docs-site/scripts/neutralize.mjs';
import { announce } from '../lib/notify.mjs';
import { listSetting, loadSettings } from '../lib/settings.mjs';
import { SEMVER } from '../release/checks.mjs';
import {
  Gates,
  banner,
  bold,
  dim,
  findBash,
  git,
  gitOut,
  green,
  loadState,
  lsRemote,
  red,
  revParse,
  run,
  saveState,
  shq,
  slash,
  stateFile,
  takeLock,
  yellow,
} from '../release/engine.mjs';
import { npmPackages } from '../release/plan.mjs';
import { duration, since, usageOf } from './cli.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(HERE, '..', '..');
const REMOTE = path.join(HERE, 'remote');

export const STEPS = ['preflight', 'backup', 'proxies', 'deploy', 'manifest', 'desktop', 'purge', 'releases', 'docs', 'npm', 'embeds', 'verify'];
/** Run again together while `deploy` has not passed: a backup per attempt. */
export const SERVER_STEPS = ['backup', 'proxies', 'deploy'];

/** What each step waits for. A step whose dependency is red is not run. */
export const AFTER = {
  preflight: [],
  backup: ['preflight'],
  proxies: ['backup'],
  deploy: ['proxies'],
  manifest: ['deploy'],
  desktop: ['deploy'],
  purge: ['desktop'],
  releases: ['preflight'],
  docs: ['releases'],
  npm: ['preflight'],
  embeds: ['npm', 'deploy'],
  verify: ['deploy', 'manifest', 'desktop', 'purge', 'docs'],
};

/**
 * What `gh release download` takes for the desktop feed: the installers and
 * packages, their blockmaps and the feeds. Not the snaps (the Snap Store
 * serves those) and not the CLI archives.
 */
export const DESKTOP_ASSETS = [
  'latest*.yml',
  'filex-desktop-*.exe',
  'filex-desktop-*.AppImage',
  'filex-desktop-*.deb',
  'filex-desktop-*.rpm',
  'filex-desktop-*.dmg',
  'filex-desktop-*.zip',
  'filex-desktop-*.blockmap',
];

/**
 * A value that goes into a command as it is: no space, no quote, nothing a
 * shell gives a meaning to. Every host, path, port and name the server steps
 * receive is one, so a command line can never be read two ways.
 */
export const SAFE = /^[A-Za-z0-9_./:@%+=,~-]+$/;

/** A Windows path as Git Bash spells it (G:\a\b -> /g/a/b); anything else as it is. */
export function posixPath(p) {
  const s = String(p).split('\\').join('/');
  const m = /^([A-Za-z]):\/(.*)$/.exec(s);
  return m ? `/${m[1].toLowerCase()}/${m[2]}` : s;
}

/** The newest migration number among file names (00086_x.sql -> 86); 0 for none. */
export function newestMigration(names) {
  let max = 0;
  for (const n of names) {
    const m = /(?:^|\/)(\d+)_[^/]*\.sql$/.exec(String(n).trim());
    if (m) max = Math.max(max, Number(m[1]));
  }
  return max;
}

/**
 * The ship's settings from the merged settings map. Returns { cfg, problems }:
 * a problem is a value that is wrong, or a setting the server steps cannot do
 * without. A publishing command that is not set leaves its step to a person.
 */
export function shipSettings(env) {
  const problems = [];
  const str = (k, def = '') => String(env[k] ?? def).trim();
  const safe = (k, v) => {
    if (v && !SAFE.test(v)) problems.push(`${k}="${v}": only letters, digits and _ . / : @ % + = , ~ - (it goes into a command as it is)`);
    return v;
  };
  // A command run on the server is written inside single quotes.
  const remoteCmd = (k) => {
    const v = str(k);
    if (v.includes("'") || /[\r\n]/.test(v)) problems.push(`${k}: no single quote and no line break (it runs on the server inside single quotes)`);
    return v;
  };
  const host = safe('SHIP_HOST', str('SHIP_HOST'));
  if (!host) problems.push('SHIP_HOST: the ssh name of the server the instances run on');
  const instances = [];
  const names = listSetting(env.SHIP_INSTANCES);
  if (names.length === 0) problems.push('SHIP_INSTANCES: the instances to deploy, in order (the demo first: it is the canary)');
  for (const name of names) {
    if (!/^[A-Za-z][A-Za-z0-9_]*$/.test(name)) {
      problems.push(`SHIP_INSTANCES: "${name}" - a name is letters, digits and _`);
      continue;
    }
    const k = (s) => `SHIP_${name.toUpperCase()}_${s}`;
    const dir = str(k('DIR'));
    const port = str(k('PORT'));
    const db = str(k('DB'), 'data/instance.sqlite');
    const service = str(k('SERVICE'), 'filex');
    if (!dir.startsWith('/') || !SAFE.test(dir) || dir.includes(':')) problems.push(`${k('DIR')}: the instance's compose directory, an absolute path`);
    if (!/^\d+$/.test(port)) problems.push(`${k('PORT')}: the host port its /healthz answers on`);
    if (db !== 'none' && (!SAFE.test(db) || db.startsWith('/') || db.includes('..') || db.includes(':'))) problems.push(`${k('DB')}: its SQLite file, relative to the directory (or none)`);
    if (!SAFE.test(service) || service.includes(':')) problems.push(`${k('SERVICE')}: its compose service`);
    instances.push({ name, dir, port, db, service, spec: `${name}:${dir}:${port}:${db}:${service}` });
  }
  const backupRoot = str('SHIP_BACKUP_ROOT', '/var/backups/filex');
  if (!backupRoot.startsWith('/') || !SAFE.test(backupRoot)) problems.push('SHIP_BACKUP_ROOT: an absolute directory on the server');
  const healthWait = Number(str('SHIP_HEALTH_WAIT', '300'));
  if (!Number.isInteger(healthWait) || healthWait < 10) problems.push('SHIP_HEALTH_WAIT: seconds, at least 10');
  const onFail = str('SHIP_ON_FAIL', 'rollback');
  if (!['rollback', 'hold'].includes(onFail)) problems.push('SHIP_ON_FAIL: rollback or hold');
  const image = safe('SHIP_IMAGE', str('SHIP_IMAGE', 'ghcr.io/brf-tech/filex'));
  const feedUrl = safe('SHIP_FEED_URL', str('SHIP_FEED_URL').replace(/\/+$/, ''));
  const docsSrc = safe('SHIP_DOCS_SRC', str('SHIP_DOCS_SRC'));
  if (docsSrc && !docsSrc.startsWith('/')) problems.push('SHIP_DOCS_SRC: an absolute directory on the server');
  const npmWait = Number(str('SHIP_NPM_WAIT', '20'));
  if (!Number.isFinite(npmWait) || npmWait < 0) problems.push('SHIP_NPM_WAIT: minutes');
  const cfg = {
    host,
    sshOpts: str('SHIP_SSH_OPTS', '-o BatchMode=yes -o ConnectTimeout=20').split(/\s+/).filter(Boolean),
    image,
    instances,
    backupRoot,
    healthWait,
    onFail,
    manifestCmd: str('SHIP_MANIFEST_CMD'),
    desktopCmd: str('SHIP_DESKTOP_CMD'),
    desktopAssets: env.SHIP_DESKTOP_ASSETS ? listSetting(env.SHIP_DESKTOP_ASSETS) : DESKTOP_ASSETS,
    feedUrl,
    purgeCmd: remoteCmd('SHIP_PURGE_CMD'),
    docsSrc,
    docsRefresh: remoteCmd('SHIP_DOCS_REFRESH'),
    exportDir: str('SHIP_EXPORT_DIR'),
    githubRepo: safe('SHIP_GITHUB_REPO', str('SHIP_GITHUB_REPO', 'BRF-Tech/filex')),
    npmWait,
    revendorCmd: str('SHIP_REVENDOR_CMD'),
    embedsDeployCmd: str('SHIP_EMBEDS_DEPLOY_CMD'),
  };
  for (const opt of cfg.sshOpts) if (!SAFE.test(opt)) problems.push(`SHIP_SSH_OPTS: "${opt}" - only plain words and -o Key=value`);
  return { cfg, problems };
}

/** ssh to the server, as a shell words. */
export const sshWords = (cfg) => ['ssh', ...cfg.sshOpts, cfg.host].map(shq).join(' ');

/**
 * What the server runs for a step file: save stdin to a file, run it, remove
 * it, answer with its exit code. One line, no comment, no apostrophe; the
 * arguments are SAFE words.
 */
export function remoteRunner(args) {
  for (const a of args) if (!SAFE.test(String(a))) throw new Error(`"${a}" cannot go into a command as it is`);
  return `f=$(mktemp) && cat > "$f" && bash "$f" ${args.join(' ')}; rc=$?; rm -f "$f"; exit $rc`;
}

/** The shell that sends common.sh and one step file to the server and runs them there. */
export function sshScript(cfg, step, args) {
  const files = [path.join(REMOTE, 'common.sh'), path.join(REMOTE, `${step}.sh`)].map((f) => shq(posixPath(f)));
  return `cat ${files.join(' ')} | ${sshWords(cfg)} ${shq(remoteRunner(args))}`;
}

/** `name=(a b c)` of a bash script, as a list. */
export function bashArray(text, name) {
  const m = new RegExp(`\\n${name}=\\(([\\s\\S]*?)\\)`).exec(`\n${text}`);
  return m ? m[1].split(/\s+/).filter((w) => w && !w.startsWith('#')) : [];
}

/**
 * What would stop `text` from going to a public site as it is: a host or a
 * path the export rewrites, a server address, a private name. Read from
 * scripts/export-public.sh itself (the one list); in a checkout without it
 * there is nothing to read and nothing is refused.
 */
export function unpublishable(text, { exportScript = path.join(REPO, 'scripts', 'export-public.sh') } = {}) {
  if (!fs.existsSync(exportScript)) return [];
  const out = [];
  const rules = exportRules(exportScript);
  if (neutralize(text, rules) !== text) out.push('it names a private host, path or address (the export would rewrite it)');
  const names = bashArray(fs.readFileSync(exportScript, 'utf8'), 'private_names');
  for (const n of names) if (new RegExp(`\\b${n}\\b`, 'i').test(text)) out.push(`it names "${n}" (private_names)`);
  return out;
}

/**
 * The steps as gate specs, with their ids, in order. A step whose setting is
 * missing is not a gate: it is a person's (`person` lines).
 */
export function buildSteps(cfg, ctx) {
  const { repo, tag, version, runDir, stamp, want, exp } = ctx;
  const ssh = sshWords(cfg);
  const env = { FILEX_SHIP_TAG: tag, FILEX_SHIP_VERSION: version, FILEX_SHIP_REPO: repo, FILEX_SHIP_EXPORT: exp, FILEX_SHIP_RUN: runDir };
  const specs = cfg.instances.map((i) => i.spec);
  const bk = `${cfg.backupRoot}/pre-${tag}-${stamp}`;
  const dl = `${posixPath(runDir)}/desktop`;
  const steps = [];
  const add = (id, title, spec) => steps.push({ id, title, gate: { cwd: runDir, env, ...spec } });
  const person = (id, title, lines) => steps.push({ id, title, person: lines });

  add('preflight', 'the tag, the public checkout, the release record, the server', { check: ctx.preflight });
  add('backup', 'every instance copied, the database checked', { sh: `set -o pipefail; ${sshScript(cfg, 'backup', [bk, ...specs])}` });
  add('proxies', 'every gateway in FILEX_TRUSTED_PROXIES', { sh: `set -o pipefail; ${sshScript(cfg, 'proxies', specs)}` });
  add('deploy', `instance by instance: ${cfg.instances.map((i) => i.name).join(', ')}`, {
    sh: `set -o pipefail; ${sshScript(cfg, 'deploy', [cfg.image, tag, String(want), String(cfg.healthWait), ctx.onFail ?? cfg.onFail, bk, ...specs])}`,
  });

  if (cfg.manifestCmd) add('manifest', 'the update manifest', { sh: cfg.manifestCmd });
  else person('manifest', 'the update manifest', ['not set up (SHIP_MANIFEST_CMD): publish the update manifest by hand - CONTRIBUTING, Release process, step 9']);

  if (cfg.desktopCmd) {
    const pats = cfg.desktopAssets.map((p) => `-p ${shq(p)}`).join(' ');
    add('desktop', 'the desktop packages and feeds', {
      sh: `set -o pipefail; rm -rf ${shq(dl)} && mkdir -p ${shq(dl)} && gh release download ${shq(tag)} -R ${shq(cfg.githubRepo)} -D ${shq(dl)} ${pats} && ls -l ${shq(dl)} && ${cfg.desktopCmd} ${shq(dl)}`,
    });
  } else {
    person('desktop', 'the desktop feed', ['not set up (SHIP_DESKTOP_CMD): publish the desktop feed by hand - CONTRIBUTING, Release process, step 9']);
  }

  if (cfg.desktopCmd && cfg.purgeCmd && cfg.feedUrl) {
    add('purge', 'the feed files out of the CDN cache', {
      sh: () => {
        const dir = path.join(runDir, 'desktop');
        const names = fs.existsSync(dir) ? fs.readdirSync(dir).filter((n) => SAFE.test(n)) : [];
        if (names.length === 0) return 'echo "no desktop files to purge (the desktop step downloads them)" >&2; exit 1';
        const urls = names.map((n) => `${cfg.feedUrl}/${n}`);
        return `${ssh} ${shq(`${cfg.purgeCmd} ${urls.join(' ')}`)}`;
      },
    });
  } else if (cfg.desktopCmd) {
    person('purge', 'the CDN cache', [
      'not set up (SHIP_PURGE_CMD, SHIP_FEED_URL): if the feed host is behind a CDN, purge every desktop file by URL -',
      'their names never change, and a stale package fails the sha512 its new feed promises',
    ]);
  }

  add('releases', 'the Releases page', { cwd: repo, check: ctx.releases });

  if (cfg.docsSrc && cfg.docsRefresh) {
    const src = cfg.docsSrc;
    const into = shq(`tar xzf - -C ${src}`);
    const e = shq(posixPath(exp));
    add('docs', 'docs.filex.sh', {
      sh: [
        `set -o pipefail; ${sshScript(cfg, 'docs-prepare', [src, stamp])}`,
        `tar -C ${e} -czf - docs README.md CHANGELOG.md | ${ssh} ${into}`,
        `tar -C ${e} -czf - --exclude=node_modules --exclude=.vitepress/dist --exclude=.vitepress/cache docs-site | ${ssh} ${into}`,
        `tar -C ${shq(posixPath(repo))} -czf - docs-site/data/release-highlights.json | ${ssh} ${into}`,
        `${ssh} ${shq(cfg.docsRefresh)}`,
      ].join(' && '),
    });
  } else {
    person('docs', 'docs.filex.sh', ['not set up (SHIP_DOCS_SRC, SHIP_DOCS_REFRESH): refresh docs.filex.sh by hand - CONTRIBUTING, Release process, step 11']);
  }

  add('npm', 'npm serves every package', { check: ctx.npm });

  if (cfg.revendorCmd) {
    add('embeds', 'the vendored explorer', { sh: cfg.embedsDeployCmd ? `${cfg.revendorCmd} && ${cfg.embedsDeployCmd}` : cfg.revendorCmd });
    if (!cfg.embedsDeployCmd) {
      person('embeds-deploy', 'the embeds', [
        'the re-vendored explorer is staged in the embedding repositories, not deployed: open the diff,',
        'commit it and deploy those surfaces, then open each and see the explorer load (SHIP_EMBEDS_DEPLOY_CMD runs this part)',
      ]);
    }
  } else {
    person('embeds', 'the embeds', ['not set up (SHIP_REVENDOR_CMD): re-vendor and deploy the embedded explorer by hand']);
  }

  add('verify', 'the release tool reads everything back', {
    cwd: repo,
    cmd: [process.execPath, path.join(repo, 'scripts', 'release.mjs'), version, '--resume', '--only', 'deploy'],
  });
  return steps;
}

/** The person's part, always: what no step here can do or read back. */
export function personLines(cfg, { tag, version, releasesCommit = null }) {
  const out = [];
  // The language packs' release-day step is a tool of its own (#177;
  // CONTRIBUTING, Release process, step 13): it commits locally, prints the
  // signed tags and the pushes, and holds back a pack still missing a
  // translation - so a person runs it, and then what it prints.
  out.push(`  language packs:  node scripts/langpacks.mjs release ${version}   then the tag and push commands it prints, pack by pack`, '');
  if (releasesCommit) out.push(`  push ${releasesCommit.slice(0, 10)} "docs(releases): ${tag}" with the next push of main; the public repository gets it with the next export`, '');
  out.push('  reply on the issues and pull requests this release answers (by hand, never "Fixes #N")', '');
  out.push(`  then:  pnpm release ${version} --resume --ack deploy`);
  return out;
}

// ── the run ─────────────────────────────────────────────────────────────────

export function parseArgs(argv) {
  const o = { version: null, plan: false, resume: false, only: null, onFail: null, jobs: 4, env: '' };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => {
      const v = argv[++i];
      if (v === undefined) throw new Error(`${a} needs a value`);
      return v;
    };
    if (a === '--plan') o.plan = true;
    else if (a === '--resume') o.resume = true;
    else if (a === '--only') o.only = next().split(',').map((s) => s.trim()).filter(Boolean);
    else if (a === '--on-fail') o.onFail = next();
    else if (a === '--jobs') o.jobs = Number(next());
    else if (a === '--env') o.env = next();
    else if (a === '-h' || a === '--help') o.help = true;
    else if (a.startsWith('-')) throw new Error(`unknown option ${a}`);
    else if (!o.version) o.version = a.replace(/^v/, '');
    else throw new Error(`one version, not "${a}" too`);
  }
  if (o.help) return o;
  if (!o.version || !SEMVER.test(o.version)) throw new Error('the release to ship, as X.Y.Z');
  if (o.onFail && !['rollback', 'hold'].includes(o.onFail)) throw new Error('--on-fail rollback|hold');
  if (!Number.isInteger(o.jobs) || o.jobs < 1) throw new Error('--jobs: a whole number, 1 or more');
  if (o.only) for (const id of o.only) if (!STEPS.includes(id)) throw new Error(`--only ${id}: the steps are ${STEPS.join(', ')}`);
  return o;
}

async function waitForNpm(packages, version, minutes, log) {
  const until = Date.now() + minutes * 60_000;
  for (;;) {
    const missing = [];
    for (const p of packages) {
      try {
        const res = await fetch(`https://registry.npmjs.org/${p.replace('/', '%2f')}/${version}`, { headers: { accept: 'application/json' }, signal: AbortSignal.timeout(20_000) });
        if (res.status !== 200) missing.push(`${p} (HTTP ${res.status})`);
      } catch (e) {
        missing.push(`${p} (${e?.message ?? e})`);
      }
    }
    if (missing.length === 0) return { ok: true, detail: `${packages.length} package(s) at ${version}: ${packages.join(', ')}` };
    if (Date.now() >= until) return { ok: false, detail: `after ${minutes} min npm still does not serve ${version} of: ${missing.join(', ')} - look at the release run's npm job (the registry can lag ~10 min)` };
    log(`npm: waiting for ${missing.length} package(s)`);
    await new Promise((r) => setTimeout(r, 30_000));
  }
}

async function main(argv) {
  let o;
  try {
    o = parseArgs(argv);
  } catch (e) {
    console.error(`ship: ${e.message}\n\n${usageOf(import.meta.url)}`);
    return 2;
  }
  if (o.help) {
    console.log(usageOf(import.meta.url));
    return 0;
  }
  const version = o.version;
  const tag = `v${version}`;
  const settings = loadSettings({ file: o.env, envVar: 'FILEX_TRAIN_ENV', fallback: 'filex-train.env' });
  const { cfg, problems } = shipSettings(settings.env);
  const repo = REPO;
  const exp = path.resolve(cfg.exportDir || path.join(repo, '..', 'filex-export'));
  const gitDir = gitOut(repo, 'rev-parse', '--absolute-git-dir');
  const home = path.join(gitDir, 'filex-ship');
  const runDir = path.join(home, tag);
  const stFile = path.join(home, `${tag}.json`);
  const bash = findBash();
  const want = newestMigration(git(repo, 'ls-tree', '--name-only', `${tag}^{commit}`, 'backend/db/migrations/sqlite/').stdout.split('\n'));

  console.log(`${bold(`filex ship ${tag}`)} ${dim(`settings: ${settings.file ? slash(settings.file) : 'environment only'}`)}`);
  if (problems.length) {
    console.log(`\n${red(bold('NOT STARTED'))}: the settings are not complete\n${problems.map((p) => `  - ${p}`).join('\n')}\n  ${dim('the keys: scripts/train/train.env.example')}`);
    return 2;
  }

  const recorded = loadState(stFile);
  if (!o.plan && recorded && !o.resume && !o.only) {
    console.log(`\na ship of ${tag} is recorded (${slash(stFile)}): carry on with --resume, or run single steps with --only`);
    return 2;
  }
  const state = recorded ?? { tag, version, started: new Date().toISOString(), steps: {}, attempts: 0 };
  const deployGreen = state.steps.deploy?.ok === true;
  // A backup per attempt: a new stamp whenever the server steps run again.
  if (!state.stamp || !deployGreen) state.stamp = new Date().toISOString().replace(/[-:]/g, '').replace('T', '-').slice(0, 15);

  const log = (m) => console.log(`  ${dim(m)}`);
  const releases = { commit: null };
  const ctx = {
    repo,
    tag,
    version,
    runDir,
    stamp: state.stamp,
    want,
    exp,
    onFail: o.onFail,
    preflight: () => preflight({ cfg, repo, exp, tag, gitDir, want, runDir, bash }),
    releases: () => releasesPage({ repo, tag, version, bash, releases }),
    npm: () => waitForNpm(npmPackages(repo), version, cfg.npmWait, log),
  };
  const steps = buildSteps(cfg, ctx);

  if (o.plan) {
    console.log('');
    for (const s of steps) {
      const g = s.gate;
      const what = s.person ? yellow('a person') : g.check ? dim('(checked here)') : dim(typeof g.sh === 'function' ? '(built when it runs)' : g.sh ?? (g.cmd ?? []).join(' '));
      console.log(`  ${s.id.padEnd(14)} ${s.title}\n  ${''.padEnd(14)} ${what}${AFTER[s.id]?.length ? dim(`  after ${AFTER[s.id].join(', ')}`) : ''}`);
      if (s.person) for (const l of s.person) console.log(`  ${''.padEnd(14)} ${dim(l)}`);
    }
    console.log(`\n${bold('then a person:')}\n${personLines(cfg, { tag, version }).join('\n')}`);
    return 0;
  }

  const lock = takeLock(`${stFile}.lock`);
  if (lock.held) {
    console.error(`ship: another ship of ${tag} is running (pid ${lock.held}). One at a time.`);
    return 2;
  }
  process.on('exit', () => lock.release?.());
  fs.mkdirSync(runDir, { recursive: true });
  state.attempts = (state.attempts ?? 0) + 1;
  saveState(stFile, state);

  const t0 = Date.now();
  const names = Object.fromEntries(steps.map((s) => [s.id, `${s.id}: ${s.title}`]));
  const gates = steps.filter((s) => s.gate && (!o.only || o.only.includes(s.id)));
  const rerunServer = !deployGreen;
  const specs = gates.map((s) => {
    const after = (AFTER[s.id] ?? []).filter((id) => names[id]).map((id) => names[id]);
    const prev = state.steps[s.id];
    const keep = o.resume && !o.only && prev?.ok && !(rerunServer && SERVER_STEPS.includes(s.id));
    if (keep) return { name: names[s.id], after, check: () => ({ ok: true, detail: `passed at ${prev.at}${prev.detail ? ` - ${prev.detail}` : ''}` }) };
    return { ...s.gate, name: names[s.id], after, lane: SERVER_STEPS.includes(s.id) ? 'server' : undefined };
  });
  const record = new Map();
  const runner = new Gates({
    logsDir: path.join(runDir, 'logs'),
    bash,
    repo,
    onRecord: (rec) => {
      const id = rec.name.split(':')[0];
      record.set(id, rec);
      state.steps[id] = { ok: rec.ok, at: rec.at, secs: rec.secs, log: rec.log, detail: String(rec.detail ?? '').split('\n')[0].slice(0, 200) };
      saveState(stFile, state);
    },
  });
  banner(`ship ${tag}`, dim(`${specs.length} step(s), at most ${o.jobs} at once; logs ${slash(path.join(runDir, 'logs'))}`));
  const ok = await runner.all('ship', specs, {}, { jobs: o.jobs });

  // ── the end ───────────────────────────────────────────────────────────────
  const wall = (Date.now() - t0) / 1000;
  const persons = steps.filter((s) => s.person);
  console.log(`\n${bold(`ship ${tag}`)} ${dim(`(${duration(wall)})`)}`);
  for (const s of steps) {
    const r = record.get(s.id);
    if (s.person) console.log(`  ${yellow('person')}  ${s.id.padEnd(10)} ${dim(s.title)}`);
    else if (r) console.log(`  ${r.ok ? green('ok    ') : red('RED   ')}  ${s.id.padEnd(10)} ${dim(`${duration(r.secs)}  ${slash(r.log)}`)}`);
  }
  const lines = [];
  for (const s of persons) lines.push(...s.person.map((l) => `  ${s.id}: ${l}`));
  if (lines.length) lines.push('');
  lines.push(...personLines(cfg, { tag, version, releasesCommit: releases.commit }));
  if (ok && wall > 20 * 60) console.log(`  ${yellow('note  ')}  ${duration(wall)} - the aim is 20 minutes from deploy to everything read back (#178)`);
  if (ok) {
    console.log(`\n${green(bold(`${tag} is deployed and every check read it back.`))} ${bold('What is left is a person\'s:')}\n${lines.join('\n')}`);
  } else {
    console.log(`\n${red(bold(`STOPPED: a step is red`))} - fix what its log says and run: bash scripts/train/filex-ship.sh ${version} --resume`);
  }
  const reds = [...record.values()].filter((r) => !r.ok).map((r) => `RED ${r.name}: ${String(r.detail ?? '').split('\n')[0]}`);
  await announce(
    settings.env,
    {
      title: `filex ship ${tag}: ${ok ? 'deployed and read back' : 'RED'} in ${since(t0)}`,
      message: [...reds, ...(ok ? ['What is left is a person\'s:', ...lines] : [`logs: ${slash(path.join(runDir, 'logs'))}`])].join('\n'),
      result: ok ? 'ok' : 'red',
    },
    { log },
  );
  return ok ? 0 : 1;
}

/** preflight: what the ship stands on, read before anything moves. */
async function preflight({ cfg, repo, exp, tag, gitDir, want, runDir, bash }) {
  const problems = [];
  const notes = [];
  const priv = revParse(repo, `${tag}^{commit}`);
  if (!priv) problems.push(`${tag} is not a tag of ${slash(repo)}`);
  if (!fs.existsSync(exp)) problems.push(`no public checkout at ${slash(exp)} (SHIP_EXPORT_DIR)`);
  else {
    if (!revParse(exp, `${tag}^{commit}`)) problems.push(`${tag} is not a tag of the public checkout ${slash(exp)} - fetch its tags`);
    const dirty = git(exp, 'status', '--porcelain').stdout.trim();
    if (dirty) problems.push(`the public checkout has changes; the docs step copies from it:\n${dirty}`);
    const remote = lsRemote(exp, 'origin', ['--tags']);
    if (remote.error) problems.push(`could not ask the public remote for ${tag}: ${remote.error}`);
    else if (!remote.map.has(`refs/tags/${tag}`)) problems.push(`${tag} is not on the public remote yet - the ship runs after the tag run`);
  }
  if (!fs.existsSync(stateFile(gitDir, tag))) problems.push(`no record of the release of ${tag} here (${slash(stateFile(gitDir, tag))}): the verify step is the release tool's own read-back, run where the release was cut`);
  for (const f of ['common.sh', 'backup.sh', 'proxies.sh', 'deploy.sh', 'docs-prepare.sh']) if (!fs.existsSync(path.join(REMOTE, f))) problems.push(`scripts/train/remote/${f} is missing`);
  if (want > 0) notes.push(`the release carries migrations up to ${want}`);
  else notes.push('no migration found at the tag: the deploy does not check the migration number');
  const reach = run(bash ?? 'bash', ['-c', `${sshWords(cfg)} true`], { cwd: runDir });
  if (reach.status !== 0) problems.push(`ssh ${cfg.host} does not answer: ${(reach.stderr || reach.stdout).trim().split('\n').slice(-3).join(' / ')}`);
  else notes.push(`${cfg.host} answers`);
  return problems.length ? { ok: false, detail: problems.join('\n') } : { ok: true, detail: notes.join('; ') };
}

/**
 * The Releases page: the release's hand-written summary is there and
 * publishable, the page is regenerated, and when it changed it is committed
 * here (CONTRIBUTING, Release process, step 10). Nothing is pushed.
 */
function releasesPage({ repo, tag, version, bash, releases }) {
  const hl = path.join(repo, 'docs-site', 'data', 'release-highlights.json');
  let text = '';
  let entry = null;
  try {
    text = fs.readFileSync(hl, 'utf8');
    entry = JSON.parse(text)[tag];
  } catch (e) {
    return { ok: false, detail: `docs-site/data/release-highlights.json cannot be read: ${e?.message ?? e}` };
  }
  if (typeof entry !== 'string' || !entry.trim()) {
    return { ok: false, detail: `docs-site/data/release-highlights.json has no "${tag}" summary: write one paragraph from the CHANGELOG first (CONTRIBUTING, Release process, step 10), then --resume` };
  }
  const bad = unpublishable(text);
  if (bad.length) return { ok: false, detail: `release-highlights.json is copied to the public docs site as it is, and ${bad.join('; ')}` };
  const gen = run(bash ?? 'bash', ['-c', 'npm run releases'], { cwd: path.join(repo, 'docs-site') });
  if (gen.status !== 0) return { ok: false, detail: `npm run releases failed:\n${(gen.stderr || gen.stdout).trim().split('\n').slice(-12).join('\n')}` };
  const files = ['docs/RELEASES.md', 'docs-site/data/releases.json', 'docs-site/data/release-highlights.json'];
  const changed = git(repo, 'status', '--porcelain', '--', ...files).stdout.trim();
  if (!changed) return { ok: true, detail: `the Releases page already shows ${tag}` };
  const branch = git(repo, 'symbolic-ref', '--short', '-q', 'HEAD').stdout.trim();
  if (branch !== 'main') return { ok: false, detail: `the Releases page changed, and ${slash(repo)} is on ${branch || 'a detached HEAD'}, not main: commit it on main by hand (docs(releases): ${tag})` };
  const c = git(repo, 'commit', '-m', `docs(releases): ${tag}`, '--', ...files);
  if (c.status !== 0) return { ok: false, detail: `git commit failed: ${(c.stderr || c.stdout).trim()}` };
  releases.commit = revParse(repo, 'HEAD');
  return { ok: true, detail: `committed ${releases.commit.slice(0, 10)} docs(releases): ${tag} (${version}); not pushed` };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (e) => {
      console.error(`ship: ${e?.stack ?? e}`);
      process.exit(1);
    },
  );
}
