<script setup lang="ts">
// Standalone-SPA install + update banner. Mounted once at the app root
// (App.vue). Self-hides unless the browser offers an install, the user is on
// iOS (manual add-to-home-screen), or a new service-worker build is waiting.
//
// ⚠ web/ only — never rendered inside the embedded <filex-explorer> hosts.
//
// ⚠⚠ TWO SHAPES, ONE MESSAGE (2026-09-13, the owner's ruling: "küçültüp köşeye
// alalım, kapanınca ack olduğu için tekrar gösterilmesin, ama ayarlar altında
// da yeri olsun"). The offer used to be ONE shape — a 448×285 card fixed to
// the bottom centre — and it wore that shape everywhere. On the sign-in page
// that is right: there is nothing else on the screen, the page reserves room
// for it, and it is the moment the desktop app is genuinely worth advertising.
// Inside the app it was the loudest object on Home, on every folder, on
// Starred, Shared and Trash, standing on the file listing until somebody
// closed it. Measured on Home at 1440×900 before this change: the card covered
// x 496-944, y 600-884 — dead centre of the listing.
//
// So the offer is now:
//
//   · SIGN-IN PAGE  → the full card, bottom centre, unchanged. It publishes
//                     its measured height (see below) and Login.vue reserves
//                     exactly that, which is what keeps the submit button
//                     clickable (cypress/e2e/91-install-banner-login.cy.ts).
//   · EVERYWHERE ELSE → a collapsed chip in the bottom-right corner that still
//                     SAYS WHAT IT IS, and opens into the same card when it is
//                     clicked. A bare icon would have been smaller still and
//                     would have been a removed feature with a decoration left
//                     behind.
//
// It is one piece of markup wearing two shells, not two components: the head,
// the download rows, the iOS help and the install button are written once, so
// the corner form cannot drift away from the card the sign-in page shows.
//
// ⚠ The service-worker UPDATE bar is NOT part of this and deliberately keeps
// its full-width bottom band — see the template.
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useDesktopDownloads, useInstallPrompt } from '@/composables/useInstallPrompt';
import { placeChip, probeDom } from '@/lib/keepClear';

// gorunum:v1 — the banner is painted from the product's palette (`--fe-*`),
// and this is the component that has to carry the import: it is mounted at the
// app root, so it renders over routes that pull in no explorer chunk of their
// own and would otherwise have no token declared at all. Measured before this
// change on /admin/login: `getPropertyValue('--fe-bg')` was the empty string.
// ⚠ It lands in the entry CSS, so every route now loads the core stylesheet
// (index CSS 48 kB -> 209 kB raw, ~23 kB gzip, and the separate style-*.css
// chunk the explorer routes used to fetch is gone). If the shell ever imports
// it globally from main.ts, this import becomes redundant rather than extra.
import '@brftech/filex-core/style.css';

// Public-asset base ('/admin/' today). Used for the icon below — see the
// comment on the <img> for why it must not be a static src.
const baseUrl = import.meta.env.BASE_URL;

// ⚠ The download rows were hardcoded English while their own card title came
// from the catalogue, so a Turkish visitor met a Turkish heading over two
// English rows (2026-09-13, the owner saw it). They now come from the
// catalogue too — and from the SAME place the settings pane reads them
// (`useDesktopDownloads`), so the two surfaces cannot disagree about which
// file to hand somebody.
const {
  platformLabel: desktopLabel,
  downloads: desktopDownloads,
  releasesUrl: RELEASES,
} = useDesktopDownloads();

const {
  canPromptInstall,
  showDesktopDownload,
  showIOSInstructions,
  shouldOfferInstall,
  needRefresh,
  promptInstall,
  dismiss,
  reloadForUpdate,
} = useInstallPrompt();

