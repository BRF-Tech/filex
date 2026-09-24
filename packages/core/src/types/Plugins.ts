/**
 * App-plugin wire shapes — the TypeScript mirror of
 * `backend/pkg/pluginkit/wire/wire.go` for the parts a browser sees.
 *
 * ⚠ Mirror, not a second design. A field lives here because it lives in
 * wire.go under the same JSON name; when the Go side grows one, add it here
 * with the same spelling. The HTTP surface around these is described in
 * docs/APP-PLUGINS-API.md.
 */

/** A label in one or more languages. `en` is required; others fall back to it. */
export type PluginText = Record<string, string>;

/** Which selections an action or view is offered for (wire.Applies). */
export interface PluginApplies {
  /** file | dir | any — the server treats an absent kind as `file`. */
  kind?: 'file' | 'dir' | 'any' | string;
  /** Lower-case, no dot. */
  ext?: string[];
  /** Exact, or a prefix glob such as `image/*`. */
  mime?: string[];
  multi?: boolean;
  min?: number;
  max?: number;
  /**
   * v2 — state-aware rows. `state` narrows the offer to files on which THIS
   * action's plugin keeps at least one of the named keys, `no_state` to
   * files that carry none of them. Listings expose the keys as
   * `app_state: ["<plugin>:<key>"]`, so a client matching this rule strips
   * the ROW'S OWN plugin prefix first (lib/pluginApplies). A signing app
   * offers "Sign" only where `pending` is set and "Request signatures" only
   * where it is not — both in the same menu.
   */
  state?: string[];
  no_state?: string[];
  /**
   * The flow the action starts ends in writing the file, although its own
   * output is `none` ("Request signatures"): filex does not offer it where
   * the file cannot be written (a read-only storage), exactly as it does not
   * offer an action whose output writes.
   */
  writable?: boolean;
}

/**
 * v2 — an app plugin holds a file read-only (`files:lock`,
 * docs/APP-PLUGINS-API.md → "File locks"). The row's `perm` arrives capped
 * at `viewer` for EVERYONE, administrators included, so nothing else has to
 * know about locks to behave; this is what the badge says out loud.
 */
export interface AppLock {
  /** The app holding it — its manifest `name`, which is what ADDRESSES it. */
  plugin: string;
  /**
   * The same app as a person should read it ({"en": "e-Signature"}), in every
   * language its manifest wrote it in.
   *
   * ⚠ Absent where the server cannot resolve the name (the app was removed,
   * or there is no app runtime), and then `plugin` is what is shown — which
   * is what every surface did before this field existed, and read as "sign
   * locked this file" where the rest of the product says "e-Signature"
   * (v0.43.0).
   */
  plugin_label?: PluginText;
  reason?: string;
  /** The reason in every language the app wrote it in (a manifest message);
   *  the reader's own is shown, `reason` is the plain fallback. */
  reason_text?: PluginText;
  /** RFC 3339; absent means "until the app lifts it". */
  until?: string | null;
}

/** One entry of `GET /api/files/plugins/actions` → `actions[]`. */
export interface PluginActionRow {
  plugin: string;
  id: string;
  /** `plugin:<plugin>/<action>` — the menu row's key. */
  key?: string;
  label: PluginText;
  icon?: string;
  applies: PluginApplies;
  /** A view id: running the action opens this surface first. */
  view?: string;
  /**
   * v2 — how `view` opens: `modal` (a dialog over the explorer) or `page`
   * (a full page in a NEW TAB, `{base}apps/{plugin}/{view}?path=…`). A
   * `page` action is not run through `…/run` at all: the tab opens on the
   * view and the job is queued from the surface's own submit.
   */
  view_placement?: 'modal' | 'page' | string;
  confirm?: PluginText | null;
  min_role?: 'viewer' | 'editor' | 'owner' | string;
  danger?: boolean;
  output_mode?: 'sibling' | 'version' | 'none' | string;
  /** The result may go into a folder the person chooses (manifest
   *  `output.elsewhere`): the action is offered on a read-only storage too,
   *  and its screen asks where the result should go. */
  output_elsewhere?: boolean;
  /**
   * What the action would ALSO be offered on once a requirement the server
   * lacks is met — sent to administrators only. The menu draws each as a
   * greyed row that says what is missing (lib/pluginMenu).
   */
  gated?: PluginGatedRule[];
}

