<script setup lang="ts">
/**
 * PanelSearch - the admin panel's search (task #168, docs/ADMIN-PANEL.md →
 * Search): one box that finds a page, a setting, a person, a group, an API
 * key, an app, a storage, a share and - on demand - a file.
 *
 * Drawn by the admin panel's top bar (web/src/components/AdminSearch.vue),
 * and written here rather than there for the reason MegaMenu is: it is a
 * search PATTERN, not the admin panel's. The host passes what may be found
 * (`items`, already limited to what the reader may open), how to ask the
 * server (`remote`) and the file index (`files`), and where the recent
 * searches live (`recent`); it gets `choose` back and does the navigating.
 * Nothing in this file names a route, a permission or an endpoint.
 *
 * Two shapes, one component (the host says which; the admin panel's own
 * breakpoint decides):
 *
 *   box      a wide screen. A box in the top bar; pressing it (or the
 *            palette key, Ctrl+K by default) opens a panel under it with the
 *            field and the results, flush with the box's end edge.
 *   compact  a phone. A search button in the top bar; it opens a layer over
 *            the whole window, the field at its top and a back button.
 *
 * Both are teleported to <body>: the top bar is `backdrop-filter`ed, which
 * makes it the containing block of every `position: fixed` inside it - a
 * full-window layer drawn in place would have been 56 pixels tall.
 *
 * ⚠ Keyboard and screen reader: the WAI-ARIA combobox pattern with a listbox
 * popup. The field is the combobox; the results are options in groups, each
 * group labelled by its heading; the option under the arrow keys is the
 * field's `aria-activedescendant`, so focus stays in the field while typing.
 *   ↓ / ↑      the next / previous row (wraps)
 *   Enter      opens the row (the first one when none is picked)
 *   Esc        closes the panel and gives focus back to the box
 *   Delete     on a recent search (an empty field), removes it
 * The palette key works wherever the explorer's does: anywhere but inside a
 * field or a code editor somebody is typing in (lib/typingTarget).
 * A polite live region says how many rows there are.
 *
 * ⚠⚠ Its rules are in styles/base.css (`.fx-psearch`), not in a <style>
 * block here - MegaMenu's lesson (web/tests/deploy/packageLook.test.ts).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';

import { eventMatchesShortcut, shortcutHint } from '../composables/useKeyboardShortcuts';
import { useLocale } from '../composables/useLocale';
import { actionIconSvg } from '../lib/actionIcons';
import { anchorUnderEndEdge, fixedViewport, type AnchoredPanelPosition } from '../lib/anchoredPanel';
import { dirOfElement } from '../lib/direction';
import { fileIconTile } from '../lib/fileIcons';
import {
  PANEL_SEARCH_FILES_CAP,
  PANEL_SEARCH_PREFIXED_CAP,
  PANEL_SEARCH_PREFIX_ORDER,
  panelSearchGroups,
  parsePanelQuery,
  withPanelPrefix,
  type PanelRecentSearch,
  type PanelRecentStore,
  type PanelSearchItem,
  type PanelSearchKind,
  type PanelSearchQuery,
} from '../lib/panelSearch';
import { popupLayer } from '../lib/popupLayer';
import { isTypingTarget } from '../lib/typingTarget';

const props = withDefaults(
  defineProps<{
    locale: string;
    /** A phone: a button and a layer over the window, instead of the box. */
    compact?: boolean;
    /** What the host can find by itself (pages, settings), already limited to what the reader may open. */
    items: PanelSearchItem[];
    /** The server's hits for a query (people, keys, apps…), already limited by the reader's role. */
    remote?: (query: PanelSearchQuery) => Promise<PanelSearchItem[]>;
    /** File hits for words, at most `limit`. */
    files?: (text: string, limit: number) => Promise<PanelSearchItem[]>;
    /** Where the recent searches live. */
    recent?: PanelRecentStore;
    /** The palette key opens this search (only one search on a page should say yes). */
    shortcut?: boolean;
  }>(),
  { compact: false, remote: undefined, files: undefined, recent: undefined, shortcut: true },
);

const emit = defineEmits<{
  /** A row was chosen: the host goes there. */
  (e: 'choose', item: PanelSearchItem): void;
}>();

