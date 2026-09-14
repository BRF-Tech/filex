<script setup lang="ts">
/**
 * gorunum:v1-advsearch — the Advanced search dialog.
 *
 * ⚠⚠ The rule this component was built under: **only offer what the backend
 * can actually do.** A control wired to a parameter the server never reads
 * looks identical to one that works and quietly changes nothing, which is
 * worse than not shipping it (the same sentence already governs the filter
 * row — see the header of `lib/fileFilters.ts`).
 *
 * The split is still real, and it is still said ON SCREEN — but as one
 * sentence under the count rather than as two half-page captions over two
 * stacked halves (the reference shell's layout, owner's call 2026-09-12:
 * "görüntü birebir yapalım"). What the halves are:
 *
 *   1. What the SERVER answers — the text, the tag filters and the scope.
 *      Those are the whole of filex's search surface (`handlers/search.go`
 *      `searchRequest`: q/query, storage_id, limit, scope; the query text may
 *      carry `tag:` / `-tag:`). Drawn at the top: one wide field, one row of
 *      scope tabs, and the tag boxes.
 *   2. What NARROWS THE ROWS THAT CAME BACK — type, modified, size, folder.
 *      No endpoint reads a date, size, mime or path parameter, so these run
 *      here, over the hits. Drawn in the two columns, and named for what they
 *      are by `advsearch.count.cost` directly under the number they produced:
 *      "Type, date, size and folder are applied to the rows that come back,
 *      not by the server."
 *
 * ⚠ Moving that sentence, or letting a redesign drop it, turns four honest
 * controls into four that quietly promise a search the server never runs.
 *
 * **Owner IS drawn now, and the reason it was not is gone.** This block used
 * to read "there is no per-node owner on the wire". Migration 00038 made
 * ownership a real fact on the row and the listing projection carries
 * `owner_id` / `owner_name` / `owner_self` on every row that has one, so the
 * Owner select answers from a field the rows ALREADY carry — the same bargain
 * as type, date and size, and it is named in the same sentence under the
 * count. ⚠ It is offered on NAME searches only: the explorer maps a content
 * hit onto the listing shape by hand (`FileExplorer.advHitToNode`) and that
 * mapping does not carry the owner through, so under the content scopes every
 * row would read as "System" — a wrong answer, which is worse than a control
 * that says why it is off.
 *
 * Still not drawn, each for a reason that was MEASURED against the reference
 * build on 2026-09-13 rather than assumed:
 *   - **A "Paths" scope tab.** The reference sends `scope:"path"` and its own
 *     server answers it with the byte-identical result set `scope:"name"`
 *     returns — measured over four queries incl. `Code/api`, which matches a
 *     path and no name. Paths are matched INSIDE the name scope (the index
 *     queries the `path` field there, `search.nameQuery`), so the tab renames
 *     a search rather than adding one.
 *   - **A "Tags" scope tab.** `search.ParseScope` is name|content|all; the
 *     reference's `scope:"tags"` returns nothing for every query on its own
 *     install. Ours has the stronger thing already: two boxes that become
 *     `tag:` / `-tag:` tokens the backend resolves against the database.
 *   - **"Match whole phrase".** The query language has no phrase operator. The
 *     only quoting the parser knows keeps a `tag:"two words"` value together
 *     (`search.splitQuery`); quotes around free text stay in the text.
 *   - **A storage chooser and a "search in / all storages" scope.** Both need
 *     the explorer to send the search somewhere else — `api.search(target, …)`
 *     builds the target from where the explorer is standing, and only the
 *     multi-storage virtual root searches across drives. A picker drawn here
 *     could narrow the rows that came back, which for a single-storage search
 *     can only ever mean "the drive you are already on". The endpoint half is
 *     ready (see `handlers/search.go`: a hit now carries its storage NAME);
 *     the routing half is `FileExplorer.advTarget`, and is not this
 *     component's to write.
 *
 * The live count is a REAL query, debounced — the same search the button runs,
 * counted after the client-side half. That costs a round trip, so the dialog
 * prints that it does instead of presenting the number as free.
 */
