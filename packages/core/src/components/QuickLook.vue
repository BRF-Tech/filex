<script setup lang="ts">
/**
 * QuickLook — wiring:c2 Space quick-look overlay.
 *
 * A thin behavioural wrapper over the existing PreviewModal (view
 * mode), so every viewer the modal knows (image/video/pdf/office/code/
 * 3D/archive/…) works in quick-look for free. What this layer adds:
 *
 *   - the quick-look key closes the peek again (Space by default — macOS
 *     Quick Look convention), and the open key promotes it into the full
 *     open flow (Enter by default, emit 'open-full'). Both come from the
 *     shortcut registry rather than from a comparison against the default
 *     key, so a remap reaches the overlay too
 *   - ← ↑ / → ↓ ask the host to move the selection; the host keeps the
 *     `file` prop in sync so the preview follows the selection. These are
 *     the peek's own arrows, not registry actions
 *   - a small floating hint bar naming those keys, read from the registry
 *
 * Keys are handled in window CAPTURE phase with stopPropagation so the
 * global shortcut registry never double-handles them; events that
 * originate in a form control inside a viewer (e.g. the CSV filter
 * input) are left alone. Esc is untouched — PreviewModal's own Modal
 * already closes on it.
 */
import { computed, onBeforeUnmount, watch } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { eventMatchesShortcut, shortcutHint } from '../composables/useKeyboardShortcuts';
import PreviewModal from '../modals/PreviewModal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  file: FileNode | null;
  previewUrl: (path: string) => string;
  downloadUrl: (path: string) => string;
  onlyOfficeBase?: string | null;
  onlyOfficeConfigEndpoint?: string | null;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
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

/**
 * The peek's own arrows. These are NOT registry actions — they only
 * mean anything while the overlay is up — so they are declared once
 * here and the hint bar prints this same constant. Two copies of a key
 * name is how a legend starts lying about the key it names.
 */
const NAV_KEYS = ['↑', '↓'] as const;

function onKeydown(e: KeyboardEvent) {
  if (!props.open) return;
  if (inFormControl(e.target)) return;

  // ⚠ The registry first, and BEFORE the modifier bail-out: the user may
  // have remapped quick-look onto a combo that carries Ctrl or Alt. The
  // old version compared `e.key` to a hardcoded ' ' / 'Enter', so after a
  // remap the peek opened on the new key and still closed on the old one
  // — and the hint bar named the old one.
  if (eventMatchesShortcut(e, 'quicklook')) {
    e.preventDefault();
    e.stopPropagation();
    emit('close');
    return;
  }
  if (eventMatchesShortcut(e, 'open')) {
    e.preventDefault();
    e.stopPropagation();
    emit('open-full');
    return;
  }

  if (e.ctrlKey || e.metaKey || e.altKey) return;
  switch (e.key) {
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
  }
}

/**
 * The legend, read from the live registry. A segment whose action the
 * user has unbound disappears rather than printing an empty key cap.
 */
const hintSegments = computed(() => {
  const segs: Array<{ id: string; keys: string[]; label: string }> = [];
  const close = shortcutHint('quicklook');
  if (close) segs.push({ id: 'quicklook', keys: [close], label: t('quicklook.hint_close') });
  segs.push({ id: 'nav', keys: [...NAV_KEYS], label: t('quicklook.hint_nav') });
  const open = shortcutHint('open');
  if (open) segs.push({ id: 'open', keys: [open], label: t('quicklook.hint_open') });
  return segs;
});

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
  <!--
    Teleported under <body> for the same reason as ContextMenu and
    OnboardingTour: the hint carries the `fe` root class (that is how it
    reaches the `--fe-*` theme variables), so while it sits inside the
    explorer tree ANY host rule that reaches `.fe` by descendant also
    reaches the hint — and a host selector is always more specific than
    our own single class. web/src/views/Explore.vue sizes the embedded
    explorer with `.explore-host[data-v-…] .fe { height: 100% }`; that
    stretched the hint pill to the full viewport height (issue #22).
    Teleporting takes it out of every host container, so only the
    package's own rules can reach it.
  -->
  <Teleport to="body">
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
        <template v-for="(seg, i) in hintSegments" :key="seg.id">
          <span v-if="i > 0" class="fe-ql-hint__sep">·</span>
          <span class="fe-ql-hint__seg">
            <kbd v-for="k in seg.keys" :key="k" class="fe-kbd">{{ k }}</kbd>
            {{ seg.label }}
          </span>
        </template>
      </div>
    </transition>
  </Teleport>
</template>
