<script setup lang="ts">
/**
 * AppPluginPermissionList — the permissions an app asks for, as its review
 * shows them: each permission's id, filex's own label for it (the server's
 * words, in the reader's language) and the APP's reason for wanting it.
 *
 * ONE component for every screen that asks an administrator to approve
 * permissions: the install wizard's review (AppPluginInstallWizard) and a
 * plugin request's review (PluginRequestsPanel). Two copies of this list
 * would drift — the reasons were already printed as raw `{"en": …, "tr": …}`
 * once (2026-09-21) — and "what am I approving" must read the same on both.
 *
 * ⚠⚠ The reason is the app's words and arrives as every language at once
 * (wire.Text): the row's own `reason`, else the manifest's
 * `permission_reasons[id]`, both through `pluginLabelOf`.
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { pluginLabelOf, type PluginText } from '@brftech/filex-core';

import type { AppPluginPermissionReview } from '@/api/appPlugins';
import Badge from '@/components/ui/Badge.vue';

const props = defineProps<{
  permissions: AppPluginPermissionReview[];
  /** The manifest's own reasons (`permission_reasons`): the fallback when a row carries none. */
  reasons?: Record<string, PluginText>;
  /** The permissions an upgrade ADDS to the grant — what is being approved; marked "new". */
  added?: string[];
}>();

const { t, locale } = useI18n();

const addedSet = computed(() => new Set(props.added ?? []));

function reasonOf(perm: AppPluginPermissionReview): string {
  return pluginLabelOf(perm.reason, locale.value) || pluginLabelOf(props.reasons?.[perm.id], locale.value);
}
</script>

<template>
  <div>
    <h3 class="text-sm font-semibold">{{ t('appPlugins.wizard.permissions') }}</h3>
    <p v-if="!permissions.length" class="mt-1 text-sm text-zinc-500">{{ t('appPlugins.wizard.noPermissions') }}</p>
    <ul v-else class="rule-list mt-2 rounded-lg" data-testid="app-plugin-permissions">
      <li v-for="perm in permissions" :key="perm.id" class="p-3" :data-testid="`perm-${perm.id}`">
        <div class="flex flex-wrap items-center gap-2">
          <Badge tone="brand" size="xs"><span class="font-mono">{{ perm.id }}</span></Badge>
          <span class="text-sm font-medium">{{ perm.label }}</span>
          <Badge v-if="addedSet.has(perm.id)" tone="amber" size="xs" :data-testid="`perm-new-${perm.id}`">
            {{ t('appPlugins.wizard.newPermission') }}
          </Badge>
        </div>
        <p class="mt-1 text-xs text-zinc-600 dark:text-zinc-400">
          {{ reasonOf(perm) || t('appPlugins.wizard.noReason') }}
        </p>
      </li>
    </ul>
  </div>
</template>
