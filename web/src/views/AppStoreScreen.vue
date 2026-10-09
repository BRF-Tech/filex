<script setup lang="ts">
/**
 * The store screen (#162) - "Uygulama mağazası" / "App store": the catalog
 * of the trusted stores an administrator turned on for this person, drawn by
 * filex itself, and a Request that lands on the Install requests list an
 * administrator decides (docs/APP-PLUGINS.md → The store screen).
 *
 * ⚠⚠ The owner's decision (2026-10-06): filex's OWN screen, over the API -
 * no iframe. The catalog is read and verified on the SERVER from the store's
 * signed index (GET /api/app-store/catalog); this page never talks to the
 * store, and its icons come through filex too, so the Content-Security-
 * Policy is the panel's own.
 *
 * ⚠ Nothing here installs. A request is a person's words for an
 * administrator; the approval asks the store for a fresh install link and the
 * install goes through the administrator's review, with its SHA-256 and
 * permission checks. The person sees their requests below, and what became
 * of each.
 *
 * Lives OUTSIDE the AdminLayout block (router/index.ts), like My shares: the
 * panel is admin-only, and this screen is for whoever the administrator chose.
 * Both lists are the explorer's table (DataTable); the row's verbs live behind
 * its one pinned Actions control.
 *
 * Two tabs (#215): Apps, and Storage - the store's storage plugins, shown
 * only where this server runs storage plugins (the server leaves them out
 * otherwise). What each row is here (installed, an older version installed,
 * asked for, nothing) is the server's `state`, and a storage plugin's
 * sentences (whether there is a build for this server, what the store's
 * checks proved, what a storage plugin is) are the server's too.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { ArrowLeft, RefreshCcw, Store, TriangleAlert } from 'lucide-vue-next';
import { ChoiceButtons, DataTable, pluginLabelOf, type ChoiceOption, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { StoreScreenApi, storeRefusal, type CatalogApp, type StoreCatalog } from '@/api/appStore';
import type { PluginRequest, PluginRequestStatus } from '@/api/pluginRequests';
import type { AppPluginPermissionReview } from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import { storeSentence } from '@/lib/storeRefusal';
import { useToastStore } from '@/stores/toast';

import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';
import Select from '@/components/ui/Select.vue';
import Textarea from '@/components/ui/Textarea.vue';
import AppPluginPermissionList from '@/components/plugins/AppPluginPermissionList.vue';

type CatalogRow = CatalogApp & { permission_rows?: AppPluginPermissionReview[] };

const { t, locale } = useI18n();
const router = useRouter();
const toast = useToastStore();

const visible = ref<boolean | null>(null);
const stores = ref<string[]>([]);
const store = ref('');
const catalog = ref<StoreCatalog | null>(null);
const loading = ref(false);
const failure = ref('');
const query = ref('');
const requests = ref<PluginRequest[]>([]);

async function start() {
  try {
    const s = await StoreScreenApi.status();
    visible.value = s.visible;
    stores.value = s.stores;
    if (!s.visible) return;
    if (!stores.value.includes(store.value)) store.value = stores.value[0] ?? '';
    await Promise.all([loadCatalog(), loadRequests()]);
  } catch (e: unknown) {
    visible.value = false;
    failure.value = extractError(e, t('errors.loadFailed'));
  }
}
onMounted(start);

async function loadCatalog() {
  if (!store.value) return;
  loading.value = true;
  failure.value = '';
  try {
    catalog.value = await StoreScreenApi.catalog(store.value);
  } catch (e: unknown) {
    catalog.value = null;
    const r = storeRefusal(e);
    failure.value = (r && storeSentence(r)) || extractError(e, t('errors.loadFailed'));
  } finally {
    loading.value = false;
  }
}

async function loadRequests() {
  try {
    requests.value = await StoreScreenApi.requests();
  } catch {
    requests.value = [];
  }
}

watch(store, () => void loadCatalog());

/** Two to four stores: a strip; more: the panel's list control (never a native select). */
const storeChoices = computed<ChoiceOption[]>(() => stores.value.map((o) => ({ value: o, label: o.replace(/^https?:\/\//, '') })));
const storeOptions = computed(() => stores.value.map((o) => ({ value: o, label: o })));

function labelOf(a: { label?: Record<string, string> | null; name: string }): string {
  return pluginLabelOf(a.label ?? undefined, locale.value) || a.name;
}
function summaryOf(a: CatalogApp): string {
  return pluginLabelOf(a.summary ?? undefined, locale.value);
}

/** What the row is here: the server's answer (installed, update, pending,
 *  none) - never worked out again in the page. */
function stateOf(a: CatalogApp): CatalogApp['state'] {
  return a.state ?? 'none';
}

/** Whether this person may ask for it: nothing of it here or asked for, and
 *  - a storage plugin - a build the store pinned for this server. */
function askable(a: CatalogApp): boolean {
  const st = stateOf(a);
  if (st !== 'none' && st !== 'update') return false;
  return !isStorage(a) || a.storage?.for_here === true;
}

const shown = computed<CatalogRow[]>(() => {
  const all = ((catalog.value?.apps ?? []) as CatalogRow[]).filter((a) => (kindTab.value === 'storage') === isStorage(a));
  const q = query.value.trim().toLocaleLowerCase(locale.value);
  if (!q) return all;
  return all.filter((a) =>
    [a.name, labelOf(a), summaryOf(a), a.publisher].some((s) => s.toLocaleLowerCase(locale.value).includes(q)),
  );
});

const columns = computed<DataColumn<CatalogRow>[]>(() => [
  { id: 'app', label: t(kindTab.value === 'storage' ? 'storeScreen.fields.plugin' : 'storeScreen.fields.app'), sortable: true, width: 320, sortValue: (r) => labelOf(r) },
  { id: 'version', label: t('storeScreen.fields.version'), sortable: true, width: 110, format: (r) => r.version },
  kindTab.value === 'storage'
    ? { id: 'checks', label: t('storeScreen.fields.checks'), sortable: true, width: 220, sortValue: (r) => r.conformance?.passed ?? -1 }
    : { id: 'permissions', label: t('storeScreen.fields.permissions'), sortable: true, width: 140, sortValue: (r) => r.permissions.length },
  { id: 'state', label: t('storeScreen.fields.state'), sortable: true, width: 150, sortValue: (r) => stateOf(r) },
]);

function requestLabel(r: CatalogApp): string {
  return t(isStorage(r) ? 'storeScreen.requestStorage' : 'storeScreen.request');
}

function rowActions(r: CatalogRow): ContextAction[] {
  const out: ContextAction[] = [{ key: 'details', label: t('storeScreen.details'), icon: 'details' }];
  if (askable(r)) out.unshift({ key: 'request', label: requestLabel(r) });
  return out;
}

// ── Apps / Storage (#215) ──────────────────────────────────────────────

type KindTab = 'apps' | 'storage';
const kindTab = ref<KindTab>('apps');
const isStorage = (a: CatalogApp) => a.kind === 'storage';
/** The Storage tab is offered when the catalog holds a storage plugin. */
const hasStorage = computed(() => (catalog.value?.apps ?? []).some(isStorage));
const kindChoices = computed<ChoiceOption[]>(() => [
  { value: 'apps', label: t('storeScreen.tabs.apps') },
  { value: 'storage', label: t('storeScreen.tabs.storage') },
]);
watch(hasStorage, (has) => {
  if (!has) kindTab.value = 'apps';
});

const iconFailed = ref<Record<string, boolean>>({});
function iconUrl(a: CatalogApp): string {
  return a.icon && !iconFailed.value[a.name] ? StoreScreenApi.iconUrl(store.value, a.icon) : '';
}

/**
 * "Back to files". ⚠ In the desktop app this page is a window of its own (the
 * store window, desktop main.ts openStoreWindow) with the window bar's
 * controls (`window.filexWin`, preload-editor): there "back" is closing it -
 * the files are the window behind it, and the SPA's home inside this one
 * would be a second explorer. The one platform difference is the window, not
 * the screen.
 */
function back() {
  const win = (window as unknown as { filexWin?: { close?: () => unknown } }).filexWin;
  if (win?.close) {
    void win.close();
    return;
  }
  void router.push({ name: 'home' });
}

// ── The request ────────────────────────────────────────────────────────

const open = ref<CatalogRow | null>(null);
const asking = ref(false);
const reason = ref('');
const sending = ref(false);
const sendError = ref('');

function onRowAction(key: string, r: CatalogRow) {
  open.value = r;
  asking.value = key === 'request';
  reason.value = '';
  sendError.value = '';
}

async function send() {
  const a = open.value;
  if (!a || !reason.value.trim()) return;
  sending.value = true;
  sendError.value = '';
  try {
    const got = await StoreScreenApi.request(store.value, a.name, reason.value.trim());
    toast.success(got.created ? t('storeScreen.sent', { name: labelOf(a) }) : t('storeScreen.alreadySent', { name: labelOf(a) }));
    open.value = null;
    // The row's state is the server's: read again, with the request.
    await Promise.all([loadRequests(), loadCatalog()]);
  } catch (e: unknown) {
    // The server's sentence for every refusal (a request too many, a
    // version installed already: server.plugin_request.*), as it came.
    const r = storeRefusal(e);
    sendError.value = (r && storeSentence(r)) || extractError(e, t('errors.generic'));
  } finally {
    sending.value = false;
  }
}

// ── What became of the requests ────────────────────────────────────────

function statusTone(s: PluginRequestStatus): 'amber' | 'emerald' | 'rose' | 'zinc' {
  if (s === 'pending') return 'amber';
  if (s === 'approved') return 'emerald';
  if (s === 'rejected' || s === 'superseded') return 'rose';
  return 'zinc';
}

const requestColumns = computed<DataColumn<PluginRequest>[]>(() => [
  { id: 'app', label: t('storeScreen.fields.app'), sortable: true, width: 240, sortValue: (r) => labelOf(r) },
  { id: 'version', label: t('storeScreen.fields.version'), width: 110, format: (r) => r.version || '-' },
  {
    id: 'created',
    label: t('storeScreen.fields.requested'),
    sortable: true,
    sortDir: 'desc',
    width: 200,
    sortValue: (r) => r.created_at,
    format: (r) => formatDate(r.created_at, locale.value),
  },
  { id: 'status', label: t('storeScreen.fields.status'), sortable: true, width: 140, sortValue: (r) => r.status },
  { id: 'note', label: t('storeScreen.fields.note'), width: 240 },
]);
</script>

<template>
  <section class="store-screen mx-auto w-full max-w-5xl space-y-4 p-4 sm:p-6" data-testid="store-screen">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <button
          type="button"
          class="inline-flex items-center gap-1 text-sm text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100"
          data-testid="store-screen-back"
          @click="back"
        >
          <ArrowLeft class="h-4 w-4" aria-hidden="true" />
          {{ t('storeScreen.back') }}
        </button>
        <h1 class="mt-2 flex items-center gap-2 text-2xl font-semibold text-zinc-900 dark:text-zinc-100" data-testid="store-screen-title">
          <Store class="h-6 w-6 text-zinc-500 dark:text-zinc-400" aria-hidden="true" />
          {{ t('storeScreen.title') }}
        </h1>
        <p class="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{{ t('storeScreen.subtitle') }}</p>
      </div>
      <Button v-if="visible" variant="outline" size="sm" :loading="loading" @click="start">
        <RefreshCcw class="h-4 w-4" aria-hidden="true" />
        {{ t('common.refresh') }}
      </Button>
    </header>

    <p v-if="visible === false" class="rounded-lg border border-zinc-200 p-4 text-sm text-zinc-600 dark:border-zinc-800 dark:text-zinc-400" data-testid="store-screen-hidden">
      {{ t('storeScreen.hidden') }}
    </p>

    <template v-else-if="visible">
      <ChoiceButtons
        v-if="hasStorage"
        :model-value="kindTab"
        :options="kindChoices"
        segmented
        :aria-label="t('storeScreen.tabs.label')"
        testid-prefix="store-screen-kind"
        @update:model-value="(v: string | string[]) => (kindTab = (Array.isArray(v) ? v[0] : v) as KindTab)"
      />
      <div class="flex flex-wrap items-end gap-3">
        <div v-if="stores.length > 1" class="min-w-0">
          <ChoiceButtons
            v-if="stores.length <= 4"
            :model-value="store"
            :options="storeChoices"
            segmented
            :aria-label="t('storeScreen.store')"
            testid-prefix="store-screen-store"
            @update:model-value="(v: string | string[]) => (store = Array.isArray(v) ? v[0] : v)"
          />
          <Select
            v-else
            :model-value="store"
            :options="storeOptions"
            :label="t('storeScreen.store')"
            name="store-screen-store"
            @update:model-value="(v: string | number | null) => (store = String(v ?? ''))"
          />
        </div>
        <div class="w-full max-w-xs">
          <Input v-model="query" type="search" name="store-screen-search" :placeholder="t('storeScreen.search')" size="sm" />
        </div>
      </div>

      <p
        v-if="catalog?.stale"
        class="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
        data-testid="store-screen-stale"
      >
        <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        {{ t('storeScreen.stale', { when: formatDate(catalog.fetched_at, locale) }) }}
      </p>
      <p v-if="failure" class="rounded-lg bg-rose-50 p-3 text-sm text-rose-900 dark:bg-rose-950/40 dark:text-rose-200" role="alert" data-testid="store-screen-error">
        {{ failure }}
      </p>

      <p
        v-if="kindTab === 'storage' && catalog?.storage_note"
        class="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
        data-testid="store-screen-storage-note"
      >
        <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        {{ catalog.storage_note }}
      </p>

      <DataTable
        :key="kindTab"
        :table-id="kindTab === 'storage' ? 'app.store.storage' : 'app.store.catalog'"
        :columns="columns"
        :rows="shown"
        row-key="name"
        :loading="loading"
        :empty="query ? t('storeScreen.noMatch') : t(kindTab === 'storage' ? 'storeScreen.storageEmpty' : 'storeScreen.empty')"
        :row-attrs="(r: CatalogRow) => ({ 'data-testid': `store-app-${r.name}` })"
        :row-actions="(r: CatalogRow) => rowActions(r)"
        :row-actions-test-id="(r: CatalogRow) => `store-app-actions-${r.name}`"
        @row-action="(key: string, r: CatalogRow) => onRowAction(key, r)"
      >
        <template #cell-app="{ row }">
          <div class="flex min-w-0 items-center gap-3 py-1">
            <img
              v-if="iconUrl(row)"
              :src="iconUrl(row)"
              alt=""
              class="h-8 w-8 shrink-0 rounded-md"
              loading="lazy"
              @error="iconFailed = { ...iconFailed, [row.name]: true }"
            />
            <span
              v-else
              class="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-zinc-100 text-sm font-semibold text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300"
              aria-hidden="true"
            >{{ labelOf(row).slice(0, 1).toLocaleUpperCase(locale) }}</span>
            <div class="min-w-0">
              <div class="truncate font-medium" :title="labelOf(row)">{{ labelOf(row) }}</div>
              <div class="truncate text-[11px] text-zinc-500" :title="summaryOf(row)">
                {{ summaryOf(row) || row.publisher }}
                <template v-if="summaryOf(row)"> · {{ row.publisher }}</template>
              </div>
            </div>
          </div>
        </template>
        <template #cell-permissions="{ row }">
          <span :title="row.permissions.join(', ')">
            {{ t('appPlugins.permissionsCount', { count: row.permissions.length }, row.permissions.length) }}
          </span>
        </template>
        <template #cell-checks="{ row }">
          <span
            class="tbl-clamp"
            :class="row.storage?.for_here === false ? 'text-rose-600 dark:text-rose-400' : ''"
            :title="row.storage?.summary || ''"
            :data-testid="`store-app-checks-${row.name}`"
          >{{ row.storage?.summary || '-' }}</span>
        </template>
        <template #cell-state="{ row }">
          <Badge v-if="stateOf(row) === 'installed'" tone="emerald" size="xs" dot>{{ t('storeScreen.state.installed') }}</Badge>
          <Badge v-else-if="stateOf(row) === 'pending'" tone="amber" size="xs" dot>{{ t('storeScreen.state.pending') }}</Badge>
          <Badge v-else-if="row.installed_version" tone="sky" size="xs">{{ t('storeScreen.state.update', { version: row.installed_version }) }}</Badge>
          <span v-else class="text-xs text-zinc-400">-</span>
        </template>
      </DataTable>

      <div class="space-y-2">
        <h2 class="text-base font-semibold">{{ t('storeScreen.mine') }}</h2>
        <DataTable
          table-id="app.store.requests"
          :columns="requestColumns"
          :rows="requests"
          row-key="id"
          :empty="t('storeScreen.noRequests')"
          :row-attrs="(r: PluginRequest) => ({ 'data-testid': `store-request-${r.id}` })"
        >
          <template #cell-app="{ row }">
            <span class="truncate" :title="row.name">{{ labelOf(row) }}</span>
          </template>
          <template #cell-status="{ row }">
            <Badge :tone="statusTone(row.status)" dot :data-testid="`store-request-status-${row.id}`">
              {{ t(`pluginRequests.status.${row.status}`) }}
            </Badge>
          </template>
          <template #cell-note="{ row }">
            <span class="line-clamp-2 break-words text-xs" :title="row.decision_note || ''">{{ row.decision_note || '-' }}</span>
          </template>
        </DataTable>
      </div>
    </template>

    <Modal
      :model-value="!!open"
      :title="open ? labelOf(open) : ''"
      size="lg"
      :prevent-close="sending"
      @update:model-value="(v: boolean) => { if (!v) open = null; }"
    >
      <div v-if="open" class="space-y-4" data-testid="store-app-detail">
        <p v-if="summaryOf(open)" class="text-sm">{{ summaryOf(open) }}</p>
        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm">
          <dt class="text-zinc-500">{{ t('storeScreen.fields.version') }}</dt>
          <dd>{{ open.version }}</dd>
          <dt class="text-zinc-500">{{ t('storeScreen.fields.publisher') }}</dt>
          <dd>
            {{ open.publisher }}
            <Badge v-if="open.publisher_verified" tone="sky" size="xs">{{ t('storeScreen.verified') }}</Badge>
          </dd>
          <dt class="text-zinc-500">{{ t('storeScreen.fields.store') }}</dt>
          <dd class="break-all font-mono text-xs">{{ store }}</dd>
        </dl>
        <template v-if="isStorage(open)">
          <p class="text-sm" data-testid="store-app-checks">{{ open.storage?.summary }}</p>
          <ul v-if="open.storage?.capabilities?.length" class="flex flex-wrap gap-1" data-testid="store-app-capabilities">
            <li v-for="c in open.storage.capabilities" :key="c.id">
              <Badge tone="zinc" size="xs">{{ c.label }}</Badge>
            </li>
          </ul>
          <p
            v-if="catalog?.storage_note"
            class="rounded-lg border border-amber-200 bg-amber-50 p-2 text-xs text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
          >
            {{ catalog.storage_note }}
          </p>
        </template>
        <AppPluginPermissionList v-else :permissions="open.permission_rows ?? open.permissions.map((id) => ({ id, label: id }))" />
        <template v-if="asking">
          <p class="rounded-lg border border-zinc-200 bg-zinc-50 p-2 text-xs text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300">
            {{ t('storeScreen.requestIntro') }}
          </p>
          <Textarea
            v-model="reason"
            name="store-request-reason"
            :label="t('storeScreen.reason')"
            :placeholder="t('storeScreen.reasonPlaceholder')"
            :rows="3"
            data-testid="store-request-reason"
          />
          <p v-if="sendError" class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200" role="alert" data-testid="store-request-error">
            {{ sendError }}
          </p>
        </template>
      </div>
      <template #footer>
        <Button variant="ghost" :disabled="sending" @click="open = null">{{ asking ? t('common.cancel') : t('common.close') }}</Button>
        <Button
          v-if="asking"
          variant="primary"
          :disabled="!reason.trim()"
          :loading="sending"
          data-testid="store-request-send"
          @click="send"
        >
          {{ t('storeScreen.send') }}
        </Button>
        <Button
          v-else-if="open && askable(open)"
          variant="primary"
          data-testid="store-request-open"
          @click="asking = true"
        >
          {{ requestLabel(open) }}
        </Button>
      </template>
    </Modal>
  </section>
</template>

<style scoped>
/* The desktop's store window draws its own bar over the page and says how
 * tall it is in core's --fe-overlay-top (desktop main.ts docChromeScript); the
 * page starts under it. The web keeps the default, 0. */
.store-screen {
  margin-top: var(--fe-overlay-top, 0px);
}
</style>
