<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import { RouterView } from 'vue-router';
import ToastContainer from '@/components/ToastContainer.vue';
import InstallPrompt from '@/components/InstallPrompt.vue';
import NotificationsPanel from '@/components/NotificationsPanel.vue';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { completeDesktopHandoff, hasPendingHandoff } from '@/lib/desktopHandoff';
import { useNotificationWatcher } from '@/composables/useNotificationWatcher';
import { onPublicPageBase } from '@/router';
import {
  ConnectionNotice,
  applyStoredPalette,
  forgetPersonalPrefs,
  hydratePrefs,
  rememberSession,
} from '@brftech/filex-core';
import { applySessionLook } from '@/lib/instanceThemes';
import { applyPrefLocale, loadOfferedLocales } from '@/i18n';
import { applyAccountTheme } from '@/lib/theme';
import { applyAccountDensity } from '@/lib/density';
import { installTableEnv } from '@/lib/tableEnv';

const auth = useAuthStore();
const caps = useCapabilitiesStore();

/* Every table in the panel is the core DataTable (the explorer's table — there
 * is no other); it learns the panel's language and light/dark mode here, once,
 * instead of from a prop on each of thirty call sites (lib/tableEnv). */
installTableEnv();

/**
 * `/s/`, `/d/` (and the retired `/p/`): a stranger with a link, not a user.
 *
 * ⚠⚠ The application's own chrome is hidden for them (v3 §1). Measured in
 * a browser on a real share link, 2026-09-20: the PWA's "a new version is
 * available — Reload" banner was sitting on a page whose whole purpose is
 * somebody else's document, in the same spot as its download button. This
 * product has nothing to say to a visitor about ITSELF.
 */
const isPublicLink = onPublicPageBase();

// The bell's poll, moved up to the root so it also runs on the screens that
// have no bell — /drive/explore is the whole product for a non-admin. Same
// 15 s cadence, one loop, and it is what raises browser notifications.
useNotificationWatcher();

const handingOff = ref(false);
const handoffCode = ref<string | null>(null);
const handoffError = ref(false);
const copied = ref(false);

async function copyCode() {
  if (!handoffCode.value) return;
  try {
    await navigator.clipboard.writeText(handoffCode.value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1500);
  } catch {
    /* clipboard blocked — the field is selectable, which is the fallback */
  }
}

// Desktop authorization: if the browser got here to authorize the desktop app,
// finish that instead of dropping the user into a file manager they did not come
// for. Lives here rather than in the login view because the OIDC path never
// returns to that view — the backend callback lands the browser on /admin/.
//
// ⚠ Driven by a WATCH on the session, not by onMounted alone. onMounted fires
// once, before any password is typed, so it only ever caught a hand-off that was
// already signed in when the tab opened: OIDC (the backend callback reloads the
// document) and non-admins signing in with a password (the router sends them to
// /drive/explore with `window.location.replace`, which is a real navigation —
// see router/index.ts, and note the pairing survives it because it is stashed in
// sessionStorage). An ADMIN signing in with a password stays inside the SPA:
// `router.push` from Login.vue re-renders the view and nothing remounts App.vue,
// so the hand-off never ran and the desktop app sat on its waiting screen.
// Watching `auth.user` covers every one of those routes, because all of them end
// with a session appearing in this store.
let handoffStarted = false;

async function runHandoffIfPending() {
  // completeDesktopHandoff() clears the stash before it awaits, but the guard is
  // still needed: two ticks can enter before the first one gets there, and the
  // second would overwrite a shown code with a null one.
  if (handoffStarted) return;
  if (!auth.user || !hasPendingHandoff()) return;
  handoffStarted = true;
  handingOff.value = true;
  try {
    handoffCode.value = await completeDesktopHandoff();
  } catch {
    // Leave the flag up and SAY it failed. This branch used to drop the
    // overlay (`handingOff.value = false`) — the exact "looks like nothing
    // happened at all" its own comment warned about: the user came from the
    // desktop app, the mint failed, and the browser showed a file manager
    // with no code and no error. The desktop app still shows its own
    // timeout; this screen owns telling the user the browser half failed.
    handoffError.value = true;
  }
}

watch(() => auth.user, runHandoffIfPending);

// ⚠⚠ And the account's LOOK, on the same signal and for the same reason the
// hand-off needs it: an admin signing in with a password never leaves the SPA
// (`router.push` from Login.vue re-renders the view, App.vue does not remount),
// so `onMounted` below has already run and returned early with no session. Left
// on that signal alone, a person who signed in got their own palette only after
// a full page load — invisible before the sign-in page stopped reading this
// browser's mirror, and a plainly wrong screen afterwards.
watch(() => auth.user, () => void applyAccountPrefs());

