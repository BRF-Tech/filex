<script setup lang="ts">
/**
 * OnboardingTour — wiring:c4 first-use coach-mark tour.
 *
 * A dependency-free spotlight walkthrough: each step targets a live DOM
 * element inside the explorer root (resolved lazily — steps whose target
 * is missing are skipped, so confined embeds with hidden surfaces never
 * show a dangling spotlight). Positioning is hand-rolled with
 * getBoundingClientRect + fixed overlay; the spotlight is a rounded
 * cutout drawn with a huge box-shadow.
 *
 * Teleported under <body> (same reason as ContextMenu: transformed embed
 * ancestors break position:fixed) and re-uses the `.fe-ctx-backdrop--theme-*`
 * classes so the CSS-variable theme cascade applies outside the `.fe` tree.
 *
 * Persistence (`filex.tourDone`) is the PARENT's job — the tour only
 * emits `close`; FileExplorer decides when it counts as done.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { shortcutHint } from '../composables/useKeyboardShortcuts';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** Explorer root — targets are resolved inside it only. */
  root: HTMLElement | null;
  theme?: ThemeMode;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const { t } = useLocale(() => props.locale);

/* The tour teaches two keys by name, and both are remappable. Read them
 * from the registry so the sentence stays true for whoever is taking the
 * tour — a walkthrough that names the wrong key is worse than one that
 * names none. */
const hintCombos = computed(() => ({
  palette: shortcutHint('palette'),
  help: shortcutHint('help'),
}));

// ------------------------------------------------------------------
// Step definitions. `target` returns the element to spotlight (null =
// centered card, e.g. the closing shortcuts step).
//
// gorunum:v3-shell — every target below is a `data-testid` or a class the
// shell owns, resolved from the live DOM. It used to find two of its
// buttons by matching their localized `title` attribute, which quietly
// stopped working the moment those verbs moved into a menu: `byTitle(r,
// t('toolbar.upload'))` matched nothing at 1440px, so the upload step was
// dropped from the walk and nobody could see that it had been. A step that
// disappears is indistinguishable from a step that was never written.
//
// ⚠ Two steps point at a control the narrow shell draws DIFFERENTLY — the
// panel (a column at 1440px, a closed drawer behind `toolbar-nav` at
// 390px) and the create button ("+ New" and its menu, versus the upload
// FAB). They say a different sentence there, and the sentence is chosen
// from THE ELEMENT THAT ACTUALLY RESOLVED, never from a width: one branch,
// no media query to keep in sync with the stylesheet.
// ------------------------------------------------------------------

interface TourStep {
  id: string;
  titleKey: string;
  /** A key, or a key chosen from whichever element resolved (see above). */
  descKey: string | ((el: HTMLElement | null) => string);
  target: (root: HTMLElement) => HTMLElement | null;
}