/* WHICH SHAPE — the full card, or the corner chip.
 *
 * ⚠ Keyed on the sign-in ROUTE, and it has to be: `meta.public` and
 * `meta.layout: 'blank'` cannot tell login apart from /explore, which carries
 * exactly the same pair and IS the file listing. `login` is also the one route
 * name the router's own guard already depends on in three places, so it is not
 * a name that moves quietly.
 *
 * ⚠ The fallback is the QUIET shape. If the route is ever renamed, or this
 * component is ever mounted outside a router, the offer degrades to a corner
 * chip everywhere rather than to a card over every listing — the cheaper of
 * the two failures by a wide margin. */
const route = useRoute();
/** Whether the full card fits on the sign-in page without standing on the form.
 *
 * ⚠⚠ The page reserves the card's height at the bottom and compacts itself,
 * and that is enough for the plain password form — not for SSO + the "or"
 * divider + the password form. Measured at 1280x800 with SSO offered: the
 * element at the submit button's centre was the card's subtitle, so nobody
 * could sign in without scrolling first, and nothing said to scroll. When the
 * card would cover a control of the form (`[data-install-clear]`), the sign-in page gets the
 * corner chip instead — the shape every other page already wears, the owner's
 * ruling of 2026-09-13. A resize re-arms the full card and measures again. */
const fits = ref(true);
const loud = computed(() => route?.name === 'login' && fits.value);

function overlaps(a: DOMRect, b: DOMRect): boolean {
  return a.bottom > b.top && a.top < b.bottom && a.left < b.right && a.right > b.left;
}

function checkFit() {
  if (typeof document === 'undefined' || !loud.value) return;
  const area = document.querySelector('[data-install-clear]');
  const card = installEl.value?.querySelector('.ip-card');
  if (!area || !card) return;
  // What must stay reachable is a CONTROL — a field, a button, a link — not the
  // card's own padding, which may run under the offer's top edge harmlessly.
  const box = card.getBoundingClientRect();
  // ⚠ Plus `[data-install-keep]` ANYWHERE on the page — text that must stay
  // readable although nobody presses it. The version line under the card is
  // outside `[data-install-clear]`, and a tester measured the full card on it
  // at 1440×900 (2026-09-21).
  const controls = [
    ...Array.from(area.querySelectorAll('button, input, select, textarea, a[href]')),
    ...Array.from(document.querySelectorAll('[data-install-keep]')),
  ];
  for (const el of controls) {
    if (overlaps(el.getBoundingClientRect(), box)) {
      fits.value = false;
      return;
    }
  }
}

function rearmFit() {
  fits.value = true;
}
/* ⚠⚠ Re-arm on a REAL viewport change only. Switching shapes changes the page's
 * height, which shows or hides the scrollbar, and Chromium reports that as a
 * `resize` of the window's width. Re-arming on it flipped card → chip → card
 * for as long as the page was open (measured: the tab never fired `load`). A
 * scrollbar is at most ~20px of width and none of height. */
let lastW = typeof window !== 'undefined' ? window.innerWidth : 0;
let lastH = typeof window !== 'undefined' ? window.innerHeight : 0;
function onViewportResize() {
  const w = window.innerWidth;
  const h = window.innerHeight;
  const real = h !== lastH || Math.abs(w - lastW) > 24;
  lastW = w;
  lastH = h;
  if (real) rearmFit();
}
if (typeof window !== 'undefined') window.addEventListener('resize', onViewportResize);
// Leaving the sign-in page and coming back measures again, from the full card.
watch(() => route?.name, rearmFit);

/** Corner chip: closed until it is asked to open. Never persisted — this is
 *  "I am looking at it now", not a preference. */
const open = ref(false);

/** The body (downloads / iOS help / install button) is on screen. */
const expanded = computed(() => loud.value || open.value);

function toggle() {
  if (!loud.value) open.value = !open.value;
}
/** The head's accessible name in the corner shell — it is a disclosure button
 *  there, and a disclosure button that does not say which way it goes is a
 *  shape with no name. On the sign-in page the head is not a control at all. */
const toggleLabel = computed(() =>
  loud.value ? undefined : open.value ? 'install.collapse' : 'install.expand',
);

