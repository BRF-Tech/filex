<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { ExternalLink, FileText, Github } from 'lucide-vue-next';

import { shortCommit } from '@brftech/filex-core';

import { useCapabilitiesStore } from '@/stores/capabilities';
import LogoMark from '@/components/LogoMark.vue';
import Badge from '@/components/ui/Badge.vue';
import CopyButton from '@/components/ui/CopyButton.vue';
import { driverName } from '@/lib/storageWords';

const { t, te } = useI18n();
const caps = useCapabilitiesStore();

const data = computed(() => caps.data);

/* The server says the release, the commit and the build time apart
   (`release`, `commit`, `built`; 0.54, #211 audit A11 - the page parsed
   `v0.46.0 (<40-digit commit>, <build time>)` itself before). The release is
   the headline; the commit, shortened as git shows it, and the build day go
   on the quiet line below. The copy button still copies the whole `version`
   string, which is what a bug report wants (the maintainer, 2026-09-26: it
   ran off the card). */
const release = computed(() => data.value.release ?? '');
const buildLine = computed(
  () => [shortCommit(data.value.commit ?? ''), (data.value.built ?? '').slice(0, 10)].filter(Boolean).join(' · ') || data.value.build,
);

interface ToolEntry {
  /** The row's id for tests and the badge; the name when it is a product's. */
  id: string;
  name: string;
  available: boolean;
}

/* ⚠ Programs are named the way their makers write them ("ImageMagick", not
 * "imagemagick"), and each row reads name first, then its state — the
 * badge used to stand BEFORE the name, so "— imagemagick Tamam ffmpeg" read
 * as "imagemagick: Tamam" (release-candidate sweep, 2026-09-21, QA #9). The
 * availability itself is the server's one answer (enginebin), shared with
 * Apps and the converter. */
const thumbnailTools = computed<ToolEntry[]>(() => [
  { id: 'ImageMagick', name: 'ImageMagick', available: data.value.imagemagick },
  /* ⚠ A row of its own, and not the ImageMagick row's answer: libheif can be
   * there without its HEVC decoder (Ubuntu 24.04), and then ImageMagick is
   * "Found" while no phone photo can be drawn. The server decodes a sample to
   * find out (enginebin.HEIC, 0.50 test phase). */
  { id: 'HEIC', name: t('about.heic'), available: data.value.heic === true },
  { id: 'FFmpeg', name: 'FFmpeg', available: data.value.ffmpeg },
  { id: 'Ghostscript', name: 'Ghostscript', available: data.value.ghostscript },
  /* 0.50: office documents are drawn by the OnlyOffice document server and
   * nothing else (LibreOffice left the image); the row says whether it is
   * configured. */
  { id: 'Office', name: t('about.office'), available: data.value.thumbs?.office === true },
]);

/** ImageMagick is there and cannot decode HEIC: say what to install. */
const heicDecoderMissing = computed(() => data.value.imagemagick && data.value.heic === false);

/** No office thumbnails: say where OnlyOffice is configured. */
const officeMissing = computed(() => data.value.thumbs?.office !== true);

/** A database engine by its product name — the page capitalised the id
 *  ("Sqlite"). */
const DB_NAMES: Record<string, string> = { sqlite: 'SQLite', postgres: 'PostgreSQL', mysql: 'MySQL' };
const dbName = computed(() => DB_NAMES[data.value.db_driver] ?? data.value.db_driver);

/** A sign-in method by name, as the Identity providers page names it. */
function authName(d: string): string {
  const k = `authProviders.providers.${d}`;
  return te(k) ? t(k) : d;
}
</script>

