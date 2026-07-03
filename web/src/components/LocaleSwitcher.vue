<script setup lang="ts">
import { Menu, MenuButton, MenuItem, MenuItems } from '@headlessui/vue';
import { Globe } from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';
import { setStoredLocale, SUPPORTED_LOCALES, type Locale } from '@/i18n';

const { locale, t } = useI18n();

const labels: Record<Locale, string> = {
  en: 'English',
  tr: 'Türkçe',
};

function pick(l: Locale) {
  setStoredLocale(l);
}
</script>

<template>
  <Menu as="div" class="relative">
    <MenuButton
      class="inline-flex h-8 items-center gap-1 rounded-md px-2 text-sm text-zinc-600 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800"
      :title="t('nav.language')"
    >
      <Globe class="h-4 w-4" />
      <span class="uppercase text-xs font-medium">{{ locale }}</span>
    </MenuButton>
    <transition
      enter-active-class="transition ease-out duration-100"
      enter-from-class="transform opacity-0 scale-95"
      enter-to-class="transform opacity-100 scale-100"
      leave-active-class="transition ease-in duration-75"
      leave-from-class="transform opacity-100 scale-100"
      leave-to-class="transform opacity-0 scale-95"
    >
      <MenuItems
        class="absolute right-0 mt-1 w-36 origin-top-right rounded-md border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 shadow-lg focus:outline-none overflow-hidden"
      >
        <MenuItem v-for="l in SUPPORTED_LOCALES" :key="l" v-slot="{ active }">
          <button
            type="button"
            :class="[
              'flex w-full items-center justify-between px-3 py-2 text-sm',
              active
                ? 'bg-zinc-100 dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100'
                : 'text-zinc-700 dark:text-zinc-200',
            ]"
            @click="pick(l)"
          >
            <span>{{ labels[l] }}</span>
            <span v-if="locale === l" class="text-brand-600 dark:text-brand-400">●</span>
          </button>
        </MenuItem>
      </MenuItems>
    </transition>
  </Menu>
</template>
