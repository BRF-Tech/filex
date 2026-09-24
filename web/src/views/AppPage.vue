<script setup lang="ts">
/**
 * AppPage — an app plugin's `page` view, in a tab of its own
 * (`{base}apps/:plugin/:view?path=…`, docs/APP-PLUGINS-API.md → Placements).
 *
 * The file menu opens this instead of a dialog when the action's view is
 * placed `page`: a signing wizard wants the document beside it, and a modal's
 * scroll inside a scroll is exactly the wrong frame for that.
 *
 * ⚠ This file is the ROUTE, not the screen. Everything a plugin surface does
 * — asking for the first surface, the debounced `change`, the footer's
 * `submit`, the `{op}` answer, the queued state — is `PluginPageView` in
 * `@brftech/filex-core`, so fm.example.com, the desktop shell and the embeds draw
 * the identical page. What this file adds is the three things only the SPA
 * knows: how it authenticates, where "back to the files" goes, and that a
 * queued job should be waiting in the explorer's tray when the person
 * returns.
 */
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { PluginPageView, useFileApi } from '@brftech/filex-core';
import type { ExplorerConfig } from '@brftech/filex-core';
import { explorerAuth } from '@/lib/explorerConfig';
import { getStoredLocale } from '@/i18n';
import { effectiveTheme } from '@/lib/theme';
import { currentMountBase } from '@/router';

// The product's palette — main.ts already loads it for the whole app; the
// import is the guarantee for a build that mounts this view on its own.
import '@brftech/filex-core/style.css';

const route = useRoute();

const plugin = computed(() => String(route.params.plugin ?? ''));
const view = computed(() => String(route.params.view ?? ''));
const path = computed(() => (typeof route.query.path === 'string' ? route.query.path : undefined));

// The ACTIVE language (web/src/i18n decides it) — a language pack's included;
// this was `tr ? tr : en`, so the page chrome stayed English under Spanish.
const locale = computed<string>(() => getStoredLocale());
const theme = computed(() => effectiveTheme());

// ⚠ The DEFAULT plugin endpoints, not a copy of them: `useFileApi` derives
// every `/api/files/plugins/…` template from the same config the explorer
// hands it, so this page and the dialog talk to one set of routes.
// ⚠ `locale` is part of the config for a reason: every call this client
// makes carries it as `Accept-Language`, and the server renders a
// plugin's screens in that language. Leaving it out asked a Turkish
// window's wizard for English labels.
const api = useFileApi({ apiBase: '', auth: explorerAuth(), locale: locale.value } as ExplorerConfig);

/**
 * Back to the files, and to the tray. Both are the explorer, so both are the
 * same address — but they are named separately because they answer different
 * questions ("I am done here" / "where did my job go?"), and a future split
 * should not have to find two call sites.
 *
 * ⚠ Built from the mount base by hand: this is an `href` the browser follows
 * in a tab the router never navigated, so `router.resolve` would give the
 * right path only by accident of the base already being applied.
 */
const exploreHref = computed(() => `${currentMountBase()}explore`);

/**
 * A job was queued from this page. ⚠ Nothing is registered in a tray HERE:
 * the tray lives in the explorer, in the other tab, and it polls
 * `/api/files/ops` — so the row is already on its way there without this page
 * doing anything. The handler exists to keep the contract explicit rather
 * than to leave a silent `@op` on the component.
 */
function onQueued(_op: Record<string, unknown>): void {
  /* the explorer's own poll picks the row up; see the note above */
}
</script>

<template>
  <PluginPageView
    :locale="locale"
    :theme="theme"
    :api="api"
    :plugin="plugin"
    :view="view"
    :path="path"
    :back-href="exploreHref"
    :ops-href="exploreHref"
    :mount-base="currentMountBase()"
    @op="onQueued"
  />
</template>
