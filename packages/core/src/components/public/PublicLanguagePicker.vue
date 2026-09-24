<script setup lang="ts">
/**
 * PublicLanguagePicker — the language a stranger reads the page in.
 *
 * ⚠ A share link is opened by somebody who has no account here, so there is
 * no saved preference to read and the server's guess (`Accept-Language`) is
 * all there was. This is how they correct it — and the correction is kept on
 * the DEVICE only, because there is nobody to keep it for.
 *
 * ⚠⚠ The list is NOT hardcoded (v3 §5). It is whatever `lib/uiLocales` says
 * the instance offers, which includes a language an app plugin shipped —
 * marked with the app it came from, so nobody wonders why "العربية" appeared
 * on a filex install. Buttons, not a dropdown, for the same reason the rest
 * of this round is: with two or three languages the answer should be
 * readable without opening anything.
 */
import { computed } from 'vue';
import { availableLocales, localesVersion } from '../../lib/uiLocales';

const props = defineProps<{
  modelValue: string;
  /** Shown beside a language an app added ("from <app>"). */
  pluginNote?: (plugin: string) => string;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: string): void;
}>();

const options = computed(() => {
  void localesVersion.value;
  return availableLocales();
});

/** One language is not a choice — the picker draws nothing rather than a stub. */
const show = computed(() => options.value.length > 1);
</script>

<template>
  <div v-if="show" class="fe-plang" role="group" data-testid="public-language">
    <button
      v-for="o in options"
      :key="o.code"
      type="button"
      class="fe-plang__btn"
      :class="{ 'is-on': o.code === props.modelValue }"
      :lang="o.code"
      :aria-pressed="o.code === props.modelValue ? 'true' : 'false'"
      :title="o.source === 'plugin' && o.plugin && pluginNote ? pluginNote(o.plugin) : undefined"
      :data-testid="`public-language-${o.code}`"
      @click="emit('update:modelValue', o.code)"
    >
      {{ o.label }}
      <small v-if="o.source === 'plugin' && o.plugin" class="fe-plang__src">{{ o.plugin }}</small>
    </button>
  </div>
</template>

