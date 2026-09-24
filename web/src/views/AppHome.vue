<script setup lang="ts">
/**
 * AppHome — an app plugin's `home` view, inside the admin panel.
 *
 * The owner's words, 2026-09-20: "app altındaki signatures menüsünde açtığım
 * ongoing sign işlemlerini de görebiliyor ve durumlarına bakabiliyor olmalıyım
 * (admin isem o da adminde de ayrı bir menüde signature tablosunu görebiliyor
 * olmalıyım bence)". The second half is this page — and it is deliberately
 * NOT a signatures page.
 *
 * ⚠⚠ GENERIC DOOR, never a page per plugin. Every installed, running plugin
 * that ships a `home` view gets a row in the sidebar's "Apps" section
 * (`usePluginHomeApps`) and lands here; the signing app's request table is
 * simply the first thing to walk through it. A page that knew about `sign`
 * would have to be written again for the next plugin, and the one after that.
 *
 * ⚠ This file is the ROUTE, not the screen. The conversation — asking for the
 * first surface, the debounced `change`, the footer's `submit`, the `{op}`
 * answer — is `PluginPageView` in `@brftech/filex-core`, the very component
 * the new-tab `page` view uses (`views/AppPage.vue`), drawn here in its
 * `embedded` frame so the panel keeps its own heading and width. What this
 * file adds is what only the panel knows: how it authenticates, what the row
 * is called in the sidebar, and where a queued job can be watched.
 */
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { PluginPageView, useFileApi } from '@brftech/filex-core';
import type { ExplorerConfig } from '@brftech/filex-core';

import { explorerAuth } from '@/lib/explorerConfig';
import { currentMountBase } from '@/router';
import { useAppHomeRoute } from '@/composables/useAppHomeRoute';

const { t } = useI18n();

// The address, the heading and the section — read once for this page and the
// explorer's own copy of it (views/AppScreen.vue), so the panel's Back walks
// the sections too.
const { plugin, view, section, onSection, title, icon, uiLocale, theme } = useAppHomeRoute();

// ⚠ The DEFAULT plugin endpoints, not a copy of them — the same `useFileApi`
// the explorer and the new-tab page view derive theirs from.
const api = useFileApi({ apiBase: '', auth: explorerAuth() } as ExplorerConfig);

/**
 * Where a job queued from this screen can be watched. The panel has a Queue
 * page of its own, which is the operations tray's admin-side twin — so an
 * administrator who submits something here is sent to the page that already
 * answers "where did my job go?" rather than out to the explorer.
 */
const queueHref = computed(() => `${currentMountBase()}queue`);

function onQueued(_op: Record<string, unknown>): void {
  /* the Queue page polls `/api/admin/queue`; nothing to register here */
}
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center gap-2">
      <!-- eslint-disable-next-line vue/no-v-html, vue/max-attributes-per-line -- static markup from lib/actionIcons -->
      <span v-if="icon" class="app-home__icon text-brand-600 dark:text-brand-400" aria-hidden="true" v-html="icon"></span>
      <h1
        class="text-xl font-semibold"
        data-testid="app-home-title"
      >
        {{ title }}
      </h1>
      <span class="app-home__plugin tbl-mono">{{ plugin }}</span>
      <RouterLink
        :to="{ name: 'plugins' }"
        class="app-home__manage ms-auto text-xs"
      >
        {{ t('nav.plugins') }}
      </RouterLink>
    </header>

    <div class="card">
      <PluginPageView
        :key="`${plugin}/${view}`"
        frame="embedded"
        :locale="uiLocale"
        :theme="theme"
        :api="api"
        :plugin="plugin"
        :view="view"
        :section="section"
        :ops-href="queueHref"
        :mount-base="currentMountBase()"
        @section="onSection"
        @op="onQueued"
      />
    </div>
  </section>
</template>

<style scoped>
/* Only the icon's box. ⚠ The surface inside is the package's to draw — see
   the lessons about a host reaching into `:deep(.fe-…)`: the frame prop is
   how this page asks for different chrome, not a stylesheet of its own. */
.app-home__icon :deep(svg) {
  width: 20px;
  height: 20px;
  display: block;
}
/* ⚠ Tokens, not `text-zinc-500`: the panel follows the palette the person
   picked, and a Tailwind colour is a hex baked into a stylesheet. */
.app-home__plugin,
.app-home__manage {
  color: var(--fe-text-muted);
}
.app-home__manage:hover {
  color: var(--fe-text);
  text-decoration: underline;
}
</style>
