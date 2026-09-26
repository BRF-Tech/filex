<script setup lang="ts">
/**
 * InspectorPanel — koru:k1 details ("Ayrıntılar") side panel.
 *
 * Rendered as a flex sibling of `.fe__body` (right side, ~300px). In
 * `fe--narrow` embeds it becomes a full-size overlay with its own close
 * button (Esc handled by FileExplorer's shortcut chain).
 *
 * Sections:
 *   - Genel     : icon/thumb, name, path (copy), size, modified, mime, etag.
 *                 Multi-select → "N items, total X" summary; no selection →
 *                 current-folder summary.
 *   - Sürümler  : version history via GET /api/files/versions?node_id=…
 *                 + restore (with optional snapshot-current) + snapshot-now.
 *                 The section hides silently when the backend gates the
 *                 endpoint (401/403) or doesn't ship it (404).
 *   - İzinler   : effective RBAC level badge + "manage" (opens the existing
 *                 PermissionsModal through the host). Only when ACL is
 *                 enforced on the storage (perm is a non-empty string).
 *   - Paylaşımlar: existing share links (GET /api/files/share?path=…) with
 *                 copy buttons. Hidden silently when the list call fails
 *                 (viewer-level users get 403 by design).
 *
 * The panel is mounted with v-if by the host — closed state leaves zero DOM.
 */
import { computed, ref, watch } from 'vue';
import type { FileApi, Grant, NodeVersion } from '../composables/useFileApi';
import type { FileNode, ShareInfo } from '../types/FileNode';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import type { PluginViewRow } from '../types/Plugins';
import { localeTag, useLocale } from '../composables/useLocale';
import { fileIconTile, isStorageRow, typeLabelFor } from '../lib/fileIcons';
import { actionIconSvg } from '../lib/actionIcons';
import { appliesToNodes } from '../lib/pluginApplies';
import { lockedRefusal, lockOf, lockWords } from '../lib/appLock';
import { linkWordsFor } from '../lib/symlink'; /* issue #34 */
import { personInitial as initialOfPerson, personName as nameOfPerson } from '../lib/personName';
import TagPicker from './TagPicker.vue';
import type { TagKind } from '../lib/tags';
import PluginInspectorSection from './plugin/PluginInspectorSection.vue';

const props = withDefaults(
  defineProps<{
  api: FileApi;
  /** Current selection (empty array → current-folder summary). */
  nodes: FileNode[];
  /** Display label of the folder being viewed (for the no-selection state). */
  dirLabel: string;
  /** Number of entries in the folder being viewed. */
  dirCount: number;
  /** RBAC effective level of the current dir ('' = ACL not enforced). */
  dirPerm?: string;
  /**
   * pane:p1 — non-empty ⇒ `nodes` is the LAST selected thing, not what is
   * ticked in the pane the person is looking at; the value NAMES where it
   * lives.
   *
   * ⚠ The panel holds its subject on purpose (owner, 2026-09-13: "son seçilen
   * şeyi tutsun"), which means it can be describing a file in the other half of
   * a split window — so it has to SAY so. Without this line the panel would be
   * a fact with no address: three sections about `report.pdf` over a listing
   * with nothing ticked in it, and no way to tell whether that is the file you
   * just clicked or the one you clicked five minutes ago.
   */
  heldIn?: string;
  locale: LocaleCode;
  /** Narrow/embed mode → full-size overlay presentation. */
  narrow?: boolean;
  /**
   * Is the reader an administrator (`capabilities.caller_admin`)?
   *
   * ⚠ Only the NODE ID row depends on it (Burak, 2026-09-23). The id is a
   * support handle — what an administrator quotes in Admin → File history or
   * an audit row — and it means nothing to anybody else; on a shared surface
   * it was a number in front of every reader. Absent = not an administrator,
   * which is the safe answer for an embed that never asks the server.
   */
  callerAdmin?: boolean;
  /** Authenticated thumbnail resolver (useThumbs.src). Optional. */
  thumbSrc?: (n: FileNode) => string | null;
  /* === surucu:d1 — the Drive shell's details panel ===================== */
  /**
   * Split the panel into **Details** and **Activity** tabs, and draw the two
   * sections the mockups add: who has access, and the share link with a
   * "Create link" button. `uiProfile: 'drive'` turns it on; absent/false is
   * the flat scroll of sections this panel has always been, with no extra
   * request made.
   *
   * ⚠ "Activity" is version history + comments, and nothing else, because
   * nothing else exists: there is no per-file activity endpoint. The audit log
   * is admin-only, has no path filter, and stores an EMPTY `target_id` for
   * every file action (auth/audit_middleware.go) — so "Ayşe renamed this on
   * Tuesday" cannot be answered by this server, and a timeline that made it up
   * would be worse than the two real feeds.
   */
  tabs?: boolean;
  /* === etiket:t1 — the Tags section ==================================
   *
   * The three props a self-fetching child in this package takes, passed
   * straight through to `TagPicker`: it talks to
   * `/api/files/manager/tags` itself and needs all three to do it.
   *
   * ⚠ `authCredentials` is not decoration. A credentialed cross-origin
   * request cannot be answered with `Access-Control-Allow-Origin: *`, so
   * an embed served from a different origin to the API loses its tags
   * without it — the same reason TagPicker's own prop documents.
   *
   * All three optional: a host that passes none gets no Tags section
   * rather than a control that reads nothing. That is the honest failure
   * for this panel, which hides every section it cannot fill.
   */
  /** API origin for the tag routes. `''` is valid — it means "same origin". */
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
  /* === App plugins (docs/APP-PLUGINS-API.md → Placements, `inspector`) ===
   * The `views[]` rows with placement `inspector`, as the host fetched them
   * (`usePluginActions.views`); `[]` while the feature is off. One collapsible
   * section per row whose `applies` rule accepts the selected SINGLE item —
   * the client-side mirror the menu uses (lib/pluginApplies), re-checked by
   * the server when the view loads. */
  pluginViews?: PluginViewRow[];
  /** Resolved theme, for the dialogs a plugin surface can open. */
  theme?: ThemeMode;
  /** Storage names a surface's file-chooser may span. */
  storages?: string[];
  }>(),
  {
    /* ⚠ ON by default, and that is the owner's call of 2026-09-12, not a
     * taste: the Details/Activity split, "People with access" and the share
     * link row used to arrive only with `uiProfile: 'drive'`, which meant the
     * screen the owner actually uses — the admin one — was the only screen
     * that never got them ("their app and our app will be one to one";
     * web/src/views/Explore.vue carries the full quote). The profile string
     * is gone; this is the last gate that answered to it, so it answers yes.
     * An embedder that wants the old flat scroll can still pass `:tabs="false"`.
     */
    tabs: true,
    narrow: false,
    dirPerm: '',
    thumbSrc: undefined,
    pluginViews: () => [],
  },
);

