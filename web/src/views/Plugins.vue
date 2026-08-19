<script setup lang="ts">
/**
 * Plugins — storage drivers that live outside the filex binary.
 *
 * What this page is for: somebody wrote a driver for their own system and
 * wants filex to speak it. They install it here; from then on it appears in
 * the ordinary storage picker as `plugin:<driver>` with the config form the
 * plugin itself describes, and behaves like any other storage.
 *
 * ⚠ Instance-wide, not per-tenant: a plugin is a program filex runs (or a
 * service it hands storage credentials to). In multi-tenant mode the server
 * answers 403 to anyone but the platform operator, and this page shows that
 * answer rather than an empty list.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Blocks, Plus, RefreshCcw, RotateCcw, Trash2, Upload } from 'lucide-vue-next';

import { PluginsApi, type Plugin, type PluginState } from '@/api/plugins';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { formatDate } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import Modal from '@/components/ui/Modal.vue';

const { t, locale } = useI18n();
const toast = useToastStore();

const items = ref<Plugin[]>([]);
const pluginDir = ref('');
const loading = ref(false);
const busyId = ref<number | null>(null);
const forbidden = ref(false);
const disabledMsg = ref('');

const showForm = ref(false);
const source = ref<'file' | 'url' | 'remote'>('file');
const formName = ref('');
const formFile = ref<File | null>(null);
const formUrl = ref('');
const formSha = ref('');
const formAddress = ref('');
const formToken = ref('');
const saving = ref(false);

async function load() {
  loading.value = true;
  forbidden.value = false;
  disabledMsg.value = '';
  try {
    const res = await PluginsApi.list();
    items.value = res.plugins;
    pluginDir.value = res.dir;
  } catch (e: unknown) {
    // The two "not a bug" answers get their own screens: a tenant admin is
    // told whose surface this is, and an operator who turned the subsystem
    // off is told which setting did it.
    const err = e as { response?: { status?: number; data?: { error?: string; message?: string } } };
    if (err?.response?.status === 403) {
      forbidden.value = true;
    } else if (err?.response?.status === 503) {
      disabledMsg.value = err.response?.data?.message ?? t('plugins.disabled');
    } else {
      toast.error(extractError(e, 'Failed to load plugins'));
    }
  } finally {
    loading.value = false;
  }
}

onMounted(load);

function openCreate() {
  source.value = 'file';
  formName.value = '';
  formFile.value = null;
  formUrl.value = '';
  formSha.value = '';
  formAddress.value = '';
  formToken.value = '';
  showForm.value = true;
}

function onFile(e: Event) {
  const input = e.target as HTMLInputElement;
  formFile.value = input.files?.[0] ?? null;
  // A plugin's name defaults to its file name, lower-cased and cleaned to the
  // server's rule — one fewer field to fill in, and it can still be edited.
  if (formFile.value && !formName.value) {
    formName.value = formFile.value.name
      .replace(/\.(exe|bin)$/i, '')
      .toLowerCase()
      .replace(/[^a-z0-9_-]+/g, '-')
      .replace(/^[^a-z0-9]+/, '')
      .slice(0, 32);
  }
}

async function save() {
  const name = formName.value.trim();
  if (!name) {
    toast.error(t('plugins.errName'));
    return;
  }
  saving.value = true;
  try {
    if (source.value === 'file') {
      if (!formFile.value) {
        toast.error(t('plugins.errFile'));
        return;
      }
      await PluginsApi.upload(name, formFile.value);
    } else if (source.value === 'url') {
      await PluginsApi.fromUrl(name, formUrl.value.trim(), formSha.value.trim());
    } else {
      await PluginsApi.remote(name, formAddress.value.trim(), formToken.value);
    }
    toast.success(t('plugins.installed', { name }));
    showForm.value = false;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, 'Install failed'));
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled(p: Plugin, enabled: boolean) {
  busyId.value = p.id;
  try {
    const updated = await PluginsApi.setEnabled(p.id, enabled);
    Object.assign(p, updated);
    // Starting is asynchronous: re-read shortly so the row settles on its
    // real state instead of sitting on "starting" until the next refresh.
    window.setTimeout(load, 1200);
  } catch (e: unknown) {
    toast.error(extractError(e, 'Update failed'));
    await load();
  } finally {
    busyId.value = null;
  }
}

async function restart(p: Plugin) {
  busyId.value = p.id;
  try {
    const updated = await PluginsApi.restart(p.id);
    Object.assign(p, updated);
    window.setTimeout(load, 1200);
  } catch (e: unknown) {
    toast.error(extractError(e, 'Restart failed'));
  } finally {
    busyId.value = null;
  }
}

async function remove(p: Plugin) {
  // Say what will stop working, with the number, before it happens.
  const msg = p.in_use > 0
    ? t('plugins.deleteConfirmInUse', { name: p.name, count: p.in_use })
    : t('plugins.deleteConfirm', { name: p.name });
  if (!window.confirm(msg)) return;
  busyId.value = p.id;
  try {
    await PluginsApi.remove(p.id);
    toast.success(t('plugins.deleted', { name: p.name }));
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, 'Delete failed'));
  } finally {
    busyId.value = null;
  }
}

function stateTone(state: PluginState): 'emerald' | 'amber' | 'rose' | 'zinc' {
  switch (state) {
    case 'running':
      return 'emerald';
    case 'starting':
      return 'amber';
    case 'failed':
    case 'refused':
      return 'rose';
    default:
      return 'zinc';
  }
}

/** The capabilities the plugin declared, as short chips. */
function capList(p: Plugin): string[] {
  const c = p.capabilities;
  if (!c) return [];
  const out: string[] = [t('plugins.caps.read')];
  if (c.write) out.push(t('plugins.caps.write'));
  else out.push(t('plugins.caps.readOnly'));
  if (c.range) out.push(t('plugins.caps.range'));
  if (c.move) out.push(t('plugins.caps.move'));
  if (c.copy) out.push(t('plugins.caps.copy'));
  if (c.set_mtime) out.push(t('plugins.caps.mtime'));
  if (c.watch) out.push(t('plugins.caps.watch'));
  return out;
}

