// A server patch on a lane of its own (task #252): which files reach which
// surface, when a patch is a server patch, what its pretag, gate, tags, tag
// run, read-back and ship do and leave out. #361: it waits for what every
// release waits for - GitHub's full matrix and the dry run - before its tag.
//
// ⚠ Why (the owner, 2026-10-12): a one-line server fix went out as a whole
// release - two to two and a half hours, every unaffected surface given a new
// version of the same bytes. "If the CLI or the UI did not change, it is not
// updated." What these tests hold is what keeps the lane narrow and honest:
// in doubt every surface is published, a migration is never a server patch,
// and nothing it publishes skips a test any other release waits for.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { profileCovers, selectGates } from '../../../scripts/release/checks.mjs';
import plan, { releaseAssets } from '../../../scripts/release/plan.mjs';
import { auditAsks, ofThisRelease } from '../../../scripts/release/stages.mjs';
import {
  ALL_SURFACES,
  CLI_PACKAGES,
  SERVER_COMMANDS,
  SURFACES_LINE,
  affectedSurfaces,
  publishes,
  releaseSurfaces,
  serverPatch,
  surfacesOfTagMessage,
} from '../../../scripts/release/surfaces.mjs';
import { buildSteps, personLines, shipSettings } from '../../../scripts/train/ship.mjs';
import { DIR, TAG_SHA, bash, code, job, jobIf, stepNamed, stepsOf, topLevelTerms, verify } from '../helpers/releaseWorkflow';

const REPO = path.resolve(__dirname, '..', '..', '..');
const read = (rel: string) => fs.readFileSync(path.join(REPO, rel), 'utf8');
const SERVER = ['server', 'docs'];
const hasJq = !spawnSync('jq', ['--version'], { encoding: 'utf8' }).error;
type Gate = { name: string; surface?: string; serverSkip?: string; github?: string };

