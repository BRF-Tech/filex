<script setup lang="ts">
/**
 * TimeZonePicker — a search box that opens a ranked list of zones.
 *
 * ⚠⚠ ONE picker, two hosts. It was written inside the web app's user-settings
 * modal (`web/src/components/UserSettingsModal.vue`), and then the embed
 * needed the same control — an embed has no settings dialog, so its "⋯" menu
 * opens `TimeZoneDialog`, which mounts this. `packages/core` cannot import from
 * `web/`, so the picker moved here and both surfaces render it from this one
 * definition. A second copy would be the duplication the owner has ruled out
 * (*"tekrar eden şeyler tek yerden eklenmeli"*) and the first thing the two
 * would disagree about is which zones they find.
 *
 * Presentational: `modelValue` is `''` or an IANA id, a pick emits
 * `update:modelValue`, and the HOST decides what a pick writes — the web app
 * writes the account, the embed writes this browser's own choice
 * (`lib/timezone`, tier `viewer`). What `''` stands for differs between them,
 * so the host says it through `fallback` and the first row names it.
 *
 * ⚠ A half-typed string never becomes the value: `query` (what you typed) and
 * `modelValue` (what is in force) are separate, and only `commit` crosses
 * between them.
 */
import { computed, nextTick, onBeforeUnmount, ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { localeTag, useLocale } from '../composables/useLocale';
import {
  deviceTimeZone,
  supportedTimeZones,
  type ResolvedTimeZone,
} from '../lib/timezone';

const props = defineProps<{
  locale: LocaleCode;
  /** `''` = the default row; otherwise an IANA id. */
  modelValue: string;
  /** What `''` resolves to on this surface — the zone and the tier. */
  fallback: ResolvedTimeZone;
  disabled?: boolean;
  /** Id for the input, so the host's own `<label for>` names the control. */
  inputId?: string;
  /** Accessible name for the list (the host's visible label, usually). */
  label?: string;
  /** `data-testid` prefix: `-filter`, `-list`, `-opt-<zone|device>`. */
  testid?: string;
}>();

const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>();

const { t } = useLocale(() => props.locale);
const tag = computed(() => localeTag(props.locale));
const tid = computed(() => props.testid || 'fe-tz');

let seq = 0;
const uid = `fe-tz-${++seq}-${Math.random().toString(36).slice(2, 7)}`;
const inputDomId = computed(() => props.inputId || `${uid}-input`);
const listId = `${uid}-list`;

/* ── the zone index ──────────────────────────────────────────────────────
 *
 * ⚠⚠ NOT a hand-written list of zones. The ids come from the platform
 * (`Intl.supportedValuesOf('timeZone')`, wrapped by `supportedTimeZones()`,
 * which falls back to a small canonical set on an engine that answers
 * nothing — so a person on such a browser still gets a usable list and, above
 * all, KEEPS the zone they already have).
 *
 * Everything a row shows or is searched by is derived from `Intl`:
 *
 *   • the offset  — `timeZoneName: 'shortOffset'` → "GMT+03:00";
 *   • the region name — `timeZoneName: 'longGeneric'` → "Turkey Time", built
 *     in BOTH English and the interface language, so "turkey" and "Türkiye"
 *     both find `Europe/Istanbul` whichever catalogue is loaded. That one line
 *     is why the alias table below is eight entries and not four hundred;
 *   • the city — the id's own last segment.
 *
 * Built lazily, the first time the field is opened: ~420 zones × two
 * formatters is work worth doing zero times for the people who never touch
 * this control. Re-built when the language or the default row changes.
 */
interface TzRow {
  /** The value that gets emitted. `''` = the default row. */
  value: string;
  /** The city, or the default sentence. What the eye lands on. */
  label: string;
  /** "Europe" — drawn muted beside the city. */
  region: string;
  /** "GMT+03:00". */
  offset: string;
  /** Folded text the query is matched against. */
  haystack: string;
  /** The city alone, folded — the strongest thing a query can hit. */
  cityFold: string;
  /** Every word of the haystack, folded — for "starts a word" matches. */
  words: string[];
  /** Clock for this zone, or null when the engine refuses the id. */
  fmt: Intl.DateTimeFormat | null;
}

/**
 * Fold a string down to something a person's typing can be compared against:
 * case, Turkish letters, accents and the id's punctuation all removed.
 *
 * ⚠ The Turkish map is explicit and comes FIRST. `'İ'.toLowerCase()` is
 * "i̇" (an i plus a combining dot) in every locale-independent lowercase,
 * and `'I'.toLowerCase()` is "i" — so a person typing İstanbul on a Turkish
 * keyboard and the id `Europe/Istanbul` do not meet unless the dot is dealt
 * with by hand. Stripping combining marks afterwards catches the rest.
 */
function fold(input: string): string {
  return input
    .replace(/[\u0130\u0131]/g, 'i')
    .replace(/[\u015e\u015f]/g, 's')
    .replace(/[\u011e\u011f]/g, 'g')
    .replace(/[\u00dc\u00fc]/g, 'u')
    .replace(/[\u00d6\u00f6]/g, 'o')
    .replace(/[\u00c7\u00e7]/g, 'c')
    .toLowerCase()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[_/\-.]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

/**
 * The handful of names `Intl` does not say.
 *
 * ⚠ Deliberately tiny, and deliberately not a zone list: the generic names
 * already carry the country for every zone that has one. These are the words
 * people type that are not a city, a country or an offset — mostly the
 * abbreviations a support ticket arrives written in.
 */
const ALIASES: Record<string, string> = {
  UTC: 'utc gmt zulu z greenwich universal',
  'Etc/UTC': 'utc gmt zulu greenwich universal',
  'Etc/GMT': 'utc gmt greenwich',
  'Europe/Istanbul': 'turkey turkiye ist constantinople',
  'Europe/London': 'uk britain england gmt bst',
  'America/New_York': 'usa united states eastern est edt nyc',
  'America/Los_Angeles': 'usa united states pacific pst pdt la',
  'Asia/Kolkata': 'india calcutta ist',
};

/** Every way a person might type an offset the platform prints as "GMT+03:00". */
function offsetAliases(offset: string): string {
  const m = /GMT([+-])(\d{1,2})(?::?(\d{2}))?/.exec(offset);
  if (!m) return 'gmt utc';
  const [, sign, hRaw, minRaw] = m;
  const h = String(Number(hRaw));
  const hh = h.padStart(2, '0');
  const mm = minRaw ?? '00';
  const forms = [`${sign}${h}`, `${sign}${hh}`, `${sign}${hh}:${mm}`, `${sign}${hh}${mm}`];
  return [...forms.map((f) => `gmt${f}`), ...forms.map((f) => `utc${f}`), ...forms].join(' ');
}

/** The `timeZoneName` part alone, e.g. "GMT+03:00" or "Turkey Time". */
function namePart(zone: string, locale: string, style: 'shortOffset' | 'longGeneric'): string {
  try {
    const parts = new Intl.DateTimeFormat(locale, {
      timeZone: zone,
      timeZoneName: style,
    }).formatToParts(new Date());
    return parts.find((x) => x.type === 'timeZoneName')?.value ?? '';
  } catch {
    return '';
  }
}

function clockFor(zone: string): Intl.DateTimeFormat | null {
  try {
    return new Intl.DateTimeFormat(tag.value, { timeZone: zone, hour: '2-digit', minute: '2-digit' });
  } catch {
    return null;
  }
}

/** The zone `''` stands for, spelled out (the device's name when it is the device). */
const fallbackZone = computed(() => props.fallback.zone ?? deviceTimeZone());

/**
 * The first row's sentence. It names the TIER as well as the zone, because
 * "use the default" is a promise about what happens next and the person
 * choosing it should not have to guess whose default that is.
 */
const defaultLabel = computed(() => {
  const zone = fallbackZone.value;
  if (props.fallback.tier === 'host') return t('tz.default.host', { zone });
  if (props.fallback.tier === 'account') return t('tz.default.account', { zone });
  return t('tz.default.device', { zone });
});

const index = ref<TzRow[] | null>(null);
let indexKey = '';

function buildIndex(): TzRow[] {
  const loc = tag.value;
  const fz = fallbackZone.value;
  const defaultHay = fold(
    `${fz} ${defaultLabel.value} default automatic device local system account site`,
  );
  const rows: TzRow[] = [
    {
      value: '',
      label: defaultLabel.value,
      region: '',
      offset: namePart(fz, loc, 'shortOffset'),
      haystack: defaultHay,
      cityFold: fold(fz.split('/').pop() ?? ''),
      words: defaultHay.split(' ').filter(Boolean),
      fmt: clockFor(fz),
    },
  ];
  /* ⚠ The zone in force is guaranteed a row even when the platform's list
     does not contain it — an engine without `Intl.supportedValuesOf` falls
     back to the small canonical set, and somebody whose zone is outside it
     must still see it, find it highlighted, and be able to land back on it
     after wandering the list. Nothing is rewritten either way: only `commit`
     emits. */
  const ids = supportedTimeZones();
  if (props.modelValue && !ids.includes(props.modelValue)) ids.unshift(props.modelValue);
  for (const zone of ids) {
    const segs = zone.split('/');
    const city = (segs[segs.length - 1] ?? zone).replace(/_/g, ' ');
    const region = segs.length > 1 ? segs[0].replace(/_/g, ' ') : '';
    const offset = namePart(zone, loc, 'shortOffset');
    const genericLocal = namePart(zone, loc, 'longGeneric');
    const genericEn = loc === 'en-US' ? '' : namePart(zone, 'en-US', 'longGeneric');
    const haystack = fold(
      [zone, city, region, genericLocal, genericEn, ALIASES[zone] ?? '', offsetAliases(offset)]
        .filter(Boolean)
        .join(' '),
    );
    rows.push({
      value: zone,
      label: city,
      region,
      offset,
      haystack,
      cityFold: fold(city),
      words: haystack.split(' ').filter(Boolean),
      fmt: clockFor(zone),
    });
  }
  return rows;
}

function ensureIndex(): void {
  const key = `${props.locale}|${props.fallback.tier}|${fallbackZone.value}|${props.modelValue}`;
  if (index.value && indexKey === key) return;
  indexKey = key;
  index.value = buildIndex();
}

/* ── the combobox ──────────────────────────────────────────────────────── */

const open = ref(false);
const query = ref('');
const active = ref(0);
const inputEl = ref<HTMLInputElement | null>(null);
const listEl = ref<HTMLElement | null>(null);

/** The instant every row's clock is formatted from — one shared tick rather
 *  than 50 rows each calling `new Date()` and disagreeing by a millisecond. */
const tick = ref(new Date());
let timer: ReturnType<typeof setInterval> | undefined;
function startTick(): void {
  tick.value = new Date();
  if (!timer) timer = setInterval(() => (tick.value = new Date()), 1000);
}
function stopTick(): void {
  if (timer) clearInterval(timer);
  timer = undefined;
}
onBeforeUnmount(stopTick);

/**
 * Where the list goes and how tall it may be — MEASURED, not assumed.
 *
 * ⚠ The host usually scrolls (a settings pane, a dialog body), so the list
 * cannot escape it: a fixed 264px drop opened straight through the settings
 * modal's footer and the bottom rows were cut off mid-word (measured at
 * 1280×900 with the field near the bottom of Preferences). So the drop takes
 * whichever side of the field has room inside its nearest scrolling ancestor
 * — the viewport when there is none — and never asks for more than that.
 */
const drop = ref<{ maxHeight: string; above: boolean }>({ maxHeight: '264px', above: false });

function scrollBox(el: HTMLElement): { top: number; bottom: number } {
  let p = el.parentElement;
  while (p) {
    const oy = getComputedStyle(p).overflowY;
    if (oy === 'auto' || oy === 'scroll') {
      const r = p.getBoundingClientRect();
      return { top: r.top, bottom: r.bottom };
    }
    p = p.parentElement;
  }
  return { top: 0, bottom: window.innerHeight };
}

function measureDrop(): void {
  const input = inputEl.value;
  if (!input) return;
  const ir = input.getBoundingClientRect();
  const box = scrollBox(input);
  const gap = 12;
  const below = box.bottom - ir.bottom - gap;
  const above = ir.top - box.top - gap;
  /* A floor, because a two-row list is worse than one that overlaps the field
     it belongs to; and a ceiling, because past ~7 rows people type instead of
     scrolling. */
  const MIN = 132;
  const MAX = 264;
  const useAbove = below < MIN && above > below;
  const room = useAbove ? above : below;
  drop.value = {
    maxHeight: `${Math.round(Math.max(MIN, Math.min(MAX, room)))}px`,
    above: useAbove,
  };
}

/** How many rows are drawn. Unfiltered this list is ~420 long and every row
 *  carries a live clock; past the first screenful nobody is reading, they are
 *  typing. The count that was left out is SAID, so a short list is never
 *  mistaken for the whole answer. */
const LIMIT = 50;

/**
 * How well one row answers one query — `-1` when it does not.
 *
 * ⚠⚠ RANKED, not merely filtered. A plain substring match over the haystack
 * is what this had first, and typing "ist" returned Boa V*ist*a, Ashgabat
 * (Turkmen*ist*an Time) and Dushanbe — with Istanbul nowhere in the visible
 * rows. Measured in a real browser before the fix:
 *
 *   the city, exactly       → it IS the answer
 *   the city, as a prefix   → "ist" → Istanbul
 *   the start of any word   → "new y" → New York, "gmt+3" → the +3 zones
 *   anywhere at all         → kept, but last
 *
 * Every term has to hit something, so two words narrow instead of widening.
 */
function score(row: TzRow, terms: string[]): number {
  let total = 0;
  for (const term of terms) {
    let best = 0;
    if (row.cityFold === term) best = 120;
    else if (row.cityFold.startsWith(term)) best = 100;
    else if (row.words.some((w) => w.startsWith(term))) best = 70;
    else if (row.haystack.includes(term)) best = 30;
    if (best === 0) return -1;
    total += best;
  }
  return total;
}

const matches = computed<TzRow[]>(() => {
  const all = index.value ?? [];
  const q = fold(query.value);
  if (!q) {
    /* ⚠ The zone in force is PINNED to the top of the unfiltered list. Only
       the first `LIMIT` rows are rendered, so a zone at index 338 of 420 could
       not be highlighted on open, and `aria-activedescendant` pointed at an id
       that was not in the document. Measured with Europe/London stored. */
    const cur = all.find((r) => r.value === props.modelValue);
    if (!cur || cur === all[0]) return all;
    return [all[0], cur, ...all.slice(1).filter((r) => r !== cur)];
  }
  const terms = q.split(' ').filter(Boolean);
  const scored: Array<{ row: TzRow; score: number; i: number }> = [];
  all.forEach((row, i) => {
    const s = score(row, terms);
    if (s >= 0) scored.push({ row, score: s, i });
  });
  // Ties keep the platform's own order, which is alphabetical by id — so a
  // list of equally-good answers does not reshuffle itself between keystrokes.
  scored.sort((a, b) => b.score - a.score || a.i - b.i);
  return scored.map((x) => x.row);
});

const visible = computed(() => matches.value.slice(0, LIMIT));
const hidden = computed(() => Math.max(0, matches.value.length - visible.value.length));

/** The closed field's text. Never a query: a control that keeps your typing
 *  on screen after you walk away looks like it saved it.
 *
 *  ⚠ It reads correctly BEFORE the index exists — the field is drawn the
 *  moment its pane is, and the index is only built when it is focused. */
const display = computed(() => {
  if (!props.modelValue) return defaultLabel.value;
  const zone = props.modelValue;
  const city = (zone.split('/').pop() ?? zone).replace(/_/g, ' ');
  const off = namePart(zone, tag.value, 'shortOffset');
  return off ? `${city} — ${off}` : city;
});

function rowTime(row: TzRow): string {
  try {
    return row.fmt ? row.fmt.format(tick.value) : '';
  } catch {
    return '';
  }
}

function openList(): void {
  ensureIndex();
  open.value = true;
  query.value = '';
  startTick();
  const idx = visible.value.findIndex((r) => r.value === props.modelValue);
  active.value = idx >= 0 ? idx : 0;
  void nextTick(() => {
    measureDrop();
    scrollActiveIntoView();
  });
}

/** Close WITHOUT choosing: the field goes back to saying what is in force. */
function closeList(): void {
  open.value = false;
  query.value = '';
  active.value = 0;
  stopTick();
}

function onInput(ev: Event): void {
  ensureIndex();
  if (!open.value) startTick();
  open.value = true;
  query.value = (ev.target as HTMLInputElement).value;
  active.value = 0;
}

function commit(row: TzRow): void {
  closeList();
  inputEl.value?.blur();
  if (row.value === props.modelValue) return;
  emit('update:modelValue', row.value);
}

function scrollActiveIntoView(): void {
  const el = listEl.value?.querySelector<HTMLElement>('[data-tz-active="1"]');
  el?.scrollIntoView({ block: 'nearest' });
}

function moveActive(delta: number): void {
  const n = visible.value.length;
  if (n === 0) return;
  // Clamp first: a filter that shrank the list can leave the cursor past its
  // end, and `aria-activedescendant` would then name a row nobody can see.
  const from = Math.min(active.value, n - 1);
  active.value = (from + delta + n) % n;
  void nextTick(() => scrollActiveIntoView());
}

function onKeydown(ev: KeyboardEvent): void {
  switch (ev.key) {
    case 'ArrowDown':
      ev.preventDefault();
      if (!open.value) openList();
      else moveActive(1);
      break;
    case 'ArrowUp':
      ev.preventDefault();
      if (!open.value) openList();
      else moveActive(-1);
      break;
    case 'Home':
      if (!open.value) return;
      ev.preventDefault();
      active.value = 0;
      void nextTick(() => scrollActiveIntoView());
      break;
    case 'End':
      if (!open.value) return;
      ev.preventDefault();
      active.value = Math.max(0, visible.value.length - 1);
      void nextTick(() => scrollActiveIntoView());
      break;
    case 'Enter': {
      if (!open.value) return;
      ev.preventDefault();
      const row = visible.value[active.value];
      if (row) commit(row);
      break;
    }
    case 'Escape':
      if (!open.value) return;
      // ⚠ Stopped here. Both hosts are dialogs that close on Escape, and the
      // first Escape belongs to the list that is open on top of them.
      ev.preventDefault();
      ev.stopPropagation();
      closeList();
      break;
    case 'Tab':
      closeList();
      break;
  }
}
</script>

<template>
  <!-- A real combobox (`role="combobox"` + `aria-expanded` + `aria-controls`
       + `aria-activedescendant`), not a div that listens for clicks: the list
       is reachable by arrow keys and announced to a screen reader as what it
       is. -->
  <div class="fe-tzpick">
    <input
      :id="inputDomId"
      ref="inputEl"
      class="fe-tzpick__input"
      type="text"
      role="combobox"
      autocomplete="off"
      spellcheck="false"
      aria-autocomplete="list"
      :aria-expanded="open"
      :aria-controls="listId"
      :aria-activedescendant="open && visible.length ? `${listId}-opt-${active}` : undefined"
      :disabled="disabled"
      :data-testid="`${tid}-filter`"
      :placeholder="t('tz.search')"
      :value="open ? query : display"
      @focus="openList"
      @input="onInput"
      @keydown="onKeydown"
      @blur="closeList"
    />
    <ul
      v-if="open"
      :id="listId"
      ref="listEl"
      class="fe-tzpick__list"
      :class="{ 'is-above': drop.above }"
      :style="{ maxHeight: drop.maxHeight }"
      role="listbox"
      :aria-label="label || t('tz.title')"
      :data-testid="`${tid}-list`"
    >
      <li
        v-for="(row, i) in visible"
        :id="`${listId}-opt-${i}`"
        :key="row.value || '__default'"
        class="fe-tzpick__opt"
        :class="{ 'is-active': i === active, 'is-current': row.value === modelValue }"
        :data-tz-active="i === active ? '1' : '0'"
        :data-testid="`${tid}-opt-${row.value || 'device'}`"
        role="option"
        :aria-selected="row.value === modelValue"
        :title="row.value || fallbackZone"
        @mousedown.prevent="commit(row)"
        @mousemove="active = i"
      >
        <span class="fe-tzpick__main">
          <span class="fe-tzpick__city">{{ row.label }}</span>
          <span v-if="row.region" class="fe-tzpick__region">{{ row.region }}</span>
        </span>
        <span class="fe-tzpick__meta">
          <span v-if="rowTime(row)" class="fe-tzpick__time">{{ rowTime(row) }}</span>
          <span class="fe-tzpick__offset">{{ row.offset }}</span>
        </span>
      </li>
      <li v-if="visible.length === 0" class="fe-tzpick__empty" role="presentation">
        {{ t('tz.no_match', { query }) }}
      </li>
      <li v-else-if="hidden > 0" class="fe-tzpick__empty" role="presentation">
        {{ t('tz.more', { n: hidden }) }}
      </li>
    </ul>
  </div>
</template>

<!-- Deliberately NOT scoped: the webcomponent injects the CORE package's built
     style.css, whose data-v scope ids do not match the ids the webcomponent's
     own compile assigns, so scoped rules would silently not apply in embeds.
     Class names are fe-* namespaced (same approach as ThemePalette). -->
<style>
/* The list is absolutely positioned so it OVERLAYS what is under it instead of
   pushing the rest of the pane down: opening a zone list must not move the
   next control out from under the pointer. */
.fe-tzpick {
  position: relative;
  z-index: 2;
}
.fe-tzpick__input {
  box-sizing: border-box;
  width: 100%;
  height: var(--fe-h-md);
  padding: 0 10px;
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  background: var(--fe-bg);
  color: var(--fe-text);
  font: inherit;
  font-size: var(--fe-text-sm);
}
.fe-tzpick__input:focus {
  outline: none;
  border-color: var(--fe-primary);
}
.fe-tzpick__input:disabled {
  background: var(--fe-bg-elev);
  color: var(--fe-text-muted);
}
.fe-tzpick__list {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  right: 0;
  z-index: 5;
  box-sizing: border-box;
  margin: 0;
  padding: var(--fe-gap-xs);
  list-style: none;
  overflow-y: auto;
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  background: var(--fe-bg);
  box-shadow: var(--fe-shadow-sm);
}
.fe-tzpick__list.is-above {
  top: auto;
  bottom: calc(100% + 4px);
}
.fe-tzpick__opt {
  display: flex;
  align-items: center;
  gap: var(--fe-gap-sm);
  min-height: var(--fe-h-md);
  padding: 0 var(--fe-gap-sm);
  border-radius: var(--fe-radius-sm);
  color: var(--fe-text);
  cursor: pointer;
}
/* Two separate states, and they are different things: `is-active` is where
   the keyboard is, `is-current` is what is actually in force. A list that
   painted only one of them cannot answer "which one am I on?" and "which one
   did I choose?" at the same time. */
.fe-tzpick__opt.is-active {
  background: var(--fe-bg-hover);
}
.fe-tzpick__opt.is-current {
  color: var(--fe-primary);
  font-weight: 600;
}
.fe-tzpick__opt.is-current.is-active {
  background: var(--fe-primary-soft);
}
.fe-tzpick__main {
  display: flex;
  align-items: baseline;
  gap: var(--fe-gap-sm);
  flex: 1 1 auto;
  min-width: 0;
}
.fe-tzpick__city {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--fe-text-sm);
}
.fe-tzpick__region {
  flex: 0 0 auto;
  font-size: var(--fe-text-xs);
  font-weight: 400;
  color: var(--fe-text-muted);
}
/* The offset, and the clock beside it, are the reason a row is readable
   without knowing IANA ids. Tabular figures so the column does not jitter as
   the minutes tick. */
.fe-tzpick__meta {
  flex: 0 0 auto;
  display: flex;
  align-items: baseline;
  gap: var(--fe-gap-sm);
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
  font-variant-numeric: tabular-nums;
}
.fe-tzpick__time {
  color: var(--fe-text);
}
.fe-tzpick__empty {
  padding: var(--fe-gap-sm);
  font-size: var(--fe-text-xs);
  line-height: 1.45;
  color: var(--fe-text-muted);
}
/* On a phone the offset stays and the clock goes: 390px minus a pane's
   padding leaves a city column too narrow to truncate politely otherwise. */
@media (max-width: 480px) {
  .fe-tzpick__time {
    display: none;
  }
}
</style>
