<script setup lang="ts">
/**
 * /admin/shares — the ADMINISTRATOR's console over every public link on the
 * instance: who created what, revoke, hard delete. The person's own half is
 * `views/MyShares.vue`; this page sits behind `requiresAdmin` and its listing
 * call is `/api/admin/shares`.
 *
 * ⚠ The PIN, since migration 00049. A link's PIN is no longer ONLY a bcrypt
 * hash: it is also sealed (AES-256-GCM under `FILEX_SECRET_KEY`), so the two
 * principals the owner named — *"paylaşımın sahibi ve admin"* — can be told it
 * again. One row at a time, through `GET /api/shares/{id}/pin`, which audits
 * every read. Each row says which world it is in with `pin_recoverable`, and
 * the words on the page follow that flag: on a recoverable row the old
 * sentence ("filex cannot show this one again") is not a stale comment, it is a
 * LIE on screen — it sends an administrator digging through job messages for
 * something the row beside them would have copied.
 *
 * ⚠⚠ The PIN goes server → clipboard and nowhere else. It is never painted
 * into a row, never put in a toast and never logged: a PIN on screen is a PIN
 * in a screenshot, over a shoulder and in a shared-screen recording. The
 * listing never carries it either — the row asks for it, when asked.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { RefreshCcw, Lock } from 'lucide-vue-next';

import { SharesApi } from '@/api/shares';
// ⚠ The SAME client the owner's own page uses. `/api/shares/{id}/pin` answers
// the creator OR an administrator (handlers/shares_mine.go), so there is one
// endpoint and there must be one caller of it: a second "admin PIN client"
// would be a second place to get the refusal shapes right.
import { MySharesApi, type PinUnavailable } from '@/api/myShares';
import type { PaginatedResponse, Share } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { copyText } from '@/lib/clipboard';
import { formatDate, formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';
import { DataTable, personName, type ContextAction, type DataColumn } from '@brftech/filex-core';
import Modal from '@/components/ui/Modal.vue';

const { t, locale } = useI18n();
const toast = useToastStore();

const shares = ref<PaginatedResponse<Share>>({
  items: [],
  total: 0,
  page: 1,
  page_size: 25,
});
const loading = ref(false);
const q = ref('');
const page = ref(1);
const pageSize = 50;

const showRevoke = ref<Share | null>(null);
const showDelete = ref<Share | null>(null);
const busyId = ref<number | null>(null);

// Admin list rows come back as { share: Share, creator_email, node_path,
// storage_name } from the backend's `ShareWithMeta` envelope. Helper
// unwraps either shape so the template can stay terse.
interface ShareRow {
  share?: Share;
  creator_email?: string;
  /** The creator as every screen names a person (server model.PersonLabel). */
  creator_name?: string;
  node_path?: string;
  storage_name?: string;
  /** Name of the app plugin that opened this link, when one did. */
  plugin_name?: string;
  /** Canonical public link from the server (configured public origin). */
  url?: string;
  [k: string]: unknown;
}
/** Who made the link, named the way every screen names a person. */
function creatorOf(row: ShareRow): string {
  return personName({ name: row.creator_name, email: row.creator_email });
}

function shareOf(row: unknown): Share {
  const r = row as ShareRow & Share;
  const s = r.share ?? (r as unknown as Share);
  // The envelope carries the server-built link; carry it onto the share so
  // shareUrl() below never has to guess an origin.
  return r.url && !s.url ? { ...s, url: r.url } : s;
}

