<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { RouterLink } from 'vue-router';
import { Bell, Check, CheckCheck } from 'lucide-vue-next';
import { useNotificationsStore } from '@/stores/notifications';
import { formatDate } from '@/lib/format';

const { t, locale } = useI18n();
const notif = useNotificationsStore();
const open = ref(false);
let pollHandle: number | null = null;

async function load() {
  await notif.fetchUserList({ unread: false });
}

async function poll() {
  await notif.fetchUnread();
  if (open.value) await load();
}

onMounted(() => {
  poll();
  pollHandle = window.setInterval(poll, 15_000);
});
onBeforeUnmount(() => {
  if (pollHandle != null) window.clearInterval(pollHandle);
});

function toggle() {
  open.value = !open.value;
  if (open.value) load();
}

const dotColor = computed(() => (notif.hasUnread ? 'bg-rose-500' : ''));

function severityClass(s: string): string {
  switch (s) {
    case 'critical':
    case 'error':
      return 'text-rose-600 dark:text-rose-400';
    case 'warning':
      return 'text-amber-600 dark:text-amber-400';
    case 'info':
    default:
      return 'text-sky-600 dark:text-sky-400';
  }
}

async function markRead(id: number) {
  await notif.markRead(id);
}

async function markAll() {
  await notif.markAllRead();
}

const list = computed(() => notif.items.slice(0, 15));
</script>

<template>
  <div class="relative">
    <button
      type="button"
      class="relative inline-flex h-9 w-9 items-center justify-center rounded-md text-zinc-500 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
      :aria-label="t('notifications.bell')"
      @click="toggle"
    >
      <Bell class="h-5 w-5" />
      <span
        v-if="notif.hasUnread"
        :class="['absolute right-1.5 top-1.5 h-2 w-2 rounded-full', dotColor]"
        aria-hidden="true"
      />
    </button>

    <div
      v-if="open"
      class="absolute right-0 z-50 mt-2 w-96 max-w-[90vw] rounded-md border border-zinc-200 bg-white shadow-lg dark:border-zinc-800 dark:bg-zinc-950"
    >
      <div class="flex items-center justify-between border-b border-zinc-200 px-3 py-2 dark:border-zinc-800">
        <span class="text-sm font-medium">{{ t('notifications.title') }}</span>
        <button
          v-if="notif.hasUnread"
          type="button"
          class="inline-flex items-center gap-1 text-xs text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100"
          @click="markAll"
        >
          <CheckCheck class="h-3.5 w-3.5" />
          {{ t('notifications.markAllRead') }}
        </button>
      </div>

      <ul class="max-h-96 overflow-y-auto py-1">
        <li v-if="!list.length" class="px-3 py-6 text-center text-sm text-zinc-500 dark:text-zinc-400">
          {{ t('notifications.empty') }}
        </li>
        <li
          v-for="n in list"
          :key="n.id"
          class="cursor-pointer px-3 py-2 hover:bg-zinc-50 dark:hover:bg-zinc-900"
          @click="markRead(n.id)"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span :class="['inline-block h-2 w-2 rounded-full', !n.read_at ? 'bg-brand-500' : 'bg-zinc-300 dark:bg-zinc-700']" />
                <span :class="['text-xs font-medium uppercase tracking-wide', severityClass(n.severity)]">{{ n.severity }}</span>
                <span class="ml-auto text-[11px] text-zinc-500 dark:text-zinc-400">{{ formatDate(n.created_at, locale) }}</span>
              </div>
              <div class="text-sm font-medium text-zinc-900 dark:text-zinc-100" :class="{ 'opacity-70': n.read_at }">
                {{ n.title }}
              </div>
              <div class="text-xs text-zinc-600 dark:text-zinc-400 line-clamp-2">{{ n.body }}</div>
            </div>
            <Check v-if="!n.read_at" class="h-4 w-4 shrink-0 text-zinc-400" />
          </div>
        </li>
      </ul>

      <div class="border-t border-zinc-200 px-3 py-2 text-center text-xs dark:border-zinc-800">
        <RouterLink to="/notifications" class="text-brand-600 hover:underline dark:text-brand-400" @click="open = false">
          {{ t('notifications.viewAll') }}
        </RouterLink>
      </div>
    </div>
  </div>
</template>
