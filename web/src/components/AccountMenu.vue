<script setup lang="ts">
/**
 * The explorer page's avatar — core's AccountMenu, told who is signed in and
 * which filex this is by THIS app's stores.
 *
 * ⚠ Not a second menu: the control is `@brftech/filex-core`'s since
 * 2026-09-27 (the desktop app's explorer draws the same one from
 * `config.account`). The rows still come from the page (Explore.vue), which
 * owns the routes, the settings dialog and the session.
 */
import { AccountMenu, type AccountAction } from '@brftech/filex-core';

import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';

defineProps<{
  /** The rows, in order. */
  actions: AccountAction[];
  /** The active language — any offered one, a language pack's included. */
  locale: string;
  /** Accessible name when this account has neither a name nor an address. */
  fallbackLabel: string;
}>();

const emit = defineEmits<{ (e: 'select', key: string): void }>();

const auth = useAuthStore();
const caps = useCapabilitiesStore();
</script>

<template>
  <AccountMenu
    :actions="actions"
    :user="auth.user"
    :version="caps.data.release"
    :locale="locale"
    :fallback-label="fallbackLabel"
    @select="emit('select', $event)"
  />
</template>
