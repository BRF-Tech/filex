<script setup lang="ts">
/**
 * ToastContainer — where every toast of the panel is drawn.
 *
 * ⚠⚠ In the TOP LAYER, above any open dialog. The panel's dialogs are native
 * `<dialog>`s opened with showModal(), which puts them in the browser's top
 * layer; a toast in the ordinary page, whatever its z-index, was drawn
 * UNDER the dialog's backdrop. "Add user" with an empty form answered
 * "email required" in a toast nobody could read (release-candidate sweep,
 * 2026-09-21). A manual popover is top-layer too, and top-layer order is
 * the order things were SHOWN in — so the layer is re-shown whenever a toast
 * arrives, which puts it back above a dialog that opened after it.
 *
 * Forms still say their own refusals inside the dialog (Users, Webhooks);
 * this is for everything else that reports while a dialog is up.
 *
 * Where the Popover API is missing (an old browser, the unit tests' DOM) it
 * is simply the fixed corner it always was.
 */
import { nextTick, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useToastStore } from '@/stores/toast';
import Toast from './ui/Toast.vue';

const toast = useToastStore();
const { toasts } = storeToRefs(toast);

const layer = ref<HTMLElement | null>(null);

type PopoverEl = HTMLElement & { showPopover?: () => void; hidePopover?: () => void };

/** Show (or re-show, to rise above a dialog opened since) the layer. */
function raise() {
  const el = layer.value as PopoverEl | null;
  if (!el || typeof el.showPopover !== 'function') return;
  try {
    if (el.matches(':popover-open')) el.hidePopover?.();
    el.showPopover();
  } catch {
    /* not connected yet, or no popover support: the fixed corner stays */
  }
}

onMounted(raise);
watch(
  () => toasts.value.length,
  (n, before) => {
    if (n > (before ?? 0)) void nextTick(raise);
  },
);
</script>

<template>
  <div
    ref="layer"
    popover="manual"
    aria-live="polite"
    class="toast-layer pointer-events-none fixed bottom-4 end-4 z-50 flex flex-col gap-2"
    data-testid="toast-layer"
  >
    <Toast v-for="t in toasts" :key="t.id" :toast="t" @dismiss="toast.dismiss" />
  </div>
</template>

<style scoped>
/* A popover arrives with the UA's centred-box look (inset 0, margin auto, a
   border, padding and a canvas background). The layer is only a corner. */
.toast-layer {
  inset-block: auto 1rem;
  inset-inline: auto 1rem;
  margin: 0;
  padding: 0;
  border: 0;
  background: transparent;
  overflow: visible;
  width: auto;
  height: auto;
  max-width: calc(100vw - 2rem);
  color: inherit;
}
</style>
