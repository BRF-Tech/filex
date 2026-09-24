/**
 * tema:v1 — publishing the instance's own palettes into the shared registry.
 *
 * One function, because three callers need exactly the same three steps and a
 * copy of them in each would drift: the boot path in main.ts, the admin editor
 * after a save or a delete, and the unit test that pins the behaviour.
 */
import { watch } from 'vue';
import {
  DEFAULT_THEME_ID,
  SESSION_LS_KEY,
  THEME_LS_KEY,
  applyInstanceDefault,
  currentPrefs,
  hasSession,
  prefsHydrated,
  resolveSessionLook,
  setCustomThemes,
  syncThemeStyle,
  useThemeState,
  type ThemeDef,
} from '@brftech/filex-core';
import type { AppearancePayload } from '@/api/appearance';
import { applyStoredTheme } from '@/lib/theme';

/**
 * The instance default, cached for the FIRST PAINT of a window that has no
 * session.
 *
 * ⚠⚠ A separate key from `filex.palette` on purpose, and the separation is the
 * whole safety argument. `GET /api/appearance` is public and identical for
 * everybody, so what is written here is a fact about the SERVER — nothing about
 * whoever used this browser last. `filex.palette` is the opposite: one person's
 * answer, and the reason a signed-out window may not read it (lib/prefs →
 * SESSION_LS_KEY). Keeping the two in one key would have made "cache the public
 * fact" indistinguishable from "remember the person", which is the leak wearing
 * a different hat.
 *
 * ⚠ The whole THEME is stored, not just its id, and that is not belt and
 * braces. An operator theme's id is `custom:<slug>` and its token maps only
 * ever arrive over the network; `syncThemeStyle('custom:acme')` against an
 * empty registry resolves to nothing and paints the STOCK palette. So an
 * id-only cache would have removed no flash at all on precisely the instances
 * that have a brand to show — it would only have worked for the seven
 * built-ins.
 */
const INSTANCE_THEME_LS_KEY = 'filex.instancetheme';

interface CachedInstanceTheme {
  /** The default theme's id — a built-in's, or `custom:<slug>`. */
  id: string;
  /** Its tokens, when it is an operator theme the registry cannot know yet. */
  def?: ThemeDef;
}

function readInstanceCache(): CachedInstanceTheme | null {
  try {
    const raw = localStorage.getItem(INSTANCE_THEME_LS_KEY);
    if (!raw) return null;
    const v = JSON.parse(raw) as CachedInstanceTheme;
    return v && typeof v.id === 'string' && v.id ? v : null;
  } catch {
    // Blocked site data, or a shape written by another version. A cache that
    // cannot be read is a cache miss: the window paints stock for one fetch,
    // which is exactly what it did before this cache existed.
    return null;
  }
}

function writeInstanceCache(payload: AppearancePayload): void {
  try {
    const id = payload?.default_theme_id ?? DEFAULT_THEME_ID;
    if (!id || id === DEFAULT_THEME_ID) {
      // ⚠ REMOVED, not left behind. An operator who clears their house theme
      // would otherwise have every signed-out window in every browser keep
      // flashing the old brand on its first frame for as long as that entry
      // survived — a stale cache nothing invalidates is worse than no cache.
      localStorage.removeItem(INSTANCE_THEME_LS_KEY);
      return;
    }
    const def = (payload?.themes ?? []).find((t) => t.id === id);
    const entry: CachedInstanceTheme = def
      ? { id, def: { id: def.id, name: def.name, light: def.light ?? {}, dark: def.dark ?? {} } }
      : { id };
    localStorage.setItem(INSTANCE_THEME_LS_KEY, JSON.stringify(entry));
  } catch {
    /* quota / private mode — a cache is never worth an exception */
  }
}

/**
 * Paint the cached instance default, before any network call.
 *
 * ⚠ Only for a window with no session, and `main.ts` is what decides that. A
 * signed-in person's own palette comes out of `filex.palette` on the same
 * frame and outranks the operator's standing answer, so priming over it would
 * put the flash back, pointing the other way.
 *
 * ⚠⚠ It seeds the registry with ONE theme it did not get from the server. That
 * is deliberate and it is bounded: `setCustomThemes` is called again with the
 * real list a moment later and replaces this wholesale, AND it re-resolves the
 * active selection — so a theme the operator has deleted since this cache was
 * written drops to the stock palette by the same path a deleted theme always
 * takes. The one window in which `allThemes()` lists a cached theme is a
 * signed-out one, where nothing draws a gallery.
 */
export function primeInstanceDefault(): void {
  const cached = readInstanceCache();
  if (!cached) return;
  if (cached.def) setCustomThemes([cached.def]);
  applyInstanceDefault(cached.id);
  startThemePainting();
}

