/**
 * The same logic must not be written twice.
 *
 * "Tekrar eden şeyler tek yerden eklenmeli, tek yerden yönetilmeli." That has
 * been the rule here for a long time and it has been broken repeatedly — and
 * every single time it was caught by a person looking at a screen, never by a
 * check. A 464-line second listing pane. `mime_type === 'inode/storage'` in
 * three view components. The brand mark hand-built as an inline SVG in five
 * more files, one of them still painted the pre-rebrand indigo. Two date
 * formatters that disagree. The shape is always identical: the second copy is
 * written because the first is inconvenient to reach, nothing notices, and the
 * two drift until somebody sees it.
 *
 * This is the check that notices. It runs `scripts/dup-scan.mjs`, which looks
 * for duplication three ways, because the real cases come in three shapes and
 * no single mechanism sees all of them:
 *
 *   1. DUPLICATED FRAGMENTS — token clones, two passes.
 *      `verbatim` keeps identifiers, so it is copy-paste: >= 100 contiguous
 *      tokens (roughly 15 lines) that agree exactly.
 *      `renamed` blanks identifiers, strings and numbers, so it is the block
 *      that was re-typed or copied and then adapted: >= 140 tokens of
 *      identical structure. This is the pass that found the 522-token
 *      `SetState` handler shared by nfsexports.go and sshkeys.go, where not
 *      one identifier is the same and the verbatim pass sees nothing.
 *
 *   2. CONCEPTS OUTSIDE THEIR HOME — some things have exactly one home here
 *      (byte formatting, date formatting, the brand mark). A `tell` that only
 *      that concept writes, found in a file that is not its home, is a second
 *      implementation regardless of how it is spelled. This is what catches
 *      the one-liner and the ten-line helper, which no token floor can reach
 *      without reporting every `if (!ok) return null` in the tree.
 *
 *   3. SURFACES THAT BUILD THEIR OWN CHROME — a component that renders two or
 *      more of ListView/GridView/GalleryView IS a listing surface, and a
 *      listing surface must take its breadcrumb, filter row, view switch, sort
 *      order and listing filter from the shared modules. SecondaryPane renders
 *      all three views and hand-rolls three of the five. That is the case the
 *      owner is angry about, and mechanism 1 cannot see it: with every name
 *      blanked, the longest matching run between SecondaryPane.vue and
 *      Breadcrumb.vue is 29 tokens. (Measured. jscpd 5.2's AST-similarity mode
 *      finds nothing between them either, at any ratio from 0.85 to 0.55.)
 *
 * HOW TO ANSWER IT WHEN IT FIRES
 * ------------------------------
 * Extract the shared thing and call it from both places. That is the whole
 * answer, and it is almost always smaller than it looks. Adding a second copy
 * plus a register entry is not an answer — it is the failure mode this check
 * exists to stop, written down.
 *
 * If the duplication really is deliberate, it goes in one of the two registers
 * below, WITH A REASON, and the reason has to be about this code — not "known
 * issue". The two registers say different things:
 *
 *   LEGITIMATE_TWINS   the duplication is the design and will not be removed.
 *   KNOWN_DUPLICATION  debt: real, pre-existing, and owed. Carries a
 *                      `maxTokens` ceiling so the area cannot quietly grow a
 *                      bigger copy than it already has.
 *
 * Both are kept honest the same way the theme-token allowlist is: an entry
 * that no longer matches anything FAILS. When you fix a duplicate the build
 * turns red and tells you to delete its entry. That is the point — an
 * allowlist nobody prunes turns into permission for the next hole.
 */
import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const SCANNER = path.join(REPO_ROOT, 'scripts', 'dup-scan.mjs');

interface Site {
  file: string;
  startLine: number;
  endLine: number;
}
interface Finding {
  kind: 'verbatim' | 'renamed';
  tokens: number;
  lines: number;
  sites: Site[];
}
interface ConceptResult {
  id: string;
  what: string;
  fix: string;
  home: string[];
  homeHits: { file: string; lines: number[] }[];
  strays: { file: string; lines: number[] }[];
}
interface CompositionResult {
  id: string;
  what: string;
  surfaces: { file: string; renders: string[]; missing: string[] }[];
  violations: { file: string; renders: string[]; missing: string[] }[];
}
interface ScanResult {
  config: Record<string, unknown>;
  stats: { files: number; tokens: number; ms: number };
  findings: Finding[];
  concepts: ConceptResult[];
  composition: CompositionResult[];
}

