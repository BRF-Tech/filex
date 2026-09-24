<script setup lang="ts">
/**
 * PublicShareBody — what a share actually contains.
 *
 * Three bodies, one shell (`PublicShell`): a document to download, a folder,
 * or an app plugin's screen when the link carries one (v3 §1.1 — "an app's
 * public page IS a share"). The chrome around all three — branding, the PIN
 * gate, expiry, the language picker — is the shell's and is not repeated
 * here.
 *
 * ⚠ The surface is drawn by the SAME `SurfaceConversation` the explorer's
 * dialog and an app's page use. A visitor and a signed-in user must see one
 * surface rendered one way, or every plugin has to be tested twice.
 *
 * ⚠⚠ The bytes are NOT fetched through `/api/`. `/s/<token>` has always been
 * the address that serves the file and still is: the SPA is what a
 * JavaScript browser gets when it NAVIGATES there, and `?download=1` /
 * `?zip=1` are the escapes that mean "the thing, not the page"
 * (`handlers/public_shell.go`). A second download route under `/api/` would
 * be a second thing to keep working.
 */
import { computed } from 'vue';
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { PublicEntry, PublicShareInfo } from '../../types/Public';
import type { PublicLinkStore } from '../../composables/usePublicLink';
import { useLocale } from '../../composables/useLocale';
import SurfaceConversation from '../plugin/SurfaceConversation.vue';
import SurfaceFooterButtons from '../plugin/SurfaceFooterButtons.vue';

const props = defineProps<{
  link: PublicLinkStore<PublicShareInfo> & {
    downloadUrl: (o?: { zip?: boolean; path?: string }) => string;
    noJsUrl: () => string;
  };
  locale: LocaleCode | string;
  theme?: ThemeMode;
}>();

const emit = defineEmits<{
  (e: 'navigate', path: string): void;
}>();

const { t, formatSize, formatDate } = useLocale(() => props.locale);

const info = computed(() => props.link.info.value);
const kind = computed(() => info.value?.kind ?? 'file');
const node = computed(() => info.value?.node ?? null);
const app = computed(() => info.value?.app ?? null);
const entries = computed<PublicEntry[]>(() => info.value?.entries ?? []);

/**
 * A picture is shown, everything else is offered.
 *
 * ⚠ Deliberately narrow. A public page renders bytes a stranger was sent by
 * a stranger; an <img> is a decode the browser does every day, while an
 * inline frame of arbitrary content on our origin is not something a share
 * link should be able to ask for.
 */
const isImage = computed(() => (node.value?.mime ?? '').startsWith('image/'));

/** The path back up, or null at the share's root. */
const parent = computed(() => {
  const p = (info.value?.path ?? '').replace(/\/+$/, '');
  if (!p) return null;
  const i = p.lastIndexOf('/');
  return i <= 0 ? '' : p.slice(0, i);
});
</script>