/**
 * The payload as it was last seen, so the decision below can be re-taken when
 * the OTHER input to it changes.
 *
 * ⚠ The two inputs arrive in either order and neither waits for the other:
 * `GET /api/appearance` is fired unawaited in `main.ts`, and the session answer
 * comes from `/api/auth/me` in `App.vue`. Whichever lands second has to be able
 * to re-run the whole decision, or the window keeps whatever the first one
 * implied.
 */
let lastPayload: AppearancePayload | null = null;

/**
 * Install the payload from `GET /api/appearance`.
 *
 * Two things happen, and the order matters:
 *
 *  1. The custom palettes are published. `setCustomThemes` ALSO re-resolves
 *     the active selection against the list it is handed, which is the client
 *     half of "deleting a theme puts anybody using it back on the default": a
 *     browser still holding `custom:acme` in localStorage after that theme was
 *     deleted drops to the stock palette here, without anything having had to
 *     go and find it.
 *  2. The INSTANCE DEFAULT is applied — but only to somebody who has not
 *     chosen for themselves, and to EVERYBODY who has no session (see
 *     `decidePalette`).
 *
 * ⚠⚠ Step 2 must never overwrite a real choice. The instance default answers
 * "what does somebody see before they have an opinion", not "what does
 * everybody see" — an operator changing it must not silently reset the palette
 * of every person who had already picked one.
 *
 * ⚠ "Has an opinion" is NOT read from the resolved id, because the two differ
 * in exactly the case that matters: somebody who deliberately picked the stock
 * palette on an instance whose default is a brand theme. Their resolved id is
 * the stock id, same as a brand-new browser's — only a STORED answer tells the
 * two apart. (This is why `setTheme` writes the stock id instead of clearing
 * the key.)
 *
 * ⚠⚠ And it is read from the ACCOUNT as well as this browser — a merge repair,
 * because the palette became an account preference in the same release that
 * gave instances a default, and the two features collide precisely here. A
 * person picks a brand-free palette on their laptop; they open filex on their
 * phone, where local storage is empty; `hasOwnChoice()` says "no opinion"; the
 * instance default is applied — and, when it was applied through `setTheme`,
 * WRITTEN to the account, overwriting on every device the choice they had just
 * made. Which is why step 2 now goes through `applyInstanceDefault`, which
 * paints and records nothing at all.
 */
export function applyInstanceThemes(payload: AppearancePayload): void {
  lastPayload = payload;
  writeInstanceCache(payload);

  setCustomThemes(
    (payload?.themes ?? []).map((t) => ({
      id: t.id,
      name: t.name,
      light: t.light ?? {},
      dark: t.dark ?? {},
    })),
  );

  decidePalette();

  startThemePainting();
}

/**
 * Whose palette does this window wear — the person's, or the instance's?
 *
 * ⚠⚠ With no session there is no person, so the operator's answer is the only
 * one there is and it is applied UNCONDITIONALLY. `hasOwnChoice()` is not even
 * consulted: a stored key belongs to whoever was signed in at this browser
 * last, and the whole point of the owner's rule is that a signed-out window
 * does not wear them. `applyInstanceDefault` accepts `default` too, so an
 * instance with no house theme lands on filex's own stock palette rather than
 * on somebody's leftover.
 */
function decidePalette(): void {
  const instanceDefault = lastPayload?.default_theme_id ?? DEFAULT_THEME_ID;
  if (!hasSession()) {
    applyInstanceDefault(instanceDefault);
    return;
  }
  if (instanceDefault !== DEFAULT_THEME_ID && !hasOwnChoice()) {
    applyInstanceDefault(instanceDefault);
  }
}

/**
 * `/api/auth/me` has answered, or this document turned out to be a public
 * link: re-decide the whole look.
 *
 * ⚠⚠ THE ORDER IS LOAD-BEARING. `resolveSessionLook()` re-reads
 * — or, with no session, stops reading — the PERSON's palette and light/dark
 * mode; `decidePalette()` then lays the operator's standing answer over the top
 * when there is nobody to have an opinion. Run the other way round, the
 * instance default is applied and immediately reset to the stock id, and the
 * sign-in page goes back to being unbranded — which is the exact failure
 * `startThemePainting` below was written for.
 *
 * ⚠ And `applyStoredTheme()` last, for the third thing the session decides:
 * `<html class="dark">`. `getStoredTheme()` answers `'auto'` once there is
 * nobody to have a preference, which `effectiveTheme()` resolves through
 * `prefers-color-scheme` — miss this call and a browser whose last user liked
 * dark keeps a dark sign-in page on a light desktop.
 *
 * ⚠⚠ ONE exported function rather than three calls at each site, so the set
 * cannot drift apart. Every caller means the same thing by it: "the session
 * answer changed, re-decide everything it decides."
 */
