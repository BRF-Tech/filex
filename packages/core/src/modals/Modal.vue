<script setup lang="ts">
/**
 * Modal — tiny headless wrapper. Backdrop + centered card + ESC + autofocus.
 *
 * ⚠⚠ Every dialog the explorer draws is this one — rename, new folder, the
 * delete confirmation, and the frame EVERY app plugin's screen opens in
 * (PluginViewModal). What it does on Escape, on a click outside the card
 * and with the focus is therefore what all of them do, and until v0.43.0
 * it did none of the three for a dialog MOUNTED open:
 *
 *   • the keys and the focus were wired in a `watch` on `open` without
 *     `immediate`, so a dialog created with `open` already true (the plugin
 *     frame is `v-if` + `:open="true"`) never heard Escape and never took
 *     the focus — the converter's dialog stayed up on Escape while the share
 *     dialog next to it closed (release-candidate sweep, 2026-09-21);
 *   • `closeOnBackdrop` was read as `!== false`, but Vue casts an absent
 *     boolean prop to `false`, so NO dialog closed on an outside click.
 */
import { watch, onBeforeUnmount, ref, getCurrentInstance, inject, nextTick } from 'vue';
import { isTopModal, popModal, pushModal } from '../lib/modalStack';
import { EXPLORER_LOCALE, useLocale } from '../composables/useLocale';

const props = withDefaults(defineProps<{
  /** The explorer's language, for the close button's name. Absent = English. */
  locale?: string;
  open: boolean;
  title?: string;
  size?: 'sm' | 'md' | 'lg' | 'xl';
  /** A click outside the card closes the dialog (the default, as the share
   *  dialog and the admin panel's dialogs do). `false` for a dialog that
   *  must be answered — the recovery key a person has to write down. */
  closeOnBackdrop?: boolean;
  /** When true, drop the dialog chrome (backdrop tint, header, footer,
   *  centered card with border-radius) and render the slot full-bleed.
   *  Used by the standalone /files/edit route where the browser tab IS
   *  the container — a modal frame on top of it just steals real estate
   *  from the editor.
   *
   *  ⚠ Escape does NOT close a chromeless dialog. There it would close the
   *  TAB (the route answers `close` with window.close()), and Escape is a
   *  key editors use for themselves — dismissing a suggestion list in the
   *  text editor must not throw the document away. It never did in
   *  practice (the route mounts the dialog open, see above); now it is a
   *  rule rather than an accident. */
  chromeless?: boolean;
  /**
   * gorunum:v1 — full-bleed overlay. Like `chromeless` it drops the dialog
   * head and footer and lets the card own the whole viewport, but it KEEPS
   * the modal identity: the card still paints a ground and the caller draws
   * its own chrome inside the slot (see PreviewModal's viewer bar).
   *
   * ⚠ Deliberately a second flag rather than a reuse of `chromeless`.
   * `chromeless` means "the browser tab IS the container" (the standalone
   * /files/edit route) and e2e/tests/83 asserts on exactly that class; an
   * in-page preview that borrowed it would start claiming to be that route.
   */
  fullbleed?: boolean;
  /** Explicit theme. When set the modal stamps the appropriate
   *  `.fe--theme-{light,dark}` class on its `.fe` backdrop so the CSS
   *  variable cascade matches the host shell regardless of OS
   *  preference (Modal is a portal-shaped descendant of `<body>`
   *  rather than a child of the parent component, so plain DOM
   *  inheritance isn't always enough). */
  theme?: 'light' | 'dark' | 'auto';
  /**
   * The work this dialog started is on its way (a rename, a delete, an app's
   * answer, a conversion). Escape and a click outside do nothing, × is shut,
   * and the dialog says `aria-busy`; the caller shuts its own buttons.
   *
   * ⚠⚠ ONE rule for every dialog. #59's dialogs closed anyway and lost the
   * server's refusal, an app's screen refused to close while its × looked
   * live, and the converter asked with the browser's own `confirm()` —
   * unthemed, not right-to-left, and simply blocked in an iframe sandboxed
   * without `allow-modals`.
   */
  busy?: boolean;
  /**
   * A busy dialog that closing can still END — the converter, whose work runs
   * in the dialog's own frame, for up to half an hour. × stays live and asks
   * INSIDE the dialog: `question`, then "Keep going" or `confirm` (default
   * "Stop and close"). Escape and an outside click still do nothing.
   */
  busyClose?: { question: string; confirm?: string } | null;
}>(), {
  closeOnBackdrop: true,
  busyClose: null,
});
// ⚠ The explorer's language when the caller names none (EXPLORER_LOCALE):
// the × used to be "Close" under every Turkish dialog.
const ambientLocale = getCurrentInstance() ? inject(EXPLORER_LOCALE, undefined) : undefined;
const { t } = useLocale(() => props.locale ?? ambientLocale?.() ?? 'en');

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const cardEl = ref<HTMLElement | null>(null);

