<script setup lang="ts">
/**
 * One account's permissions on its edit page: the role it holds (picked in
 * the account details above — one per person), the exceptions an
 * administrator sets for this person alone (Inherit / Allow / Deny per
 * permission), and beside each permission the answer that results and where
 * it comes from. The server holds the lines — a delegated administrator
 * cannot allow what they do not hold, nobody edits an administrator's — and a
 * refusal comes back as a toast.
 *
 * The installed apps' permissions (backend perm/app.go) are exceptions too,
 * in the same grid: Default / Allow / Deny, where Default is what the person
 * gets from their role and the app's default (the server's own answer,
 * `effective.apps[].inherited`). Only an administrator changes them — a
 * delegated one cannot tell what an app key would hand out, and the server
 * refuses (403) — so for anyone else they are shown, read-only.
 *
 * ⚠ A preset and "Clear exceptions" are about the catalogue's permissions
 * and leave the person's app exceptions exactly as they are, for everybody:
 * the app decisions are cleared in their own group ("Reset to defaults").
 * Writing the catalogue's alone used to wipe them silently (0.49 docs review).
 * The same holds for an exception a later filex wrote that this one does not
 * know (lib/foreignPermissions): the page cannot show it, so it keeps it.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { ShieldCheck, Save, Eraser } from 'lucide-vue-next';

import {
  RolesApi,
  type PermCatalogue,
  type PermEffect,
  type PermKey,
  type UserPermissions,
} from '@/api/roles';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { useAuthStore } from '@/stores/auth';
import { permissionLimitLines } from '@/lib/permissionLimits';
import { isAppKey, splitOverrides } from '@/lib/appPermissions';
import { isForeignPerm } from '@/lib/foreignPermissions';
import { roleName, type NamedRole } from '@/lib/roleName';
import PermissionGrid from '@/components/PermissionGrid.vue';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';

const props = defineProps<{
  userId: number;
  /** The built-in role the server holds for the account. */
  role: string;
  /** The custom role in force, if any (its id lets the grid name it too) —
   *  its own, or (group set) the one a group gives it. */
  customRole?: (NamedRole & { id?: number; group?: string | null }) | null;
}>();

const { t, locale } = useI18n();
const toast = useToastStore();
const auth = useAuthStore();

const catalogue = ref<PermCatalogue | null>(null);
const data = ref<UserPermissions | null>(null);
const overrides = ref<Record<PermKey, PermEffect>>({});
const loading = ref(true);
const saving = ref(false);

const isAdminRole = computed(() => props.role === 'admin');
/** App permissions are an administrator's to change, not a delegated one's. */
const appsReadOnly = computed(() => !auth.isAdmin);

/** The exceptions, split for the grid: the catalogue's and the apps' (app.*). */
const permOverrides = computed({
  get: () => splitOverrides(overrides.value).perms,
  set: (v: Record<PermKey, PermEffect>) => (overrides.value = { ...v, ...splitOverrides(overrides.value).apps }),
});
const appOverrides = computed({
  get: () => splitOverrides(overrides.value).apps,
  set: (v: Record<string, PermEffect>) => (overrides.value = { ...splitOverrides(overrides.value).perms, ...v }),
});
/** "Default" for each app permission: the answer without this person's exception. */
const appDefaults = computed<Record<string, boolean>>(() =>
  Object.fromEntries((data.value?.effective.apps ?? []).map((a) => [a.key, a.inherited.allowed])),
);
/** What "Clear exceptions" takes away: the catalogue's, never an app's, and
 *  never a later version's this page cannot show (lib/foreignPermissions). */
const clearable = computed(() =>
  Object.keys(overrides.value).filter((k) => !isAppKey(k) && !isForeignPerm(k, catalogue.value)),
);
function clearExceptions() {
  overrides.value = Object.fromEntries(
    Object.entries(overrides.value).filter(([k]) => isAppKey(k) || isForeignPerm(k, catalogue.value)),
  );
}
const locked = computed<PermKey[]>(() =>
  props.role === 'viewer' ? (catalogue.value?.permissions ?? []).filter((d) => d.viewer_capped).map((d) => d.key) : [],
);
/** The same exceptions in any order: the grid rebuilds the map, app keys and all. */
function sameExceptions(a: Record<string, PermEffect>, b: Record<string, PermEffect>): boolean {
  const ka = Object.keys(a);
  return ka.length === Object.keys(b).length && ka.every((k) => a[k] === b[k]);
}
const dirty = computed(() => !sameExceptions(overrides.value, data.value?.overrides ?? {}));

