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

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
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
      throw new Error(`${res.status} ${res.statusText}`);
    }
    const body = (await res.json()) as { entries?: ArchiveEntry[] };
    entries.value = body.entries ?? [];
  } catch (err) {
    error.value = err instanceof Error ? err.message : tt('viewer.archive.error', 'Could not read archive contents.');
  } finally {
    loading.value = false;
  }
}

onMounted(load);
watch(() => props.filePath, load);

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
      <table class="filex-viewer-archive__table">
        <thead>
          <tr>
            <th>{{ tt('viewer.name', 'Name') }}</th>
            <th class="filex-viewer-archive__size">{{ tt('viewer.size', 'Size') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(e, i) in entries" :key="i" :data-dir="e.is_dir ? '1' : '0'">
            <td>
              <!-- ikon:emoji — the same tile the listing draws for the same
                   kind of file, so an archive's contents and the folder it
                   came from read as one product. -->
              <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons -->
              <span class="filex-viewer-archive__icon" aria-hidden="true" v-html="entryTile(e)"></span>
              {{ e.name }}
            </td>
            <td class="filex-viewer-archive__size">{{ e.is_dir ? '' : fmtSize(e.size) }}</td>
          </tr>
        </tbody>
      </table>
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
.filex-viewer-archive__table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  font-family: var(--fe-font-mono, monospace);
}
.filex-viewer-archive__table th,
.filex-viewer-archive__table td {
  padding: 6px 12px;
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  text-align: left;
}
.filex-viewer-archive__table th {
  background: var(--fe-bg-elev, #f7f8fa);
  font-weight: 600;
  position: sticky;
  top: 0;
}
.filex-viewer-archive__size {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-archive__icon {
  margin-right: 6px;
}
.filex-viewer-archive__table tr[data-dir="1"] {
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