describe('which surfaces a change reaches', () => {
  it('places the server, the interface, the desktop app, the CLI, the docs and what publishes nothing', () => {
    const s = (files: string[]) => affectedSurfaces(files).surfaces;
    expect(s(['backend/internal/search/index.go'])).toEqual(['server']);
    expect(s(['backend/internal/thumb/source_file.go', 'docs/SEARCH.md'])).toEqual(['server', 'docs']);
    expect(s(['docker/Dockerfile', 'deploy/helm/filex/values.yaml'])).toEqual(['server']);
    // The interface is embedded in the server, synced into the desktop app, published to npm.
    expect(s(['web/src/views/Login.vue'])).toEqual(['server', 'ui', 'desktop']);
    expect(s(['packages/core/src/index.ts'])).toEqual(['server', 'ui', 'desktop']);
    expect(s(['desktop/src/main.ts'])).toEqual(['desktop']);
    // Its README goes into no package; its Store listing is the Store's text.
    expect(s(['desktop/README.md'])).toEqual(['docs']);
    expect(s(['desktop/store/microsoft/listing.md'])).toEqual(['desktop']);
    expect(serverPatch(affectedSurfaces(['backend/internal/search/index.go', 'desktop/README.md'])).ok, 'a server fix with a line of the desktop README').toBe(true);
    // The CLI's own code and the module's dependencies: the CLI and the desktop app that carries it.
    expect(s(['backend/internal/filesync/sync.go'])).toEqual(['server', 'cli', 'desktop']);
    expect(s(['backend/cmd/filex/sync.go'])).toEqual(['server', 'cli', 'desktop']);
    expect(s(['backend/go.mod'])).toEqual(['server', 'cli', 'desktop']);
    // Tests, fixtures, tooling and notes publish nothing on their own.
    expect(s(['backend/internal/search/index_test.go', 'web/tests/x.test.ts', 'e2e/tests/40-share.spec.ts', 'scripts/release/plan.mjs', 'CHANGELOG.md', 'backend/internal/wasmplugin/testdata/echo/main.go'])).toEqual([]);
  });

  it('in doubt, everything: a file no rule places makes a release of every surface', () => {
    const a = affectedSurfaces(['backend/internal/search/index.go', 'pnpm-lock.yaml', 'Makefile']);
    expect(a.unmatched).toEqual(['pnpm-lock.yaml', 'Makefile']);
    expect(a.surfaces).toEqual(ALL_SURFACES);
  });

  it('knows a migration', () => {
    expect(affectedSurfaces(['backend/db/migrations/sqlite/00114_x.sql']).migrations).toBe(true);
    expect(affectedSurfaces(['backend/internal/db/store.go']).migrations).toBe(false);
  });

  it("counts as the CLI every package the CLI commands reach, directly or not (every file of backend/cmd/filex but the server's own commands)", () => {
    const backend = path.join(REPO, 'backend');
    const mod = /^module\s+(\S+)/m.exec(fs.readFileSync(path.join(backend, 'go.mod'), 'utf8'))![1];
    const esc = mod.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&');
    const importRe = new RegExp(`"${esc}/internal/([A-Za-z0-9_/]+)"`, 'g');
    // The non-test Go files of one package directory.
    const sources = (dir: string, keep = (_n: string) => true) =>
      fs.existsSync(dir) ? fs.readdirSync(dir).filter((n) => n.endsWith('.go') && !n.endsWith('_test.go') && keep(n)).map((n) => fs.readFileSync(path.join(dir, n), 'utf8')) : [];
    const importsOf = (texts: string[]) => new Set(texts.flatMap((t) => [...t.matchAll(importRe)].map((m) => m[1])));
    const reached = new Set<string>();
    const todo = [...importsOf(sources(path.join(backend, 'cmd', 'filex'), (n) => !SERVER_COMMANDS.includes(n)))];
    while (todo.length) {
      const p = todo.pop()!;
      if (reached.has(p)) continue;
      reached.add(p);
      for (const q of importsOf(sources(path.join(backend, 'internal', ...p.split('/'))))) if (!reached.has(q)) todo.push(q);
    }
    expect([...reached].sort()).toEqual([...CLI_PACKAGES].sort());
    for (const p of CLI_PACKAGES) expect(fs.existsSync(path.join(backend, 'internal', ...p.split('/'))), p).toBe(true);
    // ...and the server keeps a lane: its handlers, search and previews are not the CLI's.
    for (const p of ['api', 'search', 'thumb', 'onlyoffice', 'share', 'dav']) expect(CLI_PACKAGES, p).not.toContain(p);
    expect(affectedSurfaces(['backend/internal/syspath/x.go']).surfaces, 'a package sync stands on').toEqual(['server', 'cli', 'desktop']);
  });

  it("names every server command that is one: a file there, reaching something the CLI's commands do not", () => {
    // ⚠ Owner's decision (0.57): `filex settings reseed` and the office
    // engine's process run on the server's own database and process, not as
    // a person's client command, so what only they reach is the server's. A
    // name here that is no file, or a file that reaches nothing beyond the
    // CLI's packages, is a CLI command filed as the server's: red, so the
    // list cannot quietly hide one. (A NEW file of cmd/filex that is not
    // listed counts as the CLI's; when it reaches a server package the
    // closure above goes red until it is filed one way or the other.)
    const backend = path.join(REPO, 'backend');
    const mod = /^module\s+(\S+)/m.exec(fs.readFileSync(path.join(backend, 'go.mod'), 'utf8'))![1];
    const esc = mod.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&');
    const importRe = new RegExp(`"${esc}/internal/([A-Za-z0-9_/]+)"`, 'g');
    for (const name of SERVER_COMMANDS) {
      const file = path.join(backend, 'cmd', 'filex', name);
      expect(fs.existsSync(file), name).toBe(true);
      const reached = [...fs.readFileSync(file, 'utf8').matchAll(importRe)].map((m) => m[1]);
      expect(reached.filter((p) => !CLI_PACKAGES.includes(p)), `${name} reaches only the CLI's packages: it is a CLI command`).not.toEqual([]);
    }
    // A package a server command shares with the CLI's commands is the
    // CLI's: a change to it still reaches the CLI (the closure is the CLI
    // commands' own imports, whatever else imports them).
    expect(affectedSurfaces(['backend/internal/leafname/leafname.go']).surfaces).toEqual(['server', 'cli', 'desktop']);
    expect(affectedSurfaces(['backend/internal/server/office_backends.go']).surfaces).toEqual(['server']);
    expect(affectedSurfaces(['backend/cmd/filex/settings.go']).surfaces, 'the file itself is in the one binary').toEqual(['server', 'cli', 'desktop']);
  });
});