/** One "offered once X is there" part of an action's rule. */
export interface PluginGatedRule {
  ext?: string[];
  needs: { kind: 'engine' | string; id: string; name: string };
}

/** One entry of `GET /api/files/plugins/actions` → `views[]`. */
export interface PluginViewRow {
  plugin: string;
  id: string;
  placement: 'modal' | 'inspector' | 'home' | string;
  label: PluginText;
  applies?: PluginApplies;
  icon?: string;
  size?: string;
}

export interface PluginActionsResponse {
  actions: PluginActionRow[];
  views: PluginViewRow[];
}

/** One component of a surface (wire.Node). */
export interface SurfaceNode {
  id?: string;
  type: string;
  props?: Record<string, unknown>;
  children?: SurfaceNode[];
}

/* ── Node props (M2) — docs/APP-PLUGINS-API.md → "Node props (M2)" ──────
 * `SurfaceNode.props` stays `Record<string, unknown>` on the wire (the host
 * validates against the catalogue, the browser never trusts it); each
 * component narrows its own node's props through these. */

export type SurfaceTone = 'muted' | 'danger' | 'info';

export interface TextNodeProps {
  text?: PluginText | string;
  /** Accepted alias of `text` (M1). */
  value?: PluginText | string;
  tone?: SurfaceTone | string;
  heading?: boolean;
}

/**
 * v3 — "that other field holds one of these values" (wire.Condition).
 *
 * Carried by `show_when` (the field is not drawn until the condition holds)
 * and `required_when` (the field is required only then). An empty `equals`
 * means "holds anything at all", which is how a field is made to depend on
 * another one merely having been answered.
 */
export interface PluginCondition {
  key: string;
  equals?: string[];
}

/**
 * A form field — the storage descriptor field (`types/Connections.StorageField`)
 * as a plugin declares it: `label` / `help` may be Text, `i18n_key` is absent.
 *
 * ⚠ v3 removed `advanced`. A surface has no collapsed section any more: a
 * field that matters is on the step, and one that does not is not in the
 * manifest. An older plugin's `advanced` is ignored, not obeyed.
 */
export interface PluginField {
  key: string;
  type?: 'string' | 'int' | 'bool' | 'password' | 'select' | string;
  label?: PluginText | string;
  help?: PluginText | string;
  required?: boolean;
  secret?: boolean;
  default?: unknown;
  placeholder?: PluginText | string;
  options?: Array<{ value: string; label?: PluginText | string }>;
  min?: number;
  max?: number;
  multiline?: boolean;
  monospace?: boolean;
  /** v3 — a `select` that takes several of its options; the value is then a list. */
  multi?: boolean;
  /**
   * v3 — how a `bool` is drawn (wire.Field.Style): `choice` for a decision
   * the person must READ before answering (two buttons, Yes / No), `switch`
   * for an on/off setting they flip in passing (a checkbox).
   *
   * ⚠ Empty is not `switch`: it leaves the answer to the guess in
   * `lib/surfaceValues.boolIsDecision`, which is exactly why the contract
   * says to state it when the answer matters. A `select` ignores `style`
   * entirely — a surface has no dropdown for it to fall back to.
   */
  style?: 'switch' | 'choice' | string;
  /** v3 — hide the field until the condition holds; a hidden value is never sent. */
  show_when?: PluginCondition | null;
  /** v3 — require the field only while the condition holds. */
  required_when?: PluginCondition | null;
}

export interface FormNodeProps {
  fields: PluginField[];
  /** Initial values, keyed by field key. Field values are FLAT in `data.values`. */
  values?: Record<string, unknown>;
}

export type StepState = 'done' | 'active' | 'todo';

export interface StepsNodeProps {
  items: Array<{ id: string; label: PluginText | string; state?: StepState | string }>;
}

export interface ListRowAction {
  id: string;
  label: PluginText | string;
  danger?: boolean;
}

/**
 * A `list` node — drawn by the product's one table (DataTable).
 *
 * ⚠ Everything past `key`/`label`/`id`/`cells` is OPTIONAL and additive
 * (docs/APP-PLUGINS-API.md → `list`): a plugin written against the first
 * contract draws the same rows, now resizable, sortable and with columns a
 * person can hide and move.
 */