function runScanner(args: string[]): ScanResult {
  return JSON.parse(
    execFileSync(process.execPath, [SCANNER, '--json', ...args], {
      cwd: REPO_ROOT,
      encoding: 'utf8',
      maxBuffer: 64 * 1024 * 1024,
      timeout: 120_000,
    }),
  );
}

const scan = runScanner([]);

// ---------------------------------------------------------------------------
// Register 1 — the duplication is the design
// ---------------------------------------------------------------------------

interface Twin {
  files: string[];
  reason: string;
}

/**
 * No `maxTokens`: these grow on purpose. Adding a query to the Postgres driver
 * means adding it to the SQLite driver too, and a ceiling here would turn
 * correct work red.
 */
const LEGITIMATE_TWINS: Twin[] = [
  {
    files: [
      'backend/internal/db/drivers/postgres/postgres.go',
      'backend/internal/db/drivers/sqlite/sqlite.go',
    ],
    reason:
      'One Store interface, two SQL dialects. What CAN be shared already is — ' +
      'internal/db/store.go rewrites SQLite upserts for MySQL at run time — and ' +
      'what is left is per-dialect SQL text plus its row scanning. Merging it ' +
      'would mean a query builder, and a query builder that silently emits the ' +
      'wrong dialect is a worse failure than two files that read the same.',
  },
  {
    files: [
      'backend/internal/db/drivers/postgres/customthemes.go',
      'backend/internal/db/drivers/sqlite/customthemes.go',
    ],
    reason:
      'tema:v1 — the same argument as the two driver files above, but only ' +
      'after the shareable half was actually shared. This gate first caught ' +
      'these two files carrying ~400 tokens of clone: the row scanning, the ' +
      'select list and the token-JSON marshalling had no dialect in them, and ' +
      'they moved to internal/db/customtheme_scan.go, which both drivers ' +
      'already import. What is left is four statements that differ in exactly ' +
      'the two ways the dialects differ (`?` vs `$1`, CURRENT_TIMESTAMP vs ' +
      'NOW()) plus the error handling around each. Parameterising those means ' +
      'a placeholder-rewriting query builder for four statements, and a builder ' +
      'that emits the wrong dialect fails at run time on the one engine nobody ' +
      'tested.',
  },
  {
    files: [
      'backend/internal/queue/drivers/postgres/postgres.go',
      'backend/internal/queue/drivers/sqlite/sqlite.go',
    ],
    reason:
      'Same argument as the db drivers, and sharper: the claim/lease SQL is ' +
      'where the dialects differ most (FOR UPDATE SKIP LOCKED, UTC time ' +
      'expressions). Four workers taking the same job is exactly what happened ' +
      'the last time this was written generically.',
  },
];

// ---------------------------------------------------------------------------
// Register 2 — debt: real duplication, recorded so the gate is red only for NEW
// ---------------------------------------------------------------------------

interface Debt {
  files: string[];
  reason: string;
  /** The biggest single clone this area may carry. Measured, then rounded up. */
  maxTokens: number;
}

/**
 * Everything here was already in the tree when this gate was written. Each
 * entry names what the shared thing would be, so the next person does not have
 * to work it out again.
 *
 * ⚠ A path ending in `/` is a prefix and covers a whole area. Those are the
 * dangerous entries — an area-wide entry is where a new duplicate could hide —
 * so they carry a token ceiling and are the first ones to break up.
 */