async function load() {
  loading.value = true;
  try {
    shares.value = await SharesApi.list({
      q: q.value || undefined,
      page: page.value,
      page_size: pageSize,
    });
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

watch(q, () => {
  page.value = 1;
  load();
});

async function revoke() {
  if (!showRevoke.value) return;
  busyId.value = showRevoke.value.id;
  try {
    await SharesApi.revoke(showRevoke.value.id);
    toast.success(t('shares.revokedOk'));
    showRevoke.value = null;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busyId.value = null;
  }
}

async function remove() {
  if (!showDelete.value) return;
  busyId.value = showDelete.value.id;
  try {
    await SharesApi.remove(showDelete.value.id);
    toast.success(t('shares.deletedOk'));
    showDelete.value = null;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busyId.value = null;
  }
}

// The public share URL the recipient would actually use. It comes from the
// server (`url` on every admin row = configured public origin + /s/<token>),
// exactly as the share dialog's links do. Building it from the panel's own
// address was wrong the moment the admin opened the panel on localhost or
// through a proxy: they copied a link nobody else could open (issue #32).
// The origin fallback remains only for a server too old to send `url`.
function shareUrl(s: Share): string {
  if (s.url) return s.url;
  if (typeof window === 'undefined') return `/s/${s.token}`;
  return `${window.location.origin}/s/${s.token}`;
}

// koru:k3 — THE "copy link" row action (clipboard + toast). The token cell
// used to carry a second, silent copy of the same link plus a "copy token
// only" button: two buttons for one link is a choice the operator should never
// have had to make, and a token on its own opens nothing.
//
// ⚠ `copyText` is the panel's ONE clipboard route (lib/clipboard.ts): the
// Clipboard API where it exists, the hidden-textarea fallback where it does not
// — which is every install reached over plain http from something that is not
// localhost. This file used to inline that fallback a second time.
async function copyShareLink(s: Share) {
  if (await copyText(shareUrl(s))) toast.success(t('shares.linkCopied'));
  else toast.error(t('shares.copyFailed'));
}

/**
 * Ask the server for ONE link's PIN and put it on the clipboard.
 *
 * ⚠ Three of the four answers are NOT the PIN and each is its own sentence:
 * the link has no PIN, it predates migration 00049, or this instance has no
 * `FILEX_SECRET_KEY`. "Copy failed" for all three would send an administrator
 * looking in the wrong place — and only the last one is anybody's to fix. The
 * wording lives once, in `myShares.pinReason.*`: the fact is about the LINK,
 * not about which page is asking, so a second set of sentences here would only
 * be a second set to keep true.
 *
 * ⚠ 403 is the server refusing this caller (not the owner, not an admin — or
 * an app token, which has no person behind it). Its body is the machine word
 * `forbidden`, so the page says the sentence instead of repeating the code.
 */
async function copyPin(s: Share) {
  if (!s.id) return;
  busyId.value = s.id;
  try {
    const answer = await MySharesApi.pin(s.id);
    if (!answer.pin) {
      toast.error(t(`myShares.pinReason.${(answer.reason ?? 'not_recoverable') as PinUnavailable}`));
      // The row offered something the server would not give. Reload, so the
      // page stops promising it — `pin_recoverable` comes back with the list.
      void load();
      return;
    }
    if (await copyText(answer.pin)) toast.success(t('shares.pinCopied'));
    else toast.error(t('shares.copyFailed'));
  } catch (e: unknown) {
    const status = (e as { response?: { status?: number } } | null)?.response?.status;
    toast.error(status === 403 ? t('shares.pinForbidden') : extractError(e, t('errors.generic')));
  } finally {
    busyId.value = null;
  }
}

// Does this link ask the visitor for a PIN? `has_pin` is the current server
// field, `pin_set` the legacy one older callers still send.
function hasPin(s: Share): boolean {
  return Boolean(s.has_pin || s.pin_set);
}

// Can this row's PIN still be SHOWN? Two different facts: a link can require a
// PIN and still be unable to produce it.
function pinRecoverable(s: Share): boolean {
  return hasPin(s) && Boolean(s.pin_recoverable);
}

// What the row can honestly say about the PIN — and `pin_recoverable` decides
// which of two true sentences that is.
//
//   · recoverable — the PIN is sealed beside its hash (migration 00049), so
//     this row can hand it over: "Copy PIN" in the actions menu, audited on
//     every read. Telling this operator the PIN "can never be shown again"
//     would be false, and falsely discouraging.
//   · not recoverable — the link was minted before 00049, or this instance has
//     no FILEX_SECRET_KEY. Then the original sentence is still the exact truth:
//     the PIN was shown once, at creation, and when an app opened the link
//     (`created_via` / `plugin_name`) that once was the app's own result
//     message, which the Operations Centre still shows.
function pinHint(row: unknown): string {
  const s = shareOf(row);
  if (pinRecoverable(s)) return t('shares.pinHintRecoverable');
  const r = row as ShareRow;
  const app = s.created_via || r.plugin_name || '';
  return app ? t('shares.pinHintApp', { app }) : t('shares.pinHint');
}

// ⚠ koru:k3 used to sort the downloads column HERE, over the one page of 50
// the server had answered — "the backend list endpoint has no sort params, so
// we order the loaded page locally". That is a lie with an arrow on it: page 1
// sorted by downloads is not the most-downloaded links, it is page 1 in a
// different order. The sort is DataTable's now, which does the honest thing
// on its own: while the list spans more than one page it closes the headers
// and says why; when every link is on screen, sorting them is true.

/**
 * The row's verbs, behind its one pinned `Actions` control.
 *
 * They were three unlabelled icon buttons — a chain link, a ban sign and a
 * bin — in a 160px column, and they are exactly the scatter the owner pointed
 * at ("adminde aksiyonlar karma karışık"). Each is now a named entry.
 *
 * ⚠ `copy` still copies `copyShareLink(shareOf(row))`, i.e. the SERVER's
 * canonical `url` from the ShareWithMeta envelope — never a link rebuilt from
 * `window.location`, which was a real bug with a test of its own
 * (tests/components/sharesRowActions.test.ts). Folding the button into a menu
 * changes where the verb is offered, not what it does.
 *
 * ⚠ The PIN BADGE is not here: it stays in the token cell, because "this link
 * asks for a PIN" is a fact about the link and not something a person can do
 * to it. "Copy PIN" is a verb, so it is here — and it is `hidden`, never
 * greyed, where the PIN cannot be produced. A greyed entry invites a click
 * that can never work, and the badge's own hint already says, in words, which
 * of the two cases that row is.
 */
function rowActions(row: Share): ContextAction[] {
  const sh = shareOf(row);
  /* Disabled while THIS row is mid-flight: a PIN read is audited, so a double
     click is two rows in the audit table for one intent. RowActions disables
     its button when no entry is usable, which is what greying every entry
     here does — the same effect its old `:disabled` prop had. */
  const busy = busyId.value === sh.id;
  return withBusy(busy, [
    { key: 'copy', label: t('shares.copyLink'), icon: 'link' },
    { key: 'copy-pin', label: t('shares.copyPin'), icon: 'copy', hidden: !pinRecoverable(sh) },
    {
      key: 'revoke',
      label: t('shares.revoke'),
      icon: 'lock',
      danger: true,
      // A link already revoked cannot be revoked again; the row says so in
      // its expiry column, so the verb goes rather than greys.
      hidden: isRevoked(sh),
    },
    { key: 'delete', label: t('common.delete'), icon: 'delete', danger: true },
  ]);
}

/** Ended by a person (or its app) rather than run out: `revoked_at`, written
 *  with the expiry since 00053; `revoked` is the legacy boolean. */
function isRevoked(s: Share): boolean {
  return Boolean(s.revoked_at || s.revoked);
}

function withBusy(busy: boolean, list: ContextAction[]): ContextAction[] {
  return busy ? list.map((a) => ({ ...a, disabled: true })) : list;
}

function onRowAction(key: string, row: Share) {
  const sh = shareOf(row);
  if (key === 'copy') void copyShareLink(sh);
  else if (key === 'copy-pin') void copyPin(sh);
  else if (key === 'revoke') showRevoke.value = sh;
  else if (key === 'delete') showDelete.value = sh;
}

/* The explorer's table (DataTable), remembered on the account under
 * `admin.shares`. Rows are ShareWithMeta envelopes, so every sort value goes
 * through `shareOf`. */
const dateOf = (v: string | null | undefined) => (v ? Date.parse(v) : null);
const columns = computed<DataColumn<Share>[]>(() => [
  {
    id: 'token',
    label: t('shares.fields.token'),
    sortable: true,
    width: 200,
    sortValue: (r) => shareOf(r).token,
  },
  {
    id: 'storage_name',
    label: t('shares.fields.storage'),
    sortable: true,
    width: 130,
    format: (r) => (r as unknown as ShareRow).storage_name || '—',
    sortValue: (r) => (r as unknown as ShareRow).storage_name || null,
  },
  {
    id: 'path',
    label: t('shares.fields.path'),
    sortable: true,
    width: 220,
    sortValue: (r) => (r as unknown as ShareRow).node_path || shareOf(r).path || null,
  },
  {
    id: 'created_at',
    label: t('shares.fields.created'),
    sortable: true,
    sortDir: 'desc',
    width: 160,
    sortValue: (r) => dateOf(shareOf(r).created_at),
  },
  {
    id: 'expires_at',
    label: t('shares.fields.expires'),
    sortable: true,
    width: 200,
    sortValue: (r) => dateOf(shareOf(r).expires_at),
  },
  {
    id: 'download_count',
    label: t('shares.fields.downloads'),
    align: 'right',
    sortable: true /* koru:k3 */,
    sortDir: 'desc',
    width: 110,
    format: (r) => {
      const s = shareOf(r);
      const max = s.max_downloads ?? null;
      const cur = s.download_count ?? 0;
      return max ? `${cur} / ${max}` : String(cur);
    },
    sortValue: (r) => shareOf(r).download_count ?? 0,
  },
  {
    id: 'creator',
    label: t('shares.fields.creator'),
    sortable: true,
    width: 180,
    sortValue: (r) => creatorOf(r as unknown as ShareRow) || null,
  },
]);

onMounted(load);
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('shares.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('shares.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" @click="load" :loading="loading">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </div>

    <DataTable
      table-id="admin.shares"
      :columns="columns"
      :rows="shares.items"
      :loading="loading"
      :empty="t('shares.noResults')"
      :page="page"
      :page-size="pageSize"
      :total="shares.total"
      :row-key="(r: Share) => shareOf(r).id"
      :row-attrs="(r: Share) => ({ 'data-testid': `share-row-${shareOf(r).id}` })"
      :row-actions="(row: Share) => rowActions(row)"
      :row-actions-test-id="(row: Share) => `share-actions-${shareOf(row).id}`"
      @row-action="(key: string, row: Share) => onRowAction(key, row)"
      @page="(p: number) => ((page = p), load())"
    >
      <template #toolbar>
        <Input v-model="q" :placeholder="t('common.search')" size="sm" class="w-60" />
      </template>

      <template #cell-token="{ row }">
        <div class="flex items-center gap-2">
          <code
            class="text-xs font-mono text-zinc-700 dark:text-zinc-300"
            data-testid="share-token"
            :data-token="shareOf(row).token"
          >
            {{ shareOf(row).token.slice(0, 10) }}…
          </code>
          <!--
            No copy button here. "Copy link" lives once, in the actions
            column, where it confirms by toast; a second, silent copy of the
            same link beside the token was the same affordance twice. The
            bare "copy token only" that used to sit next to it is gone: a
            token on its own opens nothing, and it is the thing the product
            owner asked us to drop.
          -->
          <Badge
            v-if="hasPin(shareOf(row))"
            tone="amber"
            size="xs"
            data-testid="share-pin"
            :title="pinHint(row)"
            :aria-label="pinHint(row)"
          >
            <Lock class="h-3 w-3" aria-hidden="true" />
            {{ t('shares.pinProtected') }}
          </Badge>
          <!-- ⚠ No "revoked" badge here any more. It was a hard-coded
               English word waiting for a `revoked_at` no server sent, so it
               never drew (QA, 2026-09-21). The state is said ONCE, in the
               expiry column — the same place My shares says it — because a
               row that reads "İptal edildi" twice is a row nobody re-reads. -->
        </div>
      </template>

      <template #cell-created_at="{ row }">
        <span class="text-xs whitespace-nowrap" :title="formatDate(shareOf(row).created_at, locale)">
          {{ formatDate(shareOf(row).created_at, locale) }}
        </span>
      </template>

      <template #cell-path="{ row }">
        <span class="text-xs font-mono text-zinc-500 truncate max-w-xs inline-block">
          {{ (row as ShareRow).node_path || shareOf(row).path }}
        </span>
      </template>

      <template #cell-creator="{ row }">
        <span class="text-xs text-zinc-500 dark:text-zinc-400" :title="(row as ShareRow).creator_email">
          {{ creatorOf(row as ShareRow) || ('#' + (shareOf(row).created_by ?? '?')) }}
          <!-- token username the creating API call acted under ("work", "fishapp"…) -->
          <span
            v-if="shareOf(row).created_via"
            class="ms-1 rounded bg-violet-100 dark:bg-violet-900/40 px-1 py-0.5 font-mono text-[10px] text-violet-700 dark:text-violet-300"
          >{{ shareOf(row).created_via }}</span>
        </span>
      </template>

      <template #cell-expires_at="{ row }">
        <span class="text-xs whitespace-nowrap" :title="shareOf(row).expires_at ? formatDate(shareOf(row).expires_at, locale) : ''">
          <!-- ⚠ A revoked link's `expires_at` is the moment it was revoked;
               printed as an expiry it read "21 Eyl 2026 15:55 · 9 saniye
               önce" — a link that had merely run out. `revoked_at` (00053)
               says which it is. -->
          <span v-if="isRevoked(shareOf(row))" class="text-rose-600 dark:text-rose-400" data-testid="share-revoked-on">
            {{ t('shares.revokedOn', { date: formatDate(shareOf(row).revoked_at || shareOf(row).expires_at, locale) }) }}
          </span>
          <template v-else-if="shareOf(row).expires_at">
            {{ formatDate(shareOf(row).expires_at, locale) }}
            <span class="text-zinc-400">·</span>
            {{ formatRelative(shareOf(row).expires_at, locale) }}
          </template>
          <template v-else>{{ t('shares.neverExpires') }}</template>
        </span>
      </template>

    </DataTable>

    <!--
      ⚠ The title is the QUESTION and the buttons are the two answers, in
      words. It was titled with the success toast — "Paylaşım iptal edildi"
      over "Bu paylaşımı iptal et?" — so the dialog announced as done what it
      was asking permission for, and its buttons were "İptal" / "Onayla": in
      Turkish "iptal" is both "cancel" and "revoke", so the safe button read
      like the dangerous one (QA, 2026-09-21).
    -->
    <Modal
      :model-value="showRevoke !== null"
      :title="t('shares.revokeTitle')"
      size="sm"
      @update:model-value="(v) => (v ? null : (showRevoke = null))"
    >
      <p class="text-sm">{{ t('shares.revokeConfirm') }}</p>
      <template #footer>
        <Button variant="ghost" data-testid="share-revoke-dismiss" @click="showRevoke = null">{{ t('common.dismiss') }}</Button>
        <Button variant="danger" data-testid="share-revoke-confirm" :loading="busyId === showRevoke?.id" @click="revoke">
          {{ t('shares.revokeDo') }}
        </Button>
      </template>
    </Modal>

    <Modal
      :model-value="showDelete !== null"
      :title="t('common.delete')"
      size="sm"
      @update:model-value="(v) => (v ? null : (showDelete = null))"
    >
      <p class="text-sm">{{ t('shares.deleteConfirm') }}</p>
      <template #footer>
        <Button variant="ghost" @click="showDelete = null">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="busyId === showDelete?.id" @click="remove">
          {{ t('common.yesDelete') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
