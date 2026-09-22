// Every image runs filex under an init that reaps orphaned processes.
//
// ⚠⚠ Without one, filex was PID 1 of its container. Office thumbnails run
// LibreOffice, whose conversion leaves helper processes behind (gpgconf, gpgsm,
// gpg) that are re-parented to PID 1 when it exits — and the Go runtime only
// ever waits for children IT started through os/exec. Nobody collected them.
// Measured on one deployment: 19,110 zombie processes in ten days, until the
// container's task limit was full, the healthcheck could not fork `wget`, and
// every thumbnail failed with "can't fork".
//
// A Go-side reaper is not the answer: a loop calling wait4(-1) collects the
// children os/exec is waiting for too, and their Wait() then fails with
// "no child processes" — a thumbnail that converted fine would be recorded
// as failed. tini exists for exactly this, and baking it into the image covers
// every way the image is run (compose, Helm, CasaOS, Umbrel, Runtipi, Unraid,
// `docker run`), not only the ones that can say `init: true`.
//
// And the entrypoint has to keep ending in `exec`: tini forwards SIGTERM to
// its child, so filex must BE that child, not a shell waiting on it.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');
const read = (...p: string[]) => readFileSync(path.join(REPO, ...p), 'utf8');

const IMAGES = ['Dockerfile', 'Dockerfile.slim', 'Dockerfile.local'];

/** The runtime stage: everything after the last FROM. */
function runtimeStage(dockerfile: string): string {
  const at = dockerfile.lastIndexOf('\nFROM ');
  return at < 0 ? dockerfile : dockerfile.slice(at);
}

describe.each(IMAGES)('docker/%s', (name) => {
  const stage = runtimeStage(read('docker', name));

  it('installs tini in the image that runs', () => {
    // The whole `apk add` command, line continuations folded.
    const apk = stage.match(/RUN apk --no-cache add([\s\S]*?)(?:&&|\n(?!\s))/);
    expect(apk, 'no `apk --no-cache add` in the runtime stage').not.toBeNull();
    const packages = apk![1].replace(/\\\n/g, ' ').split(/\s+/).filter(Boolean);
    expect(packages).toContain('tini');
  });

  it('starts tini as PID 1, with the entrypoint as its child', () => {
    const entry = stage.match(/^ENTRYPOINT (.+)$/m);
    expect(entry, 'no ENTRYPOINT').not.toBeNull();
    // -s: register as a subreaper too, so an outer init (compose `init: true`,
    // `docker run --init`) does not make tini warn that it cannot reap.
    expect(JSON.parse(entry![1])).toEqual(['/sbin/tini', '-s', '--', '/usr/local/bin/docker-entrypoint.sh']);
  });

  it('keeps `serve` as the command', () => {
    const cmd = stage.match(/^CMD (.+)$/m);
    expect(cmd).not.toBeNull();
    expect(JSON.parse(cmd![1])).toEqual(['serve']);
  });
});

describe('docker/entrypoint.sh', () => {
  const script = read('docker', 'entrypoint.sh');

  it('replaces itself with filex on every path — it never forks it', () => {
    const code = script
      .split('\n')
      .filter((line) => !line.trim().startsWith('#'))
      .join('\n');
    const runs = code.match(/^.*"\$BIN".*$/gm) ?? [];
    expect(runs.length, 'no line runs "$BIN" any more').toBeGreaterThan(0);
    for (const line of runs) {
      expect(line.trim(), 'filex must be exec-ed, so tini’s signals reach it').toMatch(/^exec\s/);
    }
  });
});
