// The shop window: everything a stranger touches before they trust us.
//
// ⚠⚠ Every assertion below is a defect that shipped. They were all found on
// 2026-09-07 by a person looking at the product from outside, hours before a
// public launch, and **not one of them was caught by a test, a lint, or any of
// the eleven steps of the release process** — which check README prose,
// screenshots, links, anchors and version manifests, and never once look at
// what a first-time reader actually receives. Several had been shipping for
// months.
//
// This file is the offline half of that gate: the things answerable from the
// repository alone, so they run on every push and in `pnpm test` rather than
// waiting for a release. The halves that need a running server or the network
// live in `scripts/check-shop-window.mjs` and are numbered steps in
// `docs/CONTRIBUTING.md` → Release process, because a check that needs the
// internet cannot be allowed to fail a build when the internet is down.
//
// What is deliberately NOT here, and why:
//
//   * "the export refuses a `/-/` route" — `scripts/export-public.sh` already
//     greps its own output. The tests below cover the tree it never sees
//     (`site/`, which is published straight to filex.sh by `sync-site.sh`
//     with no converter in the way) and the route shapes that survive the
//     export looking correct and 404 anyway.
//   * "the demo refuses writes" — needs a server. `scripts/check-shop-window.mjs
//     --instance`.
//   * "docs.filex.sh serves no private URL" — needs the network. Same script,
//     `--published`.

import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { ADVERTISED_QUERIES, REPO_ABOUT, REPO_HOMEPAGE, SCREENSHOTS } from '../../../scripts/shop-window-data.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const read = (...p: string[]) => readFileSync(path.join(REPO, ...p), 'utf8');
const has = (...p: string[]) => existsSync(path.join(REPO, ...p));

/**
 * Every text file git tracks, as repo-relative POSIX paths.
 *
 * `git ls-files` rather than a directory walk: it is the same list the export
 * rebuilds the public tree from (`git archive HEAD`), so a file that is not
 * tracked cannot reach a reader and has no business failing this suite.
 */
function trackedTextFiles(): string[] {
  const out = execFileSync('git', ['-C', REPO, 'ls-files', '-z'], {
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  });
  return out
    .split('\0')
    .filter(Boolean)
    .filter((f) => {
      const abs = path.join(REPO, f);
      if (!existsSync(abs)) return false; // deleted-but-tracked
      if (statSync(abs).size > 4 * 1024 * 1024) return false;
      return !/\.(png|jpe?g|gif|ico|webp|svg|woff2?|ttf|pdf|zip|gz|exe|dll|so|dylib|wasm|mp4|webm)$/i.test(
        f,
      );
    });
}

/** Reads a tracked file, or '' when it is not decodable as UTF-8 text. */
function textOf(rel: string): string {
  try {
    return readFileSync(path.join(REPO, rel), 'utf8');
  } catch {
    return '';
  }
}

const TRACKED = trackedTextFiles();

/**
 * Every path git tracks, binaries included.
 *
 * ⚠ `TRACKED` above drops images by extension, which is right for a text scan
 * and wrong for asking whether a screenshot exists — the first version of the
 * screenshot checks below asked `TRACKED` about a `.png` and reported all
 * sixteen pictures as untracked.
 */
const TRACKED_ALL = execFileSync('git', ['-C', REPO, 'ls-files', '-z'], {
  encoding: 'utf8',
  maxBuffer: 64 * 1024 * 1024,
})
  .split('\0')
  .filter(Boolean);

// The exporter is the only place the public URLs are spelled. It is read, not
// re-implemented — a second copy of the rules drifts and starts reporting
// things that work.
const EXPORTER = 'scripts/export-public.sh';
const exporter = has(EXPORTER) ? read(EXPORTER) : '';