import { computed, ref, watch, onBeforeUnmount } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from '../modals/Modal.vue';
import { actionIconSvg } from '../lib/actionIcons';
import {
  EMPTY_FILTERS,
  type AroundSpan,
  type ModifiedFilter,
  type PathMode,
  type PeopleFilter,
  type PeopleOption,
  type SizeFilter,
  type TypeFilter,
} from '../lib/fileFilters';
import {
  advQueryString,
  advSearchEmpty,
  emptyAdvSearch,
  parseTagList,
  type AdvCountResult,
  type AdvScope,
  type AdvSearchRequest,
} from '../lib/advSearch';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  theme?: 'light' | 'dark' | 'auto';
  /** Seeded from the toolbar field so the dialog continues the search in progress. */
  initialQuery?: string;
  /** Human name of the folder the dialog was opened from ("Documents"). */
  folderLabel?: string;
  /** Adapter-qualified folder the "only here" / "skip here" choice measures
   *  against (`qldemo://Documents`). Captured at open: a search rebases the
   *  explorer to the storage root, so by the time the filter runs the folder
   *  the user meant is no longer where the explorer is standing. */
  pathBase?: string;
  /**
   * Whether the content scopes may be offered.
   *
   * ⚠ Not a preference. Content search exists only on `/api/files/search`,
   * and a hit from there reaches the explorer without the drive it came from —
   * with more than one storage mounted the explorer cannot tell which drive a
   * hit is on, and a row labelled with the wrong drive is worse than a missing
   * feature. With one storage every hit is necessarily that storage's, so the
   * scopes are exact. The caller decides; this component only draws it.
   *
   * ⚠ The ENDPOINT half of that gap is closed as of 2026-09-13: a hit now
   * carries `storage` (the drive's name) as well as its id, the same way the
   * starred and recently-opened handlers have always answered
   * (`handlers/search.go` `describeHits`). What is left is the explorer's own
   * hand-written mapping, `FileExplorer.advHitToNode`, which builds a listing
   * row from the hit and does not read that field — so the caller still has to
   * pass `false` on a multi-storage install until it does.
   */
  contentSearch?: boolean;
  /** Runs the real query and returns what it found. Owned by the explorer,
   *  which is the only thing holding the API client. */
  count: (req: AdvSearchRequest) => Promise<AdvCountResult>;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', req: AdvSearchRequest): void;
}>();

const { t } = useLocale(() => props.locale);

/* ── form state ──────────────────────────────────────────────────────────── */

/**
 * The query box, focused when the dialog opens.
 *
 * ⚠ Not a flourish — it replaces something that broke. Modal autofocuses the
 * first focusable element inside its card, which used to be the × in its own
 * title bar; this dialog folds that bar away (it draws its own head), and a
 * `display:none` button cannot take focus, so opening the dialog left focus on
 * the opener and nothing in the dialog was focused at all. The field is the
 * right answer anyway: a search dialog opens ready to be typed into. The delay
 * clears Modal's own 30ms autofocus attempt rather than racing it.
 */
const queryEl = ref<HTMLInputElement | null>(null);

const text = ref('');
const scope = ref<AdvScope>('name');
const tagsRaw = ref('');
const excludeRaw = ref('');
const type = ref<TypeFilter>('any');
const modified = ref<ModifiedFilter>('any');
const aroundDate = ref('');
const aroundSpan = ref<AroundSpan>('d1');
/**
 * Whose rows to keep.
 *
 * ⚠ The same `PeopleFilter` the filter row's People pill uses, narrowed by the
 * same `matchesPeople` predicate — two definitions of "files you put here"
 * would be two products. `any` / `me` / `system` are offered unconditionally
 * because a menu whose members appear and disappear is a menu nobody can
 * learn; the per-account members are the ones that would be inert, so they are
 * the ones that have to come from data (`peopleOptions`, below, reads them off
 * the run that produced the count).
 */
const people = ref<PeopleFilter>('any');
const size = ref<SizeFilter>('any');
/* ⚠ `string | number`, not `string`. Vue's v-model casts the value of an
   `<input type="number">` to a NUMBER on its own — no `.number` modifier
   needed (`runtime-dom`: `castToNumber = number || vnode.props.type ===
   'number'`) — and hands back the raw string only when it does not parse, so
   an empty field is `''` and a typed one is `1`. Typing these as `string` and
   calling a string method on the value is not a type error the compiler can
   see, and the failure is silent: see `bytes()` below. */
const sizeFrom = ref<string | number>('');
const sizeTo = ref<string | number>('');
const sizeUnit = ref<'kb' | 'mb' | 'gb'>('mb');
const pathMode = ref<PathMode>('any');

