<script setup lang="ts">
/**
 * DraftsView — the explorer's "Drafts" view (issue #71).
 *
 * A new document is a DRAFT until its first save: a file in the person's own
 * drafts area of the storage they chose, remembered with where it is meant to
 * go (lib/drafts). This lists every draft of theirs, across every storage —
 * its name, where it will be saved, the storage, when it last changed — with
 * the three things one does with a draft: open it, save it where it belongs,
 * or delete it (it goes to the trash, like any deleted file).
 *
 * ⚠⚠ THE table (DataTable — the explorer's own), never a table of its own:
 * the owner's rule "filex'te TEK TABLO". The columns resize, sort, hide and
 * move, and that is remembered on the account like every other table's
 * (`explorer.drafts`).
 *
 * ⚠ Presentational: it fetches nothing. The explorer loads the drafts, runs
 * the verbs (the save's "name (2).ext?" question included) and hands the rows
 * back — the same split HomeView has, for the same reason: one owner of the
 * list, so the badge and the view cannot disagree.
 *
 * ⚠ No `<style>` block — the package's CSS lives in `styles/base.css` (a
 * scoped block does not reach the web-component build).
 */
import { computed } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { fileIconTile } from '../lib/fileIcons';
import { nameMatches } from '../lib/fileFilters';
import type { DraftDto } from '../lib/drafts';
import DataTable, { type DataColumn } from './DataTable.vue';
import type { ContextAction } from './ContextMenu.vue';

const props = defineProps<{
  drafts: DraftDto[];
  /** True while the list is in flight. */
  loading?: boolean;
  /** The most drafts this server keeps for one person (0 = not said). */
  limit?: number;
  locale: LocaleCode;
  /** The shell's name filter — the same box Home narrows. */
  nameFilter?: string;
  /** A draft a verb is running on: its row's actions are greyed. */
  busyKey?: string | null;
}>();

const emit = defineEmits<{
  (e: 'open', draft: DraftDto): void;
  (e: 'save', draft: DraftDto): void;
  (e: 'delete', draft: DraftDto): void;
}>();

const { t, formatDate } = useLocale(() => props.locale);

const rows = computed(() => props.drafts.filter((d) => nameMatches(d.name, props.nameFilter ?? '')));

function extOf(name: string): string {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
}

function tileFor(d: DraftDto): string {
  return fileIconTile({ type: 'file', basename: d.name, extension: extOf(d.name) || d.type });
}

/** The folder it is meant for, inside its storage: `Reports / 2026`, or `/`. */
function folderOf(d: DraftDto): string {
  const at = d.target_dir.indexOf('://');
  const rel = (at >= 0 ? d.target_dir.slice(at + 3) : d.target_dir).replace(/^\/+|\/+$/g, '');
  return rel ? rel.split('/').filter(Boolean).join(' / ') : '/';
}

function whenOf(d: DraftDto): number {
  return Date.parse(d.modified_at || d.created_at) || 0;
}

const columns = computed<DataColumn<DraftDto>[]>(() => [
  {
    id: 'name',
    label: t('col.name'),
    lead: true,
    sortable: true,
    width: 280,
    min: 160,
    format: (d) => d.name,
  },
  {
    id: 'target',
    label: t('drafts.col.target'),
    sortable: true,
    width: 240,
    format: folderOf,
    title: (d) => d.target,
  },
  {
    id: 'storage',
    label: t('drafts.col.storage'),
    sortable: true,
    width: 140,
    format: (d) => d.storage,
  },
  {
    id: 'modified',
    label: t('col.modified'),
    sortable: true,
    sortDir: 'desc',
    width: 170,
    format: (d) => formatDate(whenOf(d)),
    sortValue: (d) => whenOf(d),
  },
]);

function rowActions(d: DraftDto): ContextAction[] {
  const busy = props.busyKey === d.key;
  return [
    { key: 'open', label: t('ctx.open'), icon: 'open', disabled: busy },
    { key: 'save', label: t('drafts.action.save'), icon: 'check', disabled: busy },
    { key: 'delete', label: t('ctx.delete'), icon: 'delete', danger: true, disabled: busy },
  ];
}

function onRowAction(key: string, d: DraftDto) {
  if (key === 'open') emit('open', d);
  else if (key === 'save') emit('save', d);
  else if (key === 'delete') emit('delete', d);
}

/** "3 of 50 drafts" — how close the person is to the limit, said plainly. */
const countLine = computed(() =>
  props.limit && props.limit > 0
    ? t('drafts.count_of', { n: props.drafts.length, limit: props.limit })
    : '',
);
</script>

<template>
  <section class="fe-drafts" data-testid="drafts-view">
    <header class="fe-drafts__head">
      <p class="fe-drafts__lead">{{ t('drafts.lead') }}</p>
      <p v-if="countLine && drafts.length" class="fe-drafts__count" data-testid="drafts-count-line">
        {{ countLine }}
      </p>
    </header>

    <div v-if="!loading && drafts.length === 0" class="fe-state" data-testid="empty-drafts">
      <svg
        class="fe-state__art"
        viewBox="0 0 120 100"
        width="110"
        height="92"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M66 18H38a6 6 0 0 0-6 6v52a6 6 0 0 0 6 6h44a6 6 0 0 0 6-6V40z" />
        <path d="M66 18v22h22" />
        <path d="M50 66l18-18 6 6-18 18H50z" />
      </svg>
      <p class="fe-state__title">{{ t('empty.drafts.title') }}</p>
      <p class="fe-state__hint">{{ t('empty.drafts.hint') }}</p>
    </div>

    <DataTable
      v-else
      table-id="explorer.drafts"
      :columns="columns"
      :rows="rows"
      row-key="key"
      :locale="locale"
      :loading="loading"
      :framed="false"
      :empty="t('drafts.no_match')"
      :aria-label="t('node.drafts')"
      :row-attrs="(d: DraftDto) => ({ 'data-testid': `draft-row-${d.key}`, 'data-draft-name': d.name })"
      :row-actions="rowActions"
      :row-actions-test-id="(d: DraftDto) => `draft-actions-${d.key}`"
      @row-action="(key: string, d: DraftDto) => onRowAction(key, d)"
      @row-dblclick="(d: DraftDto) => emit('open', d)"
    >
      <template #cell-name="{ row }">
        <span class="fe-drafts__name">
          <span class="fe-drafts__tile" aria-hidden="true" v-html="tileFor(row)"></span>
          <button
            type="button"
            class="fe-drafts__open"
            :title="t('drafts.open_hint', { name: row.name })"
            :data-testid="`draft-open-${row.key}`"
            @click.stop="emit('open', row)"
          >
            <bdi>{{ row.name }}</bdi>
          </button>
        </span>
      </template>
      <template #cell-target="{ row }">
        <bdi class="fe-drafts__target">{{ folderOf(row) }}</bdi>
      </template>
    </DataTable>
  </section>
</template>
