/**
 * prefs — the preferences that live on the ACCOUNT, set the way a spec must.
 *
 * ⚠⚠ WHY THIS EXISTS. The explorer's view mode, its sort and its columns used
 * to be `localStorage` and nothing else, so a spec could seed
 * `brf-file-explorer:view-mode` before the first navigation and be done. They
 * are stored on the USER now — one JSON document behind
 * `GET/PUT /api/files/manager/view-prefs` (`packages/core/src/lib/viewPrefs.ts`,
 * `backend/internal/api/handlers/viewprefs.go`) — and the browser keys are a
 * FIRST-PAINT CACHE of it. The explorer paints from the cache and then
 * overwrites it the moment the account's document lands.
 *
 * A spec that only seeds `localStorage` therefore measures a race: the page
 * opens in the mode it asked for and drops into whatever the admin account
 * last looked at a few hundred milliseconds later. Measured 2026-09-20: both
 * grid cases of `102-touch-tap-opens` red with "`.fe-grid__card` not found",
 * on a build whose grid was perfectly fine — the listing was a list, because
 * that is what the account said.
 *
 * ⚠ The same trap is waiting for every other preference that moved onto the
 * account: theme, palette, density and the interface language
 * (`packages/core/src/lib/prefs.ts`, `GET/PUT /api/me/prefs?surface=web`).
 * Set those on the account too — never by writing their mirror keys and
 * hoping.
 */
import { expect, type APIRequestContext } from '@playwright/test';

export type ViewMode = 'list' | 'grid' | 'gallery';

/** The explorer's view-mode mirror — the key the FIRST PAINT reads. */
export const VIEW_MODE_LS_KEY = 'brf-file-explorer:view-mode';

/** The endpoint the per-user view-prefs document lives behind. */
const VIEW_PREFS_URL = '/api/files/manager/view-prefs';

/**
 * The stored document, or `{}` when the account has never arranged anything.
 *
 * ⚠ Read-modify-write, never a bare PUT of `{g:{…}}`: the endpoint stores an
 * OPAQUE object and the document also carries the per-folder map, the column
 * state and other features' namespaced slots. A spec that replaced it wholesale
 * would silently wipe whatever the spec before it set up.
 */
async function readDoc(request: APIRequestContext): Promise<Record<string, unknown>> {
  const res = await request.get(VIEW_PREFS_URL);
  if (!res.ok()) return {};
  const body = (await res.json()) as { prefs?: unknown };
  const doc = body?.prefs;
  return doc && typeof doc === 'object' && !Array.isArray(doc) ? (doc as Record<string, unknown>) : {};
}

async function writeDoc(request: APIRequestContext, doc: Record<string, unknown>) {
  const res = await request.put(VIEW_PREFS_URL, { data: { prefs: doc } });
  expect(res.ok(), `view-prefs PUT failed: ${res.status()} ${await res.text()}`).toBe(true);
}

/**
 * Make every folder open in `view` for this account, and wait for the write.
 *
 * `request` has to be authenticated as the account the page is signed in as —
 * `page.request` after `loginAs(page)` is the cheapest way to be sure, because
 * it rides the browser context's own cookie jar.
 *
 * ⚠ `u` is the document's "last written" stamp in epoch MILLISECONDS, and
 * `lib/viewPrefs` uses it to decide whose copy is newer. Bumping it is what
 * makes this write beat anything a page mounted a moment ago is holding.
 *
 * ⚠⚠ The document's shape changed (2026-09-21, "a view change belongs to the
 * folder it was made in"). There is no global `g` any more — the old "last
 * click anywhere" is retired and NOT read — and per-folder memory is always
 * on. What an untouched folder opens as is the person's DEFAULT, `p`. So this
 * writes `p.v` and forgets every folder's own memory (`f: {}`), exactly what
 * the explorer's "Apply to all folders" does: a folder an earlier spec
 * arranged OUTRANKS the default by design, and leaving it would be a race.
 * Written the old way (`g`), the helper did nothing at all, and both grid
 * cases of 102 went red on a grid that was fine.
 */
export async function setAccountViewMode(request: APIRequestContext, view: ViewMode): Promise<void> {
  const doc = await readDoc(request);
  const p = (doc.p && typeof doc.p === 'object' && !Array.isArray(doc.p) ? doc.p : {}) as Record<string, unknown>;
  const { g: _retired, ...rest } = doc;
  void _retired;
  await writeDoc(request, { ...rest, on: true, f: {}, p: { ...p, v: view }, u: Date.now() });
}
