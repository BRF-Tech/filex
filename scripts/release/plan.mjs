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
import { createRequire } from 'node:module';
import path from 'node:path';

import { ciParts, completeName, gateParts } from '../ci-parts.mjs';
import { goShellArgv, goToolchain } from '../lib/go-build.mjs';
import { goPackageDirs } from './checks.mjs';
import { docker, dockerArgv, run, shq, slash } from './engine.mjs';
import { circleciPipelines } from './circleci.mjs';
import { githubActions } from './github.mjs';
import { desktopFeeds, docsSite, readmePictures, runningRelease, snapChannel, storeListing, updateManifest, windowsFeedArches } from './verify.mjs';

// The Store's version for a release (0.52.0 -> 1.0.5200.0), the same function the
// Store build stamps into the package.
const { storeVersion } = createRequire(import.meta.url)('../../desktop/scripts/appx-manifest.cjs');

const IS_WIN = process.platform === 'win32';

/**
 * A Go gate in `dirOf(ctx)`. Under WSL, GOFLAGS=-buildvcs=false: a worktree's
 * `.git` file names a Windows path WSL's git cannot follow, and `go build`
 * stamps VCS info by default (lesson #450).
 *
 * Under WSL the gate runs in a MIRROR of the Go module on WSL's own disk
 * (wslMirrorCd), not in the checkout. `$FILEX_CHECKOUT` is the release
 * checkout as the gate's shell reaches it (natively the checkout itself,
 * under WSL its /mnt path): a script is read from it, or one built file
 * written back into it. Go never runs on it.
 *
 * `toolchain` is this machine's; the plan's test names one to read both.
 *
 * `exportsOf(c)`, when given, adds variables to the same `export` (written
 * into the command, never the environment: a variable that fails to cross
 * from Windows into WSL is a variable the gate silently goes without).
 */
function goGate(name, dirOf, script, { exportsOf = null } = {}) {
  return {
    name,
    // The shell it runs, readable by the plan's own test.
    script,
    // The command itself is the one every Go runner here shares
    // (scripts/lib/go-build.mjs goShellArgv).
    cmd: (c, toolchain = goToolchain()) => goShellArgv({ dir: dirOf(c), checkout: c.repo, script, more: exportsOf ? exportsOf(c) : '', toolchain, bash: c.bash }),
  };
}

/**
 * The Go packages a patch touched (`./internal/foo ./cmd/filex`), or `./...`
 * when that cannot be told: see goPackageDirs (checks.mjs).
 */
function changedGoDirs(c) {
  const backend = path.join(c.repo, 'backend');
  const hasGoFiles = (d) => {
    try {
      return fs.readdirSync(path.join(backend, d)).some((f) => f.endsWith('.go'));
    } catch {
      return false;
    }
  };
  const r = goPackageDirs(c.changed ?? null, hasGoFiles);
  return r.all || !r.dirs.length ? './...' : r.dirs.join(' ');
}

/**
 * The public workflows' committed tree in the export checkout, for the cache
 * key of a vitest gate whose WORKFLOW_GUARDS read them (they are not in this
 * repository). null when they cannot be read or have uncommitted changes:
 * then the gate runs.
 */
function workflowsTree(exportDir) {
  const st = run('git', ['-C', exportDir, 'status', '--porcelain', '--untracked-files=all', '--', '.github/workflows']);
  if (st.status !== 0 || st.stdout.trim()) return null;
  const r = run('git', ['-C', exportDir, 'rev-parse', 'HEAD:.github/workflows']);
  return r.status === 0 ? r.stdout.trim() : null;
}

/**
 * Builds echo.wasm in the Go module the gate is in (under WSL: its mirror),
 * with the checkout's script. The script copies the module back into the
 * checkout when it was built elsewhere (scripts/build-wasm-fixture.sh).
 */
const ECHO_FIXTURE = 'FILEX_BACKEND_DIR="$PWD" bash "$FILEX_CHECKOUT/scripts/build-wasm-fixture.sh"';

/** A patch's Go suite: the changed packages and their importers (FILEX_GO_DIRS). */
const GO_TARGETED = 'bash "$FILEX_CHECKOUT/scripts/release/gates/go-targeted.sh"';

// What a gate's verdict depends on: the gate cache's key (engine.mjs) and a
// patch's choice of gates (checks.mjs → selectGates). ⚠ A list that misses a
// file the gate reads makes the cache pass a gate that would fail, so a list
// is the whole tree unless the gate PROVABLY reads less: the Go gates run in
// a mirror holding the module alone (and the checkout's scripts they name),
// the browser suites run the binary and the specs, never the documentation.
const ALL = ['**'];
const GO_INPUTS = ['backend/**', 'scripts/build-wasm-fixture.sh', 'scripts/lib/go-build.mjs', 'scripts/release/gates/go-targeted.sh'];
const MIGRATION_INPUTS = [...GO_INPUTS, 'scripts/release/gates/engines.mjs'];
// The three engines run in a patch only for a change to the schema or the
// code that writes it (the whole suite still runs on GitHub, with both
// engines as services).
const MIGRATION_PATCH = ['backend/db/**', 'backend/internal/db/**', 'backend/go.mod', 'backend/go.sum', 'scripts/release/gates/engines.mjs'];
const E2E_INPUTS = ['**', '!docs/**', '!docs-site/**', '!site/**'];

// Names other gates wait for (`after`).
const BUILD_UI = 'build: packages, admin (vue-tsc + vite), embed';
const BUILD_BIN = 'build: server binary';
const ECHO = 'go: echo.wasm fixture';

