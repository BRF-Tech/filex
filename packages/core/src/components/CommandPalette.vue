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
 *
 * bul:s3 additions:
 *   4. "Everywhere" group — ≥3-char queries also hit the global files-search
 *      endpoint (debounced 250ms, max 8 rows) via the `globalSearch` prop.
 *      Content matches get an "İçerikte" badge + a «»-highlighted snippet
 *      rendered as pure text nodes (no innerHTML).
 *   5. Saved searches — localStorage-backed (`filex.saved-searches`, max 10);
 *      "save current query" command + per-row delete.
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { FileNode, ViewMode } from '../types/FileNode';
import type { GlobalSearchHit } from '../composables/useFileApi';
import { matchedInContent, snippetSegments } from '../lib/snippet';
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
  /* bul:s3 — global search callback; absent = older host, group hidden. */
  globalSearch?: (q: string) => Promise<GlobalSearchHit[]>;
  /* wiring:d1 — false hides the split command (narrow embeds). */
  splitEnabled?: boolean;
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
  /* bul:s3 — an "everywhere" hit was chosen. */
  (e: 'open-hit', hit: GlobalSearchHit): void;
  /* wiring:int — settings surfaces reachable from anywhere via the palette */
  (e: 'open-theme'): void;
  (e: 'open-shortcut-settings'): void;
  (e: 'start-tour'): void;
  /* wiring:d1 — tab / split commands */
  (e: 'tab-new'): void;
  (e: 'split-toggle'): void;
}>();

const { t, nodeDisplayName } = useLocale(() => props.locale);

const query = ref('');
const active = ref(0);
const inputEl = ref<HTMLInputElement | null>(null);
const listEl = ref<HTMLElement | null>(null);

type PaletteItem =
  | { kind: 'goto'; id: string; label: string; icon: string; path: string }
  | { kind: 'file'; id: string; label: string; icon: string; node: FileNode }
  | { kind: 'command'; id: string; label: string; icon: string; command: string }
  /* bul:s3 — everywhere hit / saved search / save-current-query. */
  | { kind: 'hit'; id: string; label: string; icon: string; hit: GlobalSearchHit; crumb: string; inContent: boolean; snippet: string }
  | { kind: 'saved'; id: string; label: string; icon: string; query: string }
  | { kind: 'save'; id: string; label: string; icon: string; query: string };

/* bul:s3 — narrowed aliases so the template doesn't need kind-guards. */
type HitItem = Extract<PaletteItem, { kind: 'hit' }>;
type SavedGroupItem = Extract<PaletteItem, { kind: 'saved' | 'save' }>;

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
    { command: 'toggle-view', label: t('cmd.view_toggle'), icon: props.viewMode === 'list' ? '▦' : props.viewMode === 'grid' ? '▣' : '☰', enabled: true /* wiring:d2 — sıradaki mod ikonu (list→grid→galeri→list) */ },
    { command: 'open-trash', label: t('cmd.trash'), icon: '🗑', enabled: true },
    { command: 'refresh', label: t('toolbar.refresh'), icon: '⟳', enabled: true },
    { command: 'go-up', label: t('toolbar.go_up'), icon: '↑', enabled: props.canGoUp !== false },
    /* wiring:int */
    { command: 'open-theme', label: t('theme.menu'), icon: '🎨', enabled: true },
    { command: 'open-shortcut-settings', label: t('shortcuts.settings.menu'), icon: '⌨', enabled: true },
    { command: 'start-tour', label: t('tour.restart'), icon: '🎓', enabled: true },
    /* wiring:d1 — sekme + split */
    { command: 'tab-new', label: t('cmd.tab_new'), icon: '⧉', enabled: true },
    { command: 'split-toggle', label: t('cmd.split_toggle'), icon: '◫', enabled: props.splitEnabled !== false },
  ];
  return defs
    .filter((d) => d.enabled && matchScore(d.label, q) >= 0)
    .map((d) => ({ kind: 'command' as const, id: `cmd:${d.command}`, label: d.label, icon: d.icon, command: d.command }));
});

/* === bul:s3 — "everywhere" global search ================================ */

const EVERYWHERE_MIN_CHARS = 3;
const EVERYWHERE_LIMIT = 8;
const EVERYWHERE_DEBOUNCE_MS = 250;

const everywhere = ref<GlobalSearchHit[]>([]);
const everywhereLoading = ref(false);
let everywhereTimer: ReturnType<typeof setTimeout> | undefined;
let everywhereSeq = 0;

