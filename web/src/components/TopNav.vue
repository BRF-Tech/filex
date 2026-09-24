<script setup lang="ts">
import { defineAsyncComponent, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import {
  Menu as MenuIcon,
  LogOut,
  Search,
  ChevronDown,
  SlidersHorizontal,
} from 'lucide-vue-next';
import { Menu, MenuButton, MenuItem, MenuItems } from '@headlessui/vue';
import { useI18n } from 'vue-i18n';
import { ProductVersion, localeTag, personInitial, personName, productVersionLine } from '@brftech/filex-core';

import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { signOut } from '@/lib/signOut';
// gorunum:v3-shell — ⚠ no LocaleSwitcher and no DarkModeToggle here any more.
// Both did a job the user-settings modal already does, one click away in the
// account menu below (Preferences → Language, Preferences → Theme), and the
// owner's rule for this wave is that no two controls may do the same job —
// "aynı işlevi yapan iki buton olmaması lazım, context menü butonları
// dışında". The explorer's own header cluster lost its copies in the same
// pass; this was the last pair.
import NotificationBell from './NotificationBell.vue';
import QuotaWidget from './QuotaWidget.vue';

// Async so the modal's markup, its strings and core's stylesheet stay out of
// the panel's first paint — nothing here is needed until somebody opens it.
const UserSettingsModal = defineAsyncComponent(() => import('./UserSettingsModal.vue'));

const showSettings = ref(false);

const emit = defineEmits<{ (e: 'toggleSidebar'): void }>();

const route = useRoute();
const router = useRouter();

/**
 * `?settings=1` opens the dialog — the deep link that replaced the retired
 * /admin/profile page.
 *
 * ⚠ It has to exist, and it has to be on a route rather than a button: the
 * server prints where to change the first-run password into the startup
 * banner AND into `<data>/.first-run.txt`, a file that is already sitting on
 * installs in the field saying `/admin/profile`. A dialog reachable only by
 * clicking an avatar cannot be named in either place. The parameter is
 * stripped the moment it is honoured, so a reload or a shared URL does not
 * reopen it.
 */
watch(
  () => route.query.settings,
  (v) => {
    if (v === undefined || v === null) return;
    showSettings.value = true;
    const { settings: _drop, ...rest } = route.query;
    router.replace({ path: route.path, query: rest, hash: route.hash });
  },
  { immediate: true },
);
const auth = useAuthStore();
const caps = useCapabilitiesStore();
const { t, locale } = useI18n();

async function logout() {
  await signOut(auth, router);
}

function gotoSearch() {
  router.push({ name: 'search' });
}
</script>

<template>
  <header
    class="sticky top-0 z-20 flex h-14 items-center gap-3 border-b border-zinc-200 dark:border-zinc-800 bg-white/80 dark:bg-zinc-900/80 backdrop-blur px-4 sm:px-6 lg:px-8"
  >
    <button
      type="button"
      class="lg:hidden rounded p-1.5 text-zinc-700 dark:text-zinc-200 hover:bg-zinc-100 dark:hover:bg-zinc-800"
      @click="emit('toggleSidebar')"
      :aria-label="$t('nav.dashboard')"
    >
      <MenuIcon class="h-5 w-5" />
    </button>

    <button
      type="button"
      class="hidden md:inline-flex items-center gap-2 rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 px-3 py-1.5 text-sm text-zinc-500 dark:text-zinc-400 hover:bg-zinc-100 dark:hover:bg-zinc-700 transition-colors"
      @click="gotoSearch"
    >
      <Search class="h-4 w-4" />
      <span>{{ t('search.queryPlaceholder') }}</span>
    </button>

    <div class="ms-auto flex items-center gap-1.5">
      <QuotaWidget />
      <NotificationBell />

      <Menu as="div" class="relative">
        <!-- ⚠ Named, because the only IN-PAGE way to the person's own
             settings is through this menu: `?settings=1` is a deep link and
             opening it is a full page load, which a test measuring a page
             whose server went away cannot do (e2e/132). -->
        <MenuButton
          data-testid="account-menu"
          class="inline-flex items-center gap-2 rounded-md px-2 py-1.5 text-sm text-zinc-700 dark:text-zinc-200 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors"
        >
          <span
            class="flex h-7 w-7 items-center justify-center rounded-full bg-brand-100 text-brand-700 text-xs font-semibold dark:bg-brand-500/20 dark:text-brand-300"
          >
            {{ personInitial(auth.user, localeTag(locale)) || '?' }}
          </span>
          <span class="hidden sm:inline truncate max-w-[12rem]">
            {{ personName(auth.user) || '—' }}
          </span>
          <ChevronDown class="h-4 w-4 opacity-60" />
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
            class="absolute end-0 mt-1 w-56 origin-top-right rtl:origin-top-left rounded-md bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 shadow-lg focus:outline-none overflow-hidden"
          >
            <div class="px-3 py-2 text-xs text-zinc-500 dark:text-zinc-400 truncate">
              {{ auth.user?.email }}
            </div>
            <div class="divider" />
            <!-- The person's own settings, in one place — and the ONLY
                 place. /admin/profile used to sit under this with the same
                 fields; two screens editing one account is how one of them
                 goes stale, so the page was retired and this is what the
                 account menu offers. -->
            <MenuItem v-slot="{ active }">
              <button
                type="button"
                :class="[
                  'flex w-full items-center gap-2 px-3 py-2 text-sm',
                  active
                    ? 'bg-zinc-100 dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100'
                    : 'text-zinc-700 dark:text-zinc-200',
                ]"
                data-testid="account-user-settings"
                @click="showSettings = true"
              >
                <SlidersHorizontal class="h-4 w-4" />
                {{ t('userSettings.open') }}
              </button>
            </MenuItem>
            <div class="divider" />
            <MenuItem v-slot="{ active }">
              <button
                type="button"
                :class="[
                  'flex w-full items-center gap-2 px-3 py-2 text-sm',
                  active
                    ? 'bg-zinc-100 dark:bg-zinc-800 text-rose-600 dark:text-rose-400'
                    : 'text-rose-600 dark:text-rose-400',
                ]"
                @click="logout"
              >
                <LogOut class="h-4 w-4" />
                {{ t('nav.logout') }}
              </button>
            </MenuItem>
            <!-- Which filex this is, at the foot of the menu: not a row,
                 nothing to press — the line somebody reads out when asked
                 "which version are you on?" (Burak, 2026-09-24). The same
                 piece the explorer's avatar menu and user settings draw. -->
            <template v-if="productVersionLine(caps.data.version)">
              <div class="divider" />
              <ProductVersion :version="caps.data.version" class="px-3 py-2" />
            </template>
          </MenuItems>
        </transition>
      </Menu>
    </div>

    <UserSettingsModal v-if="showSettings" v-model="showSettings" />
  </header>
</template>
