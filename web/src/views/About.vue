<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { ExternalLink, FileText, Github } from 'lucide-vue-next';

import { useCapabilitiesStore } from '@/stores/capabilities';
import LogoMark from '@/components/LogoMark.vue';
import Badge from '@/components/ui/Badge.vue';
import CopyButton from '@/components/ui/CopyButton.vue';
import { driverName } from '@/lib/storageWords';

const { t, te } = useI18n();
const caps = useCapabilitiesStore();

const data = computed(() => caps.data);

interface ToolEntry {
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
  { name: 'ImageMagick', available: data.value.imagemagick },
  { name: 'FFmpeg', available: data.value.ffmpeg },
  { name: 'Ghostscript', available: data.value.ghostscript },
  { name: 'LibreOffice', available: data.value.libreoffice },
]);

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
          <span class="text-lg font-semibold tabular-nums">{{ data.version }}</span>
          <CopyButton :value="data.version" size="xs" />
        </p>
        <p class="mt-1 text-xs font-mono text-zinc-500">{{ data.build }}</p>
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
          <span v-if="!data.storage_drivers.length" class="text-xs text-zinc-500">—</span>
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
          <span v-if="!data.auth_drivers.length" class="text-xs text-zinc-500">—</span>
        </div>
      </div>
    </div>

    <div class="card card-body">
      <p class="text-xs uppercase tracking-wide text-zinc-500 mb-2">
        {{ t('about.thumbnails') }}
      </p>
      <ul class="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <li
          v-for="t2 in thumbnailTools"
          :key="t2.name"
          class="flex items-center gap-2 text-sm"
        >
          <span>{{ t2.name }}</span>
          <Badge :tone="t2.available ? 'emerald' : 'zinc'" dot size="xs" :data-testid="`about-tool-${t2.name}`">
            {{ t2.available ? t('about.toolFound') : t('about.toolMissing') }}
          </Badge>
        </li>
      </ul>
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
