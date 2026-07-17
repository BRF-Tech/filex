<script setup lang="ts">
import { computed, ref, onMounted, watch, type Component } from 'vue';
import { useRoute, RouterLink } from 'vue-router';
import { trashApi } from '@/api/trash';
import TrashFull from './icons/TrashFull.vue';
import {
  LayoutDashboard,
  Database,
  Users,
  Settings,
  PlugZap,
  ShieldCheck,
  ScrollText,
  RefreshCcw,
  Share2,
  Search,
  Tag,
  Info,
  Trash2,
  X,
  ListChecks,
  Bell,
  Shield /* koru:k3 */,
  Webhook,
  GitBranch,
  FolderOpen,
  History,
  KeyRound,
  Copy as CopyIcon /* bul:s3 */,
  Palette /* wiring:e1 */,
} from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';
import LogoMark from './LogoMark.vue';

interface Props {
  open: boolean;
}

defineProps<Props>();
const emit = defineEmits<{ (e: 'close'): void }>();

const { t } = useI18n();
const route = useRoute();

// Trash icon reflects whether the trash has anything in it: a full bin when
// there are items, the empty bin otherwise. Refreshed on mount + on navigation
// (e.g. after emptying/restoring). Best-effort — falls back to the empty bin.
const trashCount = ref(0);
async function refreshTrash(): Promise<void> {
  try {
    trashCount.value = (await trashApi.list({ limit: 1 })).total;
  } catch {
    /* keep the empty-bin icon */
  }
}
onMounted(refreshTrash);
watch(() => route.name, refreshTrash);

interface NavItem {
  to: { name: string };
  label: string;
  icon: Component;
  group: 'main' | 'access' | 'ops' | 'meta';
}

const items = computed<NavItem[]>(() => [
  { to: { name: 'dashboard' }, label: t('nav.dashboard'), icon: LayoutDashboard, group: 'main' },
  { to: { name: 'explore' }, label: t('nav.files'), icon: FolderOpen, group: 'main' },
  { to: { name: 'admin-files' }, label: t('nav.adminFiles'), icon: History, group: 'main' },
  { to: { name: 'storages' }, label: t('nav.storages'), icon: Database, group: 'main' },
  { to: { name: 'sync' }, label: t('nav.sync'), icon: RefreshCcw, group: 'main' },
  { to: { name: 'shares' }, label: t('nav.shares'), icon: Share2, group: 'main' },
  { to: { name: 'trash' }, label: t('nav.trash'), icon: trashCount.value > 0 ? TrashFull : Trash2, group: 'main' },
  { to: { name: 'search' }, label: t('nav.search'), icon: Search, group: 'main' },
  { to: { name: 'duplicates' }, label: t('nav.duplicates'), icon: CopyIcon, group: 'main' } /* bul:s3 */,
  { to: { name: 'tagged' }, label: t('nav.tagged'), icon: Tag, group: 'main' },

  { to: { name: 'users' }, label: t('nav.users'), icon: Users, group: 'access' },
  { to: { name: 'grants' }, label: t('nav.grants'), icon: ShieldCheck, group: 'access' },
  {
    to: { name: 'auth-providers' },
    label: t('nav.authProviders'),
    icon: ShieldCheck,
    group: 'access',
  },
  { to: { name: 'api-mcp' }, label: t('nav.apiMcp'), icon: KeyRound, group: 'access' },

  { to: { name: 'settings' }, label: t('nav.settings'), icon: Settings, group: 'ops' },
  { to: { name: 'branding' }, label: t('nav.branding'), icon: Palette, group: 'ops' } /* wiring:e1 */,
  { to: { name: 'protection' }, label: t('nav.protection'), icon: Shield, group: 'ops' } /* koru:k3 */,
  { to: { name: 'external' }, label: t('nav.external'), icon: PlugZap, group: 'ops' },
  { to: { name: 'replica' }, label: t('nav.replica'), icon: GitBranch, group: 'ops' },
  { to: { name: 'queue' }, label: t('nav.queue'), icon: ListChecks, group: 'ops' },
  { to: { name: 'notifications' }, label: t('nav.notifications'), icon: Bell, group: 'ops' },
  { to: { name: 'webhooks' }, label: t('nav.webhooks'), icon: Webhook, group: 'ops' } /* bag:b3 */,
  { to: { name: 'audit' }, label: t('nav.audit'), icon: ScrollText, group: 'ops' },

  { to: { name: 'about' }, label: t('nav.about'), icon: Info, group: 'meta' },
]);

const groups = computed(() => {
  const map: Record<NavItem['group'], NavItem[]> = { main: [], access: [], ops: [], meta: [] };
  for (const it of items.value) map[it.group].push(it);
  return map;
});

function isActive(name: string): boolean {
  // Match self + child routes that declare `meta.parent`.
  if (route.name === name) return true;
  if (route.meta?.parent && route.meta.parent === name) return true;
  return false;
}
</script>

<template>
  <aside
    :class="[
      'fixed inset-y-0 left-0 z-40 w-64 transform bg-white dark:bg-zinc-900 border-r border-zinc-200 dark:border-zinc-800 transition-transform lg:translate-x-0',
      open ? 'translate-x-0' : '-translate-x-full',
    ]"
  >
    <div class="flex h-full flex-col">
      <div
        class="flex items-center justify-between gap-2 border-b border-zinc-200 dark:border-zinc-800 px-4 h-14"
      >
        <RouterLink :to="{ name: 'dashboard' }" class="flex items-center gap-2">
          <LogoMark class="h-7 w-7" />
          <div class="flex flex-col leading-tight">
            <span class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">filex</span>
            <span class="text-[10px] text-zinc-500 dark:text-zinc-400 uppercase tracking-wide">
              {{ $t('app.admin') }}
            </span>
          </div>
        </RouterLink>
        <button
          type="button"
          class="lg:hidden rounded p-1 text-zinc-500 hover:bg-zinc-100 dark:hover:bg-zinc-800"
          @click="emit('close')"
          :aria-label="$t('common.close')"
        >
          <X class="h-5 w-5" />
        </button>
      </div>

      <nav class="flex-1 overflow-y-auto px-2 py-3 space-y-4">
        <div v-for="(list, group) in groups" :key="group">
          <ul class="space-y-0.5">
            <li v-for="item in list" :key="item.to.name">
              <RouterLink
                :to="item.to"
                :class="['nav-link', isActive(item.to.name) && 'nav-link-active']"
                @click="emit('close')"
              >
                <component :is="item.icon" class="h-4 w-4" />
                <span class="truncate">{{ item.label }}</span>
              </RouterLink>
            </li>
          </ul>
          <div
            v-if="group !== 'meta'"
            class="my-3 border-t border-zinc-200/70 dark:border-zinc-800/70"
          />
        </div>
      </nav>
    </div>
  </aside>
</template>
