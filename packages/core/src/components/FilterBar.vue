<script setup lang="ts">
/**
 * FilterBar — surucu:d1, the chip row under the breadcrumb in the `drive`
 * profile.
 *
 * Four chips: Type · People · Modified · Size. Each opens a small single-choice
 * popover; a chip with a choice made carries that choice as its label, so the
 * row says what is being filtered without opening anything.
 *
 * ⚠ Type comes FIRST. The row used to open with People, which put the one
 * chip that is empty in a single-account install in front of the one every
 * listing has an answer for.
 *
 * ⚠ It was THREE for a long time, and the reason was written here: there was
 * nothing behind a People chip, because a listing row carried no owner and
 * `nodes.owner_id` was quota bookkeeping nobody serialized. That is no longer
 * true — migration 00038 made ownership a fact on the row and the listing
 * projection carries it — so the chip now filters on data the row already has,
 * like the other three. If ownership is ever taken back off the wire, this chip
 * has to go with it: a control that opens, offers names and changes nothing is
 * worse than an absent one.
 *
 * ⚠ The People options are NOT a directory listing. `Anyone`, `You` and
 * `System` are always offered; the named accounts are only the ones that
 * actually own something in the rows on screen (`peopleOptions`). Offering
 * every account on the install would put inert names in the menu and leak the
 * user list into a folder view.
 *
 * === surucu:d1-sort — the trailing edge ===============================
 * Two controls at the right of the same row: a button that NAMES the active
 * sort and flips its direction, and a chevron whose menu picks the key.
 *
 * ⚠⚠ They do not own a sort. Both reach for `lib/sortOrder`'s store for the
 * PANE they are inside, the same one the list view's own column headers reach
 * for, so the chip row and the headers are two handles on ONE piece of state
 * — sort by Size here and
 * the Size column's arrow moves, click the Name header and this button says
 * Name. A control with its own private copy of the answer is filex lesson #67
 * one level up, and it is what this row was one commit away from becoming.
 *
 * ⚠⚠ …and while a SEARCH RESULT is on screen there is no key to name. The
 * rows were ranked by the server (`order === 'relevance'`, see
 * `lib/sortOrder`), so this button prints "Relevance" instead of the key,
 * drops its direction arrow, and closes — with the reason in its own tooltip
 * and accessible name. A button reading "Name ↑" over a list that is not in
 * name order is the failure this whole row is otherwise built to avoid, and a
 * control that simply went inert would be the same lie without the wording.
 * The key itself is untouched: it is still there the moment the search is
 * left.
 *
 * ⚠ Deliberately NOT here: the reference build's "Listing actions" ⋮ that sits
 * beside these two. Its menu is New folder / New file / Upload files / Paste /
 * Select all / Deselect all — six commands this component cannot perform and
 * has no handle on. Drawing the menu here would mean six items that open,
 * offer a word and do nothing, which is the failure the People note above is
 * about. It needs events from the explorer; see the report.
 *
 * ⚠ Popovers are TELEPORTED to <body>, like ContextMenu, and positioned
 * `fixed`. An absolutely-positioned panel inside the explorer is clipped by
 * `.fe__body`'s own scroll container, which is how a menu ends up half visible
 * with its own scrollbar. ONE popover machine serves the chips and the sort
 * menu — same positioning, same outside-click, same Escape — because two of
 * them is two chances for a panel to be left open behind the other.
 *
 * ⚠ No `<style>` block. Package CSS lives in `styles/base.css` — a scoped block
 * compiles to `.cls[data-v-HASH]` and the hash does not match in the
 * web-component build, so the rules silently stop applying in every embed.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useLocale } from '../composables/useLocale';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import type { FileNode } from '../types/FileNode';
import type { DriveFilters, ModifiedFilter, SizeFilter, TypeFilter } from '../lib/fileFilters';
import { activeFilterCount, peopleOptions } from '../lib/fileFilters';
import {
  SORT_KEYS,
  setSortLocale,
  useSortStore,
  type ListingOrder,
  type SortKey,
} from '../lib/sortOrder';

const props = defineProps<{
  value: DriveFilters;
  locale: LocaleCode;
  /** Resolved theme — the teleported popover leaves the `.fe` variable scope. */
  theme?: ThemeMode;
  /** Rows currently shown / rows the folder holds, for the count chip. */
  shown?: number;
  total?: number;
  /**
   * The rows the People chip derives its named accounts from — the listing
   * BEFORE filtering, so choosing a person does not empty the menu that
   * offered them.
   *
   * ⚠ Optional. Without it the chip still offers Anyone / You / System, which
   * are the three that never depend on data; an embedder who passes nothing
   * gets a working control rather than a broken one.
   */
  files?: FileNode[];
  /* === surucu:d1-actions — the "⋮" listing menu ======================
   * ⚠ Every row in it is a command this component cannot run: the clipboard,
   * the selection and the write permissions all live in the explorer. So the
   * menu takes its two ENABLED states as props and emits the verbs, and the
   * host decides whether a row is reachable. A menu that draws "Paste" alive
   * over an empty clipboard is a menu that lies once per folder. */
  /** There is something on the clipboard to paste here. */
  canPaste?: boolean;
  /** This folder can be written to (false at a read-only root, in the trash). */
  canWrite?: boolean;
  /**
   * Where the listing behind this row got its order — see `lib/sortOrder`.
   * `relevance` (a search result) closes the sort control and makes it name
   * the order it is actually in. Omitted = an ordinary folder listing, which
   * is what every embedder that never searches has.
   */
  order?: ListingOrder;
  /* === surucu:d1-scope — the row where there is nothing to chip ==========
   *
   * ⚠⚠ `find` is not "a smaller filter bar". It is this row over a listing
   * whose rows are not files: the multi-storage ROOT (every row is a drive)
   * and HOME (three blocks of cards). Type / People / Modified / Size all
   * answer from `extension`, `mime_type`, `owner_id`, `size` and
   * `last_modified`, and a drive has none of the five — the chips would open,
   * offer ten words and narrow nothing, which is the exact failure the People
   * note above is about. The name box is the one dimension those rows DO
   * have, so it is the one that stays.
   *
   * ⚠ And it stays REACHABLE, rather than the row being dropped: "root
   * folder'da filtre barı kalsın, en kötü o bardan search kalacak — orada adam
   * isterse storage ismi aratabilir" (owner, 2026-09-13). A box that filters
   * nothing would be worse than no box; this one filters the rows in hand
   * through the same `DriveFilters.name` predicate every other listing uses.
   */
  mode?: 'full' | 'find';
  /**
   * What the name box says it narrows. The default names a FOLDER, which is
   * wrong in both `find` places — neither the drive list nor Home is one.
   * Passed already translated: the pane knows which of the two it is, the
   * catalogue lookup belongs with the caller that knows.
   */
  findLabel?: string;
}>();

