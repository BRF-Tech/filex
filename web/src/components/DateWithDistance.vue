<script setup lang="ts">
/**
 * A date in a table cell, with how far away it is on the line under it:
 * "Sep 22, 2026, 10:30 AM" over "in 7 days".
 *
 * ⚠ Two lines, not "date · distance" on one. The Shares table has seven
 * columns, and at 1440px the explorer's table (DataTable) gives Expires about
 * 175px: the one-line form was cut mid-word at the cell's edge with no
 * ellipsis - "· in 7 day" (signing/admin-table-actions-1440.png, 0.52.0 and
 * 0.53.0). The two spans are the cell's OWN children (this component renders
 * no wrapper), so core's `.fe-list__cell:has(> .tbl-sub)` stacks them, and
 * the date clamps with an ellipsis and its whole value in the title rather
 * than being cut.
 *
 * ⚠ The distance is left out when it would only repeat the date:
 * formatRelative answers with the date itself past a week, and a link that
 * expires in a month read "Nov 7, 2026, 10:30 AM · Nov 7, 2026, 10:30 AM".
 *
 * One component for every table that prints an expiry this way (Shares, My
 * shares), so the two pages cannot drift apart again.
 */
import { computed } from 'vue';
import { formatDate, formatRelative } from '@/lib/format';

const props = defineProps<{
  /** The instant, as the API sends it. */
  at: string;
  locale: string;
}>();

const date = computed(() => formatDate(props.at, props.locale));
const distance = computed(() => {
  const d = formatRelative(props.at, props.locale);
  return d === date.value || d === '-' ? '' : d;
});
</script>

<template>
  <span class="tbl-clamp text-xs" :title="date" data-testid="date-with-distance">{{ date }}</span>
  <span v-if="distance" class="tbl-sub" data-testid="date-distance">{{ distance }}</span>
</template>
