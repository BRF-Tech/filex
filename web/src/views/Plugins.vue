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
import {
  ArrowUpFromLine,
  Blocks,
  Plus,
  RefreshCcw,
  RotateCcw,
  ShieldCheck,
  Trash2,
  TriangleAlert,
  Upload,
} from 'lucide-vue-next';

import {
  PluginsApi,
  rolledBackPlugin,
  type ConformanceMode,
  type Plugin,
  type PluginProbe,
  type PluginState,
  type ProbeStatus,
} from '@/api/plugins';
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
const requiresSignature = ref(false);
const conformanceMode = ref<ConformanceMode>('enforce');

const showForm = ref(false);
const source = ref<'file' | 'url' | 'remote'>('file');
const formName = ref('');
const formFile = ref<File | null>(null);
const formUrl = ref('');
const formSha = ref('');
const formAddress = ref('');
const formToken = ref('');
const formSignature = ref('');
const saving = ref(false);

const reportOf = ref<Plugin | null>(null);

const upgradeOf = ref<Plugin | null>(null);
const upgradeFile = ref<File | null>(null);
const upgradeSignature = ref('');
const upgrading = ref(false);
const upgradeFailure = ref('');
/** What the rollback left running, shown next to the failure that caused it. */
const upgradeRestored = ref<Plugin | null>(null);

