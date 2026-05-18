<script setup lang="ts">
/**
 * CsvViewer — lightweight CSV / TSV table preview.
 *
 * Lazy-imports `papaparse` when available (best-quality parser — handles
 * embedded quotes, multi-line cells, etc). Falls back to a simple
 * `split` parser when the peer is missing — workable for the trivial
 * "first 1000 rows" preview case but loses fidelity on complex files.
 *
 * UX:
 *   - first-row-as-header toggle
 *   - filter input that case-insensitively matches any cell
 *   - 100-rows-per-page pagination
 *   - tab vs comma auto-detection (manual override via toolbar)
 */
import { computed, onMounted, ref, watch } from 'vue';
import { fetchViewerText } from '../composables/useViewerFetch';

const props = defineProps<{
  url: string;
  ext: string;
  t?: (key: string) => string;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
}>();

const PAGE_SIZE = 100;
const ROW_LIMIT = 1000;

const rows = ref<string[][]>([]);
const error = ref<string | null>(null);
const loading = ref(true);
const firstRowHeader = ref(true);
const filter = ref('');
const page = ref(1);
const detectedDelim = ref<',' | '\t' | ';'>(',');
const userDelim = ref<'auto' | ',' | '\t' | ';'>('auto');

let renderToken = 0;

function detectDelimiter(sample: string): ',' | '\t' | ';' {
  const sampleLines = sample.split(/\r?\n/).slice(0, 5).join('\n');
  const tab = (sampleLines.match(/\t/g) || []).length;
  const semi = (sampleLines.match(/;/g) || []).length;
  const comma = (sampleLines.match(/,/g) || []).length;
  if (tab > comma && tab > semi) return '\t';
  if (semi > comma && semi > tab) return ';';
  return ',';
}

async function parseWith(text: string, delim: string): Promise<string[][]> {
  try {
    const mod = await import(/* @vite-ignore */ 'papaparse');
    const Papa = (mod as any).default ?? mod;
    const result = Papa.parse(text, {
      delimiter: delim,
      skipEmptyLines: true,
      header: false,
    });
    return (result.data as string[][]).slice(0, ROW_LIMIT);
  } catch {
    // Fallback: naive line/column split. Loses quoted-comma support
    // but renders something reasonable.
    return text
      .split(/\r?\n/)
      .filter((l) => l.length > 0)
      .slice(0, ROW_LIMIT)
      .map((line) => line.split(delim));
  }
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  rows.value = [];
  page.value = 1;
  const myToken = ++renderToken;

  let text: string;
  try {
    text = await fetchViewerText({
      url: props.url,
      headers: props.authHeaders?.() ?? {},
      credentials: props.authCredentials,
    });
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'fetch failed';
    loading.value = false;
    return;
  }

  if (myToken !== renderToken) return;

  if (props.ext === 'tsv') {
    detectedDelim.value = '\t';
  } else {
    detectedDelim.value = detectDelimiter(text.slice(0, 4096));
  }

  const delim = userDelim.value === 'auto' ? detectedDelim.value : userDelim.value;
  rows.value = await parseWith(text, delim);
  loading.value = false;
}

onMounted(load);
watch(() => props.url, load);
watch(() => userDelim.value, () => {
  if (rows.value.length === 0) return;
  // Re-parse with the new delimiter using the cached source — but we
  // didn't keep it. Cheapest path is a refetch.
  load();
});

const headers = computed<string[]>(() => {
  if (!firstRowHeader.value || rows.value.length === 0) {
    if (rows.value.length === 0) return [];
    return rows.value[0].map((_, i) => `Col ${i + 1}`);
  }
  return rows.value[0];
});

const dataRows = computed<string[][]>(() => {
  return firstRowHeader.value ? rows.value.slice(1) : rows.value;
});

const filtered = computed<string[][]>(() => {
  const q = filter.value.trim().toLowerCase();
  if (!q) return dataRows.value;
  return dataRows.value.filter((r) =>
    r.some((c) => (c || '').toLowerCase().includes(q)),
  );
});

const totalPages = computed(() =>
  Math.max(1, Math.ceil(filtered.value.length / PAGE_SIZE)),
);