/* wiring:c4 — a11y: unique title id for aria-labelledby + focus restore.
 * The element focused before the modal opened gets focus back on close so
 * keyboard users return to where they were instead of <body>. */
let modalSeq = 0;
const titleId = `fe-modal-title-${++modalSeq}-${Math.random().toString(36).slice(2, 7)}`;
let prevFocus: HTMLElement | null = null;

const FOCUSABLE =
  'input:not([disabled]),select:not([disabled]),textarea:not([disabled]),' +
  'button:not([disabled]),a[href],[tabindex]:not([tabindex="-1"])';

/** This dialog's place in lib/modalStack: only the top one answers keys. */
const me = Symbol('fe-modal');
let wired = false;
let focusTimer: ReturnType<typeof setTimeout> | undefined;

/**
 * The first thing to focus: something in the BODY when there is one, so a
 * form's first box — or a plugin screen's first choice — has the cursor,
 * not the header's ×; then the footer; then anything in the card (the ×).
 */
function focusFirst() {
  const card = cardEl.value;
  if (!card) return;
  const within = (sel: string) => card.querySelector(sel)?.querySelector<HTMLElement>(FOCUSABLE) ?? null;
  const target = within('.fe-modal__body') ?? within('.fe-modal__actions') ?? card.querySelector<HTMLElement>(FOCUSABLE);
  target?.focus();
}

function wire() {
  if (wired) return;
  wired = true;
  prevFocus = (document.activeElement as HTMLElement | null) ?? null; /* wiring:c4 */
  pushModal(me);
  document.addEventListener('keydown', onKey);
  // ⚠ The handle is kept and cleared on unwire: a dialog closed (or torn
  // down, as a test environment is) inside these 30 ms must not reach for a
  // document that is gone — vitest reported exactly that as an unhandled
  // "document is not defined" once dialogs created open were wired.
  focusTimer = setTimeout(() => {
    focusTimer = undefined;
    if (typeof document === 'undefined') return;
    // Only if the focus is not already inside (a component that focuses its
    // own field on mount keeps it).
    if (wired && !cardEl.value?.contains(document.activeElement)) focusFirst();
  }, 30);
}

function unwire() {
  if (!wired) return;
  wired = false;
  if (focusTimer !== undefined) {
    clearTimeout(focusTimer);
    focusTimer = undefined;
  }
  document.removeEventListener('keydown', onKey);
  popModal(me);
  /* wiring:c4 — return focus to the opener. */
  prevFocus?.focus?.();
  prevFocus = null;
}

// ⚠ `immediate`: a dialog created open (v-if + :open="true", which is how
// the explorer mounts every plugin screen) must be wired exactly like one
// that opens later. See the header for what happened without it.
watch(
  () => props.open,
  (v) => (v ? wire() : unwire()),
  { immediate: true },
);

// Removed while still open (the parent's v-if went false first): the keys
// come off and the focus goes home, as if it had closed.
onBeforeUnmount(unwire);

/** × pressed on a busy dialog that closing can end: the question is up. */
const asking = ref(false);
const keepEl = ref<HTMLButtonElement | null>(null);
// The work ended (or the dialog went) while the question was up: there is
// nothing left to stop, and the question would be about nothing.
watch(
  () => [props.busy, props.open] as const,
  ([busy, open]) => {
    if (!busy || !open) asking.value = false;
  },
);

/** × — the one way out of a dialog that is not busy; asks on one whose work
 *  closing would end; does nothing (it is shut) on any other busy dialog. */
function requestClose() {
  if (!props.busy) {
    emit('close');
    return;
  }
  if (!props.busyClose) return;
  asking.value = true;
  // The safe answer has the focus: Enter or Space keeps the work going.
  void nextTick(() => keepEl.value?.focus());
}

