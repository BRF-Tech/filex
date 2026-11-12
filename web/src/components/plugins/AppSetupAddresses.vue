<script setup lang="ts">
/**
 * AppSetupAddresses — the setup wizard's "Addresses" step: where each of the
 * app's public routes is served (docs/APP-PLUGINS.md → Public routes).
 *
 * Two kinds of address, the same two the server knows (internal/shortlink):
 *   - this filex's own address and a path prefix (`files.example.com/l/…`):
 *     the prefix is required and may not be a path filex uses itself;
 *   - a host of its own (`l.example.com/…`), with an optional prefix: that
 *     host answers the route and nothing else, so it needs a DNS record and a
 *     certificate, which the step says.
 *
 * ⚠ Each kind keeps its OWN prefix. The route's default prefix ("l") is the
 * own address's starting value (it is required there) and only the
 * placeholder of a host of its own: 0.60.0 kept one prefix for both, so the
 * "l" came along on a switch and l.example.com was added as
 * https://l.example.com/l. Kept per kind rather than cleared on a switch, so
 * switching back and forth loses nothing a person typed.
 *
 * ⚠ The rules are the SERVER's. A refused address comes back as a code, the
 * field it is about and a sentence in the reader's language; the sentence is
 * drawn under that field and nothing is guessed here.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { ChoiceButtons, DataTable, pluginLabelOf, type ChoiceOption, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { AppPluginsApi, setupRefusalOf, type AppSetupBase, type AppSetupRoute } from '@/api/appPlugins';
import { extractError } from '@/api/client';

import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';

const props = defineProps<{
  pluginId: number;
  routes: AppSetupRoute[];
  ownHosts: string[];
  multiTenant: boolean;
  tenants: { id: number; name: string }[];
  /** The setup was finished: an address may be switched off, not removed. */
  done: boolean;
}>();

const emit = defineEmits<{ (e: 'update:routes', routes: AppSetupRoute[]): void }>();

const { t, locale } = useI18n();

type Kind = 'own' | 'host';
interface Draft {
  kind: Kind;
  host: string;
  /** The prefix of each kind, kept apart (see the note above). */
  prefixes: Record<Kind, string>;
  tenant: string;
  busy: boolean;
  errors: { host?: string; prefix?: string; other?: string };
}
const drafts = ref<Record<string, Draft>>({});
// One draft per route, made before the template reads it (never during a
// render, which would write the state the render is reading).
watch(
  () => props.routes.map((r) => r.id),
  () => {
    for (const r of props.routes) {
      if (drafts.value[r.id]) continue;
      drafts.value[r.id] = { kind: 'own', host: '', prefixes: { own: r.default_prefix ?? '', host: '' }, tenant: '0', busy: false, errors: {} };
    }
  },
  { immediate: true },
);
function draftOf(route: AppSetupRoute): Draft {
  return drafts.value[route.id];
}

/** The prefix field's value: the one of the kind chosen now. */
function prefixOf(d: Draft): string {
  return d.prefixes[d.kind];
}
function setPrefix(d: Draft, v: string | number | null) {
  d.prefixes[d.kind] = String(v ?? '');
}
/** A refusal was about the kind it was asked for; the other kind starts clean. */
function setKind(d: Draft, v: string | string[]) {
  const kind = String(v) as Kind;
  if (kind === d.kind) return;
  d.kind = kind;
  d.errors = {};
}

const kindChoices = computed<ChoiceOption[]>(() => [
  { value: 'own', label: t('appPlugins.setup.addr.kindOwn'), help: t('appPlugins.setup.addr.kindOwnHelp', { host: ownHost.value }) },
  { value: 'host', label: t('appPlugins.setup.addr.kindHost'), help: t('appPlugins.setup.addr.kindHostHelp') },
]);
const tenantChoices = computed<ChoiceOption[]>(() => [
  { value: '0', label: t('appPlugins.setup.addr.tenantPlatform') },
  ...props.tenants.map((x) => ({ value: String(x.id), label: x.name })),
]);
const ownHost = computed(() => props.ownHosts[0] ?? '');

function routeLabel(r: AppSetupRoute): string {
  return pluginLabelOf(r.label, locale.value) || r.id;
}

/** What the address will look like, before it is asked for. */
function preview(d: Draft): string {
  const host = d.kind === 'own' ? ownHost.value : d.host.trim().toLowerCase();
  const prefix = prefixOf(d).trim().replace(/^\/+|\/+$/g, '');
  if (!host) return '';
  return `https://${host}${prefix ? `/${prefix}` : ''}/${t('appPlugins.setup.addr.codeWord')}`;
}

