<script setup lang="ts">
/**
 * AppPluginPage — one installed app, as a page of the admin panel.
 *
 * ⚠⚠ It was a dialog: Plugins → Apps → a row's "Details" opened a modal
 * holding settings, the menu actions table, the schedule table, the locks
 * table and a live log — a whole page in a popup that scrolled inside itself
 * (release-candidate sweep, 2026-09-21). The owner's rule for exactly this,
 * said about the Signatures screen: a screen that big is its own page with
 * sections, not a popup. So it has an address (`/admin/plugins/apps/<name>`),
 * the browser's Back works, and a link to it can be sent.
 *
 * The sections themselves are AppPluginDetail's, unchanged in what they
 * hold; this file is the page around them — the heading, the way back, and
 * the app looked up by the name in the address.
 *
 * ⚠ By NAME, not by install id: the name is what the manifest and every
 * other screen call the app, it survives an upgrade (the id does too, but a
 * remove-and-reinstall mints a new one), and `/plugins/apps/sign` says what
 * it is. The detail endpoint takes the id, which the list row carries.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { ArrowLeft, Blocks, RotateCcw, WandSparkles } from 'lucide-vue-next';

import { AppPluginsApi, type AppPlugin, type AppPluginState, type AppSetup } from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { pluginLabelOf, invalidatePluginActions } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';
import AppPluginDetail from '@/components/plugins/AppPluginDetail.vue';
import AppPluginLanguages from '@/components/plugins/AppPluginLanguages.vue';
import AppPluginLicense from '@/components/plugins/AppPluginLicense.vue';
import AppSetupWizard from '@/components/plugins/AppSetupWizard.vue';
import AppEraseDialog from '@/components/plugins/AppEraseDialog.vue';

const { t, locale } = useI18n();
const route = useRoute();
const router = useRouter();
const toast = useToastStore();

const app = ref<AppPlugin | null>(null);
const engineNames = ref<Record<string, string>>({});
const loading = ref(true);
const missing = ref(false);

const name = computed(() => String(route.params.name ?? ''));

/** The app's setup (filex 0.60): whether it has one, and whether it is done.
 *  null for an app with none, or on a server that has no setup route. */
const setup = ref<AppSetup | null>(null);
const setupOpen = ref(false);
const reconfigureOpen = ref(false);

async function loadSetup(p: AppPlugin | null) {
  setup.value = null;
  if (!p) return;
  try {
    const s = await AppPluginsApi.setup(p.id);
    setup.value = s.needed ? s : null;
  } catch {
    /* no setup route, or an app without one: nothing to offer */
  }
}

async function load() {
  loading.value = true;
  try {
    const res = await AppPluginsApi.list();
    const found = res.plugins.find((p) => p.name === name.value) ?? null;
    engineNames.value = res.runtime.engine_names;
    app.value = found;
    missing.value = !found;
    void loadSetup(found);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

onMounted(load);
watch(name, () => void load());

/** Something the explorer's menu depends on changed: its cache is stale. */
async function onChanged() {
  invalidatePluginActions();
  await load();
}

const title = computed(() => (app.value ? pluginLabelOf(app.value.label, locale.value) || app.value.name : name.value));

function stateTone(state: AppPluginState): 'emerald' | 'rose' | 'amber' | 'zinc' {
  if (state === 'running') return 'emerald';
  if (state === 'failed' || state === 'refused' || state === 'unlicensed') return 'rose';
  if (state === 'setup') return 'amber';
  return 'zinc';
}

function stateLabel(state: AppPluginState): string {
  return ['running', 'disabled', 'refused', 'failed', 'unlicensed', 'setup'].includes(state) ? t(`appPlugins.state.${state}`) : state;
}

/** Reconfigure erased the data; the wizard opens again at once. */
function onReconfigured(p: AppPlugin | null) {
  if (p) app.value = p;
  setupOpen.value = true;
  void onChanged();
}

function onSetupDone(p: AppPlugin) {
  toast.success(t('appPlugins.setup.doneToast', { name: pluginLabelOf(p.label, locale.value) || p.name }));
  app.value = p;
  void onChanged();
}

/** Back to the list it was opened from — the Apps tab, not the first tab. */
function back() {
  void router.push({ name: 'plugins', query: { tab: 'apps' } });
}
</script>

<template>
  <div class="space-y-5" data-testid="app-plugin-page">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div class="flex min-w-0 items-start gap-3">
        <Blocks class="mt-1 h-6 w-6 shrink-0 text-brand-600 dark:text-brand-400" />
        <div class="min-w-0">
          <h1 class="flex flex-wrap items-center gap-2 text-xl font-semibold" data-testid="app-plugin-page-title">
            {{ title }}
            <Badge v-if="app" :tone="stateTone(app.state)" dot>{{ stateLabel(app.state) }}</Badge>
            <Badge v-if="app?.kind === 'language_pack'" tone="brand" data-testid="app-plugin-page-kind">
              {{ t('appPlugins.kind.languagePack') }}
            </Badge>
          </h1>
          <p v-if="app" class="text-sm text-zinc-500">
            <span class="font-mono">{{ app.name }}</span> · v{{ app.version }}
          </p>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button
          v-if="app && app.state === 'setup'"
          variant="primary"
          size="sm"
          data-testid="app-plugin-page-setup"
          @click="setupOpen = true"
        >
          <WandSparkles class="h-4 w-4" />
          {{ t('appPlugins.actions.setup') }}
        </Button>
        <Button
          v-else-if="app && setup?.done"
          variant="outline"
          size="sm"
          data-testid="app-plugin-page-reconfigure"
          @click="reconfigureOpen = true"
        >
          <RotateCcw class="h-4 w-4" />
          {{ t('appPlugins.actions.reconfigure') }}
        </Button>
        <Button variant="ghost" size="sm" data-testid="app-plugin-page-back" @click="back">
          <ArrowLeft class="h-4 w-4" />
          {{ t('appPlugins.page.back') }}
        </Button>
      </div>
    </div>

    <p
      v-if="app && app.state === 'setup'"
      class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
      data-testid="app-plugin-page-needs-setup"
    >
      {{ t('appPlugins.setup.needsSetup') }}
    </p>

    <div v-if="loading && !app" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div v-else-if="missing" class="card card-body text-sm text-zinc-600 dark:text-zinc-400" data-testid="app-plugin-page-missing">
      {{ t('appPlugins.page.missing', { name }) }}
    </div>

    <!-- The languages the app adds to filex itself, with each one's coverage
         of THIS version's catalogue — the same component and the same rows
         (the server's LanguageRows) as the Apps list and the install review,
         so the page an administrator opens for a pack says what the list
         said. feat/043-lang wrote it for the drawer this page replaced. -->
    <section
      v-if="app && !missing && app.languages?.length"
      class="card card-body space-y-2"
      data-testid="app-plugin-page-languages"
    >
      <h2 class="text-sm font-semibold">{{ t('appPlugins.lang.heading') }}</h2>
      <AppPluginLanguages :languages="app.languages" />
    </section>

    <!-- A paid app's license (0.52.0): draws nothing for a free app. -->
    <AppPluginLicense v-if="app && !missing" :plugin-id="app.id" @changed="onChanged" />

    <AppPluginDetail v-if="app && !missing" :plugin="app" :engine-names="engineNames" @changed="onChanged" />

    <AppSetupWizard v-model="setupOpen" :plugin="app" @done="onSetupDone" />
    <AppEraseDialog v-model="reconfigureOpen" :plugin="app" mode="reconfigure" @done="onReconfigured" />
  </div>
</template>