async function onInstall() {
  const outcome = await promptInstall();
  // If the user accepted, the appinstalled handler clears the offer; on
  // dismiss we hide the banner so it isn't immediately re-shown.
  if (outcome === 'dismissed') dismiss();
}

/* How much of the bottom of the viewport this banner is standing on, published
 * as `--filex-install-banner-h` on <html> so a page underneath can keep its own
 * content out from under it.
 *
 * ⚠ Not a design nicety — a measured defect. The banner is fixed to the bottom
 * centre and the sign-in card's submit button is in that same place: measured
 * at 1440x900, the banner's box started at y=600 and the submit's centre was at
 * y=600, so `document.elementFromPoint` over the button returned the banner.
 * The sign-in page used to reserve a HARDCODED 224px for it, which was the
 * banner's height on the day that number was written; it is 301px today (two
 * download rows), and it changes again with the platform, the language and the
 * width. A page that reserves the real number is right at every size; one that
 * reserves a constant is right on one day.
 *
 * Reported for the wrapper, so it includes the gap the banner leaves at the
 * bottom edge, and cleared when nothing is on screen — the page then gets a
 * plain 0 and lays out as if the banner did not exist.
 *
 * ⚠⚠ WHAT COUNTS is a full-width BOTTOM BAND: the update bar, and the install
 * card in its sign-in shape. The corner chip does NOT, and that is the whole
 * contract rather than an oversight — the variable answers "how much of the
 * bottom edge is unusable", and a 44px chip tucked into one corner leaves the
 * bottom edge usable, exactly as the toast stack in that same corner does. If
 * the chip published its height, every page that reserves room would leave a
 * blank strip across its full width for something standing in one corner.
 * The chip reads the variable INSTEAD, and lifts itself above the update bar
 * when one is showing. */
const BANNER_H_VAR = '--filex-install-banner-h';
const updateEl = ref<HTMLElement | null>(null);
const installEl = ref<HTMLElement | null>(null);
let ro: ResizeObserver | null = null;

const BANNER_CLASS = 'filex-install-banner';

function publishBannerHeight() {
  if (typeof document === 'undefined') return;
  const band = loud.value ? (installEl.value?.offsetHeight ?? 0) : 0;
  const h = Math.max(updateEl.value?.offsetHeight ?? 0, band);
  document.documentElement.style.setProperty(BANNER_H_VAR, `${h}px`);
  // The height alone cannot be asked "are you there?" in a media query, and a
  // page that has to give something up to make room needs to know whether
  // there is anything to make room for.
  document.documentElement.classList.toggle(BANNER_CLASS, h > 0);
  // ⚠ Measure AFTER the page has made its room: the reserved padding and the
  // compact card both hang off the variable and the class set just above, and
  // measuring before they apply found the full-size form under the card at
  // 1280x800 — where, with the room made, no control is.
  if (typeof requestAnimationFrame !== 'undefined') requestAnimationFrame(() => checkFit());
  else checkFit();
}

watch(
  [updateEl, installEl, loud, () => route?.name],
  ([u, i]) => {
    ro?.disconnect();
    if (typeof ResizeObserver !== 'undefined') {
      ro ??= new ResizeObserver(() => publishBannerHeight());
      if (u) ro.observe(u as HTMLElement);
      if (i) ro.observe(i as HTMLElement);
      // The form grows after first paint (capabilities add the SSO button),
      // and that is exactly when it starts to reach the card.
      const form = typeof document !== 'undefined' ? document.querySelector('[data-install-clear]') : null;
      if (form) ro.observe(form);
    }
    publishBannerHeight();
  },
  { flush: 'post', immediate: true },
);

