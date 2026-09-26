// THIS repository's release gates — what `pnpm release X.Y.Z` runs at each
// stage. The order of the stages and the checks every release needs whatever
// its repository (clean tree, tags, signatures, the export's deletions) live
// in stages.mjs; this file is the list that grows.
//
// ⚠ Add a gate here, never as a loose command in a runbook. Every gate below
// is here because a release shipped without it — the incident is next to it.
// `web/tests/deploy/releasePlan.test.ts` fails if one of them disappears.
//
// ⚠ A gate is a command judged by its OWN exit code (engine.mjs writes each to
// its own log; nothing is piped). A command that can exit 0 having run nothing
// must say what it ran (`vitest.mustPass`, `expect`), or it proves nothing.

import fs from 'node:fs';
import path from 'node:path';

import { nativeGo, wslGo, wslMirrorCd } from '../lib/go-build.mjs';
import { docker, dockerArgv, run, shq, slash } from './engine.mjs';
import { desktopFeeds, docsSite, readmePictures, runningRelease, updateManifest } from './verify.mjs';

const IS_WIN = process.platform === 'win32';

/** Where Go runs: natively, or — on the maintainer's Windows box — in WSL. */
let toolchain = null;
function goToolchain() {
  if (toolchain === null) toolchain = nativeGo() ? 'native' : wslGo() ? 'wsl' : 'none';
  return toolchain;
}

/**
 * A Go gate in `dirOf(ctx)`. Under WSL, GOFLAGS=-buildvcs=false: a worktree's
 * `.git` file names a Windows path WSL's git cannot follow, and `go build`
 * stamps VCS info by default (lesson #450).
 */
function goGate(name, dirOf, script) {
  return {
    name,
    cmd: (c) => {
      const dir = dirOf(c);
      const t = goToolchain();
      if (t === 'native') return [c.bash, '-c', `cd ${shq(slash(dir))} && ${script}`];
      if (t === 'wsl') {
        return ['wsl', '-e', 'bash', '-lc', `${wslMirrorCd(dir)} && export PATH=/usr/local/go/bin:$PATH GOFLAGS=-buildvcs=false && ${script}`];
      }
      return ['node', '-e', 'console.error("no Go toolchain: go is not on PATH, and WSL has none either"); process.exit(1)'];
    },
  };
}

// The workflow guards read the PUBLIC workflows, which exist only in the
// export checkout; without them they SKIP and the file still reads green.
// These are the tests that must have RUN (lessons #455, #510).
const WORKFLOW_GUARDS = [
  'the release calls the gate WITHOUT skip_docker',
  "ci.yml's docker job cannot be switched off, and builds both images",
  'every publishing job waits for the gate',
  'the release hands every token variable to the goreleaser step',
  'every repository token is a single {{ .Env.NAME }}, as GoReleaser demands',
  // v0.45.1: a tolerated winget warning code still failed the desktop job.
  'ends with exit 0, because GitHub exits the step with $LASTEXITCODE',
];

