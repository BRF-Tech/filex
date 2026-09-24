// Where the ACCOUNT palette meets OPERATOR themes — the defects that exist
// only in the merge.
//
// ⚠⚠ Every test in this file protects against a bug neither branch could have
// had on its own, which is why neither branch's suite covers any of it. Two
// features landed in the same release and each one's correctness depended on
// an assumption the other quietly broke:
//
//   • the palette became an ACCOUNT preference (migration 00047) — so
//     `setTheme` writes to the server, and a second device with an empty
//     localStorage is NOT a person without an opinion;
//   • an instance grew its own DEFAULT theme and an open list of palettes —
//     so "no stored key" had to start meaning "never chose", and an id can be
//     legitimately unknown while its list is still in flight.
//
// The merge compiled, typechecked and passed 1300 tests with three live bugs
// in it. That is the point of this file.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  DEFAULT_THEME_ID,
  PREF_LS_KEYS,
  SESSION_LS_KEY,
  THEME_LS_KEY,
  THEME_MODE_LS_KEY,
  applyStoredPalette,
  configurePrefs,
  currentPrefs,
  flushPrefs,
  forgetPersonalPrefs,
  hasSession,
  hydratePrefs,
  rememberSession,
  resetPrefs,
  resolveSessionLook,
  sealSessionless,
  setCustomThemes,
  setLocalPref,
  setTheme,
  setThemeMode,
  useThemeModeState,
  useThemeState,
} from '@brftech/filex-core';

import { applyInstanceThemes, applySessionLook, primeInstanceDefault } from '@/lib/instanceThemes';
import { getStoredTheme, setStoredTheme } from '@/lib/theme';
import { setDensity } from '@/lib/density';

const ACME = {
  id: 'custom:acme',
  name: 'Acme Bulut',
  light: { '--fe-primary': '#7d3cb5' },
  dark: { '--fe-primary': '#9b6fd4' },
};
const BETA = {
  id: 'custom:beta',
  name: 'Beta Kurumsal',
  light: { '--fe-primary': '#0f7a5a' },
  dark: { '--fe-primary': '#27a37c' },
};

/** What the server currently holds for this account. */
let stored: Record<string, string>;
/** Every PUT body the browser sent, in order. */
let writes: Record<string, string>[];

function mockPrefs(initial: Record<string, string> = {}) {
  stored = { ...initial };
  writes = [];
  const fetchImpl = vi.fn(async (_url: string, init?: { method?: string; body?: string }) => {
    if (init?.method === 'PUT') {
      // ⚠ The body is `{prefs: …}`, and the PUT REPLACES the document rather
      // than merging into it (handlers/userprefs.go). A mock that merged would
      // hide the data loss the real endpoint would cause.
      const doc = (JSON.parse(init.body ?? '{}').prefs ?? {}) as Record<string, string>;
      writes.push(doc);
      stored = { ...doc };
      return { ok: true, status: 200, json: async () => stored };
    }
    return { ok: true, status: 200, json: async () => stored };
  });
  // ⚠ `sessionAware` — the flag the filex SPA sets, and the one that turns
  // the signed-out rules on at all. Without it core keeps the behaviour an
  // EMBED needs (no sign-in page, so the person is always present) and every
  // assertion in the describes below would be measuring the wrong host.
  configurePrefs({
    surface: 'web',
    sessionAware: true,
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });
  return fetchImpl;
}

/** Settle the debounced PUT without waiting for real time to pass. */
async function settle() {
  await vi.runAllTimersAsync();
  await flushPrefs();
}

