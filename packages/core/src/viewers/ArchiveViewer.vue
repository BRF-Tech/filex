<script setup lang="ts">
/**
 * ArchiveViewer — minimal zip / archive contents preview.
 *
 * Hits `POST /api/files/archive/list` with the adapter-qualified path
 * and renders the member list as a flat table (name, size, mtime).
 * Read-only — extraction is exposed elsewhere (context menu / actions
 * panel). The viewer's job is just "what's inside?" so the user can
 * decide whether to extract or download.
 */
import { computed, onMounted, ref, watch } from 'vue';

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
  t?: (key: string) => string;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
}>();

const entries = ref<ArchiveEntry[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
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
    const res = await fetch('/api/files/archive/list', {
      method: 'POST',
      credentials: props.authCredentials || 'include',
      headers: {
        'Content-Type': 'application/json',
        ...(props.authHeaders ? props.authHeaders() : {}),
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
</script>

<template>
  <div class="filex-viewer-archive">
    <div v-if="error" class="filex-viewer-fallback">
      <span class="filex-viewer-fallback__icon">🗜️</span>
      <p>{{ error }}</p>
    </div>
    <div v-else-if="loading" class="filex-viewer-fallback">
      <span class="filex-viewer-fallback__icon">⏳</span>
      <p>{{ tt('viewer.loading', 'Loading…') }}</p>
    </div>
    <div v-else-if="entries.length === 0" class="filex-viewer-fallback">
      <span class="filex-viewer-fallback__icon">🗜️</span>
      <p>{{ tt('viewer.archive.empty', 'Archive is empty.') }}</p>
    </div>
    <div v-else class="filex-viewer-archive__pane">
      <div class="filex-viewer-archive__summary">
        {{ fileCount }} {{ tt('viewer.archive.entries', '{n} files').replace('{n}', String(fileCount)) }}
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
              <span class="filex-viewer-archive__icon">{{ e.is_dir ? '📁' : '📄' }}</span>
              {{ e.name }}
            </td>
            <td class="filex-viewer-archive__size">{{ e.is_dir ? '' : fmtSize(e.size) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
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