const emit = defineEmits<{
  (e: 'close'): void;
  /** surucu:d1 — a share link was minted from the panel. */
  (e: 'share-created', payload: { path: string; url: string }): void;
  (e: 'manage-permissions', node: FileNode): void;
  (e: 'toast', message: string): void;
  /** Fired after a successful restore/snapshot so the host can reload. */
  (e: 'changed'): void;
  /**
   * etiket:t1 — tags on this node were edited here.
   *
   * ⚠ The host drops its cached tag list on this, refreshes the panel's
   * Tags section and reloads an open tag view — exactly what the
   * context-menu tag modal already causes. Without the emit the edit
   * lands on the server and the sidebar keeps showing the list from
   * before it, which is the kind of staleness nobody notices until they
   * go looking for the tag they just made.
   */
  (e: 'tags-changed', tags: string[]): void;
  /**
   * etiket:t1 — a tag chip was clicked: open that tag's view.
   *
   * ⚠ Re-emitted rather than handled, like every other navigation this panel
   * offers. The panel knows a tag's NAME; only the host knows that the tag view
   * is a `.tag~<name>` sentinel, how to leave whatever view is on screen to get
   * there, and what that does to the tab strip.
   */
  (e: 'open-tag', tag: string, kind: TagKind): void;
  /** App plugins — an inspector view's event enqueued a job: the raw ops row. */
  (e: 'plugin-op', op: Record<string, unknown>): void;
  /**
   * v3 §3.0 — an app's inspector screen answered "go to this file".
   *
   * ⚠ Carried UP with the plugin's name rather than acted on: the panel
   * cannot navigate, and the explorer that can is the one holding it.
   */
  (e: 'plugin-open', payload: { plugin: string; req: { path: string; action?: string; view?: string } }): void;
}>();

const { t, formatSize, formatNodeSize, nodeSizeHint, formatDate: formatDateOf, nodeDisplayName } = useLocale(
  () => props.locale,
);

// ── selection shape ──────────────────────────────────────────────────
const single = computed<FileNode | null>(() =>
  props.nodes.length === 1 ? props.nodes[0] : null,
);
const isMulti = computed(() => props.nodes.length > 1);

/* App plugins — the app holding this file, in the same words the row's badge
 * uses (lib/appLock). ⚠ It goes ABOVE the facts, not among them: the panel's
 * "Permission: viewer" line is TRUE and misleading on its own, because the
 * reason is not a permission the owner can change. */
const lockLine = computed(() => lockWords(lockOf(single.value), { t, formatDate: formatDateOf, locale: props.locale }));
/* issue #34 — a symlink the server will not follow. It goes in the SAME place
 * and for the same reason as the lock above: this panel's "Size: 0 bytes" and
 * "Type: file" are both true and both misleading on their own, and the reason
 * is not something the person can read off any of the facts below. */
const linkNote = computed(() => linkWordsFor(single.value, { t }));
const isFile = computed(() => single.value?.type === 'file');
const nodeId = computed<number | null>(() =>
  typeof single.value?.id === 'number' ? (single.value.id as number) : null,
);
const multiTotal = computed(() =>
  props.nodes.reduce((acc, n) => acc + (typeof n.size === 'number' ? n.size : 0), 0),
);
/** The selection's total, as a size: a lower bound when any selected folder's
 *  own size is one (`size_partial`, drawn by useLocale formatNodeSize). */
const multiSize = computed(() => ({
  size: multiTotal.value,
  size_partial: props.nodes.some((n) => n.size_partial === true),
}));
const etag = computed<string | null>(() => {
  const v = single.value?.etag;
  return typeof v === 'string' && v !== '' ? v : null;
});
const thumb = computed<string | null>(() =>
  single.value && isFile.value && props.thumbSrc ? props.thumbSrc(single.value) : null,
);

/** App plugins — the inspector views whose rule accepts the selected item. */
const pluginSections = computed<PluginViewRow[]>(() => {
  const n = single.value;
  if (!n || isStorageRow(n) || n.trashed === true) return [];
  return (props.pluginViews ?? []).filter(
    (v) => v.placement === 'inspector' && appliesToNodes(v.applies, [n], v.plugin),
  );
});

/* === gorunum:v4-dialogs — what the head says =============================
 *
 * The panel used to open with the word "Details" and put the item's own name
 * a section lower, under a second heading. The reference shell puts the item
 * in the head — its glyph, its name, and one caption naming what it is — and
 * that is the right way round: the panel is ABOUT the item, and a header that
 * names the panel instead of its subject is a label on the frame.
 *
 * All three fall back cleanly: a multi-selection is counted, no selection at
 * all describes the folder being viewed. Nothing here invents a fact — the
 * caption is the same kind name the listing's Type column prints
 * (`typeLabelFor`) and the same size formatter the rows use.
 * ---------------------------------------------------------------------- */

/** The name in the head: the item, the count, or the folder you are in. */
const headName = computed<string>(() => {
  if (isMulti.value) return t('inspector.items', { n: props.nodes.length });
  if (single.value) return nodeDisplayName(single.value);
  return props.dirLabel;
});

