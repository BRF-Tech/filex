<script setup lang="ts">
/**
 * Groups (admin.users). A group is a named set of people in one tenant: it can
 * be given folder access in the explorer's sharing panel, like a person, and a
 * custom role — the role of every member who has none of their own. People
 * are added here by hand, or by linking the group to an SSO group their
 * sign-in carries. Each group is edited on its own page.
 */
import { computed, onMounted, ref } from 'vue';
import { RouterLink, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { UsersRound, Plus, ChevronRight } from 'lucide-vue-next';

import { GroupsApi, type Group } from '@/api/groups';
import { RolesApi, type PermissionRule } from '@/api/roles';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
const toast = useToastStore();
const router = useRouter();

const groups = ref<Group[]>([]);
const roles = ref<PermissionRule[]>([]);
const loading = ref(true);
const q = ref('');

async function load() {
  loading.value = true;
  try {
    const [gs, rs] = await Promise.all([
      GroupsApi.list(),
      // Best effort: without it a group's role shows as its number.
      RolesApi.listRules().catch(() => ({ rules: [] as PermissionRule[] })),
    ]);
    groups.value = gs;
    roles.value = rs.rules;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

const shown = computed(() => {
  const term = q.value.trim().toLocaleLowerCase();
  const list = !term
    ? groups.value
    : groups.value.filter(
        (g) =>
          g.name.toLocaleLowerCase().includes(term) ||
          g.description.toLocaleLowerCase().includes(term) ||
          g.links.some((l) => l.value.toLocaleLowerCase().includes(term)),
      );
  return [...list].sort((a, b) => a.name.localeCompare(b.name));
});

function roleName(g: Group): string {
  if (g.role_id == null) return '';
  return roles.value.find((r) => r.id === g.role_id)?.name ?? `#${g.role_id}`;
}
function ssoLinks(g: Group): string {
  return g.links
    .filter((l) => l.kind === 'sso')
    .map((l) => l.value)
    .join(', ');
}

// ── new group: a name, then its page ──
const creating = ref(false);
const newName = ref('');
const saving = ref(false);
function openNew() {
  newName.value = '';
  creating.value = true;
}
async function create() {
  if (!newName.value.trim()) return;
  saving.value = true;
  try {
    const d = await GroupsApi.create({ name: newName.value.trim(), description: '', role_id: null, links: [] });
    creating.value = false;
    router.push({ name: 'groups.edit', params: { id: d.group.id } });
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="space-y-5 max-w-4xl">
    <div class="flex items-start justify-between gap-3 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <UsersRound class="h-5 w-5" /> {{ t('groups.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-2xl">{{ t('groups.subtitle') }}</p>
      </div>
      <Button data-testid="group-new" @click="openNew"><Plus class="h-4 w-4" /> {{ t('groups.add') }}</Button>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <template v-else>
      <Input v-if="groups.length > 5" v-model="q" type="search" :placeholder="t('groups.search')" size="sm" class="w-64 max-w-full" />
      <ul v-if="shown.length" class="rule-list rounded-lg card" data-testid="groups-list">
        <li v-for="g in shown" :key="g.id">
          <RouterLink
            :to="{ name: 'groups.edit', params: { id: g.id } }"
            class="flex items-center justify-between gap-3 px-4 py-3 hover:bg-[var(--fe-bg-hover)]"
            :data-testid="`group-${g.id}`"
          >
            <div class="min-w-0 space-y-0.5">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-medium">{{ g.name }}</span>
                <Badge v-if="roleName(g)" tone="brand">{{ roleName(g) }}</Badge>
                <span v-if="roleName(g) && g.priority" class="text-xs text-zinc-500">{{ t('groups.prioritySummary', { n: g.priority }) }}</span>
              </div>
              <div v-if="g.description" class="text-xs text-zinc-600 dark:text-zinc-300 truncate">{{ g.description }}</div>
              <div class="text-xs text-zinc-500">
                {{ t('groups.members', { count: g.member_count ?? 0 }, g.member_count ?? 0) }} ·
                {{ t('groups.folders', { count: g.grant_count ?? 0 }, g.grant_count ?? 0) }}
                <template v-if="ssoLinks(g)"> · {{ t('groups.ssoSummary', { list: ssoLinks(g) }) }}</template>
              </div>
            </div>
            <ChevronRight class="h-4 w-4 shrink-0 text-zinc-400 rtl:rotate-180" />
          </RouterLink>
        </li>
      </ul>
      <p v-else-if="groups.length === 0" class="text-sm text-zinc-500">{{ t('groups.empty') }}</p>
      <p v-else class="text-sm text-zinc-500">{{ t('groups.noMatch') }}</p>
    </template>

    <Modal v-model="creating" :title="t('groups.add')" size="sm">
      <form class="space-y-3" @submit.prevent="create">
        <Input v-model="newName" :label="t('groups.fields.name')" required data-testid="group-new-name" />
      </form>
      <template #footer>
        <Button variant="ghost" @click="creating = false">{{ t('common.cancel') }}</Button>
        <Button :loading="saving" :disabled="!newName.trim()" data-testid="group-new-create" @click="create">{{ t('groups.create') }}</Button>
      </template>
    </Modal>
  </div>
</template>
