// The release candidate the gate stage looks into (task #361): the images the
// dry run of the export commit pushed by digest - the ones the tag run
// promotes - pulled by that digest, never built here, and the binary of the
// full image copied out for the shop window. These tests hand in a stand-in
// for GitHub and for docker: nothing is downloaded, pulled or run.
//
// ⚠ Why (the owner, 2026-11-18): the build host builds, lints and looks into
// what GitHub built. Until then the pretag built both images on the build
// host and smoke-tested those - throwaway images, not the shipped ones.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { candidateRefs, dropCandidate, prepareCandidate } from '../../../scripts/release/candidate.mjs';

const roots: string[] = [];
afterAll(() => {
  for (const r of roots) fs.rmSync(r, { recursive: true, force: true });
});
const tmp = () => {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-candidate-'));
  roots.push(d);
  return d;
};
const HEX_A = 'a'.repeat(64);
const HEX_B = 'b'.repeat(64);
const CAND = { image: 'ghcr.io/brf-tech/filex', artifact: 'digests-amd64', variants: ['full', 'slim'], binary: '/usr/local/bin/filex', localBinary: 'bin/filex-release-e2e.exe' };

/** What release.yml's "Record digests" step leaves in the artifact. */
function artifact(dir: string, files: Record<string, string[]>) {
  for (const [v, names] of Object.entries(files)) {
    fs.mkdirSync(path.join(dir, v), { recursive: true });
    for (const n of names) fs.writeFileSync(path.join(dir, v, n), '');
  }
}

/** A docker stand-in: records every call; `here` are the refs it already has. */
function fakeDocker({ here = [] as string[], pullFails = false } = {}) {
  const calls: string[][] = [];
  const docker = (args: string[]) => {
    calls.push(args);
    if (args[0] === 'image' && args[1] === 'inspect') return { status: here.includes(args[2]!) ? 0 : 1, stdout: '', stderr: '' };
    if (args[0] === 'pull') return pullFails ? { status: 1, stdout: '', stderr: 'denied: not found' } : { status: 0, stdout: '', stderr: '' };
    if (args[0] === 'cp') {
      fs.writeFileSync(args[2]!, 'the binary');
      return { status: 0, stdout: '', stderr: '' };
    }
    return { status: 0, stdout: '', stderr: '' };
  };
  return { docker, calls };
}

const gh = (files: Record<string, string[]>) => ({
  asked: [] as Array<{ runId: number; name: string }>,
  download(o: { runId: number; name: string; dir: string }) {
    this.asked.push({ runId: o.runId, name: o.name });
    artifact(o.dir, files);
    return { ok: true };
  },
});

describe('the candidate the gate stage looks into', () => {
  it("reads the dry run's digests as one file per variant, named after the digest", () => {
    const dir = tmp();
    artifact(dir, { full: [HEX_A], slim: [HEX_B] });
    expect(candidateRefs(dir, CAND.image)).toEqual({ refs: { full: `${CAND.image}@sha256:${HEX_A}`, slim: `${CAND.image}@sha256:${HEX_B}` } });
    const two = tmp();
    artifact(two, { full: [HEX_A, HEX_B], slim: [HEX_B] });
    expect(candidateRefs(two, CAND.image).error).toMatch(/full\/ holds 2 digest\(s\), not one/);
    const none = tmp();
    artifact(none, { full: [HEX_A] });
    expect(candidateRefs(none, CAND.image).error).toMatch(/no slim\/ directory/);
  });

  it('pulls each image by its digest, builds nothing, copies the full image\'s binary out, and removes only what it pulled', () => {
    const work = path.join(tmp(), 'candidate');
    const g = gh({ full: [HEX_A], slim: [HEX_B] });
    const full = `${CAND.image}@sha256:${HEX_A}`;
    const slim = `${CAND.image}@sha256:${HEX_B}`;
    const d = fakeDocker({ here: [slim] });
    const r = prepareCandidate({ gh: g, runId: 42, cand: CAND, workDir: work, repo: '/repo', platform: 'linux', docker: d.docker });
    expect(r.error, r.error).toBeUndefined();
    expect(g.asked).toEqual([{ runId: 42, name: 'digests-amd64' }]);
    expect(r.candidate).toEqual({ full, slim, bin: path.join(work, 'filex') });
    expect(fs.readFileSync(r.candidate!.bin, 'utf8')).toBe('the binary');
    // The one image that was not here is pulled, for this machine's architecture.
    expect(d.calls.filter((c) => c[0] === 'pull')).toEqual([['pull', '--platform', 'linux/amd64', full]]);
    expect(r.pulled).toEqual([full]);
    // Nothing is built, and the binary comes out of the full image.
    expect(d.calls.some((c) => c[0] === 'build' || c[0] === 'buildx')).toBe(false);
    expect(d.calls.find((c) => c[0] === 'create')).toContain(full);
    expect(d.calls.find((c) => c[0] === 'cp')![1]).toMatch(/:\/usr\/local\/bin\/filex$/);
    const drop = fakeDocker();
    expect(dropCandidate({ pulled: r.pulled, workDir: work, docker: drop.docker }).left).toEqual([]);
    expect(drop.calls).toEqual([['rmi', full]]);
    expect(fs.existsSync(work)).toBe(false);
  });

  it('is red with the reason when the artifact or a pull fails: could not look is not a pass', () => {
    const fails = { download: () => ({ error: 'HTTP 404: artifact expired' }) };
    expect(prepareCandidate({ gh: fails, runId: 7, cand: CAND, workDir: path.join(tmp(), 'c'), repo: '/repo', platform: 'linux', docker: fakeDocker().docker }).error).toMatch(/could not download digests-amd64 of the dry run 7: HTTP 404/);
    const denied = prepareCandidate({ gh: gh({ full: [HEX_A], slim: [HEX_B] }), runId: 7, cand: CAND, workDir: path.join(tmp(), 'c'), repo: '/repo', platform: 'linux', docker: fakeDocker({ pullFails: true }).docker });
    expect(denied.error).toMatch(/docker pull ghcr\.io\/brf-tech\/filex@sha256:a+: denied/);
    expect(prepareCandidate({ gh: {}, runId: 7, cand: CAND, workDir: path.join(tmp(), 'c'), repo: '/repo' }).error).toMatch(/cannot download/);
  });

  it("on Windows the image's Linux binary cannot run: the shop window boots the pretag's, and says so", () => {
    const d = fakeDocker();
    const r = prepareCandidate({ gh: gh({ full: [HEX_A], slim: [HEX_B] }), runId: 9, cand: CAND, workDir: path.join(tmp(), 'c'), repo: '/repo', platform: 'win32', docker: d.docker });
    expect(r.candidate!.bin).toBe(path.resolve('/repo', CAND.localBinary));
    expect(r.note).toMatch(/cannot run/);
    expect(d.calls.some((c) => c[0] === 'cp' || c[0] === 'create')).toBe(false);
  });
});