/** The line under it — "Folder", "TypeScript · 4.8 KB", "12.4 MB in total". */
const headCaption = computed<string>(() => {
  if (isMulti.value) return formatNodeSize(multiSize.value);
  const n = single.value;
  if (n) {
    const kind = typeLabelFor(n, t);
    if (n.type === 'dir') return kind;
    const size = typeof n.size === 'number' ? formatSize(n.size) : '';
    return size ? `${kind} · ${size}` : kind;
  }
  return t('inspector.folder_items', { n: props.dirCount });
});

/**
 * Whether the thumbnail is worth the space it takes.
 *
 * ⚠ `thumbSrc` answers for text files too — the host renders the source and
 * hands back a picture of it — and at panel width that came out as a blank
 * olive rectangle with "TS" in the middle of it: a large, prominent block
 * that tells the reader less than the one-word caption already did. A
 * thumbnail earns its place when the file IS a picture; for everything else
 * the type tile and the kind name are the whole of what an image could say.
 */
const previewSrc = computed<string | null>(() =>
  thumb.value && /^(image|video)[/]/.test(String(single.value?.mime_type ?? ''))
    ? thumb.value
    : null,
);

/**
 * The glyph — the LISTING'S tile, not a second drawing of the same idea and
 * not the thumbnail.
 *
 * ⚠ It was the thumbnail first, and that was wrong twice over: the host
 * resolves a thumbnail for text files too, so `app.ts` came out as a 30px
 * smudge of rendered source with no glyph in it at all, and the panel's mark
 * then disagreed with the mark on the row it describes. `fileIconTile` is the
 * same object the row draws, so the head and the row match by construction.
 * The thumbnail is still shown — below, at a size where a picture is a
 * picture.
 */
const headIcon = computed<string>(() =>
  fileIconTile(single.value && !isMulti.value ? single.value : { type: 'dir' }),
);

/**
 * Whether the Tags section has everything it needs.
 *
 * A node id, because the tag routes are keyed by it (a client-synthesized row
 * — a multi-storage virtual folder — has none), and a host that wired the
 * three props. Missing either, the section is not drawn at all rather than
 * drawn empty: an "Add tag" button that cannot save is worse than no button.
 */
const canTag = computed(
  () => nodeId.value != null && props.apiBase !== undefined && !isMulti.value,
);

function shortHash(h: string): string {
  return h.length > 12 ? `${h.slice(0, 12)}…` : h;
}

/**
 * zaman:z1 — the panel prints the SAME string the row behind it prints.
 *
 * This used to be a private `formatDate()` that called `toLocaleString` with
 * no `timeZone` at all, so the details panel answered on the BROWSER's clock
 * while the listing cell four pixels away answered on the zone the viewer
 * chose. It also mapped the locale by hand ('en-GB'), so even in one zone the
 * two disagreed about the shape of a date. Both halves live in
 * `useLocale.formatDate` now; `{ time: true }` is the listing's own variant.
 */
function formatDate(ms: number | undefined): string {
  return formatDateOf(ms, { time: true }) || '—';
}

function formatDateStr(s: string | undefined | null): string {
  if (!s) return '—';
  const ms = Date.parse(s);
  return Number.isNaN(ms) ? s : formatDate(ms);
}

async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
    emit('toast', t('inspector.copied'));
  } catch {
    emit('toast', text);
  }
}

// ── RBAC (İzinler) ───────────────────────────────────────────────────
// Effective level for the selected item: its own perm, else the dir's.
// A non-empty string means ACL is enforced → section shows.
const effectivePerm = computed<string>(() => {
  if (!single.value) return '';
  // Backends may send '' when ACL is off — widen past the declared union.
  const own = single.value.perm as string | undefined;
  if (typeof own === 'string' && own !== '') return own;
  return props.dirPerm || '';
});
/**
 * "Manage permissions" — for an OWNER only.
 *
 * ⚠ It used to be offered to editors too, and an editor cannot read the grant
 * list at all (`GET /api/files/permissions` answers 403): the button opened a
 * dialog with no permissions in it, and the refusal was swallowed (QA,
 * 2026-09-21). An editor shares from the row's "Share" like everybody else.
 */
const canManagePerms = computed(() => effectivePerm.value === 'owner');
function permLabel(level: string): string {
  return t(`inspector.perm.${level}`) === `inspector.perm.${level}`
    ? level
    : t(`inspector.perm.${level}`);
}

// ── versions state ───────────────────────────────────────────────────
type SectionState = 'idle' | 'loading' | 'ok' | 'hidden' | 'error';
const versionsState = ref<SectionState>('idle');
const versions = ref<NodeVersion[]>([]);
const confirmVersionId = ref<number | null>(null);
const snapshotFirst = ref(true);
/* ⚠ Which version work is under way, not just THAT one is: both copy the
 * whole file on the storage, and the buttons only went grey, so a long
 * restore read as a stuck pane. The pressed button names its work. */
const versionWork = ref<'restore' | 'snapshot' | null>(null);
const versionBusy = computed(() => versionWork.value !== null);

// ── shares state ─────────────────────────────────────────────────────
const sharesState = ref<SectionState>('idle');
const shares = ref<ShareInfo[]>([]);

// Race guard: only the latest refresh may write state.
let refreshSeq = 0;

async function refresh(): Promise<void> {
  const seq = ++refreshSeq;
  confirmVersionId.value = null;
  versions.value = [];
  shares.value = [];

  // Versions — single file with a backend node id only.
  if (single.value && isFile.value && nodeId.value != null) {
    versionsState.value = 'loading';
    void loadVersions(seq);
  } else {
    versionsState.value = 'hidden';
  }

  // Shares — any single selection (files and folders both shareable).
  if (single.value) {
    sharesState.value = 'loading';
    const path = single.value.path;
    try {
      const { shares: list } = await props.api.listShares(path);
      if (seq !== refreshSeq) return;
      shares.value = Array.isArray(list) ? list : [];
      sharesState.value = 'ok';
    } catch {
      if (seq !== refreshSeq) return;
      // 403 = viewer-level user (by design), anything else — auxiliary
      // info, hide silently rather than alarm.
      sharesState.value = 'hidden';
    }
  } else {
    sharesState.value = 'hidden';
  }
}

