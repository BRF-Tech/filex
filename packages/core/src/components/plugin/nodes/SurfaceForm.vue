<script setup lang="ts">
/**
 * SurfaceForm — a plugin surface's `form` node.
 *
 * The fields are drawn by `StorageFields`, the ONE field renderer this
 * package has (a storage driver's config form, an app's settings form and
 * now a plugin screen all declare `Field[]` in the same shape). This file
 * only translates: a plugin's `label`/`help` may be Text, and the values are
 * flat in the surface's value map, keyed by field key.
 *
 * ⚠⚠ v3 — it also decides WHICH fields exist right now. A field may carry
 * `show_when` / `required_when` (`lib/surfaceConditions`), so "the name of
 * the new file" is simply not on the step while the output is a new version
 * of the old one. A form may not present a contradiction, and the renderer
 * is what makes that true rather than each plugin.
 *
 * ⚠ Hiding is only half of it: the hidden field's VALUE must not travel
 * either, or the plugin receives an answer to a question it stopped asking.
 * That half is `usePluginSurface`'s, because it owns what goes on the wire —
 * this component could only blank the value, and `""` is an answer too.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../../../types/ExplorerConfig';
import type { PluginField, PluginText } from '../../../types/Plugins';
import { labelOf } from '../../../lib/pluginLabel';
import { storageFieldOf } from '../../../lib/surfaceValues';
import { fieldRequired, visibleFields } from '../../../lib/surfaceConditions';
import StorageFields from '../../StorageFields.vue';

const props = defineProps<{
  fields: PluginField[];
  /** The surface's whole value map — field values are read and written flat. */
  values: Record<string, unknown>;
  locale: LocaleCode;
  /** `surface.errors`: an entry under a field key marks it invalid, with those words. */
  errors?: Record<string, PluginText>;
  disabled?: boolean;
  /** Field keys the host is pointing at as unanswered (a blocked submit). */
  invalid?: string[];
}>();

const emit = defineEmits<{
  (e: 'update:values', v: Record<string, unknown>): void;
}>();

/** Only the fields whose `show_when` holds, conditions cascaded. */
const shown = computed(() => visibleFields(props.fields, props.values));

const mapped = computed(() =>
  shown.value.map((f) => {
    const sf = storageFieldOf(f, props.locale);
    // `required_when` is the same promise as `required`, made conditionally:
    // once the condition holds, the star is on the label and the submit is
    // blocked — so nobody is refused by the server for a rule the screen
    // never showed them.
    return { ...sf, required: fieldRequired(f, props.values) };
  }),
);

const fieldErrors = computed<Record<string, string>>(() => {
  const out: Record<string, string> = {};
  for (const f of mapped.value) {
    const msg = labelOf(props.errors?.[f.key], props.locale);
    if (msg) out[f.key] = msg;
  }
  return out;
});
</script>

<template>
  <div class="fe-surface__form" data-testid="surface-form">
    <StorageFields
      :fields="mapped"
      :model-value="values"
      :locale="locale"
      :errors="fieldErrors"
      :invalid="invalid"
      :disabled="disabled"
      @update:model-value="(v: Record<string, unknown>) => emit('update:values', v)"
    />
  </div>
</template>
