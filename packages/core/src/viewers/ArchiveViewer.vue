<script setup lang="ts">
/**
 * ArchiveViewer — read-only preview of an archive's contents, browsed like a
 * folder (a breadcrumb, folders first), for every format the server reads.
 *
 * Hits the configured archive-list endpoint (`archiveListUrl`, default
 * `POST /api/files/archive/list`) with the adapter-qualified path. An
 * encrypted archive answers PASSWORD_REQUIRED / BAD_PASSWORD and the shared
 * password dialog asks; the listing it unlocks is remembered for the session
 * (`archivePreviewCache`). Extraction is exposed elsewhere (context menu /
 * actions panel): the viewer's job is just "what's inside?".
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */
import { fileIconTile } from '../lib/fileIcons'; /* ikon:emoji */
import DataTable, { type DataColumn } from '../components/DataTable.vue';
import { useLocale } from '../composables/useLocale';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { ArchivePreviewCache } from '../lib/archivePreviewCache';
import ArchivePasswordModal from '../modals/ArchivePasswordModal.vue';

interface ArchiveEntry {
  name: string;
  size: number;
  mtime?: string;
  is_dir?: boolean;
}

interface ArchiveRow extends ArchiveEntry {
  displayName: string;
  fullPath: string;
}

const props = withDefaults(defineProps<{
  url: string;
  filePath?: string;
  ext: string;
  /** The interface language — the size column is written the way it writes
   *  numbers ("1,96 KB"), like every other size in the product. */
  locale?: LocaleCode;
  t?: (key: string, vars?: Record<string, string | number>) => string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
  /** ⚠ Where to ask. This used to be the literal '/api/files/archive/list',
   *  which is the ONE thing an embed cannot assume: with `apiBase` pointing at
   *  another origin the request went to the host page instead and 404'd, while
   *  every other call in the package honoured the configured base. The default
   *  keeps a same-origin install working unchanged. */
  archiveListUrl?: string;
  archivePreviewCache?: ArchivePreviewCache;
}>(), {
  locale: 'en',
});

const emit = defineEmits<{ (e: 'close'): void }>();
const entries = ref<ArchiveEntry[]>([]);
const currentDir = ref('');
const loading = ref(true);
const error = ref<string | null>(null);
const password = ref('');
const passwordNeeded = ref(false);
const passwordError = ref('');
/** The listing has taken long enough to say why. */
const slow = ref(false);

/* ⚠ The server reads the WHOLE archive from its storage before it can list
 * it — minutes for a large one on an object store — and all that time this
 * said "Loading…". Past SLOW_LISTING_MS it says what it is waiting for.
 *
 * Each listing is also THIS file's: stepping to the next file started a second
 * listing beside the first, and whichever ended last filled the screen, so the
 * big zip's contents (or its "could not read") could land under the small
 * one's name. A new listing aborts the old one — which also stops the
 * server's download — and an answer that is no longer the latest is dropped. */
const SLOW_LISTING_MS = 3000;
let listing = 0;
let inFlight: AbortController | null = null;
let slowTimer: ReturnType<typeof setTimeout> | undefined;

/** ⚠ `vars` go THROUGH `t()`, not into a `.replace()` afterwards: `t()` is what
 *  picks the singular (`viewer.archive.entries_one`) from the count, and it can
 *  only do that if it is told the count. Same shape CsvViewer's helper has. */
function tt(key: string, fallback: string, vars?: Record<string, string | number>): string {
  return props.t ? props.t(key, vars) : fallback;
}

/* ⚠ The product's one byte formatter (useLocale.formatSize). This file had
   its own — base 1024, a dot, English units — so a zip's member read
   "1.9 KB" beside the explorer's "1,96 KB" for the same bytes (QA,
   2026-09-21). */
const { formatSize } = useLocale(() => props.locale ?? 'en');
function fmtSize(n: number): string {
  return formatSize(n);
}

/** Stops the listing in flight, if any: its answer is no longer wanted. */
function stopListing(): number {
  inFlight?.abort();
  inFlight = null;
  clearTimeout(slowTimer);
  slow.value = false;
  return ++listing;
}