const anyRunning = computed(() => items.value.some((p) => p.state === 'running'));
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <Blocks class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <h1 class="text-xl font-semibold">{{ t('plugins.title') }}</h1>
      </div>
      <div v-if="!forbidden && !disabledMsg" class="flex items-center gap-2">
        <Button variant="outline" size="sm" :loading="loading" @click="load">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button variant="primary" size="sm" @click="openCreate">
          <Plus class="h-4 w-4" />
          {{ t('plugins.add') }}
        </Button>
      </div>
    </header>

    <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('plugins.subtitle') }}</p>

    <div
      v-if="forbidden"
      class="rounded-xl border border-zinc-200 bg-white p-6 text-sm text-zinc-600 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400"
      data-testid="plugins-forbidden"
    >
      {{ t('plugins.supertenantOnly') }}
    </div>

    <div
      v-else-if="disabledMsg"
      class="rounded-xl border border-zinc-200 bg-white p-6 text-sm text-zinc-600 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400"
      data-testid="plugins-disabled"
    >
      {{ disabledMsg }}
    </div>

    <template v-else>
      <div class="overflow-x-auto rounded-xl border border-zinc-200 dark:border-zinc-800">
        <table class="w-full text-sm">
          <thead class="bg-zinc-50 text-xs uppercase text-zinc-500 dark:bg-zinc-900 dark:text-zinc-400">
            <tr>
              <th class="px-3 py-2 text-left">{{ t('common.name') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.driver') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.state') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.capabilities') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.inUse') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.enabled') }}</th>
              <th class="px-3 py-2 text-right">{{ t('webhooks.fields.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
            <tr v-for="p in items" :key="p.id" class="bg-white dark:bg-zinc-950" :data-testid="`plugin-${p.name}`">
              <td class="px-3 py-2">
                <div class="font-medium">{{ p.name }}</div>
                <div class="text-[11px] text-zinc-500">
                  {{ p.kind === 'remote' ? p.address : p.binary }}
                  <template v-if="p.version"> · v{{ p.version }}</template>
                </div>
              </td>
              <td class="px-3 py-2">
                <span v-if="p.driver" class="font-mono text-xs">plugin:{{ p.driver }}</span>
                <span v-else class="text-zinc-500">—</span>
                <div v-if="p.label" class="text-[11px] text-zinc-500">{{ p.label }}</div>
              </td>
              <td class="px-3 py-2">
                <Badge :tone="stateTone(p.state)" :title="p.state_error || ''">
                  {{ t(`plugins.state.${p.state}`) }}
                </Badge>
                <div v-if="p.restarts > 0" class="mt-0.5 text-[11px] text-zinc-500">
                  {{ t('plugins.restarts', { count: p.restarts }) }}
                </div>
                <!-- The failure is shown, not hidden behind a tooltip: it is
                     the only thing that tells the operator what to fix. -->
                <div
                  v-if="p.state_error"
                  class="mt-0.5 max-w-xs break-words text-[11px] text-rose-500"
                  data-testid="plugin-error"
                >
                  {{ p.state_error }}
                </div>
              </td>
              <td class="px-3 py-2">
                <span
                  v-for="c in capList(p)"
                  :key="c"
                  class="mr-1 inline-block rounded bg-zinc-100 px-1.5 py-0.5 text-[11px] dark:bg-zinc-800"
                  >{{ c }}</span
                >
                <span v-if="!p.capabilities" class="text-zinc-500">—</span>
              </td>
              <td class="px-3 py-2">
                <span :class="p.in_use > 0 ? 'font-medium' : 'text-zinc-500'">{{ p.in_use }}</span>
              </td>
              <td class="px-3 py-2">
                <Toggle
                  :model-value="p.enabled"
                  :disabled="busyId === p.id"
                  @update:model-value="(v: boolean) => toggleEnabled(p, v)"
                />
              </td>
              <td class="px-3 py-2">
                <div class="flex justify-end gap-1">
                  <Button
                    size="xs"
                    variant="outline"
                    :loading="busyId === p.id"
                    :disabled="!p.enabled"
                    @click="restart(p)"
                  >
                    <RotateCcw class="h-3.5 w-3.5" />
                    {{ t('plugins.restart') }}
                  </Button>
                  <Button size="xs" variant="ghost" :aria-label="t('common.delete')" @click="remove(p)">
                    <Trash2 class="h-3.5 w-3.5 text-rose-500" />
                  </Button>
                </div>
              </td>
            </tr>
            <tr v-if="!items.length && !loading">
              <td colspan="7" class="px-3 py-8 text-center text-zinc-500 dark:text-zinc-400">
                <p>{{ t('plugins.empty') }}</p>
                <p v-if="pluginDir" class="mt-1 font-mono text-[11px]">{{ pluginDir }}</p>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <p v-if="anyRunning" class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('plugins.whereNext') }}</p>
    </template>

    <Modal v-model="showForm" :title="t('plugins.add')" size="lg">
      <form class="space-y-4" @submit.prevent="save">
        <div class="flex gap-2">
          <Button
            v-for="s in (['file', 'url', 'remote'] as const)"
            :key="s"
            type="button"
            size="sm"
            :variant="source === s ? 'primary' : 'outline'"
            :data-testid="`plugin-source-${s}`"
            @click="source = s"
          >
            {{ t(`plugins.source.${s}`) }}
          </Button>
        </div>

        <Input v-model="formName" :label="t('common.name')" placeholder="myfs" />
        <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.nameHint') }}</p>

        <template v-if="source === 'file'">
          <label class="block text-sm font-medium text-zinc-800 dark:text-zinc-100">
            {{ t('plugins.fields.binary') }}
          </label>
          <input
            type="file"
            class="block w-full text-sm text-zinc-600 file:mr-3 file:rounded-lg file:border-0 file:bg-zinc-100 file:px-3 file:py-1.5 file:text-sm dark:text-zinc-300 dark:file:bg-zinc-800"
            data-testid="plugin-file"
            @change="onFile"
          />
          <p class="text-xs text-zinc-500">{{ t('plugins.fileHint') }}</p>
        </template>

        <template v-else-if="source === 'url'">
          <Input v-model="formUrl" label="URL" placeholder="https://example.com/downloads/myfs-linux-amd64" />
          <Input v-model="formSha" label="SHA256" placeholder="a1b2c3…" />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.shaHint') }}</p>
        </template>

        <template v-else>
          <Input v-model="formAddress" :label="t('plugins.fields.address')" placeholder="http://myfs-plugin:8080" />
          <Input v-model="formToken" :label="t('plugins.fields.token')" type="password" />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.remoteHint') }}</p>
        </template>

        <p class="rounded-lg bg-amber-50 p-3 text-xs text-amber-900 dark:bg-amber-950/40 dark:text-amber-200">
          {{ t('plugins.trustWarning') }}
        </p>

        <div class="flex justify-end gap-2">
          <Button type="button" size="sm" variant="ghost" @click="showForm = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" size="sm" variant="primary" :loading="saving" data-testid="plugin-install">
            <Upload class="h-4 w-4" />
            {{ t('plugins.install') }}
          </Button>
        </div>
      </form>
    </Modal>
  </section>
</template>
