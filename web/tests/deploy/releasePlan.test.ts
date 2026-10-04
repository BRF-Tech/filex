// The gates of THIS repository's release (scripts/release/plan.mjs) cannot
// quietly disappear.
//
// ⚠ Every gate in the plan is there because a release shipped without it. A
// gate removed "for now" to get a release out is a gate the next release does
// not have either — this list is how deleting one becomes a visible decision
// in a diff instead of a silent one. Deleting a line here is allowed; it has
// to be done on purpose, with the incident that justified the gate in mind.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { toWslPath } from '../../../scripts/lib/go-build.mjs';
import { findBash, shq, slash } from '../../../scripts/release/engine.mjs';
import plan from '../../../scripts/release/plan.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const bash = findBash();
const p = plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9' });
const names = (list: Array<{ name: string }>) => list.map((g) => g.name);

/** gate-name fragment → why it is in the plan */
const REQUIRED: Record<string, Record<string, string>> = {
  docs: {
    'every relative link resolves': 'CONTRIBUTING step 3',
    'the site builds': 'lesson #34: a broken code span kills the build',
    'every in-page anchor lands': '98 of 366 anchors were dead on a green build',
    'Helm chart lints and renders': 'lessons #430/#432',
  },
  pretag: {
    'build: packages, admin (vue-tsc': 'v0.38.1: vue-tsc was the only gate that saw the WIP files',
    'serves the UI just built': '2026-09-14: a binary with a 16-hour-old UI',
    'go: vet + test': 'the backend suite',
    'migrations on sqlite, postgres AND mysql': 'the parity tests skip without a DSN',
    'web unit (TZ=UTC': 'lesson #448: v0.43.0 CI failed in UTC',
    'desktop: typecheck + unit': '0.43.x shipped a desktop window that never parsed (#52)',
    'the Store package works as a Store copy': 'the GitHub runner never activates an MSIX; release.yml submits what it builds (2026-09-26)',
    'docker: full image': 'v0.43.0: npm published, images never built',
    'docker: slim image': 'v0.43.0',
    'both images report the release': 'the version is baked in',
    'shop window: a throwaway instance': 'CONTRIBUTING step 12, before the tag',
    'e2e: Cypress': 'the suite release.yml waits for',
    'e2e: Playwright': 'the journeys Cypress does not walk',
  },
  exportGates: {
    'go build + vet + test (public module path)': 'lesson #55 checklist: test IN the export',
    'web and package unit tests pass in the public tree': 'v0.45.0: a test read a private-only file and failed the public gate',
    'goreleaser check, with the GoReleaser CI uses': 'lesson #510',
    'workflow guards ran against the workflows that will run': 'lessons #455, #461, #510',
  },
  published: {
    'GitHub Release': '0.44.0/0.44.1 shipped without the desktop packages',
    'ghcr:': 'v0.43.0 had no images',
    'npm:': 'every package under packages/ (0.48 added filex-app-ui, which filex-core depends on)',
    'latest.yml offers the x64 and the arm64 installer': '0.48.1: Windows reads ONE feed whatever the CPU; x64 first',
    'are amd64 + arm64': '0.48.1: the arm64 images are smoke-tested before they are tagged',
    'Snap Store: filex-app': '0.48.1: one revision per architecture on stable',
    'winget: the': '0.48.1: both winget manifests name the arm64 installer',
    'Microsoft Store:': '0.48.1: the Store gets the x64 + arm64 bundle',
  },
  deployed: {
    'update': 'lesson #69: stable.json three releases behind',
    'desktop feeds offer': 'lesson #69: the desktop feed four releases behind',
    'desktop/latest.yml offers the x64 and the arm64 installer': '0.48.1: the feed the installed apps read',
    "pages, not a snapshot": 'lesson #511: docs.filex.sh served an old snapshot',
    'shop window: the published surfaces': 'CONTRIBUTING step 12, after the docs',
  },
};