beforeEach(() => {
  resetPrefs();
  setCustomThemes([]);
  // ⚠ Reset the module singleton FIRST, wipe storage SECOND. `themeId` is
  // module-level state that outlives a test, and `applyStoredPalette` records
  // into the mirror on its way past — clearing before this call leaves the
  // key behind and every "somebody who has never chosen" test silently
  // becomes a "somebody who has" test.
  applyStoredPalette(DEFAULT_THEME_ID);
  localStorage.clear();
  // ⚠⚠ EVERY test in this file is about a SIGNED-IN person, and since
  // 2026-09-21 that has to be said out loud: with no session the palette and
  // the light/dark mode are not read from this browser at all (core's
  // `lib/prefs` → SESSION_LS_KEY), so a suite that stayed silent about it
  // would be testing the signed-OUT rules while claiming to test the account
  // ones — "does not override a choice made on ANOTHER device" would pass for
  // the wrong reason, because nothing would have read the choice in the first
  // place. The session-less rules have a describe of their own below.
  configurePrefs({ surface: 'web', sessionAware: true });
  rememberSession(true);
  resolveSessionLook();
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
  resetPrefs();
});

describe('the two fetches race, and the palette must survive either order', () => {
  // ⚠⚠ The account's answer and `GET /api/appearance` are independent
  // requests with no ordering between them. Before the repair,
  // `applyStoredPalette` validated through `themeById`, which cannot know an
  // operator theme until its list has landed — so whichever order put the
  // account first silently demoted the person to the stock palette AND wrote
  // that demotion into this browser's mirror. `setCustomThemes` then found the
  // selection already at the default and saw nothing to correct: the choice
  // was not restored, it was gone.
  it('account first, then the theme list', async () => {
    mockPrefs({ palette: ACME.id });
    const { themeId } = useThemeState();

    const prefs = await hydratePrefs();
    applyStoredPalette(prefs?.palette ?? '');
    // The list has NOT arrived. The id is unknown-but-plausible and is kept.
    expect(themeId.value).toBe(ACME.id);

    applyInstanceThemes({ themes: [ACME], default_theme_id: DEFAULT_THEME_ID });
    expect(themeId.value).toBe(ACME.id);
  });

  it('theme list first, then the account', async () => {
    mockPrefs({ palette: ACME.id });
    const { themeId } = useThemeState();

    applyInstanceThemes({ themes: [ACME], default_theme_id: DEFAULT_THEME_ID });
    const prefs = await hydratePrefs();
    applyStoredPalette(prefs?.palette ?? '');

    expect(themeId.value).toBe(ACME.id);
  });

  it('neither order sends anything back to the account', async () => {
    mockPrefs({ palette: ACME.id });

    const prefs = await hydratePrefs();
    applyStoredPalette(prefs?.palette ?? '');
    applyInstanceThemes({ themes: [ACME], default_theme_id: DEFAULT_THEME_ID });
    await settle();

    // ⚠ Painting what the server just told us is not a decision the person
    // made. A hydration that writes is a hydration that can overwrite the
    // other device.
    expect(writes).toEqual([]);
    expect(stored.palette).toBe(ACME.id);
  });
});

describe('choosing the stock palette is an answer, not the absence of one', () => {
  // ⚠⚠ `THEME_LS_KEY` and `PREF_LS_KEYS.palette` are the SAME string,
  // 'filex.palette'. `savePref` mirrors into local storage and `setLocalPref`
  // DELETES the key when handed ''. So the merged `setTheme` wrote the stock
  // id and then erased it one line later, and "deliberately chose the
  // product's own colours" collapsed back into "has never chosen" — the exact
  // state an instance default paints over, forever.
  it('setTheme(default) leaves a stored answer in both places', async () => {
    mockPrefs({});
    setTheme(DEFAULT_THEME_ID);
    await settle();

    expect(localStorage.getItem(THEME_LS_KEY)).toBe(DEFAULT_THEME_ID);
    expect(localStorage.getItem(PREF_LS_KEYS.palette)).toBe(DEFAULT_THEME_ID);
    expect(stored.palette).toBe(DEFAULT_THEME_ID);
  });

  it('and the instance default does not paint over it', async () => {
    mockPrefs({});
    const { themeId } = useThemeState();

    setTheme(DEFAULT_THEME_ID);
    await settle();
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });

    expect(themeId.value).toBe(DEFAULT_THEME_ID);
  });
});