const STEPS: TourStep[] = [
  {
    // The navigation panel: the standard views, then STORAGES, then
    // CONNECTIONS. At 390px it is a drawer that starts closed, so there is
    // nothing to spotlight and the button that opens it is the target.
    id: 'nav',
    titleKey: 'tour.step.nav.title',
    descKey: (el) =>
      el?.dataset.testid === 'sidenav' ? 'tour.step.nav.desc' : 'tour.step.nav.desc_closed',
    target: (r) =>
      r.querySelector<HTMLElement>('[data-testid="sidenav"]') ||
      r.querySelector<HTMLElement>('[data-testid="toolbar-nav"]'),
  },
  {
    // "+ New" — the menu that replaced the Upload / New folder pair. The
    // narrow shell has no panel on screen and draws the upload FAB instead,
    // which does ONE of the three things the menu offers, so it gets a
    // sentence that promises only that one.
    id: 'new',
    titleKey: 'tour.step.new.title',
    descKey: (el) =>
      el?.dataset.testid === 'sidenav-new' ? 'tour.step.new.desc' : 'tour.step.new.desc_fab',
    target: (r) =>
      r.querySelector<HTMLElement>('[data-testid="sidenav-new"]') ||
      r.querySelector<HTMLElement>('.fe-fab'),
  },
  {
    // ⚠ The FIELD, not `.fe-search__input`. The wide header's input carries
    // both class names and the narrow one carries only `fe-drivesearch__input`
    // — so the old selector resolved at 1440px and matched nothing at 390px,
    // where the field is the widest thing on the row. The testid is on the
    // wrapper in both layouts, and spotlighting the wrapper also takes in the
    // advanced-search and palette buttons the copy now names.
    id: 'search',
    titleKey: 'tour.step.search.title',
    descKey: 'tour.step.search.desc',
    target: (r) => r.querySelector<HTMLElement>('[data-testid="drive-search"]'),
  },
  {
    // The breadcrumb — this was the old first step's target while its title
    // said "Storages & folders", which is the panel. Now it has its own step
    // and its own title. Absent on Home, which has no address; the step drops
    // itself there rather than pointing at the heading that takes its place.
    id: 'crumb',
    titleKey: 'tour.step.crumb.title',
    descKey: 'tour.step.crumb.desc',
    target: (r) => r.querySelector<HTMLElement>('.fe-breadcrumb'),
  },
  {
    // The filter row. Unconditional in the shell now, but only drawn where
    // there is a listing to filter — not at the multi-storage virtual root,
    // which is where a first run usually starts, so this step is often the
    // one that drops itself.
    id: 'filters',
    titleKey: 'tour.step.filters.title',
    descKey: 'tour.step.filters.desc',
    target: (r) => r.querySelector<HTMLElement>('[data-testid="filterbar"]'),
  },
  {
    id: 'view',
    titleKey: 'tour.step.view.title',
    descKey: 'tour.step.view.desc',
    // Still `.fe-toolbar__view`: ViewSwitcher kept its class when it moved
    // from the header to the breadcrumb row.
    target: (r) => r.querySelector<HTMLElement>('.fe-toolbar__view'),
  },
  {
    // Sharing used to spotlight `.fe__body` — the whole listing, which is to
    // say nothing in particular — while talking about a context menu. The
    // details panel is where a share link is actually minted, so the step
    // points at the control that opens it and names the context menu too.
    id: 'details',
    titleKey: 'tour.step.details.title',
    descKey: 'tour.step.details.desc',
    target: (r) => r.querySelector<HTMLElement>('[data-testid="subhead-inspector"]'),
  },
  {
    id: 'help',
    titleKey: 'tour.step.help.title',
    descKey: 'tour.step.help.desc',
    target: () => null,
  },
];

// Steps re-resolved on every open so surfaces that appeared/disappeared
// since mount (narrow-mode collapse, RBAC hiding upload…) are honored.
const activeSteps = ref<TourStep[]>([]);
const stepIdx = ref(0);

const step = computed<TourStep | null>(() => activeSteps.value[stepIdx.value] ?? null);
const total = computed(() => activeSteps.value.length);

function resolveSteps() {
  const r = props.root;
  activeSteps.value = STEPS.filter((s) => {
    if (s.id === 'help') return true; // centered card — no DOM dependency
    if (!r) return false;
    const el = s.target(r);
    // Element must exist AND be laid out (visible) — a display:none
    // toolbar section in narrow mode yields a 0×0 rect.
    if (!el) return false;
    const rect = el.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  });
}

// ------------------------------------------------------------------
// Spotlight + card geometry
// ------------------------------------------------------------------

interface Rect {
  top: number;
  left: number;
  width: number;
  height: number;
}
const spot = ref<Rect | null>(null);
const cardStyle = ref<Record<string, string>>({});
const cardEl = ref<HTMLElement | null>(null);
/**
 * The element the CURRENT step resolved to, kept so a step whose control
 * differs by width can pick its sentence from what is really there.
 *
 * Written at the top of `place()`, which runs before the card re-renders
 * (the `stepIdx` watcher flushes 'pre') and again on every resize — so the
 * copy follows the spotlight instead of lagging a step behind it. Assigning
 * the same element twice is not a change, so a scroll storm costs nothing.
 */
