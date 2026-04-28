<script setup lang="ts" generic="T extends Record<string, unknown>">
import { computed, ref, watch } from 'vue';
import { ArrowUp, ArrowDown, ArrowUpDown } from 'lucide-vue-next';
import Spinner from './Spinner.vue';

export interface Column<Row> {
  key: string;
  label: string;
  sortable?: boolean;
  align?: 'left' | 'right' | 'center';
  width?: string;
  format?: (row: Row) => string | number | null;
  cell?: 'slot'; // forces use of named slot `cell-<key>`
}

interface Props {
  columns: Column<T>[];
  rows: T[];
  loading?: boolean;
  empty?: string;
  rowKey?: keyof T | ((row: T) => string | number);
  hover?: boolean;
  // Pagination (optional)
  page?: number;
  pageSize?: number;
  total?: number;
  // Sort (controlled, optional)
  sortKey?: string | null;
  sortDir?: 'asc' | 'desc' | null;
}

const props = withDefaults(defineProps<Props>(), {
  hover: true,
  empty: 'No results',
});

const emit = defineEmits<{
  (e: 'page', page: number): void;
  (e: 'sort', payload: { key: string; dir: 'asc' | 'desc' }): void;
  (e: 'row-click', row: T): void;
}>();

const localSort = ref<{ key: string | null; dir: 'asc' | 'desc' }>({
  key: props.sortKey ?? null,
  dir: props.sortDir ?? 'asc',
});

watch(
  () => [props.sortKey, props.sortDir],
  ([k, d]) => {
    localSort.value = { key: (k as string) ?? null, dir: (d as 'asc' | 'desc') ?? 'asc' };
  },
);

function clickHeader(col: Column<T>) {
  if (!col.sortable) return;
  const dir =
    localSort.value.key === col.key && localSort.value.dir === 'asc' ? 'desc' : 'asc';
  localSort.value = { key: col.key, dir };
  emit('sort', { key: col.key, dir });
}

function rowKeyFor(row: T, idx: number): string | number {
  if (typeof props.rowKey === 'function') return props.rowKey(row);
  if (props.rowKey) return row[props.rowKey] as string | number;
  return idx;
}

const totalPages = computed(() => {
  if (!props.total || !props.pageSize) return 1;
  return Math.max(1, Math.ceil(props.total / props.pageSize));
});

const currentPage = computed(() => props.page ?? 1);
const showPager = computed(
  () => props.total != null && props.pageSize != null && totalPages.value > 1,
);

function go(p: number) {
  if (p < 1 || p > totalPages.value) return;
  emit('page', p);
}

function alignClass(c: Column<T>['align']): string {
  if (c === 'right') return 'text-right';
  if (c === 'center') return 'text-center';
  return 'text-left';
}
</script>

<template>
  <div class="card overflow-hidden">
    <div v-if="$slots.toolbar" class="card-header flex items-center gap-2 flex-wrap">
      <slot name="toolbar" />
    </div>

    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead class="bg-zinc-50 dark:bg-zinc-900/50 text-zinc-600 dark:text-zinc-400">
          <tr>
            <th
              v-for="col in columns"
              :key="col.key"
              :style="col.width ? { width: col.width } : undefined"
              :class="[
                'px-4 py-2 font-medium select-none',
                alignClass(col.align),
                col.sortable && 'cursor-pointer hover:text-zinc-900 dark:hover:text-zinc-100',
              ]"
              :scope="'col'"
              @click="clickHeader(col)"
            >
              <span class="inline-flex items-center gap-1">
                {{ col.label }}
                <template v-if="col.sortable">
                  <ArrowUp
                    v-if="localSort.key === col.key && localSort.dir === 'asc'"
                    class="h-3 w-3"
                  />
                  <ArrowDown
                    v-else-if="localSort.key === col.key && localSort.dir === 'desc'"
                    class="h-3 w-3"
                  />
                  <ArrowUpDown v-else class="h-3 w-3 opacity-40" />
                </template>
              </span>
            </th>
          </tr>
          <tr v-if="$slots.filters">
            <th
              v-for="col in columns"
              :key="`f-${col.key}`"
              class="px-4 pb-2 font-normal align-top"
            >
              <slot :name="`filter-${col.key}`" :col="col" />
            </th>
          </tr>
        </thead>

        <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
          <tr v-if="loading">
            <td :colspan="columns.length" class="px-4 py-10 text-center text-zinc-500">
              <Spinner size="md" class="text-brand-600" />
            </td>
          </tr>
          <tr v-else-if="rows.length === 0">
            <td :colspan="columns.length" class="px-4 py-10 text-center text-zinc-500">
              <slot name="empty">{{ empty }}</slot>
            </td>
          </tr>
          <tr
            v-for="(row, idx) in rows"
            v-else
            :key="rowKeyFor(row, idx)"
            :class="[hover && 'hover:bg-zinc-50 dark:hover:bg-zinc-900/50 transition-colors']"
            @click="emit('row-click', row)"
          >
            <td
              v-for="col in columns"
              :key="`${rowKeyFor(row, idx)}-${col.key}`"
              :class="['px-4 py-2', alignClass(col.align)]"
            >
              <slot
                v-if="col.cell === 'slot'"
                :name="`cell-${col.key}`"
                :row="row"
                :value="(row as any)[col.key]"
              />
              <template v-else>
                {{ col.format ? col.format(row) : (row as any)[col.key] ?? '—' }}
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div
      v-if="showPager"
      class="flex items-center justify-between gap-2 border-t border-zinc-200 dark:border-zinc-800 px-4 py-2 text-sm text-zinc-600 dark:text-zinc-400"
    >
      <span>
        {{ (currentPage - 1) * (pageSize ?? 0) + 1 }}
        -
        {{ Math.min(currentPage * (pageSize ?? 0), total ?? 0) }}
        / {{ total }}
      </span>
      <div class="flex items-center gap-1">
        <button
          type="button"
          class="rounded px-2 py-1 hover:bg-zinc-100 dark:hover:bg-zinc-800 disabled:opacity-40 disabled:cursor-not-allowed"
          :disabled="currentPage <= 1"
          @click="go(currentPage - 1)"
        >
          ‹
        </button>
        <span class="px-2">{{ currentPage }} / {{ totalPages }}</span>
        <button
          type="button"
          class="rounded px-2 py-1 hover:bg-zinc-100 dark:hover:bg-zinc-800 disabled:opacity-40 disabled:cursor-not-allowed"
          :disabled="currentPage >= totalPages"
          @click="go(currentPage + 1)"
        >
          ›
        </button>
      </div>
    </div>
  </div>
</template>
