// What a release publishes, by SURFACE, and which surfaces a change reaches
// (task #252). The one map the release tool (the `server` profile, the gates
// it reads back), the ship (which steps run) and release.yml (through the
// tag's `surfaces:` line) all go by.
//
// ⚠ Why (the owner, 2026-10-12): a one-line server fix went out as a whole
// release - the pretag with the UI and the desktop app, GitHub's 45-minute
// matrix, the 28-minute tag run, the desktop packages, the stores, npm and the
// embeds - two to two and a half hours for a change only the server carried,
// and every unaffected surface got a "new" version of the same bytes. "If the
// CLI or the UI did not change, it is not updated; if it did, it is." A
// server patch now goes out on its own lane: the server's gates, then the
// release, with GitHub's full matrix BEHIND it, and the update manifest's
// auto_ok switched on only once that matrix is green (matrix-watch.mjs).
//
// ⚠ In doubt, everything: a changed file no rule below places makes the
// release one of every surface, never a server patch.

import { globMatch } from './checks.mjs';

/**
 * The surfaces, with what each publishes. Every published gate of the plan
 * and every ship step that publishes names the surface it belongs to; one
 * with none belongs to every release (the CHANGELOG, the GitHub Release, the
 * Releases page, docs.filex.sh).
 */
export const SURFACES = {
  server: 'the server: both images (amd64 + arm64), the server archives and the update manifest (stable.json), the maintainers\' instances, the Helm chart',
  cli: "the CLI's package managers: winget BRFTech.filex and Homebrew filex",
  ui: 'the web interface and its packages: npm @brftech/*, the embedded explorer in the products that vendor it',
  desktop: 'the desktop app: its packages and update feeds, the Snap Store, the Microsoft Store, winget BRFTech.filex-app, Homebrew filex-app, AUR',
  docs: 'docs.filex.sh and the Releases page',
};
export const ALL_SURFACES = Object.keys(SURFACES);

/**
 * The files of backend/cmd/filex that are the server's own commands, not the
 * CLI's: main.go starts the server, settings.go is `filex settings reseed` on
 * the server's own configuration and database (#258), office_engine.go is the
 * process the server starts for an app's office engine (#268). What only they
 * reach is the server's.
 */
export const SERVER_COMMANDS = ['main.go', 'settings.go', 'office_engine.go'];

/**
 * The CLI's packages: every backend package the CLI's commands in
 * backend/cmd/filex (every file there but the server's own, SERVER_COMMANDS)
 * reach, directly or through another - client, sync, mount, vault,
 * decrypt, plugin validation, self-update, and what those stand on (the
 * model, the path and name rules, the storage and database layers plugin
 * validation opens). A change in one of them reaches the CLI and the desktop
 * app that carries it (its sync and mount run the CLI), besides the server
 * built from the same module. Any other backend package (the handlers,
 * search, previews, office, sharing, WebDAV, ...) is the server's alone: the
 * CLI binary links it, but no command of it runs it.
 *
 * ⚠ The closure, not the direct imports: a fix in syspath or regfile changes
 * how `filex sync` behaves though no command imports them by name, and a
 * server patch that left the CLI's package managers and the desktop app on
 * the old sync would be a silent split. In doubt, the CLI.
 * web/tests/deploy/releaseSurfaces.test.ts walks the imports and holds this
 * list to exactly that closure, so a new one is placed before a patch can
 * slip past it.
 */
export const CLI_PACKAGES = [
  'auth',
  'cliclient',
  'clientip',
  'compat',
  'dailycheck',
  'db',
  'e2e',
  'e2edecrypt',
  'filesync',
  'group',
  'httpx',
  'identity',
  'leafname',
  'listorder',
  'localname',
  'metrics',
  'model',
  'mountfs',
  'namefold',
  'netguard',
  'pathkey',
  'perm',
  'plugin',
  'pluginlog',
  'quota',
  'regfile',
  'rowgate',
  'secretbox',
  'space',
  'srvtext',
  'storage',
  'syspath',
  'tagname',
  'tenant',
  'tenanturl',
  'throughput',
  'tokenperm',
  'update',
  'version',
];

/**
 * Where a changed file lands, first match wins. `surfaces` [] is a file that
 * publishes nothing on its own (a test, the release tooling, a note).
 */