function scheduleEverywhere(q: string) {
  if (everywhereTimer) clearTimeout(everywhereTimer);
  const fn = props.globalSearch;
  if (!fn || q.length < EVERYWHERE_MIN_CHARS) {
    everywhere.value = [];
    everywhereLoading.value = false;
    return;
  }
  everywhereLoading.value = true;
  everywhereTimer = setTimeout(async () => {
    const seq = ++everywhereSeq;
    try {
      const hits = await fn(q);
      if (seq !== everywhereSeq) return; // stale response — a newer query won
      everywhere.value = Array.isArray(hits) ? hits.slice(0, EVERYWHERE_LIMIT) : [];
    } catch {
      if (seq === everywhereSeq) everywhere.value = [];
    } finally {
      if (seq === everywhereSeq) everywhereLoading.value = false;
    }
  }, EVERYWHERE_DEBOUNCE_MS);
}

/** Parent-path crumb for a hit row (storage label when the hit carries one). */
function hitCrumb(h: GlobalSearchHit): string {
  const rel = String(h.path ?? '').replace(/^\/+|\/+$/g, '');
  const slash = rel.lastIndexOf('/');
  const parent = slash === -1 ? '' : rel.slice(0, slash);
  const storage =
    (typeof h.storage === 'string' && h.storage) ||
    (typeof h.storage_name === 'string' && h.storage_name) ||
    '';
  if (storage && parent) return `${storage}/${parent}`;
  if (storage) return storage;
  return parent || '/';
}

const hitItems = computed<HitItem[]>(() =>
  everywhere.value.map((h) => ({
    kind: 'hit' as const,
    id: `hit:${h.storage_id ?? ''}:${h.path ?? ''}:${h.id ?? ''}`,
    label: String(h.name ?? h.path ?? ''),
    icon: h.type === 'dir' ? '📁' : '📄',
    hit: h,
    crumb: hitCrumb(h),
    inContent: matchedInContent(h.matched),
    snippet: typeof h.snippet === 'string' ? h.snippet : '',
  })),
);

/* === bul:s3 — saved searches (localStorage) ============================= */

const SAVED_LS_KEY = 'filex.saved-searches';
const SAVED_MAX = 10;

const savedSearches = ref<string[]>([]);

function readSavedSearches(): string[] {
  try {
    const raw = localStorage.getItem(SAVED_LS_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((x): x is string => typeof x === 'string' && x.trim() !== '').slice(0, SAVED_MAX);
  } catch {
    return [];
  }
}

function writeSavedSearches(list: string[]) {
  savedSearches.value = list.slice(0, SAVED_MAX);
  try {
    localStorage.setItem(SAVED_LS_KEY, JSON.stringify(savedSearches.value));
  } catch {
    /* private mode / quota — the in-memory list still works this session */
  }
}

function saveSearch(q: string) {
  const clean = q.trim();
  if (!clean) return;
  const list = savedSearches.value.filter((s) => s !== clean);
  list.unshift(clean); // newest first; cap enforced by writeSavedSearches
  writeSavedSearches(list);
}

function removeSavedSearch(q: string) {
  writeSavedSearches(savedSearches.value.filter((s) => s !== q));
  if (active.value >= flat.value.length) active.value = Math.max(0, flat.value.length - 1);
}

const savedItems = computed<SavedGroupItem[]>(() => {
  const q = query.value.trim();
  const out: SavedGroupItem[] = [];
  // "Save this query" command — only when there IS a query and it isn't saved yet.
  if (q && !savedSearches.value.includes(q)) {
    out.push({ kind: 'save', id: `save:${q}`, label: `${t('palette.save')}: ${q}`, icon: '💾', query: q });
  }
  const scored = savedSearches.value
    .map((s) => ({ s, sc: matchScore(s, q) }))
    .filter((r) => r.sc >= 0 || !q);
  for (const r of scored) {
    out.push({ kind: 'saved', id: `saved:${r.s}`, label: r.s, icon: '🔖', query: r.s });
  }
  return out;
});

/* ======================================================================== */

const flat = computed<PaletteItem[]>(() => [
  ...gotoItems.value,
  ...fileItems.value,
  ...hitItems.value,
  ...savedItems.value,
  ...commandItems.value,
]);
const fileOffset = computed(() => gotoItems.value.length);
const hitOffset = computed(() => fileOffset.value + fileItems.value.length);
const savedOffset = computed(() => hitOffset.value + hitItems.value.length);
const cmdOffset = computed(() => savedOffset.value + savedItems.value.length);

watch(query, (q) => {
  active.value = 0;
  scheduleEverywhere(q.trim()); /* bul:s3 */
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
  /* bul:s3 — saved-search rows keep the palette OPEN: picking one re-runs
   * the palette with that query; saving just persists it. */
  if (it.kind === 'saved') {
    query.value = it.query;
    void nextTick(() => inputEl.value?.focus());
    return;
  }
  if (it.kind === 'save') {
    saveSearch(it.query);
    void nextTick(() => inputEl.value?.focus());
    return;
  }
  emit('close');
  if (it.kind === 'hit') {
    emit('open-hit', it.hit);
    return;
  }
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
      /* wiring:int */
      case 'open-theme': emit('open-theme'); break;
      case 'open-shortcut-settings': emit('open-shortcut-settings'); break;
      case 'start-tour': emit('start-tour'); break;
      /* wiring:d1 */
      case 'tab-new': emit('tab-new'); break;
      case 'split-toggle': emit('split-toggle'); break;
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
      /* bul:s3 — fresh session state for the new groups. */
      everywhere.value = [];
      everywhereLoading.value = false;
      savedSearches.value = readSavedSearches();
      document.addEventListener('keydown', onDocKeydown, true);
      void nextTick(() => inputEl.value?.focus());
    } else {
      if (everywhereTimer) clearTimeout(everywhereTimer); /* bul:s3 */
      document.removeEventListener('keydown', onDocKeydown, true);
    }
  },
);

