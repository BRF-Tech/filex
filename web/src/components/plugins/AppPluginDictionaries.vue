<script setup lang="ts">
/**
 * An app's Dictionaries section (filex 0.57): the spell-check and
 * hyphenation dictionaries an administrator adds to the app, for languages
 * its package does not bring (the office editor carries filex's interface
 * languages). Drawn for an app that names the kind in its manifest
 * (`ui.extras.dictionaries`) and reads its own package (`ui:package-fetch`).
 * When the app's release offers a catalog of them, every language of it is
 * a row, installed with one click (filex downloads its files and checks
 * each against the catalog's sha256); an upload stays for any other.
 *
 * The files are kept outside the app's package and served under its address
 * (`extra/dictionaries/<lang>/…`, backend wasmplugin/extras.go); where each
 * one goes is lib/appDictionaries. One row per language; removing a row
 * removes its files. Every refusal is the server's sentence.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { Plus } from 'lucide-vue-next';

import { AppPluginsApi, type AppExtraCatalogItem, type AppExtrasAnswer } from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { formatBytes, formatDate } from '@/lib/format';
import {
  DICTIONARY_LANG,
  dictionaryLanguageName,
  dictionaryPath,
  dictionaryRows,
  dictionaryTableRows,
  type DictionaryPart,
  type DictionaryTableRow,
} from '@/lib/appDictionaries';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';

const props = defineProps<{ pluginId: number }>();

const { t, locale } = useI18n();
const toast = useToastStore();

const ans = ref<AppExtrasAnswer | null>(null);
const loadError = ref('');
/** The languages the app's release offers (its catalog), when it offers one. */
const offered = ref<AppExtraCatalogItem[]>([]);
const catalogError = ref('');

async function load() {
  loadError.value = '';
  try {
    ans.value = await AppPluginsApi.extras(props.pluginId);
  } catch (e: unknown) {
    loadError.value = extractError(e, t('errors.loadFailed'));
    return;
  }
  catalogError.value = '';
  if (!ans.value.catalogs?.includes('dictionaries')) {
    offered.value = [];
    return;
  }
  try {
    offered.value = (await AppPluginsApi.extraCatalog(props.pluginId, 'dictionaries')).items;
  } catch (e: unknown) {
    offered.value = [];
    catalogError.value = extractError(e, t('errors.loadFailed'));
  }
}

watch(() => props.pluginId, () => void load(), { immediate: true });

const rows = computed<DictionaryTableRow[]>(() => dictionaryTableRows(dictionaryRows(ans.value?.files ?? []), offered.value));
const hasCatalog = computed(() => !!ans.value?.catalogs?.includes('dictionaries'));

const columns = computed<DataColumn<DictionaryTableRow>[]>(() => [
  {
    id: 'lang',
    label: t('appPlugins.detail.dictionaries.column.lang'),
    sortable: true,
    width: 220,
    sortValue: (r) => dictionaryLanguageName(r.lang, locale.value),
  },
  { id: 'status', label: t('appPlugins.detail.dictionaries.column.status'), width: 140, sortValue: (r) => statusOf(r) },
  { id: 'parts', label: t('appPlugins.detail.dictionaries.column.parts'), width: 220 },
  { id: 'size', label: t('appPlugins.detail.dictionaries.column.size'), sortable: true, width: 110, sortValue: (r) => r.size || r.offeredSize },
  { id: 'modified', label: t('appPlugins.detail.dictionaries.column.modified'), sortable: true, width: 160, sortValue: (r) => r.modified },
]);

function partsOf(r: DictionaryTableRow): string {
  return r.parts.map((p) => t(`appPlugins.detail.dictionaries.part.${p}`)).join(', ');
}

/** installed | partial | available: the row's word in the Status column. */
function statusOf(r: DictionaryTableRow): 'installed' | 'partial' | 'available' {
  if (r.offered && r.partial) return 'partial';
  if (r.paths.length && (!r.offered || r.installed)) return 'installed';
  return r.offered ? 'available' : 'installed';
}

/* ── install from the release's catalog ─────────────────────────────── */

const installing = ref('');

async function install(row: DictionaryTableRow) {
  installing.value = row.lang;
  try {
    await AppPluginsApi.installExtraItem(props.pluginId, 'dictionaries', row.lang);
    toast.success(t('appPlugins.detail.dictionaries.added', { lang: dictionaryLanguageName(row.lang, locale.value) }));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  } finally {
    installing.value = '';
    await load();
  }
}

