<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Pencil, Plus, Trash2, RefreshCcw, KeyRound } from 'lucide-vue-next';

import { AITokensApi, type AIToken } from '@/api/ai-tokens';
import { StoragesApi } from '@/api/storages';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Badge from '@/components/ui/Badge.vue';
import Modal from '@/components/ui/Modal.vue';
import CopyButton from '@/components/ui/CopyButton.vue';
import { DataTable, splitList, type ContextAction, type DataColumn } from '@brftech/filex-core';
import { driverName } from '@/lib/storageWords';

const { t, te, locale } = useI18n();
const toast = useToastStore();

const tokens = ref<AIToken[]>([]);
const loading = ref(false);

const SCOPES = ['read', 'write', 'delete', 'mcp', 'admin'] as const;
type Scope = (typeof SCOPES)[number];

const showCreate = ref(false);
const showDelete = ref<AIToken | null>(null);
const creating = ref(false);
const deleting = ref(false);

const newLabel = ref('');
const newScopes = ref<Record<Scope, boolean>>({
  read: true,
  write: true,
  delete: false,
  mcp: true,
  admin: false,
});
const newExpiry = ref<number | null>(null);
const newUsernames = ref('');
const newRootStorage = ref('');
const newRootPath = ref('');
const createdToken = ref<string | null>(null);

// Edit modal (label + username allow-list; the credential itself is immutable).
const showEdit = ref<AIToken | null>(null);
const editLabel = ref('');
const editUsernames = ref('');
const savingEdit = ref(false);

const storages = ref<{ value: string; label: string }[]>([]);
const storageOptions = computed(() => [
  { value: '', label: t('apiMcp.fields.rootNone') },
  ...storages.value,
]);

const origin = window.location.origin;
const mcpUrl = `${origin}/api/ai/mcp`;
const restBase = `${origin}/api/ai`;

const claudeSnippet = computed(
  () =>
    `claude mcp add --transport http filex ${mcpUrl} --header "X-Filex-Token: ${
      createdToken.value ?? '<TOKEN>'
    }"`,
);

function scopeList(s: string): string[] {
  const v = (s ?? '').trim();
  return v ? v.split(',') : [];
}

// A token's scope string mixes verb scopes (read/write/…) with at most one
// `root:<adapter>://<rel>` confinement scope. Split them for display.
function verbScopes(s: string): string[] {
  return scopeList(s).filter((x) => !x.startsWith('root:'));
}
/** A scope by name; one this build does not know is shown as its id. */
function scopeName(s: string): string {
  return (SCOPES as readonly string[]).includes(s) ? t(`apiMcp.scopeName.${s}`) : s;
}
function rootScope(s: string): string | null {
  const r = scopeList(s).find((x) => x.startsWith('root:'));
  return r ? r.slice('root:'.length) : null;
}

