/**
 * actionIcons — menu/toolbar ACTION key → inline SVG icon mapping.
 *
 * The sibling of `lib/fileIcons.ts`, for the other half of the problem: that
 * module draws what a row IS (a folder, a sheet, an archive), this one draws
 * what a row DOES (open, rename, move to trash).
 *
 * Why it exists: the action rows used to carry emoji (`↗ 👁 ⬇ 🗑 …`). Emoji are
 * not one icon set — they are a dozen sets, rendered by whatever font the OS
 * happens to ship, at whatever weight and colour that font's designer chose.
 * Two rows of the same menu could come out flat-grey and full-colour, one
 * hairline and one bold, and none of them matched the file glyphs drawn four
 * pixels to the left. A stroked set on `currentColor` gives the menu one voice
 * and lets the row's own colour reach the icon — which is how the destructive
 * entry, and only the destructive entry, comes out red.
 *
 * Conventions are `fileIcons.ts`'s, deliberately: static strings (so `v-html`
 * is injecting markup this file wrote, never user input), one cache, 24×24
 * viewBox, 1.6 stroke, round caps/joins, `currentColor`.
 *
 * Adding a key: draw it here. Nothing else has to change — the renderers ask
 * for the icon by the action's own key, so a new action gets its icon the
 * moment its glyph lands in GLYPHS, and an action with no glyph renders
 * nothing (the caller falls back to whatever `icon` string it was handed, so
 * an embedder's custom emoji still shows).
 */

/* Shared fragments — the arrow/tray pair reads as one family when download and
 * upload sit two rows apart. */
const TRAY = '<path d="M4.5 15.5v3A2 2 0 0 0 6.5 20.5h11a2 2 0 0 0 2-2v-3"/>';
const EYE =
  '<path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z"/>' +
  '<circle cx="12" cy="12" r="3"/>';
const STAR_PATH =
  'M12 3.6l2.6 5.3 5.85.85-4.23 4.12 1 5.83L12 16.97l-5.22 2.73 1-5.83L3.55 9.75l5.85-.85z';
/* The push-pin, shared by the two "keep on this device" states. */
const PIN =
  '<path d="M8 3.5h8"/>' +
  '<path d="M9.6 3.5v6.1L7.2 12.3v1.7h9.6v-1.7L14.4 9.6V3.5"/>' +
  '<path d="M12 14v6.5"/>';
/* A window/panel with one edge split off — nav and inspector are the same
 * shape mirrored, which is exactly what the two panels are. */
const PANEL = '<rect x="3.5" y="4.5" width="17" height="15" rx="2"/>';
/* The bin. `delete` moves a thing INTO it and `open-trash` opens it; those are
 * two actions on one object, so they are one drawing (Drive and Finder draw
 * them the same way too). */
const BIN =
  '<path d="M4 6.5h16"/>' +
  '<path d="M9.5 6.5V4.9a1.4 1.4 0 0 1 1.4-1.4h2.2a1.4 1.4 0 0 1 1.4 1.4v1.6"/>' +
  '<path d="M6.6 6.5l.85 12.1a2 2 0 0 0 2 1.9h5.1a2 2 0 0 0 2-1.9l.85-12.1"/>' +
  '<path d="M10.3 10.3v6.4"/><path d="M13.7 10.3v6.4"/>';
/* The ribbon a saved search hangs on — `bookmark` is the saved thing,
 * `save-search` is the same ribbon with the "add" plus in it. */
const BOOKMARK =
  '<path d="M6.8 3.5h10.4a1.6 1.6 0 0 1 1.6 1.6v15.4l-6.8-3.9-6.8 3.9V5.1A1.6 1.6 0 0 1 6.8 3.5z"/>';