const targetEl = ref<HTMLElement | null>(null);

/** The step's sentence, with the shortcut names filled in. */
const stepDesc = computed(() => {
  const s = step.value;
  if (!s) return '';
  const key = typeof s.descKey === 'function' ? s.descKey(targetEl.value) : s.descKey;
  return t(key, hintCombos.value);
});

const PAD = 6; // spotlight breathing room around the target
const GAP = 12; // gap between spotlight and the card

async function place() {
  const s = step.value;
  const r = props.root;
  if (!s) return;
  const el = r ? s.target(r) : null;
  targetEl.value = el;
  if (!el) {
    spot.value = null; // centered card
    cardStyle.value = {};
    return;
  }
  const rect = el.getBoundingClientRect();
  if (rect.width <= 0 && rect.height <= 0) {
    spot.value = null;
    cardStyle.value = {};
    return;
  }
  spot.value = {
    top: rect.top - PAD,
    left: rect.left - PAD,
    width: rect.width + PAD * 2,
    height: rect.height + PAD * 2,
  };
  // Card: below the spotlight, then above, then beside it.
  //
  // gorunum:v3-shell — the SIDE branches are why this is four cases and not
  // two. The navigation panel is a full-height column, so neither "below" nor
  // "above" fits and the old code clamped the card to the top of the viewport
  // — which put it flat on top of the panel, hiding the very rows ("Home, My
  // files, Shared with me…") the step was reading out. A coach mark that
  // covers its own subject is worse than no coach mark: it looks like the
  // product drew a dialog in the wrong place.
  await nextTick();
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const cw = Math.min(340, vw - 24);
  const ch = cardEl.value?.offsetHeight ?? 160;
  const sTop = rect.top - PAD;
  const sBottom = rect.bottom + PAD;
  const sLeft = rect.left - PAD;
  const sRight = rect.right + PAD;
  // Centred on the target along the other axis, then clamped into view.
  const clampX = (x: number) => Math.min(Math.max(12, x), Math.max(12, vw - cw - 12));
  const clampY = (y: number) => Math.min(Math.max(12, y), Math.max(12, vh - ch - 12));
  const midX = clampX(rect.left + rect.width / 2 - cw / 2);
  const midY = clampY(rect.top + rect.height / 2 - ch / 2);

  let top: number;
  let left: number;
  if (sBottom + GAP + ch <= vh - 12) {
    top = sBottom + GAP;
    left = midX;
  } else if (sTop - GAP - ch >= 12) {
    top = sTop - GAP - ch;
    left = midX;
  } else if (sRight + GAP + cw <= vw - 12) {
    left = sRight + GAP;
    top = midY;
  } else if (sLeft - GAP - cw >= 12) {
    left = sLeft - GAP - cw;
    top = midY;
  } else {
    // Nothing fits — a target that fills the viewport. Clamp, and accept the
    // overlap rather than drawing the card off screen.
    top = clampY(sBottom + GAP);
    left = midX;
  }
  cardStyle.value = {
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
    width: `${cw}px`,
  };
}

let placeRaf = 0;
/**
 * Re-place after a resize or a scroll — but on the NEXT frame, never inline.
 *
 * ⚠ The explorer decides it is narrow from a ResizeObserver on its own root
 * (FileExplorer.vue, `isNarrow`), and ResizeObserver callbacks run AFTER the
 * frame's rAF callbacks — so neither this `window` listener nor a single rAF
 * has seen the new layout yet. Measured: shrinking 1440 → 390 with the tour
 * open left the spotlight on the navigation panel (the element was still in
 * the DOM when this handler asked for it) both inline and after one frame;
 * only a SECOND resize moved it onto the top bar's button. Two frames plus a
 * tick puts this after the observer has fired and Vue has patched, so the
 * first resize gets the right answer — and the step's sentence, which is
 * chosen from that same element, changes with it.
 */
