<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Bell, BellRing, RefreshCcw, Send, Webhook } from 'lucide-vue-next';
import { RouterLink } from 'vue-router';

import { useNotificationsStore } from '@/stores/notifications';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import { useNotificationText } from '@/composables/useNotificationText';
import { eventSlug } from '@/lib/webhookEvents';
import type { Severity } from '@/api/types';

import Button from '@/components/ui/Button.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import { DataTable, foreignText, type DataColumn } from '@brftech/filex-core';

const { t, locale } = useI18n();
const notif = useNotificationsStore();
const toast = useToastStore();

const refreshing = ref(false);

async function load() {
  refreshing.value = true;
  try {
    await Promise.all([
      notif.fetchAdminList(),
      notif.fetchAdminUnread(),
    ]);
  } finally {
    refreshing.value = false;
  }
}

onMounted(load);

// ── your own notification preferences ────────────────────────────────────
//
// ⚠⚠ ONE place for them: the user settings dialog (Notifications pane), which
// every account has. This page carried a second copy of the in-app and
// browser switches (release-candidate sweep, 2026-09-21: "the page repeats
// the user's own settings") — two switches for one preference, drawn in two
// styles. It now says where they are and opens that pane.
const UserSettingsModal = defineAsyncComponent(() => import('@/components/UserSettingsModal.vue'));
const showOwnSettings = ref(false);

/* gorunum:v2 — the same localised severity the bell prints; see NotificationBell. */
function severityLabel(sev: string): string {
  const key = `notifications.severity.${sev}`;
  const out = t(key);
  return out === key ? sev : out;
}

function severityTone(s: Severity): 'sky' | 'amber' | 'rose' {
  if (s === 'critical' || s === 'error') return 'rose';
  if (s === 'warning') return 'amber';
  return 'sky';
}

async function sendTest() {
  try {
    const r = await notif.sendTest();
    toast.success(t('notifications.testSent', { id: r.id }));
    await refreshAfterTest();
  } catch (e: unknown) {
    toast.error(extractError(e));
  }
}

/**
 * The kind of event, in the reader's language. ⚠ The table printed the raw id
 * (`share.created`, `update_available`) in mono next to a Turkish sentence
 * (release-candidate sweep, 2026-09-21). The labels are `notifications.kinds`,
 * held against the Go event list by web/tests/webhooks/eventCatalog.test.ts;
 * an id this build does not know stays visible as itself rather than vanish.
 */
function eventLabel(event: string): string {
  const key = `notifications.kinds.${eventSlug(event)}`;
  const out = t(key);
  return out === key ? event : out;
}

/** Whose row it is: the person's name (the server resolves it), not `user #2`;
 *  for a broadcast, who it reaches — the bells' own rule, said by the server
 *  (`audience`). ⚠ After PR #42 "Everyone" beside a drop notice, an antivirus
 *  hit or a legacy upload row would be a lie: the first reaches administrators
 *  only, the second only those who can see the file, the third no bell. */
const AUDIENCE_KEYS: Record<string, string> = {
  everyone: 'notifications.scopeEveryone',
  viewers: 'notifications.scopeViewers',
  admins: 'notifications.scopeAdmins',
  nobody: 'notifications.scopeNobody',
};
function scopeLabel(row: {
  user_id?: number | null;
  user_name?: string;
  admins_only?: boolean;
  audience?: string;
}): string {
  if (!row.user_id) {
    const key = (row.audience && AUDIENCE_KEYS[row.audience]) || (row.admins_only ? AUDIENCE_KEYS.admins : AUDIENCE_KEYS.everyone);
    return t(key);
  }
  return row.user_name || `#${row.user_id}`;
}

/**
 * The delivery state, localised. ⚠ `skipped` means "no webhook is set up",
 * which the server records in English (`no webhook URL configured`) for the
 * API's sake; this page says it in the reader's language instead of printing
 * that string, and a real failure keeps the receiver's own error text.
 */
function webhookLabel(status: string): string {
  const key = `notifications.webhookStatus.${status}`;
  const out = t(key);
  return out === key ? status : out;
}