// The workflow guards read the PUBLIC workflows, which exist only in the
// export checkout; without them they SKIP and the file still reads green.
// These are the tests that must have RUN (lessons #455, #510).
const WORKFLOW_GUARDS = [
  "ci.yml's docker job cannot be switched off, and builds both images",
  'every publishing job waits for the gate',
  // #76: the tag is made after GitHub tested its commit; the tag run checks.
  'a tag run publishes only a commit that passed CI and a dry run, and fails closed',
  'every job that publishes waits for verify, a partial (only=arm64) run too',
  // #174: no job of the release runs the suite: the push of the commit runs
  // ci.yml's full matrix, and a dry run packages beside it.
  'a dry run packages at once: no job of the release runs the test suite, the push run of its commit does',
  'names a dry run of everything after its commit, as the release gate looks for it',
  "ci.yml's concurrency keeps a run the release calls apart from the branch's own",
  'the release hands every token variable to the goreleaser step',
  'every repository token is a single {{ .Env.NAME }}, as GoReleaser demands',
  // v0.45.1: a tolerated winget warning code still failed the desktop job.
  'ends with exit 0, because GitHub exits the step with $LASTEXITCODE',
  // 0.46.1: the CLA bot asks on every winget pull request; the release signs.
  'calls winget-cla.sh for the CLI and for the desktop app',
  "uses the CLI's title as goreleaser writes it",
  "uses the desktop app's title as wingetcreate submits it",
  'posts the agreement the bot reads, and never fails the release',
  // 0.47.0: every release goes to the Microsoft Store by itself.
  'commits the submission through msstore-submit.ps1, never as a draft',
  'leaves a submission still in certification alone, and never fails the release',
  'keeps the Store package check soft on the runner; the local release run is the strict one',
  // 0.48.1: every release builds, checks and ships the arm64 packages, and a
  // run started by hand publishes nothing unless asked to.
  'builds the Linux arm64 packages natively on ubuntu-24.04-arm',
  'builds Windows arm64 beside x64 and joins the two update feeds, x64 first',
  'bundles both Store packages and submits the bundle',
  'checks every binary inside a package is the architecture on its label',
  "uploads each Linux architecture's snap to the Snap Store",
  // 0.55 (#68): the snap is core24, with no prebuilt template: snapcraft in
  // LXD on both Linux rows, and electron-builder 26 on Node 22.
  'builds both snaps with snapcraft in LXD, and runs electron-builder on Node 22',
  // 0.55 (#68): an installed copy of the previous release updates to this
  // one on the check machines, Windows x64 and arm64, .deb, AppImage and
  // snap (packaging/ci/release-desktop-upgrade.patch,
  // web/tests/deploy/releaseDesktopUpgrade.test.ts).
  'updates from the previous release on every check machine, before the fresh installs',
  "fetches the newest release below this build's version, with the packages of the machine's row",
  'updates the x64 and the arm64 Windows copy, and the check machine gets the x64 installer',
  "runs this build's installer over the old copy as the app's updater does, and wants one copy of this version with the user's data",
  'updates the .deb as the updater does and keeps its setuid sandbox, replaces the AppImage in place, refreshes the snap with its data',
  'previous-release.mjs picks the newest published release below a version',
  'runs the arm64 CLI, server and images on arm64 machines',
  'installs and opens the arm64 desktop packages on arm64 machines',
  'names the arm64 installer in the winget manifest',
  'a dry run publishes nothing',
  'a full release publishes from its tag push only',
  'joins the Windows feeds with x64 first, and keeps what an old x64 install reads',
  // 0.48.1: only the newest winget pull request per package stays open.
  'closes superseded winget pull requests after the new one, for the CLI and the desktop app',
  'closes only older versions of the same package, and nothing when the new pull request is missing',
  // 0.50: the .deb and the AppImage open with Chromium's sandbox on, or
  // refuse. 0.52: the snap opens confined by snapd instead, and asks the
  // Store for no allow-sandbox (packaging/ci/release-snap-confinement.patch).
  "opens the .deb and the AppImage sandboxed, the AppImage's refusal, the snap confined",
  'installs the AppArmor profile docs/DESKTOP.md gives, word for word',
  'a snap waiting for the Snap Store review does not fail the release',
  "tells a sandboxed app from one running without it, from /proc",
  "tells an app confined by its snap from one that is not, from /proc",
  // 0.52.0: GitHub Actions was down at the tag, and every channel but macOS
  // was published from a PC. A run started by hand with only=macos adds the
  // macOS desktop packages and the Homebrew desktop cask to an existing
  // release, and nothing else (packaging/ci/release-macos-only.patch).
  'only=macos needs a tag, may publish, and is no full run',
  'only=macos builds the macos row of a full run, and nothing else',
  'only=macos runs plan, verify and desktop, and no other job',
  'only=macos attaches the macOS packages and commits the Homebrew cask, and leaves winget and the Store alone',
  'a dry run of only=macos publishes nothing, and says what it was',
  "verify asks the tag's commit for CI and a dry run of everything, for only=macos as for only=arm64",
  'a dry run of only=macos is let through without asking, like one of only=arm64',
  // 0.52.0 again: snapcraft cannot cross-build the arm64 snap the PC could not
  // make. only=snap-arm64 builds and sends it alone and attaches that one
  // file, never the AppImage, .deb or .rpm the winget pull request and the
  // feeds pin by hash (packaging/ci/release-snap-arm64-only.patch).
  'only=snap-arm64 needs a tag, may publish, and is no full run',
  'only=snap-arm64 builds on the linux-arm64 row of a full run, without its own build, and nothing else',
  'only=snap-arm64 runs plan, verify and desktop, and no other job',
  'only=snap-arm64 builds the app, the CLI and the snap alone, checks them arm64, and makes no AppImage, .deb or .rpm',
  'only=snap-arm64 sends the snap to the Snap Store and attaches it alone, and touches no other Release file',
  'a dry run of only=snap-arm64 publishes nothing, and keeps the snap',
  "verify asks the tag's commit for CI and a dry run of everything, for only=snap-arm64 as for only=arm64",
  'a dry run of only=snap-arm64 is let through without asking, like one of only=arm64',
  // #146: 0.50.0 shipped without its npm packages because NPM_TOKEN (90 days
  // at most) had run out unnoticed. npm publishes through trusted publishing
  // (OIDC) now, with no token to run out
  // (packaging/ci/release-npm-trusted-publishing.patch).
  'the job that publishes to npm may ask GitHub for an identity token, and reads the repository, nothing more',
  'no npm token reaches the release: no NPM_TOKEN, no NODE_AUTH_TOKEN, no _authToken',
  'publishes with an npm (11.5.1 or later) and a Node (22.14.0 or later) that can use trusted publishing',
  'asks for provenance, and names the repository the way GitHub signs it',
  // #173: the test chain is ci.yml's matrix, a job per part, named by
  // scripts/ci-parts.mjs - the names the release gate reads part by part
  // (web/tests/deploy/ciFullMatrix.test.ts).
  'names every part as scripts/ci-parts.mjs does: the fixed jobs, and every matrix job by its row',
  'the Go parts are the shards of scripts/test-shards.json, plain beside every service and under -race without one',
  'splits Playwright into parts of each engine, and runs the Document Server specs again with one, on every engine',
  'runs the browser lines the build host runs on their own: the store install without a public URL, and S3',
  'the verdict job is green only when every part of its profile is, and a pull request leaves out exactly what ci-parts says',
  'a pull request runs the light profile; a push of main and a run by hand run the full one',
  'never cancels the run of a push: each commit keeps its own verdict',
  'takes every migration up, three steps down and up again through the CLI, on three engines',
  'migrate-roundtrip.sh is red when a down does not undo exactly one migration, or an up does not bring them all back',
  'every browser part fetches the apps, maps the fonts as the build host does, and runs e2e/run.mjs',
  // #174: a tag run promotes what the dry run of its commit built
  // (web/tests/deploy/releasePromote.test.ts).
  "a tag run publishes only after ci.yml's full matrix passed on its commit",
  'a release candidate is a dry run of everything on an untagged version: it stamps the version and pushes its images by digest only',
  "a tag run that promotes builds no image: it checks the dry run's digests on their own architecture, then tags them",
  'a tag run that promotes builds no desktop package: it checks and publishes the files the dry run kept',
  'release-files.mjs passes exactly the files the dry run kept, for this version',
  'a macOS-less dry run leaves the macos row out, and only a run of everything can',
  // (verify's promotion of the rows needs jq, which GitHub's runners have and
  // a workstation may not: it runs in ci.yml, and is no guard of this gate.)
  // #181: a commit CircleCI tested while GitHub Actions was down is published
  // by its tag run once Actions is back - verify takes CircleCI's green `ci`
  // workflow when GitHub lacks its runs, and with no dry run to promote the
  // tag run builds (packaging/ci/release-verify-circleci.patch,
  // web/tests/deploy/releaseVerifyCircleci.test.ts).
  "publishes on CircleCI's green ci workflow when GitHub lacks its runs, and promotes nothing",
  'takes no other word from CircleCI: red, unfinished or unanswered publishes nothing, and says what GitHub lacked',
  "a tag run of a release already packaged off GitHub publishes nothing on CircleCI's word",
  'asks GitHub first: with its full matrix and a dry run, CircleCI is never asked',
  "a run that adds to a release takes CircleCI's word on the tag's commit, as a tag push does",
  'a dry run asks CircleCI nothing',
  "verify reads CircleCI with a token secret of its own, the project's slug from a variable, and this commit's reader",
  'with nothing to promote a tag run builds the images and the desktop packages itself, and tags only what it checked',
  'circleci-green.mjs reads a commit as the release tool does',
  'circleci-green.mjs says when it could not ask, never "not green", and never prints the token',
  'circleci-green.mjs refuses what is no project slug or no commit before it asks anything',
  // 0.53.0: GitHub never created the tag run's desktop jobs (an internal
  // error, on a run that could not be retried) after the binaries, images and
  // npm were out. only=stores sends what the Release has to the Snap Store
  // (amd64), winget and the Microsoft Store, builds nothing the Release has
  // and attaches nothing (packaging/ci/release-stores.patch,
  // web/tests/deploy/releaseStoresOnly.test.ts).
  'only=stores needs a tag, may publish, and is no full run',
  'only=stores keeps the windows, linux and store rows of a full run, and empties the scripts of the two whose files the Release has',
  'only=stores runs plan, verify and desktop, and no other job',
  "only=stores builds nothing on the windows and linux rows: it downloads the Release's files by name and checks them against the Release's digests",
  "only=stores sends the Release's installers to winget and its amd64 snap to the Snap Store, and attaches nothing",
  'only=stores sends the bundle the dry run kept to the Microsoft Store, or builds it when none was kept',
  'a dry run of only=stores publishes nothing, and writes and validates the winget manifest',
  "verify asks the tag's commit for CI and a dry run of everything, for only=stores as for only=arm64, and finds the Store bundle it kept",
  'a dry run of only=stores is let through without asking, like one of only=arm64',
];