describe('the instance default is the operator’s answer, never the person’s', () => {
  it('paints for somebody who has never chosen', () => {
    mockPrefs({});
    const { themeId } = useThemeState();

    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });

    expect(themeId.value).toBe(ACME.id);
  });

  it('records nothing — not the account, not this browser', async () => {
    mockPrefs({});

    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });
    await settle();

    // ⚠ Written to the account it would overwrite, on this device, a choice
    // made on another. Written to the mirror it would make `hasOwnChoice()`
    // true, so the day the operator picks a different house theme every
    // existing person stays pinned to the old one with no way to tell why.
    expect(writes).toEqual([]);
    expect(stored.palette).toBeUndefined();
    expect(localStorage.getItem(THEME_LS_KEY)).toBeNull();
  });

  it('does not override a choice made on ANOTHER device', async () => {
    // ⚠⚠ The sharpest of the three. This browser is brand new — empty
    // localStorage — but the person is not: they picked a palette on their
    // laptop. Reading "has an opinion" from localStorage alone made a second
    // device indistinguishable from a first-time visitor, so the instance
    // theme was applied and, through `setTheme`, WRITTEN back, destroying the
    // laptop's choice everywhere.
    mockPrefs({ palette: BETA.id });
    const { themeId } = useThemeState();

    const prefs = await hydratePrefs();
    applyStoredPalette(prefs?.palette ?? '');
    applyInstanceThemes({ themes: [ACME, BETA], default_theme_id: ACME.id });
    await settle();

    expect(themeId.value).toBe(BETA.id);
    expect(writes).toEqual([]);
    expect(stored.palette).toBe(BETA.id);
  });
});

describe('a deleted theme still falls back — by resolution, and quietly', () => {
  it('drops to the stock palette when the list no longer carries it', async () => {
    mockPrefs({ palette: ACME.id });
    const { themeId } = useThemeState();

    const prefs = await hydratePrefs();
    applyStoredPalette(prefs?.palette ?? '');
    applyInstanceThemes({ themes: [], default_theme_id: DEFAULT_THEME_ID });

    expect(themeId.value).toBe(DEFAULT_THEME_ID);
  });

  it('without writing the correction to the account', async () => {
    // ⚠ Correcting through `setTheme` would echo a fact the SERVER just told
    // us straight back at it as though the person had chosen it. That is
    // harmless only while the correction is right — and it is not always
    // right, because this browser's mirror can hold a stale id from before
    // the account was read. Leaving the dead id costs nothing: every read
    // resolves it to stock anyway, which is why deletion was built as
    // resolution rather than as a cleanup pass.
    mockPrefs({ palette: ACME.id });

    const prefs = await hydratePrefs();
    applyStoredPalette(prefs?.palette ?? '');
    applyInstanceThemes({ themes: [], default_theme_id: DEFAULT_THEME_ID });
    await settle();

    expect(writes).toEqual([]);
    expect(stored.palette).toBe(ACME.id);
  });

  it('and the person’s next deliberate pick writes a live id over it', async () => {
    mockPrefs({ palette: ACME.id });

    const prefs = await hydratePrefs();
    applyStoredPalette(prefs?.palette ?? '');
    applyInstanceThemes({ themes: [BETA], default_theme_id: DEFAULT_THEME_ID });
    setTheme(BETA.id);
    await settle();

    expect(stored.palette).toBe(BETA.id);
    expect(currentPrefs().palette).toBe(BETA.id);
  });
});

