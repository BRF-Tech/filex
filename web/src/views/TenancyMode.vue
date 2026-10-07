<script setup lang="ts">
/**
 * Admin → Multi-tenant mode (task #167, backend handlers/tenancy_admin.go,
 * internal/tenancy). The platform operator's switch: one instance serving
 * several tenants, or one organization.
 *
 * What it promises, each from the server's answer, never guessed here:
 *   - the mode IN FORCE and the one the next start will run with; a change is
 *     saved for the next start and the page says "restart filex" until then;
 *   - FILEX_MULTI_TENANT (or the config file) pins it: the switch is shown
 *     locked, with the variable to change instead;
 *   - turning it off while tenants exist is maintenance mode: the dialog says
 *     how many tenants, what happens to them and that nothing is deleted, and
 *     asks for that number before it saves.
 *
 * ⚠ This is the one page about tenants that a single-tenant install shows: it
 * is where the mode is turned on. Every other one follows `useTenancy()`.
 */
import { computed, onMounted, ref } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Building2, Lock, RotateCw, TriangleAlert } from 'lucide-vue-next';

import { TenancyApi, tenancyRefusal, type TenancyState } from '@/api/tenancy';
import { extractError } from '@/api/client';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { useToastStore } from '@/stores/toast';
import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Toggle from '@/components/ui/Toggle.vue';

const { t } = useI18n();
const toast = useToastStore();
const caps = useCapabilitiesStore();

const state = ref<TenancyState | null>(null);
const loading = ref(true);
const saving = ref(false);
const error = ref('');

