<script setup lang="ts">
/**
 * E2eLevelPicker — choose a folder's encryption level (wiring:e2).
 *
 * One picker for every place a level is chosen: creating an encrypted
 * folder, and encrypting a folder that already exists. The list comes from
 * lib/e2ecrypto `choosableLevels`. Level 1, contents only, is the default: it
 * keeps WebDAV, the command line and desktop sync working with the names.
 *
 * wiring:e2 vault — level 3, the vault, is drawn only when `vault` is set:
 * the server has its API (`capabilities.e2e_vault`) AND the folder is a new
 * one (an existing folder is never converted to a vault). A choice that does
 * not work is not offered. Its card says what it costs before anyone picks
 * it: one writer at a time, and nothing but filex reads it.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { choosableLevels, type ChoosableLevel } from '../lib/e2ecrypto';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  locale: LocaleCode;
  modelValue: ChoosableLevel;
  disabled?: boolean;
  /** Offer the vault (level 3): a new folder on a server that has it. */
  vault?: boolean;
}>();
const emit = defineEmits<{ (e: 'update:modelValue', v: ChoosableLevel): void }>();
const { t } = useLocale(() => props.locale);

const levels = computed(() => choosableLevels({ vault: props.vault === true }));

const LABEL: Record<ChoosableLevel, string> = {
  content: 'e2e.level.content',
  names: 'e2e.level.names',
  vault: 'e2e.level.vault',
};
const HINT: Record<ChoosableLevel, string> = {
  content: 'e2e.level.content_hint',
  names: 'e2e.level.names_hint',
  vault: 'e2e.level.vault_hint',
};
</script>

<template>
  <fieldset class="fe-e2e-levels" data-testid="e2e-level-picker" :disabled="disabled">
    <legend class="fe-field__label">{{ t('e2e.settings.level') }}</legend>
    <label
      v-for="(lv, i) in levels"
      :key="lv"
      class="fe-e2e-level"
      :class="{ 'fe-e2e-level--on': modelValue === lv }"
    >
      <input
        type="radio"
        name="fe-e2e-level"
        :value="lv"
        :checked="modelValue === lv"
        :data-testid="`e2e-level-${lv}`"
        @change="emit('update:modelValue', lv)"
      />
      <span class="fe-e2e-level__text">
        <span class="fe-e2e-level__name">{{ i + 1 }} · {{ t(LABEL[lv]) }}</span>
        <span class="fe-e2e-level__hint">{{ t(HINT[lv]) }}</span>
        <!-- wiring:e2 vault — what the vault costs, on its card. -->
        <span v-if="lv === 'vault'" class="fe-e2e-level__cost" data-testid="e2e-level-vault-cost">
          {{ t('e2e.level.vault_cost') }}
        </span>
      </span>
    </label>
  </fieldset>
</template>