export function applySessionLook(): void {
  resolveSessionLook();
  decidePalette();
  startThemePainting();
  applyStoredTheme();
}

/**
 * ⚠⚠ ANOTHER TAB SIGNED OUT (or in). Two tabs open, sign out in one: the
 * other tab never sees the click, and `storage` — raised by
 * `forgetPersonalPrefs`' write to `filex.session` — is the only signal that
 * crosses between them. Without this listener that tab went on wearing the
 * palette of somebody who had just left, until it was reloaded, which is
 * precisely the state a sign-out exists to end.
 *
 * ⚠⚠ Core's own palette and mode listeners CANNOT carry it, and that is not
 * an oversight: by the time they run `hasSession()` is already false and they
 * refuse the event by design (`lib/themes`), which is what stops a signed-in
 * tab pushing its person's palette into a signed-out one. The session key
 * therefore needs a listener of its own.
 *
 * ⚠ ONE listener, here, rather than one here and one in the package. The
 * package's would have had to run first and this one second, and nothing but
 * import order would have said so — `applySessionLook` already does both
 * halves in a written-down order, so there is nothing for a second listener to
 * add except a way for the two to disagree.
 */
if (typeof window !== 'undefined') {
  try {
    window.addEventListener('storage', (e) => {
      if (e.key !== SESSION_LS_KEY) return;
      applySessionLook();
    });
  } catch {
    /* non-browser env */
  }
}

/**
 * Keep the singleton `<style data-filex-theme>` in step with the selection,
 * for the whole app rather than for the explorer alone.
 *
 * ⚠⚠ WITHOUT THIS THE LOGIN PAGE IS NEVER PAINTED, and that was measured
 * rather than guessed: `syncThemeStyle` had only one caller, a watcher inside
 * `FileExplorer.vue`, so the palette existed wherever an explorer was mounted
 * and nowhere else. The registry was correctly populated on /admin/login and
 * `--fe-primary` still computed to the stock blue, because nothing had
 * emitted the stylesheet. For a feature whose whole point is "make filex look
 * like OUR product", the sign-in page is close to the worst surface to miss —
 * it is the first one a customer sees.
 *
 * ⚠ Idempotent and safe beside the explorer's own watcher: `syncThemeStyle`
 * maintains ONE element keyed on an attribute and rewrites its text only when
 * the text changes.
 */
let painting = false;
function startThemePainting(): void {
  if (painting) return;
  painting = true;
  const { themeId } = useThemeState();
  watch(themeId, (id) => syncThemeStyle(id), { immediate: true });
}

/**
 * Whether this PERSON carries a deliberate palette choice.
 *
 * ⚠⚠ The account first, this browser second — and the order is the whole
 * point. The hook left here for `user_prefs` has been taken: that table
 * (migration 00047) and `GET,PUT /api/me/prefs` arrived in the same release as
 * instance defaults, so the choice is per person now, not per browser. Reading
 * only localStorage would have meant that opening filex on a second device —
 * where the mirror is empty — looked exactly like never having chosen, and the
 * instance's brand theme would be applied over a preference the person had
 * already expressed elsewhere.
 *
 * ⚠ localStorage stays as the SECOND reading rather than being dropped,
 * because a fetch cannot beat the first frame: until `hydratePrefs` answers,
 * the mirror is the only evidence this browser has, and without it the window
 * flashes the instance default on its way to the person's own palette. When
 * the account has not answered yet (`prefsHydrated()` false), the mirror is
 * all there is and is trusted; once it has answered, the account is the truth.
 *
 * ⚠⚠ ONLY EVER REACHED WITH A SESSION — `decidePalette` asks `hasSession()`
 * first. Without that guard this function IS the leak: the mirror carries
 * whoever signed in at this browser last, so reading it for a signed-out
 * window both wore their palette on the sign-in page and suppressed the
 * operator's own default (measured 2026-09-21). Do not call it from anywhere
 * that has not already established that somebody is signed in.
 *
 * ⚠ "Chose the stock palette" is a real answer and must read as an opinion,
 * which is why both readings test for PRESENCE rather than for a non-empty
 * value, and why nothing may store '' for the palette any more (see
 * `setTheme`).
 *
 * ⚠ The deleted-theme FALLBACK needs no change at all: `setCustomThemes`
 * resolves whatever id it is handed against the themes that exist, wherever
 * that id came from. That is the whole reason deletion was built as
 * resolution rather than as a cleanup pass.
 */
function hasOwnChoice(): boolean {
  if (prefsHydrated() && (currentPrefs().palette ?? '') !== '') return true;
  try {
    return localStorage.getItem(THEME_LS_KEY) !== null;
  } catch {
    return false;
  }
}
