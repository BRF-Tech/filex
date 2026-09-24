<script setup lang="ts">
/**
 * PublicLinkPreview — the public share page, drawn with values that are not
 * saved yet, for the admin's Corporate identity page.
 *
 * ⚠⚠ It is the REAL page's components — PublicShell and PublicShareBody, the
 * ones `/s/<token>` mounts — not a picture of them. The admin page had a
 * hand-drawn mock (a centred card with an icon and a full-width button) while
 * the page it previewed is left-aligned and plain (release-candidate sweep,
 * 2026-09-21, QA #25): the preview taught the operator a page that does not
 * exist. Anything the public page changes, this changes with it.
 *
 * Nothing is fetched: the "link" handed to the body is a fixed one-file share
 * (only the fields the one-document branch reads), and the accent goes
 * through the same derivation the page uses (accentStyleOf).
 *
 * ⚠ Inert: a preview is looked at, not used. The page's controls (the
 * download button, the language picker) must not act from inside the admin
 * panel.
 */
import { computed, ref } from 'vue';
import type { LocaleCode } from '../../types/ExplorerConfig';
import type { PublicShareInfo } from '../../types/Public';
import type { PublicLinkStore } from '../../composables/usePublicLink';
import { accentStyleOf, DEFAULT_BRAND_NAME } from '../../composables/usePublicBranding';
import { publicLayoutFor } from '../../lib/publicLayout';
import PublicShell from './PublicShell.vue';
import PublicShareBody from './PublicShareBody.vue';

const props = defineProps<{
  brandName?: string;
  logoUrl?: string;
  /** A hex colour, as the operator typed it; anything else means "none". */
  accent?: string;
  footerText?: string;
  hidePoweredBy?: boolean;
  locale: LocaleCode | string;
  theme?: 'light' | 'dark';
  /** The file the sample link shares. */
  sample?: { name: string; size: number; mime?: string };
}>();

const file = computed(() => props.sample ?? { name: 'report-2026.pdf', size: 1_240_000, mime: 'application/pdf' });

const info = computed<PublicShareInfo>(() => ({
  kind: 'file',
  node: { name: file.value.name, size: file.value.size, mime: file.value.mime ?? 'application/pdf' },
}) as PublicShareInfo);

/* The body reads `info` and, for a single document, `downloadUrl()`; it
   touches nothing else of a link on that branch. */
const link = computed(
  () =>
    ({
      info: ref(info.value),
      downloadUrl: () => '#',
      noJsUrl: () => '#',
    }) as unknown as PublicLinkStore<PublicShareInfo> & {
      downloadUrl: (o?: { zip?: boolean; path?: string }) => string;
      noJsUrl: () => string;
    },
);

const name = computed(() => props.brandName?.trim() || DEFAULT_BRAND_NAME);
</script>

<template>
  <div class="fe-ppreview" inert data-testid="public-link-preview">
    <PublicShell
      status="ready"
      :locale="locale"
      :theme="theme ?? 'light'"
      :brand-name="name"
      :logo-url="logoUrl?.trim() || ''"
      :accent-style="accentStyleOf(accent)"
      :footer-text="footerText?.trim() || ''"
      :hide-powered-by="hidePoweredBy === true"
      :layout="publicLayoutFor('file')"
      badge="file"
      :title="file.name"
    >
      <PublicShareBody :link="link" :locale="locale" :theme="theme ?? 'light'" />
    </PublicShell>
  </div>
</template>
