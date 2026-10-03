<script setup lang="ts">
/**
 * AppFrameModal — the dialog an app's own interface opens in: a `modal`
 * action whose view has a `ui` file, or a `home` screen drawn in place. The
 * frame is AppFrame (the one frame of every placement); this is only the
 * dialog around it — the title, and a close that asks first when the
 * interface holds unsaved changes (AppFrame.confirmClose).
 */
import { ref } from 'vue';
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { FileApi } from '../../composables/useFileApi';
import type { PluginUIRef } from '../../types/Plugins';
import Modal from '../../modals/Modal.vue';
import AppFrame, { type AppFrameFile } from './AppFrame.vue';

const props = defineProps<{
  api: FileApi;
  locale: LocaleCode;
  theme?: ThemeMode;
  plugin: string;
  view: string;
  placement: 'modal' | 'home';
  ui: PluginUIRef;
  label: string;
  files: AppFrameFile[];
  userName?: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'toast', message: string): void;
  (e: 'op', op: Record<string, unknown>): void;
}>();

const frame = ref<InstanceType<typeof AppFrame> | null>(null);
/** What the interface calls itself (`ui.title`), under the app's name. */
const subtitle = ref('');

async function close() {
  if (frame.value && !(await frame.value.confirmClose())) return;
  emit('close');
}
</script>

<template>
  <Modal
    :open="true"
    :size="placement === 'home' ? 'xl' : 'lg'"
    :title="subtitle ? `${label} - ${subtitle}` : label"
    :locale="locale"
    :theme="theme"
    :close-on-backdrop="false"
    data-testid="app-frame-modal"
    @close="close"
  >
    <div class="fe-appframe-modal__body">
      <AppFrame
        ref="frame"
        :api="api"
        :app="plugin"
        :version="ui.version"
        :view="view"
        :placement="placement"
        :ui="ui"
        :files="files"
        :locale="locale"
        :theme="theme"
        :user-name="userName"
        :title="label"
        @title="(t) => (subtitle = t)"
        @toast="(m) => emit('toast', m.text)"
        @op="(op) => emit('op', op)"
        @close="close"
      />
    </div>
  </Modal>
</template>

<style>
.fe-appframe-modal__body {
  display: flex;
  flex-direction: column;
  height: min(75vh, 900px);
  min-height: 0;
}
</style>