const emit = defineEmits<{
  (e: 'update:value', v: DriveFilters): void;
  /* surucu:d1-actions — the listing's own verbs. Names match the events the
     navigation panel already emits for the same two commands, so the host
     wires one handler for each rather than one per menu. */
  (e: 'new-folder'): void;
  (e: 'upload'): void;
  (e: 'paste'): void;
  (e: 'select-all'): void;
}>();

const { t } = useLocale(() => props.locale);

/** surucu:d1-scope — everything except the name box is drawn only in `full`. */
const full = computed(() => (props.mode ?? 'full') === 'full');
/** The name box's placeholder AND its accessible name — one value, read twice,
 *  so the two cannot describe different things. */
const findLabel = computed(() => props.findLabel || t('filter.find'));

type Group = 'people' | 'type' | 'modified' | 'size';
/** Everything this row can open. `sort` is not a filter group — it shares the
 *  popover machine, not the filter model. */
type PopId = Group | 'sort' | 'actions';

/** The chips, in the order they are drawn. One array, so the template loop and
 *  `optionsFor` cannot disagree about how many there are. */
const GROUPS: Group[] = ['type', 'people', 'modified', 'size'];

const TYPE_OPTIONS: TypeFilter[] = [
  'any', 'folder', 'document', 'spreadsheet', 'presentation', 'pdf',
  'image', 'video', 'audio', 'archive', 'code',
];
const MODIFIED_OPTIONS: ModifiedFilter[] = ['any', 'today', '7d', '30d', 'year'];
const SIZE_OPTIONS: SizeFilter[] = ['any', 'lt1', '1to10', '10to100', 'gt100'];