/**
 * The installers, feeds and CLI archives a release's GitHub Release must
 * carry. arm64 from 0.48.1: Linux and Windows desktop packages, the Linux
 * arm64 update feed, and the arm64 CLI the smoke tests ran.
 */
export function releaseAssets(version) {
  return [
    'checksums.txt', 'latest.yml', 'latest-mac.yml', 'latest-linux.yml', 'latest-linux-arm64.yml',
    'filex-desktop-x64.exe', 'filex-desktop-portable-x64.exe', 'filex-desktop-x86_64.AppImage',
    'filex-desktop-amd64.deb', 'filex-desktop-arm64.dmg',
    'filex-desktop-arm64.exe', 'filex-desktop-portable-arm64.exe', 'filex-desktop-arm64.AppImage',
    'filex-desktop-arm64.deb', 'filex-desktop-aarch64.rpm', 'filex-desktop-amd64.snap', 'filex-desktop-arm64.snap',
    `filex_${version}_linux_x86_64.tar.gz`, `filex_${version}_windows_x86_64.zip`, `filex_${version}_darwin_arm64.tar.gz`,
    `filex_${version}_linux_arm64.tar.gz`, `filex_${version}_windows_arm64.zip`,
    'filex-linux-arm64', 'filex-windows-arm64.exe', 'filex-darwin-arm64',
    // 0.50: every file a download link names (the install prompt, Settings,
    // DESKTOP.md, CLI.md, filex.sh) is on this list, so a release that drops
    // one fails here instead of leaving a 404 behind a link
    // (web/tests/composables/installDownloads.test.ts holds the links to it).
    'filex-desktop-x86_64.rpm',
    'filex-linux-amd64', 'filex-windows-amd64.exe', 'filex-darwin-amd64',
  ];
}

/**
 * Both winget pull requests of a release name an x64 AND an arm64 installer.
 * Read from the pull request's own commit on microsoft/winget-pkgs, so a
 * merged one (branch deleted) still reads.
 */