function webhookReason(row: { webhook_status: string; webhook_error?: string }): string {
  if (row.webhook_status === 'skipped') return t('notifications.webhookNone');
  // ⚠ The RECEIVER's words, not ours — `Post "https://hooks…/x": dial tcp
  // 10.0.0.1:443: connection refused` — so they never passed through vue-i18n
  // and the panel's post-translation hook never isolated their machine runs.
  // In an Arabic panel a URL's and an address's neutrals take the line's
  // direction. This was the one text cell on the page not already going
  // through `foreignText` (the title and body are, in useNotificationText).
  return row.webhook_error ? foreignText(String(locale.value), row.webhook_error) : '';
}

function setUnread(v: boolean) {
  notif.setUnreadFilter(v);
  notif.fetchAdminList();
}

async function refreshAfterTest() {
  await Promise.all([notif.fetchAdminList(), notif.fetchAdminUnread()]);
}

// ⚠ The SAME renderer the bell, the browser toast and the desktop app use.
// This table has its own `event` column naming the kind (eventLabel, with the
// raw id kept in its tooltip), so nothing is lost by putting a sentence in the
// title column — and everything
// is lost by not: eight of the eleven file events store no title at all, so
// what stood here was `share.created` twice on the same row, once as data and
// once pretending to be a title. See lib/notificationText.ts.
const { notificationText } = useNotificationText();
const tableRows = computed(() =>
  notif.items.map((n) => ({ ...n, text: notificationText(n) })),
);

function gotoPage(p: number) {
  notif.setPage(p);
  notif.fetchAdminList();
}

function pageCount(): number {
  if (notif.limit === 0) return 1;
  return Math.max(1, Math.ceil(notif.total / notif.limit));
}

function currentPage(): number {
  if (notif.limit === 0) return 1;
  return Math.floor(notif.offset / notif.limit) + 1;
}

type NotificationRow = (typeof tableRows.value)[number];

/* Severity sorts by how loud it is, not by the word — "critical" and "error"
 * would otherwise land at the top only by the accident of the alphabet. */
const SEVERITY_RANK: Record<string, number> = { critical: 0, error: 1, warning: 2, info: 3 };

/* The explorer's table (DataTable), remembered on the account under
 * `admin.notifications`. ⚠ The list is paged by the SERVER, which has no sort
 * parameter, so while it spans more than one page DataTable closes the
 * headers and says why instead of re-ordering one page and calling that
 * sorted. */
