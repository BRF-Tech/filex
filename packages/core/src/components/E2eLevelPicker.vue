<script setup lang="ts">
/**
 * E2eLevelPicker — choose a folder's encryption level (wiring:e2).
 *
 * One picker for every place a level is chosen: creating an encrypted
 * folder, and encrypting a folder that already exists. The list comes from
 * lib/e2ecrypto `E2E_CHOOSABLE_LEVELS`; the vault (level 3) is designed and
 * not built, so it is not in the list and not drawn — a choice that does not
 * work is not offered. Level 1, contents only, is the default: it keeps
 * WebDAV, the command line and desktop sync working with the names.
 */
import type { LocaleCode } from '../types/ExplorerConfig';
import { E2E_CHOOSABLE_LEVELS, type ChoosableLevel } from '../lib/e2ecrypto';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  locale: LocaleCode;
  modelValue: ChoosableLevel;
  disabled?: boolean;
}>();
const emit = defineEmits<{ (e: 'update:modelValue', v: ChoosableLevel): void }>();
const { t } = useLocale(() => props.locale);

const LABEL: Record<ChoosableLevel, string> = { content: 'e2e.level.content', names: 'e2e.level.names' };
const HINT: Record<ChoosableLevel, string> = { content: 'e2e.level.content_hint', names: 'e2e.level.names_hint' };
</script>

<template>
  <fieldset class="fe-e2e-levels" data-testid="e2e-level-picker" :disabled="disabled">
    <legend class="fe-field__label">{{ t('e2e.settings.level') }}</legend>
    <label
      v-for="(lv, i) in E2E_CHOOSABLE_LEVELS"
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
      </span>
    </label>
  </fieldset>
</template>
