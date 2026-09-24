<script setup lang="ts">
/**
 * AppScreen — an app plugin's `home` view as a page of its own, in the same
 * tab (`/app/:plugin/:view?section=`). The explorer's "Apps" rows open it.
 *
 * ⚠⚠ The owner, 2026-09-21: "İmzalar popup açıyor ama bence popup yerine
 * kendi sayfasını açsın ve her biri ayrı bir menü içinde farklı tablolar
 * göstersin" — and then: an in-app page, the way My shares is, with its
 * sections in a menu, the Back button working, and a notification landing
 * on the right section. So this page is laid out like My shares (a way back
 * to the files, a title, the content), and it owns the ADDRESS: the section
 * on screen is `?section=`, pushed as history, so Back walks the sections
 * and a link can name one.
 *
 * ⚠ GENERIC, like AppHome in the admin panel: every running app with a
 * `home` view reaches it, and nothing here knows a plugin's name. The
 * conversation itself is `PluginPageView` from the package (its `embedded`
 * frame — this page brings its own heading and width), so this page, the
 * admin panel's copy and the new-tab `page` view cannot drift apart.
 */
import { computed } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ArrowLeft } from 'lucide-vue-next';
import { PluginPageView, useFileApi } from '@brftech/filex-core';
import type { ExplorerConfig } from '@brftech/filex-core';

import { explorerAuth } from '@/lib/explorerConfig';
import { currentMountBase } from '@/router';
import { useAppHomeRoute } from '@/composables/useAppHomeRoute';

const router = useRouter();
const { t } = useI18n();

// The address (`?section=` included), the heading and the icon — the same
// reading the admin panel's copy of this page uses.
const { plugin, view, section, onSection, title, icon, uiLocale, theme } = useAppHomeRoute();

// ⚠ `locale` rides in the config: every call carries it as Accept-Language
// and the plugin answers in it (the same reason AppPage.vue passes it).
const api = computed(() =>
  useFileApi({ apiBase: '', auth: explorerAuth(), locale: uiLocale.value } as ExplorerConfig),
);

/** The explorer — where a queued job's tray is, and "back to the files". */
const exploreHref = computed(() => `${currentMountBase()}explore`);

/**
 * Back to the files — the explorer, which reopens the folder the person was
 * in (it remembers it). ⚠ Not `history.back()`: the sections of this page
 * are steps in history too (so the BROWSER's Back walks them), and a "back
 * to the files" that stepped back one section at a time was measured doing
 * exactly that.
 */
function back(): void {
  void router.push({ name: 'explore' });
}

function onQueued(_op: Record<string, unknown>): void {
  /* the explorer's tray polls /api/files/ops — nothing to register here */
}
</script>

<template>
  <section class="mx-auto w-full max-w-5xl space-y-4 p-4 sm:p-6" data-testid="app-screen">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <button type="button" class="app-screen__back inline-flex items-center gap-1 text-sm" data-testid="app-screen-back" @click="back">
          <ArrowLeft class="h-4 w-4" aria-hidden="true" />
          {{ t('appScreen.back') }}
        </button>
        <h1 class="mt-2 flex items-center gap-2 text-2xl font-semibold" data-testid="app-screen-title">
          <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
          <span v-if="icon" class="app-screen__icon" aria-hidden="true" v-html="icon"></span>
          {{ title }}
        </h1>
      </div>
    </header>

    <div class="card">
      <PluginPageView
        :key="`${plugin}/${view}/${uiLocale}`"
        frame="embedded"
        :locale="uiLocale"
        :theme="theme"
        :api="api"
        :plugin="plugin"
        :view="view"
        :section="section"
        :ops-href="exploreHref"
        :mount-base="currentMountBase()"
        @section="onSection"
        @op="onQueued"
      />
    </div>
  </section>
</template>

<style scoped>
/* Tokens, not Tailwind colours: the page follows the palette the person
   picked (the same reason AppHome.vue and MyShares' siblings use them). */
.app-screen__back {
  color: var(--fe-text-muted);
}
.app-screen__back:hover {
  color: var(--fe-text);
}
.app-screen__icon :deep(svg) {
  width: 24px;
  height: 24px;
  display: block;
  color: var(--fe-text-muted);
}
</style>