describe("this repository's release plan", () => {
  for (const [stage, gates] of Object.entries(REQUIRED)) {
    it(`${stage}: keeps every gate a past release needed`, () => {
      const have = names(p[stage]);
      for (const [fragment, why] of Object.entries(gates)) {
        expect(have.some((n) => n.includes(fragment)), `${stage} lost "${fragment}" — it exists because: ${why}`).toBe(true);
      }
    });
  }

  it('gate names are unique (each writes its own log)', () => {
    for (const stage of ['audit', 'docs', 'pretag', 'exportGates', 'published', 'deployed']) {
      const n = names(p[stage]);
      expect(new Set(n).size, stage).toBe(n.length);
    }
  });

  it('the pretag chain runs Cypress and Playwright LAST, after the builds they test', () => {
    const n = names(p.pretag);
    const build = n.findIndex((x) => x.startsWith('build: server binary'));
    const e2e = n.findIndex((x) => x.startsWith('e2e:'));
    expect(build).toBeGreaterThanOrEqual(0);
    expect(e2e).toBeGreaterThan(build);
  });

  it('names the workflow guards by titles that exist, so a rename cannot make the gate vacuous', () => {
    const guards = p.exportGates.find((g: { vitest?: unknown }) => g.vitest).vitest.mustPass as string[];
    const sources = ['releaseGatesImages.test.ts', 'goreleaserTemplates.test.ts', 'wingetCla.test.ts', 'msstoreSubmit.test.ts', 'releaseArm64.test.ts'].map((f) =>
      fs.readFileSync(path.join(REPO, 'web', 'tests', 'deploy', f), 'utf8'),
    );
    expect(guards.length).toBeGreaterThanOrEqual(5);
    for (const title of guards) expect(sources.some((s) => s.includes(title)), title).toBe(true);
  });

  // ⚠ The export gate runs a LIST of files, not the whole suite. v0.47.0's
  // first export: the winget CLA and Store guards were named in
  // WORKFLOW_GUARDS but their files were not in that list, so the gate said
  // they never ran — after an hour of pretag. Every guard title has to live
  // in a file the gate runs.
  it('runs every workflow guard in the export gate, not only names it', () => {
    const gate = p.exportGates.find((g: { vitest?: unknown }) => g.vitest).vitest as { files: string[]; mustPass: string[] };
    const inGate = gate.files.map((f) => fs.readFileSync(path.join(REPO, 'web', f), 'utf8'));
    for (const title of gate.mustPass) {
      expect(inGate.some((s) => s.includes(title)), `"${title}" is in no file the export gate runs`).toBe(true);
    }
  });

  // v0.48.0: the fixture gate's echo.wasm was overwritten by the Go test
  // gate's own rsync of the Windows checkout, whose copy was older than the
  // echo app's main.go; every app-plugin test refused.
  it('builds the wasm fixture in the Go test gate itself, before go test', () => {
    const gate = p.pretag.find((g: { name: string }) => g.name === 'go: vet + test') as { script: string };
    expect(gate, 'the pretag chain has no "go: vet + test" gate').toBeTruthy();
    const fixture = gate.script.indexOf('build-wasm-fixture.sh');
    expect(fixture, gate.script).toBeGreaterThanOrEqual(0);
    expect(gate.script.indexOf('go test')).toBeGreaterThan(fixture);
  });

  // ⚠⚠ v0.50.0 pretag (lesson #960, #139): the fixture gate ran in a WSL
  // mirror of the whole repository and refreshed only the mirror's echo.wasm.
  // The Windows checkout's copy, the one the Playwright gate installs,
  // predated the echo change spec 192 tested; 192 failed after an hour of
  // chain. The gate builds in the Go module (under WSL its mirror on WSL's own
  // disk, never Go on /mnt) with the CHECKOUT's script, which writes the
  // module back (the next test runs that script).
  it('the echo.wasm gate builds in the Go module and refreshes the checkout before Playwright installs from it', () => {
    type GoGate = { name: string; script: string; cmd: (c: object, toolchain?: string) => string[] };
    const gates = p.pretag as GoGate[];
    const i = gates.findIndex((g) => g.name === 'go: echo.wasm fixture');
    expect(i, 'the pretag chain has no "go: echo.wasm fixture" gate').toBeGreaterThanOrEqual(0);
    const gate = gates[i];
    const builds = /FILEX_BACKEND_DIR="\$PWD" bash "\$FILEX_CHECKOUT\/scripts\/build-wasm-fixture\.sh"/;
    expect(gate.script, "the gate does not run the checkout's script in the module it is in, so the checkout keeps its old echo.wasm").toMatch(builds);
    expect(i, 'the fixture is built after the Playwright gate that installs it').toBeLessThan(gates.findIndex((g) => g.name.startsWith('e2e: Playwright')));

    const ctx = { repo: REPO, bash: 'bash' };
    const wsl = gate.cmd(ctx, 'wsl');
    const sh = wsl[wsl.length - 1];
    expect(wsl.slice(0, 2)).toEqual(['wsl', '-e']);
    // ~/wt/<repository>/backend (a tree without .git mirrors as ~/wt/backend).
    expect(sh, 'under WSL the gate runs in the mirror of the Go module').toMatch(/&& cd "\$HOME\/wt\/(?:[^"/]+\/)?backend" &&/);
    expect(sh, 'never Go on /mnt').not.toMatch(/cd ["']?\/mnt\//);
    expect(sh, 'the checkout, as WSL reaches it').toContain(`FILEX_CHECKOUT=${shq(toWslPath(REPO))}`);
    expect(sh).toMatch(builds);

    const native = gate.cmd(ctx, 'native');
    expect(native[native.length - 1]).toContain(`FILEX_CHECKOUT=${shq(slash(REPO))} && cd ${shq(slash(path.join(REPO, 'backend')))} && `);
  });

  it.runIf(!!bash)('the fixture script builds in the module it is given and writes the module back into its own checkout', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-echo-fixture-'));
    try {
      const rel = path.join('internal', 'wasmplugin', 'testdata', 'echo', 'echo.wasm');
      const checkout = path.join(dir, 'checkout');
      const mirror = path.join(dir, 'mirror', 'backend');
      const script = path.join(checkout, 'scripts', 'build-wasm-fixture.sh');
      fs.mkdirSync(path.dirname(script), { recursive: true });
      fs.copyFileSync(path.join(REPO, 'scripts', 'build-wasm-fixture.sh'), script);
      for (const b of [path.join(checkout, 'backend'), mirror]) fs.mkdirSync(path.dirname(path.join(b, rel)), { recursive: true });
      // A stand-in `go` that writes where it was asked to build, and from where.
      const bin = path.join(dir, 'bin');
      fs.mkdirSync(bin);
      fs.writeFileSync(
        path.join(bin, 'go'),
        '#!/usr/bin/env bash\nout=""\nwhile [ $# -gt 0 ]; do if [ "$1" = -o ]; then out="$2"; fi; shift; done\nprintf "built in %s\\n" "$PWD" > "$out"\n',
      );
      fs.chmodSync(path.join(bin, 'go'), 0o755);
      // One PATH key: Windows spells it Path, and a second spelling is a coin toss.
      const base: NodeJS.ProcessEnv = { ...process.env };
      const pathKey = Object.keys(base).find((k) => k.toUpperCase() === 'PATH') ?? 'PATH';
      const searchPath = base[pathKey];
      delete base[pathKey];
      const run = (env: Record<string, string>) =>
        spawnSync(bash!, [slash(script)], { encoding: 'utf8', env: { ...base, PATH: `${bin}${path.delimiter}${searchPath}`, ...env } });

      // As the release's WSL gate runs it: Go in the mirror, the module back in the checkout.
      let r = run({ FILEX_BACKEND_DIR: slash(mirror) });
      expect(r.status, `${r.stdout}${r.stderr}`).toBe(0);
      expect(fs.existsSync(path.join(mirror, rel)), 'the module was not built in the module dir it was given').toBe(true);
      const built = fs.readFileSync(path.join(mirror, rel), 'utf8');
      expect(built).toContain('/mirror/backend');
      expect(fs.existsSync(path.join(checkout, 'backend', rel)), 'the checkout did not get the module').toBe(true);
      expect(fs.readFileSync(path.join(checkout, 'backend', rel), 'utf8'), 'the checkout holds another module').toBe(built);

      // Run as it always was: in its own checkout, nothing to copy over itself.
      r = run({ FILEX_BACKEND_DIR: '' });
      expect(r.status, `${r.stdout}${r.stderr}`).toBe(0);
      expect(fs.readFileSync(path.join(checkout, 'backend', rel), 'utf8')).toContain('/checkout/backend');
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  it('tags the Go module under the directory it lives in (backend/vX.Y.Z)', () => {
    // stages.mjs signs and pushes `${plan.backendTagPrefix}${tag}`; a plan
    // without the prefix had the 0.50 sign stage look for "undefinedv0.50.0".
    expect(p.backendTagPrefix).toBe('backend/');
    expect(fs.existsSync(path.join(REPO, p.backendTagPrefix, 'go.mod')), 'no Go module under the tag prefix').toBe(true);
  });

  it('signs with the maintainer key and keeps only the contact addresses public', () => {
    expect(p.signingKeys).toEqual(['EFA3B1262FD992800DBBB5E3A8FEBA97FF786513']);
    expect(p.privateHosts.forbid.length).toBeGreaterThanOrEqual(2);
    for (const a of p.privateHosts.allow) expect(a).toMatch(/^(security|hello)@/);
  });
});