describe('signed OUT, the look is the INSTANCE’s — never a person’s', () => {
  // ⚠⚠ The owner's rule, verbatim (2026-09-21): "logoutluyken zaten seçtiğim
  // tema değil, şirketin default, yoksa filex'in default teması gelecek;
  // tarayıcı gece modundaysa gece modunda olacak ya da light mode."
  //
  // Measured before any of this was written, with vitest against the shipped
  // `packages/core/dist`: `filex.palette` = `night` and no session gave
  // `themeId` = "night" straight out of module load, `filex.thememode` = `dark`
  // gave "dark", and `applyInstanceThemes({default_theme_id: custom:acme})`
  // then left the selection on "night" — the operator's own house theme
  // suppressed by a key belonging to whoever signed in at this browser last.
  // On a shared machine that is the previous person's taste on the sign-in
  // page; on every machine it is a brand that never paints where a customer
  // first looks.

  /** A browser somebody signed in at, then walked away from. */
  function aBrowserSomebodyUsed() {
    localStorage.setItem(THEME_LS_KEY, 'night');
    localStorage.setItem(THEME_MODE_LS_KEY, 'dark');
    rememberSession(false);
    resolveSessionLook();
  }

  it('is already neutral at the FIRST PAINT, before any payload arrives', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    const { themeMode } = useThemeModeState();
    aBrowserSomebodyUsed();

    // ⚠⚠ NO `applyInstanceThemes` here, and that is the point. Every other
    // test in this describe is saved by `decidePalette`, which lays the
    // operator's answer over whatever the boot read produced — so they ALL
    // stayed green with the guard in `readStoredThemeId` deliberately removed
    // (measured 2026-09-21, the one sabotage in eleven that nothing caught).
    // `GET /api/appearance` is fired unawaited in `main.ts` and its failure is
    // swallowed, so this is the window the person actually looks at: the whole
    // of it on a slow network, and forever if the fetch never lands. The boot
    // read has to be right ON ITS OWN.
    expect(themeId.value).toBe(DEFAULT_THEME_ID);
    expect(themeMode.value).toBe('host');
  });

  it('paints the instance default over the stored personal palette', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    aBrowserSomebodyUsed();

    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });

    expect(themeId.value).toBe(ACME.id);
  });

  it('and filex’s own stock palette when the instance has set none', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    aBrowserSomebodyUsed();

    applyInstanceThemes({ themes: [], default_theme_id: DEFAULT_THEME_ID });

    // ⚠ Not "night". "No house theme" is still an answer, and it is the
    // product's own palette rather than somebody's leftover.
    expect(themeId.value).toBe(DEFAULT_THEME_ID);
  });

  it('never reads the stored light/dark mode either', () => {
    mockPrefs({});
    const { themeMode } = useThemeModeState();
    aBrowserSomebodyUsed();

    // `'host'` is the neutral answer: FileExplorer.vue falls back to
    // `config.theme || 'auto'`, and 'auto' is `prefers-color-scheme` — the
    // BROWSER decides, which is exactly what the rule asks for.
    expect(themeMode.value).toBe('host');
  });

  it('leaves the mirror on disk — unread is not erased', () => {
    mockPrefs({});
    aBrowserSomebodyUsed();
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });

    // ⚠⚠ Deleting it would look tidier and would break two things at once:
    // this person's next sign-in in this browser would flash the instance
    // default on the way to their own palette, and `hydratePrefs`' pre-v3
    // carry-over (an account that has never stored anything adopts what this
    // browser already had) would have nothing left to adopt.
    expect(localStorage.getItem(THEME_LS_KEY)).toBe('night');
    expect(localStorage.getItem(THEME_MODE_LS_KEY)).toBe('dark');
  });

  it('records nothing on the account — there is no account', async () => {
    mockPrefs({});
    aBrowserSomebodyUsed();
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });
    await settle();

    expect(writes).toEqual([]);
  });

  it('signing in brings the person’s own palette straight back', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    const { themeMode } = useThemeModeState();
    aBrowserSomebodyUsed();
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });
    expect(themeId.value).toBe(ACME.id);

    // ⚠ The other direction matters just as much. A guard that could only
    // take a palette away would mean every signed-in load flashes the
    // instance default before the account answers — the same bug pointing the
    // other way, and one the person meets on every single visit.
    rememberSession(true);
    applySessionLook();

    expect(themeId.value).toBe('night');
    expect(themeMode.value).toBe('dark');
  });

  it('a session that ends mid-visit takes the personal look back off', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    const { themeMode } = useThemeModeState();

    // Signed in and wearing their own choice…
    localStorage.setItem(THEME_MODE_LS_KEY, 'dark');
    resolveSessionLook();
    setTheme('night');
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });
    expect(themeId.value).toBe('night');

    // …then `/api/auth/me` answers "nobody" — an expired cookie, or a tab
    // closed instead of signed out. The window has ALREADY painted, so this
    // has to take the look back off rather than merely stop applying it.
    rememberSession(false);
    applySessionLook();

    expect(themeId.value).toBe(ACME.id);
    expect(themeMode.value).toBe('host');
    expect(hasSession()).toBe(false);
  });

  it('a public link is session-less whatever the browser remembers, and does not say so on disk', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    localStorage.setItem(THEME_LS_KEY, 'night');
    rememberSession(true);

    // `/s/` or `/d/`: a stranger with a token. ⚠ SEALED rather than
    // remembered — persisting "no session" here would reach the same
    // browser's admin tab through localStorage and make that tab flash.
    sealSessionless();
    applySessionLook();
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });

    expect(themeId.value).toBe(ACME.id);
    expect(localStorage.getItem(SESSION_LS_KEY)).toBe('1');
  });

  it('the APP’s light/dark falls back to the browser too', () => {
    mockPrefs({});
    // The other half of the same rule, and a different key: `filex.theme` is
    // the admin app's own light/dark (`web/src/lib/theme`), while
    // `filex.thememode` above is the explorer's. Both belong to a person.
    setStoredTheme('dark');
    expect(getStoredTheme()).toBe('dark');

    rememberSession(false);

    // ⚠ `'auto'` is not a third preference, it is the absence of one:
    // `effectiveTheme()` resolves it through `prefers-color-scheme`. Owner:
    // "tarayıcı gece modundaysa gece modunda olacak ya da light mode."
    expect(getStoredTheme()).toBe('auto');
    // ⚠ And the key is still there — unread, not erased, so signing back in
    // does not cost this person the mode they chose.
    expect(localStorage.getItem('filex.theme')).toBe('dark');
  });

  it('another tab’s palette change does not reach a signed-out window', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    aBrowserSomebodyUsed();
    applyInstanceThemes({ themes: [ACME, BETA], default_theme_id: ACME.id });

    // ⚠⚠ `storage` is broadcast to EVERY tab on the origin. Without the
    // session guard in core's listener, a signed-in tab picking a palette
    // would push that person's answer into the sign-in page open beside it —
    // the leak coming back through a side door after the front one was shut.
    window.dispatchEvent(
      new StorageEvent('storage', { key: THEME_LS_KEY, newValue: BETA.id }),
    );

    expect(themeId.value).toBe(ACME.id);
  });
});