const GLYPHS: Record<string, string> = {
  /* ── opening ─────────────────────────────────────────────────────────── */
  open:
    '<path d="M14 3.5h6.5V10"/>' +
    '<path d="M20.5 3.5L12 12"/>' +
    '<path d="M18 13.5v5a2 2 0 0 1-2 2H5.5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h5"/>',
  'open-tab':
    '<rect x="3" y="4.5" width="18" height="15" rx="2"/>' +
    '<path d="M3 9.5h18"/>' +
    '<path d="M9.5 4.5v5"/>',
  preview: EYE,
  download: TRAY + '<path d="M12 4v11.5"/><path d="M7.5 11L12 15.5 16.5 11"/>',
  'archive-create':
    '<path d="M5 8.5h14v11H5z"/><path d="M4 4h16v4.5H4z"/>' +
    '<path d="M12 11v5"/><path d="M9.5 13.5h5"/>',
  'archive-extract':
    '<path d="M5 8.5h14v11H5z"/><path d="M4 4h16v4.5H4z"/>' +
    '<path d="M12 10.5v6"/><path d="M9.5 14l2.5 2.5 2.5-2.5"/>',
  upload:
    '<path d="M4.5 15.5v3A2 2 0 0 0 6.5 20.5h11a2 2 0 0 0 2-2v-3"/>' +
    '<path d="M12 15.5V4"/><path d="M7.5 8.5L12 4l4.5 4.5"/>',
  convert:
    '<path d="M4 8.5h13"/><path d="M13.5 5L17 8.5 13.5 12"/>' +
    '<path d="M20 15.5H7"/><path d="M10.5 12L7 15.5 10.5 19"/>',
  /* "Paylaş / İzinler" — the three-node share, not a chain link: this row
     opens permissions as well as a link, and people are the subject. */
  access:
    '<circle cx="17.5" cy="6" r="2.5"/>' +
    '<circle cx="6.5" cy="12" r="2.5"/>' +
    '<circle cx="17.5" cy="18" r="2.5"/>' +
    '<path d="M8.75 10.8l6.5-3.6"/><path d="M8.75 13.2l6.5 3.6"/>',
  details:
    '<circle cx="12" cy="12" r="8.5"/>' +
    '<path d="M12 11.2v5.6"/>' +
    '<circle cx="12" cy="7.7" r="0.9" fill="currentColor" stroke="none"/>',
  /* ── identity / clipboard ────────────────────────────────────────────── */
  'copy-id':
    '<rect x="2.5" y="5" width="19" height="14" rx="2.5"/>' +
    '<circle cx="8.5" cy="10.8" r="2"/>' +
    '<path d="M5.3 16.2c.5-1.5 1.75-2.4 3.2-2.4s2.7.9 3.2 2.4"/>' +
    '<path d="M14.8 10h4.2"/><path d="M14.8 13.6h4.2"/>',
  /* A clipboard carrying a path separator — this copies the location, not the
     thing that lives at it. */
  'copy-path':
    '<path d="M9 4.5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-12a2 2 0 0 0-2-2h-2"/>' +
    '<rect x="9" y="2.8" width="6" height="3.6" rx="1.2"/>' +
    '<path d="M10 11.2l2.4 2.4-2.4 2.4"/>',
  rename: '<path d="M4 20l.9-4.1 11-11a2.05 2.05 0 0 1 2.9 2.9l-11 11z"/><path d="M14 7l3 3"/>',
  cut:
    '<circle cx="6.5" cy="17.5" r="2.5"/>' +
    '<circle cx="6.5" cy="6.5" r="2.5"/>' +
    '<path d="M8.6 8.3L19 19"/><path d="M8.6 15.7L19 5"/>',
  copy:
    '<rect x="9" y="9" width="11.5" height="11.5" rx="2"/>' +
    '<path d="M15 6v-.5a2 2 0 0 0-2-2H5.5a2 2 0 0 0-2 2V13a2 2 0 0 0 2 2H6"/>',
  paste:
    '<path d="M9 4.5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-12a2 2 0 0 0-2-2h-2"/>' +
    '<rect x="9" y="2.8" width="6" height="3.6" rx="1.2"/>',
  /* ── metadata ────────────────────────────────────────────────────────── */
  /* star = "not starred yet, do it" (outline); unstar = the state you are in
     (filled). The renderer picks between them by name, see FileExplorer. */
  star: `<path d="${STAR_PATH}"/>`,
  unstar: `<path d="${STAR_PATH}" fill="currentColor"/>`,
  tags:
    '<path d="M3.5 11.4V5.2a1.7 1.7 0 0 1 1.7-1.7h6.2a1.7 1.7 0 0 1 1.2.5l7.2 7.2a1.7 1.7 0 0 1 0 2.4l-6.2 6.2a1.7 1.7 0 0 1-2.4 0L4 12.6a1.7 1.7 0 0 1-.5-1.2z"/>' +
    '<circle cx="8" cy="8" r="1.3"/>',
  /* ── destructive / recovery ──────────────────────────────────────────── */
  delete: BIN,
  'open-trash': BIN,
  /* The undo arrow, not a rotating one: "Geri Yükle" puts a thing back where
     it was, and that is the gesture people already read as undo. */
  restore: '<path d="M9 6L5 10l4 4"/><path d="M5 10h9a5 5 0 0 1 0 10h-3.5"/>',
  /* ── folder-level ────────────────────────────────────────────────────── */
  'new-folder':
    '<path d="M3 7.5a2 2 0 0 1 2-2h4.2a2 2 0 0 1 1.4.6l1.4 1.4H19a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>' +
    '<path d="M12 11.3v5"/><path d="M9.5 13.8h5"/>',
  /* belge:n1 — a page with a plus: "make me a new document of some type".
     Keyed by the action's own key, so the menu row carries no icon of its
     own and no emoji enters the markup. */
  'new-document':
    '<path d="M13.5 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h5"/><path d="M13.5 3l5 5"/>' +
    '<path d="M13.5 3v5h5"/><path d="M17.5 14.5v6"/><path d="M14.5 17.5h6"/>',
  refresh: '<path d="M20.5 12a8.5 8.5 0 1 1-2.49-6.01"/><path d="M18 1.5V6h-4.5"/>',
  /* ── desktop sync ("keep on this device") ────────────────────────────── */
  'keep-local': PIN,
  'keep-online':
    '<path d="M7 18.5h10a3.5 3.5 0 0 0 0-7 5 5 0 0 0-9.6-1.2A4.2 4.2 0 0 0 7 18.5z"/>',
  /* Kept because a parent folder is kept — the pin, with the branch it came
     down. The row is disabled; the glyph says why. */
  'keep-inherited': PIN + '<path d="M2.5 20.5h3.6a1.6 1.6 0 0 0 1.6-1.6V17"/>',
  'keep-reveal':
    '<path d="M2.5 17.5V6.5a2 2 0 0 1 2-2h3.8a2 2 0 0 1 1.4.6l1.4 1.4h4.4a2 2 0 0 1 2 2v1.8"/>' +
    '<path d="M4 19.5h13.4a2 2 0 0 0 1.9-1.37l2-6A1.4 1.4 0 0 0 20 10.3H9.4a2 2 0 0 0-1.9 1.37L5.5 18"/>',
  /* ── view preferences ────────────────────────────────────────────────── */
  'toggle-hidden':
    '<path d="M3.5 3.5l17 17"/>' +
    '<path d="M9.9 6A9.6 9.6 0 0 1 12 5.5c6 0 9.5 6.5 9.5 6.5a17.3 17.3 0 0 1-3.35 4.15"/>' +
    '<path d="M6.55 7.7A17.4 17.4 0 0 0 2.5 12S6 18.5 12 18.5a9.7 9.7 0 0 0 3.95-.85"/>' +
    '<path d="M10.2 10.3a2.9 2.9 0 0 0 3.9 4.1"/>',
  density: '<path d="M8.5 8L12 4.5 15.5 8"/><path d="M8.5 16L12 19.5 15.5 16"/><path d="M12 4.5v15"/>',
  'view-list':
    '<path d="M8.5 6.5h12"/><path d="M8.5 12h12"/><path d="M8.5 17.5h12"/>' +
    '<path d="M4 6.5h.01"/><path d="M4 12h.01"/><path d="M4 17.5h.01"/>',
  'view-grid':
    '<rect x="3.5" y="3.5" width="7" height="7" rx="1.5"/>' +
    '<rect x="13.5" y="3.5" width="7" height="7" rx="1.5"/>' +
    '<rect x="3.5" y="13.5" width="7" height="7" rx="1.5"/>' +
    '<rect x="13.5" y="13.5" width="7" height="7" rx="1.5"/>',
  'view-gallery':
    '<rect x="3" y="4.5" width="18" height="15" rx="2"/>' +
    '<circle cx="8.3" cy="9.6" r="1.5"/>' +
    '<path d="M4.3 17l4.7-4.7 3 3 3-3 5 4.7"/>',
  nav: PANEL + '<path d="M9.5 4.5v15"/>',
  inspector: PANEL + '<path d="M14.5 4.5v15"/>',
  /* ── explorer settings ───────────────────────────────────────────────── */
  'shortcut-settings':
    '<rect x="2.5" y="6" width="19" height="12" rx="2.5"/>' +
    '<path d="M6.5 9.8h.01"/><path d="M10 9.8h.01"/><path d="M13.5 9.8h.01"/><path d="M17 9.8h.01"/>' +
    '<path d="M6.5 12.9h.01"/><path d="M10 12.9h.01"/><path d="M13.5 12.9h.01"/><path d="M17 12.9h.01"/>' +
    '<path d="M8.6 15.6h6.8"/>',
  /* Light/dark as the thing it actually switches: one disc, half of it inked. */
  theme:
    '<circle cx="12" cy="12" r="8.5"/>' +
    '<path d="M12 3.5a8.5 8.5 0 0 1 0 17z" fill="currentColor" stroke="none"/>',
  /* zaman:z3 — a clock face, for the Time zone row. */
  timezone: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.2V12l3.2 2"/>',
  tour:
    '<path d="M2.5 9L12 4.5 21.5 9 12 13.5z"/>' +
    '<path d="M6.6 11.1v4.4c0 1.6 2.4 2.9 5.4 2.9s5.4-1.3 5.4-2.9v-4.4"/>',
  /* "Dosya İste" — the public drop link. A chain, because what this makes is
     a URL to hand somebody. */
  'request-files':
    '<path d="M10.2 13.8a4.2 4.2 0 0 0 6.1 0l2.3-2.3a4.3 4.3 0 0 0-6.1-6.1l-1.35 1.35"/>' +
    '<path d="M13.8 10.2a4.2 4.2 0 0 0-6.1 0l-2.3 2.3a4.3 4.3 0 0 0 6.1 6.1l1.35-1.35"/>',
  /* ── toolbar chrome that is not a ContextAction ──────────────────────── */
  'go-up': '<path d="M12 20V5"/><path d="M5.5 11.5L12 5l6.5 6.5"/>',
  /* === gorunum:v2-topbar — the breadcrumb row's own controls ============
     The crumb trail gained the reference shell's three affordances: a home
     crumb in place of the bare "/" root, a chevron on the last crumb that
     lists what is inside it, and the path editor we already had (its glyph
     moves here from Breadcrumb.vue so every mark in the row comes from one
     file). */
  home:
    '<path d="M3.5 10.6L12 3.5l8.5 7.1"/>' +
    '<path d="M5.6 9.4V19a1.5 1.5 0 0 0 1.5 1.5h9.8a1.5 1.5 0 0 0 1.5-1.5V9.4"/>' +
    '<path d="M9.9 20.5v-5.2h4.2v5.2"/>',
  subfolders: '<path d="M6.8 9.8L12 15l5.2-5.2"/>',
  'edit-path': '<path d="M4 20h4l10-10a2.1 2.1 0 0 0-3-3L5 17z"/><path d="M14.5 6.5l3 3"/>',
  /* === gorunum:v2-topbar — the header's trailing cluster ================
     The page chrome around the explorer is gone, so the account-level doors
     are drawn by the header itself (the host fills them through the
     `header-actions` slot). The glyphs live here so an embedder's cluster
     comes out the same weight and colour as everything beside it. */
  admin:
    '<rect x="3" y="3.5" width="7.4" height="7.4" rx="1.6"/>' +
    '<rect x="13.6" y="3.5" width="7.4" height="4.6" rx="1.6"/>' +
    '<rect x="13.6" y="10.7" width="7.4" height="9.8" rx="1.6"/>' +
    '<rect x="3" y="13.6" width="7.4" height="6.9" rx="1.6"/>',
  /* ⚠ A PERSON, not a cog. The first draft drew the gear as a ringed disc with
     eight spokes and at 16px it came out as a SUN — sitting in the very row
     the light/dark toggle had just been removed from, which is the one thing
     the mark must not be mistaken for (measured at 1440 and 390: it read as a
     theme switch in both). The account bust is what the reference hangs this
     menu on, and the tooltip says which settings. */
  account: '<circle cx="12" cy="8.2" r="3.6"/><path d="M4.9 20.3a7.35 7.35 0 0 1 14.2 0"/>',
  'sign-out':
    '<path d="M9.5 20.5H6a2 2 0 0 1-2-2v-13a2 2 0 0 1 2-2h3.5"/>' +
    '<path d="M15.5 16.5L20 12l-4.5-4.5"/>' +
    '<path d="M20 12H9"/>',
  /* The AI assistant, drawn but not yet wired — a spark, the mark every
     assistant in this class of product wears, so the day it does something
     the icon does not have to change under people's fingers. */
  ai:
    '<path d="M12 3.2l1.75 4.3 4.3 1.75-4.3 1.75L12 15.3l-1.75-4.3L5.95 9.25l4.3-1.75z"/>' +
    '<path d="M18.2 15.1l.8 1.95 1.95.8-1.95.8-.8 1.95-.8-1.95-1.95-.8 1.95-.8z"/>',
  /* The notification bell, for the host's header cluster (web/src
     NotificationBell.vue). ⚠ Drawn HERE, not taken from the app's icon
     library: it sits in the same row as refresh, the spark and the avatar,
     and a glyph at a foreign stroke weight in that row is the exact mismatch
     this set exists to prevent. A dome on a flared rim, the clapper below. */
  bell:
    '<path d="M6.2 16.6V11a5.8 5.8 0 0 1 11.6 0v5.6l1.7 1.9H4.5z"/>' +
    '<path d="M10.1 21a2.1 2.1 0 0 0 3.8 0"/>',
  /* === /gorunum:v2-topbar === */
  search: '<circle cx="10.8" cy="10.8" r="6.3"/><path d="M15.4 15.4l5.1 5.1"/>',
  /* gorunum:v1-advsearch — sliders, not a funnel. A funnel says "throw rows
     away"; this control opens a dialog where you SET things, and two of its
     three rows are exactly a slider's job (a size range, a time window). */
  filter:
    '<path d="M4 7.5h4"/><path d="M13 7.5h7"/><circle cx="10.5" cy="7.5" r="2.4"/>' +
    '<path d="M4 16.5h7"/><path d="M16 16.5h4"/><circle cx="13.5" cy="16.5" r="2.4"/>',
  more:
    '<circle cx="5.5" cy="12" r="1.2" fill="currentColor" stroke="none"/>' +
    '<circle cx="12" cy="12" r="1.2" fill="currentColor" stroke="none"/>' +
    '<circle cx="18.5" cy="12" r="1.2" fill="currentColor" stroke="none"/>',
  close: '<path d="M5.5 5.5l13 13"/><path d="M18.5 5.5l-13 13"/>',
  /* A share link. Two half-chains, which is what a link IS. The details
     panel's "Not shared / Create link" row used to draw a chain EMOJI
     there, i.e. whatever glyph the OS happened to ship, full-colour,
     beside a column of stroked grey ones. */
  link:
    '<path d="M10 13.5a4.5 4.5 0 0 0 6.8.5l2.7-2.7a4.5 4.5 0 0 0-6.36-6.36L11.6 6.5"/>' +
    '<path d="M14 10.5a4.5 4.5 0 0 0-6.8-.5l-2.7 2.7a4.5 4.5 0 0 0 6.36 6.36l1.53-1.53"/>',
  /* baglan:b1 — "How to connect". A PLUG, deliberately not the chain above:
     the empty screen's account menu draws both rows one under the other
     ("Paylaştıklarım", then "Bağlantılar"), and one mark on two adjacent rows
     is the misreading this set exists to stop. It is also the mark the admin
     panel's own Connections entry already wears (Sidebar.vue, lucide `Cable`),
     so the two ways into the same screen agree on what it looks like. */
  connect:
    '<path d="M9 8.5V3"/><path d="M15 8.5V3"/>' +
    '<path d="M6 8.5h12v4.5a4.5 4.5 0 0 1-4.5 4.5h-3A4.5 4.5 0 0 1 6 13z"/>' +
    '<path d="M12 17.5V21"/>',

  /* === ikon:emoji — the command palette's own rows =======================
   * The palette printed `{{ it.icon }}` as TEXT: `➜ 📁 ⬆ ▦ 🗑 ⟳ ↑ 🎨 ⌨ 🎓 ⧉ ◫
   * 💾 🔖`. Thirteen marks from four different type families, in a list whose
   * neighbours — the context menu it duplicates and the toolbar it launches —
   * had already moved here. Most of the keys it needed were drawn already
   * (`tour` is the mortarboard, `shortcut-settings` the keyboard, the three
   * `view-*` the mode chips); these six are what was genuinely missing. */

  /* "Go to path" — the arrow goes INTO the box, the mirror of `sign-out`.
     A bare arrow would have said "next", which this row is not. */
  goto:
    '<path d="M13.5 4.5h4a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2h-4"/>' +
    '<path d="M10 8.2L13.8 12 10 15.8"/>' +
    '<path d="M4.5 12h9.3"/>',
  /* A second pane opened beside the first — `nav`/`inspector`'s panel with the
     divider down the middle, because that is literally what split view is. */
  split: PANEL + '<path d="M12 4.5v15"/>',
  /* A new tab: `open-tab`'s window, with the plus in the body rather than on
     the tab strip (at 16px a plus up there merges into the tab). */
  'tab-new':
    '<rect x="3" y="4.5" width="18" height="15" rx="2"/>' +
    '<path d="M3 9.5h18"/>' +
    '<path d="M9.5 4.5v5"/>' +
    '<path d="M12 12.1v5"/><path d="M9.5 14.6h5"/>',
  bookmark: BOOKMARK,
  'save-search': BOOKMARK + '<path d="M12 7.2v5.2"/><path d="M9.4 9.8h5.2"/>',
  /* The padlock, for the rows and strips that talk ABOUT encryption ("create
     an encrypted folder", "this one is unlocked"). The listing marker is a
     different drawing on purpose — there the lock is cut out of the folder,
     because the row still has to read as a folder. */
  lock:
    '<rect x="4.5" y="10.3" width="15" height="10.2" rx="2.2"/>' +
    '<path d="M7.9 10.3V7.6a4.1 4.1 0 0 1 8.2 0v2.7"/>' +
    '<circle cx="12" cy="15.4" r="1.5"/>',

  /* === ikon:emoji — states the viewers and modals report ================
   * `✓ ✗ ⚠️ ⏳` in the viewer chrome. The first two are typographic and came
   * out at whatever weight the UI font had; the last two are colour emoji, so
   * a fallback screen's 48px mark was the only full-colour thing on a grey
   * page. `progress` is drawn as an open ring because it is SPUN by CSS
   * (`@keyframes fe-spin`) — an hourglass cannot say "still going". */
  /* An app plugin's row when its manifest names no glyph this set draws: a
     puzzle piece, the mark every extension surface already wears. Rows whose
     manifest icon IS a key here (`convert`, `lock`, …) get that glyph instead
     — lib/pluginMenu decides, this only draws. */
  plugin:
    '<path d="M9.5 4.5a2 2 0 1 1 4 0h3a1.5 1.5 0 0 1 1.5 1.5v3a2 2 0 1 1 0 4v3a1.5 1.5 0 0 1-1.5 1.5h-3a2 2 0 1 1-4 0h-3A1.5 1.5 0 0 1 5 16v-3a2 2 0 1 1 0-4V6a1.5 1.5 0 0 1 1.5-1.5z"/>',
  /* A pen signing on a line — the e-signature app's own icon (manifest
     `icon: "sign"`). ⚠ Drawn here because a key the catalogue does not know
     falls back to the generic plugin piece: the sidebar and every menu showed
     the Signatures app as "some app" until it was (2026-09-21, a tester). */
  sign:
    '<path d="M12.5 20.5h8"/>' +
    '<path d="M16.2 3.9a2.05 2.05 0 0 1 2.9 2.9L8 17.9l-3.9 1 1-3.9z"/>' +
    '<path d="M14.6 5.5l2.9 2.9"/>',
  check: '<path d="M4.8 12.6l4.7 4.7L19.2 6.9"/>',
  alert:
    '<path d="M10.7 4.6L2.9 18.2a1.5 1.5 0 0 0 1.3 2.25h15.6a1.5 1.5 0 0 0 1.3-2.25L13.3 4.6a1.5 1.5 0 0 0-2.6 0z"/>' +
    '<path d="M12 9.9v4.3"/>' +
    '<circle cx="12" cy="17.4" r="0.9" fill="currentColor" stroke="none"/>',
  progress: '<path d="M20.5 12a8.5 8.5 0 1 1-8.5-8.5"/>',
};

