// The release candidate, looked into on this machine before anything is
// tagged (task #361): the images the dry run of the export commit built and
// pushed to ghcr.io by digest - the very images the tag run promotes - pulled
// by that digest and run, with the binary of the full image copied out of it.
// Nothing is built here.
//
// ⚠ Why here and not in the pretag: until 2026-11-18 the pretag built both
// images on the build host and smoke-tested those - throwaway images, not the
// ones that ship (GitHub builds those), and two builds of 20-30 minutes on a
// machine whose load had pushed a neighbour out of memory. The owner's rule:
// the build host builds, lints and opens up what GitHub built.
//
// The gate stage (stages.mjs → gate) calls prepareCandidate once the dry run
// passed and kept what the tag run promotes, runs the plan's candidate gates
// on what it returns, and dropCandidate whatever they said.

import fs from 'node:fs';
import path from 'node:path';

import { docker as dockerRun } from './engine.mjs';

const HEX = /^[0-9a-f]{64}$/;

/**
 * The images a dry run pushed, from its digests artifact as downloaded into
 * `dir`: release.yml writes one empty file per variant, named after the
 * digest (`full/<hex>`, `slim/<hex>`). → { refs: { full, slim } } as
 * `<image>@sha256:<hex>`, or { error } when a variant has no digest or more
 * than one (a re-run of a leg overwrites its file: one is all there is).
 */
export function candidateRefs(dir, image, variants = ['full', 'slim']) {
  const refs = {};
  for (const v of variants) {
    let files;
    try {
      files = fs.readdirSync(path.join(dir, v));
    } catch {
      return { error: `the artifact has no ${v}/ directory` };
    }
    const hex = files.filter((f) => HEX.test(f));
    if (hex.length !== 1) return { error: `the artifact's ${v}/ holds ${hex.length} digest(s), not one (${files.slice(0, 5).join(', ') || 'nothing'})` };
    refs[v] = `${image}@sha256:${hex[0]}`;
  }
  return { refs };
}

const tail = (r) => String(r.stderr || r.stdout || '').trim().split('\n').slice(-3).join(' / ');

/**
 * Downloads the dry run's digests artifact, pulls each image by its digest
 * (amd64, this machine's), and copies the full image's binary out of it
 * (CGO=0: it runs on any Linux). On Windows the image's Linux binary cannot
 * run: `bin` is then the binary the pretag built (`cand.localBinary`, under
 * `repo`), and `note` says so.
 *
 * → { candidate: { full, slim, bin }, pulled, note } or { error, pulled }.
 * `pulled` are the refs this call pulled (an image that was here already is
 * left to whoever has it): dropCandidate removes exactly those.
 */
export function prepareCandidate({ gh, runId, cand, workDir, repo, platform = process.platform, docker = dockerRun }) {
  const pulled = [];
  fs.rmSync(workDir, { recursive: true, force: true });
  fs.mkdirSync(workDir, { recursive: true });
  if (!gh?.download) return { error: "the plan's GitHub cannot download a run's artifact", pulled };
  const dir = path.join(workDir, 'digests');
  const d = gh.download({ runId, name: cand.artifact, dir });
  if (d?.error) return { error: `could not download ${cand.artifact} of the dry run ${runId}: ${d.error}`, pulled };
  const r = candidateRefs(dir, cand.image, cand.variants);
  if (r.error) return { error: `${cand.artifact} of the dry run ${runId}: ${r.error}`, pulled };
  for (const ref of Object.values(r.refs)) {
    if (docker(['image', 'inspect', ref]).status === 0) continue;
    const p = docker(['pull', '--platform', 'linux/amd64', ref]);
    if (p.status !== 0) return { error: `docker pull ${ref}: ${tail(p)}`, pulled };
    pulled.push(ref);
  }
  if (platform === 'win32') {
    return {
      candidate: { ...r.refs, bin: path.resolve(repo, cand.localBinary) },
      pulled,
      note: `on Windows the image's Linux binary cannot run: the shop window boots ${cand.localBinary}, the binary the pretag built from the release commit`,
    };
  }
  const ctr = `filex-release-candidate-${process.pid}`;
  const bin = path.join(workDir, 'filex');
  docker(['rm', '-f', ctr]);
  const c = docker(['create', '--platform', 'linux/amd64', '--name', ctr, r.refs.full]);
  if (c.status !== 0) return { error: `docker create ${r.refs.full}: ${tail(c)}`, pulled };
  try {
    const cp = docker(['cp', `${ctr}:${cand.binary}`, bin]);
    if (cp.status !== 0 || !fs.existsSync(bin)) return { error: `docker cp ${cand.binary} out of ${r.refs.full}: ${tail(cp) || 'no file'}`, pulled };
    fs.chmodSync(bin, 0o755);
  } finally {
    docker(['rm', '-f', ctr]);
  }
  return { candidate: { ...r.refs, bin }, pulled, note: `the binary is ${cand.binary} of ${r.refs.full}` };
}

/** Removes the images prepareCandidate pulled (by digest, nothing else) and its directory. Never throws. */
export function dropCandidate({ pulled = [], workDir, docker = dockerRun }) {
  const left = [];
  for (const ref of pulled) if (docker(['rmi', ref]).status !== 0) left.push(ref);
  try {
    if (workDir) fs.rmSync(workDir, { recursive: true, force: true });
  } catch {
    /* a directory that survives is litter, not a failure */
  }
  return { left };
}