/** Anyone / You / System, plus every other account owning a row on screen. */
const peopleChoices = computed(() => peopleOptions(props.files ?? []));

function optionsFor(g: Group): string[] {
  if (g === 'people') return peopleChoices.value.map((o) => o.value);
  if (g === 'type') return TYPE_OPTIONS;
  if (g === 'modified') return MODIFIED_OPTIONS;
  return SIZE_OPTIONS;
}

function optionLabel(g: Group, v: string): string {
  // A named account is labelled by its NAME, which no locale file can know.
  // The three fixed members are ordinary keys like every other group's.
  if (g === 'people' && v.startsWith('u:')) {
    return peopleChoices.value.find((o) => o.value === v)?.name || v.slice(2);
  }
  return t(`filter.${g}.${v}`);
}

/** The chip's own label: the group name until a choice is made, then the
 *  choice — a row of three identical words tells the reader nothing. */
function chipLabel(g: Group): string {
  const v = g === 'people' ? (props.value.people ?? 'any') : props.value[g];
  return v === 'any' ? t(`filter.${g}`) : optionLabel(g, v);
}

/** `value[g]` for a group whose field is optional. */
function chosen(g: Group): string {
  return g === 'people' ? (props.value.people ?? 'any') : props.value[g];
}

const activeCount = computed(() => activeFilterCount(props.value));

// ── surucu:d1-sort — the sort control ─────────────────────────────────
// Reading through the STORE OF THE PANE this row is inside, so the call
// subscribes this render to that pane's state and there is no second place
// holding a copy that could go stale.
//
// ⚠⚠ Per pane, not per bundle. The split view draws this row in both halves;
// while the key lived in a module-level singleton, choosing "Size ↓" on the
// right re-ordered the listing on the left (measured 2026-09-13). With no
// provider — one listing, a bare <FilterBar> in a test — this is the default
// store and nothing changes.
const sort = useSortStore();
const sortKey = computed<SortKey>(() => sort.key.value);
const sortAscending = computed(() => sort.dir.value === 'asc');

/** The rows behind this row are the server's ranked answer, so the key below
 *  is not what is on screen. One boolean, read by everything in the control. */
const ranked = computed(() => props.order === 'relevance');

/** "Modified", "Size" … — the SAME words the list view's column headers use
 *  (`col.*`), because they name the same sort. Over a ranked listing it is
 *  "Relevance" instead: the button must name the order it is IN. */
const sortKeyLabel = computed(() => (ranked.value ? t('sort.relevance') : t(`col.${sortKey.value}`)));
/** Read only by a screen reader, exactly as in the reference build: the arrow
 *  says the direction to everyone who can see it. Over a ranked listing the
 *  arrow is gone and this carries the REASON instead — the same sentence the
 *  tooltip shows, because "disabled" with no explanation is the state the
 *  owner specifically did not want. */
const sortDirLabel = computed(() =>
  ranked.value ? t('sort.relevance_why') : t(sortAscending.value ? 'sort.asc' : 'sort.desc'),
);

/** The sort's own `type` order is by the word the Type column prints, so the
 *  comparator has to know which catalogue that word comes from. */
watch(() => props.locale, (l) => setSortLocale(l), { immediate: true });

/**
 * ⚠ `setSort`, not `chooseSortKey`: a menu item NAMES a destination, so
 * picking the key that is already active must leave it where it is rather
 * than quietly reverse it. The button beside this menu is the toggle, and a
 * column header — where clicking the active one again is what everybody
 * expects — uses the other one.
 */
function pickSortKey(k: SortKey): void {
  sort.setSort(k);
  close(true);
}