<template>
  <!-- ── an app plugin's screen ─────────────────────────────────────── -->
  <template v-if="kind === 'app'">
    <!-- ⚠⚠ A document in an app's screen (a signing link's "see and
         approve" step) is fitted to the WINDOW, as the in-app page fits it —
         never drawn at the card's full width inside a fixed-height pane.
         Measured on a signing link, 2026-09-21: the page was 676px wide in a
         615px-tall pane, the signer saw the top 60% of it and none of their
         own boxes, which sat at the bottom — the owner's "arayüz berbat". -->
    <SurfaceConversation
      :conv="link.conv"
      :locale="locale as LocaleCode"
      :theme="theme"
      :file-url="link.resolveFile"
      :toast="link.toast.value"
      layout="page"
      page-height="min(80vh, 1000px)"
    />
    <div v-if="link.conv.footer.value.length" class="fe-ppage__actions">
      <SurfaceFooterButtons :conv="link.conv" :locale="locale as LocaleCode" testid-prefix="public-page" />
    </div>
    <!-- ⚠ The files an app exposes are drawn the way a SHARED FILE is —
         its name and size, and a Download button — because to the person
         holding the link that is what they are: a signer's copy of the
         document is a file somebody shared with them. It used to be a
         "Documents" heading over a bare link, a second way of saying the
         same thing on what should be the same page. -->
    <section v-if="app?.files?.length" class="fe-ppage__appfiles" data-testid="public-page-files">
      <div v-for="f in app?.files ?? []" :key="f.ref" class="fe-ppage__file-one fe-ppage__appfile">
        <p class="fe-surface__text">
          <strong>{{ f.name }}</strong>
          <span v-if="f.size" class="fe-ppage__filesize"> · {{ formatSize(f.size) }}</span>
        </p>
        <a
          class="fe-btn"
          :href="link.fileDownloadUrl(f.ref)"
          download
          :title="t('plugin.page.open_file', { name: f.name })"
          :data-testid="`public-page-file-${f.ref}`"
        >
          {{ t('ctx.download') }}
        </a>
      </div>
    </section>
  </template>

  <!-- ── a folder ───────────────────────────────────────────────────── -->
  <template v-else-if="kind === 'folder'">
    <div class="fe-ppage__file-one" data-testid="public-share-folder">
      <!-- Same rule as a document: the heading is the folder's name already. -->
      <p v-if="!node?.name" class="fe-surface__text">
        <strong>{{ info?.subject }}</strong>
      </p>
      <nav v-if="info?.path" class="fe-ppage__crumbs" data-testid="public-share-crumbs">
        <button type="button" class="fe-btn fe-btn--sm" @click="emit('navigate', parent ?? '')">
          {{ t('public.up') }}
        </button>
        <span class="fe-ppage__crumb">{{ info?.path }}</span>
      </nav>

      <!-- ⚠ The server does not send a listing for a folder share yet: it
           answers the folder's NAME and leaves the walking to the no-JS page.
           The listing is drawn the moment one arrives; until then the two
           things a visitor actually came for are here, and the third is one
           link away. Writing a SECOND browse implementation against a
           different endpoint is what v3 exists to stop. -->
      <ul v-if="entries.length" class="fe-ppage__files" data-testid="public-share-entries">
        <li v-for="e in entries" :key="e.path" class="fe-ppage__file" :data-dir="e.is_dir ? '1' : undefined">
          <button
            v-if="e.is_dir"
            type="button"
            class="fe-ppage__entry"
            :data-testid="`public-entry-${e.name}`"
            @click="emit('navigate', e.path)"
          >
            <bdi>{{ e.name }}</bdi>
          </button>
          <a
            v-else
            :href="e.url || link.downloadUrl({ path: e.path })"
            class="fe-ppage__entry"
            :data-testid="`public-entry-${e.name}`"
            download
          ><bdi>{{ e.name }}</bdi></a>
          <span v-if="!e.is_dir && e.size !== undefined" class="fe-ppage__filesize">{{ formatSize(e.size) }}</span>
          <span v-if="e.modified" class="fe-ppage__filesize">{{ formatDate(e.modified) }}</span>
        </li>
      </ul>

      <div class="fe-ppage__actions">
        <a class="fe-btn fe-btn--primary" :href="link.downloadUrl({ zip: true })" data-testid="public-share-zip">
          {{ t('public.download_all') }}
        </a>
        <a v-if="!entries.length" class="fe-btn" :href="link.noJsUrl()" data-testid="public-share-browse">
          {{ t('public.browse') }}
        </a>
      </div>
    </div>
  </template>

  <!-- ── one document ───────────────────────────────────────────────── -->
  <template v-else>
    <div class="fe-ppage__file-one" data-testid="public-share-file">
      <img v-if="isImage" class="fe-ppage__shot" :src="link.downloadUrl()" :alt="node?.name ?? ''" />
      <!-- ⚠ The name is NOT repeated here. The shell's heading is already the
           file's own name (`PublicLinkPage.title`), and printing it again
           under itself — "brand-guidelines.pdf" over "brand-guidelines.pdf ·
           791 B" — is the stutter the levelled-down page shipped with. Only
           the size, and the name as a fallback for a link that has none. -->
      <p class="fe-surface__text fe-surface__text--muted" data-testid="public-share-fileline">
        <strong v-if="!node?.name">{{ info?.subject }}</strong>
        <span v-if="node?.size !== undefined" class="fe-ppage__filesize">{{ formatSize(node?.size) }}</span>
      </p>
      <a class="fe-btn fe-btn--primary" :href="link.downloadUrl()" data-testid="public-share-download">
        {{ t('ctx.download') }}
      </a>
    </div>
  </template>
</template>
