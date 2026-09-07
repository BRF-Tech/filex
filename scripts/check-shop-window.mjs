#!/usr/bin/env node
// The shop window, checked where it actually is: on a running server and on
// the published product.
//
// ⚠⚠ On 2026-09-07, hours before a public launch, a person looking at filex
// from outside found seven defects. Not one had been caught by a test, a lint,
// or any of the eleven steps of the release process, and several had been
// shipping for months:
//
//   * a dead `Issues` link on 104 of 105 published release pages
//   * a public demo that answered all 101 admin routes with no refusal
//   * `GET /api/files/capabilities` handing anonymous callers the operator's
//     internal hostname
//   * docs.filex.sh built from the PRIVATE tree, publishing a Go import path
//     that names an unreachable repository and does not compile
//   * release bodies that were a commit hash where the changelog had prose
//   * a headline `docker run` that dropped the reader into an empty file
//     manager with their files in the database directory
//   * the demo's own advertised search query returning zero results
//
// The pattern: everything a stranger touches first is the least tested surface
// in the project. The half of that gate answerable from the repository alone
// lives in `web/tests/deploy/shopWindow.test.ts` and runs on every push. This
// script is the rest, split by what each check NEEDS:
//
//   --instance   needs a server. Demo mode refuses writes; an anonymous
//                capabilities call names no host, for every external service;
//                the advertised search grammar returns what it promises.
//   --published  needs the network. Release pages carry no dead links;
//                docs.filex.sh serves the current build and no private URL;
//                the DEMO's own corpus answers the queries it advertises;
//                GitHub's About box and filex.sh say what this repository says.
//   (always)     needs only git. The pictures the README shows have not been
//                left behind by the code that draws them.
//
//   node scripts/check-shop-window.mjs --instance --boot bin/filex
//   node scripts/check-shop-window.mjs --instance http://127.0.0.1:5941
//   node scripts/check-shop-window.mjs --published
//   node scripts/check-shop-window.mjs --instance --boot bin/filex --published
//
// The exhaustive half of the demo check is a Go test, because chi's route table
// is the only place the routes are knowable and it is not reachable from here:
// `backend/internal/api/shop_window_route_table_test.go` walks all 359 of them,
// classifies each by asking the server, and fails the build the day an operator
// surface appears at a prefix the guard has not heard of. The six routes probed
// from this script are the smoke test that the guard is installed at all.
//
// Exit codes — and the difference between the last two is the whole point:
//
//   0   everything checked passed
//   1   CHECKED AND WRONG — a defect is present. Fail the release.
//   2   COULD NOT CHECK — no server, no network, a rate limit, a fixture that
//       is not set up. ⚠ A gate that turns an outage into a failed build is an
//       outage of its own, so this is deliberately NOT 1. It is also not 0:
//       the release step says out loud that the check did not run.

import { execFileSync, spawn } from 'node:child_process';
import { createServer } from 'node:net';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  ADVERTISED_QUERIES,
  DEMO_ALLOWED_READS,
  DEMO_CREDENTIAL_FIELDS,
  DEMO_GUARDED_WRITES,
  EXTERNAL_SENTINELS,
  REPO_ABOUT,
  REPO_HOMEPAGE,
  SCREENSHOTS,
  SITE_MUST_LINK,
} from './shop-window-data.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

// ── argument parsing ────────────────────────────────────────────────────────
const argv = process.argv.slice(2);
const flag = (name) => argv.includes(name);
const value = (name, dflt) => {
  const i = argv.indexOf(name);
  return i >= 0 && argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[i + 1] : dflt;
};

if (flag('--help') || flag('-h') || argv.length === 0) {
  // The header IS the help, read to the first line that is not a comment.
  // ⚠ It used to be `.slice(1, 44)`, and the moment this header grew the help
  // stopped in the middle of a sentence — a hard-coded line number in a file
  // that documents itself is a comment that lies as soon as somebody edits it.
  const lines = fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n').slice(1);
  const end = lines.findIndex((l) => !l.startsWith('//'));
  console.log(lines.slice(0, end < 0 ? lines.length : end).join('\n').replace(/^\/\/ ?/gm, ''));
  process.exit(0);
}

