<script setup lang="ts">
/**
 * ContextMenu — Teleport-based menu.
 *
 * Teleported under <body> so no parent (Ionic <ion-page>, Filament's
 * transformed flex containers, panel sidebars, …) can shift the
 * `position: fixed` backdrop off-window.
 */
import { ref, computed, onMounted, onBeforeUnmount } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import type { FileNode } from '../types/FileNode';
import { useLocale } from '../composables/useLocale';

export interface ContextAction {
  key: string;
  label: string;
  icon?: string;
  danger?: boolean;
  disabled?: boolean;
  hidden?: boolean;
  divider?: boolean;
}

const props = defineProps<{
  locale: LocaleCode;
  actions: ContextAction[];
  /**
   * Resolved theme. Teleported into <body> we lose the `.fe` parent
   * that scopes CSS vars, so we tag the backdrop with the current
   * theme so variables.css can re-apply dark overrides outside the
   * explorer tree.
   */
  theme?: ThemeMode;
}>();

const emit = defineEmits<{
  (e: 'select', action: ContextAction, target: FileNode[]): void;
}>();

void useLocale(() => props.locale); // eager-instantiate t/lookup so future templates can reuse

const open = ref(false);
const x = ref(0);
const y = ref(0);
const targetNodes = ref<FileNode[]>([]);

function show(ev: { clientX: number; clientY: number }, nodes: FileNode[]) {
  open.value = true;
  x.value = ev.clientX;
  y.value = ev.clientY;
  targetNodes.value = nodes;
}

function hide() {
  open.value = false;
}

function pick(a: ContextAction) {
  if (a.disabled) return;
  emit('select', a, targetNodes.value);
  hide();
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') hide();
}

const visibleActions = computed(() => props.actions.filter((a) => !a.hidden));

const prefersDark = ref(false);
let mq: MediaQueryList | undefined;
function syncPrefersDark(e?: MediaQueryListEvent | MediaQueryList) {
  prefersDark.value = !!(e && 'matches' in e && e.matches);
}
onMounted(() => {
  if (typeof window === 'undefined') return;
  mq = window.matchMedia('(prefers-color-scheme: dark)');
  syncPrefersDark(mq);
  mq.addEventListener?.('change', syncPrefersDark);
});
onBeforeUnmount(() => {
  mq?.removeEventListener?.('change', syncPrefersDark);
});

const themeClass = computed(() => {
  const t = props.theme || 'auto';
  return `fe-ctx-backdrop--theme-${t}`;
});

defineExpose({ show, hide });
</script>

<template>
  <Teleport to="body">
    <transition name="fe-ctx">
      <div
        v-if="open"
        class="fe-ctx-backdrop"
        :class="themeClass"
        :data-prefers-dark="prefersDark ? '1' : '0'"
        @click="hide"
        @contextmenu.prevent="hide"
        @keydown="onKey"
      >
        <div
          class="fe-ctx"
          role="menu"
          :style="{ top: y + 'px', left: x + 'px' }"
          @click.stop
        >
          <template v-for="(a, i) in visibleActions" :key="a.key || i">
            <div v-if="a.divider" class="fe-ctx__sep" />
            <button
              v-else
              type="button"
              class="fe-ctx__item"
              :class="{ 'is-danger': a.danger, 'is-disabled': a.disabled }"
              :disabled="a.disabled"
              role="menuitem"
              @click="pick(a)"
            >
              <span v-if="a.icon" class="fe-ctx__icon" aria-hidden="true">{{ a.icon }}</span>
              <span class="fe-ctx__label">{{ a.label }}</span>
            </button>
          </template>
        </div>
      </div>
    </transition>
  </Teleport>
</template>
