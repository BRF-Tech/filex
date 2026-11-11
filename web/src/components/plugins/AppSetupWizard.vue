<script setup lang="ts">
/**
 * AppSetupWizard — setting an app up, once, before it runs (filex 0.60,
 * docs/APP-PLUGINS.md → Setting an app up). Generic: any app whose manifest
 * has a `setup` block or public routes is installed in the state `setup` and
 * opens this wizard; Reconfigure (AppEraseDialog) opens it again after the
 * app's data is erased.
 *
 * The steps, in this order, each one only when the app has it:
 *   1. the app's own setup screen (its `setup` view - AppSetupAppStep);
 *   2. Addresses - where each of its public routes is served;
 *   3. Who may use it - its permissions per role, group and person;
 *   4. Finish - the app runs.
 *
 * ⚠ One primary button per step (the surfaces' own rule, docs/APP-PLUGINS-API
 * → Fields): Next, or Finish on the last step. While the app's own screen is
 * open its primary button is the screen's, so the wizard's Next waits until
 * the app's part is done.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { AlertTriangle } from 'lucide-vue-next';
import { pluginLabelOf } from '@brftech/filex-core';

import { AppPluginsApi, setupRefusalOf, type AppPlugin, type AppSetup, type AppSetupRoute } from '@/api/appPlugins';
import { extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';
import AppSetupAddresses from './AppSetupAddresses.vue';
import AppSetupAppStep from './AppSetupAppStep.vue';
import AppSetupPeople from './AppSetupPeople.vue';

const props = defineProps<{
  modelValue: boolean;
  plugin: AppPlugin | null;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void;
  /** The setup is finished: the app as it runs now. */
  (e: 'done', plugin: AppPlugin): void;
}>();

const { t, locale } = useI18n();

type Step = 'app' | 'addresses' | 'people' | 'finish';

const setup = ref<AppSetup | null>(null);
const loading = ref(false);
const loadError = ref('');
const index = ref(0);
const appDone = ref(false);
const finishing = ref(false);
const finishError = ref('');

const steps = computed<Step[]>(() => {
  const s = setup.value;
  if (!s) return [];
  const out: Step[] = [];
  if (s.app_step) out.push('app');
  if (s.routes.length) out.push('addresses');
  if (s.permissions.length) out.push('people');
  out.push('finish');
  return out;
});
const step = computed<Step | null>(() => steps.value[index.value] ?? null);
const label = computed(() => (props.plugin ? pluginLabelOf(props.plugin.label, locale.value) || props.plugin.name : ''));

/** Every route has an address that is on: the server's `setup_no_base` rule. */
const addressesReady = computed(() => (setup.value?.routes ?? []).every((r) => r.bases.some((b) => b.enabled)));

const canNext = computed(() => {
  if (step.value === 'app') return appDone.value;
  if (step.value === 'addresses') return addressesReady.value;
  return true;
});

async function load() {
  const p = props.plugin;
  if (!p) return;
  loading.value = true;
  loadError.value = '';
  finishError.value = '';
  index.value = 0;
  try {
    setup.value = await AppPluginsApi.setup(p.id);
    appDone.value = setup.value.app_step?.done === true;
  } catch (e: unknown) {
    loadError.value = extractError(e, t('errors.loadFailed'));
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.modelValue, props.plugin?.id] as const,
  ([open]) => {
    if (open) void load();
  },
  { immediate: true },
);

function onRoutes(routes: AppSetupRoute[]) {
  if (setup.value) setup.value = { ...setup.value, routes };
}

function next() {
  if (!canNext.value) return;
  if (index.value < steps.value.length - 1) index.value++;
}
function back() {
  if (index.value > 0) index.value--;
}

async function finish() {
  const p = props.plugin;
  if (!p || finishing.value) return;
  finishing.value = true;
  finishError.value = '';
  try {
    const running = await AppPluginsApi.completeSetup(p.id);
    emit('done', running);
    emit('update:modelValue', false);
  } catch (e: unknown) {
    finishError.value = setupRefusalOf(e)?.message || extractError(e, t('errors.updateFailed'));
  } finally {
    finishing.value = false;
  }
}