async function load() {
  loading.value = true;
  try {
    tokens.value = await AITokensApi.list();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

async function loadStorages() {
  try {
    const list = await StoragesApi.list();
    storages.value = list.map((s) => ({ value: s.name, label: `${s.name} (${driverName(s.driver, t, te)})` }));
  } catch {
    /* tolerated — root selection simply stays empty (full disk) */
  }
}

function openCreate() {
  createFailure.value = '';
  newLabel.value = '';
  newScopes.value = { read: true, write: true, delete: false, mcp: true, admin: false };
  newExpiry.value = null;
  newRootStorage.value = '';
  newRootPath.value = '';
  newUsernames.value = '';
  createdToken.value = null;
  showCreate.value = true;
}

/*
 * ⚠⚠ At least one permission, and `admin` only when it is ticked (owner's
 * decision, v0.43.0). This form used to say "If none are selected, all
 * scopes are granted", and a token minted with nothing ticked read
 * /api/ai/admin/users — the whole admin surface (release-candidate sweep,
 * 2026-09-21). The server refuses an empty list on every door
 * (apitoken.ParseIssued); here Create is not offered until something is
 * ticked, and a refusal is said inside the dialog, not in a toast behind it.
 *
 * ⚠ ONE sentence under the checkboxes, said in red when nothing is ticked
 * and in grey once something is. It used to be two keys with the same
 * content in two wordings (`apiMcp.errScopes` + `fields.scopesHint`), which
 * every translator had to write twice and which could drift apart
 * (v0.43.0 translation sweep).
 */
const noScope = computed(() => !SCOPES.some((s) => newScopes.value[s]));
const createFailure = ref('');

async function submitCreate() {
  createFailure.value = '';
  // Enter in a box reaches here too (the form's hidden submit button), where
  // the disabled Create button does not guard.
  if (!newLabel.value.trim() || creating.value) return;
  if (noScope.value) {
    createFailure.value = t('apiMcp.fields.scopesHint');
    return;
  }
  creating.value = true;
  try {
    const parts: string[] = SCOPES.filter((s) => newScopes.value[s]);
    if (newRootStorage.value) {
      const rel = newRootPath.value.trim().replace(/^\/+|\/+$/g, '');
      parts.push(`root:${newRootStorage.value}://${rel}`);
    }
    const res = await AITokensApi.create({
      label: newLabel.value.trim(),
      scopes: parts.join(','),
      usernames: parseUsernames(newUsernames.value),
      expires_in_days: newExpiry.value && newExpiry.value > 0 ? newExpiry.value : undefined,
    });
    createdToken.value = res.token;
    toast.success(t('apiMcp.createdOk'));
    await load();
  } catch (e: unknown) {
    createFailure.value = extractError(e, t('errors.generic'));
  } finally {
    creating.value = false;
  }
}

function closeCreate() {
  showCreate.value = false;
  createdToken.value = null;
}

// "work, fishapp" → ["work","fishapp"] (comma/space separated; first = default).
// ⚠ The shared splitter (core lib/listInput): "،" and "，" separate too.
function parseUsernames(raw: string): string[] {
  return splitList(raw, { spaces: true });
}

function usernameList(tok: AIToken): string[] {
  return (tok.usernames || '')
    .split(',')
    .map((u) => u.trim())
    .filter(Boolean);
}

/** An ISO timestamp as a sortable number; absent sorts last (DataTable). */
function stamp(v: string | null | undefined): number | null {
  if (!v) return null;
  const n = Date.parse(v);
  return Number.isFinite(n) ? n : null;
}

/* The explorer's table (DataTable): every column resizes, hides, moves and
 * sorts, remembered on the account under `admin.api-mcp`. The endpoint
 * answers every token at once, so the table's own client sort is honest. */
const columns = computed<DataColumn<AIToken>[]>(() => [
  { id: 'label', label: t('apiMcp.cols.label'), sortable: true, width: 180 },
  {
    id: 'usernames',
    label: t('apiMcp.cols.usernames'),
    sortable: true,
    width: 180,
    sortValue: (tok) => usernameList(tok)[0] ?? tok.label ?? '',
  },
  {
    id: 'scopes',
    label: t('apiMcp.cols.scopes'),
    sortable: true,
    width: 180,
    /* Tokens with no scope list can do everything ("all") — they sort as the
       broadest, ahead of any narrowed set. */
    sortValue: (tok) => (scopeList(tok.scopes).length ? verbScopes(tok.scopes).join(',') : ''),
  },
  {
    id: 'root',
    label: t('apiMcp.cols.root'),
    sortable: true,
    width: 180,
    sortValue: (tok) => rootScope(tok.scopes) || '',
  },
  {
    id: 'last_used_at',
    label: t('apiMcp.cols.lastUsed'),
    sortable: true,
    sortDir: 'desc',
    width: 130,
    sortValue: (tok) => stamp(tok.last_used_at),
  },
  {
    id: 'expires_at',
    label: t('apiMcp.cols.expires'),
    sortable: true,
    width: 130,
    sortValue: (tok) => stamp(tok.expires_at),
  },
  {
    id: 'created_at',
    label: t('apiMcp.cols.created'),
    sortable: true,
    sortDir: 'desc',
    width: 130,
    sortValue: (tok) => stamp(tok.created_at),
  },
]);

function openEdit(tok: AIToken) {
  showEdit.value = tok;
  editLabel.value = tok.label;
  editUsernames.value = usernameList(tok).join(', ');
}

async function submitEdit() {
  if (!showEdit.value) return;
  savingEdit.value = true;
  try {
    await AITokensApi.update(showEdit.value.id, {
      label: editLabel.value.trim(),
      usernames: parseUsernames(editUsernames.value),
    });
    toast.success(t('apiMcp.updatedOk'));
    showEdit.value = null;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingEdit.value = false;
  }
}

async function confirmDelete() {
  if (!showDelete.value) return;
  deleting.value = true;
  try {
    await AITokensApi.remove(showDelete.value.id);
    toast.success(t('apiMcp.deletedOk'));
    showDelete.value = null;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    deleting.value = false;
  }
}

onMounted(() => {
  load();
  loadStorages();
});

/** The row's verbs. They were two unlabelled icon buttons — a pencil and a
 *  bin whose only names were `title` attributes; they are now named entries
 *  behind the row's one pinned `Actions` control. Delete keeps its
 *  confirmation dialog (`showDelete`), which is where it always was. */
function rowActions(_row: AIToken): ContextAction[] {
  return [
    { key: 'edit', label: t('common.edit'), icon: 'rename' },
    { key: 'delete', label: t('common.delete'), icon: 'delete', danger: true },
  ];
}

function onRowAction(key: string, row: AIToken) {
  if (key === 'edit') openEdit(row);
  else if (key === 'delete') showDelete.value = row;
}
</script>

<template>
  <div class="space-y-4 max-w-4xl">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('apiMcp.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('apiMcp.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" :loading="loading" @click="load">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button data-testid="ai-token-new" @click="openCreate">
          <Plus class="h-4 w-4" />
          {{ t('apiMcp.newToken') }}
        </Button>
      </div>
    </div>

    <!-- Connection info -->
    <div class="card card-body space-y-3">
      <h2 class="text-sm font-semibold flex items-center gap-2">
        <KeyRound class="h-4 w-4" />
        {{ t('apiMcp.connectTitle') }}
      </h2>
      <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('apiMcp.connectHint') }}</p>
      <div class="space-y-2">
        <div>
          <p class="text-xs text-zinc-500 mb-1">{{ t('apiMcp.mcpEndpoint') }}</p>
          <div class="flex items-center gap-2">
            <code
              class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-xs font-mono break-all"
            >{{ mcpUrl }}</code>
            <CopyButton :value="mcpUrl" />
          </div>
        </div>
        <div>
          <p class="text-xs text-zinc-500 mb-1">{{ t('apiMcp.restBase') }}</p>
          <div class="flex items-center gap-2">
            <code
              class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-xs font-mono break-all"
            >{{ restBase }}</code>
            <CopyButton :value="restBase" />
          </div>
        </div>
      </div>
    </div>

    <!-- Tokens table -->
    <DataTable
      table-id="admin.api-mcp"
      :columns="columns"
      :rows="tokens"
      :loading="loading"
      :empty="t('apiMcp.empty')"
      row-key="id"
      :row-actions="(row: AIToken) => rowActions(row)"
      :row-actions-test-id="(row: AIToken) => `ai-token-actions-${row.id}`"
      @row-action="(key: string, row: AIToken) => onRowAction(key, row)"
    >
      <template #cell-label="{ row }">
        <span class="font-medium">{{ row.label || '—' }}</span>
      </template>

      <template #cell-usernames="{ row }">
        <div class="flex flex-wrap items-center gap-1">
          <!-- first entry = default identity; no list → the label doubles as it -->
          <Badge
            v-for="(u, i) in usernameList(row)"
            :key="u"
            :tone="i === 0 ? 'violet' : 'zinc'"
            size="xs"
            >{{ u }}</Badge
          >
          <span v-if="!usernameList(row).length" class="tbl-sub">{{ row.label || '—' }}</span>
        </div>
      </template>

      <template #cell-scopes="{ row }">
        <div class="flex flex-wrap gap-1">
          <!-- ⚠ By name, in the panel's language — the chips printed the
               scope ids ("read", "write", "all") in the Turkish panel
               (release-candidate sweep, 2026-09-21). The id is the tooltip:
               it is what an API client sends. -->
          <Badge v-if="!scopeList(row.scopes).length" tone="amber" size="xs">{{ t('apiMcp.scopeAll') }}</Badge>
          <Badge
            v-for="s in verbScopes(row.scopes)"
            :key="s"
            :tone="s === 'admin' ? 'rose' : 'zinc'"
            size="xs"
            :title="s"
            data-testid="ai-token-scope-chip"
            >{{ scopeName(s) }}</Badge
          >
        </div>
      </template>

      <template #cell-root="{ row }">
        <span v-if="rootScope(row.scopes)" class="tbl-mono tbl-clamp text-violet-600 dark:text-violet-400">
          📁 {{ rootScope(row.scopes) }}
        </span>
        <span v-else>{{ t('apiMcp.fullDisk') }}</span>
      </template>

      <template #cell-last_used_at="{ row }">
        <span class="whitespace-nowrap">{{ row.last_used_at ? formatRelative(row.last_used_at, locale) : '—' }}</span>
      </template>
      <template #cell-expires_at="{ row }">
        <span class="whitespace-nowrap">{{
          row.expires_at ? formatRelative(row.expires_at, locale) : t('apiMcp.never')
        }}</span>
      </template>
      <template #cell-created_at="{ row }">
        <span class="whitespace-nowrap">{{ formatRelative(row.created_at, locale) }}</span>
      </template>
    </DataTable>

    <!-- Create / reveal modal -->
    <Modal
      :model-value="showCreate"
      :title="createdToken ? t('apiMcp.createdOk') : t('apiMcp.newToken')"
      size="md"
      :prevent-close="creating"
      @update:model-value="(v) => (v ? null : closeCreate())"
    >
      <!-- Step 1: form -->
      <!-- ⚠ novalidate: the boxes are marked `required` for the star and for
           assistive tech, but the checking is ours (said in the panel's
           language, inside the dialog). Without it the browser intercepts
           Enter / a submit button with its own bubble, in the BROWSER's
           language, and our check never runs (seen in the RC re-test,
           2026-09-21: an empty New webhook save showed no message of ours). -->
      <form v-if="!createdToken" class="space-y-3" novalidate @submit.prevent="submitCreate">
        <Input v-model="newLabel" :label="t('apiMcp.fields.label')" :placeholder="t('apiMcp.fields.labelPlaceholder')" required />
        <div>
          <p class="label-base mb-1">
            {{ t('apiMcp.fields.scopes') }}
            <span class="text-rose-500" aria-hidden="true">*</span>
          </p>
          <div class="grid grid-cols-2 gap-2" role="group" :aria-label="t('apiMcp.fields.scopes')">
            <label
              v-for="s in SCOPES"
              :key="s"
              class="flex items-start gap-2 rounded-md border border-zinc-200 dark:border-zinc-700 p-2 cursor-pointer hover:bg-zinc-50 dark:hover:bg-zinc-800/50"
            >
              <input type="checkbox" v-model="newScopes[s]" class="mt-0.5" :data-testid="`ai-token-scope-${s}`" />
              <span>
                <span class="text-sm font-medium">{{ scopeName(s) }}</span>
                <span class="ms-1 text-xs font-mono text-zinc-500">{{ s }}</span>
                <span class="block text-xs text-zinc-500">{{ t(`apiMcp.scopeDesc.${s}` as any) }}</span>
              </span>
            </label>
          </div>
          <p
            :class="noScope ? 'error-text mt-1' : 'help-text mt-1'"
            data-testid="ai-token-scopes-required"
          >{{ t('apiMcp.fields.scopesHint') }}</p>
        </div>

        <!-- Root confinement (optional) -->
        <div>
          <p class="label-base mb-1">{{ t('apiMcp.fields.root') }}</p>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
            <Select v-model="newRootStorage" :options="storageOptions" />
            <Input
              v-model="newRootPath"
              :placeholder="t('apiMcp.fields.rootPathPlaceholder')"
              :disabled="!newRootStorage"
              monospace
            />
          </div>
          <p class="help-text mt-1">{{ t('apiMcp.fields.rootHint') }}</p>
        </div>

        <Input
          v-model="newUsernames"
          :label="t('apiMcp.fields.usernames')"
          :placeholder="t('apiMcp.fields.usernamesPlaceholder')"
          :hint="t('apiMcp.fields.usernamesHint')"
          monospace
        />

        <Input
          v-model.number="newExpiry"
          type="number"
          :min="0"
          :label="t('apiMcp.fields.expiry')"
          :hint="t('apiMcp.fields.expiryHint')"
        />
        <p v-if="createFailure && !noScope" class="error-text" role="alert" data-testid="ai-token-create-error">{{ createFailure }}</p>
        <!-- ⚠ Enter in a box submits: the form's visible buttons sit in the
             dialog footer, OUTSIDE this <form>, and a form with more than one
             field and no submit button of its own ignores Enter (implicit
             submission needs one). Visually hidden, not display:none — some
             engines skip a display:none default button. RC re-test,
             2026-09-21: Enter in the Add user e-mail box did nothing. -->
        <button type="submit" class="sr-only" tabindex="-1" aria-hidden="true" data-testid="ai-token-create-submit">{{ t('common.create') }}</button>
      </form>

      <!-- Step 2: reveal -->
      <div v-else class="space-y-3">
        <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('apiMcp.tokenOnce') }}</p>
        <div class="flex items-center gap-2">
          <code
            class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-sm font-mono break-all"
          >{{ createdToken }}</code>
          <CopyButton :value="createdToken" />
        </div>
        <div>
          <p class="text-xs text-zinc-500 mb-1">{{ t('apiMcp.addToClaude') }}</p>
          <div class="flex items-start gap-2">
            <code
              class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-xs font-mono break-all"
            >{{ claudeSnippet }}</code>
            <CopyButton :value="claudeSnippet" />
          </div>
        </div>
      </div>

      <template #footer>
        <template v-if="!createdToken">
          <Button variant="ghost" @click="closeCreate">{{ t('common.cancel') }}</Button>
          <Button :loading="creating" :disabled="!newLabel.trim() || noScope" data-testid="ai-token-create" @click="submitCreate">
            {{ t('common.create') }}
          </Button>
        </template>
        <Button v-else @click="closeCreate">{{ t('common.close') }}</Button>
      </template>
    </Modal>

    <!-- Edit modal (label + usernames; the credential is immutable) -->
    <Modal
      :model-value="showEdit !== null"
      :title="t('apiMcp.editToken')"
      size="sm"
      :prevent-close="savingEdit"
      @update:model-value="(v) => (v ? null : (showEdit = null))"
    >
      <form class="space-y-3" @submit.prevent="submitEdit">
        <Input v-model="editLabel" :label="t('apiMcp.fields.label')" />
        <Input
          v-model="editUsernames"
          :label="t('apiMcp.fields.usernames')"
          :placeholder="t('apiMcp.fields.usernamesPlaceholder')"
          :hint="t('apiMcp.fields.usernamesHint')"
          monospace
        />
      </form>
      <template #footer>
        <Button variant="ghost" @click="showEdit = null">{{ t('common.cancel') }}</Button>
        <Button :loading="savingEdit" @click="submitEdit">{{ t('common.save') }}</Button>
      </template>
    </Modal>

    <!-- Delete modal -->
    <Modal
      :model-value="showDelete !== null"
      :title="t('common.delete')"
      size="sm"
      @update:model-value="(v) => (v ? null : (showDelete = null))"
    >
      <p class="text-sm">{{ t('apiMcp.deleteConfirm', { label: showDelete?.label || showDelete?.id }) }}</p>
      <template #footer>
        <Button variant="ghost" @click="showDelete = null">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="deleting" @click="confirmDelete">
          {{ t('common.yesDelete') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
