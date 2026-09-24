// Docker, for the one screenshot scene whose picture depends on programs this
// machine may not have: the converter wizard, which lists the conversion
// engines the SERVER found (ffmpeg, ImageMagick, Ghostscript, poppler,
// LibreOffice, rsvg) and names every missing one "Not installed on this
// server". On a workstation without them that is a shop-window picture telling
// strangers the feature is broken.
//
// The engines ship in filex's own full image (docker/Dockerfile → the `:full`
// tag), so the scene runs THIS tree's build inside that image instead of
// installing anything on the host: the image contributes its PATH, the binary
// is the one just built. See e2e/shots/scene.mjs → bootInstance({ engines }).
//
// ⚠ One module for the container's whole life — start, find, remove — used by
// the scene that starts one and by `pnpm shots` (scripts/shots.mjs), which
// removes whatever a crashed scene left behind. A container is not a child
// process: the process sweep (scripts/lib/procs.mjs) cannot see it, so without
// the label below a scene killed by the run's timeout would leave a filex
// running in Docker, holding a port, until somebody noticed.

import { spawnSync } from 'node:child_process';
import { readFileSync, statSync } from 'node:fs';

/** The image whose PATH carries every engine filex can drive. */
export const ENGINES_IMAGE = 'ghcr.io/brf-tech/filex:full';

/** Every container a shot scene starts carries this label; its value is the run's id. */
export const RUN_LABEL = 'filex-shots-run';

/**
 * How to reach the docker CLI from node.
 *
 * ⚠⚠ Not always a program called `docker`. On the maintainer's Windows
 * workstation the engine is docker-ce inside WSL and the CLI on PATH is a
 * `docker.cmd` shim (`wsl.exe -d Ubuntu-24.04 -e docker %*`): measured
 * 2026-09-21, `spawnSync('docker')` answers ENOENT there — node does not run a
 * .cmd without a shell — while `docker` works in every terminal, so the scene
 * reported "Docker is not available" on a machine where it was running. The
 * shell is used ONLY when the plain lookup fails on Windows, and every
 * argument this module passes is checked to be shell-inert (argv below).
 */
let reach = null;
function reachDocker() {
  if (reach) return reach;
  const direct = spawnSync('docker', ['version', '--format', '{{.Client.Version}}'], { encoding: 'utf8', windowsHide: true });
  if (!direct.error) return (reach = { shell: false });
  if (process.platform === 'win32') {
    const viaShell = spawnSync('docker version --format {{.Client.Version}}', { encoding: 'utf8', windowsHide: true, shell: true });
    if (!viaShell.error && viaShell.status === 0) return (reach = { shell: true });
  }
  return (reach = { shell: false, missing: direct.error.message });
}

/** An argument cmd.exe passes through unchanged — no quoting games, ever. */
const INERT = /^[\w@%+=:,./{}-]+$/;

function docker(args, opts = {}) {
  const how = reachDocker();
  if (!how.shell) return spawnSync('docker', args, { encoding: 'utf8', windowsHide: true, ...opts });
  const bad = args.find((a) => !INERT.test(a));
  if (bad !== undefined) throw new Error(`refusing to hand ${JSON.stringify(bad)} to docker through cmd.exe: not shell-inert`);
  return spawnSync(['docker', ...args].join(' '), { encoding: 'utf8', windowsHide: true, shell: true, ...opts });
}

/**
 * Is there a Docker daemon to talk to, and on which architecture? The arch
 * decides what the Linux binary is built for — an arm64 Mac runs the arm64
 * variant of the image.
 */
export function dockerInfo() {
  const how = reachDocker();
  if (how.missing) return { ok: false, why: `no docker CLI (${how.missing})` };
  const r = docker(['version', '--format', '{{.Server.Os}}/{{.Server.Arch}}']);
  if (r.error || r.status !== 0) {
    return { ok: false, why: (r.error?.message || r.stderr || 'docker version failed').trim().split('\n')[0] };
  }
  const [os, arch] = r.stdout.trim().split('/');
  return { ok: os === 'linux', os, arch, why: os === 'linux' ? '' : `the Docker daemon runs ${os} containers, not linux` };
}

/** Pulls `image` unless it is already here. The full image is ~1.8 GB, once. */
export function ensureImage(image, log = () => {}) {
  if (docker(['image', 'inspect', image, '--format', '{{.Id}}']).status === 0) return;
  log(`pulling ${image} (once — about 1.8 GB)`);
  const r = docker(['pull', image], { stdio: ['ignore', 'inherit', 'inherit'] });
  if (r.status !== 0) throw new Error(`docker pull ${image} failed (exit ${r.status})`);
}

