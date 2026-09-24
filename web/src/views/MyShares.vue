<script setup lang="ts">
/**
 * "Paylaştıklarım" / "My shares" — the links THIS person handed out.
 *
 * The product owner, 2026-09-20: *"Paylaşım sahibi paylaştıklarını da bir
 * menüde görebilir olsun, 'Paylaştıklarım' diye."*
 *
 * ⚠ NOT a second Shares page. `views/Shares.vue` is the ADMINISTRATOR's
 * console over everybody's links (revoke, hard delete, who created what) and
 * it sits behind `requiresAdmin`; every call it makes is `/api/admin/shares`,
 * which an ordinary account is refused. Until this screen there was no
 * user-scoped half at all: a person minted a link from the explorer, the
 * dialog showed it once, and afterwards the only route back to it was to mint
 * another one — which leaves the first still live.
 *
 * ⚠⚠ It lives OUTSIDE the AdminLayout block on purpose (router/index.ts). The
 * whole panel is admin-only and a non-admin who reaches one of its routes is
 * bounced to the end-user front door, so a "My shares" page inside it would
 * be a page for everybody that only administrators could open.
 *
 * ⚠ The PIN. Since migration 00049 a link's PIN is sealed as well as hashed,
 * so its OWNER and an ADMIN can be told it again — one row at a time, through
 * `GET /api/shares/{id}/pin`, audited on every read. It is never in the
 * listing, so this page asks for it only when the person picks "Copy PIN",
 * and copies it straight to the clipboard rather than painting it into the
 * row: a PIN on screen is a PIN in a screenshot, over a shoulder and in a
 * shared-screen recording.
 *
 * ⚠ The table is DataTable — the explorer's own table, the only one in the
 * product — so its columns resize, hide, move and sort like a folder's, and
 * the arrangement is remembered on the account under `my.shares`. The row's
 * verbs live behind its ONE pinned "Actions" control (`row-actions` → the
 * explorer's own ContextMenu); loose buttons in a cell, or a second table,
 * are regressions with a test of their own (tests/ui/tablePinnedActions.test.ts).
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { ArrowLeft, Link2, Lock, RefreshCcw } from 'lucide-vue-next';

import { MySharesApi, type MyShareRow, type PinUnavailable } from '@/api/myShares';
import type { PaginatedResponse, Share } from '@/api/types';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { copyText } from '@/lib/clipboard';
import { formatDate, formatRelative } from '@/lib/format';

import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Modal from '@/components/ui/Modal.vue';
import { DataTable, pluginLabelOf as labelOf, type ContextAction, type DataColumn } from '@brftech/filex-core';

const { t, locale } = useI18n();
const toast = useToastStore();
const router = useRouter();

const rows = ref<PaginatedResponse<MyShareRow>>({ items: [], total: 0, page: 1, page_size: 25 });
const loading = ref(false);
const page = ref(1);
const pageSize = 25;
const confirmRevoke = ref<MyShareRow | null>(null);
const busyId = ref<number | null>(null);

/**
 * An app's link, named for what it is and opening the app's page for it
 * (db.AppLink — the owner's decision, 2026-09-21: a signing link is a signing
 * request, not a plain share of the file). ⚠ The route is the app's own home
 * page in this tab (`app-home`), the same place its notices land.
 */
function appLabel(row: MyShareRow): string {
  return row.app ? labelOf(row.app.label, locale.value) : '';
}
function appRoute(row: MyShareRow) {
  const a = row.app;
  if (!a?.view) return null;
  return { name: 'app-home', params: { plugin: a.plugin, view: a.view }, query: a.section ? { section: a.section } : {} };
}
/** What revoking THIS link does: the app's own words when it gave them. */
function revokeWarning(row: MyShareRow | null): string {
  const said = row?.app?.revoke ? labelOf(row.app.revoke, locale.value) : '';
  return said || t('myShares.revokeConfirm');
}

function shareOf(row: MyShareRow): Share {
  return row.share ?? ({} as Share);
}

