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

// ------------------------------------------------------------------
// Step definitions. `target` returns the element to spotlight (null =
// centered card, e.g. the closing shortcuts step). Buttons without a
// stable class are found via their localized title attribute — the
// toolbar gives every icon-only button a title from the same catalogue.
// ------------------------------------------------------------------

function byTitle(root: HTMLElement, label: string): HTMLElement | null {
  const nodes = root.querySelectorAll<HTMLElement>('button[title]');
  for (const el of Array.from(nodes)) {
    if (el.getAttribute('title') === label) return el;
  }
  return null;
}

interface TourStep {
  id: string;
  titleKey: string;
  descKey: string;
  target: (root: HTMLElement) => HTMLElement | null;
}

const STEPS: TourStep[] = [
  {
    id: 'nav',
    titleKey: 'tour.step.nav.title',
    descKey: 'tour.step.nav.desc',
    target: (r) => r.querySelector<HTMLElement>('.fe-breadcrumb'),
  },
  {
    id: 'upload',
    titleKey: 'tour.step.upload.title',
    descKey: 'tour.step.upload.desc',
    target: (r) =>
      byTitle(r, t('toolbar.upload')) || r.querySelector<HTMLElement>('.fe-fab'),
  },
  {
    id: 'search',
    titleKey: 'tour.step.search.title',
    descKey: 'tour.step.search.desc',
    target: (r) =>
      r.querySelector<HTMLElement>('.fe-search__input') ||
      r.querySelector<HTMLElement>('.fe-toolbar__search-toggle'),
  },
  {
    id: 'view',
    titleKey: 'tour.step.view.title',
    descKey: 'tour.step.view.desc',
    target: (r) => r.querySelector<HTMLElement>('.fe-toolbar__view'),
  },
  {
    id: 'share',
    titleKey: 'tour.step.share.title',
    descKey: 'tour.step.share.desc',
    target: (r) => r.querySelector<HTMLElement>('.fe__body'),
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

const PAD = 6; // spotlight breathing room around the target
const GAP = 12; // gap between spotlight and the card

async function place() {
  const s = step.value;
  const r = props.root;
  if (!s) return;
  const el = r ? s.target(r) : null;
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
  // Card: below the spotlight when there's room, above otherwise;
  // clamped into the viewport horizontally.
  await nextTick();
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const cw = Math.min(340, vw - 24);
  const ch = cardEl.value?.offsetHeight ?? 160;
  let top = rect.bottom + PAD + GAP;
  if (top + ch > vh - 12) top = Math.max(12, rect.top - PAD - GAP - ch);
  let left = rect.left + rect.width / 2 - cw / 2;
  left = Math.min(Math.max(12, left), vw - cw - 12);
  cardStyle.value = {
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
    width: `${cw}px`,
  };
}

function onViewportChange() {
  if (props.open) void place();
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
          <p class="fe-tour__desc">{{ t(step.descKey) }}</p>
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
