<script setup lang="ts">
/**
 * The groups one account is in, on its edit page — and how: added by hand, or
 * through an SSO link (kept in step with the identity provider at each
 * sign-in). Adding and removing go through the group's own routes, so the
 * same lines hold as on the group's page: a delegated administrator never
 * changes their own groups, nor gives a role they could not give directly.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { UsersRound, X, Plus } from 'lucide-vue-next';

import { GroupsApi, type Group, type UserGroup } from '@/api/groups';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Select from '@/components/ui/Select.vue';
import Spinner from '@/components/ui/Spinner.vue';

const props = defineProps<{ userId: number; userName: string }>();
const emit = defineEmits<{ (e: 'changed'): void }>();

const { t } = useI18n();
const toast = useToastStore();

const mine = ref<UserGroup[]>([]);
const all = ref<Group[]>([]);
const loading = ref(true);
const pick = ref<string>('');
const busy = ref(false);

async function load() {
  loading.value = true;
  try {
    const [m, a] = await Promise.all([GroupsApi.forUser(props.userId), GroupsApi.list()]);
    mine.value = m;
    all.value = a;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);
watch(() => props.userId, load);

const options = computed(() => {
  const inIds = new Set(mine.value.map((g) => g.id));
  return [
    { value: '', label: t('groups.userCard.pick') },
    ...all.value.filter((g) => !inIds.has(g.id)).map((g) => ({ value: String(g.id), label: g.name })),
  ];
});

async function add() {
  if (!pick.value) return;
  busy.value = true;
  try {
    await GroupsApi.addMembers(Number(pick.value), [props.userId]);
    pick.value = '';
    await load();
    emit('changed');
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}

async function remove(g: UserGroup) {
  const msg =
    g.source === 'sso'
      ? t('groups.removeSsoConfirm', { name: props.userName })
      : t('groups.userCard.removeConfirm', { name: props.userName, group: g.name });
  if (!confirm(msg)) return;
  busy.value = true;
  try {
    await GroupsApi.removeMember(g.id, props.userId);
    await load();
    emit('changed');
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="card card-body space-y-3" data-testid="user-groups-card">
    <h2 class="flex items-center gap-2 text-base font-semibold"><UsersRound class="h-4 w-4" /> {{ t('groups.userCard.title') }}</h2>
    <div v-if="loading" class="text-center text-zinc-500"><Spinner /></div>
    <template v-else>
      <p v-if="!mine.length" class="text-sm text-zinc-500">{{ t('groups.userCard.none') }}</p>
      <ul v-else class="rule-list rounded-lg">
        <li v-for="g in mine" :key="g.id" class="flex items-center justify-between gap-3 px-3 py-2">
          <RouterLink :to="{ name: 'groups.edit', params: { id: g.id } }" class="font-medium hover:underline">{{ g.name }}</RouterLink>
          <div class="flex items-center gap-2 shrink-0">
            <Badge :tone="g.source === 'sso' ? 'sky' : 'zinc'">{{ g.source === 'sso' ? t('groups.source.sso') : t('groups.source.manual') }}</Badge>
            <Button size="sm" variant="ghost" :disabled="busy" :aria-label="t('groups.remove')" @click="remove(g)"><X class="h-4 w-4" /></Button>
          </div>
        </li>
      </ul>
      <div v-if="options.length > 1" class="flex items-end gap-2 flex-wrap">
        <Select v-model="pick" :options="options" :label="t('groups.userCard.add')" size="sm" class="w-60 max-w-full" />
        <Button type="button" size="sm" variant="outline" :disabled="!pick" :loading="busy" data-testid="user-group-add" @click="add">
          <Plus class="h-4 w-4" /> {{ t('groups.userCard.addButton') }}
        </Button>
      </div>
    </template>
  </div>
</template>
