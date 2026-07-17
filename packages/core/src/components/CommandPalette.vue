<script setup lang="ts">
/**
 * CommandPalette — Ctrl/Cmd+K quick launcher.
 *
 * Sources (no new backend calls — everything comes from props):
 *   1. Files/folders of the CURRENT listing (`files` prop), filtered by a
 *      tiny scored-includes matcher (no fuzzy-search dependency).
 *   2. Commands mapped 1:1 onto existing FileExplorer actions, surfaced as
 *      individual emits so the host wires each to code it already has.
 *   3. Path jump: any query containing `/` offers a "go to path" entry.
 *
 * Keyboard: ↑/↓ move, Enter selects, Esc closes; the listener sits on the
 * document in CAPTURE phase so it wins over useKeyboardShortcuts' window
 * handler (otherwise Enter would also fire the explorer's own onOpen).
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { FileNode, ViewMode } from '../types/FileNode';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  files: FileNode[];
  viewMode: ViewMode;
  /** Gates the mutating commands (new folder / upload). */
  canWrite?: boolean;
  /** Gates the "up one level" command. */
  canGoUp?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'open-node', node: FileNode): void;
  (e: 'navigate', path: string): void;
  (e: 'new-folder'): void;
  (e: 'upload'): void;
  (e: 'toggle-view'): void;
  (e: 'open-trash'): void;
  (e: 'refresh'): void;
  (e: 'go-up'): void;
}>();

const { t, nodeDisplayName } = useLocale(() => props.locale);

const query = ref('');
const active = ref(0);
const inputEl = ref<HTMLInputElement | null>(null);
const listEl = ref<HTMLElement | null>(null);

type PaletteItem =
  | { kind: 'goto'; id: string; label: string; icon: string; path: string }
  | { kind: 'file'; id: string; label: string; icon: string; node: FileNode }
  | { kind: 'command'; id: string; label: string; icon: string; command: string };

/**
 * Scored includes: -1 = no match; otherwise earlier + prefix matches rank
 * higher and shorter names get a small edge. Good enough without a fuzzy
 * dependency.
 */
function matchScore(label: string, q: string): number {
  if (!q) return 0;
  const n = label.toLocaleLowerCase();
  const s = q.toLocaleLowerCase();
  const i = n.indexOf(s);
  if (i === -1) return -1;
  let sc = 100 - Math.min(i, 50);
  if (i === 0) sc += 40;
  sc -= Math.min(n.length, 40) / 4;
  return sc;
}

const gotoItems = computed<PaletteItem[]>(() => {
  const q = query.value.trim();
  if (!q.includes('/')) return [];
  const clean = q.replace(/^\/+|\/+$/g, '');
  if (!clean) return [];
  return [
    { kind: 'goto', id: `goto:${clean}`, label: `${t('palette.goto')}: ${clean}`, icon: '➜', path: clean },
  ];
});

const fileItems = computed<PaletteItem[]>(() => {
  const q = query.value.trim();
  const scored = props.files
    .map((f) => ({ f, name: nodeDisplayName(f), s: matchScore(nodeDisplayName(f), q) }))
    .filter((r) => r.s >= 0);
  if (q) scored.sort((a, b) => b.s - a.s);
  return scored.slice(0, 8).map((r) => ({
    kind: 'file' as const,
    id: `file:${r.f.path}`,
    label: r.name,
    icon: r.f.type === 'dir' ? '📁' : '📄',
    node: r.f,
  }));
});

const commandItems = computed<PaletteItem[]>(() => {
  const q = query.value.trim();
  const defs: Array<{ command: string; label: string; icon: string; enabled: boolean }> = [
    { command: 'new-folder', label: t('toolbar.new_folder'), icon: '📁', enabled: props.canWrite !== false },
    { command: 'upload', label: t('toolbar.upload'), icon: '⬆', enabled: props.canWrite !== false },
    { command: 'toggle-view', label: t('cmd.view_toggle'), icon: props.viewMode === 'list' ? '▦' : '☰', enabled: true },
    { command: 'open-trash', label: t('cmd.trash'), icon: '🗑', enabled: true },
    { command: 'refresh', label: t('toolbar.refresh'), icon: '⟳', enabled: true },
    { command: 'go-up', label: t('toolbar.go_up'), icon: '↑', enabled: props.canGoUp !== false },
  ];
  return defs
    .filter((d) => d.enabled && matchScore(d.label, q) >= 0)
    .map((d) => ({ kind: 'command' as const, id: `cmd:${d.command}`, label: d.label, icon: d.icon, command: d.command }));
});

const flat = computed<PaletteItem[]>(() => [
  ...gotoItems.value,
  ...fileItems.value,
  ...commandItems.value,
]);
const fileOffset = computed(() => gotoItems.value.length);
const cmdOffset = computed(() => gotoItems.value.length + fileItems.value.length);

