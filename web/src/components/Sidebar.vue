<script setup lang="ts">
import { computed, ref, onMounted, watch, type Component } from 'vue';
import { useRoute, RouterLink } from 'vue-router';
import { trashApi } from '@/api/trash';
import TrashFull from './icons/TrashFull.vue';
import {
  LayoutDashboard,
  Blocks,
  Database,
  Users,
  Settings,
  Brush,
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
  ArrowUpCircle,
  Cable,
  BarChart3,
  Archive,
} from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';
import LogoMark from './LogoMark.vue';
import { useAuthStore } from '@/stores/auth';
import { usePluginHomeApps } from '@/composables/usePluginHomeApps';

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

/**
 * The installed apps that have a screen of their own — the sidebar's "Apps"
 * section. Only an administrator sees it (the panel is admin-only anyway, but
 * the section says so itself rather than relying on the layout above it), and
 * it is absent entirely when no running plugin ships a `home` view: a heading
 * over nothing is a promise the deployment cannot keep.
 *
 * ⚠ Nothing here names a plugin. The signing app's request table reaches the
 * panel because it is a `home` view, exactly like the next plugin's will.
 */
const auth = useAuthStore();
const { apps: homeApps } = usePluginHomeApps();
const appItems = computed<NavItem[]>(() =>
  auth.isAdmin
    ? homeApps.value.map((a) => ({
        to: { name: 'admin-app', params: { plugin: a.plugin, view: a.view } },
        label: a.label,
        svg: a.svg,
        group: 'apps' as const,
        id: `app-${a.key}`,
      }))
    : [],
);

interface NavItem {
  to: { name: string; params?: Record<string, string> };
  label: string;
  /** A panel page's icon: a Lucide component. */
  icon?: Component;
  /**
   * An app row's icon instead: inline SVG from the core icon library, chosen
   * by the manifest's icon NAME. ⚠ Static markup this repository wrote —
   * never markup a plugin supplied — which is what makes `v-html` safe here.
   */
  svg?: string;
  group: 'main' | 'apps' | 'access' | 'ops' | 'meta';
  /** A stable key + test hook; defaults to the route name for a fixed page. */
  id?: string;
}

const items = computed<NavItem[]>(() => [
  { to: { name: 'dashboard' }, label: t('nav.dashboard'), icon: LayoutDashboard, group: 'main' },
  { to: { name: 'explore' }, label: t('nav.files'), icon: FolderOpen, group: 'main' },
  { to: { name: 'admin-files' }, label: t('nav.adminFiles'), icon: History, group: 'main' },
  { to: { name: 'connections' }, label: t('nav.connections'), icon: Cable, group: 'main' },
  { to: { name: 'storages' }, label: t('nav.storages'), icon: Database, group: 'main' },
  { to: { name: 'sync' }, label: t('nav.sync'), icon: RefreshCcw, group: 'main' },
  { to: { name: 'shares' }, label: t('nav.shares'), icon: Share2, group: 'main' },
  { to: { name: 'trash' }, label: t('nav.trash'), icon: trashCount.value > 0 ? TrashFull : Trash2, group: 'main' },
  { to: { name: 'search' }, label: t('nav.search'), icon: Search, group: 'main' },
  { to: { name: 'duplicates' }, label: t('nav.duplicates'), icon: CopyIcon, group: 'main' } /* bul:s3 */,
  { to: { name: 'tagged' }, label: t('nav.tagged'), icon: Tag, group: 'main' },

  // An app is a PLACE you go, like a drive — not a setting. The explorer's
  // own navigation panel puts its "Apps" rows in the same spot, right after
  // the places, and the two sidebars should not disagree about that.
  ...appItems.value,

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
  { to: { name: 'appearance' }, label: t('nav.appearance'), icon: Brush, group: 'ops' } /* tema:v1 */,
  { to: { name: 'protection' }, label: t('nav.protection'), icon: Shield, group: 'ops' } /* koru:k3 */,
  { to: { name: 'archives' }, label: t('nav.archives'), icon: Archive, group: 'ops' },
  { to: { name: 'external' }, label: t('nav.external'), icon: PlugZap, group: 'ops' },
  { to: { name: 'replica' }, label: t('nav.replica'), icon: GitBranch, group: 'ops' },
  { to: { name: 'queue' }, label: t('nav.queue'), icon: ListChecks, group: 'ops' },
  { to: { name: 'notifications' }, label: t('nav.notifications'), icon: Bell, group: 'ops' },
  { to: { name: 'webhooks' }, label: t('nav.webhooks'), icon: Webhook, group: 'ops' } /* bag:b3 */,
  { to: { name: 'plugins' }, label: t('nav.plugins'), icon: Blocks, group: 'ops' },
  { to: { name: 'usage' }, label: t('nav.usage'), icon: BarChart3, group: 'ops' },
  { to: { name: 'audit' }, label: t('nav.audit'), icon: ScrollText, group: 'ops' },
  { to: { name: 'updates' }, label: t('nav.updates'), icon: ArrowUpCircle, group: 'ops' },

  { to: { name: 'about' }, label: t('nav.about'), icon: Info, group: 'meta' },
]);

