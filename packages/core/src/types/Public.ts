/**
 * Public surfaces — what a visitor with a link, and no account, is told.
 *
 * ⚠⚠ ONE shell draws all of them (`components/public/PublicShell.vue`): a
 * share (`/s/<token>`), a file request (`/d/<token>`) and an app plugin's
 * page (a share whose `kind` is `app`). Before v3 the first two were HTML
 * that Go wrote by hand and the third was a Vue screen, so the PIN box of a
 * signature request looked nothing like the PIN box of a download and
 * neither carried the instance's own colours. The shapes below are the
 * seam: everything the shell needs to draw ANY of the three, and nothing
 * that depends on which one it is.
 *
 * ⚠⚠ MIRROR, not a second design. Every field here exists because it exists
 * in `backend/internal/api/handlers/public_api.go` under the same JSON name
 * (`PublicBranding`, `PublicShare`, `PublicNode`, `PublicApp`,
 * `PublicDrop`). When the Go side grows one, add it here with the same
 * spelling; when the two disagree, the Go file is right.
 */
import type { PluginText, PublicPageFile } from './Plugins';

/* ── branding ─────────────────────────────────────────────────────────── */

/**
 * `GET /api/public/branding` — unauthenticated, cached.
 *
 * ⚠ Deliberately NOT the payload `/api/branding` serves: that one also
 * carries the SSO button label, which a stranger has no business receiving.
 *
 * ⚠⚠ It used to carry the operator's custom stylesheet too, and that is the
 * sentence that was wrong here for a release: the sheet left `/api/branding`
 * for `GET /api/me/custom-css` behind auth, so no anonymous surface — this
 * one included — can be handed a stylesheet that repaints a sign-in form.
 */
export interface PublicBranding {
  /** The instance's own name. Empty = the product's. */
  name?: string;
  logo_url?: string;
  /** `#rgb` / `#rrggbb` — becomes `--fe-primary` on the public shell. */
  accent?: string;
  footer_text?: string;
  hide_powered_by?: boolean;
  /** The appearance a visitor gets before they choose: `system` | `light` | `dark`. */
  theme?: 'system' | 'light' | 'dark' | string;
  /** The instance's own default language. */
  locale?: string;
  /** The languages the public pages can be rendered in. */
  locales?: string[];
  /**
   * The languages installed apps add (`manifest.ui_locales`): one row each —
   * `code`, `source: "plugin"`, the `plugin` that brought it, `rtl` — and NO
   * `strings`. One language's strings are `GET /api/public/ui-locales/{code}`
   * (`lib/uiLocales.ensureLocaleStrings`), fetched when it is first used.
   *
   * ⚠⚠ Typed from the SERVER's bytes, not from belief: this field was a map
   * on the wire and a list here for a whole release, and no pack's string
   * ever reached a screen. web/tests/lib/uiLocales.test.ts reads
   * backend/internal/api/handlers/testdata/wire/public-branding.json, which
   * the Go handler's own test writes.
   */
  ui_locales?: PublicLocaleOption[];
}

/* ── the states every public link shares ──────────────────────────────── */

/** Why a link draws nothing, for a caller that logs it. */
export type PublicFailure = '' | 'not_found' | 'gone' | 'error';

/**
 * What every public link answers, whatever kind it is.
 *
 * ⚠ `expired` and `revoked` are BOTH reported, and they are different facts:
 * the clock ran out, versus the link is dead for another reason (its visit
 * ceiling is spent, the app behind it was stopped, the file is gone). The
 * shell shows one sentence for both — a visitor must not be told which —
 * but the distinction is the server's to make, not the client's to lose.
 *
 * ⚠ `locked` is NOT one of them. It is the PIN gate shut after too many
 * wrong answers, and it lifts by itself.
 */
export interface PublicLinkBase {
  needs_pin: boolean;
  unlocked: boolean;
  expired?: boolean;
  revoked?: boolean;
  locked?: boolean;
  /** RFC 3339, or absent for "no expiry". */
  expires_at?: string | null;
  /** Words for the shell's header — a subject line, a file name. */
  subject?: string;
}

/* ── a share: `/s/<token>` ────────────────────────────────────────────── */