/**
 * Who this window's look was last decided FOR — an account id, `null` for
 * nobody, `undefined` for "not decided yet".
 *
 * ⚠ Two signals reach `applyAccountPrefs` and both are needed: the watcher
 * below (an in-SPA sign-in, where nothing remounts) and `onMounted` (a cold
 * load, where the router guard may already have hydrated the session so the
 * watcher never fires). On the ordinary cold load BOTH arrive, and without this
 * marker that is two `GET /api/me/prefs` for one page load.
 */
let lookDecidedFor: number | null | undefined;

/**
 * The account's own look and language (v3): theme, palette, row density and
 * language, from `GET /api/me/prefs?surface=web`.
 *
 * ⚠⚠ This is the fix for "the palette I picked in one browser is not in the
 * other one". The four preferences were each stored in `localStorage` and
 * nowhere else, so they were facts about a MACHINE; they are facts about a
 * person. localStorage is still read first — in `main.ts`, before the mount —
 * so nothing flashes while this fetch is out.
 *
 * ⚠ The fetch happens only when a session exists: the route is authenticated,
 * and asking before sign-in would be a 401 on every cold load of the login
 * page. The NO-session branch is not a no-op though — it is where this window
 * is told to stop wearing a person (see below).
 *
 * ⚠ Each value is applied through an `applyAccount*` helper that does NOT
 * write back. Calling the ordinary setter here would turn every tab's
 * hydration into a PUT and make the last tab opened the one that wins.
 */
async function applyAccountPrefs(): Promise<void> {
  const who = auth.user?.id ?? null;
  if (lookDecidedFor === who) return;
  lookDecidedFor = who;
  // ⚠⚠ THE ANSWER TO "is anybody signed in?", recorded for this browser's
  // NEXT first paint (`@brftech/filex-core` → lib/prefs, SESSION_LS_KEY). It
  // goes both ways on purpose. `true` is what lets a returning window paint the
  // person's own palette on frame one instead of flashing the instance default
  // at them every single load; `false` is what shuts the leak measured on
  // 2026-09-21 — a browser whose session had simply expired, or whose user
  // closed the tab rather than signing out, still wore that person's palette on
  // the sign-in page and hid the operator's own default behind it. Both states
  // are corrected within one round trip of this call, on every entry path.
  if (!auth.isAuthenticated) {
    // ⚠⚠ The session is GONE, not merely absent — this is the one place the
    // app learns it for every reason it can happen: a cold load of the sign-in
    // page, a cookie that expired, a tab closed without signing out, a second
    // person sitting down at the same browser. `forgetPersonalPrefs` drops the
    // departed person's mirrors (owner, 2026-09-21: "oturum kapanınca
    // temizlersek localstorage'ı tamamız ya o kısımda") and records the
    // session answer in the same breath.
    //
    // ⚠ Then take the look back OFF. The window has already painted from the
    // hint, so stopping is not enough: the previous person's palette may be on
    // screen at this instant.
    forgetPersonalPrefs();
    applySessionLook();
    return;
  }
  rememberSession(true);
  const prefs = await hydratePrefs();
  if (!prefs) return;
  applyAccountTheme(prefs.theme);
  // ⚠⚠ `prefs.palette`, NEVER `prefs.palette ?? ''`. An account with no
  // palette key has not been asked the question on this server — it has not
  // answered "the stock palette" — and `''` used to resolve to the stock id
  // and overwrite whatever this browser was already correctly showing, mirror
  // and all. e2e 98-custom-theme.spec.ts:97 caught it as a flake because the
  // overwrite lands mid-test: the polled `--fe-primary` read wins the race
  // against this fetch and the unpolled `--fe-bg` read on the next line loses
  // it. The three preferences beside this one already treated silence as
  // silence (`applyAccountTheme`, `applyAccountDensity`, `applyPrefLocale` all
  // return early); the palette was the only one that did not, and
  // `applyStoredPalette` now refuses a falsy id outright so no future caller
  // can reintroduce it.
  applyStoredPalette(prefs.palette);
  applyAccountDensity(prefs.density);
  applyPrefLocale(prefs.locale);
}

onMounted(async () => {
  // A public link (`/s/`, `/d/`, the retired `/p/`) has no session and asks
  // for none: a 401 from /api/auth/me on a matched route would send the
  // visitor to a sign-in form (api/client.ts → onUnauthorized). Nothing below
  // applies to them either — no hand-off, no capabilities, no preferences.
  if (onPublicPageBase()) return;
  // Hydrate session + capabilities up-front so route guards have data.
  // Errors are swallowed: an unauthenticated user just lands on /admin/login.
  await Promise.allSettled([auth.fetchMe(), caps.fetch()]);
  // ⚠ Not awaited together with the two above: the preferences need the
  // session those two establish, and the language list is a nicety nothing
  // should wait for.
  void applyAccountPrefs();
  void loadOfferedLocales();
  // The router guard hydrates the session before this component mounts on a
  // cold load, in which case the watcher above never fired — so ask once here.
  await runHandoffIfPending();
});
</script>