async function load(): Promise<void> {
  const mine = stopListing();
  loading.value = true;
  error.value = null;
  passwordError.value = '';
  entries.value = [];
  if (!props.filePath) {
    error.value = tt('viewer.archive.error', 'Could not read archive contents.');
    loading.value = false;
    return;
  }
  if (!password.value) {
    const cached = props.archivePreviewCache?.recall(props.filePath);
    if (cached) {
      entries.value = cached;
      passwordNeeded.value = false;
      loading.value = false;
      return;
    }
  }
  const ctl = new AbortController();
  inFlight = ctl;
  slowTimer = setTimeout(() => {
    if (mine === listing) slow.value = true;
  }, SLOW_LISTING_MS);
  try {
    const auth = props.authHeaders ? await props.authHeaders() : {};
    if (mine !== listing) return;
    const res = await fetch(props.archiveListUrl || '/api/files/archive/list', {
      method: 'POST',
      credentials: props.authCredentials || 'same-origin',
      headers: {
        'Content-Type': 'application/json',
        ...auth,
      },
      body: JSON.stringify({ path: props.filePath, password: password.value || undefined }),
      signal: ctl.signal,
    });
    if (mine !== listing) return;
    if (!res.ok) {
      /* ⚠ Never the raw status. This printed "503 Service Unavailable" (or a
       * JSON body) straight into the preview — the same failure the owner
       * reported for ONLYOFFICE ("Config fetch 503: {…}"): a person is shown
       * the HTTP layer instead of what happened. The server's reason, when it
       * sent one we know, becomes a sentence; anything else is the generic
       * "could not read the archive". */
      const body = await res.json().catch(() => ({})) as { code?: string; error?: string };
      if (mine !== listing) return;
      if (body.code === 'PASSWORD_REQUIRED' || body.code === 'BAD_PASSWORD') {
        props.archivePreviewCache?.forget(props.filePath);
        passwordNeeded.value = true;
        passwordError.value = body.code === 'BAD_PASSWORD'
          ? tt('archive.bad_password', 'The archive password is incorrect.')
          : '';
        return;
      }
      throw new ArchiveError(archiveErrorText(res.status, body.code ?? body.error ?? ''));
    }
    const body = (await res.json()) as { entries?: ArchiveEntry[] };
    if (mine !== listing) return;
    entries.value = body.entries ?? [];
    if (password.value) props.archivePreviewCache?.remember(props.filePath, entries.value);
    passwordNeeded.value = false;
  } catch (err) {
    if (mine !== listing) return;
    error.value =
      err instanceof ArchiveError ? err.message : tt('viewer.archive.error', 'Could not read archive contents.');
  } finally {
    if (mine === listing) {
      loading.value = false;
      inFlight = null;
      clearTimeout(slowTimer);
      slow.value = false;
    }
  }
}

/** A failure already worded for a person. Anything else that throws (the
 *  network, a JSON parse) reads as the generic sentence. */
class ArchiveError extends Error {}

function archiveErrorText(status: number, code: string): string {
  if (/not a zip|unsupported|not supported|format/i.test(code)) {
    return tt('viewer.archive.unsupported', 'This kind of archive cannot be listed here. Download it to see what is inside.');
  }
  if (status === 413 || /too large|too big/i.test(code)) {
    return tt('viewer.archive.too_large', 'This archive is too large to list here. Download it to see what is inside.');
  }
  return tt('viewer.archive.error', 'Could not read archive contents.');
}


/**
 * The entries, in THE table (DataTable — the explorer's own). ⚠ It used to be
 * the one table in the package allowed to be its own, with the reasoning that
 * a file's contents answer to the viewer's layout. The owner's rule since
 * 2026-09-21 has no such exception — "bir yere tablo gerekiyorsa bu tabloyu
 * koymak zorundayız" — and an archive's listing is a file listing: it wants
 * to be sorted by size and have its Name column widened exactly the way the
 * folder it came from does.
 */
const columns = computed<DataColumn<ArchiveRow>[]>(() => [
  {
    id: 'name',
    label: tt('viewer.name', 'Name'),
    sortable: true,
    width: 360,
    class: 'filex-viewer-archive__name',
    title: (e) => e.name,
  },
  {
    id: 'size',
    label: tt('viewer.size', 'Size'),
    sortable: true,
    width: 110,
    align: 'right',
    class: 'filex-viewer-archive__size',
    format: (e) => (e.is_dir ? '' : fmtSize(e.size)),
    sortValue: (e) => (e.is_dir ? -1 : e.size),
  },
]);