// ⚠⚠ This suite runs in TWO trees. CI runs `pnpm test` on whichever checkout
// it was handed, and the published GitHub repository is a different tree: the
// export withholds `site/`, the handovers, the operational docs — and the
// exporter itself, which "names every private file, so publishing it would
// tell a reader exactly what is being withheld".
//
// So the export is where its own absence has to be understood. The checks that
// interrogate the exporter's rules are source-only; the checks that describe
// what a reader receives — the `/-/` scan above all, which is the defect that
// shipped on 104 release pages — run in BOTH, and matter more in the published
// one. Told apart by the exporter's presence, the way siteAssets.test.ts next
// door tells them apart by `site/`.
const inSource = exporter !== '';

// ───────────────────────────────────────────────────────────────────────────
// 1. URL grammar
// ───────────────────────────────────────────────────────────────────────────
//
// GitLab separates the project path from the route with `/-/`; GitHub has no
// such segment. The export rewrote the HOST and left the GRAMMAR, so
// `github.com/BRF-Tech/filex/issues` shipped as
// `github.com/<owner>/<repo>/-/issues` — a 404. (Spelled with placeholders
// on purpose: written out, this comment would trip its own check below, and the
// export refuses a tree containing one.) Measured 2026-09-07: it was
// the "Issues:" line at the bottom of **104 of the 105 published releases**,
// which is the single worst link to have broken, because a reader who wants to
// report a bug clicks it first.

describe('url grammar: a GitLab route never arrives on a GitHub host', () => {
  // ⚠ The exporter is excluded from its own scan for the same reason it
  // excludes itself from the export: it *documents* this defect, in a comment
  // that necessarily contains an example of it.
  const scanned = TRACKED.filter((f) => f !== EXPORTER);
  const strayRe = /github\.com\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+\/-\//;

  it('scans a tree, not an empty list', () => {
    // The whole suite passes vacuously if `git ls-files` ever comes back empty
    // (a detached worktree, a bad cwd). This project has already shipped two
    // gates that measured nothing; that is what this assertion is for.
    expect(scanned.length).toBeGreaterThan(500);
  });

  it('no file carries `github.com/<owner>/<repo>/-/…`', () => {
    const hits: string[] = [];
    for (const f of scanned) {
      const text = textOf(f);
      text.split('\n').forEach((line, i) => {
        if (strayRe.test(line)) hits.push(`${f}:${i + 1}: ${line.trim().slice(0, 140)}`);
      });
    }
    expect(
      hits,
      'a GitLab `/-/` route is pointing at github.com. GitHub 404s on those.\n' +
        `Teach convert() in ${EXPORTER} how to translate the route, then fix the source.\n` +
        hits.join('\n'),
    ).toEqual([]);
  });

  // The forward-looking half, and the one the export's own guard cannot do.
  //
  // The export drops `/-/` generically, so a route it has never heard of ships
  // as `github.com/BRF-Tech/filex/<route>` with no `/-/` left in it — the
  // export's grep sees a clean tree and the link is still a 404. `/-/wikis/Home`
  // becomes `/wikis/Home` (GitHub says `/wiki`), `/-/snippets/1` becomes
  // `/snippets/1` (GitHub has gists). So the routes we *use* are pinned.
  const TRANSLATABLE = new Set([
    // dropping `/-/` lands on a real GitHub route
    'issues',
    'blob',
    'tree',
    'raw',
    'releases',
    'tags',
    'commits',
    'milestones',
    // renamed outright by convert()
    'merge_requests',
    'pipelines',
  ]);

  // ⚠ Source-only, and not because of the exporter: in the PUBLISHED tree
  // every one of these links has already been rewritten, so there is nothing
  // left to classify and `used.size` would be 0. Asserting on it there would
  // report the export working correctly as a failure.
  it.skipIf(!inSource)('every GitLab route we link is one the export can translate', () => {
    const used = new Map<string, string>(); // route -> first place it is used
    for (const f of scanned) {
      for (const m of textOf(f).matchAll(/gitlab\.com\/brftech\/filemanager\/-\/([A-Za-z0-9_-]+)/g)) {
        if (!used.has(m[1]!)) used.set(m[1]!, f);
      }
    }
    expect(used.size, 'no GitLab project links at all — has the project path changed?').toBeGreaterThan(0);
    const unknown = [...used].filter(([r]) => !TRANSLATABLE.has(r));
    expect(
      unknown.map(([r, f]) => `${r} (${f})`),
      'these GitLab routes have no known GitHub equivalent. The export drops `/-/` ' +
        'generically, so they ship looking correct and 404 anyway — the export’s own ' +
        `grep cannot see them. Add a rename to convert() in ${EXPORTER} and to TRANSLATABLE here.`,
    ).toEqual([]);
  });

  it.skipIf(!inSource)('the two renamed routes are still renamed by the exporter', () => {
    // Cross-check: TRANSLATABLE claims `merge_requests` and `pipelines` are
    // handled *by a rename*. If that table is ever deleted from convert() they
    // would ship as `/merge_requests` and `/pipelines`, which GitHub 404s, and
    // this list would be quietly wrong.
    expect(exporter, `${EXPORTER} is missing`).not.toBe('');
    expect(exporter).toMatch(/'merge_requests',\s*'pulls'/);
    expect(exporter).toMatch(/'pipelines',\s*'actions'/);
  });
});