/**
 * ⚠⚠ The two sort handles refuse to act over a RANKED listing, in the handler
 * and not only through the `disabled` attribute on the button.
 *
 * `disabled` suppresses the browser's own activation, but a click dispatched
 * in script still reaches this listener, and the damage is silent: nothing on
 * screen moves (the rows are the server's), the shared key is written to
 * `filex.list-sort`, and the FOLDER the person goes back to is re-ordered
 * behind their back. Measured 2026-09-13 on the list view's twin control — a
 * scripted click on the closed Size header left the rows untouched and still
 * turned the stored sort from Name ↑ into Size ↑.
 */
function flipSortDir(): void {
  if (ranked.value) return;
  sort.toggleSortDir();
}
function openSortMenu(): void {
  if (ranked.value) return;
  void toggle('sort');
}

/* === surucu:d1-actions — what the "⋮" offers ==========================
 * The reference build's listing menu, minus one row: it also carries "File",
 * and this explorer has no create-empty-file command anywhere — not in the
 * "+ New" menu, not in the right-click menu, not as a shortcut. Shipping the
 * word with nothing behind it is the one thing this menu was held back for.
 *
 * ⚠ Disabled rather than hidden, all of them. A menu whose height changes
 * between folders is a menu people misclick, and "Paste" greyed out says the
 * clipboard is empty — "Paste" absent says the feature does not exist.
 *
 * ⚠⚠ And there is no "Deselect all", which the reference build does carry.
 * MEASURED, not assumed: the instant anything is selected this whole row is
 * replaced by the selection bar (`.fe-selbar-slot.is-active`) — filter row
 * hidden, this button hidden — and that bar draws its own "Clear selection"
 * button. So a Deselect row here could never be reached (having a selection is
 * exactly the state that hides the menu) and would duplicate a control that IS
 * on screen at that moment. "Select all" is the opposite case and stays: it is
 * reachable precisely when it is useful, and it exists nowhere else but a
 * keystroke.
 */
const actionRows = computed(() => [
  { key: 'new-folder', label: t('toolbar.new_folder'), disabled: props.canWrite === false },
  { key: 'upload', label: t('toolbar.upload'), disabled: props.canWrite === false },
  { key: 'paste', label: t('ctx.paste'), disabled: !props.canPaste || props.canWrite === false },
  { key: 'sep', label: '', divider: true, disabled: true },
  { key: 'select-all', label: t('shortcuts.select_all'), disabled: false },
]);

function runAction(key: string): void {
  close(true);
  if (key === 'new-folder') emit('new-folder');
  else if (key === 'upload') emit('upload');
  else if (key === 'paste') emit('paste');
  else if (key === 'select-all') emit('select-all');
}

// ── the popover ──────────────────────────────────────────────────────
const openPop = ref<PopId | null>(null);
const pos = ref({ x: 0, y: 0 });
const panelEl = ref<HTMLElement | null>(null);
const anchorEls = ref<Record<string, HTMLElement | null>>({});

function setAnchorEl(id: PopId, el: unknown) {
  anchorEls.value[id] = (el as HTMLElement | null) ?? null;
}

// The open popover's data, precomputed. Reading `value[openPop]` straight
// from the template would lean on template narrowing across a Teleport, and
// `vue-tsc --noEmit` is part of this package's build.
const openGroup = computed<Group | null>(() =>
  openPop.value && openPop.value !== 'sort' && openPop.value !== 'actions'
    ? openPop.value
    : null,
);
const popOptions = computed<string[]>(() => (openGroup.value ? optionsFor(openGroup.value) : []));
const popTitle = computed(() =>
  openGroup.value
    ? t(`filter.${openGroup.value}`)
    : openPop.value === 'actions'
      ? t('filter.actions')
      : t('sort.by'),
);
function popChecked(opt: string): boolean {
  return !!openGroup.value && chosen(openGroup.value) === opt;
}
function popLabel(opt: string): string {
  return openGroup.value ? optionLabel(openGroup.value, opt) : opt;
}
function popPick(opt: string): void {
  if (openGroup.value) pick(openGroup.value, opt);
}