const { t } = useLocale(() => props.locale);

/* Random ids (MegaMenu's reasoning: two searches on one page must not share
   them, and `useId` is Vue 3.5 while the package asks 3.4). */
const uid = `fx-psearch-${Math.random().toString(36).slice(2, 10)}`;
const listId = `${uid}-list`;

const trigger = ref<HTMLButtonElement | null>(null);
const layer = ref<HTMLElement | null>(null);
const inputEl = ref<HTMLInputElement | null>(null);
const listEl = ref<HTMLElement | null>(null);

const open = ref(false);
const query = ref('');
const active = ref(0);
const pos = ref<AnchoredPanelPosition | null>(null);
const z = ref(80);

const parsed = computed(() => parsePanelQuery(query.value));

/* ── What the server and the file index found ───────────────────────────── */

/** Under this many characters nothing is asked of the server. */
const REMOTE_MIN = 2;
const DEBOUNCE_MS = 200;

const remoteItems = ref<PanelSearchItem[]>([]);
const fileItems = ref<PanelSearchItem[]>([]);
const loading = ref(false);
const failed = ref(false);
let timer: ReturnType<typeof setTimeout> | undefined;
let seq = 0;

function schedule(q: PanelSearchQuery): void {
  if (timer) clearTimeout(timer);
  const mine = ++seq;
  if (q.text.length < REMOTE_MIN || (!props.remote && !props.files)) {
    remoteItems.value = [];
    fileItems.value = [];
    loading.value = false;
    failed.value = false;
    return;
  }
  loading.value = true;
  timer = setTimeout(() => void ask(q, mine), DEBOUNCE_MS);
}

async function ask(q: PanelSearchQuery, mine: number): Promise<void> {
  const wantsRemote = !!props.remote && q.kind !== 'file' && q.kind !== 'page' && q.kind !== 'setting';
  // Files only on demand: the first few without a prefix, a full page with `file:`.
  const wantsFiles = !!props.files && (q.kind === null || q.kind === 'file');
  const limit = q.kind === 'file' ? PANEL_SEARCH_PREFIXED_CAP : PANEL_SEARCH_FILES_CAP;
  const [r, f] = await Promise.allSettled([
    wantsRemote && props.remote ? props.remote(q) : Promise.resolve([] as PanelSearchItem[]),
    wantsFiles && props.files ? props.files(q.text, limit) : Promise.resolve([] as PanelSearchItem[]),
  ]);
  if (mine !== seq) return; // a newer query won
  remoteItems.value = r.status === 'fulfilled' ? r.value.map((x) => ({ ...x, found: true })) : [];
  fileItems.value = f.status === 'fulfilled' ? f.value.map((x) => ({ ...x, kind: 'file' as const, found: true })) : [];
  // ⚠ Said, not swallowed: a failure drawn as an empty answer is a person
  // believing the user or the file is not there (CommandPalette's lesson).
  failed.value = r.status === 'rejected' || f.status === 'rejected';
  loading.value = false;
}

watch(query, () => {
  active.value = 0;
  schedule(parsed.value);
});

/* ── Recent searches ────────────────────────────────────────────────────── */

const recentList = ref<PanelRecentSearch[]>([]);

async function loadRecent(): Promise<void> {
  if (!props.recent) return;
  try {
    recentList.value = await props.recent.list();
  } catch {
    /* keep the list we had */
  }
}

function remember(raw: string): void {
  const q = raw.trim();
  if (!q || !props.recent) return;
  recentList.value = [{ id: `local-${q}`, query: q }, ...recentList.value.filter((r) => r.query !== q)];
  void props.recent.add(q).then(loadRecent, () => undefined);
}

async function forget(entry: PanelRecentSearch): Promise<void> {
  recentList.value = recentList.value.filter((r) => r.id !== entry.id);
  if (active.value >= rows.value.length) active.value = Math.max(0, rows.value.length - 1);
  try {
    await props.recent?.remove(entry.id);
  } catch {
    await loadRecent();
  }
}

async function forgetAll(): Promise<void> {
  recentList.value = [];
  active.value = 0;
  try {
    await props.recent?.clear();
  } catch {
    await loadRecent();
  }
  inputEl.value?.focus();
}