function wingetArches(version) {
  return {
    name: `winget: the ${version} pull requests offer x64 and arm64 (BRFTech.filex, BRFTech.filex-app)`,
    check: () => {
      const lines = [];
      const problems = [];
      for (const id of ['BRFTech.filex', 'BRFTech.filex-app']) {
        const title = `New version: ${id} ${version}`;
        const list = run('gh', ['pr', 'list', '--repo', 'microsoft/winget-pkgs', '--author', 'brkfun', '--state', 'all',
          '--search', `"${title}" in:title`, '--json', 'number,title', '--jq', `.[] | select(.title == "${title}") | .number`]);
        if (list.status !== 0) return { ok: false, detail: `could not check — gh: ${(list.stderr || list.stdout).trim()}` };
        const pr = list.stdout.trim().split(/\s+/)[0];
        if (!pr) {
          problems.push(`${id}: no pull request titled "${title}"`);
          continue;
        }
        const view = run('gh', ['pr', 'view', pr, '--repo', 'microsoft/winget-pkgs', '--json', 'headRefOid,files']);
        if (view.status !== 0) return { ok: false, detail: `could not check — gh pr view ${pr}: ${view.stderr.trim()}` };
        const { headRefOid, files } = JSON.parse(view.stdout);
        const inst = files.map((f) => f.path).find((f) => f.endsWith('.installer.yaml'));
        if (!inst) {
          problems.push(`${id} #${pr}: no installer manifest`);
          continue;
        }
        const body = run('gh', ['api', `repos/microsoft/winget-pkgs/contents/${inst}?ref=${headRefOid}`, '--jq', '.content']);
        if (body.status !== 0) return { ok: false, detail: `could not check — ${inst}: ${body.stderr.trim()}` };
        const yml = Buffer.from(body.stdout.trim(), 'base64').toString('utf8');
        const arches = [...yml.matchAll(/Architecture:\s*(\w+)/g)].map((m) => m[1]);
        for (const a of ['x64', 'arm64']) if (!arches.includes(a)) problems.push(`${id} #${pr}: no ${a} installer (has ${arches.join(', ') || 'none'})`);
        lines.push(`${id} #${pr}: ${arches.join(' + ')}`);
      }
      return problems.length ? { ok: false, detail: problems.join('\n') } : { ok: true, detail: lines.join('; ') };
    },
  };
}

/**
 * The tag's release run built the Store bundle (x64 + arm64) and submitted it
 * without a Store warning. The Store's own listing changes only once
 * certification passes (hours to days), so the run is what is read here.
 *
 * A release whose tag run published nothing — 0.52.0's stopped at verify
 * during a GitHub Actions outage and went out from a PC, the bundle submitted
 * by hand in Partner Center — has no Store job to read. Only then the Store's
 * public listing answers instead, which turns green once certification passes.
 * A Store job that ran and failed stays red: the listing would still show the
 * previous version, or a hand submission would hide the failure.
 *
 * 0.53.0: GitHub never created the tag run's desktop jobs, and a run of
 * release.yml with only=stores ("publish stores <tag> <commit>", run-name)
 * sent the Store bundle instead. When the tag run has no Store job, that run's
 * is read the same way.
 */
function storeBundle(tag, version) {
  return {
    name: `Microsoft Store: ${tag}'s run bundled x64 + arm64 and submitted it`,
    check: async () => {
      const listing = async (why) => {
        const r = await storeListing('9PKXDJLVZWXW', 'BRFTech.filexapp', storeVersion(version), ['x64', 'arm64']);
        return { ok: r.ok, detail: `${why}; ${r.detail}` };
      };
      const storeJob = (id) => {
        const view = run('gh', ['run', 'view', id, '-R', 'BRF-Tech/filex', '--json', 'jobs']);
        if (view.status !== 0) return { error: `could not check — gh run view ${id}: ${view.stderr.trim()}` };
        return { job: JSON.parse(view.stdout).jobs.find((j) => j.name === 'Desktop packages (store)') };
      };
      const runs = run('gh', ['run', 'list', '-R', 'BRF-Tech/filex', '--workflow', 'release.yml', '--branch', tag, '--limit', '1', '--json', 'databaseId', '--jq', '.[0].databaseId']);
      if (runs.status !== 0) return { ok: false, detail: `could not check — gh run list: ${runs.stderr.trim()}` };
      let id = runs.stdout.trim();
      let job;
      if (id) {
        const r = storeJob(id);
        if (r.error) return { ok: false, detail: r.error };
        job = r.job;
      }
      if (!job) {
        const title = `publish stores ${tag} `;
        const stores = run('gh', ['run', 'list', '-R', 'BRF-Tech/filex', '--workflow', 'release.yml', '--event', 'workflow_dispatch', '--limit', '100', '--json', 'databaseId,displayTitle',
          '--jq', `[.[] | select(.displayTitle | startswith(${JSON.stringify(title)}))][0].databaseId // empty`]);
        if (stores.status !== 0) return { ok: false, detail: `could not check — gh run list: ${stores.stderr.trim()}` };
        const sid = stores.stdout.trim();
        if (sid) {
          const r = storeJob(sid);
          if (r.error) return { ok: false, detail: r.error };
          if (r.job) [id, job] = [sid, r.job];
        }
      }
      if (!job) return listing(id ? `run ${id} has no "Desktop packages (store)" job` : `no release.yml run for ${tag}`);
      const notes = run('gh', ['api', `repos/BRF-Tech/filex/check-runs/${job.databaseId}/annotations`, '--jq', '.[] | select(.title == "Microsoft Store") | .message']);
      const problems = storeJobProblems(job.steps, notes.status === 0 ? notes.stdout.trim() : '');
      return problems.length ? { ok: false, detail: `run ${id}: ${problems.join('; ')}` } : { ok: true, detail: `run ${id}: bundled and submitted` };
    },
  };
}

/**
 * What is wrong with a release run's "Desktop packages (store)" job, from its
 * steps and its "Microsoft Store" annotations: the bundle was built there
 * ("Bundle the Store packages") or is the one the dry run kept, checked
 * against its sums (a tag run that promotes, #174, and only=stores), and it
 * was submitted without a warning (msstore-submit.ps1 warns rather than fails).
 */