async function toggle(id: PopId) {
  if (openPop.value === id) {
    close();
    return;
  }
  const r = anchorEls.value[id]?.getBoundingClientRect();
  pos.value = { x: r ? r.left : 8, y: r ? r.bottom + 6 : 8 };
  openPop.value = id;
  await nextTick();
  // Keep it on screen: a chip near the right edge would otherwise open a panel
  // that runs off it, and the row is right beside the info panel.
  const panel = panelEl.value;
  if (panel) {
    const box = panel.getBoundingClientRect();
    if (box.right > window.innerWidth - 8) {
      pos.value = { ...pos.value, x: Math.max(8, window.innerWidth - 8 - box.width) };
    }
    if (box.bottom > window.innerHeight - 8) {
      const r2 = anchorEls.value[id]?.getBoundingClientRect();
      pos.value = { ...pos.value, y: Math.max(8, (r2 ? r2.top : 0) - box.height - 6) };
    }
    // `data-active` rather than a tick: the sort menu draws no check mark (the
    // button beside it already names the active key), but the keyboard still
    // has to land on it.
    panel.querySelector<HTMLElement>('[data-active="true"]')?.focus();
  }
}

function close(restoreFocus = false) {
  const id = openPop.value;
  openPop.value = null;
  if (restoreFocus && id) anchorEls.value[id]?.focus();
}

/* surucu:d1-sort — ⚠ a listing can BECOME ranked while the Sort-by menu is
 * open: the header field searches on a debounce, so the rows change under a
 * popover that is already up. Leaving it there would offer four keys the
 * listing no longer obeys, and the anchor it is positioned against has just
 * gone disabled — so it would also be unreachable by keyboard. */
watch(
  () => props.order,
  (o) => {
    if (o === 'relevance' && openPop.value === 'sort') close();
  },
);

function pick(g: Group, v: string) {
  emit('update:value', { ...props.value, [g]: v } as DriveFilters);
  close(true);
}

function clearAll() {
  localName.value = '';
  // ⚠ Every dimension, named. This literal is hand-written rather than
  // EMPTY_FILTERS because it must NOT reset the advanced-search fields the
  // dialog owns — but a dimension left out here is a chip "Clear" cannot
  // clear, which is how `people` would have stayed set forever.
  //
  // ⚠ The sort is NOT one of them. "Clear filters" un-hides rows; it does not
  // re-order the ones that were never hidden, and a button that silently threw
  // away the order you chose would be a second, invisible job.
  emit('update:value', {
    ...props.value,
    type: 'any',
    modified: 'any',
    size: 'any',
    name: '',
    people: 'any',
  });
}

/* === gorunum:v1 — "Filter in this folder…" ============================
 * The chips narrow the rows in hand by type/date/size; this narrows the same
 * rows by name, and it is part of the SAME model (`DriveFilters.name`) rather
 * than a second channel — one predicate, one "Clear", one empty state.
 *
 * ⚠ Not the header field. That one runs a server search and replaces the
 * listing with hits from below this folder; this one never leaves the folder
 * and never issues a request.
 *
 * Debounced, because every emit clears the selection in the parent (a row the
 * filter just hid would still be what Delete acts on) — doing that on each
 * keystroke would be a selection that dies while you type. */
const localName = ref(props.value.name ?? '');
watch(
  () => props.value.name ?? '',
  (v) => {
    // The parent resets the filters on navigation; the box has to follow, or
    // it keeps printing a word that no longer filters anything.
    if (v !== localName.value.trim()) localName.value = v;
  },
);
let nameTimer: ReturnType<typeof setTimeout> | undefined;
function onNameInput(ev: Event) {
  localName.value = (ev.target as HTMLInputElement).value;
  if (nameTimer) clearTimeout(nameTimer);
  nameTimer = setTimeout(() => {
    emit('update:value', { ...props.value, name: localName.value.trim() });
  }, 140);
}
function clearName() {
  if (nameTimer) clearTimeout(nameTimer);
  localName.value = '';
  emit('update:value', { ...props.value, name: '' });
}
onBeforeUnmount(() => {
  if (nameTimer) clearTimeout(nameTimer);
});

function onDocPointer(ev: PointerEvent) {
  if (!openPop.value) return;
  const el = ev.target as Node;
  if (panelEl.value?.contains(el)) return;
  if (Object.values(anchorEls.value).some((c) => c?.contains(el))) return;
  close();
}

