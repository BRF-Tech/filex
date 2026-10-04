<script setup lang="ts">
/**
 * EncryptionPolicyCard — the tenant's policy: who may START encrypting
 * (backend internal/e2epolicy Decide asks it right after the platform
 * operator's switch). Off (administrators included), Administrators only,
 * Permitted — whoever holds `files.encrypt` in that folder, which is what
 * every install did before — or Approval: the same, and each encryption is
 * approved here first (EncryptionRequestsPanel).
 *
 * ⚠ Saved with a button, never on change: `off` stops administrators too, and
 * a select that saved as it moved would pass through it on the way to another
 * choice. Whatever is chosen, what is already encrypted keeps working.
 *
 * A tenant the platform operator has switched off may still choose: the policy
 * takes effect when encryption is switched on again, and the card says so
 * rather than hiding the choice.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, TriangleAlert } from 'lucide-vue-next';

import { extractError } from '@/api/client';
import { E2E_POLICIES, E2EPolicyApi, isE2EPolicy, type E2EPolicyState } from '@/api/e2ePolicy';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Select from '@/components/ui/Select.vue';

const props = defineProps<{ state: E2EPolicyState }>();
const emit = defineEmits<{
  /** The server's answer after a save: the page shows exactly what is stored. */
  (e: 'saved', next: E2EPolicyState): void;
}>();

const { t } = useI18n();
const toast = useToastStore();

const choice = ref<string>(props.state.policy);
watch(
  () => props.state.policy,
  (p) => {
    choice.value = p;
  },
);
const saving = ref(false);
const error = ref('');
// A refusal is about the choice that was sent. Once the choice moves, the
// message under it is about something else.
watch(choice, () => {
  error.value = '';
});

const options = computed(() => E2E_POLICIES.map((p) => ({ value: p, label: t(`encryption.policy.options.${p}`) })));
const hint = computed(() => (isE2EPolicy(choice.value) ? t(`encryption.policy.hints.${choice.value}`) : ''));
const scopeLine = computed(() =>
  props.state.scope === 'tenant' && props.state.tenant
    ? t('encryption.policy.scopeTenant', { tenant: props.state.tenant.name })
    : t('encryption.policy.scopeInstance'),
);

async function save(): Promise<void> {
  const policy = choice.value;
  if (!isE2EPolicy(policy)) return;
  saving.value = true;
  error.value = '';
  try {
    const next = await E2EPolicyApi.update(policy);
    toast.success(t('encryption.policy.saved'));
    emit('saved', next);
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.saveFailed'));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section
    class="card card-body space-y-4"
    data-testid="encryption-policy"
  >
    <div>
      <h2 class="font-semibold text-zinc-900 dark:text-zinc-100">
        {{ t('encryption.policy.title') }}
      </h2>
      <p class="text-sm text-zinc-500">
        {{ scopeLine }}
      </p>
    </div>
    <div
      v-if="!state.available"
      class="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
      role="note"
      data-testid="encryption-unavailable"
    >
      <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" />
      <p>{{ t('encryption.policy.unavailable') }}</p>
    </div>
    <Select
      v-model="choice"
      name="encryption-policy"
      :options="options"
      :label="t('encryption.policy.label')"
      :hint="hint"
    />
    <p
      v-if="error"
      class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200"
      role="alert"
    >
      {{ error }}
    </p>
    <div class="flex justify-end">
      <Button
        :loading="saving"
        :disabled="choice === state.policy"
        data-testid="encryption-policy-save"
        @click="save"
      >
        <Save class="h-4 w-4" />{{ t('common.save') }}
      </Button>
    </div>
  </section>
</template>