/**
 * ⚠ RTL — the glyphs that MEAN a direction along the line, and so are drawn
 * mirrored in a right-to-left interface (`fe-aicon--dir`, one rule in
 * base.css; `:dir(rtl)` on the icon itself, so an English explorer inside an
 * Arabic page keeps its arrows):
 *
 *   restore    — the undo arrow ("put it back") points toward the start;
 *   goto       — the arrow goes INTO the box, reading forward;
 *   sign-out   — the arrow leaves through the door on the END side;
 *   copy-path  — its `›` is a path separator, which points the way a path
 *                reads (the breadcrumb's does the same);
 *   nav, inspector — a panel on the start / end side of the window, and in RTL
 *                the navigation panel IS on the right.
 *
 * Deliberately NOT here: `open` (↗ "open in a new tab" points up and out, not
 * along the line), `refresh` (a clock turns the same way in every script),
 * `convert` (two opposed arrows, already symmetric), and everything vertical
 * (upload, download, go-up, subfolders, density).
 */
const DIRECTIONAL = new Set(['restore', 'goto', 'sign-out', 'copy-path', 'nav', 'inspector']);

const SVG_CACHE = new Map<string, string>();

/**
 * Inline SVG markup for an action key, or `''` when nothing is drawn for it.
 *
 * The empty string is a contract, not a failure: the caller renders nothing
 * (or falls back to the `icon` string an embedder supplied) rather than a
 * placeholder box, so an unknown key costs a gap and never a broken glyph.
 */
export function actionIconSvg(key: string): string {
  if (!key) return '';
  const cached = SVG_CACHE.get(key);
  if (cached !== undefined) return cached;
  const glyph = GLYPHS[key];
  const svg = glyph
    ? `<svg class="${DIRECTIONAL.has(key) ? 'fe-aicon fe-aicon--dir' : 'fe-aicon'}" viewBox="0 0 24 24" fill="none" stroke="currentColor" ` +
      'stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" ' +
      `aria-hidden="true" focusable="false">${glyph}</svg>`
    : '';
  SVG_CACHE.set(key, svg);
  return svg;
}

/** Every key this module draws — handy for tests and for a gallery page. */
export function actionIconKeys(): string[] {
  return Object.keys(GLYPHS);
}