/** The recipient's link, as the SERVER built it from its public origin. */
function linkOf(row: MyShareRow): string {
  if (row.url) return row.url;
  // Only reachable against a server older than this endpoint, which cannot
  // happen for a page the same binary serves. Kept so a missing `url` degrades
  // to something openable instead of "undefined".
  const token = shareOf(row).token ?? '';
  return typeof window === 'undefined' ? `/s/${token}` : `${window.location.origin}/s/${token}`;
}

function hasPin(row: MyShareRow): boolean {
  const s = shareOf(row);
  return Boolean(s.has_pin || s.pin_set);
}

/** Can the PIN still be SHOWN? A PIN can be required and yet unrecoverable. */
function pinRecoverable(row: MyShareRow): boolean {
  return hasPin(row) && Boolean(shareOf(row).pin_recoverable);
}

function expiresAt(row: MyShareRow): string | null | undefined {
  return shareOf(row).expires_at;
}

function isExpired(row: MyShareRow): boolean {
  const at = expiresAt(row);
  return Boolean(at && new Date(at).getTime() <= Date.now());
}

/**
 * Ended by its owner (or the app that opened it) rather than run out.
 *
 * ⚠ Revoking has always worked by setting the expiry to now, so this page
 * said "Süresi doldu" for a link revoked a minute earlier (QA, 2026-09-21).
 * The server records `revoked_at` beside the expiry since 00053; a link
 * revoked before that cannot be told apart and still reads as expired.
 */
function isRevoked(row: MyShareRow): boolean {
  const s = shareOf(row);
  return Boolean(s.revoked_at || s.revoked);
}

