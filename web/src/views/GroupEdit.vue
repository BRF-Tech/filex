<script setup lang="ts">
/**
 * One group (admin.users): its name, its role, the SSO groups whose people are
 * members too, its members, and the folders it can reach. The server holds the
 * lines — a delegated administrator gives only roles whose permissions they
 * hold, never changes their own membership, and never edits the SSO links
 * (an administrator's) — and a refusal comes back as a toast.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { RouterLink, useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ArrowLeft, Save, Trash2, UsersRound, FolderLock, UserPlus, X } from 'lucide-vue-next';

import { GroupsApi, type GroupDetail, type GroupMember } from '@/api/groups';
import { RolesApi, type PermissionRule } from '@/api/roles';
import { UsersApi } from '@/api/users';
import type { User } from '@/api/types';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { useAuthStore } from '@/stores/auth';
import { personName } from '@brftech/filex-core';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Textarea from '@/components/ui/Textarea.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const toast = useToastStore();
const auth = useAuthStore();

const id = computed(() => Number(route.params.id));
const detail = ref<GroupDetail | null>(null);
const roles = ref<PermissionRule[]>([]);
const loading = ref(true);
const saving = ref(false);

const name = ref('');
const description = ref('');
/** "" is no role; otherwise the role's id. */
const roleId = ref<string>('');
const priority = ref<number>(0);
/** One SSO group per line. */
const ssoText = ref('');

async function load() {
  loading.value = true;
  try {
    const [d, rs] = await Promise.all([
      GroupsApi.get(id.value),
      RolesApi.listRules().catch(() => ({ rules: [] as PermissionRule[] })),
    ]);
    roles.value = rs.rules;
    apply(d);
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
    router.replace({ name: 'groups' });
  } finally {
    loading.value = false;
  }
}
onMounted(load);

function apply(d: GroupDetail) {
  detail.value = d;
  name.value = d.group.name;
  description.value = d.group.description;
  roleId.value = d.group.role_id == null ? '' : String(d.group.role_id);
  priority.value = d.group.priority ?? 0;
  ssoText.value = d.group.links
    .filter((l) => l.kind === 'sso')
    .map((l) => l.value)
    .join('\n');
}

function ssoValues(raw: string): string[] {
  // One per line, and nothing else separates: a directory's group name can
  // hold spaces and commas.
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(/\r?\n/)) {
    const v = part.trim();
    if (v && !seen.has(v)) {
      seen.add(v);
      out.push(v);
    }
  }
  return out;
}

const roleOptions = computed(() => [
  { value: '', label: t('groups.fields.noRole') },
  ...roles.value.map((r) => ({ value: String(r.id), label: r.enabled ? r.name : `${r.name} (${t('permissions.rules.disabled')})` })),
]);