export interface ListNodeProps {
  columns: Array<{
    key: string;
    label: PluginText | string;
    /** Opening width in px (60–900). */
    width?: number;
    /** Default true — the node carries every row, so sorting is honest. */
    sortable?: boolean;
    align?: 'left' | 'right' | 'center';
    /** What the cells are: `date` (`YYYY-MM-DD`) or `datetime` (RFC 3339). The
     *  host prints them in the reader's language and clock and sorts by the
     *  value as sent (lib/surfaceCell). */
    format?: 'date' | 'datetime';
  }>;
  rows: Array<{
    id: string;
    cells: Record<string, PluginText | string>;
    actions?: ListRowAction[];
    /** Raw values to sort by, keyed like `cells`, where the cell is formatted
     *  for people (a localised date, "1.2 MB"). */
    sort?: Record<string, string | number>;
  }>;
  empty?: PluginText | string;
}

export interface ProgressNodeProps {
  /** 0..100, or null for indeterminate. */
  value: number | null;
  label?: PluginText | string;
}

/** One person in a `people-picker` value — and one row of the users lookup. */
export interface PluginPerson {
  email: string;
  user_id?: number;
  name?: string;
}

export interface PeoplePickerNodeProps {
  value?: PluginPerson[];
  multi?: boolean;
  allow_external?: boolean;
}

export interface PinInputNodeProps {
  /** 4..8, default 6. */
  length?: number;
}

export interface FileChooserNodeProps {
  kind?: 'file' | 'dir' | string;
  /** Adapter-qualified path. */
  value?: string;
}

export interface PreviewNodeProps {
  /** Adapter-qualified path of a storage file. */
  path?: string;
  /** An exposed copy on a public page (`pub:N`), resolved through the host's `fileUrl`. */
  ref?: string;
}

/* ── Node props (M3, sign track) — docs "Frontend needs (M3)" ─────────── */

/**
 * How a surface node reaches a file the page exposes (`files[].ref`):
 * the host resolves the ref to a URL the browser may load directly (the
 * PIN cookie rides along on the same origin). Absent outside public pages.
 */
export type FileRefResolver = (ref: string) => { url: string; name?: string; mime?: string } | null;

export interface SignaturePadNodeProps {
  /** Subset of `draw | type | upload`; all three when absent. */
  modes?: string[];
  width?: number;
  height?: number;
  /** The face a typed signature starts in (a `lib/signFonts` key). */
  font?: string;
  /** The faces a typed signature may use; one means no picker. All five when absent. */
  fonts?: string[];
  /**
   * The pad's label — the box's name — drawn like a form field's, so a
   * required signature wears the same `*` a required field does.
   */
  label?: PluginText | string;
  required?: boolean;
}

/** Where a `pdf-fields` node's document comes from — exactly one of the three. */
export interface PdfFieldsSource {
  /** An exposed copy on a public page. */
  ref?: string;
  /** Adapter-qualified path of a storage file (the explorer's authenticated preview fetch). */
  path?: string;
  /** Any URL the browser may fetch. */
  url?: string;
}

export interface PdfFieldsNodeProps {
  src: PdfFieldsSource;
  /**
   * `define` names the boxes (cards, no document), `place` puts them on the
   * page (document, and the boxes that still need a place), `edit` is the
   * one-screen form of both, `fill` is the signer's.
   */
  mode?: 'define' | 'place' | 'edit' | 'fill' | string;
  /** `lib/pdfFields.PdfField[]` on the wire; `placed: false` = defined, not yet on a page. */
  fields?: unknown[];
  signers?: unknown[];
  /** Fill mode: the signer this visitor is. */
  signer?: string;
  /** `define` / `edit`: the palette, a subset of the five types. */
  types?: string[];
  /**
   * `define` / `edit`: the date layouts a `date` box may be written in
   * (`[{id, label, example}]`). The chosen id travels back as the field's
   * `format`; with no catalogue there is no control and the plugin's own
   * default stands.
   */
  formats?: unknown[];
  /**
   * `define` / `edit`: the three captions the date control needs — the two
   * questions (`order`, `separator`) and the word over the live example
   * (`example`), each a `Text`. They ride with the node so they are the
   * PLUGIN's words in the plugin's own languages. Absent: this package's
   * own catalogue says them.
   */
  date_labels?: unknown;
  /**
   * `define` / `edit`: the lines this plugin can print under a signature
   * (`[{id, label, examples?: {<signer id>|'*': Text}, default?}]`,
   * `lib/pdfFields.PdfStampLine`). Each signature/initials box then chooses
   * its own `lines`. Absent: no choice is offered.
   */
  stamp_lines?: unknown[];
}