/* ── The rows ───────────────────────────────────────────────────────────── */

type Row =
  | { type: 'item'; id: string; index: number; item: PanelSearchItem }
  | { type: 'files-all'; id: string; index: number }
  | { type: 'recent'; id: string; index: number; entry: PanelRecentSearch };

interface Section {
  key: PanelSearchKind | 'recent';
  heading: string;
  rows: Row[];
}

const GROUP_KEYS: Record<PanelSearchKind, string> = {
  page: 'panelsearch.group.page',
  setting: 'panelsearch.group.setting',
  app: 'panelsearch.group.app',
  user: 'panelsearch.group.user',
  group: 'panelsearch.group.group',
  key: 'panelsearch.group.key',
  storage: 'panelsearch.group.storage',
  share: 'panelsearch.group.share',
  file: 'panelsearch.group.file',
};

/** May the "search files" row be offered for this query? */
const offersAllFiles = computed(() => !!props.files && parsed.value.kind === null && parsed.value.text.length >= REMOTE_MIN);

const sections = computed<Section[]>(() => {
  let index = 0;
  const out: Section[] = [];
  const q = parsed.value;
  if (q.words.length === 0) {
    if (q.kind === null && recentList.value.length) {
      out.push({
        key: 'recent',
        heading: t('panelsearch.group.recent'),
        rows: recentList.value.map((entry) => ({ type: 'recent' as const, id: `recent:${entry.id}`, index: index++, entry })),
      });
    }
    return out;
  }
  const groups = panelSearchGroups([...props.items, ...remoteItems.value, ...fileItems.value], q);
  for (const g of groups) {
    out.push({
      key: g.kind,
      heading: t(GROUP_KEYS[g.kind]),
      rows: g.items.map((item) => ({ type: 'item' as const, id: `item:${item.id}`, index: index++, item })),
    });
  }
  if (offersAllFiles.value) {
    const files = out.find((s) => s.key === 'file');
    const row: Row = { type: 'files-all', id: 'files-all', index: index++ };
    if (files) files.rows.push(row);
    else out.push({ key: 'file', heading: t(GROUP_KEYS.file), rows: [row] });
  }
  return out;
});

const rows = computed<Row[]>(() => sections.value.flatMap((s) => s.rows));

const optionId = (index: number) => `${uid}-opt-${index}`;
const activeId = computed(() => (rows.value.length && active.value < rows.value.length ? optionId(active.value) : undefined));

/** What the live region says once the rows settle. */
const liveText = computed(() => {
  if (!open.value || parsed.value.words.length === 0) return '';
  if (loading.value) return t('panelsearch.searching');
  const n = rows.value.filter((r) => r.type === 'item').length;
  return n === 0 ? t('panelsearch.empty', { q: parsed.value.text }) : t('panelsearch.count', { n });
});

const showEmpty = computed(
  () => parsed.value.words.length > 0 && !loading.value && rows.value.every((r) => r.type !== 'item'),
);

function rowIcon(item: PanelSearchItem): string {
  if (item.kind === 'file') {
    const name = item.label ?? '';
    const dot = name.lastIndexOf('.');
    return fileIconTile({
      type: item.isDir ? 'dir' : 'file',
      basename: name,
      extension: !item.isDir && dot > 0 ? name.slice(dot + 1) : '',
    });
  }
  return actionIconSvg(item.iconName ?? KIND_ICONS[item.kind]);
}

/** The mark of a row that brings no glyph of its own. */
const KIND_ICONS: Record<PanelSearchKind, string> = {
  page: 'goto',
  setting: 'admin',
  app: 'plugin',
  user: 'account',
  group: 'account',
  key: 'lock',
  storage: 'connect',
  share: 'link',
  file: 'open',
};

/* ── Opening and closing ────────────────────────────────────────────────── */

function place(): void {
  const el = trigger.value;
  if (!el || typeof window === 'undefined') return;
  z.value = popupLayer([el]);
  if (props.compact) {
    pos.value = null;
    return;
  }
  pos.value = anchorUnderEndEdge(
    el.getBoundingClientRect(),
    fixedViewport(),
    { width: 560, gutter: 16, gap: 6, maxHeight: 620, dir: dirOfElement(el) },
  );
}