/**
 * One file as a tar archive, executable. `docker cp - <ctr>:<dir>` reads
 * exactly this from stdin.
 */
function tarOne(name, data, mode = 0o755) {
  const h = Buffer.alloc(512);
  const put = (s, off, len) => h.write(String(s).slice(0, len), off, len, 'ascii');
  const oct = (n, len) => n.toString(8).padStart(len - 1, '0') + '\0';
  put(name, 0, 100);
  put(oct(mode, 8), 100, 8);
  put(oct(0, 8), 108, 8);
  put(oct(0, 8), 116, 8);
  put(oct(data.length, 12), 124, 12);
  put(oct(Math.floor(Date.now() / 1000), 12), 136, 12);
  put('        ', 148, 8);
  put('0', 156, 1);
  put('ustar\0', 257, 6);
  put('00', 263, 2);
  let sum = 0;
  for (const b of h) sum += b;
  put(sum.toString(8).padStart(6, '0') + '\0 ', 148, 8);
  const pad = Buffer.alloc((512 - (data.length % 512)) % 512);
  return Buffer.concat([h, data, pad, Buffer.alloc(1024)]);
}

/**
 * Starts `binary` inside `image` with `env`, publishing the container's
 * `innerPort` on 127.0.0.1:`port`. Returns the container's name.
 *
 * ⚠ The binary goes in as a tar stream on stdin (`docker cp -`), not as a path
 * and not as a bind mount. A path names a file on the machine running node;
 * the docker CLI may not be on that machine's filesystem at all — the WSL shim
 * above reads Linux paths, a remote `DOCKER_HOST` or a CI runner's
 * docker-in-docker reads none of ours — and a bind mount would start the
 * container with an empty file where filex should be. Bytes on stdin arrive
 * the same way everywhere, with their executable bit set in the header.
 *
 * ⚠ Not the image's entrypoint either: that one runs the image's OWN filex,
 * which is the last release, not this tree.
 */
export function startContainer({ name, image, port, innerPort, env, binary, marker, log = () => {} }) {
  const dir = '/usr/local/bin';
  const file = 'filex-shots';
  const create = docker([
    'create',
    '--name', name,
    '--label', `${RUN_LABEL}=${marker}`,
    '-p', `127.0.0.1:${port}:${innerPort}`,
    ...Object.entries(env).flatMap(([k, v]) => ['-e', `${k}=${v}`]),
    '--entrypoint', `${dir}/${file}`,
    image,
    'serve',
  ]);
  if (create.status !== 0) throw new Error(`docker create ${name}: ${(create.stderr || create.error?.message || '').trim()}`);
  log(`copying ${(statSync(binary).size / 1048576).toFixed(0)} MB of this tree's linux build into ${name}`);
  const cp = docker(['cp', '-', `${name}:${dir}`], { input: tarOne(file, readFileSync(binary)), maxBuffer: 1 << 20 });
  if (cp.status !== 0) {
    removeContainer(name);
    throw new Error(`docker cp into ${name}: ${(cp.stderr || cp.error?.message || '').trim()}`);
  }
  const start = docker(['start', name]);
  if (start.status !== 0) {
    const logs = containerLogs(name);
    removeContainer(name);
    throw new Error(`docker start ${name}: ${(start.stderr || '').trim()}\n${logs}`);
  }
  log(`container ${name} (${image}) → http://127.0.0.1:${port}`);
  return name;
}

/** The last lines the container printed — what a failed boot has to say. */
export function containerLogs(name, lines = 40) {
  const r = docker(['logs', '--tail', String(lines), name]);
  return `${r.stdout || ''}${r.stderr || ''}`.trim();
}

/** Removes one container, running or not. Quiet when there is none. */
export function removeContainer(name) {
  docker(['rm', '-f', name]);
}

/**
 * Removes every container a run left. Returns their names. Safe to call where
 * Docker is not installed at all — then there is nothing to remove.
 */
export function removeRunContainers(marker) {
  if (!marker || !INERT.test(marker) || reachDocker().missing) return [];
  const r = docker(['ps', '-a', '--filter', `label=${RUN_LABEL}=${marker}`, '--format', '{{.Names}}']);
  if (r.error || r.status !== 0) return [];
  const names = r.stdout.split('\n').map((s) => s.trim()).filter(Boolean);
  for (const n of names) removeContainer(n);
  return names;
}
