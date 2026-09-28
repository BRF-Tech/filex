<script setup lang="ts">
/**
 * The admin app's full notification list — core's NotificationsPanel, bound to
 * THIS app's store, language and router.
 *
 * ⚠ Not a second screen: the list itself is `@brftech/filex-core`'s since
 * 2026-09-27 (the desktop app's "See all" opens the same one). Rendered ONCE,
 * at the root (App.vue), and opened through the store — the bell is drawn in
 * two headers here, and a panel per bell would be two panels.
 */
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { NotificationsPanel, type NotificationRowData } from '@brftech/filex-core';

import { useNotificationsStore } from '@/stores/notifications';
import { openNotificationTarget } from '@/lib/notificationNav';

const notif = useNotificationsStore();
const router = useRouter();
const { locale } = useI18n();

function open(n: NotificationRowData) {
  void openNotificationTarget(router, n.target);
}
</script>

<template>
  <NotificationsPanel
    :feed="notif"
    :locale="locale"
    @open="open"
  />
</template>