async function load() {
  loading.value = true;
  forbidden.value = false;
  disabledMsg.value = '';
  try {
    const res = await PluginsApi.list();
    items.value = res.plugins;
    pluginDir.value = res.dir;
    requiresSignature.value = res.requires_signature;
    conformanceMode.value = res.conformance_mode;
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
  formSignature.value = '';
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
      await PluginsApi.upload(name, formFile.value, formSignature.value.trim());
    } else if (source.value === 'url') {
      await PluginsApi.fromUrl(name, formUrl.value.trim(), formSha.value.trim(), formSignature.value.trim());
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

function openUpgrade(p: Plugin) {
  upgradeOf.value = p;
  upgradeFile.value = null;
  upgradeSignature.value = '';
  upgradeFailure.value = '';
  upgradeRestored.value = null;
}

function onUpgradeFile(e: Event) {
  upgradeFile.value = (e.target as HTMLInputElement).files?.[0] ?? null;
}

async function doUpgrade() {
  const p = upgradeOf.value;
  if (!p) return;
  if (!upgradeFile.value) {
    toast.error(t('plugins.upgrade.errFile'));
    return;
  }
  upgrading.value = true;
  upgradeFailure.value = '';
  try {
    const updated = await PluginsApi.upgrade(p.id, upgradeFile.value, upgradeSignature.value.trim());
    Object.assign(p, updated);
    toast.success(t('plugins.upgrade.done', { name: p.name }));
    upgradeOf.value = null;
    await load();
  } catch (e: unknown) {
    // A rejected upgrade is not a lost plugin: the server restored the
    // previous binary and sent back what is running now. Say both, and keep
    // the modal open — the message is what tells the operator which build to
    // fix, and a toast is gone before it can be read twice.
    upgradeFailure.value = extractError(e, 'Upgrade failed');
    upgradeRestored.value = rolledBackPlugin(e);
    await load();
  } finally {
    upgrading.value = false;
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
  // presign and multipart were probed by conformance long before this list
  // mentioned them, which made a report show a probe for a capability the
  // row claimed nothing about.
  if (c.presign) out.push(t('plugins.caps.presign'));
  if (c.multipart) out.push(t('plugins.caps.multipart'));
  return out;
}

const anyRunning = computed(() => items.value.some((p) => p.state === 'running'));

// ── Conformance: did the plugin do what it said it does? ────────────────────

function probeCounts(p: Plugin) {
  const r = p.conformance?.results ?? [];
  return {
    pass: r.filter((x) => x.status === 'pass').length,
    fail: r.filter((x) => x.status === 'fail').length,
    skip: r.filter((x) => x.status === 'skip').length,
  };
}

/**
 * Three outcomes, three tones — and "never probed" is amber, not red: a
 * plugin that ships no self-test has not failed anything, it has simply not
 * been asked, and colouring that like a failure would teach operators to
 * ignore the colour.
 */
function conformanceTone(p: Plugin): 'emerald' | 'amber' | 'rose' {
  if (!p.conformance) return 'amber';
  return p.conformance.verified ? 'emerald' : 'rose';
}

function conformanceLabel(p: Plugin): string {
  if (!p.conformance) return t('plugins.conformance.unverified');
  if (p.conformance.verified) return t('plugins.conformance.verified');
  return t('plugins.conformance.failed', { count: probeCounts(p).fail });
}

function probeTone(status: ProbeStatus): 'emerald' | 'rose' | 'zinc' {
  if (status === 'pass') return 'emerald';
  return status === 'fail' ? 'rose' : 'zinc';
}

/**
 * Failures first. The server sorts them that way on its full path but returns
 * early — unsorted — when a plugin cannot even be listed, which is exactly
 * the report whose failure must not be buried at the bottom.
 */
function sortedProbes(p: Plugin): PluginProbe[] {
  const rank: Record<ProbeStatus, number> = { fail: 0, pass: 1, skip: 2 };
  return [...(p.conformance?.results ?? [])].sort((a, b) => rank[a.status] - rank[b.status]);
}

/** How long one probe took, for the report modal. */
function probeTook(probe: PluginProbe): string {
  return t('plugins.conformance.took', { ms: probe.took_ms });
}

function scratchLabel(p: Plugin): string {
  const s = p.conformance?.scratch;
  return s ? t(`plugins.conformance.scratch.${s}`) : '';
}

// ── Load: what the plugin is doing, and who it is turning away ──────────────

/** Named up front so a saturated plugin is not something you scroll to find. */
const rejecting = computed(() => items.value.filter((p) => p.load.rejected > 0));
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
      <!-- A saturated plugin is an error every one of its users is already
           meeting; it does not wait to be found in a column. -->
      <div
        v-if="rejecting.length"
        class="flex items-start gap-2 rounded-xl border border-rose-200 bg-rose-50 p-3 text-sm text-rose-900 dark:border-rose-900/50 dark:bg-rose-950/40 dark:text-rose-200"
        data-testid="plugins-rejecting"
      >
        <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" />
        <span>{{ t('plugins.load.rejectingBanner', { names: rejecting.map((p) => p.name).join(', ') }) }}</span>
      </div>

      <div class="overflow-x-auto rounded-xl border border-zinc-200 dark:border-zinc-800">
        <table class="w-full text-sm">
          <thead class="bg-zinc-50 text-xs uppercase text-zinc-500 dark:bg-zinc-900 dark:text-zinc-400">
            <tr>
              <th class="px-3 py-2 text-left">{{ t('common.name') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.driver') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.state') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.capabilities') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.conformance') }}</th>
              <th class="px-3 py-2 text-left">{{ t('plugins.fields.load') }}</th>
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
                <button
                  v-if="p.conformance"
                  type="button"
                  class="cursor-pointer"
                  :data-testid="`plugin-conformance-${p.name}`"
                  @click="reportOf = p"
                >
                  <Badge :tone="conformanceTone(p)" dot>{{ conformanceLabel(p) }}</Badge>
                </button>
                <Badge
                  v-else
                  :tone="conformanceTone(p)"
                  dot
                  :title="t('plugins.conformance.unverifiedHint')"
                  :data-testid="`plugin-conformance-${p.name}`"
                >
                  {{ conformanceLabel(p) }}
                </Badge>
              </td>
              <td class="px-3 py-2" :data-testid="`plugin-load-${p.name}`">
                <div class="whitespace-nowrap text-[11px] text-zinc-500">
                  {{ t('plugins.load.inFlight', { current: p.load.in_flight, max: p.load.max_in_flight }) }}
                </div>
                <Badge
                  v-if="p.load.rejected > 0"
                  tone="rose"
                  size="xs"
                  :title="t('plugins.load.rejectedTitle')"
                  :data-testid="`plugin-rejected-${p.name}`"
                >
                  {{ t('plugins.load.rejected', { count: p.load.rejected }) }}
                </Badge>
                <div
                  v-else-if="p.load.waited > 0"
                  class="whitespace-nowrap text-[11px] text-amber-600 dark:text-amber-400"
                  :title="t('plugins.load.waitedTitle')"
                >
                  {{ t('plugins.load.waited', { count: p.load.waited }) }}
                </div>
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
                    v-if="p.kind === 'binary'"
                    size="xs"
                    variant="outline"
                    :data-testid="`plugin-upgrade-${p.name}`"
                    @click="openUpgrade(p)"
                  >
                    <ArrowUpFromLine class="h-3.5 w-3.5" />
                    {{ t('plugins.upgrade.action') }}
                  </Button>
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
              <td colspan="9" class="px-3 py-8 text-center text-zinc-500 dark:text-zinc-400">
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

        <template v-if="requiresSignature && source !== 'remote'">
          <Input v-model="formSignature" :label="t('plugins.signature.label')" data-testid="plugin-signature" />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.signature.required') }}</p>
        </template>

        <p class="rounded-lg bg-amber-50 p-3 text-xs text-amber-900 dark:bg-amber-950/40 dark:text-amber-200">
          {{ t('plugins.trustWarning') }}
        </p>

        <!-- Under warn/off nothing stops a plugin that fails its own claims,
             so the operator learns it here rather than from a broken storage. -->
        <p
          v-if="conformanceMode !== 'enforce'"
          class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200"
          data-testid="plugins-conformance-mode"
        >
          {{ t(`plugins.conformance.mode.${conformanceMode}`) }}
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

    <Modal
      :model-value="!!upgradeOf"
      :title="upgradeOf ? t('plugins.upgrade.title', { name: upgradeOf.name }) : ''"
      @update:model-value="upgradeOf = null"
    >
      <form v-if="upgradeOf" class="space-y-4" @submit.prevent="doUpgrade">
        <p class="text-xs text-zinc-500">{{ t('plugins.upgrade.hint') }}</p>

        <label class="block text-sm font-medium text-zinc-800 dark:text-zinc-100">
          {{ t('plugins.fields.binary') }}
        </label>
        <input
          type="file"
          class="block w-full text-sm text-zinc-600 file:mr-3 file:rounded-lg file:border-0 file:bg-zinc-100 file:px-3 file:py-1.5 file:text-sm dark:text-zinc-300 dark:file:bg-zinc-800"
          data-testid="plugin-upgrade-file"
          @change="onUpgradeFile"
        />

        <template v-if="requiresSignature">
          <Input
            v-model="upgradeSignature"
            :label="t('plugins.signature.label')"
            data-testid="plugin-upgrade-signature"
          />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.signature.required') }}</p>
        </template>

        <div
          v-if="upgradeFailure"
          class="space-y-1 rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200"
          data-testid="plugin-upgrade-failed"
        >
          <p class="font-medium">{{ t('plugins.upgrade.rollback') }}</p>
          <p class="whitespace-pre-wrap break-words">{{ upgradeFailure }}</p>
          <p v-if="upgradeRestored">
            {{
              t('plugins.upgrade.restored', {
                state: t(`plugins.state.${upgradeRestored.state}`),
                binary: upgradeRestored.binary || '—',
              })
            }}
          </p>
        </div>

        <div class="flex justify-end gap-2">
          <Button type="button" size="sm" variant="ghost" @click="upgradeOf = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" size="sm" variant="primary" :loading="upgrading" data-testid="plugin-upgrade-submit">
            <ArrowUpFromLine class="h-4 w-4" />
            {{ t('plugins.upgrade.action') }}
          </Button>
        </div>
      </form>
    </Modal>

    <Modal
      :model-value="!!reportOf"
      :title="reportOf ? t('plugins.conformance.reportTitle', { name: reportOf.name }) : ''"
      size="lg"
      @update:model-value="reportOf = null"
    >
      <div v-if="reportOf?.conformance" class="space-y-3" data-testid="plugin-report">
        <div class="flex flex-wrap items-center gap-2">
          <ShieldCheck class="h-4 w-4 text-zinc-400" />
          <Badge :tone="conformanceTone(reportOf)" dot>{{ conformanceLabel(reportOf) }}</Badge>
          <span class="text-xs text-zinc-500">{{ t('plugins.conformance.summary', probeCounts(reportOf)) }}</span>
        </div>

        <p class="text-xs text-zinc-500">
          {{ t('plugins.conformance.ranAt', { when: formatDate(reportOf.conformance.ran_at, locale) }) }} ·
          {{ t('plugins.conformance.scratchLabel', { where: scratchLabel(reportOf) }) }}
        </p>

        <p
          v-if="!reportOf.conformance.verified"
          class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200"
        >
          {{ t('plugins.conformance.refusedNote') }}
        </p>

        <ul class="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          <li v-for="probe in sortedProbes(reportOf)" :key="probe.name" class="p-3" :data-testid="`probe-${probe.name}`">
            <div class="flex items-center justify-between gap-2">
              <span class="font-mono text-xs">{{ probe.name }}</span>
              <span class="flex items-center gap-2">
                <span class="text-[11px] text-zinc-500">{{ probeTook(probe) }}</span>
                <Badge :tone="probeTone(probe.status)" size="xs">
                  {{ t(`plugins.conformance.result.${probe.status}`) }}
                </Badge>
              </span>
            </div>
            <!-- Whole, wrapped, never truncated: for a failure this text is
                 what tells the plugin's author what to fix. -->
            <p
              v-if="probe.detail"
              class="mt-1 whitespace-pre-wrap break-words text-xs"
              :class="probe.status === 'fail' ? 'text-rose-600 dark:text-rose-400' : 'text-zinc-500'"
            >
              {{ probe.detail }}
            </p>
          </li>
        </ul>

        <div class="flex justify-end">
          <Button type="button" size="sm" variant="ghost" @click="reportOf = null">{{ t('common.close') }}</Button>
        </div>
      </div>
    </Modal>
  </section>
</template>