describe('the ACCOUNT said nothing about the palette, and nothing is not an answer', () => {
  // ⚠⚠ This is e2e 98-custom-theme.spec.ts:97, which went red as a flake:
  // the browser holds `custom:e2e-acme`, the account document carries other
  // keys but no `palette`, and `App.vue` called
  // `applyStoredPalette(prefs.palette ?? '')`. `''` resolved to the stock id
  // and overwrote both the selection and this browser's mirror, so the polled
  // `--fe-primary` read won the race against the fetch and the unpolled
  // `--fe-bg` read one line later lost it.
  //
  // ⚠ The three preferences beside this one already had it right —
  // `applyAccountTheme`, `applyAccountDensity` and `applyPrefLocale` each
  // return early for a value the account does not carry. The palette was the
  // only one that treated silence as an instruction.

  it('hydration leaves what is ON SCREEN standing', async () => {
    mockPrefs({ locale: 'tr' });
    const { themeId } = useThemeState();
    setCustomThemes([ACME]);
    localStorage.setItem(THEME_LS_KEY, ACME.id);
    resolveSessionLook();
    expect(themeId.value).toBe(ACME.id);

    const prefs = await hydratePrefs();
    applyStoredPalette(prefs?.palette);

    expect(themeId.value).toBe(ACME.id);
  });

  it('but the MIRROR still follows the account, and that is deliberate', async () => {
    mockPrefs({ locale: 'tr' });
    setCustomThemes([ACME]);
    localStorage.setItem(THEME_LS_KEY, ACME.id);
    resolveSessionLook();

    await hydratePrefs();

    // ⚠⚠ `hydratePrefs` rewrites the whole mirror from the account's answer,
    // so a key the account does not carry ends up with no cached value. That
    // reads like a second half of the bug above and it is NOT — do not
    // "finish the fix" by making the mirror survive here.
    //
    // The mirror is a CACHE OF THE ACCOUNT and may not outlive what it caches.
    // A palette that is in this browser and not on the account can only have
    // got there two ways: a choice made before v3, when these preferences
    // lived in localStorage alone, or one whose PUT failed. The first is
    // already handled — `hydratePrefs` adopts this browser's whole document
    // when the account has stored NOTHING AT ALL, which is exactly the upgrade
    // case. Widening that to "any key the account happens to lack" was
    // considered and rejected: on a shared machine it writes the previous
    // person's palette into the NEXT person's account, on every device they
    // own, with nothing to tell them why. A narrow, one-visit loss after a
    // failed write is the smaller harm by a long way.
    //
    // What the person is LOOKING AT is not yanked out from under them, which
    // is the visible half and the one the test above pins.
    expect(localStorage.getItem(THEME_LS_KEY)).toBeNull();
  });

  it('and sends nothing back for a question it was not asked', async () => {
    mockPrefs({ locale: 'tr' });
    setCustomThemes([ACME]);
    localStorage.setItem(THEME_LS_KEY, ACME.id);
    resolveSessionLook();

    const prefs = await hydratePrefs();
    applyStoredPalette(prefs?.palette);
    await settle();

    // ⚠ ADOPTING it into the account was the other candidate, and it was
    // rejected: `hydratePrefs` already adopts this browser's document when the
    // account has stored NOTHING at all, and widening that to "any key the
    // account happens to lack" would let a shared browser's leftover mirror be
    // written into the next person's account — the same leak as the sign-in
    // page, moved somewhere it would outlive the browser. Leaving it local
    // costs nothing: it still paints here, and the person's first deliberate
    // pick propagates it everywhere.
    expect(writes).toEqual([]);
    expect(stored.palette).toBeUndefined();
  });

  it('an empty id is refused at the source, so no future caller can bring it back', () => {
    const { themeId } = useThemeState();
    setCustomThemes([ACME]);
    localStorage.setItem(THEME_LS_KEY, ACME.id);
    resolveSessionLook();

    applyStoredPalette('');
    applyStoredPalette(undefined);
    applyStoredPalette(null);

    expect(themeId.value).toBe(ACME.id);
  });

  it('but the stock id IS an answer and still demotes off a deleted theme', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    setCustomThemes([ACME]);
    localStorage.setItem(THEME_LS_KEY, ACME.id);
    resolveSessionLook();
    expect(themeId.value).toBe(ACME.id);

    // ⚠ `setCustomThemes` corrects a deleted theme by calling
    // `applyStoredPalette(DEFAULT_THEME_ID)`. A guard that refused the stock
    // id along with the empty one would silently disarm that whole path.
    setCustomThemes([]);

    expect(themeId.value).toBe(DEFAULT_THEME_ID);
  });
});

