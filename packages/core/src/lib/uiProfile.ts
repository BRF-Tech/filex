/**
 * uiProfile — how much of the explorer to draw. Two names, and one rule for
 * everything that is not one of them.
 *
 * ⚠⚠ The profile is a REDUCTION switch, never a LOOK switch. The shell — the
 * header with its one search field, the filter row, "+ New", the Folders /
 * Files sections, the info panel's tabs, the storage line — is what filex IS;
 * nothing reads a profile to decide whether to draw it. `'simple'` turns OFF
 * the tab strip, the split pane, the gallery view mode and the panel's
 * "How to connect" / "API keys" entries, and removes nothing from the build.
 *
 * ⚠⚠ WHY THERE IS A RESOLVER AND NOT A UNION CHECK AT EACH CALL SITE. This
 * value comes from OUTSIDE: a `config` object typed by an embedder who may not
 * use TypeScript at all, or a `ui-profile="…"` attribute on a custom element,
 * which is a string the DOM will hand over unexamined. So "what does an
 * unrecognised value mean" is a real question with real users behind it, and
 * it is answered here, once, instead of by whatever each reader's `===` chain
 * happened to fall through to.
 *
 * THE ANSWER: `'simple'` and nothing else turns the reduction on. Every other
 * value — `'standard'`, `undefined`, a typo, a name a later version knows and
 * this one does not — is `'standard'`, the documented default, and a string we
 * did not recognise is said out loud once in the console.
 *
 * ⚠ WHICH RETIRES `'drive'`, deliberately and with its cost stated. It shipped
 * as a third profile in v0.32.0 and became an alias of `'simple'` in v0.40.0;
 * the owner's ruling on 2026-09-13 was to take it out ("kaldıralım direk").
 * filex is open source, so an embed somewhere may still be passing it, and
 * what that embed gets is now the FULL explorer rather than the reduced one —
 * it gains the tab strip, the split pane, the gallery mode and the connection
 * guides. That is a visible change in one direction and a one-word fix
 * (`uiProfile: 'simple'`), and it is the honest one: mapping the retired name
 * onto `'simple'` here would be the alias again, wearing a resolver's coat,
 * and it would also mean a plain typo silently REDUCED somebody's UI — a
 * failure that looks like features going missing and points at nothing. The
 * console line names the value so the fix takes a search rather than a bisect.
 */

/** The two profiles. There is no third, and no alias of either. */
export type UiProfile = 'standard' | 'simple';

/** Both of them, as data — so a doc, a test and a resolver cannot disagree
 *  about how many there are. */
export const UI_PROFILES: readonly UiProfile[] = ['standard', 'simple'] as const;

/** The one this package draws when nobody says otherwise. */
export const DEFAULT_UI_PROFILE: UiProfile = 'standard';

/* One line per distinct offending value, not one per render: this is read
 * inside a `computed` that re-runs whenever the config object changes. */
const warned = new Set<string>();

/**
 * The profile to draw for whatever the host passed.
 *
 * `undefined` / `null` / `''` are "nothing was passed" and resolve to the
 * default in silence. Any OTHER unrecognised value is a mistake worth a line
 * in the console, because it means somebody asked for something by name and
 * got something else.
 */
export function resolveUiProfile(value: unknown): UiProfile {
  if (value === 'simple') return 'simple';
  if (value === 'standard' || value === undefined || value === null || value === '') {
    return DEFAULT_UI_PROFILE;
  }
  const seen = typeof value === 'string' ? value : String(value);
  if (!warned.has(seen) && typeof console !== 'undefined') {
    warned.add(seen);
    console.warn(
      `[filex] unknown uiProfile ${JSON.stringify(seen)} — using "${DEFAULT_UI_PROFILE}". ` +
        `Valid values: ${UI_PROFILES.map((p) => `"${p}"`).join(', ')}. ` +
        `(The former "drive" profile was removed; pass "simple" for the reduced explorer.)`,
    );
  }
  return DEFAULT_UI_PROFILE;
}

/** Test seam — the console line is once per value per page, so a suite that
 *  asserts on it has to be able to start over. Not used by the app. */
export function __resetUiProfileWarnings(): void {
  warned.clear();
}