const TYPE_OPTIONS: TypeFilter[] = [
  'any', 'folder', 'document', 'spreadsheet', 'presentation', 'pdf',
  'image', 'video', 'audio', 'archive', 'code',
];
const MODIFIED_OPTIONS: ModifiedFilter[] = ['any', 'today', '7d', '30d', 'year', 'around'];
const SIZE_OPTIONS: SizeFilter[] = ['any', 'lt1', '1to10', '10to100', 'gt100', 'range'];
const SPAN_OPTIONS: AroundSpan[] = ['h1', 'd1', 'w1'];
const UNIT_FACTOR: Record<'kb' | 'mb' | 'gb', number> = {
  kb: 1024,
  mb: 1024 ** 2,
  gb: 1024 ** 3,
};

/**
 * Scopes in the order they are OFFERED — which is not the same thing as which
 * one is selected. `reset()` picks `name` either way, because it is the only
 * scope the folder-search endpoint can serve; the order below is the reference
 * shell's (widest first), and when content search is off the list collapses to
 * the single scope that exists rather than drawing two tabs that would do the
 * same thing.
 */
const scopeOptions = computed<AdvScope[]>(() =>
  props.contentSearch ? ['all', 'content', 'name'] : ['name'],
);

/**
 * The tab glyph. Keys from `lib/actionIcons` only — the reference shell puts a
 * mark on every scope tab, and an icon per tab drawn from the shared set keeps
 * this row in the same voice as the menus rather than inventing a second one.
 */
const SCOPE_ICON: Record<AdvScope, string> = {
  all: 'search',
  content: 'preview',
  name: 'copy-path',
};

/** The three answers to "where does the folder come into it". */
const PATH_MODES: PathMode[] = ['any', 'here', 'skip'];

/** The People members that need no data behind them. Named by the catalogue,
 *  not by the directory — which is also why offering them cannot leak one. */
const PEOPLE_FIXED: PeopleFilter[] = ['any', 'me', 'system'];

/**
 * Whether the Owner select can be trusted right now.
 *
 * ⚠ A capability check, not a preference — the same shape as `contentSearch`.
 * A NAME search comes back through the manager's search action, whose rows are
 * the listing projection and carry `owner_id` / `owner_name` / `owner_self`.
 * The content scopes come back through `/api/files/search`, and the explorer
 * maps those hits onto the listing shape by hand; that mapping carries no
 * owner, so every row would read as "System" and both `me` and `system` would
 * answer the wrong question confidently. Off, and it says so, until the
 * mapping carries it (`FileExplorer.advHitToNode`).
 */
const ownerFilterable = computed(() => scope.value === 'name');

/** Leaving the name scope takes the Owner filter with it — a disabled control
 *  that goes on filtering is the dishonest half of disabling it. */
watch(ownerFilterable, (ok) => {
  if (!ok) people.value = 'any';
});

function reset() {
  text.value = '';
  scope.value = 'name';
  tagsRaw.value = '';
  excludeRaw.value = '';
  type.value = 'any';
  modified.value = 'any';
  aroundDate.value = '';
  aroundSpan.value = 'd1';
  size.value = 'any';
  sizeFrom.value = '';
  sizeTo.value = '';
  sizeUnit.value = 'mb';
  pathMode.value = 'any';
  people.value = 'any';
}

/* ── continuing the search that is actually running ───────────────────────
 *
 * ⚠⚠ The bug this exists for. Opening the dialog over a live advanced filter
 * called `reset()`, so it came up saying `Type = any` and printed a count for
 * a search NOBODY had run — measured at "14 matching items" over a listing
 * holding 1. Worse than the wrong number: pressing Search from that state
 * submitted the reset form and silently dropped the filter that was in force.
 * The dialog could restart a search and never continue one.
 *
 * The fix is self-contained on purpose. The applied state lives in the
 * explorer (`advFilters` / `advScope`) and could be handed back as a prop, but
 * then the dialog would only remember in the ONE shell that passed it — the
 * desktop app, the fm.example.com explorer and the work/fishapp embeds all mount
 * this same component, and a continuity that works in one of them is two
 * products. So the dialog remembers what IT submitted.
 *
 * `query` is the honesty check, not a nicety. The toolbar field holds exactly
 * the wire query a submit produced (`applyAdvancedSearch` writes it there),
 * and typing in that field CLEARS the advanced filters (`onToolbarSearch`).
 * So "the seed still equals what I submitted" is the strongest available
 * evidence that the filter we remember is the filter in force; anything else
 * — a cleared box, an edited query, a plain search — falls back to `reset()`.
 */