const columns = computed<DataColumn<NotificationRow>[]>(() => [
  /* Sorted by the words the cell shows (eventLabel / scopeLabel), not by the
   * raw event id or user number behind them — an order the reader cannot see
   * reads as no order at all. */
  { id: 'event', label: t('notifications.fields.event'), sortable: true, width: 200, sortValue: (r) => eventLabel(r.event) },
  {
    id: 'severity',
    label: t('notifications.fields.severity'),
    sortable: true,
    width: 110,
    sortValue: (r) => SEVERITY_RANK[r.severity] ?? 9,
  },
  {
    id: 'title',
    label: t('notifications.fields.title'),
    sortable: true,
    width: 200,
    sortValue: (r) => r.text.title,
  },
  { id: 'body', label: t('notifications.fields.body'), width: 260 },
  {
    id: 'scope',
    label: t('notifications.fields.scope'),
    sortable: true,
    width: 110,
    sortValue: (r) => scopeLabel(r),
  },
  { id: 'webhook', label: t('notifications.fields.webhook'), sortable: true, width: 140, sortValue: (r) => r.webhook_status },
  {
    id: 'created_at',
    label: t('notifications.fields.createdAt'),
    sortable: true,
    sortDir: 'desc',
    width: 160,
    sortValue: (r) => (r.created_at ? Date.parse(r.created_at) : null),
  },
]);
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <Bell class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <h1 class="text-xl font-semibold">{{ t('notifications.adminTitle') }}</h1>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" @click="load" :loading="refreshing">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button variant="outline" size="sm" @click="sendTest">
          <Send class="h-4 w-4" />
          {{ t('notifications.sendTest') }}
        </Button>
      </div>
    </header>

    <!-- Your own preferences live in ONE place — the user settings dialog. -->
    <div
      class="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-zinc-200 bg-white p-4 text-sm shadow-sm dark:border-zinc-800 dark:bg-zinc-900"
      data-testid="notif-own-prefs-pointer"
    >
      <div class="flex items-center gap-2">
        <BellRing class="h-5 w-5 text-zinc-500" />
        <span>{{ t('notifications.prefs.movedToSettings') }}</span>
      </div>
      <Button variant="outline" size="sm" data-testid="notif-open-own-prefs" @click="showOwnSettings = true">
        {{ t('notifications.prefs.openSettings') }}
      </Button>
    </div>

    <!-- ⚠ Where events are DELIVERED is set up on the Webhooks page only — the
         default webhook and the signed targets side by side. This page carried
         a second webhook form of its own (release-candidate sweep, 2026-09-21:
         two webhook settings, no way to tell which one was in force). -->
    <div
      class="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-zinc-200 bg-white p-4 text-sm shadow-sm dark:border-zinc-800 dark:bg-zinc-900"
      data-testid="notif-webhooks-pointer"
    >
      <div class="flex items-center gap-2">
        <Webhook class="h-5 w-5 text-zinc-500" />
        <span>{{ t('notifications.webhooksMoved') }}</span>
      </div>
      <RouterLink :to="{ name: 'webhooks' }" class="text-sm font-medium text-brand-600 hover:underline dark:text-brand-400">
        {{ t('notifications.webhooksLink') }}
      </RouterLink>
    </div>

    <DataTable
      table-id="admin.notifications"
      :columns="columns"
      :rows="tableRows"
      :loading="notif.loading"
      :empty="t('notifications.empty')"
      row-key="id"
      :row-class="(row: NotificationRow) => (row.read_at ? 'is-muted' : undefined)"
      :page="currentPage()"
      :page-size="notif.limit"
      :total="notif.total"
      @page="gotoPage"
    >
      <template #toolbar>
        <Toggle
          :model-value="notif.onlyUnread"
          :label="t('notifications.unreadOnly')"
          @update:model-value="setUnread"
        />
        <!-- ⚠ The unread count of THIS list (everybody's rows), not of the
             admin's own bell: "1 okunmamış" stood next to three unread rows. -->
        <span data-testid="notif-admin-unread">{{ t('notifications.unreadHere', { n: notif.adminUnread }) }}</span>
      </template>

      <template #cell-event="{ row }">
        <span :title="row.event">{{ eventLabel(row.event) }}</span>
      </template>
      <template #cell-severity="{ row }">
        <Badge :tone="severityTone(row.severity)">{{ severityLabel(row.severity) }}</Badge>
      </template>
      <template #cell-title="{ row }">{{ row.text.title }}</template>
      <template #cell-body="{ row }">
        <span class="tbl-clamp" :title="row.text.body">{{ row.text.body }}</span>
      </template>
      <template #cell-scope="{ row }">
        <!-- `<bdi>`: it can be a PERSON's name, and a name's own letters
             decide its direction, not the panel's. -->
        <span data-testid="notif-scope"><bdi>{{ scopeLabel(row) }}</bdi></span>
      </template>
      <template #cell-webhook="{ row }">
        <!-- ⚠⚠ ONE root. A DataTable cell is a flex ROW whose children may
             shrink below their content (`:where(.fe-list__cell) > *
             { min-width: 0 }`), so the badge and the reason used to be two
             flex items side by side: when the reason wrapped, the badge was
             squeezed narrower than its own label and the label spilled under
             the reason — drawn over itself in Spanish and Arabic, where the
             reason is long, and in English whenever the column was narrow
             (v0.43.0 pack agent). Inside one block the badge is an inline
             box and the reason flows after it and under it, like any text. -->
        <div data-testid="notif-webhook">
          <Badge
            :tone="row.webhook_status === 'sent' ? 'emerald' : row.webhook_status === 'failed' ? 'rose' : 'zinc'"
            >{{ webhookLabel(row.webhook_status) }}</Badge
          >
          <!-- A real space, not a margin: the reason is text, and a copy of
               this cell read "skipped— no webhook URL configured". -->
          <span
            v-if="webhookReason(row)"
            data-testid="notif-webhook-reason"
            :class="row.webhook_status === 'failed' ? 'text-rose-500' : 'text-zinc-500'"
            >{{ ' — ' + webhookReason(row) }}</span
          >
        </div>
      </template>
      <template #cell-created_at="{ row }">
        <span class="whitespace-nowrap">{{ formatDate(row.created_at, locale) }}</span>
      </template>
    </DataTable>
    <UserSettingsModal v-if="showOwnSettings" v-model="showOwnSettings" initial-section="notifications" />
  </section>
</template>