describe('the instance default is cached — a public fact, in a key of its own', () => {
  const CACHE_KEY = 'filex.instancetheme';

  it('the payload is remembered, tokens and all', () => {
    mockPrefs({});
    applyInstanceThemes({ themes: [ACME, BETA], default_theme_id: ACME.id });

    const cached = JSON.parse(localStorage.getItem(CACHE_KEY) ?? 'null');
    expect(cached.id).toBe(ACME.id);
    // ⚠⚠ The TOKENS, not just the id. `custom:` ids only ever resolve through
    // the network list, so an id-only cache would paint the stock palette on
    // the first frame of exactly the instances that have a brand to show.
    expect(cached.def.light['--fe-primary']).toBe(ACME.light['--fe-primary']);
  });

  it('and painted on the first frame, before any fetch', () => {
    mockPrefs({});
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });

    // A fresh load: registry empty, nothing selected, no session.
    setCustomThemes([]);
    applyStoredPalette(DEFAULT_THEME_ID);
    rememberSession(false);
    resolveSessionLook();
    const { themeId } = useThemeState();
    expect(themeId.value).toBe(DEFAULT_THEME_ID);

    primeInstanceDefault();

    expect(themeId.value).toBe(ACME.id);
  });

  it('records nothing personal while it does it', async () => {
    mockPrefs({});
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });
    localStorage.removeItem(THEME_LS_KEY);
    rememberSession(false);
    resolveSessionLook();

    primeInstanceDefault();
    await settle();

    // ⚠ The whole reason this may be cached at all is that it says nothing
    // about a person. If it left a `filex.palette` behind it would be
    // indistinguishable from one, and `hasOwnChoice()` would pin every
    // existing person to the day's house theme for good.
    expect(localStorage.getItem(THEME_LS_KEY)).toBeNull();
    expect(writes).toEqual([]);
  });

  it('is dropped when the operator clears the house theme', () => {
    mockPrefs({});
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });
    expect(localStorage.getItem(CACHE_KEY)).not.toBeNull();

    applyInstanceThemes({ themes: [ACME], default_theme_id: DEFAULT_THEME_ID });

    // ⚠ A stale cache nothing invalidates is worse than no cache: every
    // signed-out window in every browser would keep flashing a brand the
    // operator has taken down.
    expect(localStorage.getItem(CACHE_KEY)).toBeNull();
  });
});