// ───────────────────────────────────────────────────────────────────────────
// 2. The first command a stranger runs
// ───────────────────────────────────────────────────────────────────────────
//
// The README's headline `docker run` mounted `$(pwd)/data` at `/data`. `/data`
// is filex's OWN directory — SQLite database, search index, thumbnail cache —
// so the reader's first minute was an empty file manager with their files
// sitting next to a database. The compose file two directories away had it
// right the whole time. Nothing compared them.

interface DockerRun {
  where: string;
  image: string;
  ports: string[];
  volumes: string[];
  env: Record<string, string>;
}

/** The first `docker run` in a fenced block, unwrapped from its backslashes. */
function firstDockerRun(rel: string): DockerRun {
  const text = read(rel);
  const m = text.match(/docker run[\s\S]*?ghcr\.io\/[^\s`]+/);
  if (!m) throw new Error(`${rel} has no \`docker run … ghcr.io/…\` block`);
  const cmd = m[0].replace(/\\\r?\n/g, ' ').replace(/\s+/g, ' ').trim();
  const tokens = cmd.split(' ');
  const run: DockerRun = { where: rel, image: tokens[tokens.length - 1]!, ports: [], volumes: [], env: {} };
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    const val = (tokens[i + 1] ?? '').replace(/^["']|["']$/g, '');
    if (t === '-p') run.ports.push(val);
    else if (t === '-v') run.volumes.push(val);
    else if (t === '-e') {
      const eq = val.indexOf('=');
      if (eq > 0) run.env[val.slice(0, eq)] = val.slice(eq + 1);
    }
  }
  return run;
}

describe('the quickstart command matches the compose file it is a shorthand for', () => {
  const COMPOSE = ['deploy', 'compose', 'docker-compose.minimal.yml'];
  const compose = read(...COMPOSE);

  const composeImage = compose.match(/image:\s*(\S+)/)?.[1];
  const composePort = compose.match(/-\s*"(\d+:\d+)"/)?.[1];
  const composeStoragePath = compose.match(/FILEX_DEFAULT_STORAGE_PATH:\s*(\S+)/)?.[1];
  const composeDataVolume = compose.match(/-\s*([A-Za-z0-9_-]+):\/data\b/)?.[1];

  it('the compose file itself is the shape we are comparing against', () => {
    // If this ever stops parsing, every `it` below would compare against
    // `undefined` and agree with anything.
    expect(composeImage, 'no image: line').toBeTruthy();
    expect(composePort, 'no published port').toBeTruthy();
    expect(composeStoragePath, 'no FILEX_DEFAULT_STORAGE_PATH').toBeTruthy();
    expect(
      composeDataVolume,
      '/data must be a NAMED volume in the compose file too — it is the database directory',
    ).toBeTruthy();
    expect(compose).toMatch(new RegExp(`^volumes:[\\s\\S]*^\\s+${composeDataVolume}:`, 'm'));
  });

  const runs = [firstDockerRun('README.md'), firstDockerRun('docs/INSTALLATION.md')];

  it.each(runs.map((r) => [r.where, r] as const))(
    '%s: the headline docker run agrees with the compose file',
    (_where, run) => {
      expect(run.image, 'a different image than the compose file installs').toBe(composeImage);
      expect(run.ports, 'a different published port than the compose file').toContain(composePort);

      const dataMount = run.volumes.find((v) => v.endsWith(':/data'));
      expect(dataMount, 'the command mounts nothing at /data — the reader loses their data on `docker rm`').toBeTruthy();
      const dataSource = dataMount!.slice(0, -':/data'.length);
      expect(
        /^[A-Za-z0-9_-]+$/.test(dataSource),
        `-v ${dataMount} binds a HOST path at /data. /data is filex's own directory ` +
          '(SQLite database, search index, thumbnail cache), not where the reader’s files go: ' +
          'this is the exact command that dropped a first-time reader into an empty file manager ' +
          `with their files in the database directory. Use the named volume the compose file uses (${composeDataVolume}:/data).`,
      ).toBe(true);

      // …and the reader's own files have to arrive somewhere, seeded as a
      // storage, or the first screen says "No storage configured".
      expect(
        run.env.FILEX_DEFAULT_STORAGE_DRIVER,
        'without the storage seed the first screen is "No storage configured"',
      ).toBe('local');
      const seeded = run.env.FILEX_DEFAULT_STORAGE_PATH;
      expect(seeded, 'FILEX_DEFAULT_STORAGE_PATH is missing').toBe(composeStoragePath);
      expect(
        run.volumes.some((v) => v.endsWith(`:${seeded}`)),
        `nothing is mounted at ${seeded}, so the seeded storage points at an empty directory inside the container`,
      ).toBe(true);
    },
  );
});

// ───────────────────────────────────────────────────────────────────────────
// 3. filex.sh: published with no converter in the way
// ───────────────────────────────────────────────────────────────────────────
//
// `site/` is a private_dir — the public export never sees it — and it is also
// the source of filex.sh, uploaded verbatim by `scripts/sync-site.sh`. So it
// is the one publish path where a link to the private repository reaches a
// public page with nothing at all standing in between. This has already
// happened once in pictures (`site/assets/admin-plugins.png` carried a
// `github.com/brf-tech/filex` footer on the marketing page for two
// releases, which is what `siteAssets.test.ts` next door is for); the prose
// has never been checked.

const SITE = path.join(REPO, 'site');
const sitePresent = existsSync(SITE);

// ⚠⚠ UNCONDITIONAL, and it is here because the author of this file found the
// trap and then left one of its own next door.
//
// `describe.skipIf` skips every `it` inside it — including the "the list is not
// empty" assertions written to stop those blocks passing vacuously. A guard
// inside the block it guards is not a guard: whatever empties the list is the
// same condition that switches the guard off. Measured 2026-09-07 across the
// suite: in the PUBLISHED tree — the one CI runs — this file reports 8 of its
// 17 tests and `siteAssets.test.ts` next door reports 0 of 8, both green.
//
// So the shape of the checkout is asserted first, and asserted as ONE FACT
// rather than as two independent `existsSync` calls. The export withholds
// `site/` and `scripts/export-public.sh` together, so a tree holding exactly
// one of them is neither the source nor the published product: the skips below
// would then be hiding a broken checkout instead of describing an intended one.
it('this checkout is coherently the source tree or the published one', () => {
  expect(
    sitePresent,
    inSource
      ? 'scripts/export-public.sh is here, so this is the source tree — but site/ is missing, ' +
        'and the site checks below would skip and report nothing'
      : 'the exporter is absent, so this is the published tree — but site/ is here, and it is a ' +
        'private_dir that should have been withheld along with it',
  ).toBe(inSource);
});

describe.skipIf(!sitePresent)('site/ carries nothing that names the private repo', () => {
  const SYNC = 'scripts/sync-site.sh';

  /**
   * Exactly what sync-site.sh uploads: site/**, minus two repo-only kinds.
   *
   * ⚠ `describe.skipIf` still runs this callback — it only marks the results
   * skipped — so a throw here does not skip the block, it collapses the whole
   * FILE to "no tests" with an ENOENT on stderr that nothing fails on. Hence
   * the existence guard as well as the skipIf.
   */
  function published(dir = SITE, prefix = 'site'): string[] {
    if (!existsSync(dir)) return [];
    const out: string[] = [];
    for (const name of readdirSync(dir)) {
      const abs = path.join(dir, name);
      const rel = `${prefix}/${name}`;
      if (statSync(abs).isDirectory()) out.push(...published(abs, rel));
      else if (name !== 'README.md' && !name.endsWith('.src.html')) out.push(rel);
    }
    return out;
  }

  it('the exclusion list here is the one the deploy script uses', () => {
    // If sync-site.sh ever starts uploading README.md, this test would be
    // scanning a smaller set than the one that ships and would not say so.
    const sync = read(SYNC);
    expect(sync).toMatch(/--exclude='\*\.src\.html'/);
    expect(sync).toMatch(/--exclude='README\.md'/);
  });

  const files = published().filter((f) => /\.(html|css|js|mjs|json|txt|xml|webmanifest)$/i.test(f));

  it('there are published text files to scan', () => {
    // A list that quietly went empty is the failure mode `it.each` cannot
    // report: zero cases reads as zero problems.
    //
    // ⚠ This one stays inside the block on purpose, and it is not the guard —
    // the guard is the unconditional coherence test above. Here it is only the
    // second half: site/ exists AND has something in it worth scanning.
    expect(files.length).toBeGreaterThan(0);
  });

  it.each(files)('%s names no private repository', (rel) => {
    const text = textOf(rel);
    const bad = [
      /gitlab\.com\/brftech/,
      /registry\.gitlab\.com/,
      /git@gitlab\.com:/,
      /github\.com\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+\/-\//,
    ].filter((re) => re.test(text));
    expect(
      bad.map(String),
      `${rel} is uploaded to filex.sh verbatim — sync-site.sh runs no rewrite, ` +
        'so whatever is written here is what the public reads.',
    ).toEqual([]);
  });
});

// ───────────────────────────────────────────────────────────────────────────
// 4. Nothing private survives the export
// ───────────────────────────────────────────────────────────────────────────
//
// docs.filex.sh was built from the PRIVATE tree, so it published
// `github.com/brf-tech/filex/backend/pkg/pluginsdk` as the import path for
// plugin authors: a module that does not compile, in a repository they cannot
// reach. The release step now pushes the site's prose from the export.
//
// Two different failures live here and only one of them is offline:
//
//   a) somebody publishes from the wrong tree  → only the live site can prove
//      it. `scripts/check-shop-window.mjs --published`.
//   b) somebody writes a private name the exporter has never been taught to
//      convert, into a file the export ships → provable here, and nothing
//      checks it today. The project's own domain is rewritten wherever it
//      appears; a bare IP address is not, and `docs/DEPLOY_BRF.md` is full of
//      them — it is withheld, which is the only reason that is safe.

describe.skipIf(!inSource)('the public tree the export builds carries nothing private', () => {
  /**
   * The `private_files=(…)` / `private_dirs=(…)` lists, read from the script.
   *
   * ⚠ Both spellings are in use: `private_files` is one entry per line with
   * comments between them (whose prose contains brackets), `private_dirs` is a
   * single line. Closing on "the first `)`" mangles the first, and closing on
   * "a `)` in column 0" runs the second one past the end of the array and into
   * the rest of the file — which is how this parser first read 63 private
   * directories and still looked like it worked.
   */
  function bashArray(name: string): string[] {
    // Same trap as `published()` above: this runs even when the block is
    // skipped, so in the published tree — which has no exporter — it must
    // return an empty list rather than throw.
    if (!inSource) return [];
    const open = `${name}=(`;
    const at = exporter.indexOf(open);
    if (at < 0) throw new Error(`${EXPORTER} has no ${name}=( … ) array`);
    const after = exporter.slice(at + open.length);
    const nl = after.indexOf('\n');
    const paren = after.indexOf(')');
    const body =
      paren >= 0 && (nl < 0 || paren < nl) ? after.slice(0, paren) : after.match(/^([\s\S]*?)\n\)/)![1]!;
    return body
      .split('\n')
      .map((l) => l.replace(/#.*$/, '').trim())
      .filter(Boolean)
      .flatMap((l) => l.split(/\s+/));
  }

  const privateFiles = new Set(bashArray('private_files'));
  const privateDirs = bashArray('private_dirs');

  /** Contact addresses convert() deliberately preserves — they must reach us. */
  const keepAddresses = [...exporter.matchAll(/'([A-Za-z0-9._%+-]+@brf\.sh)'/g)].map((m) => m[1]!);

  it('the lists were parsed, not guessed', () => {
    expect(privateFiles.has('docs/DEPLOY_BRF.md')).toBe(true);
    expect(privateDirs).toContain('site');
    expect(keepAddresses.length).toBeGreaterThan(0);
  });

  const exported = TRACKED.filter(
    (f) =>
      !privateFiles.has(f) &&
      !privateDirs.some((d) => f === d || f.startsWith(`${d}/`)) &&
      !/^HANDOVER-.*\.md$/.test(f),
  );

  it('the export ships most of the tree', () => {
    expect(exported.length).toBeGreaterThan(TRACKED.length * 0.8);
  });

  // Names that must never reach a public reader, and are NOT covered by
  // convert(). Host names under the project's own domain are deliberately
  // absent from this list: convert() rewrites that whole domain, so they are
  // safe by construction — which is precisely why a bare IP address, which it
  // does not touch, is the dangerous form.
  const unconverted: Array<[RegExp, string]> = [
    [/\b(?:167\.235\.143\.222|185\.11\.248\.136|100\.96\.0\.\d+|192\.168\.0\.\d+)\b/, 'a private/production IP address'],
    [/\bpass\.brf\.sh\/app\/passwords\//, 'a link into the credential vault'],
  ];

  it.each(unconverted.map(([re, what]) => [what, re] as const))(
    'no exported file carries %s',
    (what, re) => {
      const hits: string[] = [];
      for (const f of exported) {
        textOf(f)
          .split('\n')
          .forEach((line, i) => {
            if (re.test(line)) hits.push(`${f}:${i + 1}`);
          });
      }
      expect(
        hits,
        `${what} in a file the export publishes. convert() does not rewrite it, so it ` +
          'ships verbatim to GitHub. Either withhold the file (private_files in ' +
          `${EXPORTER}) or take the name out.`,
      ).toEqual([]);
    },
  );

});

// The runbook assertion, deliberately outside the block above: it is about a
// page the export publishes, so it is answerable in both trees.
//
// It is the only offline evidence that the site's prose is still being pushed
// from the export, and it is worth having precisely because it is the step
// that was silently missing — nothing scripted copies `docs/` to the server,
// so this sentence in CONTRIBUTING is the whole mechanism.
describe('the release runbook', () => {
  it('still pushes the site prose from the export', () => {
    const c = read('docs', 'CONTRIBUTING.md');
    const step = c.slice(c.indexOf('docs.filex.sh'));
    const tar = step.match(/^\s*tar czf - docs README\.md CHANGELOG\.md.*$/m);
    expect(tar, 'the step that copies docs/ to the site has gone').toBeTruthy();
    const before = step.slice(0, step.indexOf(tar![0]));
    expect(
      /cd\s+\S*filex-export/.test(before),
      'the docs are tarred from whatever directory the reader happens to be in. ' +
        'The source tree carries the private module path — pushed from there, docs.filex.sh ' +
        'hands plugin authors an import path that does not compile. `cd /g/filex-export` first.',
    ).toBe(true);
  });
});

// ───────────────────────────────────────────────────────────────────────────
// 5. The demo's advertised searches
// ───────────────────────────────────────────────────────────────────────────
//
// The demo splash told visitors what to type and both examples returned
// nothing — on the most likely first interaction a visitor has. Whether they
// still find anything is a question for a running server
// (`scripts/check-shop-window.mjs --instance`); what is answerable here is
// that the script and the product are still talking about the same strings.

describe('the advertised search queries are the ones the product prints', () => {
  const locales = ['en', 'tr'];

  it.each(ADVERTISED_QUERIES.map((q) => [q.query, q] as const))(
    '“%s” still appears in the demo copy',
    (_q, entry) => {
      const shown = locales.map((l) => JSON.parse(read('web', 'src', 'locales', `${l}.json`)));
      for (const [i, l] of locales.entries()) {
        const body: string = shown[i]?.demo?.features?.searchBody ?? '';
        expect(
          body.includes(entry.query),
          `${l}.json demo.features.searchBody no longer offers “${entry.query}”. ` +
            'Update ADVERTISED_QUERIES in scripts/shop-window-data.mjs with it, or the ' +
            'instance check goes on proving a string nobody is shown.',
        ).toBe(true);
        expect(
          body.includes(entry.finds.split('/').pop()!),
          `${l}.json no longer names ${entry.finds}, which “${entry.query}” promises`,
        ).toBe(true);
      }
    },
  );
});

// ───────────────────────────────────────────────────────────────────────────
// 6. The pictures the README shows
// ───────────────────────────────────────────────────────────────────────────
//
// Whether a screenshot is STALE is a git question and lives in
// `scripts/check-shop-window.mjs` — it belongs to a release, not to a push,
// because the fix is to re-run the capture and that needs a binary and a
// browser. What belongs here is the half that keeps that check honest: the
// declaration it reads must still describe this repository.
//
// ⚠ `SCREENSHOTS[].depicts` is the one thing in the whole gate a person has to
// know — git cannot work out which component draws which picture. A hand-written
// map rots in exactly two ways and both are silent: a new picture is added to
// the README and nobody declares it (so it is never checked), or a component is
// renamed and the path matches nothing (so the picture looks eternally fresh).
// Both are assertions here.

describe('every README screenshot declares what it shows', () => {
  const trackedSet = new Set(TRACKED_ALL);
  const shownInReadme = [
    ...new Set([...read('README.md').matchAll(/docs\/screenshots\/[A-Za-z0-9_./-]+\.png/g)].map((m) => m[0])),
  ];

  it('the README still shows screenshots at all', () => {
    // Without this the two `it.each` below are empty and agree with anything.
    expect(shownInReadme.length).toBeGreaterThan(5);
    expect(SCREENSHOTS.length).toBeGreaterThan(5);
  });

  it.each(shownInReadme)('%s is declared in SCREENSHOTS', (rel) => {
    expect(
      SCREENSHOTS.some((s: { file: string }) => s.file === rel),
      `${rel} is in the README and not in SCREENSHOTS (scripts/shop-window-data.mjs), so nothing ` +
        'will ever notice it going out of date. Add it with the sources of what is in the picture.',
    ).toBe(true);
  });

  it.each(SCREENSHOTS.map((s: { file: string }) => s.file))('%s is still a tracked file', (rel) => {
    expect(trackedSet.has(rel), `${rel} is declared in SCREENSHOTS but git does not track it`).toBe(true);
  });

  it.each(SCREENSHOTS.map((s: { file: string; depicts: string[] }) => [s.file, s] as const))(
    '%s depicts paths that still exist',
    (_file, shot) => {
      for (const dep of shot.depicts) {
        const hit = trackedSet.has(dep) || TRACKED_ALL.some((f) => f.startsWith(`${dep}/`));
        expect(
          hit,
          `${shot.file} says it depicts ${dep}, and git tracks nothing there. A renamed component ` +
            'makes the staleness check compare against nothing and report the picture as fresh ' +
            'for ever. Point it at the file that draws this picture now.',
        ).toBe(true);
      }
    },
  );
});

// ───────────────────────────────────────────────────────────────────────────
// 7. The About blurb — the shop window with no deploy step
// ───────────────────────────────────────────────────────────────────────────
//
// GitHub prints `description` under the repository name, in search results and
// on every link preview: for most readers it is the first sentence of the
// product, read before the README. It is typed into a settings form and lives
// only in GitHub's database — grepped 2026-09-07, not one character of the
// published text appeared anywhere in this tree — so nothing has ever reviewed
// it, and it drifted: five storage drivers when the product had six.
//
// `REPO_ABOUT` is the source of truth that did not exist. The live comparison
// is in `check-shop-window.mjs --published`; what is answerable here is that
// the constant is a sentence GitHub will accept and that it still describes
// this repository.

describe('the About blurb this repository claims', () => {
  it('fits in the box GitHub gives it', () => {
    // 350 is GitHub's limit; a longer one is silently truncated mid-word.
    expect(REPO_ABOUT.length).toBeGreaterThan(80);
    expect(REPO_ABOUT.length, `${REPO_ABOUT.length} characters; GitHub truncates at 350`).toBeLessThanOrEqual(350);
  });

  it('names every storage driver the product registers', () => {
    // ⚠ Read from the driver registry, not from a list here — this is the
    // assertion that would have caught the published blurb naming five drivers
    // after SMB shipped. A driver arrives as a `storage.Register("…")` call and
    // the shop window has to learn about it in the same commit.
    const drivers = [
      ...new Set(
        TRACKED.filter((f) => f.startsWith('backend/internal/storage/drivers/'))
          .flatMap((f) => [...textOf(f).matchAll(/storage\.Register\("([a-z0-9]+)"/g)])
          .map((m) => m[1]!),
      ),
    ].sort();

    expect(drivers.length, 'no storage drivers found — the registry call has been renamed and this test now proves nothing').toBeGreaterThan(3);

    const lower = REPO_ABOUT.toLowerCase();
    const unmentioned = drivers.filter((d) => !lower.includes(d));
    expect(
      unmentioned,
      `the About blurb does not name ${unmentioned.join(', ')}, and the backend registers ${drivers.join(', ')}. ` +
        'The first sentence a stranger reads is undercounting the product. Update REPO_ABOUT in ' +
        'scripts/shop-window-data.mjs and paste it into the repository settings.',
    ).toEqual([]);
  });

  it('is punctuated the way every other surface is', () => {
    // ⚠ Not "starts with the product name": GitHub prints the repository name
    // directly above the blurb, so opening with `filex` again spends a sentence
    // on nothing, and an assertion demanding it would be this file's taste
    // rather than a defect. The defect was `filex - self-hosted` — a spaced
    // ASCII hyphen where the README, the site, the locale catalogue and every
    // package.json use an em dash. Hyphenated words are untouched, which is why
    // it is the spaces on both sides that are matched.
    expect(
      / - /.test(REPO_ABOUT),
      'REPO_ABOUT separates a clause with " - ". Every other surface uses " — ".',
    ).toBe(false);
    expect(REPO_HOMEPAGE).toMatch(/^https:\/\//);
  });

  it('carries no private URL of its own', () => {
    for (const re of [/gitlab\.com\/brftech/, /registry\.gitlab\.com/, /github\.com\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+\/-\//]) {
      expect(re.test(REPO_ABOUT), `REPO_ABOUT matches ${re}`).toBe(false);
    }
  });
});