describe('a server patch', () => {
  it('asks the audit by what changed: no interface file, no README or screenshots - with that reason (#252)', () => {
    const base = { version: '1.2.4', prev: 'v1.2.3', commits: '2', docSurfaces: ['README.md', 'docs/SEARCH.md', 'docs/UPDATES.md'] };
    const server = auditAsks({ ...base, touched: ['backend/internal/search/index.go', 'docs/SEARCH.md', 'CHANGELOG.md'] }).join('\n');
    expect(server).toMatch(/1\. README\.md: not asked - no interface file changed \(#252\)/);
    expect(server).toMatch(/2\. Screenshots: not asked - nothing they show changed/);
    expect(server).toMatch(/3\. The pages that describe what changed, and that they are still true: docs\/SEARCH\.md\n/);
    expect(server).not.toMatch(/SHOTS_RELEASE/);
    expect(server).toMatch(/The CHANGELOG entry and the Releases-page summary \(docs-site\/data\/release-highlights\.json\) are asked for as always/);
    expect(server).toMatch(/pnpm release 1\.2\.4 --resume --ack audit$/);
    // The README itself changed: it is read.
    expect(auditAsks({ ...base, touched: ['README.md', 'backend/internal/search/index.go'] }).join('\n')).toMatch(/1\. README\.md: it changed in this range/);
    // The interface, the desktop app, the CLI the desktop app carries, a minor
    // step and a file no rule places: all three checks, as before.
    const all: [string, string[]][] = [
      ['1.2.4', ['web/src/views/Login.vue']],
      ['1.2.4', ['desktop/src/main.ts']],
      ['1.2.4', ['backend/internal/filesync/sync.go']],
      ['1.3.0', ['backend/internal/search/index.go']],
      ['1.2.4', ['backend/internal/search/index.go', 'Makefile']],
    ];
    for (const [version, touched] of all) {
      const asks = auditAsks({ ...base, version, touched }).join('\n');
      expect(asks, touched.join(', ')).toMatch(/three things no script can[\s\S]*2\. Screenshots: bump SHOTS_RELEASE/);
    }
  });

  it('is a patch step that reaches the server, and the docs at most, with no migration and every file placed', () => {
    expect(serverPatch(affectedSurfaces(['backend/internal/search/index.go']))).toEqual({ ok: true, why: 'only the server changed' });
    expect(serverPatch(affectedSurfaces(['backend/internal/search/index.go', 'docs/SEARCH.md'])).why).toBe('only the server and the documentation changed');
    expect(serverPatch(affectedSurfaces(['backend/internal/search/index.go']), { patchStep: false }).ok).toBe(false);
    expect(serverPatch(affectedSurfaces(['backend/db/migrations/sqlite/00114_x.sql'])).why).toMatch(/migration/);
    expect(serverPatch(affectedSurfaces(['backend/internal/search/index.go', 'web/src/App.vue'])).why).toMatch(/^it reaches ui, desktop too: web\/src\/App\.vue/);
    expect(serverPatch(affectedSurfaces(['backend/internal/update/check.go'])).why).toMatch(/reaches cli, desktop too/);
    expect(serverPatch(affectedSurfaces(['backend/internal/search/index.go', 'Makefile'])).why).toMatch(/no rule places/);
    expect(serverPatch(affectedSurfaces(['docs/SEARCH.md'])).why).toBe('nothing of the server changed');
  });

  // #361 (the owner, 2026-11-18): the build host builds, lints and looks
  // into what GitHub built. Until then a server patch ran the changed Go
  // packages under -race here, and 0.61.2 went red on that six rounds in a
  // row, each time on another timing test.
  it('runs the light server gates here - builds, go vet, the embedded UI - and reads every suite from GitHub', () => {
    for (const platform of ['win32', 'linux']) {
      const p = plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9', platform });
      const sel = selectGates(p, 'server', ['backend/internal/search/index.go']);
      expect(sel.pretag.map((g: Gate) => g.name), platform).toEqual(['build: packages, admin (vue-tsc + vite), embed', 'build: server binary', 'go: vet', 'the binary serves the UI just built, byte for byte']);
      for (const left of ['desktop: typecheck', 'desktop: the Store package works as a Store copy']) {
        expect(sel.skipped.find((x: { gate: Gate }) => x.gate.name === left)?.why, `${platform}: ${left}`).toBeTruthy();
      }
      // Nothing deferred to a Windows machine, every heavy suite read from GitHub, none run here.
      expect(sel.pretag.some((g: { deferred?: unknown }) => g.deferred)).toBe(false);
      expect(sel.remote.map((g: Gate) => g.name).sort(), platform).toEqual(p.heavy.map((g: Gate) => g.name).sort());
      expect(sel.local).toEqual([]);
      // A docs-only server patch reads the same.
      expect(selectGates(p, 'server', ['docs/SEARCH.md']).remote).toHaveLength(p.heavy.length);
    }
  });

  it('a server pretag answers for a server patch alone; any other pretag answers for one', () => {
    expect(profileCovers('server', 'server')).toBe(true);
    for (const was of ['patch', 'minor', 'full']) expect(profileCovers(was, 'server'), was).toBe(true);
    for (const want of ['patch', 'minor', 'full']) expect(profileCovers('server', want), want).toBe(false);
    expect(profileCovers('minor', 'patch')).toBe(true);
    expect(profileCovers('patch', 'minor')).toBe(false);
  });

  it('waits at the gate for what every release waits for, and its tags say so', () => {
    const p = plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9' });
    expect(p.serverGate, 'a lane of its own at the gate (#361: gone)').toBeUndefined();
    expect(p.matrixWatch).toBeUndefined();
    expect(fs.existsSync(path.join(REPO, 'scripts', 'release', 'matrix-watch.mjs')), 'the matrix behind a server patch (#361: gone)').toBe(false);
    expect(SURFACES_LINE).toBe('surfaces: server');
    expect(surfacesOfTagMessage('v1.2.3\n\nsurfaces: server\n-----BEGIN PGP SIGNATURE-----\n')).toBe('surfaces: server');
    expect(surfacesOfTagMessage('v1.2.3\n')).toBe('');
    const stages = read('scripts/release/stages.mjs');
    expect(stages).not.toMatch(/gateServer/);
    expect(stages).not.toMatch(/if \(R\.profile === 'server'\) return/);
    expect(stages).toMatch(/const message = tagMessages\(tag, \{ server \}\)\.map\(\(m\) => `-m "\$\{m\}"`\)\.join\(' '\);/);
    expect(stages).toMatch(/if \(server && line !== SURFACES_LINE\) return \{ ok: false/);
    expect(stages).toMatch(/if \(!server && line\) return \{ ok: false/);
  });

  it('reads back what it publishes and names the rest as not this release', () => {
    const p = plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9' });
    const log = console.log;
    console.log = () => {};
    try {
      const R = { state: { surfaces: SERVER }, tag: 'v9.9.9' };
      const ci = ofThisRelease(R, p.published).map((g: Gate) => g.name);
      expect(ci.some((n: string) => n.startsWith('GitHub Release'))).toBe(true);
      expect(ci.filter((n: string) => n.startsWith('ghcr:'))).toHaveLength(3);
      for (const gone of ['Snap Store:', 'winget:', 'Microsoft Store:', 'npm:', 'GitHub Release v9.9.9: latest.yml']) expect(ci.some((n: string) => n.startsWith(gone)), gone).toBe(false);
      const deployed = ofThisRelease(R, p.deployed).map((g: Gate) => g.name);
      expect(deployed.some((n: string) => n.startsWith('fm.example.com runs'))).toBe(true);
      expect(deployed.some((n: string) => n.startsWith('filex.sh/updates/stable.json'))).toBe(true);
      expect(deployed.some((n: string) => n.startsWith('desktop feeds'))).toBe(false);
      // A release of every surface reads everything back, as before.
      expect(ofThisRelease({ state: {}, tag: 'v9.9.9' }, p.published)).toHaveLength(p.published.length);
    } finally {
      console.log = log;
    }
    // Every gate that names a surface names one there is.
    for (const g of [...p.published, ...p.deployed] as Gate[]) if (g.surface) expect(ALL_SURFACES, g.name).toContain(g.surface);
    // Its Release carries the archives and the CLI binaries, no desktop file.
    const server = releaseAssets('9.9.9', SERVER);
    expect(server).toContain('filex_9.9.9_linux_x86_64.tar.gz');
    expect(server).toContain('filex-linux-amd64');
    expect(server.some((a: string) => a.startsWith('filex-desktop-') || a.startsWith('latest'))).toBe(false);
    expect(new Set(releaseAssets('9.9.9'))).toEqual(new Set(releaseAssets('9.9.9', ALL_SURFACES)));
    expect(releaseSurfaces(null)).toEqual(ALL_SURFACES);
    expect(releaseSurfaces({ surfaces: SERVER })).toEqual(SERVER);
    expect(publishes(SERVER, undefined)).toBe(true);
    expect(publishes(SERVER, 'desktop')).toBe(false);
  });

  it('ships the server: the manifest as every release, the desktop feed, npm and the embeds left alone', () => {
    const env = {
      SHIP_HOST: 'app-server',
      SHIP_INSTANCES: 'demo',
      SHIP_DEMO_DIR: '/srv/filex-demo',
      SHIP_DEMO_PORT: '5212',
      SHIP_MANIFEST_CMD: 'bash /x/publish-manifest.sh',
      SHIP_DESKTOP_CMD: 'bash /x/publish-desktop.sh',
      SHIP_PURGE_CMD: 'bash /srv/purge-urls.sh',
      SHIP_FEED_URL: 'https://downloads.example.com/desktop/',
      SHIP_REVENDOR_CMD: 'bash /x/revendor.sh --apply --force',
    };
    const { cfg } = shipSettings(env);
    const runDir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-surfaces-ship-'));
    try {
      const ctx = { repo: REPO, tag: 'v0.57.1', version: '0.57.1', runDir, stamp: 's', want: 0, exp: '/x', preflight: () => ({ ok: true }), releases: () => ({ ok: true }), npm: () => ({ ok: true }), helm: () => ({ ok: true }) };
      type Step = { id: string; skipped?: string; gate?: { sh?: string } };
      const server = buildSteps(cfg, { ...ctx, surfaces: SERVER, profile: 'server' }) as Step[];
      // #361: the full matrix passed before the tag; no auto_ok held off behind it.
      expect(server.find((s) => s.id === 'manifest')!.gate!.sh).toBe('bash /x/publish-manifest.sh');
      for (const id of ['desktop', 'purge', 'npm', 'embeds']) {
        const s = server.find((x) => x.id === id)!;
        expect(s.skipped, id).toMatch(/^v0\.57\.1 publishes server, docs, not (desktop|ui): left as it is$/);
        expect(s.gate, id).toBeUndefined();
      }
      for (const id of ['backup', 'proxies', 'deploy', 'releases', 'verify']) expect(server.find((x) => x.id === id)!.gate, id).toBeTruthy();
      // A release of every surface ships as before.
      const all = buildSteps(cfg, ctx) as Step[];
      expect(all.find((s) => s.id === 'manifest')!.gate!.sh).toBe('bash /x/publish-manifest.sh');
      expect(all.some((s) => s.skipped)).toBe(false);
    } finally {
      fs.rmSync(runDir, { recursive: true, force: true });
    }
    expect(personLines(cfg, { tag: 'v0.57.1', version: '0.57.1', release: { profile: 'server' } }).join('\n')).not.toContain('matrix-watch');
  });
});

describe("a server patch's tag run (#252)", () => {
  it.runIf(!!DIR)("a tag that says surfaces: server publishes the server's surface alone: plan reads it, npm and the desktop job do not run", () => {
    const release = code();
    const plan = job(release, 'plan');
    expect(plan).toMatch(/\n\s+surfaces: \$\{\{ steps\.p\.outputs\.surfaces \}\}\n/);
    expect(plan).toMatch(/obj=\$\(gh api "repos\/\$GITHUB_REPOSITORY\/git\/ref\/tags\/\$tag" --jq '\.object\.type \+ " " \+ \.object\.sha'\)/);
    expect(plan).toMatch(/"surfaces: server"\) surfaces=server ;;/);
    expect(plan).toMatch(/\*\) echo "::error::\$tag says/);
    expect(plan).toMatch(/echo "surfaces=\$surfaces"/);
    for (const name of ['npm', 'desktop']) {
      expect(topLevelTerms(jobIf(job(release, name))), name).toContain("needs.plan.outputs.surfaces != 'server'");
    }
    // The server's surface still goes out: binaries, the images, their check and tags.
    for (const name of ['binaries', 'docker', 'promote-check', 'docker-manifest', 'cli-smoke']) expect(jobIf(job(release, name)), name).not.toMatch(/surfaces/);
    // The AUR repackages the desktop job's .deb: no desktop job, no AUR (#145).
    expect(topLevelTerms(jobIf(job(release, 'aur')))).toContain("needs.desktop.result != 'skipped'");
  });

  it('the workflow patch is the documented one, on top of the nightly one', () => {
    const patch = read('packaging/ci/release-surfaces.patch');
    // Made against the workflows with patches 8-11 applied: the AUR job's
    // new condition is its context, not its change, and the nightly
    // workflow (patch 11) is not touched.
    expect(patch).toMatch(/^\+\s+surfaces: \$\{\{ steps\.p\.outputs\.surfaces \}\}$/m);
    expect(patch).not.toMatch(/^[-+]\s+- name: The AUR key$/m);
    expect(patch).not.toMatch(/^diff --git a\/\.github\/workflows\/nightly-desktop\.yml/m);
    expect(read('docs/CONTRIBUTING.md')).toMatch(/\n12\. `release-surfaces\.patch` \(#252, 0\.57\), on top of 11:/);
    // #361: verify's lane of its own for a server patch went again, later.
    const promote = read('packaging/ci/release-server-promote.patch');
    expect(promote).toMatch(/^diff --git a\/\.github\/workflows\/release\.yml/m);
    expect(promote).toMatch(/^-\s+if \[ "\$EVENT" = push \] && \[ "\$\{SURFACES:-all\}" = server \]; then$/m);
    expect(promote).not.toMatch(/^\+.*github-server/m);
    expect(read('docs/CONTRIBUTING.md')).toMatch(/\n20\. `release-server-promote\.patch` \(#361, 0\.61\.2\), on top of 19:/);
  });

  it.runIf(!!DIR)("a server patch's Release is not the latest, and carries no CLI package manager", () => {
    const binaries = job(code(), 'binaries');
    expect(binaries).toMatch(/HOMEBREW_TAP_DEPLOY_KEY: \$\{\{ needs\.plan\.outputs\.surfaces != 'server' && secrets\.HOMEBREW_TAP_DEPLOY_KEY \|\| '' \}\}/);
    expect(binaries).toMatch(/WINGET_TOKEN: \$\{\{ needs\.plan\.outputs\.surfaces != 'server' && secrets\.WINGET_TOKEN \|\| '' \}\}/);
    expect(binaries).toMatch(/FILEX_SURFACES: \$\{\{ needs\.plan\.outputs\.surfaces \}\}/);
    expect(stepNamed(binaries, 'Did the CLI reach winget and Homebrew?').cond).toBe("env.PUBLISH == 'true' && env.SURFACES != 'server'");
    const gr = read('.goreleaser.yml');
    expect(gr).toMatch(/^ {2}make_latest: '\{\{ if eq \(index \.Env "FILEX_SURFACES"\) "server" \}\}false\{\{ else \}\}true\{\{ end \}\}'$/m);
    expect(stepsOf(binaries).length).toBeGreaterThan(3);
  });

  it.runIf(!!DIR)("verify asks a server patch what it asks every release, and promotes the dry run's images", () => {
    const v = stepNamed(job(code(), 'verify'), 'The commit this run publishes passed the full matrix and a dry run of this workflow, or CircleCI').text;
    // #361: no lane of its own - the build part alone publishes nothing.
    expect(v).not.toMatch(/github-server|VERIFY_SERVER_WAIT_SECS|export PART="Go vet \+ build"/);
    expect(v).not.toMatch(/\[ "\$\{SURFACES:-all\}" = server \]; then\n\s+export PART/);
    // ...and a server patch is said so in the summary, after the evidence every release gives.
    expect(v.indexOf('echo "promote=true"')).toBeLessThan(v.indexOf("a server patch: the dry run's images are promoted"));
    // The images are promoted, never built in a tag run that has a dry run.
    expect(jobIf(job(code(), 'docker'))).toMatch(/needs\.verify\.outputs\.promote == 'false'/);
  });

  // jq reads the desktop rows the dry run kept, as on the runner.
  it.runIf(!!DIR && !!bash && hasJq)('a server tag run with the full matrix and the dry run passed promotes; one without either publishes nothing', () => {
    const rows = [{ label: 'linux' }];
    const env = { EVENT: 'push', SHA: TAG_SHA, ONLY: 'all', PUBLISH: 'true', TAG: 'v1.2.4', SURFACES: 'server', DESKTOP: JSON.stringify({ include: rows }) };
    const kept = ['digests-amd64', 'digests-arm64', 'release-files-linux'];
    const green = verify(env, { ci: true, dry: true, artifacts: kept });
    expect(green.code, green.out).toBe(0);
    expect(green.outputs).toMatchObject({ evidence: 'github', promote: 'true' });
    expect(green.summary).toContain("a server patch: the dry run's images are promoted");
    // The full matrix and the dry run, as for every release: neither alone.
    expect(green.calls.some((c) => c.includes('workflows/release.yml/runs'))).toBe(true);
    expect(green.calls.some((c) => c.includes('workflows/ci.yml/runs'))).toBe(true);
    for (const answers of [{ ci: true, artifacts: kept }, { dry: true, artifacts: kept }, { ci: true, dry: true, full: false, artifacts: kept }]) {
      const r = verify(env, answers);
      expect(r.code, JSON.stringify(answers)).not.toBe(0);
    }
    // #361: the build part alone no longer publishes one.
    expect(code()).not.toMatch(/github-server|VERIFY_SERVER_WAIT_SECS/);
  });
});
