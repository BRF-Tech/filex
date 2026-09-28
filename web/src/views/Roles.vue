<script setup lang="ts">
/**
 * Roles (administrators only). Every person has ONE role: a built-in one —
 * Administrator (everything, locked), User or Viewer, whose permissions are
 * edited here — or a custom one: its own list of permissions and limits,
 * optionally different in some folders (backend: permission rules). People are given a role on their own page; a custom
 * role can also be the starting role of new SSO accounts in some groups.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ShieldCheck, Plus, Pencil, Trash2, Lock } from 'lucide-vue-next';

import { RolesApi, type BuiltinRole, type PermCatalogue, type PermissionRule } from '@/api/roles';
import { StoragesApi } from '@/api/storages';
import type { StorageRef } from '@/api/types';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { permissionLimitLines } from '@/lib/permissionLimits';
import RoleEditor from '@/components/RoleEditor.vue';
import BuiltinRoleEditor from '@/components/BuiltinRoleEditor.vue';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Modal from '@/components/ui/Modal.vue';
import Select from '@/components/ui/Select.vue';

const { t, te, locale } = useI18n();
const toast = useToastStore();

const catalogue = ref<PermCatalogue | null>(null);
const roles = ref<PermissionRule[]>([]);
/** user id → the custom role they hold. */
const assignments = ref<Record<string, number>>({});
const storages = ref<StorageRef[]>([]);
/** People on each built-in role itself (counted by the server). */
const builtinMembers = ref<Partial<Record<'admin' | BuiltinRole, number>>>({});
const loading = ref(true);

const editorOpen = ref(false);
const editing = ref<PermissionRule | null>(null);
const builtinOpen = ref(false);
const builtinRole = ref<BuiltinRole>('user');

