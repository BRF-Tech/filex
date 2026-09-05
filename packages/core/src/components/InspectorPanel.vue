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
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { fileIconSvg } from '../lib/fileIcons';

const props = defineProps<{
  api: FileApi;
  /** Current selection (empty array → current-folder summary). */
  nodes: FileNode[];
  /** Display label of the folder being viewed (for the no-selection state). */
  dirLabel: string;
  /** Number of entries in the folder being viewed. */
  dirCount: number;
  /** RBAC effective level of the current dir ('' = ACL not enforced). */
  dirPerm?: string;
  locale: LocaleCode;
  /** Narrow/embed mode → full-size overlay presentation. */
  narrow?: boolean;
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
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  /** surucu:d1 — a share link was minted from the panel. */
  (e: 'share-created', payload: { path: string; url: string }): void;
  (e: 'manage-permissions', node: FileNode): void;
  (e: 'toast', message: string): void;
  /** Fired after a successful restore/snapshot so the host can reload. */
  (e: 'changed'): void;
}>();

const { t, formatSize, nodeDisplayName } = useLocale(() => props.locale);

// ── selection shape ──────────────────────────────────────────────────
const single = computed<FileNode | null>(() =>
  props.nodes.length === 1 ? props.nodes[0] : null,
);
const isMulti = computed(() => props.nodes.length > 1);
const isFile = computed(() => single.value?.type === 'file');
const nodeId = computed<number | null>(() =>
  typeof single.value?.id === 'number' ? (single.value.id as number) : null,
);
const multiTotal = computed(() =>
  props.nodes.reduce((acc, n) => acc + (typeof n.size === 'number' ? n.size : 0), 0),
);
const etag = computed<string | null>(() => {
  const v = single.value?.etag;
  return typeof v === 'string' && v !== '' ? v : null;
});
const thumb = computed<string | null>(() =>
  single.value && isFile.value && props.thumbSrc ? props.thumbSrc(single.value) : null,
);

function shortHash(h: string): string {
  return h.length > 12 ? `${h.slice(0, 12)}…` : h;
}

function formatDate(ms: number | undefined): string {
  if (!ms) return '—';
  try {
    return new Date(ms).toLocaleString(props.locale === 'en' ? 'en-GB' : 'tr-TR', {
      dateStyle: 'medium',
      timeStyle: 'short',
    });
  } catch {
    return new Date(ms).toISOString();
  }
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
const canManagePerms = computed(
  () => effectivePerm.value === 'editor' || effectivePerm.value === 'owner',
);
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
const versionBusy = ref(false);

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
  versionBusy.value = true;
  try {
    await props.api.restoreVersion(id, v.id, snapshotFirst.value);
    emit('toast', t('inspector.versions.restored'));
    confirmVersionId.value = null;
    emit('changed');
    await loadVersions(++refreshSeq);
  } catch (err) {
    emit('toast', (err as Error).message);
  } finally {
    versionBusy.value = false;
  }
}

async function takeSnapshot(): Promise<void> {
  const id = nodeId.value;
  if (id == null || versionBusy.value) return;
  versionBusy.value = true;
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
    versionBusy.value = false;
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

function personName(g: Grant): string {
  return g.user_display_name || g.user_email || `#${g.user_id}`;
}

function personInitial(g: Grant): string {
  const n = personName(g).trim();
  return n ? n[0].toUpperCase() : '?';
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
  () => props.nodes.map((n) => n.path).join(' '),
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
      props.locale === 'en' ? 'en' : 'tr',
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
    <header class="fe-inspector__head">
      <h2 class="fe-inspector__title">{{ t('inspector.title') }}</h2>
      <button
        type="button"
        class="fe-inspector__close"
        :title="t('inspector.close')"
        :aria-label="t('inspector.close')"
        @click="emit('close')"
      >×</button>
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

        <!-- Multi selection → summary -->
        <div v-if="isMulti" class="fe-inspector__hero">
          <span class="fe-inspector__bigicon" v-html="fileIconSvg({ type: 'dir' })"></span>
          <p class="fe-inspector__name">
            {{ t('inspector.items_summary', { n: nodes.length, size: formatSize(multiTotal) }) }}
          </p>
        </div>

        <!-- Single selection → full meta -->
        <template v-else-if="single">
          <div class="fe-inspector__hero">
            <img
              v-if="thumb"
              class="fe-inspector__thumb"
              :src="thumb"
              alt=""
              aria-hidden="true"
            />
            <span
              v-else
              class="fe-inspector__bigicon"
              v-html="fileIconSvg(single)"
            ></span>
            <p class="fe-inspector__name" :title="single.basename">
              {{ nodeDisplayName(single) }}
            </p>
          </div>

          <dl class="fe-inspector__meta">
            <div class="fe-inspector__row">
              <dt>{{ t('inspector.path') }}</dt>
              <dd class="fe-inspector__pathcell">
                <span class="fe-inspector__path" :title="single.path">{{ single.path }}</span>
                <button
                  type="button"
                  class="fe-inspector__copy"
                  :title="t('inspector.copy')"
                  :aria-label="t('inspector.copy')"
                  @click="copyText(single.path)"
                >⧉</button>
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
                >⧉</button>
              </dd>
            </div>
          </dl>
        </template>

        <!-- No selection → current folder summary -->
        <div v-else class="fe-inspector__hero">
          <span class="fe-inspector__bigicon" v-html="fileIconSvg({ type: 'dir' })"></span>
          <p class="fe-inspector__name" :title="dirLabel">{{ dirLabel }}</p>
          <p class="fe-inspector__sub">{{ t('inspector.folder_items', { n: dirCount }) }}</p>
        </div>
      </section>

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
                    @click="confirmRestore(v)"
                  >{{ t('inspector.versions.confirm') }}</button>
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
            @click="takeSnapshot"
          >{{ t('inspector.versions.take_snapshot') }}</button>
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
          <span class="fe-inspector__linkicon" aria-hidden="true">🔗</span>
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
            >⧉</button>
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