export function storeJobProblems(steps, warnings = '') {
  const step = (n) => steps.find((x) => x.name === n)?.conclusion ?? 'missing';
  const built = 'Bundle the Store packages';
  const kept = 'They are the files the dry run kept, for this version';
  const problems = [];
  if (step(built) !== 'success' && step(kept) !== 'success') problems.push(`"${built}": ${step(built)}, "${kept}": ${step(kept)}`);
  if (step('Submit to the Microsoft Store') !== 'success') problems.push(`"Submit to the Microsoft Store": ${step('Submit to the Microsoft Store')}`);
  if (warnings) problems.push(`the run warned: ${warnings}`);
  return problems;
}

/**
 * #174: a tag run PROMOTES the images the dry run of its commit built, it
 * does not build them - unless the commit was gated on CircleCI and has no
 * dry run (#181), and then the tag run builds them from the same commit. So
 * the tag's images must say they were built from the tag's public commit (the
 * revision label `docker` wrote) and, asked `filex --version`, name the tag
 * and that commit. An image of another commit, or one that names another, is
 * red here. Read on amd64 (this machine); the run's `promote-check` read each
 * architecture on its own.
 * `docker image inspect` whole, no --format: an argument with quotes or
 * spaces may not survive the way to a daemon in WSL (engine.mjs, dockerArgv).
 */
export function promotedImages(tag) {
  return {
    name: `ghcr: filex:${tag} and filex:slim-${tag} were built from the tag's commit (promoted from its dry run, or built by a tag run with none), and filex --version says so`,
    check: (c) => {
      if (!c.exportHead) return { ok: false, detail: 'no export commit is recorded (the land stage records it)' };
      const lines = [];
      for (const ref of [`ghcr.io/brf-tech/filex:${tag}`, `ghcr.io/brf-tech/filex:slim-${tag}`]) {
        const pull = docker(['pull', '-q', '--platform', 'linux/amd64', ref]);
        if (pull.status !== 0) return { ok: false, detail: `docker pull ${ref}: ${(pull.stderr || pull.stdout).trim()} - could not check is not a pass` };
        const inspect = docker(['image', 'inspect', ref]);
        let rev = '';
        try {
          rev = JSON.parse(inspect.stdout)[0]?.Config?.Labels?.['org.opencontainers.image.revision'] ?? '';
        } catch {
          return { ok: false, detail: `docker image inspect ${ref} did not answer JSON: ${(inspect.stderr || inspect.stdout).trim().slice(0, 200)}` };
        }
        const out = docker(['run', '--rm', '--platform', 'linux/amd64', '--entrypoint', '/usr/local/bin/filex', ref, '--version']);
        const said = String(out.stdout ?? '').trim();
        if (out.status !== 0) return { ok: false, detail: `${ref}: filex --version exited ${out.status}: ${String(out.stderr ?? '').trim()}` };
        if (rev !== c.exportHead) {
          return { ok: false, detail: `${ref} was built from ${rev || 'no labelled commit'}, not from ${tag}'s commit ${c.exportHead}: it is not the image the dry run (or the tag run) of that commit built` };
        }
        if (!said.includes(tag) || !said.includes(c.exportHead)) return { ok: false, detail: `${ref}: filex --version says "${said}", not ${tag} (${c.exportHead})` };
        lines.push(`${ref}: ${said}`);
      }
      return { ok: true, detail: lines.join('; ') };
    },
  };
}

