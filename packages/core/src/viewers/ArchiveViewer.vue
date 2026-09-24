<script setup lang="ts">
/**
 * ArchiveViewer — minimal zip / archive contents preview.
 *
 * Hits the configured archive-list endpoint (`archiveListUrl`, default
 * `POST /api/files/archive/list`) with the adapter-qualified path
 * and renders the member list as a flat table (name, size, mtime).
 * Read-only — extraction is exposed elsewhere (context menu / actions
 * panel). The viewer's job is just "what's inside?" so the user can
 * decide whether to extract or download.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */
import { fileIconTile } from '../lib/fileIcons'; /* ikon:emoji */
import DataTable, { type DataColumn } from '../components/DataTable.vue';
import { useLocale } from '../composables/useLocale';

interface ArchiveEntry {
  name: string;
  size: number;
  mtime?: string;
  is_dir?: boolean;
}

const props = defineProps<{
  url: string;
  filePath?: string;
  ext: string;
  t?: (key: string, vars?: Record<string, string | number>) => string;
  /** The interface language — the size column is written the way it writes
   *  numbers ("1,96 KB"), like every other size in the product. */
  locale?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
  /** ⚠ Where to ask. This used to be the literal '/api/files/archive/list',
   *  which is the ONE thing an embed cannot assume: with `apiBase` pointing at
   *  another origin the request went to the host page instead and 404'd, while
   *  every other call in the package honoured the configured base. The default
   *  keeps a same-origin install working unchanged. */
  archiveListUrl?: string;
}>();

const entries = ref<ArchiveEntry[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

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

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  entries.value = [];
  // The viewer is mounted with the resolved file path. Hit the API
  // endpoint with the same adapter-qualified path the preview URL
  // points at — the backend resolves storage + relative path itself.
  if (!props.filePath) {
    error.value = tt('viewer.archive.error', 'Could not read archive contents.');
    loading.value = false;
    return;
  }
  try {
    const res = await fetch(props.archiveListUrl || '/api/files/archive/list', {
      method: 'POST',
      credentials: props.authCredentials || 'same-origin',
      headers: {
        'Content-Type': 'application/json',
        ...(props.authHeaders ? await props.authHeaders() : {}),
      },
      body: JSON.stringify({ path: props.filePath }),
    });
    if (!res.ok) {
      /* ⚠ Never the raw status. This printed "503 Service Unavailable" (or a
       * JSON body) straight into the preview — the same failure the owner
       * reported for ONLYOFFICE ("Config fetch 503: {…}"): a person is shown
       * the HTTP layer instead of what happened. The server's reason, when it
       * sent one we know, becomes a sentence; anything else is the generic
       * "could not read the archive". */
      throw new ArchiveError(await archiveErrorText(res));
    }
    const body = (await res.json()) as { entries?: ArchiveEntry[] };
    entries.value = body.entries ?? [];
  } catch (err) {
    error.value =
      err instanceof ArchiveError ? err.message : tt('viewer.archive.error', 'Could not read archive contents.');
  } finally {
    loading.value = false;
  }
}

/** A failure already worded for a person. Anything else that throws (the
 *  network, a JSON parse) reads as the generic sentence. */
class ArchiveError extends Error {}

async function archiveErrorText(res: Response): Promise<string> {
  let code = '';
  try {
    const body = (await res.json()) as { error?: unknown; code?: unknown };
    code = String(body?.code ?? body?.error ?? '');
  } catch {
    /* not JSON — the generic sentence below */
  }
  if (/not a zip|unsupported|not supported|format/i.test(code)) {
    return tt('viewer.archive.unsupported', 'This kind of archive cannot be listed here. Download it to see what is inside.');
  }
  if (res.status === 413 || /too large|too big/i.test(code)) {
    return tt('viewer.archive.too_large', 'This archive is too large to list here. Download it to see what is inside.');
  }
  return tt('viewer.archive.error', 'Could not read archive contents.');
}

onMounted(load);
watch(() => props.filePath, load);

/**
 * The entries, in THE table (DataTable — the explorer's own). ⚠ It used to be
 * the one table in the package allowed to be its own, with the reasoning that
 * a file's contents answer to the viewer's layout. The owner's rule since
 * 2026-09-21 has no such exception — "bir yere tablo gerekiyorsa bu tabloyu
 * koymak zorundayız" — and an archive's listing is a file listing: it wants
 * to be sorted by size and have its Name column widened exactly the way the
 * folder it came from does.
 */
const columns = computed<DataColumn<ArchiveEntry>[]>(() => [
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

const totalSize = computed(() => entries.value.reduce((sum, e) => sum + (e.size || 0), 0));
const fileCount = computed(() => entries.value.filter((e) => !e.is_dir).length);

/* === ikon:emoji — the fallback screen's mark ==========================
 * Every viewer opened its "cannot show this" / "still loading" screen with a
 * 48px colour emoji, one per format, each from whatever emoji font the OS
 * shipped. The format mark is `lib/fileIcons`'s tile — the SAME tile the row
 * the person just clicked is wearing, so the fallback is recognisably about
 * that file — and "loading" is the stroked ring, spun by CSS, because no
 * still picture can say "still going". */
const typeTile = computed(() => fileIconTile({ type: 'file', extension: props.ext }));

/** A row inside the archive, drawn the way the listing draws the same kind. */
function entryTile(e: ArchiveEntry): string {
  const name = e.name || '';
  const dot = name.lastIndexOf('.');
  return fileIconTile({
    type: e.is_dir ? 'dir' : 'file',
    extension: dot > 0 ? name.slice(dot + 1) : '',
    basename: name,
  });
}
</script>

<template>
  <div class="filex-viewer-archive">
    <div v-if="error" class="filex-viewer-fallback">
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
      <p>{{ tt('viewer.loading', 'Loading…') }}</p>
    </div>
    <div v-else-if="entries.length === 0" class="filex-viewer-fallback">
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span class="filex-viewer-fallback__icon" aria-hidden="true" v-html="typeTile"></span>
      <p>{{ tt('viewer.archive.empty', 'Archive is empty.') }}</p>
    </div>
    <div v-else class="filex-viewer-archive__pane">
      <div class="filex-viewer-archive__summary">
        <!-- ⚠ The count is IN the message ("{n} files"). A `{{ fileCount }}`
             printed in front of it read "3 3 files". -->
        {{ tt('viewer.archive.entries', `${fileCount} files`, { n: fileCount }) }}
        · {{ fmtSize(totalSize) }}
      </div>
      <DataTable
        table-id="viewer.archive"
        :columns="columns"
        :rows="entries"
        :row-key="(e: ArchiveEntry) => e.name"
        :row-attrs="(e: ArchiveEntry) => ({ 'data-dir': e.is_dir ? '1' : '0' })"
      >
        <template #cell-name="{ row }">
          <!-- ikon:emoji — the same tile the listing draws for the same kind
               of file, so an archive's contents and the folder it came from
               read as one product. -->
          <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons -->
          <span class="filex-viewer-archive__icon" aria-hidden="true" v-html="entryTile(row)"></span>
          <span class="fe-list__cell-text">{{ row.name }}</span>
        </template>
      </DataTable>
    </div>
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
</style>