/* ── the corner chip never stands on a control ──────────────────────────
 *
 * ⚠⚠ Measured 2026-09-21 on the signing page's last step: at 1366×768 the
 * chip hid the "İmzala" button completely, at 1440×1000 the element at the
 * button's centre was the chip's icon, at 390×844 its head. The sign-in page
 * was guarded (`checkFit` above); no other page was. `lib/keepClear.ts`
 * decides where the chip may stand — its corner, on top of a pinned bar of
 * actions, or aside (hidden) while something to press is under it — and this
 * asks it again whenever what is under the chip can have changed: a scroll
 * (of the window or of any inner pane — hence `capture`), a resize, or the
 * page's DOM changing (a step of a wizard replaces its footer without the
 * window scrolling at all). One answer per animation frame.
 *
 * ⚠ Never while the person has OPENED the chip: then it is a panel they asked
 * for, standing where they asked for it. */
const lift = ref(0);
const aside = ref(false);
let clearFrame = 0;
let clearMo: MutationObserver | null = null;

function keepClear() {
  const el = installEl.value;
  if (!el || loud.value) {
    lift.value = 0;
    aside.value = false;
    return;
  }
  if (open.value) return;
  // ⚠ The chip's HOME is where it would be with no lift. Its box is read with
  // the current lift applied (the `bottom` offset carries it), so the lift is
  // added back — and `bottom` is deliberately not transitioned, so the box is
  // never read half-way between two positions.
  const r = el.getBoundingClientRect();
  const home = { top: r.top + lift.value, bottom: r.bottom + lift.value, left: r.left, right: r.right };
  const next = placeChip(home, (b) => probeDom(b, el), window.innerHeight, window.innerWidth);
  lift.value = next.lift;
  aside.value = next.aside;
}

function scheduleKeepClear() {
  if (clearFrame || typeof requestAnimationFrame === 'undefined') return;
  clearFrame = requestAnimationFrame(() => {
    clearFrame = 0;
    keepClear();
  });
}

function watchClear(on: boolean) {
  if (typeof window === 'undefined') return;
  if (on && !clearMo) {
    window.addEventListener('scroll', scheduleKeepClear, { capture: true, passive: true });
    window.addEventListener('resize', scheduleKeepClear, { passive: true });
    if (typeof MutationObserver !== 'undefined') {
      clearMo = new MutationObserver(scheduleKeepClear);
      clearMo.observe(document.body, {
        childList: true,
        subtree: true,
        attributes: true,
        attributeFilter: ['class', 'style', 'hidden'],
      });
    }
    scheduleKeepClear();
  } else if (!on && clearMo) {
    window.removeEventListener('scroll', scheduleKeepClear, { capture: true });
    window.removeEventListener('resize', scheduleKeepClear);
    clearMo.disconnect();
    clearMo = null;
    lift.value = 0;
    aside.value = false;
  }
}

watch(
  [installEl, loud],
  ([el, isLoud]) => watchClear(!!el && !isLoud),
  { flush: 'post', immediate: true },
);
// Closing the opened chip asks again: it may be standing on something now.
watch(open, (o) => {
  if (!o) scheduleKeepClear();
});

onBeforeUnmount(() => {
  watchClear(false);
  if (clearFrame && typeof cancelAnimationFrame !== 'undefined') cancelAnimationFrame(clearFrame);
  if (typeof window !== 'undefined') window.removeEventListener('resize', onViewportResize);
  ro?.disconnect();
  ro = null;
  if (typeof document !== 'undefined') {
    document.documentElement.style.removeProperty(BANNER_H_VAR);
    document.documentElement.classList.remove(BANNER_CLASS);
  }
});
</script>