const visibleRows = computed<string[][]>(() => {
  const start = (page.value - 1) * PAGE_SIZE;
  return filtered.value.slice(start, start + PAGE_SIZE);
});

watch(filter, () => {
  page.value = 1;
});

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
}
</script>

<template>
  <div class="filex-viewer-csv">
    <div class="filex-viewer-csv__pane">
      <div v-if="error" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">📊</span>
        <p>{{ error }}</p>
      </div>
      <div v-else-if="loading" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">⏳</span>
        <p>{{ tt('viewer.loading', 'Loading…') }}</p>
      </div>
      <table v-else class="filex-viewer-csv__table">
        <thead>
          <tr>
            <th class="filex-viewer-csv__rownum">#</th>
            <th v-for="(h, i) in headers" :key="i">{{ h }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, ri) in visibleRows" :key="ri">
            <td class="filex-viewer-csv__rownum">{{ (page - 1) * 100 + ri + 1 }}</td>
            <td v-for="(c, ci) in row" :key="ci">{{ c }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!loading && !error && totalPages > 1" class="filex-viewer-csv__pager">
      <button
        type="button"
        class="filex-viewer-btn"
        :disabled="page <= 1"
        @click="page--"
      >‹</button>
      <span class="filex-viewer-csv__pageno">{{ page }} / {{ totalPages }} ({{ filtered.length }} satır)</span>
      <button
        type="button"
        class="filex-viewer-btn"
        :disabled="page >= totalPages"
        @click="page++"
      >›</button>
    </div>
  </div>
</template>

<style scoped>
.filex-viewer-csv {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
  color: var(--fe-text, #1a1e27);
}
.filex-viewer-csv__bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  font-size: 12px;
}
.filex-viewer-csv__check {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
}
.filex-viewer-csv__select,
.filex-viewer-csv__filter {
  border: 1px solid var(--fe-border, #e2e6ed);
  background: var(--fe-bg, #fff);
  color: inherit;
  padding: 4px 8px;
  border-radius: 4px;
  font: inherit;
  font-size: 12px;
}
.filex-viewer-csv__filter {
  flex: 0 1 240px;
}
.filex-viewer-spacer { flex: 1; }
.filex-viewer-csv__count {
  font-size: 11px;
  color: var(--fe-text-muted, #5a6475);
  font-variant-numeric: tabular-nums;
}
.filex-viewer-csv__pane {
  flex: 1;
  overflow: auto;
}
.filex-viewer-csv__table {
  border-collapse: collapse;
  width: max-content;
  min-width: 100%;
  font-size: 12px;
  font-family: var(--fe-font-mono, monospace);
}
.filex-viewer-csv__table th,
.filex-viewer-csv__table td {
  padding: 4px 10px;
  border: 1px solid var(--fe-border, #e2e6ed);
  white-space: nowrap;
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.filex-viewer-csv__table th {
  background: var(--fe-bg-elev, #f7f8fa);
  position: sticky;
  top: 0;
  font-weight: 600;
  text-align: left;
}
.filex-viewer-csv__table tbody tr:nth-child(even) {
  background: var(--fe-bg-hover, rgba(0, 0, 0, 0.02));
}
.filex-viewer-csv__rownum {
  color: var(--fe-text-muted, #5a6475);
  text-align: right;
  background: var(--fe-bg-elev, #f7f8fa);
  font-variant-numeric: tabular-nums;
  position: sticky;
  left: 0;
}
.filex-viewer-csv__pager {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 8px;
  padding: 6px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-top: 1px solid var(--fe-border, #e2e6ed);
  font-size: 12px;
}
.filex-viewer-csv__pageno {
  font-variant-numeric: tabular-nums;
  font-size: 12px;
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
.filex-viewer-btn {
  border: 1px solid var(--fe-border, #e2e6ed);
  background: var(--fe-bg, #fff);
  color: var(--fe-text, #1a1e27);
  padding: 4px 10px;
  border-radius: 4px;
  cursor: pointer;
  font: inherit;
  font-size: 12px;
}
.filex-viewer-btn:hover:not(:disabled) {
  background: var(--fe-bg-hover, #edf0f5);
}
.filex-viewer-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
</style>