const wantInstance = flag('--instance');
const wantPublished = flag('--published');
// A pinned, unusual port. NOT 5212: that is the default every filex on the
// machine already listens on, and a check that silently interviewed the
// developer's own running server would pass or fail for reasons that have
// nothing to do with the release.
const PORT = Number(value('--port', '5941'));
const bootBin = value('--boot', null);
const instanceURL = argv.find((a) => /^https?:\/\//.test(a)) ?? null;

// ── reporting ───────────────────────────────────────────────────────────────
//
// Three outcomes, never two. `skip` is not a pass: it is recorded, printed,
// and turns the exit code into 2 unless something already failed.
const results = [];
const pass = (what, detail = '') => results.push({ state: 'ok', what, detail });
const fail = (what, detail) => results.push({ state: 'FAIL', what, detail });
const skip = (what, detail) => results.push({ state: 'skip', what, detail });

/** A fetch whose transport failures are told apart from its answers. */
async function get(url, init = {}) {
  try {
    const res = await fetch(url, { redirect: 'follow', ...init, signal: AbortSignal.timeout(20_000) });
    return { res };
  } catch (e) {
    return { unreachable: `${e.name}: ${e.message}` };
  }
}

// ════════════════════════════════════════════════════════════════════════════
// Against a running instance
// ════════════════════════════════════════════════════════════════════════════

// The sentinels are what make two of these checks non-vacuous.
//
// ⚠⚠ A fresh filex has no external services configured, so `external.*.url` is
// the empty string and "the anonymous answer contains no hostname" is true of
// an instance that would leak one the moment an operator configured it. The
// booted instance is therefore given a hostname on purpose, and the check
// first proves an AUTHENTICATED caller can see it. If it cannot, the fixture
// is broken and the result is `skip`, never `ok`.
//
// ⚠ One sentinel is not enough, which is why EXTERNAL_SENTINELS has three.
// `redactExternalHosts` does two different things — it walks the `external` map
// AND blanks three flat aliases by name — so a single seeded service exercised
// the loop and left `drawio_url` and `convert_url` unproved. A redaction that
// dropped one alias, or a fourth service arriving with no alias entry, would
// have passed. See handlers/capabilities.go.
const SENTINEL_ENV = Object.fromEntries(EXTERNAL_SENTINELS.map((s) => [s.env, `https://${s.host}:9443`]));
const ADMIN_EMAIL = 'gate@shop.window';
const ADMIN_PASSWORD = 'shop-window-gate-pw-9941';

async function freePort(preferred) {
  const busy = await new Promise((resolve) => {
    const s = createServer();
    s.once('error', () => resolve(true));
    s.once('listening', () => s.close(() => resolve(false)));
    s.listen(preferred, '127.0.0.1');
  });
  return busy ? null : preferred;
}

/**
 * Boot a throwaway instance in demo mode and hand back its base URL.
 *
 * Everything it needs is passed as environment: `FILEX_LISTEN` — ⚠ there is no
 * `FILEX_ADDR`, and a typo there does not fail, it silently leaves the server
 * on :5212 where you are then interviewing somebody else's process.
 */
async function boot(bin) {
  const abs = path.isAbsolute(bin) ? bin : path.join(REPO, bin);
  const exe = fs.existsSync(abs) ? abs : fs.existsSync(`${abs}.exe`) ? `${abs}.exe` : null;
  if (!exe) return { skip: `no filex binary at ${abs} — build one with \`pnpm run build:backend\`` };

  const port = await freePort(PORT);
  if (!port) return { skip: `port ${PORT} is already in use; pass --port to pick another` };

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-shop-window-'));
  const files = path.join(dir, 'files');
  fs.mkdirSync(files, { recursive: true });

  const child = spawn(exe, ['serve'], {
    env: {
      ...process.env,
      FILEX_LISTEN: `127.0.0.1:${port}`,
      FILEX_DATA_DIR: path.join(dir, 'data'),
      FILEX_PUBLIC_URL: `http://127.0.0.1:${port}`,
      FILEX_DEMO_MODE: 'true',
      FILEX_ADMIN_EMAIL: ADMIN_EMAIL,
      FILEX_ADMIN_PASSWORD: ADMIN_PASSWORD,
      FILEX_DEFAULT_STORAGE_DRIVER: 'local',
      FILEX_DEFAULT_STORAGE_PATH: files,
      ...SENTINEL_ENV,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let log = '';
  child.stdout.on('data', (b) => (log += b));
  child.stderr.on('data', (b) => (log += b));

  const base = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 150; i++) {
    if (child.exitCode !== null) {
      return { skip: `the instance exited with code ${child.exitCode}:\n${log.slice(-2000)}` };
    }
    const { res } = await get(`${base}/api/files/capabilities`);
    if (res?.ok) {
      // ⚠ Waiting for the process to actually exit, not just signalling it:
      // on Windows the SQLite file stays locked for as long as the process
      // lives, and the cleanup below then throws EBUSY *over the top of the
      // results* — a check whose findings are destroyed by its own teardown.
      const stop = () =>
        new Promise((resolve) => {
          if (child.exitCode !== null) return resolve();
          child.once('exit', resolve);
          child.kill();
          setTimeout(resolve, 5000);
        });
      return { base, stop, dir };
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  child.kill();
  return { skip: `the instance never answered on ${base}:\n${log.slice(-2000)}` };
}

/** Sign in, returning the session cookie header. */
async function signIn(base) {
  const { res, unreachable } = await get(`${base}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
  });
  if (unreachable) return { unreachable };
  if (!res.ok) return { unreachable: `login answered ${res.status}` };
  const setCookie = res.headers.getSetCookie?.() ?? [];
  const jar = setCookie.map((c) => c.split(';')[0]).join('; ');
  const body = await res.json().catch(() => ({}));
  return { cookie: jar, token: body.token };
}

async function checkInstance(base, owned) {
  // ── is this instance ours? ────────────────────────────────────────────────
  //
  // ⚠ Two of the checks below WRITE (they seed files to search for) and the
  // rest interrogate an admin surface. Doing that to a server that belongs to
  // somebody else is not a check, it is an incident.
  const host = new URL(base).hostname;
  if (!owned && !['127.0.0.1', 'localhost', '::1'].includes(host)) {
    skip('instance', `${base} is not loopback. This check writes; point it at a throwaway you booted.`);
    return;
  }

  const auth = await signIn(base);
  if (auth.unreachable) {
    skip('instance', `could not sign in at ${base}: ${auth.unreachable}`);
    return;
  }
  const asAdmin = { headers: { Cookie: auth.cookie } };

  // ── 1. an anonymous caller is told WHETHER, never WHERE ───────────────────
  {
    const signedIn = await get(`${base}/api/files/capabilities`, asAdmin);
    const anon = await get(`${base}/api/files/capabilities`);
    if (signedIn.unreachable || anon.unreachable) {
      skip('capabilities', signedIn.unreachable ?? anon.unreachable);
    } else {
      const signedInBody = await signedIn.res.text();
      const anonBody = await anon.res.text();

      // The fixture first, service by service. An instance that was given no
      // hostname for `drawio` proves nothing about drawio's redaction, and
      // saying so is the difference between `skip` and a false `ok`.
      const unseeded = EXTERNAL_SENTINELS.filter((s) => !signedInBody.includes(s.host));
      const leaked = EXTERNAL_SENTINELS.filter((s) => anonBody.includes(s.host));

      if (unseeded.length) {
        skip(
          'capabilities',
          `${unseeded.length} of ${EXTERNAL_SENTINELS.length} external services carry no host on this instance ` +
            `(${unseeded.map((s) => `${s.name}: looked for ${s.host}`).join(', ')}), so "the anonymous answer names ` +
            'no host" would be true of an instance that leaks. Boot with --boot, or set ' +
            `${unseeded.map((s) => s.env).join(', ')} before starting it.`,
        );
      } else if (leaked.length) {
        fail(
          'capabilities',
          `GET /api/files/capabilities answered an ANONYMOUS caller with ${leaked.length} operator hostname(s): ` +
            `${leaked.map((s) => `${s.name} → ${s.host}`).join(', ')}. ` +
            'Those are the operator\'s internal addresses, handed to anyone with curl. ' +
            'Embedders probe this endpoint before they log in, so it stays public — but the ' +
            'hostnames must travel with the credential. See handlers/capabilities.go → redactExternalHosts.',
        );
      } else {
        const j = JSON.parse(anonBody);

        // The list-free half, and the one that covers a service nobody has
        // added yet. `mermaid` is already in the payload on demo.filex.sh and
        // has no environment variable and no flat alias, so no sentinel can
        // reach it: the property that DOES reach it is that redaction leaves
        // no `url` key anywhere under `external`.
        const external = j.external && typeof j.external === 'object' ? j.external : null;
        const withURL = external
          ? Object.entries(external).filter(([, v]) => v && typeof v === 'object' && 'url' in v)
          : [];
        // …and the three flat aliases, which are a separate code path.
        const aliasLeaks = EXTERNAL_SENTINELS.filter((s) => s.alias in j && j[s.alias] !== '');

        if (!external || Object.keys(external).length === 0) {
          fail('capabilities', 'the anonymous answer has no `external` block at all — the redaction removed the feature flags an embedder probes for, not just the hosts');
        } else if (withURL.length) {
          fail(
            'capabilities',
            `${withURL.length} service(s) under \`external\` still carry a \`url\` key for an anonymous caller ` +
              `(${withURL.map(([k]) => k).join(', ')}). No sentinel was needed to see this and none would have ` +
              'caught it: these are the services with no environment variable, so a check that only looks for ' +
              'seeded hostnames is blind to exactly the ones added last.',
          );
        } else if (aliasLeaks.length) {
          fail(
            'capabilities',
            `the flat alias(es) ${aliasLeaks.map((s) => `${s.alias}="${j[s.alias]}"`).join(', ')} are non-empty for an ` +
              'anonymous caller. `redactExternalHosts` blanks these by name, one line each — a service added ' +
              'without its line here goes out with the host attached.',
          );
        } else {
          const flags = Object.keys(external).length;
          pass(
            'capabilities',
            `${EXTERNAL_SENTINELS.length} seeded hosts withheld, ${flags} feature flags still answered, ` +
              'no `url` anywhere in the anonymous payload',
          );
        }
      }
    }
  }

  // ── 2. demo mode refuses writes ───────────────────────────────────────────
  //
  // ⚠ Signed in as an administrator, because that is the situation: a demo
  // publishes its admin login on purpose, so "admin-only" and "public" are the
  // same set of people there. An unauthenticated probe would be answered 401
  // by an instance with no guard at all and the check would pass vacuously.
  {
    const refused = [];
    const allowed = [];
    let broken = null;
    for (const { method, path: p, why } of DEMO_GUARDED_WRITES) {
      const r = await get(`${base}${p}`, {
        method,
        headers: { ...asAdmin.headers, 'Content-Type': 'application/json' },
        // ⚠ Deliberately invalid. A working guard answers at the mount point
        // and the handler never sees this; a guard that has been removed
        // answers 400 rather than performing the write. That is what keeps
        // this check inert against the very instance it is testing.
        body: JSON.stringify({ __shop_window_probe: true }),
      });
      if (r.unreachable) {
        broken = r.unreachable;
        break;
      }
      const body = await r.res.text();
      if (r.res.status === 403 && body.includes('read-only')) refused.push(`${method} ${p}`);
      else allowed.push(`${method} ${p} → ${r.res.status} (${why})`);
    }
    if (broken) skip('demo writes', broken);
    else if (allowed.length) {
      fail(
        'demo writes',
        `a signed-in visitor was NOT refused on ${allowed.length} of ${DEMO_GUARDED_WRITES.length} routes:\n` +
          allowed.map((a) => `      ${a}`).join('\n') +
          '\n    On a public demo the admin account is published, so this is every visitor. ' +
          'See backend/internal/api/demo_guard.go.',
      );
    } else {
      // The other half: a guard that refused everything would be "safe" and
      // would also mean there is nothing left to demonstrate.
      const readable = [];
      for (const p of DEMO_ALLOWED_READS) {
        const r = await get(`${base}${p}`, asAdmin);
        if (!r.unreachable && r.res.status === 200) readable.push(p);
      }
      if (readable.length !== DEMO_ALLOWED_READS.length) {
        fail(
          'demo writes',
          `writes are refused, but so are reads (${readable.length}/${DEMO_ALLOWED_READS.length} admin pages render). ` +
            'A demo that hides its admin panel is not demonstrating the product.',
        );
      } else {
        pass('demo writes', `${refused.length} state-changing routes refused, ${readable.length} admin reads still render`);
      }
    }
  }

  // ── 3. the searches the demo tells a visitor to type ──────────────────────
  //
  // ⚠ Honest scope: this proves the QUERY works against the real search
  // engine — the failure was "budget 2026" not matching Budget-2026.csv, a
  // separator problem, and `tag:report` naming a tag that cannot exist. It
  // does NOT prove demo.filex.sh still holds the files, because that corpus is
  // a byte copy on the host restored nightly (docs/DEMO.md) and is not this
  // repository's to inspect. The Go suite pins the corpus side
  // (backend/internal/api/handlers/search_demo_suggestion_test.go).
  {
    const seeded = [];
    let broken = null;
    for (const { finds } of ADVERTISED_QUERIES) {
      const dir = path.posix.dirname(`/${finds}`);
      const name = path.posix.basename(finds);
      await get(`${base}/api/files/manager?action=newfolder`, {
        method: 'POST',
        headers: { ...asAdmin.headers, 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: '/', name: dir.replace(/^\//, '') }),
      });
      const form = new FormData();
      form.append('path', dir);
      form.append('file[]', new Blob([`shop window fixture for ${finds}\n`]), name);
      const up = await get(`${base}/api/files/manager?action=upload`, {
        method: 'POST',
        headers: asAdmin.headers,
        body: form,
      });
      if (up.unreachable) {
        broken = up.unreachable;
        break;
      }
      if (up.res.ok) seeded.push(finds);
    }
    if (broken) skip('advertised search', broken);
    else if (seeded.length !== ADVERTISED_QUERIES.length) {
      skip('advertised search', `could only seed ${seeded.length}/${ADVERTISED_QUERIES.length} fixture files, so a zero result would not mean anything`);
    } else {
      const missed = [];
      for (const { query, finds } of ADVERTISED_QUERIES) {
        let hit = false;
        // The index is written asynchronously; give it a bounded chance
        // rather than sampling once and calling the product broken.
        for (let i = 0; i < 40 && !hit; i++) {
          const r = await get(`${base}/api/files/search?q=${encodeURIComponent(query)}&limit=50`, asAdmin);
          if (r.unreachable) break;
          const j = await r.res.json().catch(() => ({}));
          hit = (j.results ?? []).some((n) => String(n.path ?? '').replace(/^\//, '') === finds);
          if (!hit) await new Promise((r2) => setTimeout(r2, 250));
        }
        if (!hit) missed.push(`“${query}” → ${finds}`);
      }
      if (missed.length) {
        fail(
          'advertised search',
          'the demo tells visitors to type these and they find nothing:\n' +
            missed.map((m) => `      ${m}`).join('\n') +
            '\n    This is the first thing a visitor does. Fix the query grammar or change ' +
            'the copy in web/src/locales/*.json AND scripts/shop-window-data.mjs.',
        );
      } else {
        pass('advertised search', `${ADVERTISED_QUERIES.length} advertised queries return the file they promise`);
      }
    }
  }
}

// ════════════════════════════════════════════════════════════════════════════
// Against the published product
// ════════════════════════════════════════════════════════════════════════════

// ⚠ Both endpoints are overridable, and that is not a convenience: the live
// product is (today) correct, so the only way to show that these checks
// actually catch their defect is to serve the defect from somewhere. The red
// proof points them at a local fixture. It also lets a maintainer check a
// staging docs host before switching DNS.
const GH_API =
  process.env.SHOP_WINDOW_GH_API ?? 'https://api.github.com/repos/BRF-Tech/filex/releases?per_page=100';
const DOCS = process.env.SHOP_WINDOW_DOCS ?? 'https://docs.filex.sh';

/** A GitLab route pasted onto a GitHub host — the /-/ that 404s. */
const STRAY_ROUTE = /github\.com\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+\/-\/[A-Za-z0-9._/-]*/g;

/**
 * A private URL a reader would ACT on, as opposed to one a page merely
 * mentions.
 *
 * ⚠ Measured 2026-09-07 while writing this: docs.filex.sh/RELEASES contains
 * the string `github.com/brf-tech/filex` — inside a `<code>` span, in a
 * release highlight *describing the day this very defect was fixed*. A check
 * that flagged the bare host would have been red on a correct page from the
 * hour it was written, and a gate that cries wolf gets deleted. So the shapes
 * a reader can copy are what is flagged:
 *
 *   …/backend/pkg/pluginsdk   an import path (the actual PLUGINS defect)
 *   …/-/issues                a URL
 *   …filemanager.git          a clone URL
 *   href="…filemanager…"      a link
 *
 * and a bare host name in prose is not. The cost of this line is real and
 * worth stating: a page that publishes the private repo's *name* with no path
 * after it passes.
 */
const PRIVATE_URLS = [
  /gitlab\.com\/brftech\/filemanager\/[A-Za-z0-9._/-]+/g,
  /gitlab\.com\/brftech\/filemanager\.git/g,
  /git@gitlab\.com:[A-Za-z0-9._/-]+/g,
  /registry\.gitlab\.com\/[A-Za-z0-9._/-]+/g,
  /(?:href|src)="[^"]*gitlab\.com\/brftech[^"]*"/g,
  STRAY_ROUTE,
];

// Tags that actually exist on GitHub, filled in by checkReleasePages and read
// by checkDocsSite. Empty when the API could not be reached, which is why the
// version assertion downstream degrades to `skip` rather than to `fail`.
const publishedTags = new Set();

async function checkReleasePages() {
  const releases = [];
  for (let page = 1; page <= 3; page++) {
    const { res, unreachable } = await get(`${GH_API}&page=${page}`, {
      headers: { Accept: 'application/vnd.github+json', 'User-Agent': 'filex-shop-window-check' },
    });
    if (unreachable) return skip('release pages', `github api unreachable: ${unreachable}`);
    if (res.status === 403 || res.status === 429) {
      return skip('release pages', 'github api rate limit (60/hour unauthenticated). Set GITHUB_TOKEN and re-run, or try later.');
    }
    if (!res.ok) return skip('release pages', `github api answered ${res.status}`);
    const batch = await res.json();
    releases.push(...batch);
    if (batch.length < 100) break;
  }
  if (releases.length === 0) return skip('release pages', 'the api returned no releases at all');
  for (const r of releases) publishedTags.add(r.tag_name);

  const dead = [];
  for (const r of releases) {
    for (const m of (r.body ?? '').match(STRAY_ROUTE) ?? []) dead.push(`${r.tag_name}: ${m}`);
  }

  if (dead.length) {
    fail(
      'release pages',
      `${dead.length} GitLab \`/-/\` routes on github.com across ${releases.length} published releases — every one is a 404:\n` +
        dead.slice(0, 8).map((d) => `      ${d}`).join('\n') +
        (dead.length > 8 ? `\n      … and ${dead.length - 8} more` : '') +
        '\n    The bodies are published text: fix scripts/export-public.sh AND edit the released bodies.',
    );
  } else {
    pass('release pages', `${releases.length} published release bodies, no \`/-/\` route on github.com`);
  }

  // ── does the release page say anything? ──────────────────────────────────
  //
  // goreleaser derives a body from `git log` and its filters drop docs:, test:,
  // chore: and ci: — so a release whose work was any of those publishes a
  // heading and a commit hash while CHANGELOG.md has the paragraphs. v0.34.2's
  // page was 2,092 characters of which 16 were about the release.
  //
  // ⚠ Only the five most recent. Measured 2026-09-07 across all 105 published
  // releases: 89 of them carry no prose. That is history — the changelog has
  // moved on and they cannot be regenerated — and a gate that is red on 89
  // things nobody will ever fix is a gate that gets switched off. The fix
  // (`scripts/release-notes.mjs --github`, fed to goreleaser by the workflow)
  // applies from here forward, so from here forward is what is checked.
  //
  // ⚠⚠ The boilerplate is subtracted before measuring. Every body carries the
  // same goreleaser header, `docker pull` block and footer, so a body that is
  // ONLY those measures 295 characters — long enough to satisfy a naive length
  // test, which is precisely what the first version of this check did.
  const BOILERPLATE = [
    'Self-hosted file manager — Go single binary + multi-framework frontend.',
    'Download a binary below, or pull a Docker image:',
    '**Verify:** `sha256sum -c checksums.txt`',
    'Issues:',
    'Docs:',
  ];
  const prose = (body) => {
    let t = (body ?? '')
      .replace(/```[\s\S]*?```/g, '')
      .replace(/^#{1,6} .*$/gm, '')
      .replace(/https?:\/\/\S+/g, '')
      .replace(/\b[0-9a-f]{7,40}\b/g, '')
      .replace(/^\s*---\s*$/gm, '');
    for (const b of BOILERPLATE) t = t.split(b).join('');
    return t.replace(/[*\-\s]+/g, ' ').trim();
  };
  // Calibrated rather than guessed: the five most recent measure 1,448-18,979
  // characters of prose, and an empty one measures 16.
  const recent = releases
    .slice(0, 5)
    .map((r) => ({ tag: r.tag_name, n: prose(r.body).length }))
    .filter((r) => r.n < 200)
    .map((r) => `${r.tag} (${r.n} chars of prose)`);
  if (recent.length) {
    fail(
      'release notes',
      `${recent.length} of the 5 most recent releases publish no prose:\n` +
        recent.map((e) => `      ${e}`).join('\n') +
        '\n    goreleaser derives bodies from git log and its filters drop docs:/test:/chore:/ci:, ' +
        'so a release of documentation work publishes a heading and a commit hash while ' +
        'CHANGELOG.md has the paragraphs. `node scripts/release-notes.mjs --github` is what the workflow feeds it.',
    );
  } else {
    pass('release notes', 'the five most recent releases carry prose, not a commit hash');
  }

  // The links themselves. Distinct across every body, so the shared footer is
  // one request rather than a hundred.
  const urls = new Set();
  for (const r of releases.slice(0, 10)) {
    for (const m of (r.body ?? '').match(/https?:\/\/[^\s)>\]]+/g) ?? []) {
      urls.add(m.replace(/[.,;:]+$/, ''));
    }
  }
  const broken = [];
  let unchecked = 0;
  for (const u of [...urls].slice(0, 40)) {
    const { res, unreachable } = await get(u, { method: 'HEAD' });
    if (unreachable) unchecked++;
    else if (res.status >= 400) broken.push(`${u} → ${res.status}`);
  }
  if (broken.length) {
    fail('release links', `dead links in the ten most recent release bodies:\n${broken.map((b) => `      ${b}`).join('\n')}`);
  } else if (unchecked && unchecked === urls.size) {
    skip('release links', 'every link was unreachable — this looks like the network, not the links');
  } else {
    pass('release links', `${urls.size - unchecked} distinct links in the ten most recent releases all answer`);
  }
}

async function checkDocsSite() {
  const version = JSON.parse(fs.readFileSync(path.join(REPO, 'web', 'package.json'), 'utf8')).version;

  // Pages chosen for what each one proves, not for coverage: PLUGINS and
  // CONTRIBUTING are where the private module path actually appeared, RELEASES
  // is the only page that names the version, and REALTIME is the page that was
  // a 404 for weeks because the site was serving a v0.33.0 build while the
  // README's first paragraph advertised the feature and nine pages link to it.
  const pages = ['/', '/PLUGINS', '/CONTRIBUTING', '/RELEASES', '/REALTIME'];
  const fetched = new Map();
  for (const p of pages) {
    const { res, unreachable } = await get(`${DOCS}${p}`);
    if (unreachable) return skip('docs.filex.sh', `unreachable: ${unreachable}`);
    fetched.set(p, { status: res.status, text: res.ok ? await res.text() : '' });
  }

  const missing = [...fetched].filter(([, v]) => v.status !== 200).map(([p, v]) => `${p} → ${v.status}`);
  if (missing.length) {
    fail(
      'docs.filex.sh',
      `pages the README and the docs link are not on the site:\n${missing.map((m) => `      ${m}`).join('\n')}` +
        '\n    Usually the site is serving an older build than the release — release step 11.',
    );
  } else {
    pass('docs.filex.sh', `${pages.length} pages answer 200`);
  }

  const leaks = [];
  for (const [p, v] of fetched) {
    for (const re of PRIVATE_URLS) {
      for (const m of v.text.match(re) ?? []) leaks.push(`${p}: ${m}`);
    }
  }
  if (leaks.length) {
    fail(
      'docs.filex.sh',
      `${leaks.length} private URLs on the public docs site:\n` +
        [...new Set(leaks)].slice(0, 10).map((l) => `      ${l}`).join('\n') +
        '\n    The site was published from the SOURCE tree instead of the export. A plugin ' +
        'author following /PLUGINS copies an import path that names a repository they cannot ' +
        'reach and does not compile. Release step 11: `cd /g/filex-export` before the tar.',
    );
  } else {
    pass('docs.filex.sh', 'no private repository URL on any page checked');
  }

  const rel = fetched.get('/RELEASES');
  if (rel?.status === 200) {
    if (rel.text.includes(`v${version}`)) {
      pass('docs.filex.sh', `the site names the released version (v${version})`);
    } else if (!publishedTags.has(`v${version}`)) {
      // ⚠⚠ This is the difference between a gate and a nuisance. `web/package.json`
      // is bumped at release step 5 and the GitHub Release does not exist until
      // step 8; run in between — which is most of the release — the site
      // CANNOT name this version and saying "wrong" would be a lie. Measured
      // while writing this: v0.36.0 was in package.json and the newest
      // published release was v0.35.0.
      skip(
        'docs.filex.sh',
        `v${version} is not published as a GitHub release yet, so the site cannot name it. ` +
          'Run this after release steps 8-11.',
      );
    } else {
      fail(
        'docs.filex.sh',
        `v${version} is published on GitHub but the site's Releases page does not mention it — ` +
          'docs.filex.sh is serving an older build. Release step 11 pushes the prose; step 10 ' +
          'regenerates the page.',
      );
    }
  }
}

// ── the demo's own corpus, asked of the demo ────────────────────────────────
//
// ⚠⚠ The `--instance` half of this gate seeds the file it then searches for, so
// it proves the query GRAMMAR: that "budget 2026" survives the separator and
// that one typo is forgiven. It cannot prove what the DEMO holds, because that
// corpus is a byte copy on the host restored nightly (docs/DEMO.md) and is not
// this repository's to inspect. And the corpus half is where the defect
// actually was: on 2026-09-07 `invoice 2026` and `tag:report` — the only two
// strings the splash printed — returned nothing, not because search was broken
// but because no file with "invoice" in its name existed anywhere in the tree.
//
// The live demo can be asked directly, and that is what this does.
//
// ⚠ Read-only, and deliberately no credential is carried here: the demo
// publishes `demo_user`/`demo_pass` in its own anonymous capabilities payload
// because the login splash prints them, so this reads what the demo is telling
// visitors and then does exactly what a visitor does — sign in, type the
// suggested query. Nothing else is called. The demo guard refuses every
// state-changing route to that account anyway, which is the check two sections
// up.
const DEMO = process.env.SHOP_WINDOW_DEMO ?? 'https://demo.filex.sh';

async function checkPublishedDemoSearch() {
  const caps = await get(`${DEMO}/api/files/capabilities`);
  if (caps.unreachable) return skip('demo search', `${DEMO} unreachable: ${caps.unreachable}`);
  if (!caps.res.ok) return skip('demo search', `${DEMO}/api/files/capabilities answered ${caps.res.status}`);

  const j = await caps.res.json().catch(() => ({}));
  if (!j.demo_mode) {
    return skip('demo search', `${DEMO} is not running in demo mode, so it is not the corpus the splash describes`);
  }
  const email = j[DEMO_CREDENTIAL_FIELDS.user];
  const password = j[DEMO_CREDENTIAL_FIELDS.pass];
  if (!email || !password) {
    // ⚠ Not a pass. The splash prints these; an instance that does not publish
    // them is either misconfigured or has changed the contract, and either way
    // this check has no way in.
    return skip(
      'demo search',
      `${DEMO} publishes no ${DEMO_CREDENTIAL_FIELDS.user}/${DEMO_CREDENTIAL_FIELDS.pass} in its capabilities, ` +
        'so the searches a visitor is told to type cannot be run as a visitor',
    );
  }

  const login = await get(`${DEMO}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  });
  if (login.unreachable) return skip('demo search', `sign-in unreachable: ${login.unreachable}`);
  if (!login.res.ok) {
    return skip(
      'demo search',
      `the credentials ${DEMO} publishes were refused by ${DEMO} (${login.res.status}). ` +
        'That is worth looking at by hand — it is the first thing a visitor tries — but it is not ' +
        'something this check can call a search defect.',
    );
  }
  const cookie = (login.res.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ');

  const missed = [];
  for (const { query, finds } of ADVERTISED_QUERIES) {
    const r = await get(`${DEMO}/api/files/search?q=${encodeURIComponent(query)}&limit=50`, {
      headers: { Cookie: cookie },
    });
    if (r.unreachable) return skip('demo search', `search unreachable: ${r.unreachable}`);
    if (!r.res.ok) return skip('demo search', `search answered ${r.res.status}`);
    const body = await r.res.json().catch(() => ({}));
    const hit = (body.results ?? []).some((n) => String(n.path ?? '').replace(/^\//, '') === finds);
    if (!hit) missed.push(`“${query}” → ${finds} (${(body.results ?? []).length} results, none of them it)`);
  }

  if (missed.length) {
    fail(
      'demo search',
      `${DEMO} tells visitors to type these and its own corpus does not answer:\n` +
        missed.map((m) => `      ${m}`).join('\n') +
        '\n    This is the first interaction a visitor has. The corpus is a byte copy on the host ' +
        'restored nightly (docs/DEMO.md) — the fix is `demo-reset.sh --refresh-golden` from an ' +
        'instance that holds the files, or changing the suggestion in web/src/locales/*.json AND ' +
        'ADVERTISED_QUERIES in scripts/shop-window-data.mjs.',
    );
  } else {
    pass('demo search', `${ADVERTISED_QUERIES.length} advertised queries return their file on ${new URL(DEMO).host}`);
  }
}

// ── the repository's own About box, and the front page ──────────────────────

const GH_REPO_API = process.env.SHOP_WINDOW_GH_REPO ?? 'https://api.github.com/repos/BRF-Tech/filex';
const SITE = process.env.SHOP_WINDOW_SITE ?? 'https://filex.sh';

async function checkRepoMetadata() {
  const { res, unreachable } = await get(GH_REPO_API, {
    headers: { Accept: 'application/vnd.github+json', 'User-Agent': 'filex-shop-window-check' },
  });
  if (unreachable) return skip('repo about', `github api unreachable: ${unreachable}`);
  if (res.status === 403 || res.status === 429) {
    return skip('repo about', 'github api rate limit (60/hour unauthenticated). Set GITHUB_TOKEN and re-run, or try later.');
  }
  if (!res.ok) return skip('repo about', `github api answered ${res.status}`);
  const repo = await res.json();

  // ⚠⚠ The failure has to say WHAT TO PASTE. This is the one shop-window
  // surface with no deploy step: it is a text field in a settings form, edited
  // by a person, and a check that only says "wrong" leaves that person to
  // invent the replacement — which is how it drifted in the first place.
  const problems = [];
  if ((repo.description ?? '') !== REPO_ABOUT) {
    problems.push(
      `the About blurb does not match REPO_ABOUT in scripts/shop-window-data.mjs.\n` +
        `      published: ${repo.description ?? '(none)'}\n` +
        `      expected : ${REPO_ABOUT}\n` +
        '      Paste the expected line into Settings → General → Description (the ✎ beside "About" on\n' +
        '      the repository page does the same thing). If the published one is the better sentence,\n' +
        '      change REPO_ABOUT instead — but change one of them.',
    );
  }
  if ((repo.homepage ?? '') !== REPO_HOMEPAGE) {
    problems.push(
      `the About "Website" is ${repo.homepage ?? '(none)'}, expected ${REPO_HOMEPAGE}. ` +
        'Same form, the field under Description.',
    );
  }
  if (repo.has_issues === false) {
    // Every release body ends with an Issues: link. If the tab is off they are
    // all 404s, which is the defect this whole gate started from, wearing a
    // different hat.
    problems.push('the Issues tab is DISABLED, and every published release body links to it');
  }
  if (repo.archived) problems.push('the repository is marked ARCHIVED');

  if (problems.length) {
    fail('repo about', `${problems.length} problem(s) with what GitHub prints under the repository name:\n      ${problems.join('\n      ')}`);
  } else {
    pass('repo about', `the About blurb, website and Issues tab are what this repository says they should be`);
  }
}

async function checkFrontPage() {
  const { res, unreachable } = await get(SITE);
  if (unreachable) return skip('filex.sh', `unreachable: ${unreachable}`);
  if (!res.ok) return skip('filex.sh', `answered ${res.status}`);
  const html = await res.text();

  const missing = SITE_MUST_LINK.filter((l) => !html.includes(l.host));
  if (missing.length) {
    fail(
      'filex.sh',
      `the front page links none of ${missing.length} host(s) it has to:\n` +
        missing.map((l) => `      ${l.host} — ${l.why}`).join('\n') +
        '\n    ⚠ Check `site/index.html` before assuming the page is wrong: sync-site.sh uploads and ' +
        'never verifies, so the usual cause is a deploy that did not happen. If the local file ' +
        'already carries the link, the fix is `bash scripts/sync-site.sh`, not an edit.',
    );
  } else {
    pass('filex.sh', `links ${SITE_MUST_LINK.map((l) => l.host).join(', ')}`);
  }

  // The same private-URL scan the docs site gets. `site/` is scanned offline by
  // shopWindow.test.ts, and that proves nothing about what is PUBLISHED: the
  // two are only equal when somebody remembered to run the upload.
  const leaks = [];
  for (const re of PRIVATE_URLS) for (const m of html.match(re) ?? []) leaks.push(m);
  if (leaks.length) {
    fail(
      'filex.sh',
      `${leaks.length} private URL(s) on the public front page:\n      ${[...new Set(leaks)].slice(0, 10).join('\n      ')}\n` +
        '    site/ is uploaded verbatim — no converter stands between what is written there and what ' +
        'the public reads.',
    );
  } else {
    pass('filex.sh', 'no private repository URL on the front page');
  }
}

// ── screenshots: needs neither a server nor the network, only git ───────────
//
// ⚠ Full visual judgement is not automatable and this does not attempt it.
// Staleness is mechanical: a picture committed before the last change to the
// code that draws it cannot be showing that change. What it CANNOT see is
// listed with the failure, because a check whose limits are not written down
// gets trusted for things it never measured.
//
// The threshold is measured, not chosen: `admin-plugins.png` shipped SIX
// releases out of date, so six released versions of unfollowed change is the
// line. Below that the drift is reported and passes — this project tags several
// times a day, and every one of its sixteen README pictures is normally one or
// two releases behind by lunchtime. A gate that is red on all of them at every
// release is a gate somebody switches off.
const STALE_RELEASES = 6;

function gitOut(args) {
  return execFileSync('git', ['-C', REPO, ...args], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
}

function checkScreenshots() {
  let tagTimes;
  try {
    tagTimes = gitOut(['for-each-ref', '--format=%(*committerdate:unix)%(committerdate:unix)', 'refs/tags'])
      .split('\n')
      .map((s) => Number(s.trim()))
      .filter((n) => Number.isFinite(n) && n > 0)
      .sort((a, b) => a - b);
  } catch (e) {
    return skip('screenshots', `git is not answering here: ${e.message}`);
  }
  if (tagTimes.length === 0) {
    // A shallow clone or a fresh fork has no tags, and "zero releases have
    // passed" would make every picture eternally fresh.
    return skip('screenshots', 'this checkout has no tags, so "releases since the picture" cannot be counted');
  }

  const lastCommit = (p) => Number(gitOut(['log', '-1', '--format=%ct', '--', p]).trim() || 0);

  const drifted = [];
  const stale = [];
  for (const shot of SCREENSHOTS) {
    const taken = lastCommit(shot.file);
    if (!taken) {
      fail('screenshots', `${shot.file} is declared in SCREENSHOTS but git has no commit for it`);
      return;
    }
    const changes = gitOut(['log', '--format=%ct', `--since=@${taken}`, 'HEAD', '--', ...shot.depicts])
      .split('\n')
      .map((s) => Number(s.trim()))
      .filter((n) => Number.isFinite(n) && n > 0);
    if (changes.length === 0) continue;

    // How many RELEASES shipped a change to this surface that the picture did
    // not follow: for each such commit, the first tag at or after it.
    const releases = new Set();
    for (const c of changes) {
      const tag = tagTimes.find((t) => t >= c);
      if (tag) releases.add(tag);
    }
    const entry = `${shot.file} — ${changes.length} change(s) to ${shot.depicts.join(', ')} since it was taken, across ${releases.size} released version(s)`;
    if (releases.size >= STALE_RELEASES) stale.push(entry);
    else drifted.push(entry);
  }

  if (stale.length) {
    fail(
      'screenshots',
      `${stale.length} README picture(s) have been out of date through ${STALE_RELEASES} or more releases:\n` +
        stale.map((s) => `      ${s}`).join('\n') +
        '\n    A stale screenshot is not missing information, it is WRONG information: the reader ' +
        'takes it for the current product. Retake them with one command — `node e2e/shots/capture.mjs` ' +
        '— then look at the PNGs before committing (docs/CONTRIBUTING.md → Release process, step 2).' +
        '\n    ⚠ What this could not see: whether any picture is actually wrong. It compares commit ' +
        'dates, so a comment added to a component counts and a theme, font or browser change counts ' +
        'for nothing. Looking is still step 2.',
    );
  } else if (drifted.length) {
    pass(
      'screenshots',
      `${SCREENSHOTS.length} README pictures, none stale past ${STALE_RELEASES} releases ` +
        `(${drifted.length} drifting: ${drifted.map((d) => d.split(' — ')[0].replace('docs/screenshots/', '')).join(', ')})`,
    );
  } else {
    pass('screenshots', `${SCREENSHOTS.length} README pictures are newer than everything that draws them`);
  }
}

// ── run ─────────────────────────────────────────────────────────────────────
let booted = null;
try {
  if (wantInstance) {
    if (bootBin) {
      const b = await boot(bootBin);
      if (b.skip) skip('instance', b.skip);
      else {
        booted = b;
        await checkInstance(b.base, true);
      }
    } else if (instanceURL) {
      await checkInstance(instanceURL, false);
    } else {
      skip('instance', '--instance needs either --boot <path-to-filex-binary> or a base URL');
    }
  }
  if (wantPublished) {
    await checkReleasePages();
    await checkDocsSite();
    await checkPublishedDemoSearch();
    await checkRepoMetadata();
    await checkFrontPage();
  }
  // ⚠ Not behind either flag. It needs nothing but the checkout, so gating it
  // on --instance would skip it whenever there is no binary and gating it on
  // --published would skip it whenever the network is down — and a picture
  // going stale has nothing to do with either.
  checkScreenshots();
} finally {
  await booted?.stop();
  // Best effort. A temp directory that survives is litter; a teardown that
  // throws would replace the findings with a stack trace.
  try {
    if (booted?.dir) fs.rmSync(booted.dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  } catch { /* the OS still holds the database file; the temp dir can wait */ }
}

const width = Math.max(...results.map((r) => r.what.length), 0);
for (const r of results) {
  const mark = r.state === 'ok' ? '  ok  ' : r.state === 'skip' ? ' skip ' : ' FAIL ';
  console.log(`${mark} ${r.what.padEnd(width)}  ${r.detail}`);
}

const failed = results.filter((r) => r.state === 'FAIL').length;
const skipped = results.filter((r) => r.state === 'skip').length;
console.log();
if (failed) {
  console.log(`check-shop-window: ${failed} defect(s) in the shop window. This fails the release.`);
  process.exit(1);
}
if (skipped || results.length === 0) {
  console.log(
    `check-shop-window: ${skipped} check(s) COULD NOT RUN — nothing was found wrong, and nothing ` +
      'was proved right either. Exit 2 rather than 1: a network outage must not fail a build. ' +
      'Re-run before you tag.',
  );
  process.exit(2);
}
console.log(`check-shop-window: ${results.length} checks passed.`);
