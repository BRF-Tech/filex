<script setup lang="ts">
/**
 * The web app's binding for a public link — and nothing more.
 *
 * ⚠⚠ v3 §1: the public shell lives in `@brftech/filex-core`
 * (`PublicLinkPage`), so a share, a file request and an app plugin's page are
 * ONE page wherever they are drawn — this SPA, the desktop app, an embed.
 * What is left for the web app is the address: which prefix served the
 * document, and the token in it. If anything about how a public page LOOKS
 * ever needs changing, it is changed in the package, once.
 *
 * ⚠ The stylesheet is imported here as well as in `main.ts` (one module,
 * resolved once) so a build that mounts only this view still paints.
 */
import { computed, onBeforeUnmount, onMounted } from 'vue';
import { PublicLinkPage } from '@brftech/filex-core';
import { setPageOwnsLanguage } from '@/i18n';
import '@brftech/filex-core/style.css';

const props = defineProps<{
  /**
   * From the route: `share` (`/s/`) or `request` (`/d/`).
   *
   * ⚠ `/p/` is retired (v3 §1) and the SERVER 301s it to the share, so
   * there is no third kind and no second code path waiting for one.
   */
  kind: 'share' | 'request';
  /** From the route; empty for a URL that is not a token. */
  token?: string;
}>();

const token = computed(() => props.token ?? '');

/* ⚠⚠ The language of a public page is the PAGE's, not this app's: there is
   no account behind a link, and the visitor's pick lives in their browser
   alone. Both used to write `<html lang>`, and this app's copy — restamped
   whenever the offered languages or a pack's strings arrived — won the race
   often enough to put a reader back into English mid-visit (v0.43.0). */
onMounted(() => setPageOwnsLanguage(true));
onBeforeUnmount(() => setPageOwnsLanguage(false));
</script>

<template>
  <PublicLinkPage :kind="kind" :token="token" />
</template>