export const SURFACE_RULES = [
  // Nothing on its own: tests, fixtures, the maintainers' tooling and notes.
  // CHANGELOG.md is every release's (and is written by every one).
  {
    globs: [
      'CHANGELOG.md',
      '**/*_test.go',
      '**/testdata/**',
      'web/tests/**',
      'e2e/**',
      'desktop/test/**',
      'packages/*/test/**',
      'packages/*/tests/**',
      'scripts/**',
      '.gitlab-ci.yml',
      '.circleci/**',
      '.github/**',
      'packaging/ci/**',
      'packaging/store-e2e-lab/**',
      'plan/**',
      'sweep-*/**',
      'HANDOVER-*.md',
      'docs/handovers/**',
      'site/**',
      '.gitignore',
      '.gitattributes',
      '.editorconfig',
    ],
    surfaces: [],
  },
  // The server's database: a server change, and one a patch may not carry
  // (docs/UPDATES.md: a release with migrations is never applied unasked).
  { globs: ['backend/db/**'], surfaces: ['server'], migrations: true },
  { globs: ['docs/**', 'docs-site/**', 'README.md', 'README.*.md', 'SECURITY.md', 'CODE_OF_CONDUCT.md'], surfaces: ['docs'] },
  { globs: ['desktop/**'], surfaces: ['desktop'] },
  // The interface: embedded in the server, synced into the desktop app,
  // published to npm and vendored into the embeds.
  { globs: ['web/**', 'packages/**'], surfaces: ['server', 'ui', 'desktop'] },
  // The CLI's own code, and the module's dependencies: the server, the CLI and
  // the desktop app that carries the CLI.
  {
    globs: ['backend/cmd/**', 'backend/pkg/**', 'backend/go.mod', 'backend/go.sum', ...CLI_PACKAGES.map((p) => `backend/internal/${p}/**`)],
    surfaces: ['server', 'cli', 'desktop'],
  },
  { globs: ['backend/**', 'docker/**', 'deploy/**', 'docker-compose.yml'], surfaces: ['server'] },
];

/**
 * The surfaces a list of changed files reaches: { surfaces, unmatched,
 * migrations, byFile }. `surfaces` is every surface when a file matches no
 * rule (`unmatched` names them): in doubt, everything is published.
 */
export function affectedSurfaces(files) {
  const found = new Set();
  const unmatched = [];
  const byFile = {};
  let migrations = false;
  for (const f of files) {
    const rule = SURFACE_RULES.find((r) => r.globs.some((g) => globMatch(g, f)));
    if (!rule) {
      unmatched.push(f);
      continue;
    }
    byFile[f] = rule.surfaces;
    for (const s of rule.surfaces) found.add(s);
    if (rule.migrations) migrations = true;
  }
  const surfaces = unmatched.length ? [...ALL_SURFACES] : ALL_SURFACES.filter((s) => found.has(s));
  return { surfaces, unmatched, migrations, byFile };
}

/**
 * Whether a patch is a SERVER patch, which the `server` profile releases on
 * its own lane: a patch step (X.Y.Z+1), every changed file placed, nothing
 * but the server (and the documentation) reached, no migration. Returns
 * { ok, why } - `why` names what keeps it off the lane, or what it carries.
 */
export function serverPatch(analysis, { patchStep = true } = {}) {
  if (!patchStep) return { ok: false, why: 'not a patch step: a minor or a major is a release of every surface' };
  if (analysis.unmatched.length) return { ok: false, why: `${analysis.unmatched.length} changed file(s) no rule places (in doubt, every surface): ${analysis.unmatched.slice(0, 5).join(', ')}` };
  if (analysis.migrations) return { ok: false, why: 'it carries a migration: a patch never does (docs/UPDATES.md)' };
  const beyond = analysis.surfaces.filter((s) => s !== 'server' && s !== 'docs');
  if (beyond.length) {
    const files = Object.entries(analysis.byFile).filter(([, s]) => s.some((x) => beyond.includes(x))).map(([f]) => f);
    return { ok: false, why: `it reaches ${beyond.join(', ')} too: ${files.slice(0, 5).join(', ')}${files.length > 5 ? ', ...' : ''}` };
  }
  if (!analysis.surfaces.includes('server')) return { ok: false, why: 'nothing of the server changed' };
  return { ok: true, why: `only the server${analysis.surfaces.includes('docs') ? ' and the documentation' : ''} changed` };
}

/**
 * The surfaces a release publishes, from its record (state.json `surfaces`):
 * every one when the record names none (every release before #252).
 */
export function releaseSurfaces(state) {
  return Array.isArray(state?.surfaces) && state.surfaces.length ? state.surfaces : [...ALL_SURFACES];
}

/** Whether a gate or a ship step that names `surface` (or none: every release's) is this release's. */
export function publishes(surfaces, surface) {
  return !surface || surfaces.includes(surface);
}

/**
 * The line a server patch's tags carry, which release.yml's `plan` reads
 * (`surfaces: server`): the tag run then builds and publishes the server's
 * surface alone. A tag of every surface carries none.
 */
export const SURFACES_LINE = 'surfaces: server';

/**
 * The messages a release's tags carry, in order (`git tag -s <tag> -m <each>`):
 * the tag itself, and a server patch's SURFACES_LINE. The one place the sign
 * stage (scripts/release/stages.mjs) and the autonomous round
 * (scripts/train/auto.mjs) take them from.
 */
export function tagMessages(tag, { server = false } = {}) {
  return server ? [tag, SURFACES_LINE] : [tag];
}

/** The `surfaces:` line of a tag message, or '' (every surface). */
export function surfacesOfTagMessage(message) {
  const m = /^surfaces:[^\n]*$/m.exec(String(message ?? ''));
  return m ? m[0].trim() : '';
}