describe('a sign-out takes this browser’s copy of the person with it', () => {
  // Owner, 2026-09-21: *"oturum kapanınca temizlersek localstorage'ı tamamız ya
  // o kısımda"*.
  //
  // ⚠⚠ AN "AND", NOT AN "INSTEAD OF". Clearing covers only the sign-outs the
  // app is told about — the button, and the `/api/auth/me` that answers
  // "nobody". It cannot cover a cookie that quietly expired between visits,
  // and the describe above is what covers those by making the keys UNREADABLE
  // rather than absent. Remove either half and the sign-in page goes back to
  // wearing whoever used this browser last, so both are pinned here.

  /** What signing out does, wherever it is learned. */
  function signOut() {
    forgetPersonalPrefs();
    applySessionLook();
  }

  it('drops every per-person mirror', () => {
    mockPrefs({});
    setTheme('night');
    setThemeMode('dark');
    setStoredTheme('dark');
    setDensity('compact');
    setLocalPref('locale', 'tr');

    signOut();

    expect(localStorage.getItem(THEME_LS_KEY)).toBeNull();
    expect(localStorage.getItem(THEME_MODE_LS_KEY)).toBeNull();
    expect(localStorage.getItem(PREF_LS_KEYS.theme)).toBeNull();
    expect(localStorage.getItem(PREF_LS_KEYS.density)).toBeNull();
    expect(localStorage.getItem(PREF_LS_KEYS.locale)).toBeNull();
    expect(localStorage.getItem(SESSION_LS_KEY)).toBeNull();
  });

  it('and keeps everything that is a fact about the MACHINE', () => {
    mockPrefs({});
    // ⚠ An allow-list, never `localStorage.clear()`. Each of these survives a
    // sign-out for a reason of its own:
    //   the instance default is PUBLIC and identical for everybody, and
    //     clearing it puts the sign-in page's flash straight back;
    //   "already dismissed" / "already asked" state exists so the next person
    //     at this machine is not nagged again;
    //   window furniture is this machine's ergonomics, not a look.
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });
    localStorage.setItem('filex.installPrompt.dismissed', '1');
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.sidenav', 'collapsed');

    signOut();

    expect(localStorage.getItem('filex.instancetheme')).not.toBeNull();
    expect(localStorage.getItem('filex.installPrompt.dismissed')).toBe('1');
    expect(localStorage.getItem('filex.tourDone')).toBe('1');
    expect(localStorage.getItem('filex.sidenav')).toBe('collapsed');
  });

  it('leaves the window on the instance’s look, not on a blank one', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    const { themeMode } = useThemeModeState();
    setTheme('night');
    setThemeMode('dark');
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });
    expect(themeId.value).toBe('night');

    signOut();

    // ⚠ Clearing storage on its own only changes what the NEXT load reads.
    // The window has already painted, so the look has to be taken off this
    // one too — the person is looking at it right now.
    expect(themeId.value).toBe(ACME.id);
    expect(themeMode.value).toBe('host');
    expect(getStoredTheme()).toBe('auto');
  });

  it('never posts the departed person’s preferences after they have gone', async () => {
    mockPrefs({});
    // A change made a moment before signing out is still sitting in the
    // debounce when the cookie stops existing.
    setTheme('night');

    signOut();
    await settle();

    // ⚠ `forgetPersonalPrefs` drops the queued PUT and the in-memory document
    // with it. Left alone, the timer fires a write with a departed person's
    // values against a session that no longer exists — and `currentPrefs()`
    // would go on answering `hasOwnChoice()` for somebody who has left.
    expect(writes).toEqual([]);
    expect(currentPrefs().palette).toBeUndefined();
  });

  it('reaches the OTHER tab, which is the one that cannot see the click', () => {
    mockPrefs({});
    const { themeId } = useThemeState();
    const { themeMode } = useThemeModeState();

    // This is tab B: signed in, wearing a personal palette, on an instance
    // that has a house theme of its own.
    localStorage.setItem(THEME_MODE_LS_KEY, 'dark');
    rememberSession(true);
    resolveSessionLook();
    setTheme('night');
    applyInstanceThemes({ themes: [ACME], default_theme_id: ACME.id });
    expect(themeId.value).toBe('night');

    // Tab A signs out. All that crosses between tabs is shared storage and
    // the `storage` event the browser raises from it.
    localStorage.removeItem(THEME_LS_KEY);
    localStorage.removeItem(THEME_MODE_LS_KEY);
    localStorage.removeItem(SESSION_LS_KEY);
    window.dispatchEvent(new StorageEvent('storage', { key: SESSION_LS_KEY, newValue: null }));

    // ⚠⚠ The palette and mode listeners CANNOT carry this: by the time they
    // run `hasSession()` is already false and they refuse the event by
    // design. Without a listener on the session key itself, tab B sat there
    // wearing a palette belonging to somebody who had just signed out, until
    // it was reloaded.
    expect(themeId.value).toBe(ACME.id);
    expect(themeMode.value).toBe('host');
  });
});

