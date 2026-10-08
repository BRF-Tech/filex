<script setup lang="ts">
/**
 * #191 - where the tap on a push notification lands.
 *
 * The service worker (web/public/notify-sw.js) has no page to call back into
 * and cannot tell where a notification goes - that is the bell's resolver,
 * `@brftech/filex-core` lib/notificationTarget, and a second copy of it in a
 * worker that is never compiled would be a second answer. So a tap opens
 * `<worker scope>notify/<id>`, and this view does what a click on the same row
 * in the bell does: marks it read and goes where it points (lib/notificationNav),
 * or to the front door when it points nowhere.
 *
 * ⚠ The worker's scope is the operator's prefix (`/admin/`), and a person who
 * is not an administrator belongs on the end-user one - the same door rule as
 * the router's guard (GitHub #14).
 */
import { onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { rowOpens } from '@brftech/filex-core';

import { NotificationsApi } from '@/api/notifications';
import type { NotificationItem } from '@/api/types';
import { openNotificationTarget } from '@/lib/notificationNav';
import { onUserBase, userDoor } from '@/router';
import { useAuthStore } from '@/stores/auth';
import { useNotificationsStore } from '@/stores/notifications';
import Spinner from '@/components/ui/Spinner.vue';

const route = useRoute();
const router = useRouter();
const auth = useAuthStore();
const notif = useNotificationsStore();

/** The row in the person's own list (the bell's feed first, then a page of it). */
async function findRow(id: number): Promise<NotificationItem | null> {
  const inFeed = notif.feed.find((n) => n.id === id);
  if (inFeed) return inFeed;
  try {
    const page = await NotificationsApi.list({ limit: 100 });
    return page.items.find((n) => n.id === id) ?? null;
  } catch {
    return null;
  }
}

onMounted(async () => {
  const raw = Array.isArray(route.params.id) ? route.params.id[0] : route.params.id;
  const id = Number(raw ?? '');
  if (!auth.isAdmin && !onUserBase()) {
    window.location.replace(`${userDoor()}notify/${Number.isInteger(id) && id > 0 ? id : ''}`);
    return;
  }
  const row = Number.isInteger(id) && id > 0 ? await findRow(id) : null;
  await router.replace({ name: 'home' });
  if (!row) return;
  void notif.markRead(row.id).catch(() => {});
  if (rowOpens(row)) await openNotificationTarget(router, row.target);
});
</script>

<template>
  <div class="flex min-h-screen items-center justify-center" data-testid="notify-open" aria-busy="true">
    <Spinner />
  </div>
</template>