async function load() {
  loading.value = true;
  try {
    rows.value = await MySharesApi.list({ page: page.value, page_size: pageSize });
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

async function copyLink(row: MyShareRow) {
  if (await copyText(linkOf(row))) toast.success(t('myShares.linkCopied'));
  else toast.error(t('myShares.copyFailed'));
}

/**
 * Ask the server for this link's PIN and put it on the clipboard.
 *
 * ⚠ Three of the four answers are NOT the PIN, and each is a different
 * sentence: the link has none, it predates the feature, or this server has no
 * encryption key at all. Saying "copy failed" for all three would send the
 * person (or their administrator) looking in the wrong place — the last one is
 * the only one anybody can fix.
 */
async function copyPin(row: MyShareRow) {
  const id = shareOf(row).id;
  if (!id) return;
  busyId.value = id;
  try {
    const answer = await MySharesApi.pin(id);
    if (!answer.pin) {
      const reason = (answer.reason ?? 'not_recoverable') as PinUnavailable;
      toast.error(t(`myShares.pinReason.${reason}`));
      // The row said it could be shown and the server disagreed — reload so
      // the screen stops offering something that is not there.
      void load();
      return;
    }
    if (await copyText(answer.pin)) toast.success(t('myShares.pinCopied'));
    else toast.error(t('myShares.copyFailed'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busyId.value = null;
  }
}

async function revoke() {
  const row = confirmRevoke.value;
  const id = row ? shareOf(row).id : 0;
  if (!id) return;
  busyId.value = id;
  try {
    await MySharesApi.revoke(id);
    toast.success(t('myShares.revokedOk'));
    confirmRevoke.value = null;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busyId.value = null;
  }
}

/**
 * The row's verbs.
 *
 * ⚠ "Copy PIN" is `hidden`, not `disabled`, when there is nothing to copy: a
 * greyed entry invites a click that can never work, and the row already says
 * in words which of the two cases it is (the badge beside the item).
 */
function rowActions(row: MyShareRow): ContextAction[] {
  /* ⚠ A row mid-request (a PIN being fetched, a revoke in flight) greys every
     verb, which is what greys its one Actions control — the table's own
     control, so "busy" is said through the entries rather than a prop of its
     own. */
  const busy = busyId.value !== null && busyId.value === shareOf(row).id;
  return [
    { key: 'copy-link', label: t('myShares.copyLink'), icon: 'link', disabled: busy },
    {
      key: 'copy-pin',
      label: t('myShares.copyPin'),
      icon: 'copy',
      hidden: !pinRecoverable(row),
      disabled: busy,
    },
    {
      key: 'revoke',
      label: t('myShares.revoke'),
      icon: 'lock',
      danger: true,
      hidden: isExpired(row),
      disabled: busy,
    },
  ];
}

function onRowAction(key: string, row: MyShareRow) {
  if (key === 'copy-link') void copyLink(row);
  else if (key === 'copy-pin') void copyPin(row);
  else if (key === 'revoke') confirmRevoke.value = row;
}

/* ⚠ The list is paged by the SERVER, which has no sort parameter: while it
 * spans more than one page the table closes its headers and says why, rather
 * than re-ordering one page and calling it sorted. */
const columns = computed<DataColumn<MyShareRow>[]>(() => [
  {
    id: 'item',
    label: t('myShares.fields.item'),
    sortable: true,
    width: 260,
    sortValue: (r) => r.node_path || '',
  },
  {
    id: 'storage_name',
    label: t('myShares.fields.storage'),
    sortable: true,
    width: 140,
    format: (r) => r.storage_name || '—',
  },
  {
    id: 'pin',
    label: t('myShares.fields.pin'),
    sortable: true,
    width: 150,
    /* recoverable PIN → PIN that cannot be shown → no PIN */
    sortValue: (r) => (pinRecoverable(r) ? 0 : hasPin(r) ? 1 : 2),
  },
  {
    id: 'expires_at',
    label: t('myShares.fields.expires'),
    sortable: true,
    width: 220,
    sortValue: (r) => {
      const at = expiresAt(r);
      return at ? Date.parse(at) : null;
    },
  },
  {
    id: 'download_count',
    label: t('myShares.fields.downloads'),
    sortable: true,
    sortDir: 'desc',
    align: 'right',
    width: 110,
    format: (r) => {
      const s = shareOf(r);
      const max = s.max_downloads ?? null;
      const cur = s.download_count ?? 0;
      return max ? `${cur} / ${max}` : String(cur);
    },
    sortValue: (r) => shareOf(r).download_count ?? 0,
  },
]);

onMounted(load);
</script>

<template>
  <section class="mx-auto w-full max-w-5xl space-y-4 p-4 sm:p-6">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <button
          type="button"
          class="inline-flex items-center gap-1 text-sm text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100"
          data-testid="my-shares-back"
          @click="router.push({ name: 'home' })"
        >
          <ArrowLeft class="h-4 w-4" aria-hidden="true" />
          {{ t('myShares.back') }}
        </button>
        <h1
          class="mt-2 flex items-center gap-2 text-2xl font-semibold text-zinc-900 dark:text-zinc-100"
          data-testid="my-shares-title"
        >
          <Link2 class="h-6 w-6 text-zinc-500 dark:text-zinc-400" aria-hidden="true" />
          {{ t('myShares.title') }}
        </h1>
        <p class="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{{ t('myShares.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" :loading="loading" @click="load">
        <RefreshCcw class="h-4 w-4" aria-hidden="true" />
        {{ t('common.refresh') }}
      </Button>
    </header>

    <DataTable
      table-id="my.shares"
      :columns="columns"
      :rows="rows.items"
      :loading="loading"
      :empty="t('myShares.noResults')"
      :page="page"
      :page-size="pageSize"
      :total="rows.total"
      :row-key="(r: MyShareRow) => shareOf(r).id"
      :row-attrs="(r: MyShareRow) => ({ 'data-testid': `my-share-row-${shareOf(r).id}` })"
      :row-actions="(r: MyShareRow) => rowActions(r)"
      :row-actions-test-id="(r: MyShareRow) => `my-share-actions-${shareOf(r).id}`"
      @row-action="(key: string, r: MyShareRow) => onRowAction(key, r)"
      @page="(p: number) => ((page = p), load())"
    >
      <!-- WHAT is shared. The link itself is not painted: it is long, it is a
           live public URL, and the row's "Copy link" is what a person does
           with it. -->
      <!-- ⚠ Wrapped: a DataTable cell is a flex row, and the app's badge
           belongs UNDER the path, not squeezed beside a truncated one. -->
      <template #cell-item="{ row }">
        <div class="min-w-0">
          <span class="block max-w-xs truncate font-mono text-xs" :title="row.node_path || ''">
            {{ row.node_path || '—' }}
          </span>
          <!-- An app's link says what it is and opens the app's page for it. -->
          <router-link
            v-if="row.app && appRoute(row)"
            :to="appRoute(row)!"
            class="mt-0.5 inline-flex"
            :data-testid="`my-share-app-${shareOf(row).id}`"
          >
            <Badge tone="sky" size="xs">{{ appLabel(row) }}</Badge>
          </router-link>
          <Badge v-else-if="row.app" tone="sky" size="xs" class="mt-0.5" :data-testid="`my-share-app-${shareOf(row).id}`">
            {{ appLabel(row) }}
          </Badge>
        </div>
      </template>

      <!-- Whether a PIN is set, and — separately — whether it can still be
           shown. The two are different facts and a row that ran them together
           would offer a copy button with nothing behind it. -->
      <template #cell-pin="{ row }">
        <Badge
          v-if="pinRecoverable(row)"
          tone="amber"
          size="xs"
          data-testid="my-share-pin"
          :title="t('myShares.pinProtected')"
        >
          <Lock class="h-3 w-3" aria-hidden="true" />
          {{ t('myShares.pinProtected') }}
        </Badge>
        <Badge
          v-else-if="hasPin(row)"
          tone="zinc"
          size="xs"
          data-testid="my-share-pin-hidden"
          :title="t('myShares.pinReason.not_recoverable')"
        >
          <Lock class="h-3 w-3" aria-hidden="true" />
          {{ t('myShares.pinHidden') }}
        </Badge>
        <span v-else class="text-xs text-zinc-400" data-testid="my-share-no-pin">
          {{ t('myShares.noPin') }}
        </span>
      </template>

      <template #cell-expires_at="{ row }">
        <span class="whitespace-nowrap text-xs" data-testid="my-share-expires">
          <template v-if="expiresAt(row)">
            <span v-if="isRevoked(row)" class="text-rose-600 dark:text-rose-400" data-testid="my-share-revoked">
              {{ t('myShares.revoked') }}
            </span>
            <span v-else-if="isExpired(row)" class="text-rose-600 dark:text-rose-400">
              {{ t('myShares.expired') }}
            </span>
            <template v-else>
              {{ formatDate(expiresAt(row), locale) }}
              <span class="text-zinc-400">·</span>
              {{ formatRelative(expiresAt(row), locale) }}
            </template>
          </template>
          <template v-else>{{ t('myShares.neverExpires') }}</template>
        </span>
      </template>
    </DataTable>

    <!--
      ⚠ Title = the question, buttons = the two answers in words. It read
      "İptal et" over "İptal" / "Onayla" — in Turkish "iptal" is both
      "cancel" and "revoke", so the title and the safe button said the same
      word for opposite things (QA, 2026-09-21).
    -->
    <Modal
      :model-value="confirmRevoke !== null"
      :title="t('myShares.revokeTitle')"
      size="sm"
      @update:model-value="(v) => (v ? null : (confirmRevoke = null))"
    >
      <p class="text-sm" data-testid="my-share-revoke-warning">{{ revokeWarning(confirmRevoke) }}</p>
      <template #footer>
        <Button variant="ghost" data-testid="my-share-revoke-dismiss" @click="confirmRevoke = null">{{ t('common.dismiss') }}</Button>
        <Button
          variant="danger"
          data-testid="my-share-revoke-confirm"
          :loading="busyId !== null"
          @click="revoke"
        >
          {{ t('myShares.revokeDo') }}
        </Button>
      </template>
    </Modal>
  </section>
</template>
