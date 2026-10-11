// #165: the release's speed-ups, proved where they could quietly weaken a gate.
//
//   the image gate   builds from `git archive` of the release commit, streamed
//                    to `docker build -` (scripts/release/gates/docker-build.mjs):
//                    at 0.54.0 and 0.55.0 BuildKit spent 30-52 minutes per
//                    image walking the checkout through WSL's 9P bridge. What
//                    must hold: the context is exactly the commit (no working-
//                    tree change, no untracked file), the build is judged by
//                    both exit codes, and a .dockerignore that would make the
//                    stream differ from CI's context is red, before docker runs.
//   the ci stage     waits for the tag's run before it reads what it published
//                    (stages.mjs → waitForTagRun): a wait never passes anything.
//
// docker is a stand-in (a `docker` script first on PATH, in $HOME/bin) that
// keeps what it was given; nothing here talks to a daemon.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

import { waitForTagRun } from '../../../scripts/release/stages.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const SCRIPT = path.join(REPO, 'scripts', 'release', 'gates', 'docker-build.mjs');
const TIMEOUT = 60_000;
const temps: string[] = [];
afterEach(() => {
  for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
});

function git(dir: string, ...args: string[]) {
  const r = spawnSync('git', ['-C', dir, '-c', 'user.name=Release Test', '-c', 'user.email=release@test.invalid', '-c', 'commit.gpgSign=false', '-c', 'core.autocrlf=false', ...args], {
    encoding: 'utf8',
    windowsHide: true,
  });
  if (r.status !== 0) throw new Error(`git ${args.join(' ')}: ${r.stderr}`);
  return r.stdout.trim();
}

/** The entries of a tar (ustar, with git archive's pax headers): name → content. */
function untar(buf: Buffer): Map<string, string> {
  const out = new Map<string, string>();
  const str = (b: Buffer) => b.toString('utf8').replace(/\0.*$/s, '');
  let next: string | null = null;
  for (let off = 0; off + 512 <= buf.length; ) {
    const h = buf.subarray(off, off + 512);
    if (h.every((x) => x === 0)) break;
    const size = parseInt(str(h.subarray(124, 136)).trim() || '0', 8);
    const type = String.fromCharCode(h[156]!);
    const prefix = str(h.subarray(345, 500));
    const data = buf.subarray(off + 512, off + 512 + size);
    const name = next ?? (prefix ? `${prefix}/${str(h.subarray(0, 100))}` : str(h.subarray(0, 100)));
    next = null;
    if (type === 'x') next = /\d+ path=([^\n]*)\n/.exec(data.toString('utf8'))?.[1] ?? null;
    else if (type === '0' || type === String.fromCharCode(0)) out.set(name, data.toString('utf8'));
    off += 512 + Math.ceil(size / 512) * 512;
  }
  return out;
}