function onKey(ev: KeyboardEvent) {
  if (ev.key === 'Escape' && openPop.value) {
    ev.stopPropagation();
    close(true);
  }
}

onMounted(() => {
  document.addEventListener('pointerdown', onDocPointer, true);
  document.addEventListener('keydown', onKey, true);
});
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocPointer, true);
  document.removeEventListener('keydown', onKey, true);
});
</script>

<template>
  <div class="fe-filterbar" :data-mode="full ? 'full' : 'find'" data-testid="filterbar">
    <!-- `display: contents` — the chips stay flex items of the row while the
         group role covers exactly the four that ARE filters. The sort control
         at the far end is not one of them and must not be announced as one. -->
    <div v-if="full" class="fe-filterbar__chips" role="group" :aria-label="t('filter.aria')">
      <button
        v-for="g in GROUPS"
        :key="g"
        :ref="(el) => setAnchorEl(g, el)"
        type="button"
        class="fe-filterbar__chip"
        :class="{ 'is-set': chosen(g) !== 'any', 'is-open': openPop === g }"
        :aria-expanded="openPop === g"
        aria-haspopup="listbox"
        :data-testid="`filter-${g}`"
        @click="toggle(g)"
      >
        <span class="fe-filterbar__label">{{ chipLabel(g) }}</span>
        <svg
          class="fe-ficon fe-filterbar__caret"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
          focusable="false"
        >
          <path d="M7 10l5 5 5-5" />
        </svg>
      </button>
    </div>

    <!-- gorunum:v1 — the name box. Same height as the chips beside it; it is
         one more way to narrow THIS folder, not a second search. -->
    <div class="fe-filterbar__find">
      <svg
        class="fe-ficon fe-filterbar__find-glass"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.9"
        stroke-linecap="round"
        aria-hidden="true"
        focusable="false"
      >
        <circle cx="11" cy="11" r="6.5" />
        <path d="M16 16l4.5 4.5" />
      </svg>
      <input
        type="search"
        class="fe-filterbar__find-input"
        :placeholder="findLabel"
        :aria-label="findLabel"
        :value="localName"
        data-testid="filter-find"
        @input="onNameInput"
        @keydown.esc.stop.prevent="clearName"
      />
    </div>

    <button
      v-if="full && activeCount > 0"
      type="button"
      class="fe-filterbar__clear"
      data-testid="filter-clear"
      @click="clearAll"
    >
      {{ t('filter.clear') }}
    </button>

    <span
      v-if="full && activeCount > 0 && typeof shown === 'number' && typeof total === 'number'"
      class="fe-filterbar__count"
      role="status"
      data-testid="filter-count"
      >{{ t('filter.count', { shown: String(shown), total: String(total) }) }}</span
    >

    <!-- surucu:d1-sort — pushed to the trailing edge of the row.
         ⚠ `full` only: a drive list and Home's three card blocks are not a
         sorted listing this row owns, so a button naming a sort key would be
         naming one nothing on screen obeys — the same lie the ranked-search
         case above is built to avoid. -->
    <div v-if="full" class="fe-filterbar__sort">
      <button
        :ref="(el) => setAnchorEl('sort', el)"
        type="button"
        class="fe-filterbar__sortdir"
        :class="{ 'is-ranked': ranked }"
        :disabled="ranked"
        :title="ranked ? sortDirLabel : undefined"
        data-testid="sort-dir"
        @click="flipSortDir()"
      >
        <span>{{ sortKeyLabel }}</span>
        <!-- The accessible name becomes "Modified, Descending". The arrow
             carries the same fact for everyone who can see it, so it is not
             repeated as a title. Ranked: "Relevance, <why it is closed>" —
             the reason reaches a screen reader through the same channel it
             reaches a mouse, rather than living only in a tooltip. -->
        <span class="fe-sr-only">, {{ sortDirLabel }}</span>
        <svg
          v-if="!ranked"
          class="fe-ficon fe-filterbar__sortarrow"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
          focusable="false"
        >
          <template v-if="sortAscending">
            <path d="M12 19V5" />
            <path d="M5 12l7-7 7 7" />
          </template>
          <template v-else>
            <path d="M12 5v14" />
            <path d="M19 12l-7 7-7-7" />
          </template>
        </svg>
      </button>
      <button
        type="button"
        class="fe-filterbar__sortpick"
        :aria-label="ranked ? sortDirLabel : t('sort.by')"
        :title="ranked ? sortDirLabel : t('sort.by')"
        :disabled="ranked"
        aria-haspopup="menu"
        :aria-expanded="openPop === 'sort'"
        data-testid="sort-menu"
        @click="openSortMenu()"
      >
        <svg
          class="fe-ficon fe-filterbar__caret"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
          focusable="false"
        >
          <path d="M7 10l5 5 5-5" />
        </svg>
      </button>
    </div>

    <!-- surucu:d1-actions — the listing's own menu, at the very end of the row.
         ⚠ VERTICAL dots, and the header's overflow is HORIZONTAL dots, which is
         the whole distinction: in this product ⋮ means "act on this thing" (it
         is the glyph on every file row) and ⋯ means "settings for the app". Two
         menus one row apart would otherwise read as two doors to one place. -->
    <button
      v-if="full"
      :ref="(el) => setAnchorEl('actions', el)"
      type="button"
      class="fe-filterbar__actions"
      :aria-label="t('filter.actions')"
      :title="t('filter.actions')"
      aria-haspopup="menu"
      :aria-expanded="openPop === 'actions'"
      data-testid="listing-actions"
      @click="toggle('actions')"
    >
      <svg
        class="fe-ficon"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        aria-hidden="true"
        focusable="false"
      >
        <circle cx="12" cy="5.5" r="1.2" fill="currentColor" stroke="none" />
        <circle cx="12" cy="12" r="1.2" fill="currentColor" stroke="none" />
        <circle cx="12" cy="18.5" r="1.2" fill="currentColor" stroke="none" />
      </svg>
    </button>

    <Teleport to="body">
      <div
        v-if="openPop"
        ref="panelEl"
        class="fe-filterpop"
        :class="{
          'fe--theme-light': theme === 'light',
          'fe--theme-dark': theme === 'dark',
          'fe-filterpop--sort': openPop === 'sort',
          'fe-filterpop--actions': openPop === 'actions',
        }"
        :role="openPop === 'sort' || openPop === 'actions' ? 'menu' : 'listbox'"
        :aria-label="popTitle"
        :style="{ left: pos.x + 'px', top: pos.y + 'px' }"
      >
        <template v-if="openPop === 'actions'">
          <template v-for="a in actionRows" :key="a.key">
            <hr v-if="a.divider" class="fe-filterpop__rule" aria-hidden="true" />
            <button
              v-else
              type="button"
              class="fe-filterpop__item"
              role="menuitem"
              :disabled="a.disabled"
              :aria-disabled="a.disabled"
              :data-active="'false'"
              :data-testid="`listing-action-${a.key}`"
              @click="runAction(a.key)"
            >
              <span>{{ a.label }}</span>
            </button>
          </template>
        </template>
        <template v-else-if="openPop === 'sort'">
          <button
            v-for="k in SORT_KEYS"
            :key="k"
            type="button"
            class="fe-filterpop__item"
            role="menuitem"
            :data-active="sortKey === k ? 'true' : 'false'"
            :data-testid="`sort-opt-${k}`"
            @click="pickSortKey(k)"
          >
            <span>{{ t(`col.${k}`) }}</span>
          </button>
        </template>
        <template v-else>
          <button
            v-for="opt in popOptions"
            :key="opt"
            type="button"
            class="fe-filterpop__item"
            role="option"
            :aria-selected="popChecked(opt)"
            :data-checked="popChecked(opt) ? 'true' : 'false'"
            :data-active="popChecked(opt) ? 'true' : 'false'"
            :data-testid="`filter-opt-${opt}`"
            @click="popPick(opt)"
          >
            <span class="fe-filterpop__tick" aria-hidden="true">{{ popChecked(opt) ? '✓' : '' }}</span>
            <span>{{ popLabel(opt) }}</span>
          </button>
        </template>
      </div>
    </Teleport>
  </div>
</template>