<template>
  <!-- Service-worker update prompt (registerType: 'prompt').
       ⚠⚠ NOT given the corner treatment, on purpose. Everything else this
       component says is an OFFER the person may ignore forever; this one is
       the only thing here they actually have to act on — the bundle in their
       tab is stale until they press Reload — and it is transient, gone the
       moment they do. Shrinking the advert and shrinking the thing that says
       "you are running old code" are not the same decision. It keeps the
       full-width band, keeps z-50 above everything, and keeps publishing its
       height so a page can stay clear of it. -->
  <div
    v-if="needRefresh"
    ref="updateEl"
    class="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex justify-center px-4 pb-4"
    data-testid="pwa-update-banner"
  >
    <div class="pointer-events-auto flex w-full max-w-md items-center gap-3 ip-card ip-card--update">
      <span class="flex-1 ip-text">
        {{ $t('install.updateAvailable') }}
      </span>
      <button type="button" class="ip-btn" data-testid="pwa-update-reload" @click="reloadForUpdate">
        {{ $t('install.reload') }}
      </button>
    </div>
  </div>

  <!-- Install offer: the desktop app (PC), the native prompt (Chrome/Edge/
       Android) or the iOS instructions. One markup, two shells — see the
       header for why. -->
  <div
    v-if="shouldOfferInstall"
    ref="installEl"
    :class="['ip-dock', loud ? 'ip-dock--band' : 'ip-dock--corner', { 'ip-dock--aside': aside && !loud && !open }]"
    :style="!loud && lift ? { '--ip-lift': `${lift}px` } : undefined"
    :inert="(aside && !loud && !open) || undefined"
    :data-place="loud ? 'band' : aside && !open ? 'aside' : lift ? 'lifted' : 'corner'"
    data-testid="pwa-install-banner"
  >
    <div
      class="ip-card ip-card--install"
      :class="{ 'ip-card--chip': !expanded }"
      data-testid="pwa-install-card"
    >
      <div class="ip-head">
        <!-- On the sign-in page there is nothing to toggle, so the head is not
             a control. In the corner the WHOLE head opens it: a 16px caret is
             not a target on a phone. -->
        <component
          :is="loud ? 'div' : 'button'"
          :type="loud ? undefined : 'button'"
          class="ip-head__main"
          :class="{ 'ip-head__main--static': loud }"
          :aria-expanded="loud ? undefined : open"
          :aria-controls="loud ? undefined : 'filex-install-body'"
          :aria-label="toggleLabel ? $t(toggleLabel) : undefined"
          :data-testid="loud ? undefined : 'pwa-install-toggle'"
          @click="toggle"
        >
          <!-- Bound, not a static src: Vue's SFC compiler turns a literal `src`
               into a module import, and this file lives in public/ so Rollup
               cannot resolve it — the production build fails outright. Binding
               keeps it a plain runtime URL, and BASE_URL keeps it correct if the
               app's base ever moves off /admin/. -->
          <img
            :src="`${baseUrl}icons/icon.svg`"
            alt=""
            class="ip-mark"
          />
          <span class="ip-head__text">
            <span class="ip-title">
              {{ showDesktopDownload ? $t('install.desktopTitle') : $t('install.title') }}
            </span>
            <!-- ⚠ The subtitle is what the chip gives up, and it is the right
                 thing to give up: the title alone still names the offer, which
                 is the test a corner treatment has to pass. -->
            <span v-if="expanded" class="ip-muted">
              {{
                showDesktopDownload
                  ? $t('install.desktopSubtitle', { platform: desktopLabel })
                  : $t('install.subtitle')
              }}
            </span>
          </span>
          <span v-if="!loud" class="ip-caret" :class="{ 'ip-caret--open': open }" aria-hidden="true"
            >&#9662;</span
          >
        </component>
        <button
          type="button"
          class="ip-close"
          :aria-label="$t('common.close')"
          data-testid="pwa-install-dismiss"
          @click="dismiss"
        >
          <span aria-hidden="true">&times;</span>
        </button>
      </div>

      <div v-if="expanded" id="filex-install-body" class="ip-body">
        <!-- iOS: no programmatic prompt, walk the user through Share sheet. -->
        <p v-if="showIOSInstructions" class="ip-ios" data-testid="pwa-ios-instructions">
          {{ $t('install.iosInstructions') }}
        </p>

        <!-- PC: the useful install is the desktop app — it is the only build
             that syncs folders to disk and stays running in the tray. -->
        <template v-if="showDesktopDownload">
          <a
            v-for="d in desktopDownloads"
            :key="d.href"
            :href="d.href"
            class="flex items-center justify-between gap-3 ip-dl"
            data-testid="desktop-download-button"
          >
            <span class="min-w-0">
              <span class="block ip-dl__label">{{ d.label }}</span>
              <span class="block ip-muted">{{ d.hint }}</span>
            </span>
            <span aria-hidden="true" class="ip-dl__arrow">↓</span>
          </a>
          <a :href="RELEASES" target="_blank" rel="noopener noreferrer" class="block ip-all">
            {{ $t('install.desktopAllDownloads') }}
          </a>
        </template>

        <div v-else-if="canPromptInstall" class="flex justify-end">
          <button type="button" class="ip-btn" data-testid="pwa-install-button" @click="onInstall">
            {{ $t('install.install') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* gorunum:v1 — the banner sits over every page until it is dismissed, so it
   is painted from the same tokens as everything under it. Nothing here is a
   Tailwind palette name or a raw hex; the dark variant comes from the tokens
   themselves rather than from a `dark:` twin that has to be kept in step. */

/* ── the two shells ─────────────────────────────────────────────────────── */

.ip-dock {
  position: fixed;
  z-index: 40;
  pointer-events: none;
}
.ip-dock > * {
  pointer-events: auto;
}

/* Sign-in page: the full-width band it has always been. `pointer-events-none`
   on the wrapper is load-bearing — clicks pass through everywhere except the
   card itself, which is half of what keeps the submit button reachable. */
.ip-dock--band {
  inset-inline: 0;
  bottom: 0;
  display: flex;
  justify-content: center;
  padding: 0 16px 16px;
}
.ip-dock--band > * {
  width: 100%;
  max-width: 28rem;
}

/* In-app: the bottom-RIGHT corner.
   ⚠ Chosen by measurement, not by taste. Bottom-left is the sidebar's own
   footer (the storage read-out), top-right is the account menu, and the
   listing itself reads from the top-left down — so the bottom-right is the
   one corner of this product that is empty at rest.
   ⚠⚠ It is not empty when something is HAPPENING there: the toast stack
   (`ToastContainer.vue`, z-50) and the pending-ops tray (z-40) already share
   that corner. This chip is therefore the BOTTOM of that stack at z-30 — a
   transient thing the person has to read always draws over a standing advert,
   never the other way round.
   ⚠ `bottom` reads the update bar's published height, so the chip steps up
   over it instead of sitting under it on a narrow screen where the bar spans
   the full width. */
.ip-dock--corner {
  inset-inline-end: 12px;
  /* `--ip-lift`: standing on a pinned bar of actions (lib/keepClear.ts).
     ⚠ Not transitioned — keepClear reads the box and must never find it
     half-way between two positions. */
  bottom: calc(12px + var(--filex-install-banner-h, 0px) + var(--ip-lift, 0px));
  inset-inline-start: auto;
  z-index: 30;
  max-width: min(22rem, calc(100vw - 24px));
  transition: opacity 0.15s ease;
}
/* Something to press is under the corner: the chip steps aside. `visibility`
   (not only opacity) so it can be neither clicked nor tabbed to, and it keeps
   its box, so keepClear can still ask whether the corner is clear again. */
.ip-dock--aside {
  opacity: 0;
  visibility: hidden;
  transition: opacity 0.15s ease, visibility 0s linear 0.15s;
}

/* ── the card ───────────────────────────────────────────────────────────── */

.ip-card {
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-lg);
  background: var(--fe-bg);
  box-shadow: var(--fe-shadow);
  font-family: var(--fe-font);
}
.ip-card--update {
  padding: 12px;
}
.ip-card--install {
  padding: 16px;
}
/* Collapsed: a chip. The padding shrinks with it — a 16px inset around one
   line of text is what made the old card look like a dialog. */
.ip-card--chip {
  padding-block: 6px; padding-inline: 10px 6px;
  border-radius: 999px;
}
/* It is a control, so it says so on hover. Without this the chip reads as a
   label somebody left in the corner rather than as something to press. */
.ip-card--chip:hover {
  border-color: var(--fe-primary);
}

.ip-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}
.ip-card--chip .ip-head {
  align-items: center;
  gap: 4px;
}