function close() {
  if (finishing.value) return;
  emit('update:modelValue', false);
}
</script>

<template>
  <Modal
    :model-value="modelValue"
    :title="t('appPlugins.setup.title', { name: label })"
    size="xl"
    :close-on-backdrop="false"
    :prevent-close="finishing"
    @update:model-value="(v: boolean) => !v && close()"
  >
    <div class="space-y-4" data-testid="app-setup-wizard">
      <div v-if="loading" class="text-center text-zinc-500"><Spinner /></div>
      <p v-else-if="loadError" class="text-sm text-rose-700 dark:text-rose-300" role="alert">{{ loadError }}</p>
      <template v-else-if="setup && step">
        <!-- Where the person is: every step by name, the open one marked. -->
        <ol class="flex flex-wrap gap-x-4 gap-y-1 text-xs" data-testid="app-setup-steps">
          <li
            v-for="(s, i) in steps"
            :key="s"
            :class="i === index ? 'font-semibold text-brand-700 dark:text-brand-300' : 'text-zinc-500'"
            :aria-current="i === index ? 'step' : undefined"
            :data-testid="`app-setup-step-${s}`"
          >
            {{ i + 1 }}. {{ t(`appPlugins.setup.step.${s}`) }}
          </li>
        </ol>

        <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t(`appPlugins.setup.lead.${step}`) }}</p>

        <AppSetupAppStep
          v-if="step === 'app' && setup.app_step && plugin"
          :plugin-id="plugin.id"
          :plugin="plugin.name"
          :view="setup.app_step.view"
          :done="appDone"
          @done="appDone = true"
        />

        <AppSetupAddresses
          v-else-if="step === 'addresses' && plugin"
          :plugin-id="plugin.id"
          :routes="setup.routes"
          :own-hosts="setup.own_hosts"
          :multi-tenant="setup.multi_tenant"
          :tenants="setup.tenants ?? []"
          :done="setup.done"
          @update:routes="onRoutes"
        />

        <AppSetupPeople v-else-if="step === 'people'" :permissions="setup.permissions" />

        <div v-else-if="step === 'finish'" class="space-y-3 text-sm" data-testid="app-setup-finish">
          <ul class="list-disc space-y-1 ps-5">
            <li v-for="r in setup.routes" :key="r.id">
              {{ pluginLabelOf(r.label, locale) || r.id }}:
              <span class="font-mono text-xs">{{ r.bases.filter((b) => b.enabled).map((b) => b.url).join(', ') }}</span>
            </li>
          </ul>
          <p>{{ t('appPlugins.setup.finishText') }}</p>
        </div>

        <!-- The app's data is this node's alone: said before it starts keeping any. -->
        <div
          v-if="setup.data?.node_local && step === 'finish'"
          class="flex gap-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
          role="note"
          data-testid="app-setup-node-local"
        >
          <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <p>{{ t('appPlugins.setup.nodeLocal') }}</p>
        </div>

        <p v-if="finishError" class="text-sm text-rose-700 dark:text-rose-300" role="alert" data-testid="app-setup-finish-error">{{ finishError }}</p>
      </template>
    </div>

    <template #footer>
      <Button v-if="index > 0" variant="ghost" size="sm" :disabled="finishing" data-testid="app-setup-back" @click="back">
        {{ t('appPlugins.setup.back') }}
      </Button>
      <Button variant="ghost" size="sm" :disabled="finishing" data-testid="app-setup-later" @click="close">
        {{ t('appPlugins.setup.later') }}
      </Button>
      <Button
        v-if="step && step !== 'finish' && !(step === 'app' && !appDone)"
        variant="primary"
        size="sm"
        :disabled="!canNext"
        data-testid="app-setup-next"
        @click="next"
      >
        {{ t('appPlugins.setup.next') }}
      </Button>
      <Button v-if="step === 'finish'" variant="primary" size="sm" :loading="finishing" data-testid="app-setup-complete" @click="finish">
        {{ t('appPlugins.setup.complete') }}
      </Button>
    </template>
  </Modal>
</template>