function tenantName(id: number): string {
  if (!id) return t('appPlugins.setup.addr.tenantPlatform');
  return props.tenants.find((x) => x.id === id)?.name ?? String(id);
}

function replaceBase(routeId: string, next: AppSetupBase | null, removedId?: number) {
  const routes = props.routes.map((r) => {
    if (r.id !== routeId) return r;
    let bases = r.bases.filter((b) => b.id !== (removedId ?? next?.id));
    if (next) {
      bases = r.bases.some((b) => b.id === next.id) ? r.bases.map((b) => (b.id === next.id ? next : b)) : [...r.bases, next];
      // One default per route: the server moved it, the list says so too.
      if (next.default) bases = bases.map((b) => (b.id === next.id ? b : { ...b, default: false }));
    }
    return { ...r, bases };
  });
  emit('update:routes', routes);
}

async function add(route: AppSetupRoute) {
  const d = draftOf(route);
  if (d.busy) return;
  d.busy = true;
  d.errors = {};
  try {
    const base = await AppPluginsApi.addSetupBase(props.pluginId, route.id, {
      host: d.kind === 'own' ? '' : d.host.trim(),
      prefix: prefixOf(d).trim(),
      tenant_id: d.kind === 'host' && props.multiTenant ? Number(d.tenant) : 0,
      default: !route.bases.some((b) => b.enabled),
    });
    replaceBase(route.id, base);
    d.host = '';
  } catch (e: unknown) {
    const r = setupRefusalOf(e);
    const said = r?.message || extractError(e, t('errors.updateFailed'));
    if (r?.field === 'host') d.errors = { host: said };
    else if (r?.field === 'prefix') d.errors = { prefix: said };
    else d.errors = { other: said };
  } finally {
    d.busy = false;
  }
}

const rowError = ref('');
async function onRowAction(route: AppSetupRoute, key: string, b: AppSetupBase) {
  rowError.value = '';
  try {
    if (key === 'default') replaceBase(route.id, await AppPluginsApi.patchSetupBase(props.pluginId, b.id, { default: true }));
    else if (key === 'off') replaceBase(route.id, await AppPluginsApi.patchSetupBase(props.pluginId, b.id, { enabled: false }));
    else if (key === 'on') replaceBase(route.id, await AppPluginsApi.patchSetupBase(props.pluginId, b.id, { enabled: true }));
    else if (key === 'remove') {
      await AppPluginsApi.deleteSetupBase(props.pluginId, b.id);
      replaceBase(route.id, null, b.id);
    }
  } catch (e: unknown) {
    rowError.value = setupRefusalOf(e)?.message || extractError(e, t('errors.updateFailed'));
  }
}

function rowActions(b: AppSetupBase): ContextAction[] {
  return [
    { key: 'default', label: t('appPlugins.setup.addr.makeDefault'), icon: 'star', hidden: b.default || !b.enabled },
    { key: 'off', label: t('appPlugins.setup.addr.switchOff'), icon: 'lock', hidden: !b.enabled },
    { key: 'on', label: t('appPlugins.setup.addr.switchOn'), icon: 'restore', hidden: b.enabled },
    { key: 'remove', label: t('appPlugins.setup.addr.remove'), icon: 'delete', danger: true, hidden: props.done },
  ];
}

const columns = computed<DataColumn<AppSetupBase>[]>(() => [
  { id: 'url', label: t('appPlugins.setup.addr.colAddress'), width: 320, lead: true },
  {
    id: 'kind',
    label: t('appPlugins.setup.addr.colKind'),
    width: 170,
    format: (b) => (b.host ? t('appPlugins.setup.addr.kindHost') : t('appPlugins.setup.addr.kindOwn')),
  },
  ...(props.multiTenant
    ? [{ id: 'tenant', label: t('appPlugins.setup.addr.colTenant'), width: 150, format: (b: AppSetupBase) => (b.host ? tenantName(b.tenant_id) : t('appPlugins.setup.addr.everyTenant')) }]
    : []),
  { id: 'state', label: t('appPlugins.setup.addr.colState'), width: 150 },
]);
</script>

