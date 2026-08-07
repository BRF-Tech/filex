<script setup lang="ts">
// Standalone-SPA install + update banner. Mounted once at the app root
// (App.vue). Self-hides unless the browser offers an install, the user is on
// iOS (manual add-to-home-screen), or a new service-worker build is waiting.
//
// ⚠ web/ only — never rendered inside the embedded <filex-explorer> hosts.
import { computed } from 'vue';
import { useInstallPrompt } from '@/composables/useInstallPrompt';

// Public-asset base ('/admin/' today). Used for the icon below — see the
// comment on the <img> for why it must not be a static src.
const baseUrl = import.meta.env.BASE_URL;

// Where the installers live. Kept as a single constant so the banner and the
// docs cannot drift apart.
const RELEASES = 'https://github.com/BRF-Tech/filex/releases/latest';
const desktopLabel = computed(() =>
  desktopPlatform.value === 'windows'
    ? 'Windows (.exe)'
    : desktopPlatform.value === 'linux'
      ? 'Linux (.AppImage / .deb)'
      : 'macOS',
);

const {
  canPromptInstall,
  desktopPlatform,
  showDesktopDownload,
  showIOSInstructions,
  shouldOfferInstall,
  needRefresh,
  promptInstall,
  dismiss,
  reloadForUpdate,
} = useInstallPrompt();

async function onInstall() {
  const outcome = await promptInstall();
  // If the user accepted, the appinstalled handler clears the offer; on
  // dismiss we hide the banner so it isn't immediately re-shown.
  if (outcome === 'dismissed') dismiss();
}
</script>

<template>
  <!-- Service-worker update prompt (registerType: 'prompt'). Sits above the
       install banner; both are fixed to the bottom of the viewport. -->
  <div
    v-if="needRefresh"
    class="fixed inset-x-0 bottom-0 z-50 flex justify-center px-4 pb-4"
    data-testid="pwa-update-banner"
  >
    <div
      class="flex w-full max-w-md items-center gap-3 rounded-xl border border-indigo-500/30 bg-white p-3 shadow-lg dark:bg-zinc-900"
    >
      <span class="flex-1 text-sm text-zinc-700 dark:text-zinc-200">
        {{ $t('install.updateAvailable') }}
      </span>
      <button
        type="button"
        class="rounded-lg bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
        data-testid="pwa-update-reload"
        @click="reloadForUpdate"
      >
        {{ $t('install.reload') }}
      </button>
    </div>
  </div>

  <!-- Install offer: native prompt (Chrome/Edge/Android) or iOS instructions. -->
  <div
    v-if="shouldOfferInstall"
    class="fixed inset-x-0 bottom-0 z-40 flex justify-center px-4 pb-4"
    data-testid="pwa-install-banner"
  >
    <div
      class="w-full max-w-md rounded-xl border border-zinc-200 bg-white p-4 shadow-lg dark:border-zinc-700 dark:bg-zinc-900"
    >
      <div class="flex items-start gap-3">
        <!-- Bound, not a static src: Vue's SFC compiler turns a literal `src`
             into a module import, and this file lives in public/ so Rollup
             cannot resolve it — the production build fails outright. Binding
             keeps it a plain runtime URL, and BASE_URL keeps it correct if the
             app's base ever moves off /admin/. -->
        <img
          :src="`${baseUrl}icons/icon.svg`"
          alt=""
          class="h-10 w-10 shrink-0 rounded-lg"
        />
        <div class="min-w-0 flex-1">
          <p class="text-sm font-semibold text-zinc-900 dark:text-zinc-50">
            {{ showDesktopDownload ? $t('install.desktopTitle') : $t('install.title') }}
          </p>
          <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">
            {{
              showDesktopDownload
                ? $t('install.desktopSubtitle', { platform: desktopLabel })
                : $t('install.subtitle')
            }}
          </p>

          <!-- iOS: no programmatic prompt, walk the user through Share sheet. -->
          <p
            v-if="showIOSInstructions"
            class="mt-2 text-xs text-zinc-600 dark:text-zinc-300"
            data-testid="pwa-ios-instructions"
          >
            {{ $t('install.iosInstructions') }}
          </p>
        </div>
        <button
          type="button"
          class="-mr-1 -mt-1 rounded-md p-1 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200"
          :aria-label="$t('common.close')"
          data-testid="pwa-install-dismiss"
          @click="dismiss"
        >
          <span aria-hidden="true">&times;</span>
        </button>
      </div>

      <!-- PC: the useful install is the desktop app — it is the only build
           that syncs folders to disk and stays running in the tray. -->
      <div v-if="showDesktopDownload" class="mt-3 flex justify-end">
        <a
          :href="RELEASES"
          target="_blank"
          rel="noopener noreferrer"
          class="rounded-lg bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
          data-testid="desktop-download-button"
        >
          {{ $t('install.desktopDownload', { platform: desktopLabel }) }}
        </a>
      </div>

      <div
        v-else-if="canPromptInstall"
        class="mt-3 flex justify-end"
      >
        <button
          type="button"
          class="rounded-lg bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
          data-testid="pwa-install-button"
          @click="onInstall"
        >
          {{ $t('install.install') }}
        </button>
      </div>
    </div>
  </div>
</template>