interface FormState {
  text: string;
  scope: AdvScope;
  tagsRaw: string;
  excludeRaw: string;
  type: TypeFilter;
  modified: ModifiedFilter;
  aroundDate: string;
  aroundSpan: AroundSpan;
  size: SizeFilter;
  sizeFrom: string | number;
  sizeTo: string | number;
  sizeUnit: 'kb' | 'mb' | 'gb';
  pathMode: PathMode;
  people: PeopleFilter;
}

function snapshot(): FormState {
  return {
    text: text.value,
    scope: scope.value,
    tagsRaw: tagsRaw.value,
    excludeRaw: excludeRaw.value,
    type: type.value,
    modified: modified.value,
    aroundDate: aroundDate.value,
    aroundSpan: aroundSpan.value,
    size: size.value,
    sizeFrom: sizeFrom.value,
    sizeTo: sizeTo.value,
    sizeUnit: sizeUnit.value,
    pathMode: pathMode.value,
    people: people.value,
  };
}

function restore(s: FormState) {
  text.value = s.text;
  scope.value = s.scope;
  tagsRaw.value = s.tagsRaw;
  excludeRaw.value = s.excludeRaw;
  type.value = s.type;
  modified.value = s.modified;
  aroundDate.value = s.aroundDate;
  aroundSpan.value = s.aroundSpan;
  size.value = s.size;
  sizeFrom.value = s.sizeFrom;
  sizeTo.value = s.sizeTo;
  sizeUnit.value = s.sizeUnit;
  pathMode.value = s.pathMode;
  people.value = s.people;
}

/** The form as it stood when Search was last pressed, with the wire query it
 *  produced. Not a ref: nothing renders it, and a reactive copy of the whole
 *  form would re-run the count watcher for a value the user cannot see. */
let submitted: { query: string; form: FormState } | null = null;

// Opening either CONTINUES the advanced search that is running, or seeds a
// fresh one from whatever the toolbar held.
watch(
  () => props.open,
  (v) => {
    if (!v) return;
    const seed = props.initialQuery ?? '';
    if (submitted && submitted.query !== '' && submitted.query === seed) {
      restore(submitted.form);
    } else {
      reset();
      text.value = seed;
    }
    counted.value = null;
    countError.value = false;
    setTimeout(() => queryEl.value?.focus(), 60);
  },
);

/**
 * Bytes for one end of the custom range, or null when it is left open.
 *
 * ⚠⚠ Takes `unknown` on purpose. This read `raw.trim()` and typed the
 * parameter `string`, which is what the ref was declared as — and Vue casts a
 * number input's value to a NUMBER, so `trim` was not a function. The throw
 * happened inside the `request` computed, which is what the count watcher
 * reads, so the watcher's getter threw before it could fire: the dialog kept
 * printing the PREVIOUS count and looked like a filter that simply did not
 * narrow. Measured in a real browser on 2026-09-12 — "to = 0 MB" over two
 * files of 16 and 184 bytes still said "2 matching items". Nothing in the
 * console of the page under test, nothing in vue-tsc, nothing in the unit
 * suite: a filter that is a no-op is indistinguishable from a filter that
 * matched everything unless you check it against rows you know it must drop.
 *
 * An empty field is `''` and must stay "open end", never `Number('') === 0` —
 * a zero ceiling means nothing passes.
 */
function bytes(raw: unknown): number | null {
  const s = String(raw ?? '').trim();
  if (!s) return null;
  const n = Number(s);
  if (!Number.isFinite(n) || n < 0) return null;
  return Math.round(n * UNIT_FACTOR[sizeUnit.value]);
}