const KNOWN_DUPLICATION: Debt[] = [
  // -- backend: same file, twice --------------------------------------------
  {
    files: ['backend/internal/db/drivers/postgres/postgres.go'],
    reason:
      'Not the dialect argument — both copies are the SAME dialect. The ' +
      'insert-then-return-GetByID / SELECT-columns-WHERE-id / list-and-scan ' +
      'triplet is written out once per table (ssh keys, s3 keys, nfs exports, …). ' +
      'One generic scan-and-return per row type would remove most of it.',
    maxTokens: 450,
  },
  {
    files: ['backend/internal/db/drivers/sqlite/sqlite.go'],
    reason:
      'Same as the Postgres driver, in the same dialect: the per-table insert / ' +
      'get-by-id / list triplet written out once per table instead of once.',
    maxTokens: 450,
  },
  {
    files: ['backend/internal/api/handlers/'],
    reason:
      'The resource-handler shape — owned() guard, decode into an anonymous ' +
      'struct, store call, writeJSON on three error paths, re-read and return — ' +
      'is re-typed per resource, and so is the auth guard above it (nil user -> ' +
      '401, admin -> allow, otherwise check the ACL level). The 522-token pair is ' +
      'nfsexports.SetState vs sshkeys.SetState: identical control flow, not one ' +
      'shared name. Wants a generic CRUD helper over the store method.',
    maxTokens: 600,
  },
  {
    files: [
      'backend/internal/ftpsrv/',
      'backend/internal/nfssrv/',
      'backend/internal/sftpsrv/',
      'backend/internal/dav/',
    ],
    reason:
      'Four protocol front ends over one storage layer. rootInfo/storageInfo/' +
      'objectInfo are byte-for-byte identical in ftpsrv and sftpsrv, and the ' +
      'listing walk is the same in all of them. The protocol-specific part is ' +
      'the type each one returns; the ACL-to-mode mapping underneath is not.',
    maxTokens: 350,
  },
  {
    files: ['backend/internal/storage/drivers/'],
    reason:
      'Three things repeat here: the driver descriptor (ftp/sftp/smb are the same ' +
      'field list with three labels changed), the dial/probe preamble, and the ' +
      "remote-entry -> storage.Object mapping, which webdav.go itself writes twice. " +
      'The descriptors are data and should be built by a helper.',
    maxTokens: 175,
  },
  {
    files: ['backend/internal/thumb/'],
    reason:
      'Every thumbnailer opens with the same preamble — binary-not-in-PATH error, ' +
      'MkdirTemp, defer RemoveAll, derive a source filename, create it — and only ' +
      'then differs in which command it runs. That preamble is one helper.',
    maxTokens: 200,
  },
  {
    files: ['backend/internal/s3api/'],
    reason:
      'The write-permission preamble (read-only check, ACL lookup, WriteError with ' +
      'the S3 error code) is repeated inside write.go, and the multipart part ' +
      'handlers repeat their own parse-and-validate block inside multipart.go.',
    maxTokens: 150,
  },
  {
    files: ['backend/internal/auth/drivers/'],
    reason:
      'Driver boilerplate: the same struct{store}, New(), Name(), and Init() ' +
      'nil-store check, once per auth driver. A small embeddable base would carry ' +
      'all of it and leave each driver with only its Authenticate.',
    maxTokens: 125,
  },
  {
    files: ['backend/internal/dbsetting/'],
    reason:
      'Each typed spec (bool, string) writes its own Seed(): read the env var, bail ' +
      'if empty, skip when the setting is already stored, log the same debug line. ' +
      'Only the parse in the middle is type-specific.',
    maxTokens: 125,
  },
  {
    files: ['backend/internal/testutil/'],
    reason:
      'The in-memory SQLite test DB helper — blank driver imports, the per-test DSN ' +
      'counter with its comment, NewTestDB and its migration run — exists in both ' +
      'testutil and testutil/dbtest. Harness code, so low priority, but the ' +
      'per-DSN trick is subtle enough that two copies WILL diverge.',
    maxTokens: 200,
  },
  {
    files: ['backend/internal/staging/staging.go'],
    reason:
      'Part finalisation is written twice: Sync, Close with its own wrapped error ' +
      'on each, then take the per-upload lock and re-read the manifest. Two copies ' +
      'of a sequence whose whole job is to be crash-safe.',
    maxTokens: 250,
  },
  {
    files: ['backend/internal/ops/service.go'],
    reason:
      'The operation row scanner — the 15-column rows.Scan, the JSON unmarshal of ' +
      'sources, the DestStorageID fallback — is written out at two query sites. ' +
      'Add a column to operations and one of them is silently wrong.',
    maxTokens: 150,
  },
  {
    files: ['backend/internal/trash/service.go', 'backend/internal/versioning/cleanup.go'],
    reason:
      'Two daily-purge loops: clamp a non-positive interval to 24h, NewTicker, defer ' +
      'Stop, select on ctx.Done, call the purge. One ticker-loop helper taking a ' +
      'function would cover both, and both carry the same "first tick after the ' +
      'interval, not immediately" reasoning in a comment.',
    maxTokens: 200,
  },
  {
    files: ['backend/internal/auth/registry.go', 'backend/internal/db/driver.go'],
    reason:
      'Two driver registries — register by name, look up, error on unknown — ' +
      'written twice. A generic registry would be about thirty lines.',
    maxTokens: 200,
  },

  // -- frontend --------------------------------------------------------------
  {
    files: [
      'packages/core/src/components/GalleryView.vue',
      'packages/core/src/components/GridView.vue',
      'packages/core/src/components/ListView.vue',
    ],
    reason:
      'THE case the rule is about. The three view components share ~350 tokens ' +
      'of identical selection, drag payload, context-menu and badge logic. They ' +
      'are three renderings of one listing, so that belongs in a composable ' +
      'they all call, not in each of them.',
    maxTokens: 400,
  },
  {
    files: [
      'packages/core/src/composables/useNFSExports.ts',
      'packages/core/src/composables/useS3Keys.ts',
      'packages/core/src/composables/useSSHKeys.ts',
      'packages/core/src/composables/useTokens.ts',
      'packages/core/src/components/NFSExportsPanel.vue',
      'packages/core/src/components/S3KeysPanel.vue',
      'packages/core/src/components/SSHKeysPanel.vue',
      'packages/core/src/components/TokensPanel.vue',
      'packages/core/src/components/ConnectionGuideView.vue',
    ],
    reason:
      'One CRUD-resource composable written four times (list/create/delete/ ' +
      'toggle + loading + error), and the panels around them written four times ' +
      'to match. A `useResource<T>(endpoint)` would collapse all of it; the ' +
      'four-way clone is the clearest signal in the frontend.',
    maxTokens: 400,
  },
  {
    files: ['web/src/api/', 'web/src/stores/'],
    reason:
      'Same story on the admin side: every api module is the same axios wrapper ' +
      'and every pinia store is the same page/loading/error/fetch triple with a ' +
      'different type parameter.',
    maxTokens: 250,
  },
  {
    files: [
      'packages/core/src/components/StorageFields.vue',
      'web/src/components/StorageDriverFields.vue',
    ],
    reason:
      'The driver-config field resolution — i18n_key-over-label, emit a patched ' +
      'model, and read a value through its legacy aliases — is implemented in the ' +
      'explorer form and again in the admin form. Two forms over one set of ' +
      'drivers is how a new field ends up editable in one place and not the other.',
    maxTokens: 200,
  },
  {
    files: [
      'packages/core/src/components/ShortcutSettings.vue',
      'packages/core/src/components/ShortcutsHelp.vue',
    ],
    reason:
      'The settings editor and the help sheet each build the shortcut list from ' +
      'the registry with the same grouping and label code.',
    maxTokens: 200,
  },
  {
    files: ['web/src/views/Protection.vue'],
    reason:
      'Two settings-save handlers in one view repeat the same block: clear the error ' +
      'ref, await, read the response back into local refs, toast, extractError into ' +
      'the error ref, clear the saving flag in finally.',
    maxTokens: 175,
  },
  {
    files: [
      'packages/core/src/components/ContextMenu.vue',
      'packages/core/src/components/OnboardingTour.vue',
    ],
    reason:
      'Both watch prefers-color-scheme with the same ref + matchMedia + ' +
      'addEventListener/removeEventListener pair, because both draw outside `.fe` ' +
      "where the theme cascade does not reach. OnboardingTour's copy even says " +
      '"same pattern as ContextMenu" in a comment — it was copied knowingly. One ' +
      'usePrefersDark() composable.',
    maxTokens: 150,
  },
  {
    files: ['desktop/src/dragout.ts'],
    reason:
      'Listing a remote directory through the manager API — build the URL, set ' +
      'action=index, check the status code, parse `files` — is written twice in the ' +
      'drag-out path.',
    maxTokens: 150,
  },
];