export default function plan({ repo, version, tag }) {
  const exportDir = path.resolve(process.env.FILEX_EXPORT_DIR ?? path.join(repo, '..', 'filex-export'));
  const bin = `bin/filex-release-e2e${IS_WIN ? '.exe' : ''}`;
  const img = (kind) => `filex-release:${kind}-${version}`;
  const dockerBuild = (dockerfile, kind) => ({
    name: `docker: ${kind} image (${dockerfile}, amd64)`,
    env: { MSYS_NO_PATHCONV: '1', MSYS2_ARG_CONV_EXCL: '*' },
    cmd: (c) => {
      const [bin, args] = dockerArgv([
        'build', '--progress=plain', '--platform', 'linux/amd64',
        '--build-arg', `VERSION=${c.tag}`, '--build-arg', `COMMIT=${c.releaseCommit}`,
        '--build-arg', `DATE=${new Date().toISOString().replace(/\.\d+Z$/, 'Z')}`,
        '-f', dockerfile, '-t', img(kind), '.',
      ], c.bash);
      return [bin, ...args];
    },
  });
  const workflowsOf = (dir) => slash(path.join(dir, '.github', 'workflows'));

  return {
    exportDir,
    branch: 'main',
    remote: 'origin',
    exportRemote: 'origin',
    exportScript: 'scripts/export-public.sh',
    // The maintainer key (docs/CONTRIBUTING.md → Release process, step 7).
    signingKeys: ['EFA3B1262FD992800DBBB5E3A8FEBA97FF786513'],
    // The public tree may name this project's hosts only as these two
    // contact addresses, which the export keeps reachable on purpose.
    privateHosts: {
      allow: ['security@brf.sh', 'hello@brf.sh'],
      forbid: [/brf\.sh/, /gitlab\.com[/:]brftech/],
    },

    // Every text that describes the product (CONTRIBUTING step 3). The audit
    // prints which of them this release touched — a reminder, never a list
    // that could be "complete".
    docSurfaces: [
      'README.md', 'site/index.html', 'web/src/views/Login.vue', 'web/src/locales/*.json',
      'docs/*.md', 'docs/index.md', 'docs/README.md', 'docs-site/.vitepress/config.mts',
      'packages/*/README.md', 'desktop/README.md', 'deploy/*/README.md',
      'deploy/umbrel/*/umbrel-app.yml', 'deploy/casaos/*', 'deploy/runtipi/*/metadata/description.md',
      'deploy/helm/*/Chart.yaml', 'deploy/compose/*.yml',
    ],

    // ── 2. audit: what a script CAN say about the pictures ──────────────────
    audit: [
      readmePictures(),
      // Commit dates, not pixels: fails only past six releases of drift, and
      // lists the drifting pictures for the person doing step 2.
      { name: 'README pictures are not left behind (shop window)', cmd: ['node', 'scripts/check-shop-window.mjs', '--pictures'] },
    ],

    // ── 3. docs: the four documentation commands, all required ──────────────
    docs: [
      { name: 'docs: every relative link resolves', cmd: ['node', 'scripts/check-links.mjs'] },
      // VitePress fails on a dead PAGE link; lesson #34: an inline code span
      // broken over a line kills the build with a misleading line number.
      { name: 'docs: the site builds', sh: 'cd docs-site && npm run build' },
      // …and ignores dead #anchors entirely (98 of 366 were dead on a green build).
      { name: 'docs: every in-page anchor lands', cmd: ['node', 'scripts/check-doc-anchors.mjs'] },
      // Lessons #430/#432: Helm templates are not YAML until rendered, and the
      // "every part on" switches are read from values.yaml, never typed.
      { name: 'docs: YAML parses, the Helm chart lints and renders', cmd: (c) => [c.bash, 'scripts/release/gates/yaml-helm.sh', '.'] },
    ],

    // ── 5. pretag: the whole chain, on the release commit ───────────────────
    // After the stamp, so a test that reads "the newest CHANGELOG section" or
    // "the version in package.json" sees THIS release (two did not, on the
    // 0.44.1 tag round, because they ran before the bump).
    pretag: [
      { name: 'build: packages, admin (vue-tsc + vite), embed', sh: "pnpm -r --filter './packages/*' build && pnpm --filter ./web build && node scripts/sync-embed.mjs" },
      { name: 'build: server binary', cmd: ['node', 'scripts/build-backend.mjs', '--out', bin] },
      // 2026-09-14: a binary with a 16-hour-old UI passed every API check.
      { name: 'the binary serves the UI just built, byte for byte', cmd: ['node', 'scripts/check-embed.mjs', '--binary', bin] },
      // Without the fixture the app-plugin Go tests skip in silence.
      goGate('go: echo.wasm fixture', (c) => c.repo, 'bash scripts/build-wasm-fixture.sh'),
      goGate('go: vet + test', (c) => path.join(c.repo, 'backend'), 'go vet ./... && go test ./...'),
      // A migration that only works on sqlite bricks the first boot after an
      // upgrade for everyone else; the parity tests SKIP without a DSN.
      { name: 'go: migrations on sqlite, postgres AND mysql', cmd: ['node', 'scripts/release/gates/engines.mjs'] },
      {
        name: 'web unit (this machine\'s clock)',
        vitest: { cwd: 'web', files: [], env: (c) => ({ FILEX_WORKFLOWS_DIR: workflowsOf(c.exportDir) }), mustPass: WORKFLOW_GUARDS },
      },
      // Lesson #448: v0.43.0's CI failed a test a UTC+3 machine had hidden.
      { name: 'TZ=UTC takes effect for node here', cmd: ['node', '-e', 'process.exit(new Date().getTimezoneOffset() === 0 ? 0 : 1)'], env: { TZ: 'UTC' } },
      {
        name: 'web unit (TZ=UTC, like CI)',
        vitest: { cwd: 'web', files: [], env: (c) => ({ TZ: 'UTC', FILEX_WORKFLOWS_DIR: workflowsOf(c.exportDir) }), mustPass: WORKFLOW_GUARDS },
      },
      { name: 'packages unit (TZ=UTC)', sh: "pnpm --filter './packages/*' test", env: { TZ: 'UTC' } },
      // 0.43.x shipped a desktop window whose script never parsed (#52).
      { name: 'desktop: typecheck + unit', sh: 'cd desktop && npx tsc --noEmit -p . && pnpm test' },
      // v0.43.0: npm and the Release went out, the images never built —
      // nothing local had ever built one.
      dockerBuild('docker/Dockerfile', 'full'),
      dockerBuild('docker/Dockerfile.slim', 'slim'),
      { name: 'docker: both images report the release', cmd: ['node', 'scripts/release/gates/docker-image.mjs', '--version', tag, img('full'), img('slim')] },
      { name: 'docker: the full image serves the admin UI and /embed.js', cmd: ['node', 'scripts/release/gates/docker-image.mjs', '--serve', img('full')] },
      // CONTRIBUTING step 12, "before the tag": demo mode refuses writes,
      // anonymous capabilities name no host. Exit 2 (could not check) is red.
      { name: 'shop window: a throwaway instance', cmd: ['node', 'scripts/check-shop-window.mjs', '--instance', '--boot', bin] },
      // The suite release.yml waits for, and the journeys it does not walk.
      { name: 'e2e: Cypress (whole)', cmd: ['node', 'e2e/run.mjs', 'cypress', '--binary', bin] },
      { name: 'e2e: Playwright (whole)', cmd: ['node', 'e2e/run.mjs', 'local', '--binary', bin] },
    ],

    // ── 6. export: the tree that ships ──────────────────────────────────────
    exportGates: [
      {
        // The public tree is a fresh `git archive`: backend/embed/{admin,web}
        // are build output (ignored), so the tree only compiles with the UI
        // this release built. Copied, never committed.
        name: 'export: the UI this release built, in its (ignored) embed dirs',
        check: (c) => {
          const lines = [];
          for (const sub of ['admin', 'web']) {
            const from = path.join(c.repo, 'backend', 'embed', sub);
            const to = path.join(c.exportTarget, 'backend', 'embed', sub);
            if (!fs.existsSync(path.join(from, 'index.html')) && sub === 'admin') return { ok: false, detail: `${slash(from)} has no build — the pretag build step makes it` };
            const ign = run('git', ['-C', c.exportTarget, 'check-ignore', '-q', `backend/embed/${sub}/x`]);
            if (ign.status !== 0) return { ok: false, detail: `backend/embed/${sub}/ is not ignored in the public tree — copying the build there would change what is committed` };
            fs.rmSync(to, { recursive: true, force: true });
            fs.cpSync(from, to, { recursive: true });
            lines.push(`${sub}: copied`);
          }
          return { ok: true, detail: lines.join(', ') };
        },
      },
      // Lesson #55's checklist: build and test IN the target — the rewrite
      // changes the module path and every example domain.
      goGate('export: go build + vet + test (public module path)', (c) => path.join(c.exportTarget, 'backend'), 'go build ./... && go vet ./... && go test ./...'),
      {
        // ⚠⚠ The same for the web tests, run exactly as the release
        // workflow's Frontend job runs them. v0.45.0 passed every pretag gate
        // and published nothing: a new test read scripts/export-public.sh,
        // which the public tree never has, and failed there with ENOENT — the
        // private tree, where pretag ran it, has the file (2026-09-25).
        name: 'export: the web and package unit tests pass in the public tree',
        cwd: (c) => c.exportTarget,
        env: { NODE_OPTIONS: '--max-old-space-size=4096' },
        sh: "pnpm install --frozen-lockfile && pnpm -r --filter='./packages/*' build && pnpm --filter='./web' build && pnpm --filter='./web' --filter='./packages/*' test",
      },
      {
        // Lesson #510: check with the GoReleaser the release workflow gets.
        name: 'export: goreleaser check, with the GoReleaser CI uses',
        check: (c) => {
          const wf = fs.readFileSync(path.join(c.exportTarget, '.github', 'workflows', 'release.yml'), 'utf8');
          const want = /goreleaser\/goreleaser-action@[^\n]*\n(?:\s+[^\n]*\n)*?\s+version:\s*["']?([^"'\n]+)["']?/.exec(wf)?.[1]?.trim();
          if (!want) return { ok: false, detail: 'release.yml names no GoReleaser version' };
          let ver = want;
          if (!/^v\d+\.\d+\.\d+$/.test(want)) {
            const major = /v(\d+)/.exec(want)?.[1];
            const r = run('gh', ['api', 'repos/goreleaser/goreleaser/releases', '--jq', `[.[] | select(.prerelease == false and (.tag_name | startswith("v${major}.")))][0].tag_name`]);
            ver = r.stdout.trim();
            if (r.status !== 0 || !/^v\d+\.\d+\.\d+$/.test(ver)) return { ok: false, detail: `could not resolve "${want}" to a GoReleaser release (gh: ${(r.stderr || r.stdout).trim()}) — could not check is not a pass` };
          }
          // No bind mount: the daemon may live in WSL and not see this path
          // (engine.mjs → dockerArgv). The config goes in through stdin, into
          // a git repository with the public remote — `check` insists on both.
          const remote = run('git', ['-C', c.exportDir, 'remote', 'get-url', 'origin']).stdout.trim() || 'https://github.com/BRF-Tech/filex.git';
          const script = 'mkdir -p /w && cd /w && git init -q && git remote add origin "$1" && cat > .goreleaser.yml && goreleaser check';
          const r = docker(['run', '-i', '--rm', '--entrypoint', 'sh', `goreleaser/goreleaser:${ver}`, '-c', script, 'sh', remote], {
            input: fs.readFileSync(path.join(c.exportTarget, '.goreleaser.yml'), 'utf8'),
          });
          const out = `${r.stdout}\n${r.stderr}`;
          return r.status === 0 ? { ok: true, detail: `goreleaser ${ver} (release.yml: ${want})`, log: out } : { ok: false, detail: `goreleaser ${ver} check failed:\n${out.trim().split('\n').slice(-12).join('\n')}` };
        },
      },
      {
        name: 'export: the workflow guards ran against the workflows that will run',
        vitest: {
          cwd: 'web',
          files: ['tests/deploy/releaseGatesImages.test.ts', 'tests/deploy/goreleaserTemplates.test.ts', 'tests/deploy/dockerFrontendInputs.test.ts'],
          env: (c) => ({ FILEX_WORKFLOWS_DIR: workflowsOf(c.exportTarget) }),
          mustPass: WORKFLOW_GUARDS,
        },
      },
    ],

    // ── 9. ci: what the tag's workflow actually published ───────────────────
    ciWatch: [
      'gh run list -R BRF-Tech/filex --workflow release.yml -L 3',
      'gh run watch <run id> -R BRF-Tech/filex --exit-status',
    ],
    published: [
      {
        // A Release with its CLI binaries but without the desktop packages is
        // what 0.44.0 and 0.44.1 were: the job after GoReleaser failed.
        name: `GitHub Release ${tag} carries the installers, the feeds and the CLI`,
        check: () => {
          const r = run('gh', ['release', 'view', tag, '-R', 'BRF-Tech/filex', '--json', 'assets', '--jq', '.assets[].name']);
          if (r.status !== 0) return { ok: false, detail: `gh release view ${tag}: ${(r.stderr || r.stdout).trim()}` };
          const have = new Set(r.stdout.split('\n').map((s) => s.trim()).filter(Boolean));
          const want = [
            'checksums.txt', 'latest.yml', 'latest-mac.yml', 'latest-linux.yml',
            'filex-desktop-x64.exe', 'filex-desktop-portable-x64.exe', 'filex-desktop-x86_64.AppImage',
            'filex-desktop-amd64.deb', 'filex-desktop-arm64.dmg',
            `filex_${version}_linux_x86_64.tar.gz`, `filex_${version}_windows_x86_64.zip`, `filex_${version}_darwin_arm64.tar.gz`,
          ];
          const missing = want.filter((w) => !have.has(w));
          return missing.length ? { ok: false, detail: `${have.size} asset(s); missing: ${missing.join(', ')}` } : { ok: true, detail: `${have.size} assets` };
        },
      },
      { name: `ghcr: filex:${tag} and filex:slim-${tag}`, sh: `docker manifest inspect ghcr.io/brf-tech/filex:${tag} >/dev/null && docker manifest inspect ghcr.io/brf-tech/filex:slim-${tag} >/dev/null` },
      {
        name: `npm: the three packages at ${version}`,
        sh: ['@brftech/filex-core', '@brftech/filex', '@brftech/filex-react'].map((p) => `test "$(npm view ${p}@${version} version)" = ${shq(version)}`).join(' && '),
      },
    ],

    // ── 10. deploy: a person's, then read back ──────────────────────────────
    deployChecklist: [
      '1. fm + demo (backup first — docs/DEPLOY_BRF.md):   ssh main "bash /root/filex-deploy.sh {tag}"',
      '2. update manifest (servers + CLI):                bash /g/mail/scripts/filex-publish-manifest.sh',
      '3. desktop feed:   gh release download {tag} -R BRF-Tech/filex -p "filex-desktop-*" -p "latest*.yml" -D <dir>',
      '                   bash /g/mail/scripts/filex-publish-desktop.sh <dir>',
      '4. Releases page:  release-highlights.json entry, (cd docs-site && npm run releases), commit (CONTRIBUTING step 10)',
      '5. docs.filex.sh:  copy docs/ + docs-site/ FROM THE EXPORT ({export}) to /root/filex-docs-src, then refresh (step 11)',
      '6. embeds:         bash /g/mail/scripts/filex-revendor-sync.sh --apply, then brf-mono pull-deploy full + fishapp CI',
      '7. replies on the issues and pull requests this release answers (no "Fixes #N")',
    ],
    deployed: [
      runningRelease(`fm.example.com runs ${tag}, built from the export commit`, 'https://fm.example.com'),
      runningRelease(`demo.filex.sh runs ${tag}, built from the export commit`, 'https://demo.filex.sh'),
      updateManifest(`filex.sh/updates/stable.json offers ${tag}`, 'https://filex.sh/updates/stable.json'),
      desktopFeeds(`desktop feeds offer ${version} and serve the bytes they promise`, 'https://filex.sh/desktop', ['latest.yml', 'latest-mac.yml', 'latest-linux.yml'], {
        mustExist: ['filex-desktop-portable-x64.exe'],
      }),
      docsSite(`docs.filex.sh serves ${tag}'s pages, not a snapshot`, 'https://docs.filex.sh'),
      { name: 'shop window: the published surfaces', cmd: ['node', 'scripts/check-shop-window.mjs', '--published'] },
    ],
  };
}