watch(query, () => {
  active.value = 0;
});

function move(delta: number) {
  const n = flat.value.length;
  if (!n) return;
  active.value = (active.value + delta + n) % n;
  void nextTick(() => {
    listEl.value?.querySelector('.is-active')?.scrollIntoView({ block: 'nearest' });
  });
}

function choose(item?: PaletteItem) {
  const it = item ?? flat.value[active.value];
  if (!it) return;
  emit('close');
  if (it.kind === 'file') {
    emit('open-node', it.node);
  } else if (it.kind === 'goto') {
    emit('navigate', it.path);
  } else {
    switch (it.command) {
      case 'new-folder': emit('new-folder'); break;
      case 'upload': emit('upload'); break;
      case 'toggle-view': emit('toggle-view'); break;
      case 'open-trash': emit('open-trash'); break;
      case 'refresh': emit('refresh'); break;
      case 'go-up': emit('go-up'); break;
    }
  }
}

function onDocKeydown(e: KeyboardEvent) {
  if (!props.open) return;
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault();
      e.stopPropagation();
      move(1);
      break;
    case 'ArrowUp':
      e.preventDefault();
      e.stopPropagation();
      move(-1);
      break;
    case 'Enter':
      e.preventDefault();
      e.stopPropagation();
      choose();
      break;
    case 'Escape':
      e.preventDefault();
      e.stopPropagation();
      emit('close');
      break;
  }
}

watch(
  () => props.open,
  (v) => {
    if (v) {
      query.value = '';
      active.value = 0;
      document.addEventListener('keydown', onDocKeydown, true);
      void nextTick(() => inputEl.value?.focus());
    } else {
      document.removeEventListener('keydown', onDocKeydown, true);
    }
  },
);

onBeforeUnmount(() => document.removeEventListener('keydown', onDocKeydown, true));
</script>

<template>
  <transition name="fe-modal">
    <div
      v-if="open"
      class="fe-modal__backdrop fe-cmdp__backdrop"
      role="presentation"
      @click="emit('close')"
    >
      <div
        class="fe-cmdp"
        role="dialog"
        aria-modal="true"
        :aria-label="t('palette.placeholder')"
        @click.stop
      >
        <div class="fe-cmdp__inputwrap">
          <svg
            class="fe-cmdp__glyph"
            width="16"
            height="16"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            aria-hidden="true"
          >
            <circle cx="7" cy="7" r="4.5" />
            <path d="m10.5 10.5 3.5 3.5" />
          </svg>
          <input
            ref="inputEl"
            v-model="query"
            type="text"
            class="fe-cmdp__input"
            :placeholder="t('palette.placeholder')"
            autocomplete="off"
            spellcheck="false"
          />
        </div>

        <div ref="listEl" class="fe-cmdp__list" role="listbox">
          <button
            v-for="(it, i) in gotoItems"
            :key="it.id"
            type="button"
            class="fe-cmdp__item"
            :class="{ 'is-active': i === active }"
            role="option"
            :aria-selected="i === active"
            @mouseenter="active = i"
            @click="choose(it)"
          >
            <span class="fe-cmdp__icon" aria-hidden="true">{{ it.icon }}</span>
            <span class="fe-cmdp__label">{{ it.label }}</span>
          </button>

          <template v-if="fileItems.length">
            <div class="fe-cmdp__group">{{ t('palette.files') }}</div>
            <button
              v-for="(it, i) in fileItems"
              :key="it.id"
              type="button"
              class="fe-cmdp__item"
              :class="{ 'is-active': fileOffset + i === active }"
              role="option"
              :aria-selected="fileOffset + i === active"
              @mouseenter="active = fileOffset + i"
              @click="choose(it)"
            >
              <span class="fe-cmdp__icon" aria-hidden="true">{{ it.icon }}</span>
              <span class="fe-cmdp__label">{{ it.label }}</span>
            </button>
          </template>

          <template v-if="commandItems.length">
            <div class="fe-cmdp__group">{{ t('palette.commands') }}</div>
            <button
              v-for="(it, i) in commandItems"
              :key="it.id"
              type="button"
              class="fe-cmdp__item"
              :class="{ 'is-active': cmdOffset + i === active }"
              role="option"
              :aria-selected="cmdOffset + i === active"
              @mouseenter="active = cmdOffset + i"
              @click="choose(it)"
            >
              <span class="fe-cmdp__icon" aria-hidden="true">{{ it.icon }}</span>
              <span class="fe-cmdp__label">{{ it.label }}</span>
            </button>
          </template>

          <div v-if="flat.length === 0" class="fe-cmdp__empty">
            {{ t('palette.empty') }}
          </div>
        </div>

        <div class="fe-cmdp__hint">{{ t('palette.hint') }}</div>
      </div>
    </div>
  </transition>
</template>