// ---------------------------------------------------------------------------
// Register 3 — concepts found outside their home
// ---------------------------------------------------------------------------

interface ConceptExemption {
  concept: string;
  file: string;
  reason: string;
}

const CONCEPT_EXEMPTIONS: ConceptExemption[] = [
  // -- genuinely a different job --------------------------------------------
  {
    concept: 'byte-size',
    file: 'packages/core/src/components/AdvancedSearch.vue',
    reason:
      'PARSES a size the user typed ("10mb" -> bytes) rather than formatting one ' +
      'for display. Opposite direction, and the tell (a 1024 lookup table) cannot ' +
      'tell them apart.',
  },
  {
    concept: 'datetime-format',
    file: 'packages/core/src/components/TimeZonePicker.vue',
    reason:
      'The time-zone picker builds one formatter PER CANDIDATE ZONE to show what ' +
      "time it is there. That is not \"render this instant in the viewer's zone\", " +
      'which is what the shared formatter does.',
  },

  // -- debt: a second implementation, recorded until it is removed ----------
  {
    concept: 'brand-mark',
    file: 'backend/internal/api/handlers/share.go',
    reason:
      'DEBT. publicBrandMark is the mark hand-typed as a Go string for the ' +
      'no-JavaScript fallback pages, which have no bundler and cannot import ' +
      'the component. It is the RIGHT colour today (#2f6ceb, checked ' +
      '2026-09-23) — the reason here used to say it still filled the ' +
      'pre-rebrand indigo, which was true when it was written and is the case ' +
      'this register exists for: a second copy goes out of step silently. It ' +
      'is also a fixed hex where the SPA shell now follows --fe-primary, so a ' +
      'branded instance gets its accent on the JS page and product blue on ' +
      'the fallback.',
  },
  {
    concept: 'brand-mark',
    file: 'desktop/ui/app.html',
    reason: 'DEBT. The mark re-typed as a JS string; recoloured separately from LogoMark.',
  },
  {
    concept: 'brand-mark',
    file: 'desktop/ui/index.html',
    reason:
      'DEBT. The same hand-typed string a second time in the other desktop shell ' +
      'page, so the desktop app alone carries two independent copies of the mark ' +
      'and a recolour has to find both.',
  },
  {
    concept: 'brand-mark',
    file: 'site/index.html',
    reason:
      'DEBT. Twice in one file — once as a data: favicon, once inline in the ' +
      'header. scripts/sync-site-assets.mjs already syncs this direction and could ' +
      'generate both.',
  },
  {
    concept: 'brand-mark',
    file: 'site/social-preview.src.html',
    reason:
      'DEBT. The mark again in the OG-image source, which is the copy strangers ' +
      'see first when a link is shared and the one nobody thinks to re-check ' +
      'after a rebrand.',
  },
];