<template>
  <div class="space-y-6" data-testid="app-setup-addresses">
    <section v-for="route in routes" :key="route.id" class="space-y-3" :data-testid="`app-setup-route-${route.id}`">
      <h3 class="text-sm font-semibold">{{ routeLabel(route) }}</h3>

      <DataTable
        v-if="route.bases.length"
        :table-id="`admin.app-setup.bases`"
        :columns="columns"
        :rows="route.bases"
        row-key="id"
        :row-actions="(b: AppSetupBase) => rowActions(b)"
        :row-actions-test-id="(b: AppSetupBase) => `app-setup-base-actions-${b.id}`"
        @row-action="(key: string, b: AppSetupBase) => onRowAction(route, key, b)"
      >
        <template #cell-url="{ row }">
          <span class="font-mono text-xs" :data-testid="`app-setup-base-${row.id}`">{{ row.url }}</span>
        </template>
        <template #cell-state="{ row }">
          <span class="inline-flex flex-wrap gap-1">
            <Badge v-if="row.default" tone="brand" size="xs">{{ t('appPlugins.setup.addr.default') }}</Badge>
            <Badge :tone="row.enabled ? 'emerald' : 'zinc'" size="xs" dot>
              {{ row.enabled ? t('appPlugins.setup.addr.on') : t('appPlugins.setup.addr.off') }}
            </Badge>
          </span>
        </template>
      </DataTable>
      <p v-else class="text-sm text-zinc-600 dark:text-zinc-400" :data-testid="`app-setup-route-empty-${route.id}`">
        {{ t('appPlugins.setup.addr.none') }}
      </p>
      <p v-if="rowError" class="text-sm text-rose-700 dark:text-rose-300" role="alert">{{ rowError }}</p>

      <!-- Adding one: which kind, then its parts, then what it will look like. -->
      <div class="space-y-3 rounded-lg border border-zinc-200 p-3 dark:border-zinc-800" :data-testid="`app-setup-add-${route.id}`">
        <ChoiceButtons
          :model-value="draftOf(route).kind"
          :options="kindChoices"
          segmented
          :aria-label="t('appPlugins.setup.addr.kindLabel')"
          :testid-prefix="`app-setup-kind-${route.id}`"
          @update:model-value="(v: string | string[]) => setKind(draftOf(route), v)"
        />
        <Input
          v-if="draftOf(route).kind === 'host'"
          v-model="draftOf(route).host"
          :name="`app-setup-host-${route.id}`"
          :label="t('appPlugins.setup.addr.host')"
          placeholder="l.example.com"
          autocomplete="off"
          monospace
          :error="draftOf(route).errors.host"
        />
        <Input
          :model-value="prefixOf(draftOf(route))"
          :name="`app-setup-prefix-${route.id}`"
          :label="draftOf(route).kind === 'own' ? t('appPlugins.setup.addr.prefix') : t('appPlugins.setup.addr.prefixOptional')"
          :placeholder="route.default_prefix || 'l'"
          autocomplete="off"
          monospace
          :error="draftOf(route).errors.prefix"
          @update:model-value="(v: string | number | null) => setPrefix(draftOf(route), v)"
        />
        <div v-if="draftOf(route).kind === 'host' && multiTenant" class="space-y-1">
          <span class="label-base">{{ t('appPlugins.setup.addr.tenant') }}</span>
          <ChoiceButtons
            :model-value="draftOf(route).tenant"
            :options="tenantChoices"
            :aria-label="t('appPlugins.setup.addr.tenant')"
            :testid-prefix="`app-setup-tenant-${route.id}`"
            @update:model-value="(v: string | string[]) => (draftOf(route).tenant = String(v))"
          />
        </div>
        <p v-if="preview(draftOf(route))" class="text-xs text-zinc-600 dark:text-zinc-400" :data-testid="`app-setup-preview-${route.id}`">
          {{ t('appPlugins.setup.addr.preview') }} <span class="font-mono" dir="ltr">{{ preview(draftOf(route)) }}</span>
        </p>
        <p v-if="draftOf(route).kind === 'host'" class="text-xs text-zinc-600 dark:text-zinc-400" :data-testid="`app-setup-dns-${route.id}`">
          {{ t('appPlugins.setup.addr.dnsHint', { host: draftOf(route).host.trim() || 'l.example.com', target: ownHost }) }}
        </p>
        <p v-if="draftOf(route).errors.other" class="text-sm text-rose-700 dark:text-rose-300" role="alert">{{ draftOf(route).errors.other }}</p>
        <div class="flex justify-end">
          <Button variant="secondary" size="sm" :loading="draftOf(route).busy" :data-testid="`app-setup-add-button-${route.id}`" @click="add(route)">
            {{ t('appPlugins.setup.addr.add') }}
          </Button>
        </div>
      </div>
    </section>
  </div>
</template>