/** A scratch repository with a Dockerfile, and a stand-in `docker` that keeps its arguments and stdin. */
function scratch(files: Record<string, string>) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-docker-build-'));
  temps.push(root);
  const repo = path.join(root, 'repo');
  const put = (rel: string, text: string) => {
    fs.mkdirSync(path.dirname(path.join(repo, rel)), { recursive: true });
    fs.writeFileSync(path.join(repo, rel), text);
  };
  fs.mkdirSync(repo);
  git(repo, 'init', '-q');
  for (const [f, t] of Object.entries(files)) put(f, t);
  git(repo, 'add', '-A', '-f');
  git(repo, 'commit', '-q', '-m', 'the release commit');
  // ⚠ Git for Windows' bin/bash.exe puts $HOME/bin before PATH, and the
  // maintainer's `docker` (a forwarder to WSL) lives in ~/bin: the stand-in is
  // found first only in the HOME this run is given.
  const home = path.join(root, 'home');
  const bin = path.join(home, 'bin');
  const kept = path.join(root, 'kept');
  fs.mkdirSync(bin, { recursive: true });
  fs.mkdirSync(kept);
  fs.writeFileSync(path.join(bin, 'docker'), '#!/usr/bin/env bash\nprintf \'%s\\n\' "$@" > "$STUB_KEPT/args"\ncat > "$STUB_KEPT/context.tar"\nexit "${STUB_EXIT:-0}"\n');
  fs.chmodSync(path.join(bin, 'docker'), 0o755);
  const run = (args: string[], env: Record<string, string> = {}) => {
    // One PATH key: Windows spells it Path, and a second spelling is a coin toss.
    const base: NodeJS.ProcessEnv = { ...process.env };
    const key = Object.keys(base).find((k) => k.toUpperCase() === 'PATH') ?? 'PATH';
    const searchPath = base[key];
    delete base[key];
    const r = spawnSync(process.execPath, [SCRIPT, ...args], {
      cwd: repo,
      encoding: 'utf8',
      windowsHide: true,
      env: { ...base, HOME: home, PATH: `${bin}${path.delimiter}${searchPath}`, STUB_KEPT: kept, ...env },
    });
    return { code: r.status, out: `${r.stdout}${r.stderr}` };
  };
  const argsOf = () => (fs.existsSync(path.join(kept, 'args')) ? fs.readFileSync(path.join(kept, 'args'), 'utf8').split('\n').filter(Boolean) : null);
  const context = () => untar(fs.readFileSync(path.join(kept, 'context.tar')));
  return { repo, put, run, argsOf, context };
}

const IMAGE = ['--dockerfile', 'docker/Dockerfile', '--tag', 'filex-release:full-1.2.3', '--version', 'v1.2.3'];
const DOCKERFILE = 'FROM alpine:3.24\nCOPY web/ /web/\nCOPY docker/entrypoint.sh /e\n';