function onViewportChange() {
  if (!props.open) return;
  if (placeRaf) cancelAnimationFrame(placeRaf);
  placeRaf = requestAnimationFrame(() => {
    placeRaf = requestAnimationFrame(() => {
      placeRaf = 0;
      void nextTick().then(place);
    });
  });
}

// ------------------------------------------------------------------
// Flow
// ------------------------------------------------------------------

function next() {
  if (stepIdx.value >= total.value - 1) {
    emit('close');
    return;
  }
  stepIdx.value += 1;
}

function back() {
  if (stepIdx.value > 0) stepIdx.value -= 1;
}

function skip() {
  emit('close');
}

function onKey(e: KeyboardEvent) {
  if (!props.open) return;
  if (e.key === 'Escape') {
    e.stopPropagation();
    skip();
  } else if (e.key === 'ArrowRight' || e.key === 'Enter') {
    e.preventDefault();
    next();
  } else if (e.key === 'ArrowLeft') {
    e.preventDefault();
    back();
  }
}

watch(
  () => props.open,
  async (v) => {
    if (v) {
      stepIdx.value = 0;
      resolveSteps();
      await nextTick();
      await place();
      cardEl.value?.focus();
    }
  },
);

watch(stepIdx, async () => {
  await place();
  cardEl.value?.focus();
});

onMounted(() => {
  window.addEventListener('resize', onViewportChange);
  window.addEventListener('scroll', onViewportChange, true);
});
onBeforeUnmount(() => {
  window.removeEventListener('resize', onViewportChange);
  window.removeEventListener('scroll', onViewportChange, true);
  if (placeRaf) cancelAnimationFrame(placeRaf);
});

// Theme cascade outside `.fe` — same pattern as ContextMenu.
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
const themeClass = computed(() => `fe-ctx-backdrop--theme-${props.theme || 'auto'}`);
</script>

<template>
  <Teleport to="body">
    <transition name="fe-tour">
      <div
        v-if="open && step"
        class="fe-tour"
        :class="themeClass"
        :data-prefers-dark="prefersDark ? '1' : '0'"
        role="dialog"
        aria-modal="true"
        :aria-label="t('tour.aria')"
        @keydown="onKey"
      >
        <!-- Spotlight: rounded cutout via huge box-shadow. Centered steps
             (no target) use a full-dim overlay instead. -->
        <div
          v-if="spot"
          class="fe-tour__spot"
          :style="{
            top: spot.top + 'px',
            left: spot.left + 'px',
            width: spot.width + 'px',
            height: spot.height + 'px',
          }"
          aria-hidden="true"
        />
        <div v-else class="fe-tour__dim" aria-hidden="true" />

        <div
          ref="cardEl"
          class="fe-tour__card"
          :class="{ 'fe-tour__card--center': !spot }"
          :style="spot ? cardStyle : {}"
          tabindex="-1"
        >
          <p class="fe-tour__progress" aria-hidden="true">
            {{ t('tour.progress', { n: stepIdx + 1, m: total }) }}
          </p>
          <h3 class="fe-tour__title">{{ t(step.titleKey) }}</h3>
          <p class="fe-tour__desc">{{ stepDesc }}</p>
          <div class="fe-tour__dots" aria-hidden="true">
            <span
              v-for="(s, i) in activeSteps"
              :key="s.id"
              class="fe-tour__dot"
              :class="{ 'is-active': i === stepIdx }"
            />
          </div>
          <div class="fe-tour__actions">
            <button type="button" class="fe-btn fe-tour__skip" @click="skip">
              {{ t('tour.skip') }}
            </button>
            <span class="fe-tour__actions-spacer" />
            <button
              v-if="stepIdx > 0"
              type="button"
              class="fe-btn"
              @click="back"
            >
              {{ t('tour.back') }}
            </button>
            <button type="button" class="fe-btn fe-btn--primary" @click="next">
              {{ stepIdx >= total - 1 ? t('tour.done') : t('tour.next') }}
            </button>
          </div>
        </div>
      </div>
    </transition>
  </Teleport>
</template>