async function load() {
  loading.value = true;
  try {
    const [cat, perms] = await Promise.all([RolesApi.catalogue(), RolesApi.forUser(props.userId)]);
    catalogue.value = cat;
    apply(perms);
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

function apply(p: UserPermissions) {
  data.value = p;
  overrides.value = { ...(p.overrides ?? {}) };
}

async function save() {
  saving.value = true;
  try {
    apply(await RolesApi.setOverrides(props.userId, overrides.value));
    toast.success(t('permissions.card.saved'));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

/** A preset pins every permission it can: allowed if in the preset, denied
 *  if not — so the account ends up exactly there whatever the defaults and
 *  rules say. "Clear" goes back to inheriting everything. */
function applyPreset(name: string) {
  const preset = catalogue.value?.presets.find((p) => p.name === name);
  if (!preset || !catalogue.value) return;
  // A preset is the catalogue's; the person's app exceptions stay as they
  // are, and so does a later version's exception this page cannot show.
  const cat = catalogue.value;
  const next: Record<PermKey, PermEffect> = Object.fromEntries(
    Object.entries(overrides.value).filter(([k]) => isAppKey(k) || isForeignPerm(k, cat)),
  );
  for (const d of catalogue.value.permissions) {
    if (d.role_only || locked.value.includes(d.key)) continue;
    next[d.key] = preset.permissions.includes(d.key) ? 'allow' : 'deny';
  }
  overrides.value = next;
}

const presetNames = computed(() => (catalogue.value?.presets ?? []).map((p) => p.name).filter((n) => n !== 'full_admin'));

const currentPreset = computed(() => data.value?.effective.preset || 'custom');

const limits = computed(() => permissionLimitLines(data.value?.effective.settings, t, locale.value));

/** The held role's name in the panel's language (lib/roleName). */
const customRoleName = computed(() => roleName(props.customRole, locale.value));

onMounted(load);
// The role is changed above; when it is saved, redraw what it means here.
watch(() => [props.userId, props.role, props.customRole?.name, props.customRole?.group], load);
</script>

<template>
  <div class="card card-body space-y-3" data-testid="user-permissions-card">
    <div class="flex items-center justify-between gap-2 flex-wrap">
      <h2 class="flex items-center gap-2 text-base font-semibold">
        <ShieldCheck class="h-4 w-4" /> {{ t('permissions.card.title') }}
      </h2>
      <Badge v-if="data && customRole" tone="brand" data-testid="user-permissions-preset">{{ customRoleName }}</Badge>
      <Badge v-else-if="data" :tone="currentPreset === 'custom' ? 'amber' : 'zinc'" data-testid="user-permissions-preset">
        {{ t(`permissions.presets.${currentPreset}`) }}
      </Badge>
    </div>

    <div v-if="loading" class="text-center text-zinc-500"><Spinner /></div>

    <template v-else-if="catalogue && data">
      <p v-if="isAdminRole" class="text-sm text-zinc-600 dark:text-zinc-300">{{ t('permissions.card.adminNote') }}</p>
      <template v-else>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('permissions.card.subtitle') }}</p>
        <p class="text-sm" data-testid="user-role-line">
          <template v-if="customRole?.group">{{ t('permissions.card.groupRole', { role: customRoleName, group: customRole.group }) }}</template>
          <template v-else>{{ t('permissions.card.builtinRole', { role: customRole ? customRoleName : t(`users.roles.${role}`) }) }}</template>
        </p>
        <p v-if="role === 'viewer' && !customRole" class="text-xs text-amber-700 dark:text-amber-400">{{ t('permissions.card.viewerNote') }}</p>
        <p v-if="role === 'viewer' && customRole" class="text-xs text-amber-700 dark:text-amber-400" data-testid="read-only-role-note">
          <template v-if="customRole.group">{{ t('permissions.card.groupReadOnlyNote', { role: customRoleName, group: customRole.group }) }}</template>
          <template v-else>{{ t('permissions.card.readOnlyRoleNote', { role: customRoleName }) }}</template>
        </p>

        <h3 class="text-sm font-medium pt-2">{{ t('permissions.card.exceptionsTitle') }}</h3>

        <div class="flex items-center gap-2 flex-wrap text-sm">
          <span class="text-zinc-500">{{ t('permissions.presets.label') }}:</span>
          <Button
            v-for="name in presetNames"
            :key="name"
            type="button"
            size="sm"
            variant="outline"
            :data-testid="`preset-${name}`"
            @click="applyPreset(name)"
          >
            {{ t(`permissions.presets.${name}`) }}
          </Button>
        </div>

        <PermissionGrid
          v-model:effects="permOverrides"
          v-model:app-effects="appOverrides"
          mode="effects"
          :catalogue="catalogue.permissions"
          :effective="data.effective"
          :locked="locked"
          :rules="customRole ? [customRole] : []"
          :apps="catalogue.apps"
          :app-defaults="appDefaults"
          :apps-read-only="appsReadOnly"
          :apps-read-only-note="t('permissions.apps.readOnly')"
        />

        <p v-if="data.effective.conditional_rules.length" class="text-xs text-zinc-500">
          {{ t('permissions.card.conditional', { count: data.effective.conditional_rules.length }, data.effective.conditional_rules.length) }}
        </p>
        <div v-if="limits.length" class="text-xs text-zinc-600 dark:text-zinc-300">
          <div class="font-medium">{{ t('permissions.card.settingsTitle') }}</div>
          <ul class="list-disc ps-5">
            <li v-for="l in limits" :key="l">{{ l }}</li>
          </ul>
        </div>

        <div class="flex justify-end gap-2">
          <Button type="button" variant="ghost" :disabled="clearable.length === 0" data-testid="user-permissions-clear" @click="clearExceptions">
            <Eraser class="h-4 w-4" /> {{ t('permissions.card.clear') }}
          </Button>
          <Button type="button" :loading="saving" :disabled="!dirty" data-testid="user-permissions-save" @click="save">
            <Save class="h-4 w-4" /> {{ t('common.save') }}
          </Button>
        </div>
      </template>
    </template>
  </div>
</template>