const request = computed<AdvSearchRequest>(() => ({
  ...emptyAdvSearch(props.pathBase ?? ''),
  text: text.value,
  tags: parseTagList(tagsRaw.value),
  excludeTags: parseTagList(excludeRaw.value),
  scope: scope.value,
  filters: {
    ...EMPTY_FILTERS,
    type: type.value,
    modified: modified.value,
    aroundDate: aroundDate.value,
    aroundSpan: aroundSpan.value,
    size: size.value,
    sizeMin: size.value === 'range' ? bytes(sizeFrom.value) : null,
    sizeMax: size.value === 'range' ? bytes(sizeTo.value) : null,
    pathMode: pathMode.value,
    pathBase: props.pathBase ?? '',
    /* ⚠ Read through the guard, not off the ref. The `ownerFilterable` watch
       below already clears the choice when the scope leaves `name`, so this is
       the SECOND lock, not the first: a disabled select cannot be changed but
       it can still hold a value chosen before the scope moved, and a filter
       that outlives its own control narrows a search nobody asked it to. The
       two are worth keeping apart — one is what the user sees change, the
       other is what actually reaches the predicate. */
    people: ownerFilterable.value ? people.value : 'any',
  },
}));

const nothingToAsk = computed(() => advSearchEmpty(request.value));

/** Exactly what would go on the wire, shown to the user. The tag box becomes
 *  `tag:` tokens and there is no reason to hide that — somebody who learns the
 *  syntax here can type it straight into the toolbar next time. */
const wireQuery = computed(() => advQueryString(request.value));

/* ── the live count: a real search, debounced ────────────────────────────── */

const counted = ref<AdvCountResult | null>(null);
const counting = ref(false);
const countError = ref(false);

/**
 * The per-account People members, offered only when a real search actually
 * returned rows belonging to somebody else.
 *
 * ⚠ Derived from the count run, never from a directory. The dialog holds no
 * listing of its own, and offering every account on the install would put
 * names in the menu that cannot narrow anything here — the same reason
 * `peopleOptions` in `lib/fileFilters.ts` derives the pill's members from the
 * rows in hand. A count result that does not report them (the field is
 * optional, and the explorer does not fill it yet) simply offers the three
 * that need no data.
 */
const peopleOptions = computed<PeopleOption[]>(() => [
  ...PEOPLE_FIXED.map((value) => ({ value })),
  ...(counted.value?.people ?? []),
]);

/** The label for one member: the fixed three are named by the catalogue, an
 *  account by itself. A nameless account falls back to the neutral word rather
 *  than printing `u:7` at somebody. */
function peopleLabel(o: PeopleOption): string {
  if (o.value === 'any' || o.value === 'me' || o.value === 'system') {
    return t(`filter.people.${o.value}`);
  }
  return o.name || t('filter.people.someone');
}

/**
 * ⚠ A member can leave the menu — a later count over different rows may not
 * contain that account at all — and a `<select>` whose value is no longer one
 * of its options renders BLANK while it goes on filtering. Falling back to
 * `any` keeps what is shown and what is applied the same thing.
 */
watch(peopleOptions, (opts) => {
  if (!opts.some((o) => o.value === people.value)) people.value = 'any';
});
let timer: ReturnType<typeof setTimeout> | null = null;
/** Only the newest run may write the answer — a slow early query landing after
 *  a fast later one would print a count for a search nobody is looking at. */
let seq = 0;

function scheduleCount() {
  if (timer) clearTimeout(timer);
  countError.value = false;
  if (nothingToAsk.value) {
    counted.value = null;
    counting.value = false;
    return;
  }
  counting.value = true;
  timer = setTimeout(run, 450);
}

async function run() {
  const mine = ++seq;
  const req = request.value;
  try {
    const res = await props.count(req);
    if (mine !== seq) return;
    counted.value = res;
  } catch {
    if (mine !== seq) return;
    counted.value = null;
    countError.value = true;
  } finally {
    if (mine === seq) counting.value = false;
  }
}

/**
 * ⚠ The guard the `bytes()` bug earned. A throw while building `request` used
 * to happen inside this watcher's own getter, so the watcher never fired and
 * the dialog went on showing the count from before the change — a broken
 * filter wearing the face of a filter that matched everything. A failure is
 * now REPORTED (the count line says it could not run) instead of leaving a
 * stale number on screen. `'ERR'` is a constant, not a timestamp: a value that
 * changed every evaluation would re-trigger the watcher forever.
 */
watch(
  () => {
    if (!props.open) return '';
    try {
      return JSON.stringify(request.value);
    } catch {
      return 'ERR';
    }
  },
  (v) => {
    if (!v) return;
    if (v === 'ERR') {
      if (timer) clearTimeout(timer);
      seq++;
      counted.value = null;
      counting.value = false;
      countError.value = true;
      return;
    }
    scheduleCount();
  },
);

