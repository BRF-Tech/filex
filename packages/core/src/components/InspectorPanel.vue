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
import type { FileApi, NodeVersion } from '../composables/useFileApi';
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
}>();

const emit = defineEmits<{
  (e: 'close'): void;
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

watch(
  () => props.nodes.map((n) => n.path).join(' '),
  () => void refresh(),
  { immediate: true },
);
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

    <div class="fe-inspector__scroll">
      <!-- ══ Genel ══ -->
      <section class="fe-inspector__section">
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
        v-if="versionsState === 'ok' || versionsState === 'error'"
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
      <section v-if="single && effectivePerm" class="fe-inspector__section">
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

      <!-- ══ Paylaşımlar ══ -->
      <section v-if="sharesState === 'ok'" class="fe-inspector__section">
        <h3 class="fe-inspector__heading">{{ t('inspector.section.shares') }}</h3>
        <p v-if="shares.length === 0" class="fe-inspector__empty">
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
    </div>
  </aside>
</template>
