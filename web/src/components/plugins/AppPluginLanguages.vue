<script setup lang="ts">
/**
 * AppPluginLanguages — the languages an app adds to filex itself, and how
 * much of the interface each one covers.
 *
 * A language pack is judged by one number: what share of the CURRENT
 * catalogue it translates. The server measures it (wasmplugin LanguageRows —
 * against the catalogue this very binary's interface draws from, so a pack
 * written for an older filex says how much of THIS one it covers) and this
 * component only says it, in the reader's language:
 *
 *   Español — 97% translated · the rest shows in English
 *   العربية — 82% translated · the rest shows in English
 *
 * ⚠ `total: 0` means the binary carries no catalogue (a development build);
 * the row then says how many strings the pack has rather than inventing a
 * percentage.
 *
 * ⚠ The same component on every Apps surface that shows a pack — the list
 * row and the install review today — so "what share is translated" is worded
 * once. (The app detail drawer is being reworked separately; it takes the
 * same `languages` rows from the same answer.)
 */
import { useI18n } from 'vue-i18n';
import { localeLabel } from '@brftech/filex-core';
import type { AppPluginLanguage } from '@/api/appPlugins';

defineProps<{
  languages: AppPluginLanguage[];
}>();

const { t } = useI18n();
</script>

<template>
  <ul class="space-y-1 text-xs" data-testid="app-plugin-languages">
    <li v-for="l in languages" :key="l.code" :data-testid="`app-plugin-language-${l.code}`">
      <span class="font-medium text-zinc-800 dark:text-zinc-100" :lang="l.code">{{ localeLabel(l.code) }}</span>
      <span class="font-mono text-zinc-500"> ({{ l.code }})</span>
      —
      <template v-if="l.total > 0">
        <span :data-testid="`app-plugin-language-${l.code}-coverage`">{{ t('appPlugins.lang.coverage', { percent: l.percent }) }}</span>
        <span v-if="l.percent < 100" class="text-zinc-500"> · {{ t('appPlugins.lang.fallback') }}</span>
        <span v-if="l.unknown > 0" class="text-zinc-500"> · {{ t('appPlugins.lang.unknown', { count: l.unknown }, l.unknown) }}</span>
      </template>
      <span v-else>{{ t('appPlugins.lang.strings', { count: l.keys }, l.keys) }}</span>
    </li>
  </ul>
</template>