<template>
  <!-- While handing a credential back to the desktop app, say so. The window
       is about to be navigated to a filex:// URL and the panel behind is not
       what the user came for. -->
  <div
    v-if="handingOff"
    class="fixed inset-0 z-50 flex items-center justify-center bg-white/95 dark:bg-zinc-950/95"
    data-testid="desktop-handoff"
  >
    <div class="w-full max-w-sm px-6 text-center">
      <p v-if="handoffError" class="text-sm text-red-600 dark:text-red-400" data-testid="handoff-error">
        {{ $t('desktop.handoffError') }}
      </p>
      <p v-else class="text-sm text-zinc-600 dark:text-zinc-300">{{ $t('desktop.handoff') }}</p>
      <!-- The code is shown, not hidden behind a failure: a browser silently
           does nothing when no filex:// handler is registered, so there is no
           event to react to. Whoever needs it can copy it into the app. -->
      <template v-if="handoffCode">
        <p class="mt-6 text-xs text-zinc-500 dark:text-zinc-400">{{ $t('desktop.codeHint') }}</p>
        <div class="mt-2 flex items-center gap-2">
          <input
            readonly
            :value="handoffCode"
            data-testid="handoff-code"
            class="w-full rounded-lg border border-zinc-300 bg-white px-3 py-2 font-mono text-xs text-zinc-800 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100"
            @focus="($event.target as HTMLInputElement).select()"
          />
          <button
            type="button"
            class="shrink-0 rounded-lg border border-zinc-300 px-3 py-2 text-xs text-zinc-700 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
            @click="copyCode"
          >
            {{ copied ? $t('desktop.copied') : $t('desktop.copy') }}
          </button>
        </div>
      </template>
    </div>
  </div>

  <!-- ⚠⚠ NO `mode="out-in"` on this transition, and that is a fix, not a
       preference.
       `out-in` serialises the route change: the entering component mounts only
       after the leaving one's leave transition RESOLVES. When that resolution
       never arrives the RouterView renders nothing at all — and it stays that
       way for every navigation afterwards, because the transition never
       releases. Measured 2026-09-13: signing in left `#app` holding only the
       toast container and the install banner, with the route matched, its
       component resolved and NOT ONE warning or error in the console; pushing
       another route from the console rendered nothing either. It looked like a
       dead application and it was one line of decoration.
       Without `mode`, the two views cross-fade for 120ms — imperceptible on a
       full-page route change, and it cannot deadlock. -->
  <RouterView v-slot="{ Component, route }">
    <transition name="fade">
      <component :is="Component" :key="route.path" />
    </transition>
  </RouterView>
  <!-- ⚠⚠ ONE line while the server cannot be reached, instead of a toast per
       failed request. A dropped connection is a STATE, and the corner used to
       fill with copies of one sentence because every answerless call raised
       its own (owner, 2026-09-24). The folding rule is shared
       (core lib/connection); this only draws it, and it takes itself down the
       moment anything gets an answer.

       ⚠ Same exclusion as the toasts below: a visitor with a share link is
       reading somebody else's document, and that page says for itself when it
       cannot be loaded. -->
  <ConnectionNotice v-if="!isPublicLink" :locale="$i18n.locale" />
  <!-- ⚠ Toasts are the application's voice (a failed upload, a copied
       link). A visitor with a share link is not using the application and
       has nothing to be told in its words. -->
  <ToastContainer v-if="!isPublicLink" />
  <!-- ALL of the signed-in person's notifications — the screen "see all"
       opens (docs/NOTIFICATIONS.md → "The bell, and who can reach it",
       rule 2).

       ⚠⚠ Rendered HERE, at the root, and opened through the store, for the
       same reason the poll is here: the bell is drawn in two headers (the
       admin nav and the explorer's) and the screen must be exactly one. A
       panel per bell would be two panels, and the one nobody could see would
       still be fetching pages.

       ⚠ Not for a share visitor: `/s/` is somebody else's document, and this
       is the application talking about itself. -->
  <NotificationsPanel v-if="!isPublicLink" />
  <!-- PWA install + update banner. Standalone SPA only (see component note).

       ⚠ Not before sign-in. The banner is fixed to the bottom centre of the
       viewport, and the sign-in form's primary button is in that same place —
       so on the login screen it sits on top of the one control the page exists
       for. It also has nothing to say to somebody who does not yet have an
       account on this server. This is the second time this component has
       covered something: the first was fixed by making the wrapper
       pointer-events-none, which lets clicks through everywhere EXCEPT the
       card itself, and the card grew a row taller when the Windows portable
       download was added. Guarded by 91-install-banner-login.cy.ts. -->
  <InstallPrompt v-if="!isPublicLink" />
</template>

<style>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 120ms ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
