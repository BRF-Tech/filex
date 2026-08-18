<script setup lang="ts">
import { onMounted, ref } from 'vue';
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

onMounted(async () => {
  // Hydrate session + capabilities up-front so route guards have data.
  // Errors are swallowed: an unauthenticated user just lands on /admin/login.
  await Promise.allSettled([auth.fetchMe(), caps.fetch()]);

  // Desktop authorization: if the browser got here to authorize the desktop
  // app, finish that instead of dropping the user into a file manager they did
  // not come for. Runs here rather than in the login view because the OIDC path
  // never returns to that view — the backend callback lands on /admin/.
  if (auth.user && hasPendingHandoff()) {
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
  <!-- PWA install + update banner. Standalone SPA only (see component note). -->
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