<template>
  <div class="space-y-5 max-w-3xl">
    <header class="flex items-center gap-4">
      <LogoMark class="h-12 w-12" />
      <div>
        <h1 class="text-xl font-semibold">{{ t('about.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('about.subtitle') }}</p>
      </div>
    </header>

    <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
      <div class="card card-body">
        <p class="text-xs uppercase tracking-wide text-zinc-500">{{ t('about.version') }}</p>
        <p class="mt-1 flex items-center gap-2">
          <span class="text-lg font-semibold tabular-nums" data-testid="about-version">{{ release }}</span>
          <CopyButton :value="data.version" size="xs" />
        </p>
        <p v-if="buildLine" class="mt-1 text-xs font-mono text-zinc-500" :title="data.version" data-testid="about-build">{{ buildLine }}</p>
      </div>

      <div class="card card-body">
        <p class="text-xs uppercase tracking-wide text-zinc-500">{{ t('about.db') }}</p>
        <p class="mt-1 text-lg font-semibold" data-testid="about-db">{{ dbName }}</p>
        <p class="mt-1 flex items-center gap-1.5 text-xs text-zinc-500">
          <span>{{ t('about.search') }}</span>
          <Badge :tone="data.search_enabled ? 'emerald' : 'zinc'" size="xs">
            {{ data.search_enabled ? t('common.enabled') : t('common.disabled') }}
          </Badge>
        </p>
      </div>

      <div class="card card-body">
        <p class="text-xs uppercase tracking-wide text-zinc-500">{{ t('about.storage') }}</p>
        <div class="mt-2 flex flex-wrap gap-1.5">
          <Badge
            v-for="d in data.storage_drivers"
            :key="d"
            tone="brand"
            size="xs"
            :title="d"
          >
            {{ driverName(d, t, te) }}
          </Badge>
          <span v-if="!data.storage_drivers.length" class="text-xs text-zinc-500">-</span>
        </div>
      </div>

      <div class="card card-body">
        <p class="text-xs uppercase tracking-wide text-zinc-500">{{ t('about.auth') }}</p>
        <div class="mt-2 flex flex-wrap gap-1.5">
          <Badge
            v-for="d in data.auth_drivers"
            :key="d"
            tone="violet"
            size="xs"
            :title="d"
          >
            {{ authName(d) }}
          </Badge>
          <span v-if="!data.auth_drivers.length" class="text-xs text-zinc-500">-</span>
        </div>
      </div>
    </div>

    <div class="card card-body">
      <p class="text-xs uppercase tracking-wide text-zinc-500 mb-2">
        {{ t('about.thumbnails') }}
      </p>
      <!-- As many columns as fit a name and its badge on one line: four fixed
           columns broke "HEIC fotoğrafları" + "Bulunamadı" onto two lines at
           1440 px (0.50 test phase), and a row taller than its neighbours
           threw the grid out of line. -->
      <ul class="grid grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] gap-x-4 gap-y-2" data-testid="about-tools">
        <li
          v-for="t2 in thumbnailTools"
          :key="t2.id"
          class="flex items-center gap-2 text-sm"
        >
          <span class="whitespace-nowrap">{{ t2.name }}</span>
          <Badge :tone="t2.available ? 'emerald' : 'zinc'" dot size="xs" :data-testid="`about-tool-${t2.id}`">
            {{ t2.available ? t('about.toolFound') : t('about.toolMissing') }}
          </Badge>
        </li>
      </ul>
      <p v-if="heicDecoderMissing" class="mt-2 text-xs text-zinc-500" data-testid="about-heic-hint">
        {{ t('about.heicHint') }}
      </p>
      <p v-if="officeMissing" class="mt-2 text-xs text-zinc-500" data-testid="about-office-hint">
        {{ t('about.officeHint') }}
      </p>
    </div>

    <div class="card card-body">
      <p class="text-xs uppercase tracking-wide text-zinc-500 mb-2">{{ t('about.links') }}</p>
      <ul class="space-y-2 text-sm">
        <li>
          <a
            href="https://github.com/BRF-Tech/filex"
            target="_blank"
            rel="noopener"
            class="inline-flex items-center gap-2 text-brand-600 dark:text-brand-400 hover:underline"
          >
            <Github class="h-4 w-4" />
            {{ t('about.source') }}
            <ExternalLink class="h-3 w-3 opacity-60" />
          </a>
        </li>
        <li>
          <a
            href="https://docs.filex.sh"
            target="_blank"
            rel="noopener"
            class="inline-flex items-center gap-2 text-brand-600 dark:text-brand-400 hover:underline"
          >
            <FileText class="h-4 w-4" />
            {{ t('about.documentation') }}
            <ExternalLink class="h-3 w-3 opacity-60" />
          </a>
        </li>
      </ul>
      <p class="mt-3 text-xs text-zinc-500">{{ t('about.licenseIs', { license: 'MIT' }) }}</p>
    </div>
  </div>
</template>