// ---------------------------------------------------------------------------
// Register 4 — listing surfaces that build their own chrome
// ---------------------------------------------------------------------------

// ⚠ EMPTY, and that is the point. The one entry here was SecondaryPane.vue —
// a second implementation of a listing pane whose breadcrumb was a private
// 28-line computed and which had no filter row and no view switch at all, so
// every feature added to the primary pane missed the right-hand half of a
// split. It is the case that started this check, and it was answered the way
// the check asks: the file is deleted and both panes are now one FilePane.vue
// rendered twice. Add an entry here only for a listing surface that genuinely
// must build its own chrome — and say why, because the last one could not.
const COMPOSITION_DEBT: { file: string; reason: string }[] = [];

// ---------------------------------------------------------------------------
// Matching
// ---------------------------------------------------------------------------

const matches = (file: string, pattern: string): boolean =>
  pattern.endsWith('/') ? file.startsWith(pattern) : file === pattern;

const covers = (entry: { files: string[] }, finding: Finding): boolean =>
  finding.sites.every((s) => entry.files.some((p) => matches(s.file, p)));

const describeFinding = (f: Finding): string =>
  `[${f.kind}] ${f.tokens} tokens\n      ` +
  f.sites.map((s) => `${s.file}:${s.startLine}-${s.endLine}`).join('\n      ');

/**
 * Debt is matched BEFORE twins on purpose. The narrow entries (one file, its
 * own internal duplication) claim their findings first, so the broad twin entry
 * that names both driver files does not swallow them and leave the narrow ones
 * looking stale.
 */
function classify(f: Finding): { kind: 'debt' | 'twin' | 'new'; entry?: Debt | Twin } {
  for (const entry of KNOWN_DUPLICATION) if (covers(entry, f)) return { kind: 'debt', entry };
  for (const entry of LEGITIMATE_TWINS) if (covers(entry, f)) return { kind: 'twin', entry };
  return { kind: 'new' };
}

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// The detector's own red/green proof
// ---------------------------------------------------------------------------
//
// A gate that only ever passes has proved nothing: a scanner that quietly
// stopped matching — a lexer change, a threshold nudge, a scope typo — would
// leave this whole file green and the rule unenforced. So the scanner is also
// pointed at a tiny fixture tree that contains, on purpose, one copy-pasted
// pair, one re-typed pair, and one file full of things it must NOT report.

