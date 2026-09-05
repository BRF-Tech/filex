<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import { RouterView } from 'vue-router';
import ToastContainer from '@/components/ToastContainer.vue';
import InstallPrompt from '@/components/InstallPrompt.vue';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { completeDesktopHandoff, hasPendingHandoff } from '@/lib/desktopHandoff';

const auth = useAuthStore();
const caps = useCapabilitiesStore();

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

onMounted(async () => {
  // Hydrate session + capabilities up-front so route guards have data.
  // Errors are swallowed: an unauthenticated user just lands on /admin/login.
  await Promise.allSettled([auth.fetchMe(), caps.fetch()]);
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

  <RouterView v-slot="{ Component, route }">
    <transition name="fade" mode="out-in">
      <component :is="Component" :key="route.path" />
    </transition>
  </RouterView>
  <ToastContainer />
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
  <InstallPrompt />
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