/**
 * ⚠ Built from the ORDER in `items`, not from a fixed list of group names:
 * an empty group renders nothing at all (`v-for` over no entries and a
 * heading that is only drawn when the group has rows), which is what keeps
 * the Apps section from appearing as an empty heading on an installation
 * with no plugins.
 */
const groups = computed(() => {
  const map: Record<NavItem['group'], NavItem[]> = {
    main: [],
    apps: [],
    access: [],
    ops: [],
    meta: [],
  };
  for (const it of items.value) map[it.group].push(it);
  return map;
});

/** A group's heading, or '' for the ones that have never had one. */
function groupLabel(group: string): string {
  return group === 'apps' ? t('nav.apps') : '';
}

function isActive(item: NavItem): boolean {
  // Match self + child routes that declare `meta.parent`.
  if (route.name === item.to.name) {
    // ⚠ Several app rows share ONE route name and differ only in their
    // params; comparing the name alone would light up every app in the list
    // whenever any one of them is open.
    const params = item.to.params;
    if (!params) return true;
    return Object.entries(params).every(([k, v]) => String(route.params[k] ?? '') === v);
  }
  if (route.meta?.parent && route.meta.parent === item.to.name) return true;
  return false;
}
</script>

<template>
  <aside
    :class="[
      'fixed inset-y-0 start-0 z-40 w-64 transform bg-white dark:bg-zinc-900 border-e border-zinc-200 dark:border-zinc-800 transition-transform lg:translate-x-0',
      // ⚠ RTL: closed = pushed off the START edge, which is the right one there.
      open ? 'translate-x-0' : '-translate-x-full rtl:translate-x-full',
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
        <!-- ⚠ The `v-if` sits on the INNER div, not beside the `v-for`: in
             Vue 3 `v-if` wins that race and `list` would not exist yet. An
             empty group must vanish completely — a heading over nothing is a
             promise the deployment cannot keep. -->
        <template
          v-for="(list, group) in groups"
          :key="group"
        >
          <div
            v-if="list.length"
            :data-testid="`nav-group-${group}`"
          >
            <p
              v-if="groupLabel(group)"
              class="nav-heading"
            >
              {{ groupLabel(group) }}
            </p>
            <ul class="space-y-0.5">
              <li
                v-for="item in list"
                :key="item.id ?? item.to.name"
              >
                <RouterLink
                  :to="item.to"
                  :class="['nav-link', isActive(item) && 'nav-link-active']"
                  :data-testid="`nav-${item.id ?? item.to.name}`"
                  @click="emit('close')"
                >
                  <!-- eslint-disable-next-line vue/no-v-html, vue/max-attributes-per-line -- static markup from lib/actionIcons -->
                  <span v-if="item.svg" class="nav-appicon" aria-hidden="true" v-html="item.svg"></span>
                  <component
                    :is="item.icon"
                    v-else
                    class="h-4 w-4"
                  />
                  <span class="truncate">{{ item.label }}</span>
                </RouterLink>
              </li>
            </ul>
            <div
              v-if="group !== 'meta'"
              class="my-3 border-t border-zinc-200/70 dark:border-zinc-800/70"
            />
          </div>
        </template>
      </nav>
    </div>
  </aside>
</template>