/* ── add ─────────────────────────────────────────────────────────────── */

const adding = ref(false);
const saving = ref(false);
const lang = ref('');
const langError = ref<string | null>(null);
const picked = ref<Partial<Record<DictionaryPart, File>>>({});
const formError = ref('');

function openAdd() {
  lang.value = '';
  langError.value = null;
  picked.value = {};
  formError.value = '';
  adding.value = true;
}

function onPick(part: DictionaryPart, ev: Event) {
  const f = (ev.target as HTMLInputElement).files?.[0];
  picked.value = { ...picked.value, [part]: f };
}

async function save() {
  const code = lang.value.trim();
  langError.value = DICTIONARY_LANG.test(code) ? null : t('appPlugins.detail.dictionaries.langInvalid');
  formError.value = picked.value.aff && picked.value.dic ? '' : t('appPlugins.detail.dictionaries.needBoth');
  if (langError.value || formError.value) return;
  saving.value = true;
  try {
    // The spelling rules first: a word list without them checks nothing.
    for (const part of ['aff', 'dic', 'hyph'] as const) {
      const f = picked.value[part];
      if (f) await AppPluginsApi.putExtra(props.pluginId, dictionaryPath(code, part), f);
    }
    adding.value = false;
    toast.success(t('appPlugins.detail.dictionaries.added', { lang: dictionaryLanguageName(code, locale.value) }));
  } catch (e: unknown) {
    formError.value = extractError(e, t('errors.actionFailed'));
  } finally {
    saving.value = false;
    await load();
  }
}

/* ── remove ──────────────────────────────────────────────────────────── */

const confirmRemove = ref<DictionaryTableRow | null>(null);
const removing = ref('');

function actions(row: DictionaryTableRow): ContextAction[] {
  const out: ContextAction[] = [];
  if (row.offered && statusOf(row) !== 'installed') {
    out.push({ key: 'install', label: t('appPlugins.detail.dictionaries.install'), icon: 'download', disabled: installing.value !== '' || !ans.value?.read });
  }
  if (row.paths.length) {
    out.push({ key: 'remove', label: t('appPlugins.detail.dictionaries.remove'), icon: 'delete', danger: true, disabled: removing.value === row.lang });
  }
  return out;
}

function onRowAction(key: string, row: DictionaryTableRow) {
  if (key === 'install') void install(row);
  else if (key === 'remove') confirmRemove.value = row;
}

async function doRemove(row: DictionaryTableRow) {
  confirmRemove.value = null;
  removing.value = row.lang;
  try {
    for (const p of row.paths) await AppPluginsApi.deleteExtra(props.pluginId, p);
    toast.success(t('appPlugins.detail.dictionaries.removed', { lang: dictionaryLanguageName(row.lang, locale.value) }));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  } finally {
    removing.value = '';
    await load();
  }
}
</script>

