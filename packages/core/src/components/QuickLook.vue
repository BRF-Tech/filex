<script setup lang="ts">
/**
 * QuickLook — wiring:c2 Space quick-look overlay.
 *
 * A thin behavioural wrapper over the existing PreviewModal (view
 * mode), so every viewer the modal knows (image/video/pdf/office/code/
 * 3D/archive/…) works in quick-look for free. What this layer adds:
 *
 *   - Space closes again (macOS Quick Look convention)
 *   - ← ↑ / → ↓ ask the host to move the selection; the host keeps the
 *     `file` prop in sync so the preview follows the selection
 *   - Enter promotes the peek into the full open flow (emit 'open-full')
 *   - a small floating hint bar with the key legend
 *
 * Keys are handled in window CAPTURE phase with stopPropagation so the
 * global shortcut registry never double-handles them; events that
 * originate in a form control inside a viewer (e.g. the CSV filter
 * input) are left alone. Esc is untouched — PreviewModal's own Modal
 * already closes on it.
 */
import { onBeforeUnmount, watch } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import PreviewModal from '../modals/PreviewModal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  file: FileNode | null;
  previewUrl: (path: string) => string;
  downloadUrl: (path: string) => string;
  onlyOfficeBase?: string | null;
  onlyOfficeConfigEndpoint?: string | null;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
  drawioUrl?: string | null;
  pdfWorkerUrl?: string | null;
  viewerBaseUrl?: string | null;
  theme?: 'light' | 'dark' | 'auto';
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  /** Arrow navigation: -1 = previous file, +1 = next file. */
  (e: 'nav', delta: number): void;
  /** Enter — close the peek and open the file for real. */
  (e: 'open-full'): void;
}>();

const { t } = useLocale(() => props.locale);

function inFormControl(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  return !!(
    el &&
    (el.tagName === 'INPUT' ||
      el.tagName === 'TEXTAREA' ||
      el.tagName === 'SELECT' ||
      el.isContentEditable)
  );
}

function onKeydown(e: KeyboardEvent) {
  if (!props.open) return;
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  if (inFormControl(e.target)) return;
  switch (e.key) {
    case ' ':
      e.preventDefault();
      e.stopPropagation();
      emit('close');
      break;
    case 'ArrowRight':
    case 'ArrowDown':
      e.preventDefault();
      e.stopPropagation();
      emit('nav', 1);
      break;
    case 'ArrowLeft':
    case 'ArrowUp':
      e.preventDefault();
      e.stopPropagation();
      emit('nav', -1);
      break;
    case 'Enter':
      e.preventDefault();
      e.stopPropagation();
      emit('open-full');
      break;
  }
}

watch(
  () => props.open,
  (open) => {
    if (open) window.addEventListener('keydown', onKeydown, true);
    else window.removeEventListener('keydown', onKeydown, true);
  },
  { immediate: true },
);

onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown, true));
</script>

<template>
  <PreviewModal
    class="fe-quicklook"
    :open="open"
    :locale="locale"
    :file="file"
    :theme="theme"
    :preview-url="previewUrl"
    :download-url="downloadUrl"
    :only-office-base="onlyOfficeBase"
    :only-office-config-endpoint="onlyOfficeConfigEndpoint"
    open-mode="view"
    :auth-headers="authHeaders"
    :auth-credentials="authCredentials"
    :drawio-url="drawioUrl"
    :pdf-worker-url="pdfWorkerUrl"
    :viewer-base-url="viewerBaseUrl"
    @close="emit('close')"
  />
  <transition name="fe-toast">
    <div
      v-if="open"
      class="fe fe-ql-hint"
      :class="{
        'fe--theme-light': theme === 'light',
        'fe--theme-dark': theme === 'dark',
      }"
      aria-hidden="true"
    >
      <kbd class="fe-kbd">Space</kbd> {{ t('quicklook.hint_close') }}
      <span class="fe-ql-hint__sep">·</span>
      <kbd class="fe-kbd">↑</kbd><kbd class="fe-kbd">↓</kbd> {{ t('quicklook.hint_nav') }}
      <span class="fe-ql-hint__sep">·</span>
      <kbd class="fe-kbd">Enter</kbd> {{ t('quicklook.hint_open') }}
    </div>
  </transition>
</template>
