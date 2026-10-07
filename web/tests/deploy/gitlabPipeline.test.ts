// The private repository's pipeline (.gitlab-ci.yml) and its runner
// (scripts/gitlab-runner/): what runs there, where, and what never leaves.
//
// ⚠ Why (#182, 2026-10-06): the private project gets its own runner on the
// build host for two things - embargoed security branches (sec/<name>),
// which must pass the full chain before they reach main or the public
// repository, and the last release gate when GitHub and CircleCI are both
// down. Not the nightly run: the build host's own systemd timer starts that
// (scripts/chain/systemd/filex-nightly.timer, #175) and is its one trigger;
// a GitLab schedule beside it would run the same chain twice a night on the
// same host and lock. Until then .gitlab-ci.yml carried a second copy of
// the suites (lint/test/e2e/build/release jobs that had drifted from the
// chain), routed every job to the host's deploy runner - a docker executor
// whose job containers hold other projects' deploy keys - and never ran,
// because CI was switched off for the project. Each rule below is one
// careless edit away from sending an embargoed branch's code, or its
// artifacts, somewhere it must not be.
//
// The pipeline and the runner are withheld from the export
// (scripts/export-public.sh), so the public tree skips every case here. This
// file IS published: it names no host, no path on one and no vault entry.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';

import { parseEnvFile } from '../../../scripts/chain/run.mjs';
import { findBash } from '../../../scripts/release/engine.mjs';
import { bashArray } from '../helpers/exporterArrays';

const REPO = path.resolve(__dirname, '..', '..', '..');
const CI_FILE = path.join(REPO, '.gitlab-ci.yml');
const RUNNER = path.join(REPO, 'scripts', 'gitlab-runner');
// The private tree has the pipeline; the public one has neither it nor the
// runner. Keyed on the pipeline alone, so a private tree that lost the runner
// directory is red here, not skipped.
const present = fs.existsSync(CI_FILE);
const TAG = 'filex-chain';
const read = (file: string) => fs.readFileSync(file, 'utf8').replace(/\r\n/g, '\n');
const runnerFile = (name: string) => read(path.join(RUNNER, name));

// ── a reader for the YAML this pipeline is written in ───────────────────────
//
// No YAML parser is a dependency of this repository, and the guard wants the
// file's text anyway: the runner a job lands on is decided by what the job
// itself says. Blocks are read by indentation, two spaces a level, lists one
// level in (`tags:` / `  - filex-chain`). A file written another way does not
// parse here and the cases below are red, which is the point.

type Lines = string[];