describe('the detector still detects', () => {
  const fixtures = runScanner([
    '--root',
    path.join(REPO_ROOT, 'web', 'tests', 'quality', 'fixtures', 'dup'),
    '--only',
    'src',
  ]);
  const between = (a: string, b: string) =>
    fixtures.findings.filter(
      (f) =>
        f.sites.length === 2 && f.sites.some((s) => s.file.endsWith(a)) &&
        f.sites.some((s) => s.file.endsWith(b)),
    );

  it('finds a block that was copy-pasted with its names intact', () => {
    const hit = between('copied-a.ts', 'copied-b.ts');
    expect(hit.length, 'the verbatim pass no longer sees an identical block').toBe(1);
    expect(hit[0].kind).toBe('verbatim');
    expect(hit[0].tokens).toBeGreaterThan(150);
  });

  it('finds a block that was re-typed with every name changed', () => {
    // The pass that earns its keep: no identifier, string or number is shared
    // between these two files, so a value-token detector sees nothing at all.
    const hit = between('retyped-a.ts', 'retyped-b.ts');
    expect(hit.length, 'the renamed pass no longer sees a re-typed block').toBe(1);
    expect(hit[0].kind).toBe('renamed');
    expect(hit[0].tokens).toBeGreaterThan(150);
  });

  it('does not report two token maps or two unrelated functions', () => {
    // The other half of the proof. A detector that reports everything gets
    // switched off in a week, which is worse than no detector.
    const noise = fixtures.findings.filter((f) => f.sites.some((s) => s.file.endsWith('innocent.ts')));
    expect(
      noise.map(describeFinding),
      'the shape filter has been loosened — declarative tables are being reported as clones',
    ).toEqual([]);
  });
});

describe('duplicated code fragments', () => {
  it('the scanner actually looked at the code', () => {
    // A guard for the guard. If the scan silently collected nothing — a moved
    // directory, a broken walk — every assertion below would pass by checking
    // an empty list.
    expect(scan.stats.files).toBeGreaterThan(400);
    expect(scan.stats.tokens).toBeGreaterThan(500_000);
    expect(scan.concepts.length).toBeGreaterThan(0);
    expect(scan.composition.length).toBeGreaterThan(0);
  });

  it('no fragment is duplicated outside the two registers', () => {
    const fresh = scan.findings.filter((f) => classify(f).kind === 'new');
    expect(
      fresh.map(describeFinding),
      'Duplicated code with no entry in either register.\n' +
        '  The answer is to extract the shared thing and call it from both places.\n' +
        '  Adding a copy AND a register entry is the failure this check exists to stop.\n' +
        '  Reproduce with: node scripts/dup-scan.mjs',
    ).toEqual([]);
  });

  it('no area grows a bigger copy than the one it is on record for', () => {
    const over: string[] = [];
    for (const f of scan.findings) {
      const { kind, entry } = classify(f);
      if (kind !== 'debt') continue;
      const debt = entry as Debt;
      if (f.tokens > debt.maxTokens) {
        over.push(`${describeFinding(f)}\n      (ceiling ${debt.maxTokens} for ${debt.files[0]})`);
      }
    }
    expect(
      over,
      'A known-duplicated area now carries a LARGER duplicate than it did.\n' +
        '  That is new duplication in an old place. Extract it; do not raise the ceiling.',
    ).toEqual([]);
  });
});

describe('concepts stay in one home', () => {
  it('every concept still lives where the register says it does', () => {
    // If the home stops matching its own tell, the concept moved (or the tell
    // rotted) and the rule is now silently allowing everything.
    const homeless = scan.concepts.filter((c) => c.homeHits.length === 0);
    expect(
      homeless.map((c) => `${c.id}: no file in ${c.home.join(', ')} matches its own tell`),
      "A concept's tell no longer matches inside its home — fix the tell or the home in scripts/dup-scan.mjs",
    ).toEqual([]);
  });

  it('no concept is implemented outside its home without a reason', () => {
    const unlisted: string[] = [];
    for (const c of scan.concepts) {
      for (const stray of c.strays) {
        const listed = CONCEPT_EXEMPTIONS.some((e) => e.concept === c.id && e.file === stray.file);
        if (!listed) unlisted.push(`${c.id}: ${stray.file}:${stray.lines.join(',')} — ${c.fix}`);
      }
    }
    expect(
      unlisted,
      'A second implementation of a concept that has exactly one home.\n' +
        '  Call the one in its home. If this really is a different job, add it to\n' +
        '  CONCEPT_EXEMPTIONS with a reason that says WHY it is different.',
    ).toEqual([]);
  });
});

