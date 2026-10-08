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

import { ciParts, gateParts } from '../../../scripts/ci-parts.mjs';
import { toWslPath } from '../../../scripts/lib/go-build.mjs';
import { gateCacheKey, inputEntries, matchesInputs, parseLsTree, selectGates } from '../../../scripts/release/checks.mjs';
import { findBash, shq, slash } from '../../../scripts/release/engine.mjs';
import plan, { storeJobProblems } from '../../../scripts/release/plan.mjs';
import { STAGES } from '../../../scripts/release/stages.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const bash = findBash();
const p = plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9' });
const names = (list: Array<{ name: string }>) => list.map((g) => g.name);

type Gate = {
  name: string;
  lane?: string;
  after?: string[];
  always?: boolean;
  inputs?: string[];
  patchWhen?: string[];
  github?: string;
  patch?: string;
  patchOnly?: boolean;
  script?: string;
  sh?: unknown;
  vitest?: { files: string[] };
  cmd?: (c: object, toolchain?: string) => string[];
};
const pretag = p.pretag as Gate[];
const heavy = p.heavy as Gate[];
const every = [...pretag, ...heavy];
const gate = (name: string) => every.find((g) => g.name === name)!;
const BUILD_UI = 'build: packages, admin (vue-tsc + vite), embed';
const BUILD_BIN = 'build: server binary';
const GO_PATCH = 'go: vet + test, the packages a patch changed and their importers';
const MIGRATIONS = 'go: migrations on sqlite, postgres AND mysql';

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
    'web unit (TZ=UTC': 'lesson #448: v0.43.0 CI failed in UTC',
    'desktop: typecheck + unit': '0.43.x shipped a desktop window that never parsed (#52)',
    'the Store package works as a Store copy': 'the GitHub runner never activates an MSIX; release.yml submits what it builds (2026-09-26)',
    'docker: full image': 'v0.43.0: npm published, images never built',
    'docker: slim image': 'v0.43.0',
    'both images report the release': 'the version is baked in',
    'shop window: a throwaway instance': 'CONTRIBUTING step 12, before the tag',
  },
  // #172: the heavy suites left the fast pretag, not the release. The profile
  // says where each runs (GitHub, here during the gate stage, or here first).
  heavy: {
    'go: echo.wasm fixture': 'without it the app-plugin tests skip in silence (lesson #960)',
    'go: vet + test': 'the backend suite',
    'the packages a patch changed and their importers': 'a patch tests what it changed (2026-09-25 rule, #172)',
    'migrations on sqlite, postgres AND mysql': 'the parity tests skip without a DSN',
    "web unit (this machine's clock)": 'CI runs the unit suite in UTC only',
    'e2e: Cypress': 'the suite release.yml waits for',
    'e2e: Playwright': 'the journeys Cypress does not walk',
  },
  exportGates: {
    'go build (public module path)': 'lesson #55 checklist: the rewritten module builds IN the export',
    'goreleaser check, with the GoReleaser CI uses': 'lesson #510',
    'workflow guards ran against the workflows that will run': 'lessons #455, #461, #510',
  },
  published: {
    'GitHub Release': '0.44.0/0.44.1 shipped without the desktop packages',
    'ghcr:': 'v0.43.0 had no images',
    'npm:': 'every package under packages/ (0.48 added filex-app-ui, which filex-core depends on)',
    'latest.yml offers the x64 and the arm64 installer': '0.48.1: Windows reads ONE feed whatever the CPU; x64 first',
    'are amd64 + arm64': '0.48.1: the arm64 images are smoke-tested before they are tagged',
    "were built from the tag's commit": "#174: a tag run promotes the images the dry run of its commit built (#181: or builds them when there is none); their --version names the tag's commit",
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

  // ⚠⚠ #76: the tag started the test, and 0.43.0, 0.43.1, 0.44.0, 0.44.1 and
  // 0.45.0 each spent a number on a red tag run. The tags come after GitHub
  // has tested the very commits they name.
  it('tags only after GitHub tested the export commit: export, land, gate, sign, push, ci', () => {
    const ids = STAGES.map((s: { id: string }) => s.id);
    const order = ['stamp', 'pretag', 'export', 'land', 'gate', 'sign', 'push', 'ci', 'deploy'].map((id) => ids.indexOf(id));
    expect(order.every((i) => i >= 0), `the stages are ${ids.join(', ')}`).toBe(true);
    expect([...order].sort((a, b) => a - b), `the stages are ${ids.join(', ')}`).toEqual(order);
  });

  it('the gate asks GitHub through the plan, starts the dry run CONTRIBUTING names, and stops waiting', () => {
    expect(p.github?.repo).toBe('BRF-Tech/filex');
    expect(typeof p.github.runs).toBe('function');
    expect(typeof p.github.dispatch).toBe('function');
    const cmd = p.github.command({ workflow: 'release.yml', ref: p.branch, inputs: { publish: 'false' } });
    expect(cmd).toBe('gh workflow run release.yml -R BRF-Tech/filex --ref main -f publish=false');
    expect(fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8'), 'the Release process names another command').toContain(cmd);
    expect(p.gateWait.pollMs).toBeGreaterThan(0);
    expect(p.gateWait.timeoutMs).toBeGreaterThan(p.gateWait.pollMs);
    expect(Number.isFinite(p.gateWait.timeoutMs)).toBe(true);
  });

  it('gate names are unique (each writes its own log, and keys its own cache entry)', () => {
    for (const stage of ['audit', 'docs', 'pretag', 'heavy', 'exportGates', 'published', 'deployed']) {
      const n = names(p[stage]);
      expect(new Set(n).size, stage).toBe(n.length);
    }
    // The heavy gates run in pretag (full) or beside the gate stage: one name, one log.
    expect(new Set(names(every)).size).toBe(every.length);
  });

  it('the browser suites wait for the binary they test, and come after it in a full run', () => {
    for (const g of heavy.filter((x) => x.name.startsWith('e2e:'))) {
      expect(g.after, `${g.name} does not wait for the binary`).toContain(BUILD_BIN);
    }
    const n = names(selectGates(p, 'full', null).pretag);
    const build = n.indexOf(BUILD_BIN);
    const e2e = n.findIndex((x) => x.startsWith('e2e:'));
    expect(build).toBeGreaterThanOrEqual(0);
    expect(e2e).toBeGreaterThan(build);
  });

  it('every `after` names a gate of the plan', () => {
    const all = new Set(names(every));
    for (const g of every) for (const a of g.after ?? []) expect(all.has(a), `${g.name} waits for "${a}", which no gate is called`).toBe(true);
  });

  // ⚠⚠ Every Go call goes through ONE WSL mirror of the module, and each
  // rsyncs it with --delete (scripts/lib/go-build.mjs): two at once rewrite
  // files under a running build. One lane runs them one after the other.
  it('everything that goes through the WSL mirror of the Go module is in one lane', () => {
    const wsl = every.filter((g) => g.name.startsWith('go:') || g.name === BUILD_BIN);
    expect(wsl.length).toBeGreaterThanOrEqual(5);
    for (const g of wsl) expect(g.lane, `${g.name} runs beside another Go gate`).toBe('go');
  });

  // ⚠ #172: 71-90 minutes of pretag found no product fault from 0.50 to
  // 0.52; the heavy suites ran there and again in the export, on GitHub and
  // on the build host. The fast pretag is the builds and what only this
  // machine can do.
  it('the minor pretag is the fast one: the builds, vue-tsc, the unit suites in UTC, the desktop, the Store copy, the images', () => {
    const minor = selectGates(p, 'minor', null);
    expect(names(minor.pretag)).toEqual(names(pretag));
    for (const n of names(minor.pretag)) {
      expect(n, `${n} is a heavy suite in the fast pretag`).not.toMatch(/^go: |^e2e: |migrations|this machine's clock/);
    }
  });

  // #173/#174: since ci.yml's full matrix every heavy suite has its parts on
  // GitHub - Playwright on three engines with and without a Document Server,
  // the unit suite on Istanbul's clock, the echo fixture in every Go part -
  // so a minor runs none of them here: the gate stage reads them part by part.
  it('a minor reads every heavy suite from GitHub, part by part, and runs none here at the gate stage', () => {
    const minor = selectGates(p, 'minor', null);
    expect(names(minor.github).sort()).toEqual(heavy.filter((g) => !g.patchOnly).map((g) => g.name).sort());
    for (const g of minor.github) expect(g.github).toBe('ci.yml');
    expect(minor.local).toEqual([]);
    expect(names(minor.skipped.map((s: { gate: Gate }) => s.gate))).toEqual([GO_PATCH]);
  });

  it('every heavy gate GitHub runs names its parts, and every part is a job of the full matrix', () => {
    const full = ciParts(REPO, 'full');
    for (const g of heavy.filter((x) => x.github)) {
      const parts = (g as Gate & { githubJobs?: (c: object) => string[] }).githubJobs?.({ repo: REPO }) ?? [];
      expect(parts.length, `${g.name} has no parts on GitHub`).toBeGreaterThan(0);
      for (const n of parts) expect(full, `${g.name}: "${n}" is no job of ci.yml's full matrix`).toContain(n);
    }
    const pw = gateParts(REPO).playwright;
    expect(pw.filter((n: string) => n.startsWith('Playwright (chromium ')).length, 'Playwright on Chromium in parts').toBeGreaterThan(1);
    expect(pw.some((n: string) => n.startsWith('Playwright + Document Server'))).toBe(true);
    expect(gateParts(REPO).webLocalClock).toEqual(['Frontend (pnpm, TZ=Europe/Istanbul)']);
  });

  it('the gate reads the whole matrix, and asks the dry run for what the tag run promotes', () => {
    expect(p.githubMatrix.workflow).toBe('ci.yml');
    expect(p.githubMatrix.parts({ repo: REPO })).toEqual(ciParts(REPO, 'full'));
    expect(p.githubMatrix.complete).toBe('All tests (full)');
    expect(p.githubMatrix.parts({ repo: REPO }).at(-1)).toBe(p.githubMatrix.complete);
    expect(typeof p.github.jobs).toBe('function');
    expect(typeof p.github.artifacts).toBe('function');
    // Both images by digest, and every desktop row's files; macOS alone may
    // be missing (a dry run started with -f macos=false).
    expect(p.promotion.required).toEqual(expect.arrayContaining(['digests-amd64', 'digests-arm64', 'release-files-windows', 'release-files-linux', 'release-files-linux-arm64', 'release-files-store']));
    expect(p.promotion.optional).toEqual(['release-files-macos']);
    expect([...p.promotion.required, ...p.promotion.optional].filter((n: string) => n.startsWith('release-files-'))).toHaveLength(5);
  });

  // #181: GitHub Actions down, the gate reads CircleCI (--gate circleci).
  // What CircleCI runs - the Go suite beside both engines, Playwright in
  // Chromium, the echo fixture they need - is read from it; what it does not
  // run (Cypress, this machine's clock) runs here at the gate stage.
  it('with the gate on CircleCI, a minor reads Go, the engines and Playwright from it, and runs Cypress and this machine\'s clock here', () => {
    const minor = selectGates(p, 'minor', null, { source: 'circleci' });
    expect(names(minor.remote).sort()).toEqual(['e2e: Playwright (whole)', 'go: echo.wasm fixture', MIGRATIONS, 'go: vet + test'].sort());
    for (const g of minor.remote as Array<Gate & { circleci?: string }>) expect(g.circleci, g.name).toBeTruthy();
    const local = names(minor.local);
    expect(local).toContain('e2e: Cypress (whole)');
    expect(local).toContain("web unit (this machine's clock)");
    expect(local).toContain(BUILD_BIN);
    expect(local).toContain(BUILD_UI);
    expect(local).not.toContain('e2e: Playwright (whole)');
    expect(local).not.toContain('go: vet + test');
    expect(local).not.toContain(MIGRATIONS);
    // GitHub stays the default, and `github` is the same list as `remote`
    const byDefault = selectGates(p, 'minor', null);
    expect(names(byDefault.remote)).toEqual(names(byDefault.github));
    expect(names(byDefault.remote)).toContain('e2e: Cypress (whole)');
    expect(() => selectGates(p, 'minor', null, { source: 'gitlab' })).toThrow('gate source gitlab');
  });

  it('with the gate on CircleCI, a patch runs here what GitHub would have run for it and CircleCI does not', () => {
    const patch = selectGates(p, 'patch', ['e2e/tests/40-share.spec.ts', 'CHANGELOG.md', 'web/package.json'], { source: 'circleci' });
    expect(names(patch.remote)).toContain('go: vet + test');
    expect(names(patch.remote)).toContain(MIGRATIONS);
    expect(names(patch.local)).toContain('e2e: Cypress (whole)');
    expect(names(patch.skipped.map((s: { gate: Gate }) => s.gate))).not.toContain('e2e: Cypress (whole)');
    expect(names(patch.pretag)).not.toContain('e2e: Cypress (whole)');
  });

  it('the full profile runs every heavy suite here, before the export, as every release did up to 0.52', () => {
    const full = selectGates(p, 'full', null);
    expect(full.github).toEqual([]);
    expect(full.local).toEqual([]);
    for (const g of heavy) if (!g.patchOnly) expect(names(full.pretag), g.name).toContain(g.name);
    expect(names(full.pretag)).not.toContain(GO_PATCH);
  });

  // #76 (2026-09-25): "a patch is a patch" - but the two ghost releases died
  // building and packaging, not testing, so those run in every profile.
  it('a patch builds and packages whatever it changed, and nothing else when nothing changed', () => {
    const patch = selectGates(p, 'patch', []);
    const ran = names(patch.pretag);
    for (const g of pretag.filter((x) => x.always)) expect(ran, g.name).toContain(g.name);
    for (const must of [BUILD_UI, BUILD_BIN, 'the binary serves the UI just built, byte for byte', 'docker: full image (docker/Dockerfile, amd64)', 'docker: slim image (docker/Dockerfile.slim, amd64)', 'desktop: the Store package works as a Store copy', 'desktop: typecheck + unit']) {
      expect(ran, `a patch skips ${must}`).toContain(must);
    }
    for (const g of heavy) expect(ran, `${g.name} runs in a patch that changed nothing`).not.toContain(g.name);
  });

  it('a patch that touched only e2e/ runs neither the Go suite nor the migrations here, and leaves them to GitHub', () => {
    const patch = selectGates(p, 'patch', ['e2e/tests/40-share.spec.ts', 'CHANGELOG.md', 'web/package.json']);
    const ran = names(patch.pretag);
    expect(ran).not.toContain(GO_PATCH);
    expect(ran).not.toContain(MIGRATIONS);
    expect(ran).not.toContain('go: vet + test');
    expect(names(patch.github)).toContain('go: vet + test');
    expect(names(patch.github)).toContain(MIGRATIONS);
    expect(ran).not.toContain('e2e: Playwright (whole)');
  });

  it('a patch that touched a Go package runs the Go suite on what it changed; a schema change adds the three engines', () => {
    const code = names(selectGates(p, 'patch', ['backend/internal/perm/upgrade.go']).pretag);
    expect(code).toContain(GO_PATCH);
    expect(code).not.toContain('go: vet + test');
    expect(code).not.toContain(MIGRATIONS);
    const schema = names(selectGates(p, 'patch', ['backend/db/migrations/sqlite/00087_x.sql']).pretag);
    expect(schema).toContain(MIGRATIONS);
    expect(schema).toContain(GO_PATCH);
  });

  it("a patch's Go gate names the packages it changed, and the whole module for a change it cannot place", () => {
    const g = gate(GO_PATCH);
    expect(g.script).toContain('scripts/release/gates/go-targeted.sh');
    const sh = (changed: string[]) => {
      const argv = g.cmd!({ repo: REPO, bash: 'bash', changed }, 'native');
      return argv[argv.length - 1];
    };
    expect(sh(['backend/internal/perm/upgrade.go', 'e2e/x.spec.ts'])).toContain(`FILEX_GO_DIRS=${shq('./internal/perm')}`);
    expect(sh(['backend/go.mod'])).toContain(`FILEX_GO_DIRS=${shq('./...')}`);
    const wsl = g.cmd!({ repo: REPO, bash: 'bash', changed: ['backend/internal/perm/upgrade.go'] }, 'wsl');
    expect(wsl[wsl.length - 1], 'under WSL the variable must be written into the command').toContain(`FILEX_GO_DIRS=${shq('./internal/perm')}`);
  });

  // ⚠ The cache may cost a run, never pass one: a gate that reads a file its
  // `inputs` miss would pass from the cache after that file changed. So a
  // gate is cached only with inputs, and builds and images never are - they
  // make what the later gates use.
  it('builds and images are never cached; every other test gate names its inputs', () => {
    for (const g of every) {
      if (/^(build|docker): /.test(g.name) || g.name === 'go: echo.wasm fixture') expect(g.inputs, `${g.name} would pass from the cache`).toBeUndefined();
    }
    for (const n of ['go: vet + test', GO_PATCH, MIGRATIONS, 'web unit (TZ=UTC, like CI)', "web unit (this machine's clock)", 'e2e: Cypress (whole)', 'e2e: Playwright (whole)']) {
      expect(gate(n).inputs, `${n} has no inputs and runs every time`).toBeTruthy();
    }
  });

  // The kabul of #172: a fix that touches only e2e/ finds the Go suite and
  // the migrations in the cache; anything in backend/ runs them again.
  it('a fix in e2e/ leaves the Go and migration keys alone; a change in backend/ moves them', () => {
    const NUL = String.fromCharCode(0);
    const tree = (rows: Array<[string, string]>) => parseLsTree(rows.map(([id, p]) => `100644 blob ${id}\t${p}`).join(NUL) + NUL);
    const before = tree([['a1', 'backend/internal/perm/upgrade.go'], ['b1', 'e2e/tests/40-share.spec.ts'], ['c1', 'backend/db/migrations/sqlite/00001_init.sql'], ['d1', 'docs/STORAGE.md']]);
    const e2eFix = tree([['a1', 'backend/internal/perm/upgrade.go'], ['b2', 'e2e/tests/40-share.spec.ts'], ['c1', 'backend/db/migrations/sqlite/00001_init.sql'], ['d1', 'docs/STORAGE.md']]);
    const goFix = tree([['a2', 'backend/internal/perm/upgrade.go'], ['b1', 'e2e/tests/40-share.spec.ts'], ['c1', 'backend/db/migrations/sqlite/00001_init.sql'], ['d1', 'docs/STORAGE.md']]);
    const docsFix = tree([['a1', 'backend/internal/perm/upgrade.go'], ['b1', 'e2e/tests/40-share.spec.ts'], ['c1', 'backend/db/migrations/sqlite/00001_init.sql'], ['d2', 'docs/STORAGE.md']]);
    const key = (name: string, entries: ReturnType<typeof parseLsTree>) =>
      gateCacheKey({ name, recipe: 'same', files: inputEntries(entries, gate(name).inputs!), facts: {} });
    for (const n of ['go: vet + test', MIGRATIONS]) {
      expect(key(n, e2eFix), `${n} runs again after a fix in e2e/`).toBe(key(n, before));
      expect(key(n, goFix), `${n} passes from the cache after a change in backend/`).not.toBe(key(n, before));
    }
    expect(key('e2e: Playwright (whole)', e2eFix)).not.toBe(key('e2e: Playwright (whole)', before));
    expect(key('e2e: Playwright (whole)', docsFix), 'a docs-only change re-runs the browser suite').toBe(key('e2e: Playwright (whole)', before));
    expect(matchesInputs(gate('web unit (TZ=UTC, like CI)').inputs!, 'docs/STORAGE.md'), 'the unit suite reads the docs').toBe(true);
  });

  it('the chain profiles a minor or a patch accepts are profiles scripts/chain has', () => {
    const known = ['full', 'targeted', 'nightly'];
    for (const [profile, accepted] of Object.entries(p.chainProfiles as Record<string, string[]>)) {
      for (const a of accepted) expect(known, `${profile} accepts a chain profile "${a}"`).toContain(a);
    }
    const chainPlan = path.join(REPO, 'scripts', 'chain', 'plan.mjs');
    if (fs.existsSync(chainPlan)) {
      const m = /export const PROFILES = \[([^\]]*)\]/.exec(fs.readFileSync(chainPlan, 'utf8'));
      expect(m, 'scripts/chain/plan.mjs no longer exports PROFILES').toBeTruthy();
      const theirs = [...m![1]!.matchAll(/'([^']+)'/g)].map((x) => x[1]);
      for (const accepted of Object.values(p.chainProfiles as Record<string, string[]>)) for (const a of accepted) expect(theirs).toContain(a);
    }
  });

  // ⚠⚠ #172: the export ran the Go and web suites, a fourth and a fifth time
  // per release (19 minutes at 0.51), and GitHub runs both again on the very
  // commit it lands - before any tag, so a red there spends no number (#76).
  // What stays is what nothing after the land catches, or catches only once
  // it is public.
  it('the export runs no test suite: it builds, scans and guards the workflows', () => {
    for (const g of p.exportGates as Gate[]) {
      const text = `${g.script ?? ''} ${typeof g.sh === 'string' ? g.sh : ''}`;
      expect(text, g.name).not.toMatch(/go (test|vet)|pnpm[^&]*\btest\b|vitest/);
      if (g.vitest) expect(g.vitest.files.length, `${g.name} runs the whole web suite`).toBeGreaterThan(0);
    }
    const builtin = STAGES.find((s: { id: string }) => s.id === 'export').builtin.join('\n');
    for (const guard of [/no private host/, /no deletion without a reason/, /executable bit/, /workflows untouched/, /export-public\.sh/]) {
      expect(builtin, `the export stage lost a built-in guard: ${guard}`).toMatch(guard);
    }
  });

  it('names the workflow guards by titles that exist, so a rename cannot make the gate vacuous', () => {
    const guards = p.exportGates.find((g: { vitest?: unknown }) => g.vitest).vitest.mustPass as string[];
    const sources = ['releaseGatesImages.test.ts', 'goreleaserTemplates.test.ts', 'wingetCla.test.ts', 'msstoreSubmit.test.ts', 'releaseArm64.test.ts', 'releaseMacosOnly.test.ts', 'releaseSnapArm64Only.test.ts', 'releaseNpmTrusted.test.ts', 'ciFullMatrix.test.ts', 'releasePromote.test.ts', 'releaseVerifyCircleci.test.ts', 'releaseStoresOnly.test.ts'].map((f) =>
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
  it('builds the wasm fixture in the Go test gates themselves, before go test', () => {
    for (const name of ['go: vet + test', GO_PATCH]) {
      const g = gate(name) as { script: string };
      expect(g, `the plan has no "${name}" gate`).toBeTruthy();
      const fixture = g.script.indexOf('build-wasm-fixture.sh');
      expect(fixture, g.script).toBeGreaterThanOrEqual(0);
      const test = Math.max(g.script.indexOf('go test'), g.script.indexOf('go-targeted.sh'));
      expect(test, g.script).toBeGreaterThan(fixture);
    }
  });

  // ⚠⚠ v0.50.0 pretag (lesson #960, #139): the fixture gate ran in a WSL
  // mirror of the whole repository and refreshed only the mirror's echo.wasm.
  // The Windows checkout's copy, the one the Playwright gate installs,
  // predated the echo change spec 192 tested; 192 failed after an hour of
  // chain. The gate builds in the Go module (under WSL its mirror on WSL's own
  // disk, never Go on /mnt) with the CHECKOUT's script, which writes the
  // module back (the next test runs that script).
  it('the echo.wasm gate builds in the Go module and refreshes the checkout before Playwright installs from it', () => {
    type GoGate = { name: string; script: string; after?: string[]; cmd: (c: object, toolchain?: string) => string[] };
    const gates = heavy as GoGate[];
    const i = gates.findIndex((g) => g.name === 'go: echo.wasm fixture');
    expect(i, 'the plan has no "go: echo.wasm fixture" gate').toBeGreaterThanOrEqual(0);
    const gate = gates[i];
    const builds = /FILEX_BACKEND_DIR="\$PWD" bash "\$FILEX_CHECKOUT\/scripts\/build-wasm-fixture\.sh"/;
    expect(gate.script, "the gate does not run the checkout's script in the module it is in, so the checkout keeps its old echo.wasm").toMatch(builds);
    const pw = gates.find((g) => g.name.startsWith('e2e: Playwright'))!;
    expect(i, 'the fixture is built after the Playwright gate that installs it').toBeLessThan(gates.indexOf(pw));
    // Side by side (#172), the order of the list is not enough: Playwright waits for it.
    expect(pw.after, 'Playwright can start before the fixture is built').toContain('go: echo.wasm fixture');

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

// 0.53.0: the tag run had no Store job (GitHub never created its desktop
// jobs), and only=stores sent the bundle the dry run kept: it is promoted,
// not bundled there, and a promoted bundle is as good as a built one.
describe("the Microsoft Store check reads the run's Store job", () => {
  const steps = (s: Record<string, string>) => Object.entries(s).map(([name, conclusion]) => ({ name, conclusion }));
  const KEPT = 'They are the files the dry run kept, for this version';
  it('takes a bundle built in the job, or the one the dry run kept, once submitted without a warning', () => {
    expect(storeJobProblems(steps({ 'Bundle the Store packages': 'success', 'Submit to the Microsoft Store': 'success' }))).toEqual([]);
    expect(storeJobProblems(steps({ 'Bundle the Store packages': 'skipped', [KEPT]: 'success', 'Submit to the Microsoft Store': 'success' }))).toEqual([]);
    expect(storeJobProblems(steps({ 'Bundle the Store packages': 'skipped', [KEPT]: 'skipped', 'Submit to the Microsoft Store': 'success' })).join()).toContain('"Bundle the Store packages": skipped');
    expect(storeJobProblems(steps({ [KEPT]: 'success', 'Submit to the Microsoft Store': 'skipped' })).join()).toContain('"Submit to the Microsoft Store": skipped');
    expect(storeJobProblems(steps({ [KEPT]: 'success', 'Submit to the Microsoft Store': 'success' }), 'submission 9 is still Certification').join()).toContain('the run warned: submission 9 is still Certification');
    expect(storeJobProblems([]).length).toBe(2);
  });
});