/* === ikon:emoji — the fallback screen's mark ==========================
 * Every viewer opened its "cannot show this" / "still loading" screen with a
 * 48px colour emoji, one per format, each from whatever emoji font the OS
 * shipped. The format mark is `lib/fileIcons`'s tile — the SAME tile the row
 * the person just clicked is wearing, so the fallback is recognisably about
 * that file — and "loading" is the stroked ring, spun by CSS, because no
 * still picture can say "still going". */
function submitPassword(value: string) {
  password.value = value;
  void load();
}

onMounted(load);
/* Closing the preview stops the server reading an archive nobody will see. */
onBeforeUnmount(() => {
  stopListing();
});
watch(() => props.filePath, () => {
  currentDir.value = '';
  password.value = '';
  passwordNeeded.value = false;
  void load();
});

const totalSize = computed(() => entries.value.reduce((sum, entry) => sum + (entry.size || 0), 0));
const fileCount = computed(() => entries.value.filter((entry) => !entry.is_dir).length);

function cleanPath(value: string): string {
  return value.replace(/\\/g, '/').replace(/^\/+|\/+$/g, '');
}

const visibleEntries = computed<ArchiveRow[]>(() => {
  const rows = new Map<string, ArchiveRow>();
  for (const entry of entries.value) {
    const fullPath = cleanPath(entry.name);
    if (!fullPath || (currentDir.value && !fullPath.startsWith(currentDir.value))) continue;
    const rest = currentDir.value ? fullPath.slice(currentDir.value.length) : fullPath;
    if (!rest) continue;
    const slash = rest.indexOf('/');
    if (slash >= 0) {
      const displayName = rest.slice(0, slash);
      const dirPath = `${currentDir.value}${displayName}`;
      if (!rows.has(dirPath)) {
        rows.set(dirPath, { name: dirPath, fullPath: dirPath, displayName, size: 0, is_dir: true });
      }
      continue;
    }
    rows.set(fullPath, { ...entry, name: fullPath, fullPath, displayName: rest });
  }
  return [...rows.values()].sort((a, b) => {
    if (!!a.is_dir !== !!b.is_dir) return a.is_dir ? -1 : 1;
    return a.displayName.localeCompare(b.displayName, undefined, { numeric: true, sensitivity: 'base' });
  });
});

const breadcrumbs = computed(() => {
  const parts = cleanPath(currentDir.value).split('/').filter(Boolean);
  return parts.map((name, index) => ({ name, path: `${parts.slice(0, index + 1).join('/')}/` }));
});

function openRow(row: ArchiveRow) {
  if (row.is_dir) currentDir.value = `${cleanPath(row.fullPath)}/`;

}
const typeTile = computed(() => fileIconTile({ type: 'file', extension: props.ext }));

function entryTile(entry: ArchiveEntry): string {
  const name = entry.name || '';
  const dot = name.lastIndexOf('.');
  return fileIconTile({
    type: entry.is_dir ? 'dir' : 'file',
    extension: dot > 0 ? name.slice(dot + 1) : '',
    basename: name,
  });
}
</script>