<template>
  <section class="space-y-3" data-testid="app-dictionaries">
    <div class="flex flex-wrap items-baseline justify-between gap-2">
      <h3 class="text-sm font-semibold">{{ t('appPlugins.detail.dictionaries.title') }}</h3>
      <Button
        v-if="ans?.read"
        size="sm"
        variant="outline"
        data-testid="app-dictionaries-add"
        @click="openAdd"
      >
        <Plus class="h-4 w-4" />
        {{ t('appPlugins.detail.dictionaries.add') }}
      </Button>
    </div>
    <p v-if="loadError" class="text-sm text-rose-700 dark:text-rose-300" role="alert">{{ loadError }}</p>
    <template v-else-if="ans">
      <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('appPlugins.detail.dictionaries.desc') }}</p>
      <p class="text-xs text-zinc-500">{{ t(hasCatalog ? 'appPlugins.detail.dictionaries.sourceCatalog' : 'appPlugins.detail.dictionaries.source') }}</p>
      <p v-if="catalogError" class="text-sm text-amber-700 dark:text-amber-300" role="alert" data-testid="app-dictionaries-catalog-error">
        {{ t('appPlugins.detail.dictionaries.catalogUnread', { why: catalogError }) }}
      </p>
      <p v-if="!ans.read" class="text-sm text-zinc-500" data-testid="app-dictionaries-unread">
        {{ t('appPlugins.detail.dictionaries.notRead') }}
      </p>
      <DataTable
        table-id="admin.apps.detail.dictionaries"
        :columns="columns"
        :rows="rows"
        row-key="lang"
        :empty="t('appPlugins.detail.dictionaries.empty')"
        :row-actions="(row: DictionaryTableRow) => actions(row)"
        :row-actions-test-id="(row: DictionaryTableRow) => `app-dictionary-actions-${row.lang}`"
        @row-action="onRowAction"
      >
        <template #cell-lang="{ row }">
          <div :data-testid="`app-dictionary-${row.lang}`">
            <div class="font-medium">{{ dictionaryLanguageName(row.lang, locale) }}</div>
            <div class="font-mono text-[11px] text-zinc-500">{{ row.lang }}</div>
          </div>
        </template>
        <template #cell-status="{ row }">
          <span class="text-xs" :data-testid="`app-dictionary-status-${row.lang}`">
            {{ installing === row.lang ? t('appPlugins.detail.dictionaries.installing') : t(`appPlugins.detail.dictionaries.status.${statusOf(row)}`) }}
          </span>
        </template>
        <template #cell-parts="{ row }">
          <span class="text-xs">{{ partsOf(row) }}</span>
        </template>
        <template #cell-size="{ row }">
          <span class="text-xs">{{ formatBytes(row.size || row.offeredSize, locale) }}</span>
        </template>
        <template #cell-modified="{ row }">
          <span class="text-xs">{{ row.modified ? formatDate(row.modified, locale) : '' }}</span>
        </template>
      </DataTable>
      <p class="text-[11px] text-zinc-500" data-testid="app-dictionaries-used">
        {{ t('appPlugins.detail.dictionaries.used', { used: formatBytes(ans.bytes, locale), max: formatBytes(ans.max_bytes, locale) }) }}
      </p>
    </template>

    <Modal
      :model-value="adding"
      :title="t('appPlugins.detail.dictionaries.addTitle')"
      size="sm"
      @update:model-value="(v: boolean) => !v && !saving && (adding = false)"
    >
      <form class="space-y-3" @submit.prevent="save">
        <div data-testid="app-dictionaries-lang">
          <Input
            v-model="lang"
            name="app-dictionary-lang"
            monospace
            :label="t('appPlugins.detail.dictionaries.lang')"
            placeholder="pl_PL"
            :hint="t('appPlugins.detail.dictionaries.langHint')"
            :error="langError"
          />
        </div>
        <div v-for="part in (['aff', 'dic', 'hyph'] as const)" :key="part" class="space-y-1">
          <label :for="`app-dictionary-${part}`" class="block text-sm font-medium text-zinc-800 dark:text-zinc-100">
            {{ t(`appPlugins.detail.dictionaries.${part}`) }}
          </label>
          <input
            :id="`app-dictionary-${part}`"
            type="file"
            :accept="part === 'aff' ? '.aff' : '.dic'"
            class="block w-full text-sm text-zinc-600 file:me-3 file:rounded-lg file:border-0 file:bg-zinc-100 file:px-3 file:py-1.5 file:text-sm dark:text-zinc-300 dark:file:bg-zinc-800"
            :data-testid="`app-dictionaries-${part}`"
            @change="onPick(part, $event)"
          />
        </div>
        <p v-if="formError" class="text-sm text-rose-700 dark:text-rose-300" role="alert" data-testid="app-dictionaries-error">{{ formError }}</p>
      </form>
      <template #footer>
        <Button size="sm" variant="outline" :disabled="saving" @click="adding = false">{{ t('common.cancel') }}</Button>
        <Button size="sm" variant="primary" :loading="saving" data-testid="app-dictionaries-save" @click="save">
          {{ t('appPlugins.detail.dictionaries.add') }}
        </Button>
      </template>
    </Modal>

    <!-- Confirmed, never one click: the language stops being checked for
         everybody who uses the app. -->
    <Modal
      :model-value="confirmRemove !== null"
      :title="t('appPlugins.detail.dictionaries.confirmTitle')"
      size="sm"
      @update:model-value="(v: boolean) => !v && (confirmRemove = null)"
    >
      <p v-if="confirmRemove" class="text-sm">
        {{ t('appPlugins.detail.dictionaries.confirmText', { lang: dictionaryLanguageName(confirmRemove.lang, locale) }) }}
      </p>
      <template #footer>
        <Button size="sm" variant="outline" @click="confirmRemove = null">{{ t('common.cancel') }}</Button>
        <Button
          size="sm"
          variant="danger"
          data-testid="app-dictionaries-remove-confirm"
          @click="confirmRemove && doRemove(confirmRemove)"
        >
          {{ t('appPlugins.detail.dictionaries.remove') }}
        </Button>
      </template>
    </Modal>
  </section>
</template>