/**
 * `kind` decides the BODY the shell draws and nothing else — the header,
 * the PIN gate, the language picker and the footer are the shell's.
 *
 *   file   → one document: download it
 *   folder → a folder, downloaded whole or walked
 *   app    → an app plugin's screen, drawn by SurfaceRenderer
 */
export type PublicShareKind = 'file' | 'folder' | 'app';

/**
 * The little a visitor learns about the thing behind a link: what it is
 * called, how big it is, what kind of thing it is. Never its path, never its
 * storage, never who owns it. Absent until the link is unlocked.
 */
export interface PublicNode {
  name: string;
  size?: number;
  mime?: string;
}

/** The app surface a link carries, once it is unlocked. */
export interface PublicApp {
  plugin: string;
  page: string;
  title?: PluginText;
  /** Copies the app exposed to this visitor (`pub:N`). */
  files?: PublicPageFile[];
}

/** One row of a folder share's listing, when the server sends one. */
export interface PublicEntry {
  name: string;
  /** Path relative to the share root, for the browse and download URLs. */
  path: string;
  is_dir: boolean;
  size?: number;
  mime?: string;
  /** Epoch seconds or milliseconds, as the rest of the wire sends them. */
  modified?: number;
  /** Where the browser downloads this entry from; built from the token when absent. */
  url?: string;
}

/** `GET /api/public/s/{token}`. */
export interface PublicShareInfo extends PublicLinkBase {
  kind: PublicShareKind;
  /** Visits left before the link stops answering; null = uncounted. */
  visits_left?: number | null;
  node?: PublicNode | null;
  app?: PublicApp | null;
  /**
   * A folder share's listing.
   *
   * ⚠ Not sent today — the server answers a folder share with its NAME and
   * leaves the walking to the no-JS page. Declared because the shell draws a
   * listing the moment one arrives, and because the alternative (a second
   * browse implementation in the SPA, reading a different endpoint) is the
   * thing this round exists to stop.
   */
  entries?: PublicEntry[];
  /** The path being listed, relative to the share root. */
  path?: string;
}

/* ── a file request: `/d/<token>` ─────────────────────────────────────── */

/** What a file request will accept. */
export interface PublicDropLimits {
  max_files?: number;
  /** Megabytes, as the server states it. */
  max_file_size_mb?: number;
  /** Lower-case extensions, no dot. Empty = anything. */
  allowed_ext?: string[];
  /** The uploader is asked for their name. */
  ask_name?: boolean;
}

/** `GET /api/public/d/{token}` — the drop box an outsider uploads into. */
export interface PublicRequestInfo extends PublicLinkBase {
  kind?: 'drop' | string;
  /** The destination's NAME — never its path, and never a listing. */
  folder?: string;
  /** How many files may still be dropped; null/absent = uncounted. */
  uploads_left?: number | null;
  limits?: PublicDropLimits;
}

/* ── languages ────────────────────────────────────────────────────────── */

/**
 * One entry of the language list the interface offers.
 *
 * ⚠ The list is NOT a constant any more (v3 §5). It is the product's own
 * languages plus whatever the server adds — including a language an app
 * plugin ships (`manifest.ui_locales`), which is marked with the app it came
 * from and leaves with it. A key the extra language does not carry falls
 * back to English, exactly as a missing key always has.
 */
export interface PublicLocaleOption {
  /** BCP-47 primary subtag, e.g. `en`, `tr`, `ar`. */
  code: string;
  /** The language's name IN that language ("Türkçe"), not in the viewer's. */
  label?: string;
  /** `builtin` = shipped with filex; `plugin` = added by an app. */
  source?: 'builtin' | 'plugin' | string;
  /** The app that ships it, when `source` is `plugin` — shown beside the name. */
  plugin?: string;
  /**
   * That language's strings by filex's own keys — absent on the branding
   * list, present once `ensureLocaleStrings` has fetched them.
   */
  strings?: Record<string, string>;
  /**
   * Written right to left (Arabic, Hebrew, Persian, Urdu, …): the page's
   * `dir` follows (lib/direction, docs/RTL.md).
   */
  rtl?: boolean;
}