function stopAndClose() {
  asking.value = false;
  emit('close');
}

function onKey(e: KeyboardEvent) {
  if (!isTopModal(me)) return;
  if (e.key === 'Escape') {
    if (props.chromeless) return;
    // Escape answers the question with the safe answer, and otherwise does
    // nothing while the work is on its way.
    if (asking.value) {
      asking.value = false;
      return;
    }
    if (props.busy) return;
    emit('close');
    return;
  }
  /* wiring:c4 — focus trap: Tab cycles inside the card (WAI-ARIA dialog).
   * Without it, Tab walked out into the host page behind the backdrop. */
  if (e.key === 'Tab' && cardEl.value) {
    const nodes = Array.from(cardEl.value.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
      (el) => el.offsetParent !== null || el === document.activeElement,
    );
    if (nodes.length === 0) return;
    const first = nodes[0];
    const last = nodes[nodes.length - 1];
    const active = document.activeElement as HTMLElement | null;
    if (e.shiftKey && (active === first || !cardEl.value.contains(active))) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && (active === last || !cardEl.value.contains(active))) {
      e.preventDefault();
      first.focus();
    }
  }
}

/**
 * A click outside the card closes the dialog — but only a click that also
 * STARTED outside it. A drag that selects text in a field and is released
 * over the backdrop fires `click` on the backdrop (the common ancestor), and
 * closing then would throw away what was being typed.
 */
let downOnBackdrop = false;
function onBackdropDown(e: PointerEvent) {
  downOnBackdrop = e.target === e.currentTarget;
}
function onBackdrop(e: MouseEvent) {
  const started = downOnBackdrop;
  downOnBackdrop = false;
  if (!props.closeOnBackdrop || props.chromeless || props.busy) return;
  if (e.target !== e.currentTarget || !started) return;
  emit('close');
}
</script>

<template>
  <transition name="fe-modal">
    <div
      v-if="open"
      class="fe fe-modal__backdrop"
      :class="[
        { 'fe-modal__backdrop--chromeless': chromeless },
        { 'fe-modal__backdrop--fullbleed': fullbleed && !chromeless },
        theme === 'light' ? 'fe--theme-light' : '',
        theme === 'dark' ? 'fe--theme-dark' : '',
      ]"
      role="presentation"
      @pointerdown="onBackdropDown"
      @click="onBackdrop"
    >
      <div
        ref="cardEl"
        class="fe-modal__card"
        :class="[
          `fe-modal__card--${size || 'md'}`,
          chromeless && 'fe-modal__card--chromeless',
          fullbleed && !chromeless && 'fe-modal__card--fullbleed',
        ]"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="title && !chromeless && !fullbleed ? titleId : undefined"
        :aria-label="!title || chromeless || fullbleed ? title || undefined : undefined"
        :aria-busy="busy ? 'true' : undefined"
        @click.stop
      >
        <header v-if="title && !chromeless && !fullbleed" class="fe-modal__head">
          <h2 :id="titleId" class="fe-modal__title">{{ title }}</h2>
          <button
            type="button"
            class="fe-modal__close"
            :aria-label="t('modal.close')"
            :disabled="busy && !busyClose"
            @click="requestClose"
          >×</button>
        </header>
        <div class="fe-modal__body">
          <slot />
        </div>
        <!-- The question a busy dialog asks before closing ends its work — in
             the dialog, themed and in the reader's direction, never the
             browser's confirm(). -->
        <div v-if="asking && busyClose" class="fe-modal__ask" role="alert" data-testid="modal-busy-close">
          <p class="fe-modal__ask-text">{{ busyClose.question }}</p>
          <div class="fe-modal__ask-actions">
            <button
              ref="keepEl"
              type="button"
              class="fe-btn"
              data-testid="modal-busy-close-keep"
              @click="asking = false"
            >{{ t('modal.busy_close.keep') }}</button>
            <button
              type="button"
              class="fe-btn fe-btn--danger"
              data-testid="modal-busy-close-confirm"
              @click="stopAndClose"
            >{{ busyClose.confirm || t('modal.busy_close.confirm') }}</button>
          </div>
        </div>
        <footer v-if="$slots.actions && !chromeless && !fullbleed" class="fe-modal__actions">
          <slot name="actions" />
        </footer>
      </div>
    </div>
  </transition>
</template>
