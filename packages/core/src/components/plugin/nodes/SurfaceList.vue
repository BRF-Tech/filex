<script setup lang="ts">
/**
 * SurfaceList — a plugin surface's `list` node: a table whose rows may carry
 * actions. Picking one posts `event: "action"` with the button's id and
 * `data.row_id` — the plugin answers with a new screen (or a job).
 *
 * ⚠⚠ THIS IS THE PRODUCT'S ONE TABLE (DataTable — the explorer's own), not a
 * table of the plugin surface's own. `/admin/apps/<plugin>/<view>` is a menu
 * in the admin panel (Signatures is the one the owner opens daily), and the
 * owner's rule since 2026-09-21 is that every table in the product is the
 * explorer's: "bir yere tablo gerekiyorsa bu tabloyu koymak zorundayız." So an
 * app's list resizes, sorts, hides and moves its columns like the file list
 * does, and remembers that on the account per app and node
 * (`app.<plugin>.<node id>`), with:
 *
 *   · the first column frozen left (it is what says which row this is),
 *   · ONE `Actions` control frozen right, opening the same ContextMenu the
 *     explorer's ⋮ opens, instead of a scatter of loose buttons,
 *   · the shared empty state rather than a paragraph above an absent table.
 *
 * ⚠ The wire contract is unchanged and every field below is OPTIONAL
 * (docs/APP-PLUGINS-API.md → `list`): a plugin written against the old node
 * draws the same rows, now sortable. Sorting is on by default because a list
 * node carries ALL its rows — nothing is paged away, so sorting what is on
 * screen is sorting the list. A plugin that needs a fixed order says
 * `sortable: false` on the column; one whose cells are formatted for people
 * (a localised date, "1.2 MB") hands the table the raw value to sort by in
 * `row.sort`.
 *
 * ⚠ A plugin's own action ids and labels are passed straight through, so a
 * plugin that offered three buttons still offers three verbs — nothing is
 * dropped in the move, and `danger` still paints the destructive one apart.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../../../types/ExplorerConfig';
import type { ListNodeProps, PluginText } from '../../../types/Plugins';
import type { ContextAction } from '../../ContextMenu.vue';
import DataTable, { type DataColumn } from '../../DataTable.vue';
import { useLocale } from '../../../composables/useLocale';
import { appTextOr, labelOf } from '../../../lib/pluginLabel';
import { formatSurfaceCell } from '../../../lib/surfaceCell';

type ListRow = ListNodeProps['rows'][number];

const props = defineProps<{
  columns: ListNodeProps['columns'];
  rows: ListNodeProps['rows'];
  empty?: PluginText | string;
  locale: LocaleCode;
  disabled?: boolean;
  /** Where the table's arrangement is remembered (`app.<plugin>.<node>`). */
  tableId?: string;
}>();

const emit = defineEmits<{
  (e: 'action', p: { action_id: string; row_id: string }): void;
}>();

const { t, formatDate } = useLocale(() => props.locale);

const rows = computed(() => props.rows ?? []);

/** The actions column's control exists when ANY row has a verb — one shape for
 *  the whole table, so the column does not go ragged from row to row. */
const hasActions = computed(() => rows.value.some((r) => (r.actions ?? []).length > 0));

/* ⚠ The app's empty line for THIS reader, else filex's own, else whatever
   language the app has — see `appTextOr`. */
const emptyText = computed(() => appTextOr(props.empty, props.locale, () => t('plugin.list.empty')));

function cell(row: ListRow, key: string): string {
  return labelOf(row.cells?.[key], props.locale);
}

/** The node's columns, as the table draws them. Width is clamped to what a
 *  person could have dragged it to; a plugin cannot make a column that no
 *  handle can reach. */
const columns = computed<DataColumn<ListRow>[]>(() =>
  (props.columns ?? []).map((c) => {
    const w =
      typeof c.width === 'number' && Number.isFinite(c.width) ? Math.round(c.width) : undefined;
    return {
      id: c.key,
      label: labelOf(c.label, props.locale),
      width: w === undefined ? undefined : Math.min(900, Math.max(60, w)),
      align: c.align,
      sortable: c.sortable !== false,
      // A date column is printed the explorer's way, and sorted by the value.
      format: (row: ListRow) => formatSurfaceCell(cell(row, c.key), c.format, props.locale, formatDate),
      sortValue: (row: ListRow) => {
        const raw = row.sort?.[c.key];
        return typeof raw === 'number' || typeof raw === 'string' ? raw : cell(row, c.key);
      },
    };
  }),
);

/** The plugin's buttons, as menu entries. `testid` keeps the address each
 *  action already had (`surface-list-action-<row>-<action>`) so a spec, and a
 *  plugin's own e2e, can still reach a named verb. */
function actionsOf(row: ListRow): ContextAction[] {
  return (row.actions ?? []).map((a) => ({
    key: a.id,
    label: labelOf(a.label, props.locale),
    danger: a.danger,
    disabled: props.disabled,
    testid: `surface-list-action-${row.id}-${a.id}`,
  }));
}
</script>

<template>
  <div class="fe-slist" data-testid="surface-list">
    <DataTable
      :table-id="tableId"
      :columns="columns"
      :rows="rows"
      row-key="id"
      :locale="locale"
      :empty="emptyText"
      :row-attrs="(r: ListRow) => ({ 'data-row': r.id })"
      :row-actions="hasActions ? actionsOf : undefined"
      :row-actions-test-id="(r: ListRow) => ((r.actions ?? []).length ? `surface-list-actions-${r.id}` : undefined)"
      @row-action="(key: string, r: ListRow) => emit('action', { action_id: key, row_id: r.id })"
    />
  </div>
</template>