async function load() {
  loading.value = true;
  try {
    const [cat, rs, sts] = await Promise.all([
      RolesApi.catalogue(),
      RolesApi.listRules(),
      StoragesApi.list().catch(() => [] as StorageRef[]),
    ]);
    catalogue.value = cat;
    roles.value = rs.rules;
    assignments.value = rs.assignments;
    builtinMembers.value = rs.builtinMembers ?? {};
    storages.value = sts;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

const builtins = computed(() =>
  (['admin', 'user', 'viewer'] as const).map((role) => ({
    role,
    // People on the built-in role itself, not on a custom role.
    members: builtinMembers.value[role] ?? 0,
  })),
);

function openBuiltin(role: BuiltinRole) {
  builtinRole.value = role;
  builtinOpen.value = true;
}
function openNew() {
  editing.value = null;
  editorOpen.value = true;
}
function openEdit(r: PermissionRule) {
  editing.value = r;
  editorOpen.value = true;
}
function onSaved(r: PermissionRule) {
  const i = roles.value.findIndex((x) => x.id === r.id);
  if (i >= 0) roles.value.splice(i, 1, r);
  else roles.value.push(r);
  // Recount who is on which role.
  RolesApi.listRules()
    .then((rs) => {
      assignments.value = rs.assignments;
      builtinMembers.value = rs.builtinMembers ?? {};
    })
    .catch(() => {});
}

async function setEnabled(r: PermissionRule, enabled: boolean) {
  const n = memberCount(r);
  // A switched-off role gives its people nothing until it is on again.
  if (!enabled && n > 0 && !confirm(t('permissions.rules.disableConfirm', { name: r.name, count: n }, n))) return;
  try {
    const { id, created_by: _c, created_at: _a, updated_at: _u, ...body } = r;
    onSaved(await RolesApi.updateRule(id, { ...body, enabled }));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

// Deleting: a role nobody holds just goes; one people hold asks what they
// become (the server refuses otherwise).
const deleting = ref<PermissionRule | null>(null);
const moveTo = ref('user');
const moveOptions = computed(() => [
  { value: 'user', label: t('users.roles.user') },
  { value: 'viewer', label: t('users.roles.viewer') },
  ...roles.value.filter((x) => x.id !== deleting.value?.id).map((x) => ({ value: String(x.id), label: x.name })),
]);
async function remove(r: PermissionRule) {
  if (memberCount(r) > 0) {
    deleting.value = r;
    moveTo.value = 'user';
    return;
  }
  if (!confirm(t('permissions.rules.deleteConfirm', { name: r.name }))) return;
  await doDelete(r);
}
async function doDelete(r: PermissionRule, to?: string) {
  try {
    await RolesApi.deleteRule(r.id, to);
    roles.value = roles.value.filter((x) => x.id !== r.id);
    deleting.value = null;
    toast.success(t('permissions.rules.deleted'));
    await load();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

function permLabel(k: string): string {
  const key = `permissions.items.${k}.label`;
  return te(key) ? t(key) : k;
}
function memberCount(r: PermissionRule): number {
  return Object.values(assignments.value).filter((id) => id === r.id).length;
}
function membersText(r: PermissionRule): string {
  const n = memberCount(r);
  const parts = [t('permissions.rules.members', { count: n }, n)];
  const groups = r.targets.filter((x) => x.kind === 'sso_group').map((x) => x.value);
  if (groups.length) parts.push(t('permissions.rules.plusGroups', { list: groups.join(', ') }));
  return parts.join(' · ');
}
function placeText(r: PermissionRule): string {
  const parts: string[] = [];
  const ids = r.conditions?.storage_ids ?? [];
  if (ids.length) parts.push(ids.map((id) => storages.value.find((s) => s.id === id)?.name ?? `#${id}`).join(', '));
  const paths = r.conditions?.paths ?? [];
  if (paths.length) parts.push(paths.join(', '));
  return parts.join(' · ');
}
function effectsText(r: PermissionRule, eff: 'allow' | 'deny'): string {
  return Object.entries(r.effects)
    .filter(([, v]) => v === eff)
    .map(([k]) => permLabel(k))
    .join(', ');
}
function limitsText(r: PermissionRule): string {
  return permissionLimitLines(r.settings, t, locale.value).join('; ');
}
</script>

<template>
  <div class="space-y-5 max-w-4xl">
    <div class="flex items-start justify-between gap-3 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <ShieldCheck class="h-5 w-5" /> {{ t('permissions.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-2xl">{{ t('permissions.subtitle') }}</p>
      </div>
      <Button data-testid="rule-new" @click="openNew"><Plus class="h-4 w-4" /> {{ t('permissions.rules.add') }}</Button>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <template v-else-if="catalogue">
      <ul class="rule-list rounded-lg card" data-testid="roles-list">
        <!-- built-in -->
        <li
          v-for="b in builtins"
          :key="b.role"
          class="flex items-center justify-between gap-3 px-4 py-3"
          :data-testid="`role-builtin-${b.role}`"
        >
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="font-medium">{{ t(`users.roles.${b.role}`) }}</span>
              <Badge tone="zinc">{{ t('permissions.rules.builtin') }}</Badge>
            </div>
            <div class="text-xs text-zinc-500">
              {{ t('permissions.rules.members', { count: b.members }, b.members) }}
              <template v-if="b.role === 'admin'"> · {{ t('permissions.rules.locked') }}</template>
            </div>
          </div>
          <Lock v-if="b.role === 'admin'" class="h-4 w-4 shrink-0 text-zinc-400" :aria-label="t('permissions.rules.locked')" />
          <Button
            v-else
            size="sm"
            variant="ghost"
            :aria-label="t('permissions.rules.editBuiltin', { role: t(`users.roles.${b.role}`) })"
            @click="openBuiltin(b.role)"
          >
            <Pencil class="h-4 w-4" />
          </Button>
        </li>

        <!-- custom -->
        <li v-for="r in roles" :key="r.id" class="flex items-start justify-between gap-3 px-4 py-3" :data-testid="`rule-${r.id}`">
          <div class="min-w-0 space-y-0.5">
            <div class="flex items-center gap-2">
              <span class="font-medium">{{ r.name }}</span>
              <Badge tone="brand">{{ t('permissions.rules.custom') }}</Badge>
              <Badge v-if="!r.enabled" tone="zinc">{{ t('permissions.rules.disabled') }}</Badge>
            </div>
            <div class="text-xs text-zinc-500">
              {{ membersText(r) }} ·
              {{ t('permissions.rules.summaryCount', { count: (r.permissions ?? []).length, total: catalogue.permissions.length - 1 }) }}
            </div>
            <div v-if="placeText(r) && (effectsText(r, 'deny') || effectsText(r, 'allow'))" class="text-xs text-zinc-600 dark:text-zinc-300">
              {{ t('permissions.rules.summaryFolders', { places: placeText(r) }) }}
              <span v-if="effectsText(r, 'allow')" class="text-emerald-700 dark:text-emerald-400">
                {{ t('permissions.rules.summaryAllow', { list: effectsText(r, 'allow') }) }}
              </span>
              <span v-if="effectsText(r, 'deny')" class="text-rose-700 dark:text-rose-400">
                {{ t('permissions.rules.summaryDeny', { list: effectsText(r, 'deny') }) }}
              </span>
            </div>
            <div v-if="limitsText(r)" class="text-xs text-zinc-600 dark:text-zinc-300">
              {{ t('permissions.rules.summaryLimits', { list: limitsText(r) }) }}
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <Toggle :model-value="r.enabled" :label="t('permissions.rules.enabled')" @update:model-value="(v: boolean) => setEnabled(r, v)" />
            <Button size="sm" variant="ghost" :aria-label="t('permissions.rules.edit')" @click="openEdit(r)"><Pencil class="h-4 w-4" /></Button>
            <Button size="sm" variant="ghost" :aria-label="t('common.delete')" @click="remove(r)"><Trash2 class="h-4 w-4" /></Button>
          </div>
        </li>
      </ul>
      <p v-if="roles.length === 0" class="text-sm text-zinc-500">{{ t('permissions.rules.empty') }}</p>

      <RoleEditor
        v-model="editorOpen"
        :rule="editing"
        :catalogue="catalogue"
        :storages="storages"
        @saved="onSaved"
      />
      <BuiltinRoleEditor v-model="builtinOpen" :role="builtinRole" :catalogue="catalogue" />

      <Modal
        :model-value="deleting !== null"
        :title="t('permissions.rules.deleteTitle', { name: deleting?.name ?? '' })"
        @update:model-value="(v: boolean) => { if (!v) deleting = null; }"
      >
        <div v-if="deleting" class="space-y-3" data-testid="role-delete-move">
          <p class="text-sm">
            {{ t('permissions.rules.deleteMove', { count: memberCount(deleting) }, memberCount(deleting)) }}
          </p>
          <Select v-model="moveTo" :options="moveOptions" :label="t('permissions.rules.moveTo')" class="w-60 max-w-full" />
        </div>
        <template #footer>
          <Button variant="ghost" @click="deleting = null">{{ t('common.cancel') }}</Button>
          <Button variant="danger" data-testid="role-delete-confirm" @click="deleting && doDelete(deleting, moveTo)">
            {{ t('common.delete') }}
          </Button>
        </template>
      </Modal>
    </template>
  </div>
</template>