const layerStyle = computed<Record<string, string | undefined>>(() => {
  const style: Record<string, string | undefined> = { zIndex: String(z.value) };
  if (pos.value) {
    style.top = pos.value.top;
    style.right = pos.value.right;
    style.left = pos.value.left;
    style.width = pos.value.width;
    style.maxHeight = pos.value.maxHeight;
  }
  return style;
});

async function openPanel(seed?: string): Promise<void> {
  if (!open.value) {
    place();
    open.value = true;
    void loadRecent();
  }
  if (typeof seed === 'string') query.value = seed;
  await nextTick();
  inputEl.value?.focus();
  if (typeof seed !== 'string') inputEl.value?.select();
}

function close(returnFocus: boolean): void {
  if (!open.value) return;
  open.value = false;
  if (timer) clearTimeout(timer);
  loading.value = false;
  if (returnFocus) void nextTick(() => trigger.value?.focus());
}

function onTriggerKey(ev: KeyboardEvent): void {
  // Typing on the box starts the search with that letter.
  if (ev.key.length === 1 && !ev.ctrlKey && !ev.metaKey && !ev.altKey && ev.key !== ' ') {
    ev.preventDefault();
    void openPanel(ev.key);
  } else if (ev.key === 'ArrowDown') {
    ev.preventDefault();
    void openPanel();
  }
}

/* ── Choosing ───────────────────────────────────────────────────────────── */

function choose(row: Row | undefined): void {
  if (!row) return;
  if (row.type === 'recent') {
    query.value = row.entry.query;
    void nextTick(() => inputEl.value?.focus());
    return;
  }
  if (row.type === 'files-all') {
    // The owner's "all": the same words, as a file search.
    query.value = withPanelPrefix(query.value, 'file');
    void nextTick(() => inputEl.value?.focus());
    return;
  }
  remember(query.value);
  close(false);
  emit('choose', row.item);
}

function setPrefix(prefix: string): void {
  query.value = withPanelPrefix(query.value, prefix);
  void nextTick(() => inputEl.value?.focus());
}

function clearQuery(): void {
  query.value = '';
  void nextTick(() => inputEl.value?.focus());
}

function move(delta: number): void {
  const n = rows.value.length;
  if (!n) return;
  active.value = (active.value + delta + n) % n;
  void nextTick(() => {
    const row = listEl.value?.querySelector<HTMLElement>(`#${optionId(active.value)}`);
    if (row && typeof row.scrollIntoView === 'function') row.scrollIntoView({ block: 'nearest' });
  });
}

function onInputKey(ev: KeyboardEvent): void {
  switch (ev.key) {
    case 'ArrowDown':
      ev.preventDefault();
      move(1);
      break;
    case 'ArrowUp':
      ev.preventDefault();
      move(-1);
      break;
    case 'Enter':
      if (ev.isComposing) return;
      ev.preventDefault();
      choose(rows.value[active.value] ?? rows.value[0]);
      break;
    case 'Escape':
      ev.preventDefault();
      ev.stopPropagation();
      close(true);
      break;
    case 'Delete': {
      const row = rows.value[active.value];
      if (query.value === '' && row?.type === 'recent') {
        ev.preventDefault();
        void forget(row.entry);
      }
      break;
    }
  }
}

/* ── Leaving ────────────────────────────────────────────────────────────── */

function inside(node: Node | null): boolean {
  if (!node) return false;
  return !!(layer.value?.contains(node) || trigger.value?.contains(node));
}

function onFocusOut(ev: FocusEvent): void {
  // The phone's layer covers the window: focus leaves it only by its own
  // buttons. The box's panel closes when focus goes elsewhere on the page.
  if (props.compact) return;
  const to = ev.relatedTarget as Node | null;
  if (to && !inside(to)) close(false);
}

function onDocPointer(ev: PointerEvent): void {
  if (!open.value || props.compact) return;
  if (!inside(ev.target as Node | null)) close(false);
}