.ip-head__main {
  display: flex;
  flex: 1 1 auto;
  min-width: 0;
  align-items: flex-start;
  gap: 12px;
  padding: 0;
  border: 0;
  background: none;
  font: inherit;
  color: inherit;
  text-align: start;
  cursor: pointer;
}
.ip-head__main--static {
  cursor: default;
}
.ip-card--chip .ip-head__main {
  align-items: center;
  gap: 8px;
}

.ip-head__text {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  flex-direction: column;
  gap: 2px;
}

.ip-mark {
  width: 40px;
  height: 40px;
  flex: 0 0 auto;
  border-radius: var(--fe-radius);
}
.ip-card--chip .ip-mark {
  width: 20px;
  height: 20px;
  border-radius: 6px;
}

.ip-caret {
  flex: 0 0 auto;
  padding: 0 2px;
  color: var(--fe-text-muted);
  font-size: 12px;
  line-height: 1;
  transition: transform 120ms ease;
}
/* On the expanded card the head is top-aligned (a 40px mark beside two lines
   of text), so the caret has to be nudged onto the title's line rather than
   floating above it. */
.ip-card--install:not(.ip-card--chip) .ip-caret {
  align-self: flex-start;
  margin-top: 4px;
}
.ip-caret--open {
  transform: rotate(180deg);
}