/** The npm names of the public packages under packages/ (release.yml publishes them all). */
export function npmPackages(repo) {
  const dir = path.join(repo, 'packages');
  return fs
    .readdirSync(dir)
    .map((d) => path.join(dir, d, 'package.json'))
    .filter((f) => fs.existsSync(f))
    .map((f) => JSON.parse(fs.readFileSync(f, 'utf8')))
    .filter((pkg) => !pkg.private)
    .map((pkg) => pkg.name)
    .sort();
}

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
    // The Go module lives in backend/, so its tag is backend/vX.Y.Z (stages.mjs
    // signs and pushes `${backendTagPrefix}${tag}`). 0.50: the plan did not
    // name it and the sign/push stages looked for "undefinedv0.50.0".
    backendTagPrefix: 'backend/',
    // The maintainer key (docs/CONTRIBUTING.md → Release process, step 8).
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
      // README.*.md: its translations, carried along or left behind whole
      // (CONTRIBUTING → Docs); listed so the audit says which way it went.
      'README.md', 'README.*.md', 'site/index.html', 'web/src/views/Login.vue', 'web/src/locales/*.json',
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
      // #176: the README, the docs and filex.sh show the pictures published on
      // filex.sh/shots. Every link must be the manifest's current file and
      // answer with exactly its bytes - read back over the network, because
      // the public README is exported from this tree and a link to a picture
      // nobody uploaded is a broken image in the shop window.
      {
        name: 'README pictures are published (filex.sh/shots, read back)',
        cmd: ['node', 'scripts/shots-site.mjs', 'verify', '--live'],
        expect: /answer 200 with the bytes the manifest names/,
      },
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

    // ── 5. pretag: the fast gates, on the release commit (task #172) ────────
    // After the stamp, so a test that reads "the newest CHANGELOG section" or
    // "the version in package.json" sees THIS release (two did not, on the
    // 0.44.1 tag round, because they ran before the bump).
    //
    // ⚠ Fast: the builds and vue-tsc, the unit suites in UTC, the desktop and
    // the Store copy, both images, the shop window - about 15-20 minutes with
    // the lanes side by side (`jobs`). The heavy suites are `heavy` below, and
    // the profile (checks.mjs → PROFILES) says where they run. `always`: a
    // patch runs it too, whatever changed - the builds and the packaging,
    // because 0.43.0 and 0.44.0/0.44.1 died building, not testing (#76).
    jobs: 3,
    pretag: [
      { name: BUILD_UI, lane: 'build', always: true, sh: "pnpm -r --filter './packages/*' build && pnpm --filter ./web build && node scripts/sync-embed.mjs" },
      // ⚠ Lane `go` holds every gate that goes through the WSL mirror of the
      // Go module (scripts/lib/go-build.mjs): each one rsyncs it with
      // --delete, and two at once would rewrite files under a running build.
      { name: BUILD_BIN, lane: 'go', always: true, after: [BUILD_UI], cmd: ['node', 'scripts/build-backend.mjs', '--out', bin] },
      // 2026-09-14: a binary with a 16-hour-old UI passed every API check.
      { name: 'the binary serves the UI just built, byte for byte', lane: 'build', always: true, after: [BUILD_BIN], cmd: ['node', 'scripts/check-embed.mjs', '--binary', bin] },
      // Lesson #448: v0.43.0's CI failed a test a UTC+3 machine had hidden.
      { name: 'TZ=UTC takes effect for node here', lane: 'web', always: true, cmd: ['node', '-e', 'process.exit(new Date().getTimezoneOffset() === 0 ? 0 : 1)'], env: { TZ: 'UTC' } },
      // The web suite reads the core package's build (lesson #1075).
      {
        name: 'web unit (TZ=UTC, like CI)',
        lane: 'web',
        after: [BUILD_UI],
        inputs: ALL,
        cacheExtra: (c) => workflowsTree(c.exportDir),
        vitest: { cwd: 'web', files: [], env: (c) => ({ TZ: 'UTC', FILEX_WORKFLOWS_DIR: workflowsOf(c.exportDir) }), mustPass: WORKFLOW_GUARDS },
      },
      { name: 'packages unit (TZ=UTC)', lane: 'web', after: [BUILD_UI], inputs: ALL, sh: "pnpm --filter './packages/*' test", env: { TZ: 'UTC' } },
      // 0.43.x shipped a desktop window whose script never parsed (#52).
      { name: 'desktop: typecheck + unit', lane: 'desktop', always: true, after: [BUILD_UI], inputs: ALL, sh: 'cd desktop && npx tsc --noEmit -p . && pnpm test' },
      // A Store package proves itself only once Windows has installed AND
      // activated it, and the GitHub runner never has (v0.44.2 to v0.46.1 its
      // activation checks only warned). release.yml sends the package it
      // builds from this commit to certification, so it is seen working here
      // first, on a desktop, strictly: a protocol launch, the app answering
      // from inside the package, its toast filed under the package, the
      // startup task (desktop/scripts/store-e2e.mjs, needs Developer Mode).
      IS_WIN
        ? {
            name: 'desktop: the Store package works as a Store copy',
            lane: 'desktop',
            always: true,
            after: [BUILD_BIN],
            inputs: ALL,
            sh: 'cd desktop && pnpm run build && node scripts/fetch-cli.mjs --platform win32 && node scripts/store-e2e.mjs',
            env: (c) => ({ FILEX_CLI_BIN: path.resolve(c.repo, bin) }),
          }
        : {
            name: 'desktop: the Store package works as a Store copy',
            lane: 'desktop',
            always: true,
            cmd: ['node', '-e', 'console.error("store-e2e installs an MSIX: run the release on Windows with Developer Mode"); process.exit(1)'],
          },
      // v0.43.0: npm and the Release went out, the images never built —
      // nothing local had ever built one. After the local builds: the image
      // build reads the tree they are writing into.
      { ...dockerBuild('docker/Dockerfile', 'full'), lane: 'docker', always: true, after: [BUILD_BIN] },
      { ...dockerBuild('docker/Dockerfile.slim', 'slim'), lane: 'docker', always: true, after: [BUILD_BIN] },
      { name: 'docker: both images report the release', lane: 'docker', always: true, cmd: ['node', 'scripts/release/gates/docker-image.mjs', '--version', tag, img('full'), img('slim')] },
      { name: 'docker: the full image serves the admin UI and /embed.js', lane: 'docker', always: true, cmd: ['node', 'scripts/release/gates/docker-image.mjs', '--serve', img('full')] },
      // CONTRIBUTING step 12, "before the tag": demo mode refuses writes,
      // anonymous capabilities name no host. Exit 2 (could not check) is red.
      { name: 'shop window: a throwaway instance', lane: 'e2e', always: true, after: [BUILD_BIN], cmd: ['node', 'scripts/check-shop-window.mjs', '--instance', '--boot', bin] },
    ],

    // ── 5b. the heavy gates: where the profile says (task #172) ─────────────
    // `github`: the workflow that runs the same suite on the export commit -
    // a minor reads it there, at the gate stage, instead of running it here
    // first; `githubJobs`, the jobs of ci.yml's matrix that stand for it, which
    // the gate stage reads one by one (#173, #174: since the matrix, every
    // heavy gate has its parts on GitHub, and a minor runs none of them here).
    // Without one a minor runs it HERE during the gate stage, beside the CI's
    // wait, on the release commit - with the builds it is `after`, made again
    // there - or a green scripts/chain run stands in (--chain, `chainProfiles`
    // below); with the gate on CircleCI (#181) that is what it does not run.
    // `full` runs every one here in pretag. A patch runs the Go suite on what
    // it changed (and its importers) and the migrations when the schema
    // changed, and leaves the rest to GitHub (#165, decision 5; #76: e2e
    // whole only in a minor or major).
    heavy: [
      // Without the fixture the app-plugin Go tests skip in silence.
      // ⚠⚠ In the Go module, and back into the CHECKOUT: the Playwright gate
      // below installs the checkout's echo.wasm, and on Windows Go builds in a
      // WSL mirror. On the v0.50.0 pretag this gate refreshed only its mirror (it
      // ran in a mirror of the whole repository); the checkout's copy predated
      // the echo change spec 192 tested, and 192 failed after an hour of
      // chain (lesson #960, #139). The module dir also keeps the private
      // pretag's mirror module-only, like the export's (#141).
      // `circleci`: the build and Go jobs build it there (.circleci/config.yml),
      // so a gate stage reading CircleCI does not build it here for nothing.
      // #174: on GitHub every Go part and the browser build make it, and the
      // Go parts refuse to run without it (FILEX_REQUIRE_WASM_FIXTURE).
      {
        ...goGate(ECHO, (c) => path.join(c.repo, 'backend'), ECHO_FIXTURE),
        lane: 'go',
        github: 'ci.yml',
        githubJobs: (c) => gateParts(c.repo).go,
        circleci: 'build',
        patchSkip: 'it is for the browser suite, which a patch leaves to GitHub',
      },
      // ⚠ The fixture AGAIN, in this gate's own mirror. Each Go gate rsyncs
      // its module with --delete, so the Windows checkout's gitignored
      // echo.wasm - as old as whoever last built it there - came back over the
      // one the gate above had just built. After a change to the echo app's
      // main.go it is older than main.go, and every app-plugin test refuses to
      // run on it (the v0.48.0 pretag: 50-odd "echo.wasm is older than
      // main.go" failures). The gate above now writes the module back into
      // the checkout as well; building here keeps this gate right on its own.
      // -timeout 30m: go test's own default is 10m PER PACKAGE, and handlers
      // (~5 min alone) ran beside wasmplugin on a busy workstation and crossed it
      // (v0.49.0 release run, 2026-09-29: "test timed out after 10m0s" with
      // no test hung). A hung test still fails, only later.
      {
        ...goGate('go: vet + test', (c) => path.join(c.repo, 'backend'), `${ECHO_FIXTURE} >/dev/null && go vet ./... && go test -timeout 30m ./...`),
        lane: 'go',
        github: 'ci.yml',
        // #173: vet + build, then one job per shard of the `go` profile of
        // scripts/test-shards.json; the -race profile's shards are the
        // matrix's too, though no gate here runs them (githubMatrix below).
        githubJobs: (c) => gateParts(c.repo).go,
        // #181: vet in the build job, the `go` profile's shards in the go job.
        circleci: 'build + go',
        inputs: GO_INPUTS,
      },
      // A patch's Go suite: what it changed and everything that imports it
      // (scripts/release/gates/go-targeted.sh); the whole module when the
      // change cannot be placed (go.mod, a file at the module root).
      {
        ...goGate('go: vet + test, the packages a patch changed and their importers', (c) => path.join(c.repo, 'backend'), `${ECHO_FIXTURE} >/dev/null && ${GO_TARGETED}`, {
          exportsOf: (c) => ` FILEX_GO_DIRS=${shq(changedGoDirs(c))}`,
        }),
        lane: 'go',
        patchOnly: true,
        inputs: GO_INPUTS,
      },
      // A migration that only works on sqlite bricks the first boot after an
      // upgrade for everyone else; the parity tests SKIP without a DSN.
      // In the Go lane: it runs in the same WSL mirror of the module.
      // CircleCI: the go job's shards run beside PostgreSQL and MySQL, and the
      // one that ran internal/db must show both engines' parity subtests
      // passed (scripts/ci/engines-ran.mjs), or it is red - a skip is no pass.
      {
        name: 'go: migrations on sqlite, postgres AND mysql',
        lane: 'go',
        github: 'ci.yml',
        // #173: the parity suites, and the CLI's up, down three, up on all three.
        githubJobs: (c) => gateParts(c.repo).migrations,
        circleci: 'go',
        patch: 'changed',
        inputs: MIGRATION_INPUTS,
        patchWhen: MIGRATION_PATCH,
        cmd: ['node', 'scripts/release/gates/engines.mjs'],
      },
      // CI runs the unit suite in UTC only; this machine's clock is the one
      // a test that assumes UTC hides behind (and the other way round).
      {
        name: 'web unit (this machine\'s clock)',
        lane: 'web',
        // #173: the matrix runs the unit suite on Istanbul's clock too.
        github: 'ci.yml',
        githubJobs: (c) => gateParts(c.repo).webLocalClock,
        after: [BUILD_UI],
        inputs: ALL,
        cacheExtra: (c) => workflowsTree(c.exportDir),
        patchSkip: 'a patch runs the unit suite once, in UTC',
        vitest: { cwd: 'web', files: [], env: (c) => ({ FILEX_WORKFLOWS_DIR: workflowsOf(c.exportDir) }), mustPass: WORKFLOW_GUARDS },
      },
      // The suite release.yml waits for, and the journeys it does not walk.
      { name: 'e2e: Cypress (whole)', lane: 'e2e', github: 'ci.yml', githubJobs: (c) => gateParts(c.repo).cypress, after: [BUILD_BIN], inputs: E2E_INPUTS, cmd: ['node', 'e2e/run.mjs', 'cypress', '--binary', bin] },
      {
        name: 'e2e: Playwright (whole)',
        lane: 'e2e',
        // #173: three engines in parts, the Document Server specs again with
        // one on every engine, the store and S3 lines.
        github: 'ci.yml',
        githubJobs: (c) => gateParts(c.repo).playwright,
        // #181: the e2e job, in parts (Chromium, as here).
        circleci: 'e2e',
        after: [BUILD_BIN, ECHO],
        inputs: E2E_INPUTS,
        patchSkip: 'a patch runs no browser suite here (#76: whole only in a minor or major)',
        cmd: ['node', 'e2e/run.mjs', 'local', '--binary', bin],
      },
    ],

    // The scripts/chain profiles (task #170, run on the build host) whose
    // green result.json may stand in for the heavy gates a minor would run
    // here at the gate stage: `pnpm release X --resume --chain <result.json>`.
    // Its commit must be in this release, with nothing changed since but what
    // the stamp writes (checks.mjs → chainVerdict).
    chainProfiles: { minor: ['full', 'nightly'], patch: ['targeted', 'full', 'nightly'] },

    // ── 6. export: the tree that ships ──────────────────────────────────────
    // ⚠⚠ No test suite runs here any more (task #172). Since #76 the public
    // tree lands untagged and GitHub runs ci.yml and the release dry run on
    // that very commit before anything is tagged, so a red there spends no
    // number - and the Go and web suites this stage ran were the same ones,
    // a fourth and a fifth time, 19 minutes a release (0.51). What stays is
    // what nothing after the land would catch, or would catch only once it is
    // public: the scan for what must never be published, the deletions and
    // the executable bits (stages.mjs), that the public module still BUILDS
    // under its rewritten path, goreleaser's check, and the workflow guards.
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
      // Lesson #55's checklist: the rewrite changes the module path and every
      // example domain, so the public module is BUILT in the target - a build
      // is never left to GitHub (#76). Its vet and tests run there, in ci.yml
      // and the dry run, on the landed commit (v0.45.0's test that read the
      // private export script failed there too, before any tag).
      goGate('export: go build (public module path)', (c) => path.join(c.exportTarget, 'backend'), 'go build ./...'),
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
          files: [
            'tests/deploy/releaseGatesImages.test.ts',
            'tests/deploy/goreleaserTemplates.test.ts',
            'tests/deploy/dockerFrontendInputs.test.ts',
            'tests/deploy/wingetCla.test.ts',
            'tests/deploy/msstoreSubmit.test.ts',
            'tests/deploy/releaseArm64.test.ts',
            'tests/deploy/releaseMacosOnly.test.ts',
            'tests/deploy/releaseSnapArm64Only.test.ts',
            'tests/deploy/releaseNpmTrusted.test.ts',
            'tests/deploy/ciFullMatrix.test.ts',
            'tests/deploy/releasePromote.test.ts',
            'tests/deploy/releaseVerifyCircleci.test.ts',
            'tests/deploy/releaseStoresOnly.test.ts',
            'tests/deploy/releaseDesktopUpgrade.test.ts',
          ],
          env: (c) => ({ FILEX_WORKFLOWS_DIR: workflowsOf(c.exportTarget) }),
          mustPass: WORKFLOW_GUARDS,
        },
      },
    ],

    // ── 8. gate: GitHub tests the export commit before it is tagged ─────────
    // #76: both mains go out untagged; the gate starts release.yml's dry run
    // (publish=false) on the export commit and waits until it and ci.yml (the
    // push of main) passed there. Only then are the tags made, and the tag
    // run's `verify` job refuses a commit without those two runs. A dry run
    // takes about as long as a release run; four hours is a stuck runner.
    github: githubActions('BRF-Tech/filex'),
    gateWait: { pollMs: 60_000, timeoutMs: 4 * 3600_000, appearMs: 10 * 60_000 },
    // #173/#174: the push run of ci.yml is read part by part - every job of
    // its full matrix (scripts/ci-parts.mjs) must have run and passed, the
    // last of them `All tests (full)`, which is also what a tag run's
    // `verify` asks for. A run that passed without a part is no full matrix.
    githubMatrix: { workflow: 'ci.yml', parts: (c) => ciParts(c.repo, 'full'), complete: completeName('full') },
    // #174: what the tag run promotes instead of building it again - both
    // images by digest, and the files of every desktop row the dry run built.
    // A dry run without them is no release candidate (its version was tagged
    // already, it was no run of everything, or they expired after 14 days).
    // macOS may be missing: a dry run started with -f macos=false while
    // GitHub has no macOS runner, and the release goes out without it.
    promotion: {
      required: ['digests-amd64', 'digests-arm64', 'release-files-windows', 'release-files-linux', 'release-files-linux-arm64', 'release-files-store'],
      optional: ['release-files-macos'],
    },
    // #181: when GitHub Actions is down, `--gate circleci` reads the workflow
    // `ci` of .circleci/config.yml on the export commit instead (no dry run).
    // The project's slug: `gh/BRF-Tech/filex` for an organization connected
    // through GitHub OAuth, `circleci/<org id>/<project id>` on the GitHub App
    // (Project Settings → Overview); CIRCLECI_PROJECT_SLUG overrides it. The
    // token is CIRCLECI_TOKEN, from the environment only.
    circleci: circleciPipelines(process.env.CIRCLECI_PROJECT_SLUG || 'gh/BRF-Tech/filex'),
    circleciWorkflow: 'ci',

    // ── 11. ci: what the tag's workflow actually published ──────────────────
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
          const want = releaseAssets(version);
          const missing = want.filter((w) => !have.has(w));
          return missing.length ? { ok: false, detail: `${have.size} asset(s); missing: ${missing.join(', ')}` } : { ok: true, detail: `${have.size} assets` };
        },
      },
      // 0.48.1: the arm64 installer joined the x64 one in the ONE feed
      // Windows reads; x64 first, or every existing install is offered arm64.
      windowsFeedArches(`GitHub Release ${tag}: latest.yml offers the x64 and the arm64 installer, x64 first`, `https://github.com/BRF-Tech/filex/releases/download/${tag}/latest.yml`),
      { name: `ghcr: filex:${tag} and filex:slim-${tag}`, sh: `docker manifest inspect ghcr.io/brf-tech/filex:${tag} >/dev/null && docker manifest inspect ghcr.io/brf-tech/filex:slim-${tag} >/dev/null` },
      // Each image for both architectures, each smoke-tested on its own
      // machine before docker-manifest joined them.
      {
        name: `ghcr: filex:${tag} and filex:slim-${tag} are amd64 + arm64`,
        sh: ['', 'slim-'].map((p) => `docker manifest inspect ghcr.io/brf-tech/filex:${p}${tag} | grep -q '"architecture": "arm64"' && docker manifest inspect ghcr.io/brf-tech/filex:${p}${tag} | grep -q '"architecture": "amd64"'`).join(' && '),
      },
      // #174: promoted, not rebuilt - the tag's commit, and its --version.
      promotedImages(tag),
      // One revision per architecture, both on stable (0.48.1: arm64).
      snapChannel(`Snap Store: filex-app ${version} on stable for amd64 and arm64`, 'filex-app', ['amd64', 'arm64']),
      wingetArches(version),
      storeBundle(tag, version),
      {
        // Every package under packages/ — what release.yml publishes
        // (`pnpm publish --filter='./packages/*'`). A written list missed
        // @brftech/filex-app-ui (0.48), which filex-core depends on.
        name: `npm: every package under packages/ at ${version} (${npmPackages(repo).join(', ')})`,
        sh: npmPackages(repo).map((p) => `test "$(npm view ${p}@${version} version)" = ${shq(version)}`).join(' && '),
      },
    ],

    // ── 12. deploy: a person's, then read back ──────────────────────────────
    deployChecklist: [
      'ONE COMMAND does 1-6 below, then the checks that follow, and prints what is left to a person (#178):',
      '                   bash scripts/train/filex-ship.sh {version}        (settings: scripts/train/train.env.example)',
      'By hand, step by step:',
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
      desktopFeeds(`desktop feeds offer ${version} and serve the bytes they promise`, 'https://filex.sh/desktop', ['latest.yml', 'latest-mac.yml', 'latest-linux.yml', 'latest-linux-arm64.yml'], {
        mustExist: ['filex-desktop-portable-x64.exe', 'filex-desktop-portable-arm64.exe'],
      }),
      windowsFeedArches('filex.sh/desktop/latest.yml offers the x64 and the arm64 installer, x64 first', 'https://filex.sh/desktop/latest.yml'),
      docsSite(`docs.filex.sh serves ${tag}'s pages, not a snapshot`, 'https://docs.filex.sh'),
      { name: 'shop window: the published surfaces', cmd: ['node', 'scripts/check-shop-window.mjs', '--published'] },
    ],
  };
}