function onDocKey(ev: KeyboardEvent): void {
  if (!props.shortcut || ev.defaultPrevented) return;
  if (!eventMatchesShortcut(ev, 'palette')) return;
  // The explorer's rule for the same key (useKeyboardShortcuts): typing in a
  // field or a code editor is typing, and the key is the editor's there.
  if (isTypingTarget(ev.target) && !inside(ev.target as Node | null)) return;
  ev.preventDefault();
  if (open.value) {
    inputEl.value?.focus();
    inputEl.value?.select();
  } else {
    void openPanel();
  }
}

function onResize(): void {
  if (open.value) place();
}

onMounted(() => {
  document.addEventListener('keydown', onDocKey);
  document.addEventListener('pointerdown', onDocPointer, true);
  window.addEventListener('resize', onResize);
});

onBeforeUnmount(() => {
  if (timer) clearTimeout(timer);
  document.removeEventListener('keydown', onDocKey);
  document.removeEventListener('pointerdown', onDocPointer, true);
  window.removeEventListener('resize', onResize);
});

/** The palette key as this person has it bound, for the box's key cap ('' = none). */
const keyHint = computed(() => (props.shortcut ? shortcutHint('palette') : ''));

defineExpose({ openPanel, close });
</script>

<template>
  <div :class="['fx-psearch', compact ? 'fx-psearch--compact' : 'fx-psearch--box']">
    <button
      ref="trigger"
      type="button"
      class="fx-psearch__trigger"
      :aria-label="t('panelsearch.label')"
      aria-haspopup="dialog"
      :aria-expanded="open ? 'true' : 'false'"
      data-testid="panel-search-open"
      @click="open ? close(true) : openPanel()"
      @keydown="onTriggerKey"
    >
      <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons -->
      <span class="fx-psearch__glyph" aria-hidden="true" v-html="actionIconSvg('search')"></span>
      <span v-if="!compact" class="fx-psearch__trigger-text">{{ t('panelsearch.trigger') }}</span>
      <kbd v-if="!compact && keyHint" class="fx-psearch__kbd">{{ keyHint }}</kbd>
    </button>

    <Teleport to="body">
      <div
        v-if="open"
        ref="layer"
        :class="['fx-psearch__layer', compact ? 'fx-psearch__layer--full' : 'fx-psearch__layer--drop']"
        :style="layerStyle"
        role="dialog"
        :aria-modal="compact ? 'true' : undefined"
        :aria-label="t('panelsearch.label')"
        data-testid="panel-search"
        @focusout="onFocusOut"
      >
        <div class="fx-psearch__field">
          <button
            v-if="compact"
            type="button"
            class="fx-psearch__iconbtn"
            :aria-label="t('panelsearch.close')"
            data-testid="panel-search-close"
            @click="close(true)"
          >
            <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons -->
            <span class="fx-psearch__glyph" aria-hidden="true" v-html="actionIconSvg('close')"></span>
          </button>
          <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons -->
          <span v-else class="fx-psearch__glyph fx-psearch__glyph--field" aria-hidden="true" v-html="actionIconSvg('search')"></span>
          <input
            ref="inputEl"
            v-model="query"
            type="text"
            class="fx-psearch__input"
            role="combobox"
            :aria-label="t('panelsearch.label')"
            aria-autocomplete="list"
            :aria-expanded="rows.length > 0 ? 'true' : 'false'"
            :aria-controls="listId"
            :aria-activedescendant="activeId"
            :placeholder="t('panelsearch.placeholder')"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            enterkeyhint="search"
            data-testid="panel-search-input"
            @keydown="onInputKey"
          />
          <button
            v-if="query"
            type="button"
            class="fx-psearch__iconbtn"
            :aria-label="t('panelsearch.clear')"
            data-testid="panel-search-clear"
            @click="clearQuery"
          >
            <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons -->
            <span class="fx-psearch__glyph" aria-hidden="true" v-html="actionIconSvg('close')"></span>
          </button>
        </div>

        <div :id="listId" ref="listEl" class="fx-psearch__list" role="listbox" :aria-label="t('panelsearch.results')">
          <div
            v-for="section in sections"
            :key="section.key"
            class="fx-psearch__group"
            role="group"
            :aria-labelledby="`${uid}-h-${section.key}`"
            :data-testid="`panel-search-group-${section.key}`"
          >
            <div :id="`${uid}-h-${section.key}`" class="fx-psearch__heading" role="presentation">{{ section.heading }}</div>
            <div
              v-for="row in section.rows"
              :id="optionId(row.index)"
              :key="row.id"
              :class="['fx-psearch__row', row.index === active && 'is-active']"
              role="option"
              :aria-selected="row.index === active ? 'true' : 'false'"
              :data-testid="row.type === 'item' ? `panel-search-row-${row.item.kind}` : `panel-search-${row.type}`"
              :data-id="row.type === 'item' ? row.item.id : undefined"
              @mousedown.prevent
              @mouseenter="active = row.index"
              @click="choose(row)"
            >
              <template v-if="row.type === 'item'">
                <span class="fx-psearch__icon" aria-hidden="true">
                  <component :is="row.item.icon" v-if="row.item.icon && row.item.kind !== 'file'" class="fx-psearch__svg" />
                  <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons + lib/fileIcons -->
                  <span v-else class="fx-psearch__svg" v-html="rowIcon(row.item)"></span>
                </span>
                <span class="fx-psearch__text">
                  <span class="fx-psearch__label"><bdi>{{ row.item.label }}</bdi></span>
                  <span v-if="row.item.detail" class="fx-psearch__detail"><bdi>{{ row.item.detail }}</bdi></span>
                </span>
              </template>
              <template v-else-if="row.type === 'files-all'">
                <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons -->
                <span class="fx-psearch__icon" aria-hidden="true"><span class="fx-psearch__svg" v-html="actionIconSvg('search')"></span></span>
                <span class="fx-psearch__text">
                  <span class="fx-psearch__label">{{ t('panelsearch.files_all', { q: parsed.text }) }}</span>
                </span>
              </template>
              <template v-else>
                <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons -->
                <span class="fx-psearch__icon" aria-hidden="true"><span class="fx-psearch__svg" v-html="actionIconSvg('search')"></span></span>
                <span class="fx-psearch__text">
                  <span class="fx-psearch__label"><bdi>{{ row.entry.query }}</bdi></span>
                </span>
                <button
                  type="button"
                  class="fx-psearch__iconbtn fx-psearch__remove"
                  tabindex="-1"
                  :aria-label="t('panelsearch.recent_remove')"
                  :title="t('panelsearch.recent_remove')"
                  data-testid="panel-search-recent-remove"
                  @click.stop="forget(row.entry)"
                >
                  <!-- eslint-disable-next-line vue/no-v-html -- static SVG from lib/actionIcons -->
                  <span class="fx-psearch__glyph" aria-hidden="true" v-html="actionIconSvg('close')"></span>
                </button>
              </template>
            </div>
          </div>

          <p v-if="loading && rows.length === 0" class="fx-psearch__note">{{ t('panelsearch.searching') }}</p>
          <p v-else-if="showEmpty" class="fx-psearch__note" data-testid="panel-search-empty">
            {{ t('panelsearch.empty', { q: parsed.text }) }}
          </p>
          <p v-if="failed" class="fx-psearch__note fx-psearch__note--warn" role="alert" data-testid="panel-search-failed">
            {{ t('panelsearch.failed') }}
          </p>
        </div>

        <div class="fx-psearch__foot">
          <span class="fx-psearch__foot-label">{{ t('panelsearch.prefixes') }}</span>
          <button
            v-for="p in PANEL_SEARCH_PREFIX_ORDER"
            :key="p"
            type="button"
            :class="['fx-psearch__chip', parsed.prefix === p && 'is-on']"
            :aria-pressed="parsed.prefix === p ? 'true' : 'false'"
            :data-testid="`panel-search-prefix-${p}`"
            @mousedown.prevent
            @click="setPrefix(parsed.prefix === p ? '' : p)"
          >{{ p }}:</button>
          <button
            v-if="parsed.words.length === 0 && parsed.kind === null && recentList.length"
            type="button"
            class="fx-psearch__clearall"
            data-testid="panel-search-recent-clear"
            @mousedown.prevent
            @click="forgetAll"
          >{{ t('panelsearch.recent_clear') }}</button>
        </div>

        <p class="fe-sr-only" role="status" aria-live="polite">{{ liveText }}</p>
      </div>
    </Teleport>
  </div>
</template>