.ip-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 12px;
}

.ip-text {
  font-size: var(--fe-text-md);
  color: var(--fe-text);
}
.ip-title {
  font-size: var(--fe-text-md);
  font-weight: 600;
  color: var(--fe-text);
}
/* One line, and truncated rather than wrapped: a chip that grows to two lines
   on a long Turkish title stops being a chip. */
.ip-card--chip .ip-title {
  font-size: var(--fe-text-sm);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ip-muted {
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}
.ip-ios {
  font-size: var(--fe-text-xs);
  color: var(--fe-text);
}

.ip-btn {
  height: var(--fe-h-sm);
  padding: 0 14px;
  border: 1px solid var(--fe-primary);
  border-radius: var(--fe-radius);
  background: var(--fe-primary);
  font-family: inherit;
  font-size: var(--fe-text-md);
  font-weight: 500;
  color: var(--fe-text-on-primary);
  cursor: pointer;
}
.ip-btn:hover {
  background: var(--fe-primary-hover);
  border-color: var(--fe-primary-hover);
}

.ip-close {
  flex: 0 0 auto;
  padding: 4px;
  margin-block: -4px 0; margin-inline: 0 -4px;
  border: 0;
  border-radius: var(--fe-radius-sm);
  background: none;
  color: var(--fe-text-muted);
  line-height: 1;
  cursor: pointer;
}
/* The chip's body is one large target, so the × is the only small one on it —
   it gets a touch-sized box of its own rather than the 4px inset the card
   uses, where it sits in acres of padding already. */
.ip-card--chip .ip-close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  margin: 0;
  padding: 0;
  border-radius: 999px;
  font-size: 16px;
}
.ip-card--chip .ip-close:hover {
  background: var(--fe-bg-elev);
}
.ip-close:hover {
  color: var(--fe-text);
}

.ip-dl {
  padding: 8px 12px;
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  text-decoration: none;
}
.ip-dl:hover {
  border-color: var(--fe-primary);
}
.ip-dl__label {
  font-size: var(--fe-text-md);
  font-weight: 500;
  color: var(--fe-text);
}
.ip-dl__arrow {
  color: var(--fe-primary);
}

.ip-all {
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
  text-decoration: underline;
}
.ip-all:hover {
  color: var(--fe-text);
}

.ip-card :focus-visible {
  outline: 2px solid var(--fe-primary);
  outline-offset: 2px;
}
</style>
