<script setup lang="ts">
/**
 * StorageTags — what kind of storage a row is, in the same words everywhere.
 *
 * ⚠⚠ ONE vocabulary (release-candidate sweep, 2026-09-21, QA #34): the admin
 * panel had TWO lists of the same storages, and they described them in two
 * dialects — Admin → Storages marked a read-only storage "RO" next to a raw
 * driver id ("local"), Connections → Storages marked it "SALT OKUNUR" next to
 * "LOCAL" (the same id, upper-cased by CSS), and the explorer's side bar says
 * "Salt okunur". This component is the one place those words are chosen: the
 * driver's NAME (the same `storages.driver.*` names the storage form offers),
 * then "Read-only" and "Disabled" when they apply. Both lists draw it.
 *
 * A driver this build has no name for (an external plugin's) is shown as its
 * id rather than hidden.
 *
 * ⚠ It is also THE read-only mark of the explorer: the side panel's storage
 * row and the Home card draw it with no `driver` (only the fact that matters
 * there). Wave 2 had grown two looks for one fact — the explorer's tinted pill
 * ("Salt okunur" on the panel and on Home) and this bordered one in the admin
 * lists — so the explorer now draws this component too.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';

const props = withDefaults(
  defineProps<{
    /** The driver id; absent draws no driver tag (the explorer's read-only mark). */
    driver?: string;
    readOnly?: boolean;
    /** `false` draws "Disabled"; `undefined` / `true` draws nothing. */
    enabled?: boolean;
    locale?: LocaleCode | string;
  }>(),
  // ⚠ An explicit `undefined` default. Vue casts an ABSENT boolean prop to
  // `false`, so without it every caller that does not pass `enabled` (the
  // explorer's marks, Admin → Replica) drew "Disabled" beside "Read-only".
  { enabled: undefined },
);

const { t } = useLocale(() => props.locale ?? 'en');

const driverName = computed(() => {
  if (!props.driver) return '';
  const key = `storages.driver.${props.driver}`;
  const out = t(key);
  return out === key ? props.driver : out;
});
</script>

<template>
  <span class="fe-stags" data-testid="storage-tags">
    <span v-if="driver" class="fe-stag" :title="driver" data-testid="storage-tag-driver">{{ driverName }}</span>
    <span v-if="readOnly" class="fe-stag fe-stag--warn" data-testid="storage-tag-readonly">{{
      t('sidenav.storage.readOnly')
    }}</span>
    <span v-if="enabled === false" class="fe-stag" data-testid="storage-tag-disabled">{{
      t('conn.list.disabled')
    }}</span>
  </span>
</template>