/** The top-level keys, each with the lines under it (comments and blank lines dropped). */
function topLevel(text: string): Map<string, Lines> {
  const out = new Map<string, Lines>();
  let cur: Lines | null = null;
  for (const line of text.split('\n')) {
    if (!line.trim() || line.trimStart().startsWith('#')) continue;
    // Up to the colon that ends the key: job names have colons of their own (`chain:embargo:`).
    const m = /^([^\s#].*?):(?:\s+(.*))?$/.exec(line);
    if (m) {
      cur = [];
      out.set(m[1]!.trim(), cur);
      if (m[2]?.trim()) cur.push(`  ${m[2].trim()}`);
      continue;
    }
    cur?.push(line);
  }
  return out;
}

/** `key` at `indent` spaces in `lines`: its inline text and the lines under it. */
function child(lines: Lines | undefined, key: string, indent = 2): { inline: string; under: Lines } | undefined {
  if (!lines) return undefined;
  const pad = ' '.repeat(indent);
  const i = lines.findIndex((l) => l.startsWith(`${pad}${key}:`));
  if (i < 0) return undefined;
  const inline = lines[i]!.slice(pad.length + key.length + 1).trim();
  const under: Lines = [];
  for (const l of lines.slice(i + 1)) {
    if (l.length - l.trimStart().length <= indent) break;
    under.push(l);
  }
  return { inline, under };
}

const unquote = (s: string) => s.trim().replace(/^(['"])(.*)\1$/, '$2');

/** A list's items: `[a, b]`, a scalar, or `- a` lines one level in. */
function list(v: { inline: string; under: Lines } | undefined, indent = 2): string[] {
  if (!v) return [];
  if (v.inline.startsWith('[')) return v.inline.replace(/^\[|\]$/g, '').split(',').map(unquote).filter(Boolean);
  if (v.inline) return [unquote(v.inline)];
  const pad = ' '.repeat(indent + 2);
  return v.under.filter((l) => l.startsWith(`${pad}- `)).map((l) => unquote(l.slice(pad.length + 2)));
}

/** The items of a list of mappings (`rules:`), each as its lines, the `- ` taken off the first. */
function items(v: { inline: string; under: Lines } | undefined, indent = 2): Lines[] {
  if (!v) return [];
  const pad = ' '.repeat(indent + 2);
  const out: Lines[] = [];
  for (const l of v.under) {
    if (l.startsWith(`${pad}- `)) out.push([`${pad}  ${l.slice(pad.length + 2)}`]);
    else out.at(-1)?.push(l);
  }
  return out;
}

/** The keys GitLab reads at the top level that are not jobs. */
const GLOBAL = new Set(['workflow', 'stages', 'variables', 'default', 'include', 'image', 'services', 'cache', 'before_script', 'after_script', 'spec']);

function pipeline() {
  const top = topLevel(read(CI_FILE));
  const jobs = [...top.keys()].filter((k) => !GLOBAL.has(k) && !k.startsWith('.'));
  const templates = [...top.keys()].filter((k) => k.startsWith('.'));
  /** A job's key, its own or its template's (`extends:` one template of this file). */
  const effective = (job: string, key: string) => {
    const own = child(top.get(job), key);
    if (own) return own;
    const ext = child(top.get(job), 'extends')?.inline ?? '';
    return ext ? child(top.get(unquote(ext)), key) : undefined;
  };
  return { top, jobs, templates, effective };
}

describe('the private pipeline', () => {
  it.runIf(!present)('is not in this tree (the export withholds it)', () => {
    expect(fs.existsSync(RUNNER)).toBe(false);
  });

  describe.skipIf(!present)('.gitlab-ci.yml', () => {
    it('has the two jobs, one per way a pipeline starts', () => {
      const { jobs } = pipeline();
      expect(jobs.sort()).toEqual(['chain:embargo', 'chain:manual']);
    });

    // ⚠ #182 (decided 2026-10-06): the nightly run is the build host's own
    // systemd timer (#175), the night's one trigger. A schedule here as well
    // would run the same three-hour chain a second time on the same host.
    it("makes no pipeline from a schedule and offers no nightly: the nightly run is the build host's timer", () => {
      const { top, jobs, effective } = pipeline();
      const rules = items(child(top.get('workflow'), 'rules'));
      expect(rules.length).toBeGreaterThan(1);
      for (const r of rules) expect(r.join(' '), 'a workflow rule names a schedule').not.toMatch(/schedule|nightly/);
      expect(jobs.filter((j) => /nightly/.test(j)), 'a nightly job').toEqual([]);
      for (const job of jobs) {
        expect(list(effective(job, 'script')).join(' '), `${job}: script`).not.toMatch(/nightly/);
        for (const r of items(child(top.get(job), 'rules'))) expect(r.join(' '), `${job}: rules`).not.toMatch(/schedule|nightly/);
      }
      // A run by hand cannot pick the nightly profile either.
      const options = list(child(child(top.get('variables'), 'CHAIN_PROFILE')?.under, 'options', 4), 4);
      expect(options.sort()).toEqual(['full', 'targeted']);
      // Nothing outside a comment asks for a scheduled pipeline.
      const code = read(CI_FILE).split('\n').filter((l) => !l.trimStart().startsWith('#'));
      expect(code.filter((l) => /CI_PIPELINE_SOURCE\s*==\s*"schedule"|\$CI_PIPELINE_SCHEDULE/.test(l))).toEqual([]);
    });

    it('sends every job to the private runner, by a tag each job names itself', () => {
      const { top, jobs, templates } = pipeline();
      expect(list(child(top.get('default'), 'tags')), 'default: tags').toEqual([TAG]);
      for (const job of jobs) {
        expect(list(child(top.get(job), 'tags')), `${job}: its own tags`).toEqual([TAG]);
      }
      // A template that named a tag would be overridden silently by a job that
      // names its own, or would tag a job that forgot to: tags live on jobs.
      for (const t of templates) expect(child(top.get(t), 'tags'), `${t} names no tags`).toBeUndefined();
    });

    it('can reach no other runner and no other place: no include, image, service, cache, trigger, Pages, release or environment', () => {
      const { top, jobs, templates } = pipeline();
      for (const key of ['include', 'image', 'services', 'cache', 'before_script', 'after_script', 'pages']) {
        expect(top.has(key), `top-level ${key}`).toBe(false);
      }
      for (const name of [...jobs, ...templates, 'default']) {
        for (const key of ['trigger', 'image', 'services', 'cache', 'release', 'environment', 'pages', 'hooks', 'inherit', 'needs', 'dependencies']) {
          expect(child(top.get(name), key), `${name}: ${key}`).toBeUndefined();
        }
      }
    });

    it('runs the chain and nothing else: one ci-chain.sh line per job, no test of its own', () => {
      const { top, jobs, templates, effective } = pipeline();
      for (const job of jobs) {
        expect(child(top.get(job), 'before_script'), `${job}: before_script`).toBeUndefined();
        const script = list(effective(job, 'script'));
        expect(script, `${job}: script`).toHaveLength(1);
        expect(script[0], `${job}: script`).toMatch(/^bash scripts\/gitlab-runner\/ci-chain\.sh ("\$CHAIN_PROFILE"|full|targeted)$/);
        expect(list(effective(job, 'after_script')), `${job}: after_script`).toEqual(['bash scripts/gitlab-runner/ci-chain.sh --after']);
        expect(child(top.get(job), 'extends')?.inline, `${job}: extends`).toBe('.chain');
      }
      for (const t of templates) {
        expect(child(top.get(t), 'script'), `${t}: script`).toBeUndefined();
        expect(child(top.get(t), 'before_script'), `${t}: before_script`).toBeUndefined();
      }
    });

    it('makes a pipeline for a push to sec/* and a run by hand on main or sec/* - nothing else', () => {
      const { top } = pipeline();
      const rules = items(child(top.get('workflow'), 'rules'));
      const cond = (r: Lines) => r.find((l) => /^\s+if: /.test(l))?.replace(/^\s+if: /, '') ?? '';
      const trigger = (r: Lines) => r.find((l) => /^\s+CHAIN_TRIGGER: /.test(l))?.replace(/^\s+CHAIN_TRIGGER: /, '') ?? '';
      expect(rules.map((r) => [cond(r), trigger(r)])).toEqual([
        [String.raw`$CI_PIPELINE_SOURCE == "push" && $CI_COMMIT_BRANCH =~ /^sec\/./`, 'embargo'],
        [
          String.raw`($CI_PIPELINE_SOURCE == "web" || $CI_PIPELINE_SOURCE == "api") && ($CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH || $CI_COMMIT_BRANCH =~ /^sec\/./)`,
          'manual',
        ],
        ['', ''],
      ]);
      // The last rule is the catch-all refusal: a schedule, a merge request,
      // a tag, any other branch or source makes no pipeline at all.
      expect(rules.at(-1)!.map((l) => l.trim())).toEqual(['when: never']);
    });

    it('starts each job on its own trigger only', () => {
      const { top, jobs } = pipeline();
      const want: Record<string, string> = { 'chain:embargo': 'embargo', 'chain:manual': 'manual' };
      for (const job of jobs) {
        const rules = items(child(top.get(job), 'rules'));
        expect(rules.map((r) => r.map((l) => l.trim())), job).toEqual([[`if: $CHAIN_TRIGGER == "${want[job]}"`]]);
      }
    });

    it('keeps its artifacts for the project developers, and only the chain result in them', () => {
      const { jobs, effective } = pipeline();
      for (const job of jobs) {
        const a = effective(job, 'artifacts');
        expect(a, `${job}: artifacts`).toBeTruthy();
        const under = a!.under;
        expect(child(under, 'access', 4)?.inline, `${job}: artifacts.access`).toBe('developer');
        expect(child(under, 'when', 4)?.inline, `${job}: artifacts.when`).toBe('always');
        expect(child(under, 'expire_in', 4)?.inline, `${job}: artifacts.expire_in`).toMatch(/^\d+ days?$/);
        expect(list(child(under, 'paths', 4), 4), `${job}: artifacts.paths`).toEqual(['chain-result/']);
        for (const key of ['public', 'reports', 'untracked', 'exclude', 'expose_as']) {
          expect(child(under, key, 4), `${job}: artifacts.${key}`).toBeUndefined();
        }
      }
    });

    it('leaves the host settings to the host: no CHAIN_ROOT, CHAIN_LOCK, CHAIN_PREFIX, CHAIN_ENV or notify setting in the pipeline', () => {
      // run.mjs lets the environment win over the host's env file, so a
      // variable here would choose the run's lock or root for it.
      const allowed = new Set([
        'CHAIN_PROFILE', 'CHAIN_TRIGGER', 'CHAIN_GO_PKGS', 'CHAIN_GO_RUN', 'CHAIN_RACE_PKGS', 'CHAIN_MIGRATE',
        'CHAIN_CY_SPECS', 'CHAIN_E2E_GREP', 'CHAIN_E2E_BROWSERS', 'CHAIN_E2E_DS',
      ]);
      const set = [...read(CI_FILE).matchAll(/^\s+(CHAIN_[A-Z0-9_]+):/gm)].map((m) => m[1]!);
      expect(set.length).toBeGreaterThan(0);
      expect(set.filter((k) => !allowed.has(k))).toEqual([]);
    });

    it('lets a newer push to the same sec/* branch supersede a run still going, and nothing else', () => {
      const { top, jobs } = pipeline();
      for (const job of jobs) {
        expect(child(top.get(job), 'interruptible')?.inline ?? 'false', job).toBe(job === 'chain:embargo' ? 'true' : 'false');
      }
    });

    it('is withheld from the export, with the runner directory', () => {
      const exporter = read(path.join(REPO, 'scripts', 'export-public.sh'));
      expect(bashArray(exporter, 'private_files')).toContain('.gitlab-ci.yml');
      expect(bashArray(exporter, 'private_dirs')).toContain('scripts/gitlab-runner');
    });
  });

  describe.skipIf(!present)('the runner', () => {
    it('takes one job at a time, as a shell executor on the host, with its own config and the host gate', () => {
      const conf = runnerFile('config.toml.tmpl');
      expect(conf).toMatch(/^concurrent = 1$/m);
      expect(conf).toMatch(/^ {2}limit = 1$/m);
      expect(conf).toMatch(/^ {2}request_concurrency = 1$/m);
      expect(conf).toMatch(/^ {2}executor = "shell"$/m);
      expect(conf).not.toMatch(/\[runners\.docker\]/);
      expect(conf).toMatch(/^ {2}token = "@TOKEN@"$/m);
      expect(conf).toMatch(/^ {2}environment = \[[^\]]*"CHAIN_ENV=\/etc\/filex-chain\/ci\.env"/m);
      expect(conf).toMatch(/^ {2}pre_get_sources_script = "bash \/etc\/gitlab-runner-filex\/pre-job\.sh"$/m);
      // Local cache only: nothing is uploaded anywhere.
      expect(conf).not.toMatch(/\[runners\.cache\.(s3|gcs|azure)\]/);
      const unit = runnerFile('gitlab-runner-filex.service');
      const exec = /^ExecStart=(.*)$/m.exec(unit)?.[1] ?? '';
      expect(exec).toContain('--config /etc/gitlab-runner-filex/config.toml');
      expect(exec).not.toContain('/etc/gitlab-runner/config.toml');
      expect(exec).not.toMatch(/--user\b/);
    });

    it('commits no runner token anywhere', () => {
      const files = [CI_FILE, ...fs.readdirSync(RUNNER).map((f) => path.join(RUNNER, f))];
      for (const f of files) expect(read(f), f).not.toMatch(/glrt-[A-Za-z0-9_-]{10,}/);
    });

    it('install.sh never puts the token on a command line or in its output', () => {
      const script = runnerFile('install.sh');
      expect(script).not.toMatch(/^\s*set -[a-z]*x/m);
      // Every line that touches $TOKEN (not TOKEN_FILE, not the @TOKEN@
      // placeholder) is one of these shell builtins, or a test of it.
      const allowed = [
        /^TOKEN=""$/,
        /^IFS= read -r TOKEN( < "\$TOKEN_FILE")? \|\| \[ -n "\$TOKEN" \] \|\| die "[^"$]*"$/,
        /^TOKEN="\$\{TOKEN%\$'\\r'\}"$/,
        /^TOKEN="\$\{BASH_REMATCH\[1\]\}"$/,
        /^\[ -n "\$TOKEN" \] \|\| die "(?:[^"$]|\$(?!\{?TOKEN\b))*"$/,
        /^if \[ -n "\$TOKEN" \] && ! \[\[ "\$TOKEN" =~ \^glrt-\S+ \]\]; then$/,
        /^rendered="\$\{tmpl\/\/@TOKEN@\/\$TOKEN\}"$/,
        /^unset TOKEN$/,
      ];
      const touching = script
        .split('\n')
        .map((l) => l.trim())
        .filter((l) => !l.startsWith('#') && /\$\{?TOKEN\b|\bTOKEN=|read -r TOKEN|unset TOKEN/.test(l.replace(/TOKEN_FILE/g, '')));
      expect(touching.length).toBeGreaterThan(4);
      expect(touching.filter((l) => !allowed.some((re) => re.test(l)))).toEqual([]);
      // The rendered config goes to its file through printf, and nowhere else.
      const rendered = script.split('\n').map((l) => l.trim()).filter((l) => !l.startsWith('#') && l.includes('$rendered'));
      expect(rendered).toEqual([`printf '%s\\n' "$rendered" > "$tmp" || die "cannot write $tmp"`]);
    });

    it('ci.env gives a CI run the build lock, a root and a container prefix of its own', () => {
      const env = parseEnvFile(runnerFile('ci.env'));
      expect(env.CHAIN_LOCK, 'CHAIN_LOCK').toMatch(/^\/\S+$/);
      expect(env.CHAIN_ROOT, 'CHAIN_ROOT').toMatch(/^\/\S+$/);
      expect(env.CHAIN_PREFIX, 'CHAIN_PREFIX').toMatch(/^[a-z0-9][a-z0-9-]*$/);
      expect(env.CHAIN_PREFIX).not.toBe('fxchain');
    });
  });

  // ── the host gate, run ────────────────────────────────────────────────────

  describe.skipIf(!present)('pre-job.sh, the host gate', () => {
    const bash = present ? findBash() : null;
    const ok = {
      GITLAB_CI: 'true',
      CI_PROJECT_PATH: 'brftech/filemanager',
      CI_PROJECT_VISIBILITY: 'private',
      CI_COMMIT_REF_PROTECTED: 'true',
      CI_DEFAULT_BRANCH: 'main',
      CI_COMMIT_BRANCH: 'main',
      CI_COMMIT_REF_NAME: 'main',
      CI_PIPELINE_SOURCE: 'push',
      CHAIN_ENV: '/etc/filex-chain/ci.env',
    };
    function gate(env: Record<string, string>) {
      const r = spawnSync(bash!, [path.join(RUNNER, 'pre-job.sh').split(path.sep).join('/')], {
        encoding: 'utf8',
        env: { PATH: process.env.PATH ?? '', SYSTEMROOT: process.env.SYSTEMROOT ?? '', ...env },
      });
      return { status: r.status, out: `${r.stdout}${r.stderr}` };
    }
    const sec = { CI_COMMIT_BRANCH: 'sec/053-example', CI_COMMIT_REF_NAME: 'sec/053-example' };

    it.each([
      ['a push to main', {}],
      ['a push to an embargoed branch', sec],
      ['a run by hand on an embargoed branch', { ...sec, CI_PIPELINE_SOURCE: 'web' }],
      ['a run through the API on main', { CI_PIPELINE_SOURCE: 'api' }],
    ])('lets through %s', (_what, extra) => {
      expect(bash, 'bash').toBeTruthy();
      const r = gate({ ...ok, ...extra });
      expect(r.status, r.out).toBe(0);
    });

    it.each([
      ['another project', { CI_PROJECT_PATH: 'acme/other' }],
      ['a project that is not private', { CI_PROJECT_VISIBILITY: 'internal' }],
      ['a public project', { CI_PROJECT_VISIBILITY: 'public' }],
      ['an unprotected ref', { CI_COMMIT_REF_PROTECTED: 'false' }],
      ['a feature branch', { CI_COMMIT_BRANCH: 'feat/x', CI_COMMIT_REF_NAME: 'feat/x' }],
      ['sec/ with no name', { CI_COMMIT_BRANCH: 'sec/', CI_COMMIT_REF_NAME: 'sec/' }],
      ['a tag', { CI_COMMIT_BRANCH: '', CI_COMMIT_TAG: 'v1.2.3', CI_COMMIT_REF_NAME: 'v1.2.3' }],
      ['a merge request', { CI_MERGE_REQUEST_IID: '7', CI_PIPELINE_SOURCE: 'merge_request_event' }],
      ['a trigger token', { CI_PIPELINE_SOURCE: 'trigger' }],
      // #182: the nightly is the build host's timer, never a schedule here.
      ['a schedule, even on main', { CI_PIPELINE_SOURCE: 'schedule' }],
      ['a parent pipeline', { CI_PIPELINE_SOURCE: 'parent_pipeline' }],
      ['settings from another file', { CHAIN_ENV: '/tmp/mine.env' }],
      ['no GitLab job at all', { GITLAB_CI: '' }],
    ])('refuses %s', (_what, extra) => {
      expect(bash, 'bash').toBeTruthy();
      const r = gate({ ...ok, ...extra });
      expect(r.status, r.out).toBe(1);
      expect(r.out).toMatch(/pre-job: refused: /);
    });
  });

  // ── ci-chain.mjs, run ─────────────────────────────────────────────────────

  describe.skipIf(!present)('ci-chain.mjs, the jobs side of the chain', () => {
    const temps: string[] = [];
    afterEach(() => {
      for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true });
    });
    const tmp = () => {
      const d = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-gl-'));
      temps.push(d);
      return d;
    };
    const SCRIPT = path.join(RUNNER, 'ci-chain.mjs');

    /** A host env file in a scratch directory, and the job environment that names it. */
    function host(lines: string[] = []) {
      const d = tmp();
      const root = path.join(d, 'root');
      const file = path.join(d, 'ci.env');
      fs.writeFileSync(file, [`CHAIN_ROOT=${root}`, `CHAIN_LOCK=${path.join(d, 'build.lock')}`, 'CHAIN_PREFIX=fxci', ...lines].join('\n'));
      const env: Record<string, string> = {
        GITLAB_CI: 'true',
        CI_PROJECT_PATH: 'brftech/filemanager',
        CI_PROJECT_VISIBILITY: 'private',
        CI_DEFAULT_BRANCH: 'main',
        CI_COMMIT_BRANCH: 'main',
        CI_PIPELINE_SOURCE: 'push',
        CI_PIPELINE_ID: '11',
        CI_JOB_ID: '22',
        CHAIN_ENV: file,
      };
      return { d, root, file, env };
    }

    function plan(profile: string, env: Record<string, string>) {
      const r = spawnSync(process.execPath, [SCRIPT, 'plan', profile], {
        encoding: 'utf8',
        env: { PATH: process.env.PATH ?? '', SYSTEMROOT: process.env.SYSTEMROOT ?? '', ...env },
      });
      const lines = Object.fromEntries(
        r.stdout.split('\n').filter((l) => l.includes('=')).map((l) => [l.slice(0, l.indexOf('=')), l.slice(l.indexOf('=') + 1)]),
      );
      return { status: r.status, err: r.stderr, lines };
    }

    /** Runs `body` with the module as `m` in a plain node process; returns what it returns. */
    function inNode<T>(body: string): T {
      const url = pathToFileURL(SCRIPT).href;
      const src = `const m = await import(${JSON.stringify(url)}); const r = await (async () => { ${body} })(); process.stdout.write(JSON.stringify(r ?? null));`;
      const r = spawnSync(process.execPath, ['--input-type=module', '-e', src], { encoding: 'utf8' });
      if (r.status !== 0) throw new Error(r.stderr);
      return JSON.parse(r.stdout) as T;
    }

    it('plans a run on main: the chain with notify on, as gl-<pipeline>-<job> under the host root', () => {
      const h = host();
      const r = plan('full', h.env);
      expect(r.status, r.err).toBe(0);
      expect(r.lines).toEqual({ RUN_ID: 'gl-11-22', PROFILE: 'full', NOTIFY: 'on', ROOT: path.resolve(h.root) });
    });

    it('posts nothing to notify from an embargoed branch', () => {
      const h = host();
      for (const profile of ['full', 'targeted']) {
        const r = plan(profile, { ...h.env, CI_COMMIT_BRANCH: 'sec/053-example' });
        expect(r.status, r.err).toBe(0);
        expect(r.lines.NOTIFY).toBe('off');
      }
    });

    // #182: the nightly is the build host's timer (#175), its one trigger.
    it('runs no nightly: refuses the nightly profile and a scheduled pipeline, on main too', () => {
      const h = host();
      for (const env of [h.env, { ...h.env, CI_COMMIT_BRANCH: 'sec/053-example' }]) {
        const nightly = plan('nightly', env);
        expect(nightly.status, nightly.err).toBe(2);
        expect(nightly.err).toMatch(/nightly profile is the build host's own/);
        expect(nightly.lines.RUN_ID).toBeUndefined();
      }
      const scheduled = plan('full', { ...h.env, CI_PIPELINE_SOURCE: 'schedule' });
      expect(scheduled.status, scheduled.err).toBe(2);
      expect(scheduled.err).toMatch(/a scheduled pipeline: the nightly run is the build host's own timer/);
      // A job comes from what the host gate lets through, and nothing else.
      for (const source of ['push', 'web', 'api']) {
        const r = plan('targeted', { ...h.env, CI_PIPELINE_SOURCE: source });
        expect(r.status, `${source}: ${r.err}`).toBe(0);
      }
      for (const source of ['trigger', 'merge_request_event', 'parent_pipeline', '']) {
        const r = plan('full', { ...h.env, CI_PIPELINE_SOURCE: source });
        expect(r.status, source).toBe(2);
        expect(r.err).toMatch(/pipeline source/);
      }
    });

    it.each([
      ['a project that is not private', { CI_PROJECT_VISIBILITY: 'internal' }, /not private/],
      ['another project', { CI_PROJECT_PATH: 'acme/other' }, /this runner tests/],
      ['a feature branch', { CI_COMMIT_BRANCH: 'feat/x' }, /main and sec\/\* only/],
      ['a tag or a merge request', { CI_COMMIT_BRANCH: '' }, /not a branch pipeline/],
      ['no job ids', { CI_JOB_ID: '' }, /CI_JOB_ID/],
      ['a lock the job chose', { CHAIN_LOCK: '/tmp/other.lock' }, /CHAIN_LOCK: the host's env file/],
      ['a root the job chose', { CHAIN_ROOT: '/tmp/root' }, /CHAIN_ROOT: the host's env file/],
      ['no env file', { CHAIN_ENV: '' }, /CHAIN_ROOT is not set/],
    ])('refuses %s', (_what, extra, why) => {
      const h = host();
      const r = plan('full', { ...h.env, ...extra });
      expect(r.status).toBe(2);
      expect(r.err).toMatch(why);
    });

    it.each([
      ['CHAIN_LOCK=none', /CHAIN_LOCK=none/],
      ['CHAIN_LOCK=build.lock', /CHAIN_LOCK=build\.lock is not an absolute path/],
      ['CHAIN_PREFIX=fxchain', /hand runs' prefix/],
      ['CHAIN_PREFIX=Fx CI', /not a container name prefix/],
    ])('refuses a host file with %s', (line, why) => {
      const h = host([line]);
      const r = plan('full', h.env);
      expect(r.status).toBe(2);
      expect(r.err).toMatch(why);
    });

    it('accepts the env file the runner ships (ci.env)', () => {
      const h = host();
      const r = plan('targeted', { ...h.env, CHAIN_ENV: path.join(RUNNER, 'ci.env') });
      expect(r.status, r.err).toBe(0);
      expect(r.lines.ROOT).toBe(path.resolve(parseEnvFile(runnerFile('ci.env')).CHAIN_ROOT!));
    });

    it('collects result.json, chain.log and the tails of the red jobs into chain-result/, and nothing from outside the run', () => {
      const d = tmp();
      const root = path.join(d, 'root');
      const run = path.join(root, 'runs', 'gl-11-22');
      const repo = path.join(d, 'checkout');
      fs.mkdirSync(path.join(run, 'logs'), { recursive: true });
      fs.mkdirSync(path.join(run, 'out', 'web'), { recursive: true });
      fs.mkdirSync(repo);
      const outside = path.join(d, 'secret.log');
      fs.writeFileSync(outside, 'not part of the run\n');
      fs.writeFileSync(path.join(run, 'out', 'web', 'trace.zip'), 'test output stays on the host');
      const long = Array.from({ length: 1000 }, (_, i) => `line ${i + 1}`).join('\n');
      fs.writeFileSync(path.join(run, 'logs', 'web.log'), `${long}\n`);
      fs.writeFileSync(path.join(run, 'logs', 'build.log'), 'green\n');
      fs.writeFileSync(path.join(run, 'chain.log'), 'JOBSTART build\n');
      const result = {
        schema: 1, run_id: 'gl-11-22', profile: 'full', sha: 'a'.repeat(40), finished: '2026-10-06T00:00:00Z', ok: false, stopped: false, wall: '2h00m00s',
        counts: { passed: 1, failed: 2, skipped: 0, total: 3 },
        jobs: [
          { name: 'build', status: 'passed', log: path.join(run, 'logs', 'build.log') },
          { name: 'web', status: 'failed', log: path.join(run, 'logs', 'web.log'), summary: 'vitest=1' },
          { name: 'go-rest', status: 'failed', log: outside, summary: 'go=1' },
        ],
      };
      fs.writeFileSync(path.join(run, 'result.json'), JSON.stringify(result));
      const text = inNode<string>(`return m.collect({ root: ${JSON.stringify(root)}, runId: 'gl-11-22', repo: ${JSON.stringify(repo)}, env: {} });`);
      const out = path.join(repo, 'chain-result');
      expect(JSON.parse(fs.readFileSync(path.join(out, 'result.json'), 'utf8')).run_id).toBe('gl-11-22');
      expect(fs.existsSync(path.join(out, 'chain.log'))).toBe(true);
      expect(fs.readdirSync(path.join(out, 'logs'))).toEqual(['web.log']);
      const web = fs.readFileSync(path.join(out, 'logs', 'web.log'), 'utf8');
      expect(web).toContain('line 1000');
      expect(web).not.toContain('line 600\n');
      expect(fs.existsSync(path.join(out, 'out'))).toBe(false);
      expect(text).toMatch(/RED/);
      expect(text).toMatch(/RED web: vitest=1/);
    });

    it('leaves NO-RESULT.txt when the job made no run', () => {
      const d = tmp();
      const repo = path.join(d, 'checkout');
      fs.mkdirSync(repo);
      inNode(`return m.collect({ root: ${JSON.stringify(path.join(d, 'root'))}, runId: 'gl-1-2', repo: ${JSON.stringify(repo)}, env: {} });`);
      expect(fs.readdirSync(path.join(repo, 'chain-result')).sort()).toEqual(['NO-RESULT.txt', 'job.json']);
    });

    it('prunes old CI runs only: never a hand run, latest-*.json or this job', () => {
      const d = tmp();
      const runs = path.join(d, 'runs');
      const old = (Date.now() - 10 * 86_400_000) / 1000;
      for (const name of ['gl-1-2', 'gl-3-4', 'gl-5-6', 'full-20261001-000000Z-abcdef12']) {
        fs.mkdirSync(path.join(runs, name), { recursive: true });
        if (name !== 'gl-3-4') fs.utimesSync(path.join(runs, name), old, old);
      }
      fs.writeFileSync(path.join(runs, 'latest-full.json'), '{}');
      fs.utimesSync(path.join(runs, 'latest-full.json'), old, old);
      const removed = inNode<string[]>(`return m.prune({ root: ${JSON.stringify(d)}, keepDays: 7, keep: 'gl-5-6' });`);
      expect(removed).toEqual(['gl-1-2']);
      expect(fs.readdirSync(runs).sort()).toEqual(['full-20261001-000000Z-abcdef12', 'gl-3-4', 'gl-5-6', 'latest-full.json']);
    });
  });
});