async function loadVersions(seq: number): Promise<void> {
  const id = nodeId.value;
  if (id == null) return;
  try {
    const list = await props.api.listVersions(id);
    if (seq !== refreshSeq) return;
    versions.value = list;
    versionsState.value = 'ok';
  } catch (err) {
    if (seq !== refreshSeq) return;
    const status = (err as { status?: number }).status;
    // Gated (401/403) or absent (404) endpoint → section silently hidden.
    if (status === 401 || status === 403 || status === 404) {
      versionsState.value = 'hidden';
    } else {
      versionsState.value = 'error';
    }
  }
}

function askRestore(v: NodeVersion): void {
  confirmVersionId.value = v.id;
  snapshotFirst.value = true;
}

async function confirmRestore(v: NodeVersion): Promise<void> {
  const id = nodeId.value;
  if (id == null || versionBusy.value) return;
  versionWork.value = 'restore';
  try {
    await props.api.restoreVersion(id, v.id, snapshotFirst.value);
    emit('toast', t('inspector.versions.restored'));
    confirmVersionId.value = null;
    emit('changed');
    await loadVersions(++refreshSeq);
  } catch (err) {
    // ⚠ A 423 is an app's freeze (the signing app, while signatures are
    // collected): say which app and why, in the reader's language — the
    // server's own text is English and names a path.
    const held = lockedRefusal(err);
    emit('toast', held ? lockWords(held, { t, formatDate: formatDateOf, locale: props.locale }) : (err as Error).message);
  } finally {
    versionWork.value = null;
  }
}

async function takeSnapshot(): Promise<void> {
  const id = nodeId.value;
  if (id == null || versionBusy.value) return;
  versionWork.value = 'snapshot';
  try {
    await props.api.snapshotVersion(id);
    emit('toast', t('inspector.versions.snapshotted'));
    await loadVersions(++refreshSeq);
  } catch (err) {
    const status = (err as { status?: number }).status;
    // Older backends don't ship POST /versions/snapshot yet.
    if (status === 404 || status === 405 || status === 501) {
      emit('toast', t('inspector.versions.unsupported'));
    } else {
      emit('toast', (err as Error).message);
    }
  } finally {
    versionWork.value = null;
  }
}

/* === surucu:d1 — tabs, people with access, the share-link row ==========
 *
 * The tab strip is presentation only: every section below is the same section,
 * rendered under Details or under Activity. Nothing is fetched twice, and
 * nothing new is fetched at all unless `tabs` is on.
 */
type InspectorTab = 'details' | 'activity';
const tab = ref<InspectorTab>('details');

/** People with access — `GET /api/files/permissions?path=…`.
 *
 * ⚠ Owner-gated on the server (handlers/grants.go `requireOwner`), so an
 * editor or a viewer gets a 403 for a file they can perfectly well see. That
 * is the server's call, not something to route around: the section hides,
 * exactly the way the shares list already hides for the same class of caller.
 * What it must NOT do is render an empty "People with access" and let the
 * reader conclude that nobody has any.
 */
const peopleState = ref<SectionState>('hidden');
const people = ref<Grant[]>([]);
let peopleSeq = 0;

async function loadPeople(): Promise<void> {
  const seq = ++peopleSeq;
  const node = single.value;
  if (!props.tabs || !node) {
    peopleState.value = 'hidden';
    people.value = [];
    return;
  }
  peopleState.value = 'loading';
  try {
    const r = await props.api.listPermissions(node.path);
    if (seq !== peopleSeq) return;
    // RBAC off on this storage → there are no grants, and a section reading
    // "nobody" would misdescribe a drive where everyone can already read it.
    if (!r.storage_rbac) {
      peopleState.value = 'hidden';
      people.value = [];
      return;
    }
    people.value = [...(r.direct ?? []), ...(r.inherited ?? [])];
    peopleState.value = 'ok';
  } catch {
    if (seq !== peopleSeq) return;
    people.value = [];
    peopleState.value = 'hidden';
  }
}

/* One rule for naming a person (lib/personName). */
function grantPerson(g: Grant) {
  return { name: g.user_name, display_name: g.user_display_name, email: g.user_email };
}

function personName(g: Grant): string {
  return nameOfPerson(grantPerson(g)) || `#${g.user_id}`;
}

function personInitial(g: Grant): string {
  return initialOfPerson(grantPerson(g), localeTag(props.locale)) || '?';
}

/** The share-link row: the first live link, or the Create button. */
const primaryShare = computed(() => (shares.value.length ? shares.value[0] : null));
const shareBusy = ref(false);

async function createLink(): Promise<void> {
  const node = single.value;
  if (!node || shareBusy.value) return;
  shareBusy.value = true;
  try {
    const r = await props.api.createShare({ path: node.path });
    const url = r.share?.url ?? '';
    // Re-read the list rather than push the response into it: the LIST
    // endpoint is what a later copy or revoke acts on, and its `uuid` is the
    // numeric id the DELETE route wants — not the token the create response
    // carries under that name.
    const { shares: list } = await props.api.listShares(node.path);
    shares.value = Array.isArray(list) ? list : [];
    sharesState.value = 'ok';
    if (url) {
      emit('share-created', { path: node.path, url });
      await copyText(url);
    }
  } catch (err) {
    emit('toast', (err as Error).message);
  } finally {
    shareBusy.value = false;
  }
}

watch(
  () => [props.nodes.map((n) => n.path).join('|'), props.tabs] as const,
  () => void loadPeople(),
  { immediate: true },
);

/* Versions and comments are the two real feeds; when a selection has neither,
 * the Activity tab says so instead of showing two empty headings. */
const activityUsable = computed(
  () => versionsState.value !== 'hidden' || commentsState.value !== 'hidden',
);
/* === /surucu:d1 === */

