<script setup lang="ts">
import { sayFailure } from '../lib/errorWords';
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
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */
import { fileIconTile } from '../lib/fileIcons'; /* ikon:emoji */
import { fetchViewerText } from '../composables/useViewerFetch';
import DataTable, { type DataColumn } from '../components/DataTable.vue';

const props = defineProps<{
  url: string;
  ext: string;
  t?: (key: string, vars?: Record<string, string | number>) => string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
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
      headers: (await props.authHeaders?.()) ?? {},
      credentials: props.authCredentials,
    });
  } catch (err) {
    error.value = sayFailure(err, tt('viewer.failed_to_load', 'Failed to load file'), { t: props.t }).text;
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

/** A parsed row with the line it came from, so `#` keeps naming the row in
 *  the FILE after a sort re-orders the screen. */
interface CsvRow {
  n: number;
  cells: string[];
}

const filtered = computed<CsvRow[]>(() => {
  const q = filter.value.trim().toLowerCase();
  const all = dataRows.value.map((cells, i) => ({ n: i + 1, cells }));
  if (!q) return all;
  return all.filter((r) => r.cells.some((c) => (c || '').toLowerCase().includes(q)));
});

/**
 * THE SORT, done here and handed to the table as CONTROLLED.
 *
 * ⚠⚠ Here and not in the table, because the table only ever holds one page
 * (100 rows) of the parsed file, and a table that sorts the page it holds
 * would call a re-ordered 100 of 1000 "sorted" — so it rightly closes its
 * headers over a paged list. This component holds EVERY parsed row, so it can
 * sort all of them and then page, which is the honest order.
 */
const sort = ref<{ key: string; dir: 'asc' | 'desc' } | null>(null);

function cellOf(r: CsvRow, key: string): string {
  if (key === 'n') return String(r.n);
  return r.cells[Number(key.slice(1))] ?? '';
}

const sorted = computed<CsvRow[]>(() => {
  const s = sort.value;
  if (!s) return filtered.value;
  const dir = s.dir === 'asc' ? 1 : -1;
  return [...filtered.value].sort((a, b) => {
    if (s.key === 'n') return dir * (a.n - b.n);
    const x = cellOf(a, s.key);
    const y = cellOf(b, s.key);
    // Empty cells last in both directions; numbers as numbers.
    if (!x !== !y) return x ? -1 : 1;
    const nx = Number(x);
    const ny = Number(y);
    if (x && y && Number.isFinite(nx) && Number.isFinite(ny)) return dir * (nx - ny) || a.n - b.n;
    return dir * x.localeCompare(y, undefined, { numeric: true, sensitivity: 'base' }) || a.n - b.n;
  });
});

function onSort(next: { key: string; dir: 'asc' | 'desc' }) {
  sort.value = next;
}

const totalPages = computed(() =>
  Math.max(1, Math.ceil(sorted.value.length / PAGE_SIZE)),
);

const visibleRows = computed<CsvRow[]>(() => {
  const start = (page.value - 1) * PAGE_SIZE;
  return sorted.value.slice(start, start + PAGE_SIZE);
});

watch([filter, sort], () => {
  page.value = 1;
});

/** The file's columns, as THE table (DataTable — the explorer's own) draws
 *  them. `#` is the lead: it is what names a row of a spreadsheet. */
const columns = computed<DataColumn<CsvRow>[]>(() => [
  {
    id: 'n',
    label: '#',
    lead: true,
    width: 64,
    min: 48,
    align: 'right',
    sortable: true,
    class: 'filex-viewer-csv__rownum',
    format: (r) => r.n,
  },
  ...headers.value.map((h, i) => ({
    id: `c${i}`,
    label: h,
    width: 140,
    sortable: true,
    format: (r: CsvRow) => r.cells[i] ?? '',
  })),
]);

function tt(key: string, fallback: string, vars?: Record<string, string | number>): string {
  return props.t ? props.t(key, vars) : fallback;
}

/* === ikon:emoji — the fallback screen's mark ==========================
 * Every viewer opened its "cannot show this" / "still loading" screen with a
 * 48px colour emoji, one per format, each from whatever emoji font the OS
 * shipped. The format mark is `lib/fileIcons`'s tile — the SAME tile the row
 * the person just clicked is wearing, so the fallback is recognisably about
 * that file — and "loading" is the stroked ring, spun by CSS, because no
 * still picture can say "still going". */
const typeTile = computed(() => fileIconTile({ type: 'file', extension: props.ext }));
</script>

<template>
  <div class="filex-viewer-csv">
    <div class="filex-viewer-csv__pane">
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
      <!-- ⚠ No table id: a spreadsheet's columns are whatever THIS file has
           (`c0`, `c1`, …), so widths remembered against them would land on a
           different file's different columns next time. The arrangement
           lives for this preview. -->
      <DataTable
        v-else
        :table-id="undefined"
        :columns="columns"
        :rows="visibleRows"
        :row-key="(r: CsvRow) => r.n"
        :sort="sort"
        @sort="onSort"
      />
    </div>
    <div v-if="!loading && !error && totalPages > 1" class="filex-viewer-csv__pager">
      <button
        type="button"
        class="filex-viewer-btn"
        :disabled="page <= 1"
        @click="page--"
      >‹</button>
      <span class="filex-viewer-csv__pageno"><bdi dir="ltr">{{ page }} / {{ totalPages }}</bdi> ({{ tt('viewer.csv_rows', `${filtered.length} rows`, { n: filtered.length }) }})</span>
      <button
        type="button"
        class="filex-viewer-btn"
        :disabled="page >= totalPages"
        @click="page++"
      >›</button>
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
.filex-viewer-csv__pane .fe-list__cell {
  font-family: var(--fe-font-mono, monospace);
  font-size: 12px;
}
/* The table itself is DataTable's (styles/base.css) — the product's one table.
   ⚠ The row number used to pin itself (`position: sticky; left: 0`) because
   this was a table of its own; it is the table's LEAD now, and the table
   freezes its lead itself, only while there is room to (a pin written here
   would fight that gate). What stays is the look of a row number. */
.filex-viewer-csv__rownum {
  color: var(--fe-text-muted, #5a6475);
  font-variant-numeric: tabular-nums;
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