describe('listing surfaces use the shared chrome', () => {
  it('the rule found some listing surfaces to check', () => {
    const surfaces = scan.composition.flatMap((r) => r.surfaces);
    expect(
      surfaces.length,
      'no component renders two or more of the view components — the marker list is stale',
    ).toBeGreaterThan(0);
  });

  it('no listing surface hand-rolls chrome that already exists', () => {
    const unlisted: string[] = [];
    for (const rule of scan.composition) {
      for (const v of rule.violations) {
        if (!COMPOSITION_DEBT.some((d) => d.file === v.file)) {
          unlisted.push(`${v.file} renders ${v.renders.join(', ')} but not ${v.missing.join(', ')}`);
        }
      }
    }
    expect(
      unlisted,
      'A listing surface is building its own breadcrumb / filter row / view switch.\n' +
        '  Render the shared component instead. This is how the two halves of a split\n' +
        '  pane end up with different features.',
    ).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// Register hygiene — the part that keeps the escape hatch from becoming a hole
// ---------------------------------------------------------------------------

describe('the registers stay honest', () => {
  it('every entry carries a reason about this code', () => {
    const thin: string[] = [];
    const check = (label: string, reason: string) => {
      if (reason.trim().length < 60) thin.push(`${label}: reason is too short to be a reason`);
      if (/^(known|legacy|todo|wip|pre-existing)\b/i.test(reason.trim())) {
        thin.push(`${label}: "${reason.slice(0, 30)}…" says nothing about this code`);
      }
    };
    for (const e of LEGITIMATE_TWINS) check(`twin ${e.files[0]}`, e.reason);
    for (const e of KNOWN_DUPLICATION) check(`debt ${e.files[0]}`, e.reason);
    for (const e of CONCEPT_EXEMPTIONS) check(`concept ${e.concept}/${e.file}`, e.reason);
    for (const e of COMPOSITION_DEBT) check(`composition ${e.file}`, e.reason);
    expect(thin).toEqual([]);
  });

  it('every debt entry names a ceiling it is actually under', () => {
    const bad = KNOWN_DUPLICATION.filter((e) => !Number.isInteger(e.maxTokens) || e.maxTokens < 100);
    expect(bad.map((e) => e.files[0])).toEqual([]);
  });

  it('no register entry matches nothing any more', () => {
    const stale: string[] = [];

    const claimed = new Map<object, number>();
    for (const f of scan.findings) {
      const { entry } = classify(f);
      if (entry) claimed.set(entry, (claimed.get(entry) ?? 0) + 1);
    }
    for (const e of [...KNOWN_DUPLICATION, ...LEGITIMATE_TWINS]) {
      if (!claimed.get(e)) stale.push(`fragment register: ${e.files.join(' + ')}`);
    }

    for (const e of CONCEPT_EXEMPTIONS) {
      // ⚠ The public export withholds whole directories (site/ is the project
      // page, not the product), so on GitHub these entries name files that
      // are not in the tree at all — and were reported as stale (measured in
      // the export of v0.41.0, before it was pushed). An entry is judged only where its
      // top-level directory exists; a file deleted from a directory that is
      // still here is still stale.
      if (!existsSync(path.join(REPO_ROOT, e.file.split('/')[0]))) continue;
      const concept = scan.concepts.find((c) => c.id === e.concept);
      if (!concept) stale.push(`concept exemption names an unknown concept: ${e.concept}`);
      else if (!concept.strays.some((s) => s.file === e.file)) {
        stale.push(`concept exemption: ${e.concept} in ${e.file}`);
      }
    }

    const violating = new Set(scan.composition.flatMap((r) => r.violations.map((v) => v.file)));
    for (const e of COMPOSITION_DEBT) {
      if (!violating.has(e.file)) stale.push(`composition debt: ${e.file}`);
    }

    expect(
      stale,
      'These entries no longer match anything.\n' +
        '  If you fixed the duplication: delete the entry — that is the whole point.\n' +
        '  If you moved the code: update the paths.\n' +
        '  An allowlist nobody prunes becomes the place the next duplicate hides.',
    ).toEqual([]);
  });
});