describe('the image gate builds the release commit, not the working tree (#165)', () => {
  it('streams exactly the commit to `docker build -`, and stamps it into the image', { timeout: TIMEOUT }, () => {
    const s = scratch({ 'docker/Dockerfile': DOCKERFILE, 'docker/entrypoint.sh': '#!/bin/sh\n', '.dockerignore': 'web/dist\n', 'web/a.txt': 'committed\n', 'README.md': 'r\n' });
    const sha = git(s.repo, 'rev-parse', 'HEAD');
    // what the working tree has and the commit does not
    s.put('web/a.txt', 'changed, not committed\n');
    s.put('web/stray.txt', 'untracked\n');
    const r = s.run(IMAGE);
    expect(r.code, r.out).toBe(0);
    expect(r.out).toContain(`context: git archive ${sha}`);
    const files = s.context();
    expect([...files.keys()].sort()).toEqual(['.dockerignore', 'README.md', 'docker/Dockerfile', 'docker/entrypoint.sh', 'web/a.txt']);
    expect(files.get('web/a.txt')).toBe('committed\n');
    const args = s.argsOf()!;
    expect(args[0]).toBe('build');
    expect(args.at(-1), 'the context is stdin').toBe('-');
    expect(args).not.toContain('.');
    for (const pair of [['-f', 'docker/Dockerfile'], ['-t', 'filex-release:full-1.2.3'], ['--platform', 'linux/amd64']]) expect(args[args.indexOf(pair[0]!) + 1]).toBe(pair[1]);
    expect(args).toContain(`COMMIT=${sha}`);
    expect(args).toContain('VERSION=v1.2.3');
    expect(args.some((a) => /^DATE=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(a))).toBe(true);
  });

  it('is red when docker build is, and when it is asked for something that is no commit', { timeout: TIMEOUT }, () => {
    const s = scratch({ 'docker/Dockerfile': DOCKERFILE, 'docker/entrypoint.sh': '#!/bin/sh\n', 'web/a.txt': 'a\n' });
    expect(s.run(IMAGE, { STUB_EXIT: '1' }).code).toBe(1);
    const r = s.run([...IMAGE, '--commit', 'no-such-commit']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('is no commit');
    expect(s.run(['--dockerfile', 'docker/Dockerfile', '--tag', 'x', '--version', 'v1', '--skip']).code, 'an unknown option').toBe(2);
  });

  it('is red, before docker runs, when .dockerignore leaves out a tracked file the image copies, or cannot be read', { timeout: TIMEOUT }, () => {
    const s = scratch({ 'docker/Dockerfile': DOCKERFILE, 'docker/entrypoint.sh': '#!/bin/sh\n', '.dockerignore': 'web/dist\nnotes\n', 'web/dist/old.js': 'stale\n', 'notes/x.md': 'not copied\n' });
    let r = s.run(IMAGE);
    expect(r.code, r.out).toBe(1);
    expect(r.out).toContain('web/dist/old.js');
    expect(r.out, 'a file no COPY reads is no difference').not.toContain('notes/x.md');
    expect(s.argsOf(), 'docker ran').toBeNull();

    const t = scratch({ 'docker/Dockerfile': DOCKERFILE, 'docker/entrypoint.sh': '#!/bin/sh\n', '.dockerignore': 'web/[ab].txt\n', 'web/a.txt': 'a\n' });
    r = t.run(IMAGE);
    expect(r.code, r.out).toBe(1);
    expect(r.out).toContain('cannot read');
    expect(t.argsOf()).toBeNull();
  });
});

describe('the ci stage waits for the tag run, and passes nothing by waiting (#165)', () => {
  type Answer = { runs?: object[]; error?: string };
  const make = (answers: Answer[], wait: object | null = {}) => {
    const asked: object[] = [];
    let i = 0;
    const R = {
      tag: 'v1.2.3',
      version: '1.2.3',
      state: { exportHead: 'abc' },
      plan: {
        tagRunWait: wait ? { pollMs: 1, timeoutMs: 300, appearMs: 60, ...wait } : undefined,
        github: {
          runs: (q: object) => {
            asked.push(q);
            return answers[Math.min(i++, answers.length - 1)];
          },
        },
      },
    };
    return { R, asked };
  };
  const tagRun = (status: string, conclusion = '', branch = 'v1.2.3') => ({ id: 9, status, conclusion, url: 'https://x/runs/9', branch });

  it('without the plan asking for it, does not wait and asks nothing', async () => {
    const { R, asked } = make([{ runs: [] }], null);
    expect(await waitForTagRun(R)).toEqual({ waited: false });
    expect(asked).toEqual([]);
  });

  it('waits until the tag run has finished, however it ended, then reads', async () => {
    const { R, asked } = make([{ runs: [tagRun('queued')] }, { runs: [tagRun('in_progress')] }, { runs: [tagRun('completed', 'failure')] }]);
    expect(await waitForTagRun(R)).toEqual({ waited: true });
    expect(asked.length).toBe(3);
    expect(asked[0]).toEqual({ workflow: 'release.yml', sha: 'abc', event: 'push' });
  });

  it('hands over to a person when the run is still going at the limit, and reads at once when none appears', async () => {
    const going = await waitForTagRun(make([{ runs: [tagRun('in_progress')] }]).R);
    expect(going.going).toContain('in_progress');
    expect(await waitForTagRun(make([{ runs: [] }]).R)).toEqual({ waited: true });
    // a run of another ref on the same commit is not the tag's
    expect(await waitForTagRun(make([{ runs: [tagRun('in_progress', '', 'main')] }]).R)).toEqual({ waited: true });
  });

  it('asks again when GitHub does not answer, and reads at once when it answers no', async () => {
    const { R, asked } = make([{ error: 'gh: Server Error (HTTP 502)' }, { error: 'gh: Server Error (HTTP 503)' }, { runs: [tagRun('completed', 'success')] }]);
    expect(await waitForTagRun(R)).toEqual({ waited: true });
    expect(asked.length).toBe(3);
    const no = make([{ error: 'HTTP 404: Not Found' }]);
    expect(await waitForTagRun(no.R)).toEqual({ waited: true });
    expect(no.asked.length).toBe(1);
  });
});