watch(
  () => props.nodes.map((n) => n.path).join('\u0000'),
  () => void refresh(),
  { immediate: true },
);

/* === calisma:d3 — Yorumlar (node comments) ===
 * Flat chronological thread per node (files AND folders — every backend
 * row carries an id). Loaded through its own watcher + seq guard so the
 * existing refresh() stays untouched. Add/delete are NOT optimistic: the
 * list re-fetches after each successful API round-trip. The section hides
 * silently when the backend gates (401/403) or lacks (404) the endpoint.
 */
import type { NodeComment } from '../composables/useFileApi';

const commentsState = ref<SectionState>('hidden');
const comments = ref<NodeComment[]>([]);
const commentDraft = ref('');
const commentBusy = ref(false);
const commentNodeId = computed<number | null>(() =>
  typeof single.value?.id === 'number' ? (single.value.id as number) : null,
);
let commentSeq = 0;

async function loadComments(): Promise<void> {
  const seq = ++commentSeq;
  const id = commentNodeId.value;
  if (id == null) {
    commentsState.value = 'hidden';
    comments.value = [];
    return;
  }
  commentsState.value = comments.value.length > 0 ? 'ok' : 'loading';
  try {
    const list = await props.api.listComments(id);
    if (seq !== commentSeq) return;
    comments.value = list;
    commentsState.value = 'ok';
  } catch (err) {
    if (seq !== commentSeq) return;
    comments.value = [];
    const status = (err as { status?: number }).status;
    commentsState.value =
      status === 401 || status === 403 || status === 404 ? 'hidden' : 'error';
  }
}

async function sendComment(): Promise<void> {
  const id = commentNodeId.value;
  const body = commentDraft.value.trim();
  if (id == null || body === '' || commentBusy.value) return;
  commentBusy.value = true;
  try {
    await props.api.addComment(id, body);
    commentDraft.value = '';
    await loadComments();
  } catch (err) {
    emit('toast', (err as Error).message);
  } finally {
    commentBusy.value = false;
  }
}

async function removeComment(c: NodeComment): Promise<void> {
  if (commentBusy.value) return;
  commentBusy.value = true;
  try {
    await props.api.deleteComment(c.id);
    await loadComments();
  } catch (err) {
    emit('toast', (err as Error).message);
  } finally {
    commentBusy.value = false;
  }
}

/** Relative "3 minutes ago" stamp via Intl (locale-aware, no i18n keys). */
function relativeTime(iso: string): string {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return iso;
  const diffS = Math.round((ms - Date.now()) / 1000);
  const abs = Math.abs(diffS);
  const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ['year', 31536000],
    ['month', 2592000],
    ['day', 86400],
    ['hour', 3600],
    ['minute', 60],
  ];
  try {
    const rtf = new Intl.RelativeTimeFormat(
      localeTag(props.locale),
      { numeric: 'auto' },
    );
    for (const [unit, secs] of units) {
      if (abs >= secs) return rtf.format(Math.trunc(diffS / secs), unit);
    }
    return rtf.format(diffS, 'second');
  } catch {
    return formatDateStr(iso);
  }
}

watch(
  () => commentNodeId.value,
  () => {
    comments.value = [];
    commentDraft.value = '';
    void loadComments();
  },
  { immediate: true },
);
/* === /calisma:d3 === */
</script>