async function load() {
  loading.value = true;
  error.value = '';
  try {
    state.value = await TenancyApi.get();
  } catch (e) {
    error.value = extractError(e, t('errors.generic'));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

const readOnly = computed(() => caps.demoReadOnly || state.value?.locked === true);

/** Why the switch cannot be changed here, in the page's words. */
const lockedText = computed(() => {
  const s = state.value;
  if (!s?.locked) return '';
  return s.locked_by === 'config_file'
    ? t('tenancy.lockedFile', { key: s.variable || 'multi_tenant' })
    : t('tenancy.lockedEnv', { variable: s.variable || 'FILEX_MULTI_TENANT' });
});

// ── turning it off while tenants exist: said, and confirmed by their number ──
const confirming = ref(false);
const typed = ref('');
const confirmMatches = computed(() => !!state.value && typed.value.trim() === String(state.value.tenants));

async function save(enabled: boolean, confirm?: string) {
  saving.value = true;
  error.value = '';
  try {
    state.value = await TenancyApi.set(enabled, confirm);
    confirming.value = false;
    toast.success(state.value.restart_required ? t('tenancy.savedRestart') : t('tenancy.saved'));
  } catch (e) {
    const r = tenancyRefusal(e);
    if (r?.code === 'confirm_required') {
      // The count changed under the page (a tenant was added): ask again with it.
      if (state.value) state.value = { ...state.value, tenants: r.tenants };
      typed.value = '';
      confirming.value = true;
    } else if (r?.code === 'tenancy_locked') {
      await load();
    } else {
      error.value = extractError(e, t('errors.generic'));
    }
  } finally {
    saving.value = false;
  }
}

function onToggle(enabled: boolean) {
  const s = state.value;
  if (!s || readOnly.value || saving.value) return;
  if (!enabled && s.tenants > 0) {
    typed.value = '';
    confirming.value = true;
    return;
  }
  void save(enabled);
}

function confirmOff() {
  if (!state.value || !confirmMatches.value) return;
  void save(false, typed.value.trim());
}

const onOff = (on: boolean) => (on ? t('tenancy.on') : t('tenancy.off'));
</script>

<template>
  <div class="max-w-3xl space-y-4" data-testid="tenancy-page">
    <div class="flex items-center gap-2">
      <Building2 class="h-6 w-6 text-brand-600 dark:text-brand-400" aria-hidden="true" />
      <div>
        <h1 class="text-xl font-semibold">{{ t('tenancy.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tenancy.subtitle') }}</p>
      </div>
    </div>

    <div v-if="loading" class="flex justify-center py-16"><Spinner /></div>
    <div v-else-if="!state" class="rounded-lg border border-rose-200 bg-rose-50 p-4 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-200" role="alert">
      {{ error }}
    </div>
    <template v-else>
      <div v-if="error" class="rounded-lg border border-rose-200 bg-rose-50 p-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-200" role="alert">
        {{ error }}
      </div>

      <!-- Saved, not yet in force: the running server keeps its mode until
           filex is restarted (backend internal/tenancy says why). -->
      <div
        v-if="state.restart_required"
        class="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-200"
        role="status"
        data-testid="tenancy-restart"
      >
        <p class="flex items-center gap-1.5 font-medium">
          <RotateCw class="h-4 w-4 shrink-0" aria-hidden="true" /> {{ t('tenancy.restartTitle') }}
        </p>
        <p class="mt-1">{{ state.next_start ? t('tenancy.restartToOn') : t('tenancy.restartToOff') }}</p>
      </div>

      <section class="card card-body space-y-4">
        <Toggle
          :model-value="state.next_start"
          :label="t('tenancy.switchLabel')"
          :description="t('tenancy.switchHelp')"
          :disabled="readOnly || saving"
          name="tenancy-switch"
          data-testid="tenancy-switch"
          @update:model-value="onToggle"
        />

        <p
          v-if="state.locked"
          class="flex items-start gap-1.5 text-sm text-zinc-600 dark:text-zinc-300"
          data-testid="tenancy-locked"
        >
          <Lock class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <span>{{ lockedText }}</span>
        </p>

        <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
          <div>
            <dt class="text-zinc-500 dark:text-zinc-400">{{ t('tenancy.inForce') }}</dt>
            <dd class="mt-0.5">
              <Badge :tone="state.in_force ? 'emerald' : 'zinc'" data-testid="tenancy-in-force">{{ onOff(state.in_force) }}</Badge>
            </dd>
          </div>
          <div v-if="state.in_force || state.tenants > 0">
            <dt class="text-zinc-500 dark:text-zinc-400">{{ t('tenancy.tenantsLabel') }}</dt>
            <dd class="mt-0.5" data-testid="tenancy-tenants">{{ state.tenants }}</dd>
          </div>
        </dl>

        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ state.next_start ? t('tenancy.whatOff') : t('tenancy.whatOn') }}</p>

        <p v-if="state.in_force && caps.data.caller_admin === true">
          <RouterLink :to="{ name: 'tenants' }" class="text-sm font-medium text-brand-700 hover:underline dark:text-brand-300" data-testid="tenancy-open-tenants">
            {{ t('tenancy.openTenants') }}
          </RouterLink>
        </p>
      </section>
    </template>

    <Modal v-model="confirming" :title="t('tenancy.confirmTitle')" size="md">
      <div v-if="state && confirming" class="space-y-3 text-sm" data-testid="tenancy-confirm">
        <p class="flex items-start gap-1.5 font-medium text-amber-800 dark:text-amber-200">
          <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <span>{{ t('tenancy.confirmCount', { n: state.tenants }, state.tenants) }}</span>
        </p>
        <ul class="list-disc space-y-1 ps-5 text-zinc-700 dark:text-zinc-300">
          <li>{{ t('tenancy.confirmMaintenance') }}</li>
          <li>{{ t('tenancy.confirmKept') }}</li>
          <li>{{ t('tenancy.confirmRestart') }}</li>
        </ul>
        <Input
          v-model="typed"
          :label="t('tenancy.confirmLabel', { n: state.tenants })"
          inputmode="numeric"
          autocomplete="off"
          name="tenancy-confirm"
          data-testid="tenancy-confirm-input"
          @enter="confirmOff"
        />
      </div>
      <template #footer>
        <Button variant="ghost" @click="confirming = false">{{ t('common.cancel') }}</Button>
        <Button
          variant="danger"
          :loading="saving"
          :disabled="!confirmMatches"
          data-testid="tenancy-confirm-off"
          @click="confirmOff"
        >{{ t('tenancy.confirmAction') }}</Button>
      </template>
    </Modal>
  </div>
</template>
