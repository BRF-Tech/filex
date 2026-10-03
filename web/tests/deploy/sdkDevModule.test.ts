// scripts/pluginkit-devmodule.sh writes the guest SDK copy the apps build
// against (the other half, the staleness guard, is sdkStamp.test.ts).
//
// ⚠ 0.50 (lesson #922): the generated go.mod named go-pdk alone while
// pkg/pluginkit/thumbkit imports golang.org/x/image, and the script ended in
// `go mod tidy || true`. In a shell without Go the copy was written with no
// go.sum and the script exited 0, so the copy's own `go build` failed on the
// first import; in a shell with Go the tidy fetched the NEWEST x/image, which
// needs a newer Go than filex builds with. Nothing reported either.
//
// These drive the real script against a stand-in `go` (GO=...), so they
// measure what it writes and what it does when Go fails, with no Go installed.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

import { findBash } from '../../../scripts/release/engine.mjs';

const REPO = path.resolve(__dirname, '../../..');
const slash = (p: string) => p.split(path.sep).join('/');
const SCRIPT = slash(path.join(REPO, 'scripts', 'pluginkit-devmodule.sh'));
const bash = findBash();

/** The version backend/go.mod pins for `mod`. */
function pinned(mod: string): string {
  for (const line of fs.readFileSync(path.join(REPO, 'backend', 'go.mod'), 'utf8').split('\n')) {
    const f = line.trim().replace(/^require\s+/, '').split(/\s+/);
    if (f[0] === mod && f[1]) return f[1];
  }
  throw new Error(`backend/go.mod does not require ${mod}`);
}

const temps: string[] = [];
afterEach(() => {
  for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true });
});

/** A scratch directory with a stand-in `go` that logs each call. */
function scratch() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-sdk-gen-'));
  temps.push(dir);
  const go = path.join(dir, 'fakego');
  fs.writeFileSync(
    go,
    '#!/usr/bin/env bash\necho "GOTOOLCHAIN=${GOTOOLCHAIN:-} $*" >> "$FAKE_GO_LOG"\nexit "${FAKE_GO_EXIT:-0}"\n',
  );
  fs.chmodSync(go, 0o755);
  return { dir, go, log: path.join(dir, 'go.log'), out: path.join(dir, 'sdk') };
}

function generate(out: string, env: Record<string, string>) {
  return spawnSync(bash!, [SCRIPT, slash(out)], {
    encoding: 'utf8',
    env: { ...process.env, PATH: `${path.dirname(process.execPath)}${path.delimiter}${process.env.PATH}`, ...env },
  });
}

describe('the guest SDK generator (scripts/pluginkit-devmodule.sh)', () => {
  it.runIf(!!bash)('writes every module the SDK imports at the version backend/go.mod pins, and tidies with the local Go', () => {
    const s = scratch();
    const r = generate(s.out, { GO: slash(s.go), FAKE_GO_LOG: slash(s.log) });
    expect(r.status, `${r.stdout}${r.stderr}`).toBe(0);
    const gomod = fs.readFileSync(path.join(s.out, 'go.mod'), 'utf8');
    // thumbkit's import: the one the old go.mod left out.
    expect(gomod).toContain(`golang.org/x/image ${pinned('golang.org/x/image')}`);
    expect(gomod).toContain(`github.com/extism/go-pdk ${pinned('github.com/extism/go-pdk')}`);
    expect(gomod).toMatch(/^module github\.com\/brf-tech\/filex\/backend$/m);
    // The tidy ran, through GO, pinned to the local toolchain.
    expect(fs.readFileSync(s.log, 'utf8')).toContain('GOTOOLCHAIN=local mod tidy');
  });

  it.runIf(!!bash)('a failing go mod tidy fails the generator', () => {
    const s = scratch();
    const r = generate(s.out, { GO: slash(s.go), FAKE_GO_LOG: slash(s.log), FAKE_GO_EXIT: '1' });
    expect(r.status, `${r.stdout}${r.stderr}`).not.toBe(0);
  });

  it.runIf(!!bash)('without Go it refuses, and leaves the existing copy alone', () => {
    const s = scratch();
    fs.mkdirSync(s.out, { recursive: true });
    const marker = path.join(s.out, 'still-here');
    fs.writeFileSync(marker, '');
    const r = generate(s.out, { GO: slash(path.join(s.dir, 'no-such-go')) });
    expect(r.status).toBe(1);
    expect(r.stderr).toContain('Go is needed');
    expect(fs.existsSync(marker), 'the old copy was removed before the Go check').toBe(true);
  });
});