<template>
  <aside
    class="fe-inspector"
    :class="{ 'fe-inspector--overlay': narrow }"
    role="complementary"
    :aria-label="t('inspector.title')"
  >
    <!-- The head is about the ITEM, not about the panel: its glyph, its name
         and one caption naming what it is. "Copy name" is next to the name it
         copies; the close button is last, on the edge. -->
    <header class="fe-inspector__head">
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons -->
      <span class="fe-inspector__head-icon" aria-hidden="true" v-html="headIcon"></span>
      <div class="fe-inspector__head-text">
        <h2 class="fe-inspector__title" :title="headName"><bdi>{{ headName }}</bdi></h2>
        <p class="fe-inspector__caption">{{ headCaption }}</p>
        <!-- pane:p1 — the held-subject line. Drawn ONLY while this panel is
             describing the last selected thing rather than a live selection,
             and it names the folder that thing is in, because with a split
             window that folder may not be the one on screen. `role="note"`:
             it is a fact about the panel, not another fact about the file. -->
        <p v-if="heldIn" class="fe-inspector__held" role="note" data-testid="inspector-held">
          {{ t('inspector.held', { where: heldIn }) }}
        </p>
      </div>
      <button
        v-if="single"
        type="button"
        class="fe-inspector__iconbtn"
        :title="t('inspector.copy_name')"
        :aria-label="t('inspector.copy_name')"
        data-testid="inspector-copy-name"
        @click="copyText(headName)"
      >
        <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
        <span aria-hidden="true" v-html="actionIconSvg('copy')"></span>
      </button>
      <button
        type="button"
        class="fe-inspector__iconbtn fe-inspector__close"
        :title="t('inspector.close')"
        :aria-label="t('inspector.close')"
        @click="emit('close')"
      >
        <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
        <span aria-hidden="true" v-html="actionIconSvg('close')"></span>
      </button>
    </header>

    <!-- surucu:d1 — Details / Activity. Rendered only in the drive shell; the
         classic panel keeps its single flat scroll of sections. -->
    <div v-if="tabs" class="fe-inspector__tabs" role="tablist" :aria-label="t('inspector.title')">
      <button
        type="button"
        class="fe-inspector__tab"
        :class="{ 'is-active': tab === 'details' }"
        role="tab"
        :aria-selected="tab === 'details'"
        data-testid="inspector-tab-details"
        @click="tab = 'details'"
      >{{ t('inspector.tab.details') }}</button>
      <button
        type="button"
        class="fe-inspector__tab"
        :class="{ 'is-active': tab === 'activity' }"
        role="tab"
        :aria-selected="tab === 'activity'"
        data-testid="inspector-tab-activity"
        @click="tab = 'activity'"
      >{{ t('inspector.tab.activity') }}</button>
    </div>

    <div class="fe-inspector__scroll">
      <!-- ══ Genel ══ -->
      <section v-if="!tabs || tab === 'details'" class="fe-inspector__section">
        <h3 class="fe-inspector__heading">{{ t('inspector.section.general') }}</h3>

        <!-- App plugins — an app is holding this file read-only. -->
        <p v-if="lockLine" class="fe-inspector__lock" role="note" data-testid="inspector-lock">
          <span aria-hidden="true">&#128274;</span>
          <span><strong>{{ t('applock.inspector') }}</strong><br />{{ lockLine }}</span>
        </p>

        <!-- issue #34 — a link the server will not follow. -->
        <p
          v-if="linkNote"
          class="fe-inspector__linknote"
          role="note"
          data-testid="inspector-symlink"
          :data-link-state="linkNote.state"
        >
          <span aria-hidden="true">&#128279;</span>
          <span><strong>{{ t('symlink.inspector') }}</strong><br />{{ linkNote.why }}</span>
        </p>

        <!-- Multi selection → summary. The head already counts them, so this
             is the one fact the head does not carry. -->
        <dl v-if="isMulti" class="fe-inspector__meta">
          <div class="fe-inspector__row">
            <dt>{{ t('inspector.size') }}</dt>
            <dd :title="nodeSizeHint(multiSize)">{{ formatNodeSize(multiSize) }}</dd>
          </div>
        </dl>

        <!-- Single selection → full meta.
             ⚠ No Owner row. There is no per-node owner on the wire
             (`handlers/shared.go`: "There is no per-node owner"), so the
             reference shell's "Owner: You" would be a constant wearing the
             shape of a fact. -->
        <template v-else-if="single">
          <!-- The thumbnail, when the host resolved one. Wide and short: this
               is the one place in the panel where a picture beats a word, and
               the 30px square in the head is not that place.
               ⚠ INSIDE this branch, not before it: a `v-if` sibling between
               `v-if="isMulti"` and this `v-else-if` silently captures the
               else — which is exactly what happened, and the whole General
               table stopped rendering while the heading above it stayed. -->
          <img
            v-if="previewSrc"
            class="fe-inspector__preview"
            :src="previewSrc"
            alt=""
            aria-hidden="true"
          />
          <dl class="fe-inspector__meta">
            <div class="fe-inspector__row">
              <dt>{{ t('inspector.type') }}</dt>
              <dd>{{ typeLabelFor(single, t) }}</dd>
            </div>
            <div class="fe-inspector__row">
              <dt>{{ t('inspector.path') }}</dt>
              <dd class="fe-inspector__pathcell">
                <span class="fe-inspector__path" :title="single.path"><bdi>{{ single.path }}</bdi></span>
                <button
                  type="button"
                  class="fe-inspector__copy"
                  :title="t('inspector.copy')"
                  :aria-label="t('inspector.copy')"
                  @click="copyText(single.path)"
                >
                  <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
                  <span aria-hidden="true" v-html="actionIconSvg('copy')"></span>
                </button>
              </dd>
            </div>
            <div v-if="isFile" class="fe-inspector__row">
              <dt>{{ t('inspector.size') }}</dt>
              <dd>{{ formatSize(typeof single.size === 'number' ? single.size : null) }}</dd>
            </div>
            <div class="fe-inspector__row">
              <dt>{{ t('inspector.modified') }}</dt>
              <dd>{{ formatDate(single.last_modified) }}</dd>
            </div>
            <div v-if="isFile && single.mime_type" class="fe-inspector__row">
              <dt>{{ t('inspector.mime') }}</dt>
              <dd class="fe-inspector__mime">{{ single.mime_type }}</dd>
            </div>
            <!-- ⚠ The node id lives HERE and nowhere else (owner's call,
                 2026-09-13). It used to be a row in the right-click menu and the
                 selection bar — a developer's handle on a support ticket sitting
                 in front of everyone, twice. This panel is where the other
                 technical facts about a file already are (Path, MIME, ETag), and
                 it is still one click to copy. Drawn only when the row actually
                 has one: a client-synthesized row (a multi-storage folder) has
                 no backend id, and an empty "ID —" teaches nothing.

                 ⚠⚠ And only for an ADMINISTRATOR (Burak, 2026-09-23): the id
                 is the handle admin screens take (Admin → File history, an
                 audit row), and it says nothing to anyone else. Here, in the
                 shared panel, so every surface that draws it — the explorer,
                 the Drive shell, an embed — answers the same way. -->
            <div v-if="callerAdmin && typeof single.id === 'number'" class="fe-inspector__row">
              <dt>{{ t('inspector.nodeId') }}</dt>
              <dd class="fe-inspector__pathcell">
                <span class="fe-inspector__path">{{ single.id }}</span>
                <button
                  type="button"
                  class="fe-inspector__copy"
                  :title="t('inspector.copy')"
                  :aria-label="t('inspector.copy')"
                  @click="copyText(String(single.id))"
                >
                  <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
                  <span aria-hidden="true" v-html="actionIconSvg('copy')"></span>
                </button>
              </dd>
            </div>
            <div v-if="etag" class="fe-inspector__row">
              <dt>{{ t('inspector.etag') }}</dt>
              <dd class="fe-inspector__pathcell">
                <span class="fe-inspector__path" :title="etag">{{ shortHash(etag) }}</span>
                <button
                  type="button"
                  class="fe-inspector__copy"
                  :title="t('inspector.copy')"
                  :aria-label="t('inspector.copy')"
                  @click="copyText(etag)"
                >
                  <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
                  <span aria-hidden="true" v-html="actionIconSvg('copy')"></span>
                </button>
              </dd>
            </div>
          </dl>
        </template>

        <!-- No selection → nothing more to say. The head already names the
             folder you are in and counts what is in it. -->
        <p v-else class="fe-inspector__empty">{{ t('inspector.select_hint') }}</p>
      </section>

      <!-- ══ etiket:t1 — Etiketler ══
           The SAME `TagPicker` the context menu opens in a modal, mounted
           inline. Not a second tag editor: one component, so the chips, the
           colours and the add flow are identical wherever tags are edited,
           and a fix to any of them reaches both places at once. -->
      <section
        v-if="canTag && (!tabs || tab === 'details')"
        class="fe-inspector__section"
        data-testid="inspector-tags"
      >
        <h3 class="fe-inspector__heading">{{ t('inspector.section.tags') }}</h3>
        <TagPicker
          :node-id="nodeId as number"
          :locale="locale"
          :api-base="apiBase"
          :auth-headers="authHeaders"
          :auth-credentials="authCredentials"
          @change="(tags: string[]) => emit('tags-changed', tags)"
          @open="(tag: string, kind: TagKind) => emit('open-tag', tag, kind) /* etiket:k2 —
                 the KIND travels with the name: a file can carry a personal
                 and a team tag of one name, and the chip that was clicked
                 decides which view opens. */"
          @error="() => emit('toast', t('inspector.error'))"
        />
      </section>

      <!-- ══ App plugins — one collapsible section per matching `inspector`
           view. Lazy: nothing is requested until the section is opened. -->
      <template v-if="single && (!tabs || tab === 'details')">
        <PluginInspectorSection
          v-for="v in pluginSections"
          :key="`${v.plugin}/${v.id}`"
          :api="api"
          :view="v"
          :path="single.path"
          :locale="locale"
          :theme="theme"
          :storages="storages"
          @op="(op) => emit('plugin-op', op)"
          @toast="(m) => emit('toast', m)"
          @open="(req) => emit('plugin-open', { plugin: v.plugin, req })"
        />
      </template>

      <!-- ══ Sürümler ══ -->
      <section
        v-if="(versionsState === 'ok' || versionsState === 'error') && (!tabs || tab === 'activity')"
        class="fe-inspector__section"
      >
        <h3 class="fe-inspector__heading">{{ t('inspector.section.versions') }}</h3>

        <p v-if="versionsState === 'error'" class="fe-inspector__empty">
          {{ t('inspector.error') }}
        </p>
        <template v-else>
          <p v-if="versions.length === 0" class="fe-inspector__empty">
            {{ t('inspector.versions.empty') }}
          </p>
          <ul v-else class="fe-inspector__versions">
            <li v-for="v in versions" :key="v.id" class="fe-inspector__version">
              <div class="fe-inspector__version-main">
                <span class="fe-inspector__version-n">
                  {{ t('inspector.versions.v', { n: v.version_n }) }}
                </span>
                <span class="fe-inspector__version-meta">
                  {{ formatDateStr(v.created_at) }} · {{ formatSize(v.size) }}
                </span>
              </div>
              <div v-if="confirmVersionId === v.id" class="fe-inspector__confirm">
                <p class="fe-inspector__confirm-q">{{ t('inspector.versions.restore_confirm') }}</p>
                <label class="fe-inspector__check">
                  <input v-model="snapshotFirst" type="checkbox" />
                  {{ t('inspector.versions.snapshot_current') }}
                </label>
                <div class="fe-inspector__confirm-actions">
                  <button
                    type="button"
                    class="fe-btn fe-btn--primary fe-btn--sm"
                    :disabled="versionBusy"
                    :aria-busy="versionWork === 'restore'"
                    @click="confirmRestore(v)"
                  >{{ versionWork === 'restore' ? t('inspector.versions.restoring') : t('inspector.versions.confirm') }}</button>
                  <button
                    type="button"
                    class="fe-btn fe-btn--sm"
                    :disabled="versionBusy"
                    @click="confirmVersionId = null"
                  >{{ t('inspector.versions.cancel') }}</button>
                </div>
              </div>
              <button
                v-else
                type="button"
                class="fe-btn fe-btn--sm"
                :disabled="versionBusy"
                @click="askRestore(v)"
              >{{ t('inspector.versions.restore') }}</button>
            </li>
          </ul>
          <button
            type="button"
            class="fe-btn fe-btn--sm fe-inspector__snapshot"
            :disabled="versionBusy"
            :aria-busy="versionWork === 'snapshot'"
            @click="takeSnapshot"
          >{{ versionWork === 'snapshot' ? t('inspector.versions.snapshotting') : t('inspector.versions.take_snapshot') }}</button>
        </template>
      </section>

      <!-- ══ İzinler ══ -->
      <!-- ⚠ surucu:d1 — stands down when "People with access" is on screen. The
           two say the same thing there (your own row is in the roster, with the
           same level) and each carries its own button into the SAME modal —
           two doors into one room, six lines apart. It stays for every other
           case, which is exactly the case where People is hidden: RBAC off on
           the storage, or the server refusing the grant list to a non-owner. -->
      <section
        v-if="
          single &&
          effectivePerm &&
          (!tabs || tab === 'details') &&
          !(tabs && peopleState === 'ok')
        "
        class="fe-inspector__section"
      >
        <h3 class="fe-inspector__heading">{{ t('inspector.section.permissions') }}</h3>
        <div class="fe-inspector__permrow">
          <span
            class="fe-inspector__badge"
            :class="`fe-inspector__badge--${effectivePerm}`"
          >{{ permLabel(effectivePerm) }}</span>
          <button
            v-if="canManagePerms"
            type="button"
            class="fe-btn fe-btn--sm"
            @click="emit('manage-permissions', single)"
          >{{ t('inspector.perm.manage') }}</button>
        </div>
      </section>

      <!-- ══ surucu:d1 — People with access ══
           Real grants from `GET /api/files/permissions`; hidden entirely when
           the caller is not an owner (403) or the storage has RBAC off, rather
           than drawn empty. -->
      <section
        v-if="tabs && tab === 'details' && peopleState === 'ok'"
        class="fe-inspector__section"
        data-testid="inspector-people"
      >
        <h3 class="fe-inspector__heading">{{ t('inspector.people') }}</h3>
        <p v-if="people.length === 0" class="fe-inspector__empty">
          {{ t('inspector.people.empty') }}
        </p>
        <ul v-else class="fe-inspector__people">
          <li v-for="g in people" :key="`${g.id}-${g.user_id}`" class="fe-inspector__person">
            <span class="fe-inspector__avatar" aria-hidden="true">{{ personInitial(g) }}</span>
            <span class="fe-inspector__person-main">
              <span class="fe-inspector__person-name" :title="g.user_email">{{ personName(g) }}</span>
              <span class="fe-inspector__person-sub">
                {{ permLabel(g.level) }}<template v-if="g.inherited"> · {{ t('inspector.people.inherited') }}</template>
              </span>
            </span>
          </li>
        </ul>
        <button
          v-if="canManagePerms && single"
          type="button"
          class="fe-btn fe-btn--sm"
          @click="emit('manage-permissions', single)"
        >{{ t('inspector.people.manage') }}</button>
      </section>

      <!-- ══ Paylaşımlar ══ -->
      <section
        v-if="sharesState === 'ok' && (!tabs || tab === 'details')"
        class="fe-inspector__section"
        data-testid="inspector-shares"
      >
        <h3 class="fe-inspector__heading">
          {{ tabs ? t('inspector.link') : t('inspector.section.shares') }}
        </h3>
        <!-- surucu:d1 — the mockup's link row: what the state IS, and the one
             button that changes it. Only when there is no link yet; the list
             below is what an item that HAS links has always shown. -->
        <div v-if="tabs && shares.length === 0" class="fe-inspector__linkrow">
          <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
          <span class="fe-inspector__linkicon" aria-hidden="true" v-html="actionIconSvg('link')"></span>
          <span class="fe-inspector__linknone">{{ t('inspector.link.none') }}</span>
          <button
            type="button"
            class="fe-btn fe-btn--sm"
            :disabled="shareBusy || !single"
            data-testid="inspector-create-link"
            @click="createLink"
          >{{ t('inspector.link.create') }}</button>
        </div>
        <p v-else-if="shares.length === 0" class="fe-inspector__empty">
          {{ t('inspector.shares.empty') }}
        </p>
        <ul v-else class="fe-inspector__shares">
          <li v-for="s in shares" :key="s.uuid" class="fe-inspector__share">
            <span class="fe-inspector__share-url" :title="s.url">{{ s.url }}</span>
            <span v-if="s.password_pin" class="fe-inspector__badge fe-inspector__badge--pin">PIN</span>
            <span v-if="s.expires_at" class="fe-inspector__share-exp">
              {{ formatDateStr(s.expires_at) }}
            </span>
            <button
              type="button"
              class="fe-inspector__copy"
              :title="t('inspector.shares.copy')"
              :aria-label="t('inspector.shares.copy')"
              @click="copyText(s.url)"
            >
              <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
              <span aria-hidden="true" v-html="actionIconSvg('copy')"></span>
            </button>
          </li>
        </ul>
      </section>

      <!-- ══ calisma:d3 — Yorumlar ══ -->
      <section
        v-if="(commentsState === 'ok' || commentsState === 'error') && (!tabs || tab === 'activity')"
        class="fe-inspector__section"
      >
        <h3 class="fe-inspector__heading">
          {{ t('inspector.section.comments') }}
          <span
            v-if="comments.length > 0"
            class="fe-inspector__countbadge"
          >{{ comments.length }}</span>
        </h3>

        <p v-if="commentsState === 'error'" class="fe-inspector__empty">
          {{ t('inspector.error') }}
        </p>
        <template v-else>
          <p v-if="comments.length === 0" class="fe-inspector__empty">
            {{ t('inspector.comments.empty') }}
          </p>
          <ul v-else class="fe-inspector__comments">
            <li v-for="c in comments" :key="c.id" class="fe-inspector__comment">
              <div class="fe-inspector__comment-top">
                <span class="fe-inspector__comment-author" :title="c.author_name">
                  {{ c.author_name || '—' }}
                </span>
                <span class="fe-inspector__comment-time" :title="formatDateStr(c.created_at)">
                  {{ relativeTime(c.created_at) }}
                </span>
                <button
                  v-if="c.can_delete"
                  type="button"
                  class="fe-inspector__comment-del"
                  :disabled="commentBusy"
                  :title="t('inspector.comments.delete')"
                  :aria-label="t('inspector.comments.delete')"
                  @click="removeComment(c)"
                >×</button>
              </div>
              <p class="fe-inspector__comment-body">{{ c.body }}</p>
            </li>
          </ul>
          <form class="fe-inspector__comment-form" @submit.prevent="sendComment">
            <input
              v-model="commentDraft"
              type="text"
              class="fe-inspector__comment-input"
              maxlength="5000"
              :placeholder="t('inspector.comments.placeholder')"
              :aria-label="t('inspector.comments.placeholder')"
              :disabled="commentBusy"
            />
            <button
              type="submit"
              class="fe-btn fe-btn--primary fe-btn--sm"
              :disabled="commentBusy || commentDraft.trim() === ''"
            >{{ t('inspector.comments.send') }}</button>
          </form>
        </template>
      </section>
      <!-- ══ /calisma:d3 ══ -->

      <!-- surucu:d1 — the Activity tab with nothing behind it. It says which
           two feeds fill it, because there is no third one to wait for: this
           server keeps no per-file audit trail (target_id is empty for every
           file action, and the log is admin-only). -->
      <section
        v-if="tabs && tab === 'activity' && !activityUsable"
        class="fe-inspector__section"
        data-testid="inspector-activity-empty"
      >
        <p class="fe-inspector__empty">
          {{ single ? t('inspector.activity.empty') : t('inspector.activity.select') }}
        </p>
        <p v-if="single" class="fe-inspector__hint">{{ t('inspector.activity.hint') }}</p>
      </section>
    </div>
  </aside>
</template>
