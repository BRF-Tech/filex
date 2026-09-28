<script setup lang="ts">
/**
 * The admin app's bell — core's NotificationBell, bound to THIS app.
 *
 * ⚠⚠ Not a second bell. The bell, its rows, the count badge and the full list
 * are `@brftech/filex-core` components since 2026-09-27 (they moved there so
 * the desktop app, which mounts `<filex-explorer>`, could draw the very same
 * ones). What stays here is only what the package cannot know about this app:
 *
 *   · the FEED is this app's pinia store (stores/notifications.ts), filled by
 *     the one 15 s loop in App.vue (composables/useNotificationWatcher);
 *   · the LANGUAGE is vue-i18n's;
 *   · a click GOES where the router takes it (lib/notificationNav — the shared
 *     resolver, then a route push), and the admin door is the admin page.
 *
 * Drawn in the admin panel's top nav and in the explorer's header cluster
 * (Explore.vue, to the left of the avatar).
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { NotificationBell, type NotificationRowData } from '@brftech/filex-core';

import { useAuthStore } from '@/stores/auth';
import { useNotificationsStore } from '@/stores/notifications';
import { openNotificationTarget } from '@/lib/notificationNav';

const notif = useNotificationsStore();
const auth = useAuthStore();
const router = useRouter();
const { locale } = useI18n();

/**
 * The admin console for notifications — a SECOND, smaller door, for managing
 * the subsystem (the webhook, everybody's rows), and only for an administrator.
 * Nobody is sent there by a "see all" (that opens the full list over the
 * explorer, for everybody).
 */
const manageHref = computed(() => (auth.isAdmin ? router.resolve('/notifications').href : undefined));

function open(n: NotificationRowData) {
  void openNotificationTarget(router, n.target);
}

function manage() {
  void router.push('/notifications');
}
</script>

<template>
  <NotificationBell
    :feed="notif"
    :locale="locale"
    :manage-href="manageHref"
    @open="open"
    @manage="manage"
  />
</template>
