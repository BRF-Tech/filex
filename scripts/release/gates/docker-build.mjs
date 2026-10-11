#!/usr/bin/env node
// The image builds of `pnpm release`: `docker build` from the release COMMIT,
// streamed to docker as a tar - never from the working tree (task #165).
//
//   node scripts/release/gates/docker-build.mjs --dockerfile docker/Dockerfile \
//        --tag filex-release:full-0.56.0 --version v0.56.0 [--commit <rev>] [--platform linux/amd64]
//
// ⚠⚠ Why not `docker build .`: on the maintainer's workstation docker is the
// WSL docker-ce behind a forwarder, so `.` is the Windows checkout read through WSL's 9P
// bridge, and BuildKit walks all of it before it sends a byte - .git, every
// worktree under .claude/worktrees, the ignored build output (.dockerignore
// leaves out only node_modules, dist and the like). At 0.55.0 that walk took
// 31.6 and 30.4 minutes of the full and slim builds' 34.0 and 31.2, to send
// 276 kB; at 0.54.0 52.4 and 49.5; every pretag run paid it again. The
// commit's own tree, from `git archive`, is ~70 MB and a second, and it is
// exactly what the release commit holds: nothing loose in the working tree
// can reach the image.
//
// ⚠ BuildKit applies no .dockerignore to a tar on stdin (a "remote" context;
// measured with docker 29.7, 2026-10-10). The tracked tree is what CI's
// checkout holds, so the two contexts are the same exactly when .dockerignore
// leaves out no tracked file the Dockerfile copies: that is checked first
// (checks.mjs → dockerignoreLeavesOut), and an unreadable pattern is red -
// could not check is not a pass.
//
// Judged by its own exit code: 0 only when `git archive` AND `docker build`
// both exited 0.

import { spawn } from 'node:child_process';

import { dockerfileSources, dockerignoreLeavesOut } from '../checks.mjs';
import { dockerArgv, findBash, git } from '../engine.mjs';

const USAGE = 'usage: docker-build.mjs --dockerfile <path> --tag <image> --version vX.Y.Z [--commit <rev>] [--platform linux/amd64]';

function usage(msg) {
  if (msg) console.error(`docker-build: ${msg}`);
  console.error(USAGE);
  process.exit(2);
}

const opts = { commit: 'HEAD', platform: 'linux/amd64' };
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i++) {
  const a = argv[i];
  const value = () => {
    const v = argv[++i];
    if (v === undefined || v.startsWith('--')) usage(`${a} needs a value`);
    return v;
  };
  if (a === '--dockerfile') opts.dockerfile = value();
  else if (a === '--tag') opts.tag = value();
  else if (a === '--version') opts.version = value();
  else if (a === '--commit') opts.commit = value();
  else if (a === '--platform') opts.platform = value();
  else usage(`unknown argument ${a}`);
}

if (!opts.dockerfile || !opts.tag || !opts.version) usage('--dockerfile, --tag and --version are required');

const repo = process.cwd();
const fail = (msg) => {
  console.error(msg);
  process.exit(1);
};

const rev = git(repo, 'rev-parse', '--verify', '-q', `${opts.commit}^{commit}`);
if (rev.status !== 0) fail(`${opts.commit} is no commit in ${repo}`);
const sha = rev.stdout.trim();

const ls = git(repo, 'ls-tree', '-r', '-z', '--name-only', sha);
if (ls.status !== 0) fail(`git ls-tree ${sha}: ${ls.stderr.trim()}`);
const files = ls.stdout.split(String.fromCharCode(0)).filter(Boolean);
if (!files.includes(opts.dockerfile)) fail(`${opts.dockerfile} is not in ${sha.slice(0, 10)}`);

const show = (f) => {
  const r = git(repo, 'show', `${sha}:${f}`);
  return r.status === 0 ? r.stdout : null;
};
const sources = dockerfileSources(show(opts.dockerfile));
const ignore = show('.dockerignore') ?? '';
const v = dockerignoreLeavesOut(ignore, files, sources);
if (v.unsupported.length) {
  fail(
    `.dockerignore has pattern(s) this gate cannot read (${v.unsupported.join(', ')}), so it cannot tell whether the context it streams is the one ` +
      `CI's checkout gives docker. Write them with *, ** and ? alone (scripts/release/checks.mjs, dockerignoreRules).`,
  );
}
if (v.excluded.length) {
  fail(
    `.dockerignore leaves out ${v.excluded.length} tracked file(s) ${opts.dockerfile} copies:\n  ${v.excluded.slice(0, 20).join('\n  ')}\n` +
      `CI's build does not see them and this one (git archive of ${sha.slice(0, 10)}) would. Untrack them, or take them back in .dockerignore.`,
  );
}

const date = new Date().toISOString().replace(/\.\d+Z$/, 'Z');
const args = [
  'build', '--progress=plain', '--platform', opts.platform,
  '--build-arg', `VERSION=${opts.version}`, '--build-arg', `COMMIT=${sha}`, '--build-arg', `DATE=${date}`,
  '-f', opts.dockerfile, '-t', opts.tag, '-',
];
console.log(`context: git archive ${sha} (${files.length} tracked files), not the working tree; .dockerignore leaves out none of what ${opts.dockerfile} copies${sources ? '' : ' (its sources cannot be told: the whole tree was checked)'}`);

const t0 = Date.now();
const [bin, dargs] = dockerArgv(args, findBash());
const env = { ...process.env, MSYS_NO_PATHCONV: '1', MSYS2_ARG_CONV_EXCL: '*' };
// core.autocrlf off: `git archive` converts line endings as a checkout does,
// and Git for Windows turns autocrlf on system-wide. Off, the tree is what
// CI's Linux checkout writes - .gitattributes (`* text=auto eol=lf`) still
// decides every file it names.
const archive = spawn('git', ['-C', repo, '-c', 'core.autocrlf=false', 'archive', '--format=tar', sha], { stdio: ['ignore', 'pipe', 'inherit'], windowsHide: true });
const build = spawn(bin, dargs, { stdio: ['pipe', 'inherit', 'inherit'], env, windowsHide: true });
let bytes = 0;
archive.stdout.on('data', (d) => {
  bytes += d.length;
});
archive.stdout.pipe(build.stdin);
// docker may exit before it has read everything (a bad flag): its stdin then
// closes under the pipe, which is no crash of ours - its exit code says why.
build.stdin.on('error', () => {});
const exited = (child, name) =>
  new Promise((resolve) => {
    child.on('error', (e) => {
      console.error(`could not start ${name}: ${e.message}`);
      resolve(127);
    });
    child.on('close', (code) => resolve(typeof code === 'number' ? code : 1));
  });
const [a, b] = await Promise.all([exited(archive, 'git archive'), exited(build, 'docker build')]);
console.log(`git archive exited ${a} (${(bytes / 1048576).toFixed(1)} MB), docker build exited ${b}, in ${Math.round((Date.now() - t0) / 1000)} s`);
process.exit(a === 0 && b === 0 ? 0 : 1);
