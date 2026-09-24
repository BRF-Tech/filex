<script setup lang="ts">
/**
 * SurfaceFooterButtons — a surface's footer, wherever the frame puts it.
 *
 * The rule a plugin's buttons follow is not the frame's business: the
 * primary one posts `submit`, every other one posts `action`, a `danger` one
 * is red, and none of them is clickable while the conversation is busy
 * (`usePluginSurface.press`). Three frames (dialog, page, public shell)
 * place the row differently and must not each re-derive that.
 *
 * ⚠⚠ v3 §2 rule 3 — on a STEP (a surface carrying a `steps` node) the row is
 * arranged rather than merely listed: Back on the left, one primary on the
 * right, everything else between them, and a second `primary: true` is drawn
 * as an ordinary button. `lib/surfaceSteps` holds the arrangement so the
 * frames, the tests and the plugin test kit all read one answer.
 *
 * ⚠ `testidPrefix` is the one thing that legitimately differs per frame: the
 * suites address the dialog's buttons and the page's separately, so a shared
 * id would make a passing test ambiguous about which screen it measured.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../../types/ExplorerConfig';
import type { PluginSurfaceStore } from '../../composables/usePluginSurface';
import type { SurfaceAction } from '../../types/Plugins';
import { labelOf } from '../../lib/pluginLabel';
import { hasSteps, stepFooter } from '../../lib/surfaceSteps';

const props = defineProps<{
  conv: PluginSurfaceStore;
  locale: LocaleCode;
  /** `plugin-view` → `data-testid="plugin-view-action-<id>"`. */
  testidPrefix: string;
  /** The inspector's row is compact; every other frame uses the full size. */
  small?: boolean;
}>();

const stepped = computed(() => hasSteps(props.conv.current.value?.nodes));

const arranged = computed(() => stepFooter(props.conv.footer.value, stepped.value));

/** Back, the ordinary buttons, then the one primary — in that DOM order. */
const ordered = computed<SurfaceAction[]>(() => {
  const f = arranged.value;
  return [...(f.back ? [f.back] : []), ...f.others, ...(f.primary ? [f.primary] : [])];
});

/** Only the arranged primary is primary; a plugin's second one is not. */
function isPrimary(a: SurfaceAction): boolean {
  if (!stepped.value) return a.primary === true;
  return a === arranged.value.primary;
}
</script>

<template>
  <button
    v-for="a in ordered"
    :key="a.id"
    type="button"
    class="fe-btn"
    :class="{
      'fe-btn--sm': small,
      'fe-btn--primary': isPrimary(a) && !a.danger,
      'fe-btn--danger': a.danger,
      'fe-surface__back': stepped && a === arranged.back,
    }"
    :disabled="props.conv.busy.value || a.disabled"
    :data-testid="`${testidPrefix}-action-${a.id}`"
    @click="props.conv.press(a)"
  >
    {{ labelOf(a.label, locale) }}
  </button>
</template>

