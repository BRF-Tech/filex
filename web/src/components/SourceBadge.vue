<script setup lang="ts">
/**
 * Where something comes from, as one small badge with a title that says what
 * it means:
 *  - of="account": an account's users.auth_source (migration 00081) — local,
 *    sso, ldap, proxy;
 *  - of="member": a group membership's source — manual, sso, ldap;
 *  - of="group": what a group's members come through — local (by hand only),
 *    sso, ldap (the kinds of its links).
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';

import Badge from '@/components/ui/Badge.vue';

const props = withDefaults(
  defineProps<{ source?: string; of?: 'account' | 'member' | 'group'; size?: 'xs' | 'sm' | 'md'; directory?: string }>(),
  { source: '', of: 'account', size: 'xs', directory: '' },
);

const { t } = useI18n();

const TONES: Record<string, 'zinc' | 'sky' | 'violet' | 'amber'> = {
  local: 'zinc',
  manual: 'zinc',
  sso: 'sky',
  ldap: 'violet',
  proxy: 'amber',
};

/** What each use can say; anything else reads as the first. */
const KNOWN: Record<string, string[]> = {
  account: ['local', 'sso', 'ldap', 'proxy'],
  member: ['manual', 'sso', 'ldap'],
  group: ['local', 'sso', 'ldap'],
};

const key = computed(() => {
  const known = KNOWN[props.of];
  return known.includes(props.source) ? props.source : known[0];
});
</script>

<template>
  <Badge
    :tone="TONES[key] ?? 'zinc'"
    :size="size"
    :title="directory && directory !== 'ldap' ? `${t(`sources.${of}Hint.${key}`)} (${directory})` : t(`sources.${of}Hint.${key}`)"
    :data-testid="`source-${of}-${key}`"
  >
    {{ t(`sources.${key}`) }}
  </Badge>
</template>