describe('a host with no sign-in page is not gated at all', () => {
  // ⚠⚠ THE REGRESSION THIS FLAG EXISTS TO PREVENT. `hasSession()` is the
  // right rule for the filex SPA and the wrong one everywhere else: an
  // explorer embedded in work.example.com or in the fishapp is mounted inside a
  // page the host already authenticated, the desktop app is past its pairing
  // screen, and none of them calls `configurePrefs` at all — measured
  // 2026-09-21, the only caller in the repo is `web/src/main.ts`. Gate them
  // and a palette somebody picks inside the embed simply stops applying on
  // the next load, which is a regression dressed as a fix.
  //
  // ⚠ `sessionAware` is a fact about the HOST declared once beside `surface`
  // and `base`, not a branch on which surface is running: one mechanism, one
  // code path, and a host that says nothing keeps what it has always had.

  it('reads the person’s palette and mode with no session hint anywhere', () => {
    configurePrefs({ surface: 'web' });
    localStorage.setItem(THEME_LS_KEY, 'night');
    localStorage.setItem(THEME_MODE_LS_KEY, 'dark');
    localStorage.removeItem(SESSION_LS_KEY);

    const { themeId } = useThemeState();
    const { themeMode } = useThemeModeState();
    resolveSessionLook();

    expect(hasSession()).toBe(true);
    expect(themeId.value).toBe('night');
    expect(themeMode.value).toBe('dark');
  });

  it('but a public link still seals itself shut', () => {
    configurePrefs({ surface: 'web' });
    localStorage.setItem(THEME_LS_KEY, 'night');
    localStorage.removeItem(SESSION_LS_KEY);

    // ⚠ `/s/` and `/d/` carry a token and nothing else, on ANY host. The
    // escape above is "nobody told us about sessions"; this is somebody
    // telling us, so it outranks it.
    sealSessionless();
    const { themeId } = useThemeState();
    resolveSessionLook();

    expect(hasSession()).toBe(false);
    expect(themeId.value).toBe(DEFAULT_THEME_ID);
  });
});