onBeforeUnmount(() => {
  if (timer) clearTimeout(timer);
});

/**
 * The count sentence.
 *
 * ⚠ This dialog's most common real answer on a small folder IS one, and "1
 * matching items" is what a bare `{n}` message produces. Both keys have a
 * `_one` form, and `t()` picks it from `n` (composables/useLocale →
 * countedKey) — the rule that used to be written out here, once per caller.
 */
const countLabel = computed(() => {
  const c = counted.value;
  if (!c) return '';
  return t(c.capped ? 'advsearch.count.capped' : 'advsearch.count.result', { n: String(c.count) });
});

function submit() {
  if (nothingToAsk.value) return;
  // Remember BEFORE emitting: the parent closes the dialog synchronously, and
  // the `open` watcher that reads this runs on the next opening.
  submitted = { query: wireQuery.value, form: snapshot() };
  emit('submit', request.value);
}
</script>

<template>
  <Modal
    :open="open"
    :title="t('advsearch.title')"
    size="lg"
    :theme="theme"
    @close="emit('close')"
  >
    <form class="fe-advsearch" @submit.prevent="submit">
      <!-- ── the dialog's own head ───────────────────────────────────────
           Modal draws a plain title bar; this dialog needs the mark, the
           title and the sentence under it as ONE block across the full
           width, so it draws its own and the stylesheet folds Modal's away
           (`.fe-modal__card:has(.fe-advsearch) .fe-modal__head`). The h2 up
           there stays in the DOM on purpose — it is what `aria-labelledby`
           on the dialog points at, so the name survives the fold. -->
      <header class="fe-advsearch__head">
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-advsearch__head-icon" aria-hidden="true" v-html="actionIconSvg('search')"></span>
        <div class="fe-advsearch__head-text">
          <p class="fe-advsearch__head-title">{{ t('advsearch.title') }}</p>
          <p class="fe-advsearch__head-sub">{{ t('advsearch.subtitle') }}</p>
        </div>
        <button
          type="button"
          class="fe-advsearch__head-close"
          :title="t('advsearch.close')"
          :aria-label="t('advsearch.close')"
          @click="emit('close')"
        >
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
          <span aria-hidden="true" v-html="actionIconSvg('close')"></span>
        </button>
      </header>

      <!-- ── what the server answers: the text and the scope ─────────────
           One wide field and one row of tabs, because this is the whole of
           the question that leaves the browser. -->
      <label class="fe-advsearch__box">
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-advsearch__glass" aria-hidden="true" v-html="actionIconSvg('search')"></span>
        <input
          ref="queryEl"
          v-model="text"
          type="search"
          class="fe-advsearch__input"
          :placeholder="t('advsearch.query.placeholder')"
          :aria-label="t('advsearch.query.label')"
          data-testid="advsearch-query"
        />
      </label>

      <div
        class="fe-advsearch__seg fe-advsearch__seg--tabs"
        role="tablist"
        :aria-label="t('advsearch.scope.label')"
      >
        <button
          v-for="s in scopeOptions"
          :key="s"
          type="button"
          role="tab"
          class="fe-advsearch__seg-btn"
          :class="{ 'is-active': scope === s }"
          :aria-selected="scope === s"
          :data-testid="`advsearch-scope-${s}`"
          @click="scope = s"
        >
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
          <span class="fe-advsearch__seg-icon" aria-hidden="true" v-html="actionIconSvg(SCOPE_ICON[s])"></span>
          {{ t(`advsearch.scope.${s}`) }}
        </button>
      </div>

      <!-- ── the two columns ─────────────────────────────────────────────
           ⚠ Every control below the tabs narrows the rows that CAME BACK —
           no endpoint reads a date, a size, a mime or a path. The dialog
           says so in one sentence under the count rather than shouting it
           over two half-page captions; the sentence is the part that has to
           survive, not the caption. -->
      <div class="fe-advsearch__cols">
        <div class="fe-advsearch__col">
          <div class="fe-advsearch__group">
            <label class="fe-advsearch__field">
              <span class="fe-advsearch__label">{{ t('filter.modified') }}</span>
              <select v-model="modified" class="fe-input" data-testid="advsearch-modified">
                <option v-for="o in MODIFIED_OPTIONS" :key="o" :value="o">
                  {{ t(`filter.modified.${o}`) }}
                </option>
              </select>
            </label>
            <div v-if="modified === 'around'" class="fe-advsearch__field">
              <span class="fe-advsearch__sublabel">{{ t('advsearch.around.label') }}</span>
              <div class="fe-advsearch__pair">
                <input
                  v-model="aroundDate"
                  type="datetime-local"
                  class="fe-input"
                  :aria-label="t('advsearch.around.date')"
                  data-testid="advsearch-around-date"
                />
                <div class="fe-advsearch__seg" role="group" :aria-label="t('advsearch.around.label')">
                  <button
                    v-for="s in SPAN_OPTIONS"
                    :key="s"
                    type="button"
                    class="fe-advsearch__seg-btn"
                    :class="{ 'is-active': aroundSpan === s }"
                    :aria-pressed="aroundSpan === s"
                    @click="aroundSpan = s"
                  >
                    {{ t(`advsearch.around.${s}`) }}
                  </button>
                </div>
              </div>
            </div>
          </div>

          <label class="fe-advsearch__field">
            <span class="fe-advsearch__label">{{ t('filter.type') }}</span>
            <select v-model="type" class="fe-input" data-testid="advsearch-type">
              <option v-for="o in TYPE_OPTIONS" :key="o" :value="o">{{ t(`filter.type.${o}`) }}</option>
            </select>
          </label>

          <!-- Whose it is. Same union and same predicate as the filter row's
               People pill, so "files you put here" means one thing in this
               product. Off under the content scopes, and it says why rather
               than vanishing — see `ownerFilterable`. -->
          <div class="fe-advsearch__group">
            <label class="fe-advsearch__field">
              <span class="fe-advsearch__label">{{ t('filter.people') }}</span>
              <select
                v-model="people"
                class="fe-input"
                :disabled="!ownerFilterable"
                data-testid="advsearch-people"
              >
                <option v-for="o in peopleOptions" :key="o.value" :value="o.value">
                  {{ peopleLabel(o) }}
                </option>
              </select>
            </label>
            <p v-if="!ownerFilterable" class="fe-advsearch__hint fe-advsearch__hint--block">
              {{ t('advsearch.people.name_only') }}
            </p>
          </div>

          <div class="fe-advsearch__group">
            <label class="fe-advsearch__field">
              <span class="fe-advsearch__label">{{ t('advsearch.tags.label') }}</span>
              <input
                v-model="tagsRaw"
                type="text"
                class="fe-input"
                :placeholder="t('advsearch.tags.placeholder')"
                data-testid="advsearch-tags"
              />
            </label>
            <label class="fe-advsearch__field">
              <span class="fe-advsearch__label">{{ t('advsearch.tags.exclude') }}</span>
              <input
                v-model="excludeRaw"
                type="text"
                class="fe-input"
                :placeholder="t('advsearch.tags.exclude_placeholder')"
                data-testid="advsearch-extags"
              />
            </label>
            <p class="fe-advsearch__hint fe-advsearch__hint--block">{{ t('advsearch.tags.hint') }}</p>
          </div>
        </div>

        <div class="fe-advsearch__col">
          <div class="fe-advsearch__group">
            <label class="fe-advsearch__field">
              <span class="fe-advsearch__label">{{ t('filter.size') }}</span>
              <select v-model="size" class="fe-input" data-testid="advsearch-size">
                <option v-for="o in SIZE_OPTIONS" :key="o" :value="o">{{ t(`filter.size.${o}`) }}</option>
              </select>
            </label>
            <div v-if="size === 'range'" class="fe-advsearch__field">
              <span class="fe-advsearch__sublabel">{{ t('advsearch.size.range_label') }}</span>
              <div class="fe-advsearch__pair">
                <input
                  v-model="sizeFrom"
                  type="number"
                  min="0"
                  class="fe-input fe-advsearch__num"
                  :placeholder="t('advsearch.size.from')"
                  :aria-label="t('advsearch.size.from')"
                  data-testid="advsearch-size-from"
                />
                <span class="fe-advsearch__dash" aria-hidden="true">–</span>
                <input
                  v-model="sizeTo"
                  type="number"
                  min="0"
                  class="fe-input fe-advsearch__num"
                  :placeholder="t('advsearch.size.to')"
                  :aria-label="t('advsearch.size.to')"
                  data-testid="advsearch-size-to"
                />
                <select v-model="sizeUnit" class="fe-input fe-advsearch__unit" :aria-label="t('filter.size')">
                  <option value="kb">{{ t('unit.kb') }}</option>
                  <option value="mb">{{ t('unit.mb') }}</option>
                  <option value="gb">{{ t('unit.gb') }}</option>
                </select>
              </div>
            </div>
          </div>

          <!-- The folder. A segmented trio rather than a menu, because the
               three answers are the whole of the choice and the sentence
               under it then names the folder the choice is about — the
               folder captured when the dialog opened, not wherever the
               explorer has since been rebased to. -->
          <div class="fe-advsearch__group">
            <span class="fe-advsearch__label">{{ t('advsearch.where.label') }}</span>
            <div
              class="fe-advsearch__seg fe-advsearch__seg--wide"
              role="group"
              :aria-label="t('advsearch.where.label')"
              data-testid="advsearch-where"
            >
              <button
                v-for="m in PATH_MODES"
                :key="m"
                type="button"
                class="fe-advsearch__seg-btn"
                :class="{ 'is-active': pathMode === m }"
                :aria-pressed="pathMode === m"
                :data-testid="`advsearch-where-${m}`"
                @click="pathMode = m"
              >
                {{ t(`advsearch.where.${m}_short`) }}
              </button>
            </div>
            <span class="fe-advsearch__hint">
              {{ t(`advsearch.where.${pathMode}`, { folder: folderLabel || '' }) }}
            </span>
          </div>

          <!-- What "contents" means here. This is the 200 KB sentence, kept
               where the reference shell keeps its content options: a reader
               who picks the Content tab has to learn what the index covers
               BEFORE an empty result teaches them. -->
          <div class="fe-advsearch__group">
            <p class="fe-advsearch__grouphead">
              <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
              <span class="fe-advsearch__grouphead-icon" aria-hidden="true" v-html="actionIconSvg('preview')"></span>
              {{ t('advsearch.content.heading') }}
            </p>
            <p class="fe-advsearch__hint fe-advsearch__hint--block">
              {{ contentSearch ? t('advsearch.scope.hint') : t('advsearch.scope.name_only') }}
            </p>
          </div>
        </div>
      </div>

      <!-- ── the live count ──────────────────────────────────────────────
           A real query, debounced, and the line under it says both what
           that cost and which half of the form the server never saw. -->
      <div class="fe-advsearch__count" data-testid="advsearch-count">
        <div class="fe-advsearch__count-top">
          <p class="fe-advsearch__count-line">
            <span v-if="nothingToAsk">{{ t('advsearch.count.idle') }}</span>
            <span v-else-if="counting">{{ t('advsearch.count.counting') }}</span>
            <span v-else-if="countError">{{ t('advsearch.count.error') }}</span>
            <strong v-else-if="counted">{{ countLabel }}</strong>
          </p>
          <button
            v-if="!nothingToAsk"
            type="button"
            class="fe-advsearch__viewall"
            data-testid="advsearch-viewall"
            @click="submit"
          >{{ t('advsearch.viewall') }}</button>
        </div>
        <p class="fe-advsearch__hint fe-advsearch__hint--block">{{ t('advsearch.count.cost') }}</p>
        <p v-if="wireQuery" class="fe-advsearch__wire">
          {{ t('advsearch.wire') }} <code>{{ wireQuery }}</code>
        </p>
      </div>
    </form>

    <template #actions>
      <button type="button" class="fe-btn fe-advsearch__reset" @click="reset">
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-advsearch__btn-icon" aria-hidden="true" v-html="actionIconSvg('refresh')"></span>
        {{ t('advsearch.reset') }}
      </button>
      <button type="button" class="fe-btn" @click="emit('close')">{{ t('advsearch.cancel') }}</button>
      <button
        type="button"
        class="fe-btn fe-btn--primary"
        :disabled="nothingToAsk"
        data-testid="advsearch-submit"
        @click="submit"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-advsearch__btn-icon" aria-hidden="true" v-html="actionIconSvg('search')"></span>
        {{ t('advsearch.submit') }}
      </button>
    </template>
  </Modal>
</template>