async function save() {
  if (!detail.value) return;
  saving.value = true;
  try {
    // Links of kinds this page does not edit are kept as they are.
    const other = detail.value.group.links.filter((l) => l.kind !== 'sso');
    apply(
      await GroupsApi.update(id.value, {
        name: name.value.trim(),
        description: description.value.trim(),
        role_id: roleId.value === '' ? null : Number(roleId.value),
        priority: Number(priority.value) || 0,
        links: [...other, ...ssoValues(ssoText.value).map((value) => ({ kind: 'sso', value }))],
      }),
    );
    toast.success(t('groups.savedOk'));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

// ── delete ──
const showDelete = ref(false);
const deleting = ref(false);
async function confirmDelete() {
  deleting.value = true;
  try {
    await GroupsApi.remove(id.value);
    toast.success(t('groups.deletedOk'));
    router.replace({ name: 'groups' });
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    deleting.value = false;
    showDelete.value = false;
  }
}

// ── members ──
const memberQuery = ref('');
const suggestions = ref<User[]>([]);
let searchTimer: ReturnType<typeof setTimeout> | undefined;
onBeforeUnmount(() => searchTimer && clearTimeout(searchTimer));
const memberIds = computed(() => new Set((detail.value?.members ?? []).map((m) => m.user_id)));

function onMemberInput() {
  const q = memberQuery.value.trim();
  if (searchTimer) clearTimeout(searchTimer);
  if (!q) {
    suggestions.value = [];
    return;
  }
  searchTimer = setTimeout(async () => {
    try {
      const page = await UsersApi.list({ q });
      // Matched here too, not only by the server: a server that ignores
      // ?q= answers with everyone, and the first eight of those are not
      // the person being looked for.
      const term = q.toLocaleLowerCase();
      suggestions.value = page.items
        .filter((u) => !memberIds.value.has(u.id))
        .filter((u) => [u.email, u.display_name, u.username].some((s) => (s ?? '').toLocaleLowerCase().includes(term)))
        .slice(0, 8);
    } catch {
      suggestions.value = [];
    }
  }, 180);
}

async function addMember(u: User) {
  suggestions.value = [];
  memberQuery.value = '';
  try {
    apply(await GroupsApi.addMembers(id.value, [u.id]));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

async function removeMember(m: GroupMember) {
  // One who is in it through SSO is back at their next sign-in while the
  // identity provider still says so — say it before, not after.
  const msg =
    m.source === 'sso'
      ? t('groups.removeSsoConfirm', { name: m.name })
      : t('groups.removeConfirm', { name: m.name });
  if (!confirm(msg)) return;
  try {
    apply(await GroupsApi.removeMember(id.value, m.user_id));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

function sourceLabel(src: string): string {
  return src === 'sso' ? t('groups.source.sso') : t('groups.source.manual');
}

const levelLabel = (l: string) => t(`groups.level.${l}`);
</script>

<template>
  <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
  <div v-else-if="detail" class="space-y-5 max-w-2xl">
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2"><UsersRound class="h-5 w-5" /> {{ detail.group.name }}</h1>
        <p class="text-sm text-zinc-500">{{ t('groups.members', { count: detail.members.length }, detail.members.length) }}</p>
      </div>
      <Button variant="ghost" size="sm" @click="router.push({ name: 'groups' })">
        <ArrowLeft class="h-4 w-4 rtl:rotate-180" /> {{ t('common.back') }}
      </Button>
    </div>

    <form class="card card-body space-y-3" data-testid="group-form" @submit.prevent="save">
      <Input v-model="name" :label="t('groups.fields.name')" required />
      <Input v-model="description" :label="t('groups.fields.description')" />
      <Select v-model="roleId" :options="roleOptions" :label="t('groups.fields.role')" :hint="t('groups.fields.roleHint')" data-testid="group-role" />
      <Input
        v-if="roleId !== ''"
        v-model="priority"
        type="number"
        :min="-1000"
        :max="1000"
        :step="1"
        :label="t('groups.fields.priority')"
        :hint="auth.isAdmin ? t('groups.fields.priorityHint') : t('groups.fields.priorityAdminOnly')"
        :disabled="!auth.isAdmin"
        data-testid="group-priority"
      />
      <Textarea
        v-model="ssoText"
        :rows="3"
        :label="t('groups.fields.sso')"
        :hint="auth.isAdmin ? t('groups.fields.ssoHint') : t('groups.fields.ssoAdminOnly')"
        :disabled="!auth.isAdmin"
        data-testid="group-sso"
      />

      <div class="flex justify-between items-center pt-2 gap-2">
        <Button type="button" variant="danger" @click="showDelete = true">
          <Trash2 class="h-4 w-4" /> {{ t('common.delete') }}
        </Button>
        <Button type="submit" :loading="saving" data-testid="group-save">
          <Save class="h-4 w-4" /> {{ t('common.save') }}
        </Button>
      </div>
    </form>

    <!-- members -->
    <div class="card card-body space-y-3" data-testid="group-members">
      <h2 class="flex items-center gap-2 text-base font-semibold"><UserPlus class="h-4 w-4" /> {{ t('groups.membersTitle') }}</h2>
      <div class="relative">
        <Input v-model="memberQuery" type="search" :placeholder="t('groups.addMember')" size="sm" @update:model-value="onMemberInput" />
        <ul
          v-if="suggestions.length"
          class="row-box absolute z-10 mt-1 w-full rounded-lg shadow-lg"
        >
          <li v-for="u in suggestions" :key="u.id">
            <button
              type="button"
              class="w-full text-start px-3 py-2 text-sm hover:bg-[var(--fe-bg-hover)]"
              :data-testid="`group-add-${u.id}`"
              @click="addMember(u)"
            >
              {{ personName(u) }} <span class="text-xs text-zinc-500"><bdi>{{ u.email }}</bdi></span>
            </button>
          </li>
        </ul>
      </div>
      <p v-if="!detail.members.length" class="text-sm text-zinc-500">{{ t('groups.noMembers') }}</p>
      <ul v-else class="rule-list rounded-lg">
        <li v-for="m in detail.members" :key="m.user_id" class="flex items-center justify-between gap-3 px-3 py-2">
          <div class="min-w-0">
            <RouterLink :to="{ name: 'users.edit', params: { id: m.user_id } }" class="font-medium hover:underline">{{ m.name }}</RouterLink>
            <div class="text-xs text-zinc-500 truncate"><bdi>{{ m.email }}</bdi> · {{ t(`users.roles.${m.role}`) }}</div>
          </div>
          <div class="flex items-center gap-2 shrink-0">
            <Badge :tone="m.source === 'sso' ? 'sky' : 'zinc'" :title="m.source === 'sso' ? t('groups.source.ssoHint') : undefined">{{ sourceLabel(m.source) }}</Badge>
            <Button size="sm" variant="ghost" :aria-label="t('groups.remove')" @click="removeMember(m)"><X class="h-4 w-4" /></Button>
          </div>
        </li>
      </ul>
    </div>

    <!-- folder access -->
    <div class="card card-body space-y-3" data-testid="group-grants">
      <h2 class="flex items-center gap-2 text-base font-semibold"><FolderLock class="h-4 w-4" /> {{ t('groups.foldersTitle') }}</h2>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('groups.foldersHint') }}</p>
      <ul v-if="detail.grants.length" class="rule-list rounded-lg">
        <li v-for="g in detail.grants" :key="g.id" class="flex items-center justify-between gap-3 px-3 py-2 text-sm">
          <span class="tbl-mono truncate"><bdi>{{ g.storage_name }}://{{ g.path_prefix || '' }}</bdi></span>
          <Badge>{{ levelLabel(g.level) }}</Badge>
        </li>
      </ul>
    </div>

    <Modal v-model="showDelete" :title="t('common.delete')" size="sm">
      <p class="text-sm">{{ t('groups.deleteConfirm', { name: detail.group.name }) }}</p>
      <template #footer>
        <Button variant="ghost" @click="showDelete = false">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="deleting" @click="confirmDelete">{{ t('common.yesDelete') }}</Button>
      </template>
    </Modal>
  </div>
</template>