/** What `GET /api/p/{token}` answered before v3. `/api/p/*` is retired (it
 *  301s to `/api/public/s/{token}`); kept for the deprecated `usePublicPage`
 *  exports — new code reads `PublicShareInfo` through `usePublicShare`. */
export interface PublicPageInfo {
  plugin: string;
  page: string;
  title: PluginText;
  subject?: string;
  requires_pin: boolean;
  unlocked: boolean;
  expires_at?: string | null;
  files: PublicPageFile[];
}

export interface PublicPageFile {
  ref: string;
  name: string;
  size?: number;
  mime?: string;
}

/** `GET /api/files/plugins/users?plugin=<name>&q=` — docs "Frontend needs (M2)". */
export interface PluginUsersResponse {
  users: PluginPerson[];
}

/**
 * v3 §3.0 — `wire.OpenRequest`: "go to this file, and start this there".
 * `action` or `view`, never both; neither just opens the file.
 */
export interface SurfaceOpenRequest {
  /** Adapter-qualified (`docs://reports/nda.pdf`). */
  path: string;
  action?: string;
  view?: string;
}

/** A footer button (wire.SurfaceAction). */
export interface SurfaceAction {
  id: string;
  label: PluginText;
  primary?: boolean;
  danger?: boolean;
  disabled?: boolean;
}

/** wire.JobRequest — the surface asks the host to enqueue an action. */
export interface SurfaceJobRequest {
  action_id: string;
  params?: Record<string, unknown>;
  /**
   * v2 — this one job's output, replacing the action's manifest output
   * ("same file as a new version" / "a new file beside it" / a name). The
   * server re-checks the ACL against the EFFECTIVE mode, so a surface
   * cannot turn a read-only action into a write.
   */
  output?: { mode: 'sibling' | 'version' | 'none' | string; name?: string };
}

/** A declarative screen (wire.Surface). Drawn only by filex's own components. */
export interface PluginSurface {
  title?: PluginText;
  size?: string;
  state?: Record<string, unknown>;
  nodes: SurfaceNode[];
  actions?: SurfaceAction[];
  toast?: PluginText;
  done?: boolean;
  job?: SurfaceJobRequest | null;
  errors?: Record<string, PluginText>;
  /**
   * v3 §3.0 — send the person to a FILE, and start a screen on it.
   *
   * A surface runs on the file it was opened on, so an app's home screen
   * could list documents and then do nothing when a row was clicked. The
   * client navigates (`lib/surfaceOpen`); with `done: true` the screen is
   * closed first and then the navigation happens.
   */
  open?: SurfaceOpenRequest | null;
  /**
   * A home page's MENU: one entry per section, drawn by the frame
   * (`SurfaceSections`), and `section` — which one this surface is.
   *
   * ⚠ The frame and not a node, because only the frame owns the address
   * bar: it keeps the open section in its URL (`?section=`), so the
   * browser's Back walks the sections and a link — a notification's —
   * lands on the right one (the owner, 2026-09-21).
   */
  sections?: SurfaceSection[];
  section?: string;
}

/** One entry of a home page's menu. */
export interface SurfaceSection {
  id: string;
  label: PluginText;
  /** Drawn beside the label (rows waiting there); absent draws none. */
  count?: number;
}

/**
 * What `POST …/run` (and a view event that enqueued a job) answers with. The
 * ops row is the raw server shape; `usePendingOps.register` normalises it.
 */
export type PluginRunResult =
  | { op: Record<string, unknown>; job_id?: string; surface?: undefined }
  | { surface: PluginSurface; op?: undefined };

/** Body of `POST …/run` and `POST …/views/{plugin}/{view}/event`. */
export interface PluginViewEventBody {
  /** Adapter-qualified wire path of the row the view was opened on. */
  path?: string;
  storage_id?: number;
  state?: Record<string, unknown>;
  event: 'open' | 'change' | 'submit' | 'action';
  action_id?: string;
  data?: PluginViewEventData;
}

/** `data` of a view event: every node with an id contributes `values[id]`. */
export interface PluginViewEventData {
  values?: Record<string, unknown>;
  /** `list` row actions only. */
  row_id?: string;
  [k: string]: unknown;
}
