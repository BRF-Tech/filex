<script setup lang="ts">
import { ref } from 'vue';
import { Menu, MenuButton, MenuItem, MenuItems } from '@headlessui/vue';
import { Moon, Sun, MonitorSmartphone } from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';
import { getStoredTheme, setStoredTheme, type ThemeMode } from '@/lib/theme';

const { t } = useI18n();
const current = ref<ThemeMode>(getStoredTheme());

function pick(mode: ThemeMode) {
  setStoredTheme(mode);
  current.value = mode;
}

const items: { mode: ThemeMode; label: string; icon: typeof Sun }[] = [
  { mode: 'auto', label: t('nav.themeAuto'), icon: MonitorSmartphone },
  { mode: 'light', label: t('nav.themeLight'), icon: Sun },
  { mode: 'dark', label: t('nav.themeDark'), icon: Moon },
];
</script>

<template>
  <Menu as="div" class="relative">
    <MenuButton
      class="inline-flex h-8 w-8 items-center justify-center rounded-md text-zinc-600 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800"
      :title="t('nav.theme')"
    >
      <Sun v-if="current === 'light'" class="h-4 w-4" />
      <Moon v-else-if="current === 'dark'" class="h-4 w-4" />
      <MonitorSmartphone v-else class="h-4 w-4" />
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
        <MenuItem v-for="it in items" :key="it.mode" v-slot="{ active }">
          <button
            type="button"
            :class="[
              'flex w-full items-center gap-2 px-3 py-2 text-sm',
              active
                ? 'bg-zinc-100 dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100'
                : 'text-zinc-700 dark:text-zinc-200',
            ]"
            @click="pick(it.mode)"
          >
            <component :is="it.icon" class="h-4 w-4" />
            <span class="flex-1 text-left">{{ it.label }}</span>
            <span v-if="current === it.mode" class="text-brand-600 dark:text-brand-400">●</span>
          </button>
        </MenuItem>
      </MenuItems>
    </transition>
  </Menu>
</template>