onBeforeUnmount(() => {
  if (everywhereTimer) clearTimeout(everywhereTimer); /* bul:s3 */
  document.removeEventListener('keydown', onDocKeydown, true);
});
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

          <!-- bul:s3 — "Everywhere" global-search hits -->
          <template v-if="hitItems.length || everywhereLoading">
            <div class="fe-cmdp__group">{{ t('palette.everywhere') }}</div>
            <div v-if="everywhereLoading && !hitItems.length" class="fe-cmdp__loading">
              {{ t('palette.searching') }}
            </div>
            <button
              v-for="(it, i) in hitItems"
              :key="it.id"
              type="button"
              class="fe-cmdp__item fe-cmdp__item--hit"
              :class="{ 'is-active': hitOffset + i === active }"
              role="option"
              :aria-selected="hitOffset + i === active"
              @mouseenter="active = hitOffset + i"
              @click="choose(it)"
            >
              <span class="fe-cmdp__icon" aria-hidden="true">{{ it.icon }}</span>
              <span class="fe-cmdp__hitbody">
                <span class="fe-cmdp__hitline">
                  <span class="fe-cmdp__label">{{ it.label }}</span>
                  <span v-if="it.inContent" class="fe-cmdp__badge">{{ t('search.in_content') }}</span>
                </span>
                <span v-if="it.crumb" class="fe-cmdp__crumb" :title="it.crumb">{{ it.crumb }}</span>
                <!-- Snippet: «»-highlights become <mark> via TEXT segments — never innerHTML. -->
                <span v-if="it.snippet" class="fe-cmdp__snippet">
                  <template v-for="(seg, si) in snippetSegments(it.snippet)" :key="si">
                    <mark v-if="seg.match" class="fe-cmdp__mark">{{ seg.text }}</mark>
                    <template v-else>{{ seg.text }}</template>
                  </template>
                </span>
              </span>
            </button>
          </template>

          <!-- bul:s3 — saved searches -->
          <template v-if="savedItems.length">
            <div class="fe-cmdp__group">{{ t('palette.saved') }}</div>
            <div
              v-for="(it, i) in savedItems"
              :key="it.id"
              class="fe-cmdp__item fe-cmdp__item--saved"
              :class="{ 'is-active': savedOffset + i === active }"
              role="option"
              :aria-selected="savedOffset + i === active"
              tabindex="-1"
              @mouseenter="active = savedOffset + i"
              @click="choose(it)"
              @keydown.enter.prevent="choose(it)"
            >
              <span class="fe-cmdp__icon" aria-hidden="true">{{ it.icon }}</span>
              <span class="fe-cmdp__label">{{ it.label }}</span>
              <button
                v-if="it.kind === 'saved'"
                type="button"
                class="fe-cmdp__del"
                :title="t('palette.saved.delete')"
                :aria-label="t('palette.saved.delete')"
                @click.stop="removeSavedSearch(it.query)"
              >🗑</button>
            </div>
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

          <div v-if="flat.length === 0 && !everywhereLoading" class="fe-cmdp__empty">
            {{ t('palette.empty') }}
          </div>
        </div>

        <div class="fe-cmdp__hint">{{ t('palette.hint') }}</div>
      </div>
    </div>
  </transition>
</template>
