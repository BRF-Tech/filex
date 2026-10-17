<script setup lang="ts">
/**
 * RemoteImageDialog — the question a held picture asks before it is loaded
 * (composables/useRemoteImages, lib/sanitizeHtml RemoteImages).
 *
 * It names the server the picture is on and says what loading it does: that
 * server learns this file was opened, and from which IP address. Cancel is
 * the first button and has the focus, so Enter and Escape load nothing.
 * "Load all remote images in this file" is offered when others wait, for this
 * opening only - and alone when the question is about the whole file (a
 * book, whose chapters are drawn in a frame); where the person can change
 * it, the dialog says the setting that loads them without asking.
 *
 * ⚠ filex's own dialog (modals/Modal), never the browser's `confirm()`:
 * themed, in the reader's direction, and not blocked in a sandboxed frame.
 */
import { computed } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import type { RemoteImageGate } from '../composables/useRemoteImages';
import { useLocale } from '../composables/useLocale';
import { remoteImagesSettable } from '../lib/remoteImages';
import Modal from './Modal.vue';

const props = defineProps<{
  gate: RemoteImageGate;
  locale?: LocaleCode | string;
  theme?: ThemeMode;
}>();

const { t } = useLocale(() => props.locale ?? 'en');

const ask = computed(() => props.gate.asking.value);
const hint = computed(() => !!ask.value && remoteImagesSettable());
</script>

<template>
  <Modal
    :open="!!ask"
    :title="t('viewer.remote_image.title')"
    size="sm"
    :theme="theme"
    :locale="String(locale ?? 'en')"
    @close="gate.dismiss()"
  >
    <div class="fe-remote-ask" data-testid="remote-image-dialog">
      <p class="fe-remote-ask__text">{{ t('viewer.remote_image.body') }}</p>
      <p class="fe-remote-ask__host" dir="ltr" data-testid="remote-image-host">{{ ask?.host }}</p>
      <p v-if="hint" class="fe-remote-ask__hint" data-testid="remote-image-hint">{{ t('viewer.remote_image.setting_hint') }}</p>
    </div>
    <template #actions>
      <button type="button" class="fe-btn" data-testid="remote-image-cancel" @click="gate.dismiss()">
        {{ t('common.cancel') }}
      </button>
      <button
        v-if="ask?.whole || (ask?.others ?? 0) > 0"
        type="button"
        class="fe-btn"
        :class="{ 'fe-btn--primary': ask?.whole }"
        data-testid="remote-image-load-all"
        @click="gate.acceptAll()"
      >
        {{ t('viewer.remote_image.load_all') }}
      </button>
      <button v-if="!ask?.whole" type="button" class="fe-btn fe-btn--primary" data-testid="remote-image-load" @click="gate.accept()">
        {{ t('viewer.remote_image.load') }}
      </button>
    </template>
  </Modal>
</template>