<template>
  <div class="filex-viewer-archive">
    <div v-if="passwordNeeded" class="filex-viewer-fallback">
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span class="filex-viewer-fallback__icon" aria-hidden="true" v-html="actionIconSvg('lock')"></span>
      <p>{{ tt('archive.password_required', 'This archive requires a password.') }}</p>
    </div>
    <div v-else-if="error" class="filex-viewer-fallback">
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span class="filex-viewer-fallback__icon" aria-hidden="true" v-html="typeTile"></span>
      <p>{{ error }}</p>
    </div>
    <div v-else-if="loading" class="filex-viewer-fallback">
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span
        class="filex-viewer-fallback__icon filex-viewer-fallback__icon--spin"
        aria-hidden="true"
        v-html="actionIconSvg('progress')"
      ></span>
      <p>
        {{
          slow
            ? tt(
                'viewer.archive.reading',
                'Still reading… the whole archive is read before its contents can be listed, which takes a while for a large one.',
              )
            : tt('viewer.loading', 'Loading…')
        }}
      </p>
    </div>
    <div v-else-if="entries.length === 0" class="filex-viewer-fallback">
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span class="filex-viewer-fallback__icon" aria-hidden="true" v-html="typeTile"></span>
      <p>{{ tt('viewer.archive.empty', 'Archive is empty.') }}</p>
    </div>
    <div v-else class="filex-viewer-archive__pane">
      <nav class="filex-viewer-archive__crumbs" :aria-label="tt('viewer.archive.location', 'Archive location')">
        <button type="button" @click="currentDir = ''">{{ tt('viewer.archive.root', 'Archive') }}</button>
        <template v-for="crumb in breadcrumbs" :key="crumb.path">
          <span aria-hidden="true">›</span>
          <button type="button" @click="currentDir = crumb.path">{{ crumb.name }}</button>
        </template>
      </nav>
      <div class="filex-viewer-archive__summary">
        <!-- ⚠ The count is IN the message ("{n} files"). A `{{ fileCount }}`
             printed in front of it read "3 3 files". -->
        {{ tt('viewer.archive.entries', `${fileCount} files`, { n: fileCount }) }}
        · {{ fmtSize(totalSize) }}
      </div>
      <DataTable
        table-id="viewer.archive"
        :columns="columns"
        :rows="visibleEntries"
        :row-key="(e: ArchiveRow) => e.fullPath"
        :row-attrs="(e: ArchiveRow) => ({ 'data-dir': e.is_dir ? '1' : '0', onDblclick: () => openRow(e) })"
      >
        <template #cell-name="{ row }">
          <!-- ikon:emoji — the same tile the listing draws for the same kind
               of file, so an archive's contents and the folder it came from
               read as one product. -->
          <button type="button" class="filex-viewer-archive__entry" :disabled="!row.is_dir" @click="openRow(row)">
            <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons -->
            <span class="filex-viewer-archive__icon" aria-hidden="true" v-html="entryTile(row)"></span>
            <span class="fe-list__cell-text">{{ row.displayName }}</span>
          </button>
        </template>
      </DataTable>
    </div>
    <ArchivePasswordModal
      :open="passwordNeeded"
      :locale="props.locale"
      :archive-name="(props.filePath || '').split('/').pop() || ''"
      :busy="loading"
      :error="passwordError"
      @close="emit('close')"
      @submit="submitPassword"
    />
  </div>
</template>

<style>
/* ⚠ NOT `scoped`, deliberately. Vue's scoped styles compile to
   `.cls[data-v-HASH]`, and in the web-component build the hash baked into
   this CSS does not match the one Vue stamps onto the DOM — so every rule
   here silently stopped applying. Measured in the desktop app: the share
   dialog had `position: static`, no background and no radius, i.e. raw
   unstyled HTML, in EVERY embedded surface.
   Safe to drop: every selector below is prefixed (fx-/fe-/filex-), so
   there is nothing here that can leak into a host page. */
.filex-viewer-archive {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
  color: var(--fe-text, #1a1e27);
}
.filex-viewer-archive__pane {
  flex: 1;
  overflow: auto;
  padding: 16px 20px;
}
.filex-viewer-archive__summary {
  font-size: 12px;
  color: var(--fe-text-muted, #5a6475);
  margin-bottom: 12px;
  font-variant-numeric: tabular-nums;
}
/* The table itself is DataTable's (styles/base.css); only the viewer's own
   touches stay here — tabular figures and muted sizes, a folder row read as
   quieter than a file. */
.filex-viewer-archive__size {
  text-align: end;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-archive__icon {
  margin-inline-end: 6px;
}
.filex-viewer-archive .fe-list__row[data-dir="1"] {
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-fallback {
  text-align: center;
  padding: 32px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-fallback__icon {
  font-size: 48px;
  display: block;
  margin-bottom: 12px;
}
.filex-viewer-archive__crumbs { display: flex; align-items: center; gap: 6px; margin-bottom: 10px; color: var(--fe-text-muted, #5a6475); }
.filex-viewer-archive__crumbs button { border: 0; background: transparent; color: inherit; cursor: pointer; padding: 2px 4px; border-radius: 4px; }
.filex-viewer-archive__crumbs button:hover { background: var(--fe-bg-elev, #f7f8fa); color: var(--fe-text, #1a1e27); }
.filex-viewer-archive__entry { display: inline-flex; align-items: center; max-width: 100%; border: 0; background: transparent; color: inherit; font: inherit; padding: 0; text-align: start; }
.filex-viewer-archive__entry:not(:disabled) { cursor: pointer; }
.filex-viewer-archive__entry:disabled { opacity: 1; }
</style>
