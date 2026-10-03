<script setup lang="ts">
/**
 * A tenant running itself (docs/TENANT-ADMIN.md): its own OIDC and LDAP, the
 * providers the platform operator bound to it, and its own domains. ONE
 * component for both doors - a tenant's administrator on their own tenant
 * (no `tenantId`), the platform operator on any tenant's page (`tenantId`,
 * `operator`) - so the two see the same thing; what only the operator may do
 * (allow insecure and internal-network providers) is the server's to refuse
 * and this component's to not offer.
 */
import { computed, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Activity, Globe, KeyRound, Plus, Save, ShieldCheck, Trash2 } from 'lucide-vue-next';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { TenantSelfApi, domainRefusal, type TenantDomain, type TenantOverview } from '@/api/tenantSelf';
import type { AuthProvider, AuthProviderTestResult } from '@/api/types';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import { useToastStore } from '@/stores/toast';
import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Textarea from '@/components/ui/Textarea.vue';
import Toggle from '@/components/ui/Toggle.vue';
import ProviderFields from '@/components/ProviderFields.vue';
import ProviderChecks from '@/components/ProviderChecks.vue';

const props = defineProps<{ tenantId?: number; operator?: boolean }>();

const { t, locale } = useI18n();
const toast = useToastStore();

const view = ref<TenantOverview | null>(null);
const loading = ref(true);
const refusals = reactive<Record<string, string | undefined>>({});
const results = reactive<Record<string, AuthProviderTestResult | undefined>>({});
const busy = ref<string | null>(null);

interface Draft {
  enabled: boolean;
  label: string;
  values: Record<string, string | boolean>;
}
const drafts = reactive<Record<string, Draft>>({});

async function load() {
  loading.value = true;
  try {
    view.value = await TenantSelfApi.get(props.tenantId);
    for (const k of Object.keys(drafts)) delete drafts[k];
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

const fieldsOf = (p: AuthProvider) => view.value?.fields[p.driver] ?? [];

function draftOf(p: AuthProvider): Draft {
  if (!drafts[p.id]) {
    const cfg = (p.config_redacted ?? {}) as Record<string, unknown>;
    const values: Record<string, string | boolean> = {};
    for (const f of fieldsOf(p)) {
      if (f.kind === 'bool') values[f.key] = cfg[f.key] === undefined ? f.default === 'true' : cfg[f.key] === true;
      else if (f.kind === 'secret') values[f.key] = '';
      else values[f.key] = cfg[f.key] != null ? String(cfg[f.key]) : '';
    }
    drafts[p.id] = { enabled: p.enabled, label: p.label ?? '', values };
  }
  return drafts[p.id];
}

/** The form as the config map a save writes and a test checks (one builder). */
function configOf(p: AuthProvider): Record<string, unknown> {
  const d = draftOf(p);
  const out: Record<string, unknown> = {};
  for (const f of fieldsOf(p)) {
    const v = d.values[f.key];
    if (f.kind === 'bool') out[f.key] = !!v;
    else if (f.kind === 'secret') {
      if (String(v ?? '').trim()) out[f.key] = String(v).trim();
    } else out[f.key] = String(v ?? '').trim();
  }
  return out;
}

const title = (p: AuthProvider) => {
  const kind = t(`authProviders.providers.${p.driver}` as never);
  return p.label?.trim() ? `${kind} · ${p.label.trim()}` : `${kind} · ${p.id}`;
};
const stateTone = (s?: string) => (s === 'running' ? 'emerald' : s === 'failed' ? 'rose' : 'zinc');

async function addProvider(driver: string) {
  busy.value = `add-${driver}`;
  try {
    await TenantSelfApi.createProvider(props.tenantId, { driver, enabled: false });
    toast.success(t('tenantSelf.providerAdded'));
    await load();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = null;
  }
}

async function save(p: AuthProvider, confirmFailedTest = false) {
  refusals[p.id] = undefined;
  busy.value = p.id;
  const d = draftOf(p);
  try {
    const res = await TenantSelfApi.updateProvider(props.tenantId, p.id, {
      enabled: d.enabled,
      label: d.label.trim(),
      config: configOf(p),
      confirm_failed_test: confirmFailedTest || undefined,
    });
    results[p.id] = res.checks.length ? { testable: true, ok: res.checks.every((c) => c.status !== 'fail'), checks: res.checks } : undefined;
    toast.success(t('authProviders.savedApplied'));
    await load();
  } catch (e) {
    const res = (e as { response?: { status?: number; data?: { error?: string; checks?: AuthProviderTestResult['checks']; confirm_allowed?: boolean } } }).response;
    if (res?.status === 409 && res.data?.error === 'test_failed') {
      results[p.id] = { testable: true, ok: false, checks: res.data.checks ?? [] };
      if (res.data.confirm_allowed !== false && confirm(t('tenantSelf.confirmFailed'))) {
        busy.value = null;
        await save(p, true);
        return;
      }
      refusals[p.id] = t('authProviders.testFail');
    } else {
      refusals[p.id] = extractError(e, t('errors.generic'));
    }
  } finally {
    busy.value = null;
  }
}

async function test(p: AuthProvider) {
  busy.value = `test-${p.id}`;
  results[p.id] = undefined;
  try {
    results[p.id] = await TenantSelfApi.testProvider(props.tenantId, p.id, configOf(p));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = null;
  }
}

async function removeProvider(p: AuthProvider) {
  if (!confirm(t('authProviders.deleteConfirm', { name: title(p) }))) return;
  try {
    await TenantSelfApi.deleteProvider(props.tenantId, p.id);
    toast.success(t('authProviders.deleted'));
    await load();
  } catch (e) {
    refusals[p.id] = extractError(e, t('errors.generic'));
  }
}

async function setInsecure(allow: boolean) {
  if (!props.tenantId) return;
  try {
    await TenantSelfApi.setInsecure(props.tenantId, allow);
    await load();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

// ── own domains ──
const newDomain = ref('');
const domainError = ref('');
function refusalText(e: unknown): string {
  const code = domainRefusal(e);
  return code ? t(`tenantSelf.domainErrors.${code}` as never) : extractError(e, t('errors.generic'));
}
async function addDomain() {
  const name = newDomain.value.trim();
  if (!name) return;
  domainError.value = '';
  busy.value = 'add-domain';
  try {
    await TenantSelfApi.addDomain(props.tenantId, name);
    newDomain.value = '';
    await load();
  } catch (e) {
    domainError.value = refusalText(e);
  } finally {
    busy.value = null;
  }
}

const statusTone = (s: string) => (s === 'active' ? 'emerald' : s === 'suspended' ? 'amber' : 'zinc');
/** The check's findings, in the reader's language (last_error_code); the
 *  server's English sentence only for a code this screen does not know. */
const WHY_CODES = ['no_record', 'no_cname', 'points_elsewhere', 'wildcard_cname', 'dns_failed'] as const;
function detailOf(d: TenantDomain): string {
  if (d.status === 'active') return t('tenantSelf.activeSince', { when: when(d.active_since) });
  const code = d.last_error_code ?? '';
  if ((WHY_CODES as readonly string[]).includes(code)) {
    return t(`tenantSelf.why.${code}` as never, { target: d.target, ...(d.last_error_params ?? {}) });
  }
  return d.last_error || t('tenantSelf.pointAt', { target: d.target });
}
const when = (iso?: string | null) => (iso ? formatDate(iso, String(locale.value)) : '');
/** What filex's own ACME did for a domain, said honestly: obtained until when,
 *  not obtained (the reason on the line under it), or not asked for yet. */
function acmeText(d: TenantDomain): string {
  switch (d.acme?.state) {
    case 'obtained':
      return t('tenantSelf.acmeObtained', { when: when(d.acme?.not_after) });
    case 'failed':
      return t('tenantSelf.acmeFailed', { when: when(d.acme?.at) });
    default:
      return t('tenantSelf.acmeNone');
  }
}
/** A brought certificate within 14 days of its end (or past it) is said in warning colours. */
const SOON_MS = 14 * 24 * 3600 * 1000;
const certSoon = (d: TenantDomain) =>
  !!d.own_certificate && !!d.tls_not_after && new Date(d.tls_not_after).getTime() - Date.now() < SOON_MS;

const domainColumns = computed<DataColumn<TenantDomain>[]>(() => [
  { id: 'domain', label: t('tenantSelf.domain'), sortable: true, width: 200, min: 140, sortValue: (d) => d.domain },
  { id: 'status', label: t('tenantSelf.domainStatus'), sortable: true, width: 110, min: 90, sortValue: (d) => d.status },
  { id: 'detail', label: t('tenantSelf.domainDetail'), width: 280, min: 160 },
  { id: 'certificate', label: t('tenantSelf.certificate'), sortable: true, width: 150, min: 110, sortValue: (d) => (d.own_certificate ? 1 : 0) },
]);
function domainActions(d: TenantDomain): ContextAction[] {
  const out: ContextAction[] = [
    { key: 'check', label: t('tenantSelf.checkNow'), icon: 'refresh' },
    { key: 'cert', label: t('tenantSelf.bringCertificate'), icon: 'upload' },
  ];
  if (d.own_certificate) out.push({ key: 'uncert', label: t('tenantSelf.removeCertificate'), icon: 'close' });
  out.push({ key: 'delete', label: t('common.delete'), icon: 'delete', danger: true });
  return out;
}
async function onDomainAction(key: string, d: TenantDomain) {
  try {
    if (key === 'check') {
      const fresh = await TenantSelfApi.checkDomain(props.tenantId, d.id);
      toast.success(t(`tenantSelf.status.${fresh.status}` as never));
      await load();
    } else if (key === 'cert') {
      openCert(d);
    } else if (key === 'uncert') {
      await TenantSelfApi.removeCertificate(props.tenantId, d.id);
      await load();
    } else if (key === 'delete') {
      if (!confirm(t('tenantSelf.deleteDomainConfirm', { domain: d.domain }))) return;
      await TenantSelfApi.deleteDomain(props.tenantId, d.id);
      await load();
    }
  } catch (e) {
    toast.error(refusalText(e));
  }
}

const certFor = ref<TenantDomain | null>(null);
const certPem = ref('');
const keyPem = ref('');
const certError = ref('');
function openCert(d: TenantDomain) {
  certFor.value = d;
  certPem.value = '';
  keyPem.value = '';
  certError.value = '';
}
async function saveCert() {
  if (!certFor.value) return;
  busy.value = 'cert';
  certError.value = '';
  try {
    await TenantSelfApi.setCertificate(props.tenantId, certFor.value.id, certPem.value, keyPem.value);
    certFor.value = null;
    toast.success(t('tenantSelf.certificateSaved'));
    await load();
  } catch (e) {
    certError.value = refusalText(e);
  } finally {
    // The key leaves the page with the request, whatever the answer.
    keyPem.value = '';
    busy.value = null;
  }
}
</script>

<template>
  <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
  <div v-else-if="view" class="space-y-5" data-testid="tenant-self">
    <!-- ── sign-in ── -->
    <div class="card card-body space-y-3" data-testid="tenant-self-signin">
      <h2 class="flex items-center gap-2 text-base font-semibold"><KeyRound class="h-4 w-4" /> {{ t('tenantSelf.signinTitle') }}</h2>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tenantSelf.signinHint') }}</p>

      <Toggle
        v-if="operator"
        :model-value="view.tenant.allow_insecure_auth"
        :label="t('tenantSelf.insecure')"
        :description="t('tenantSelf.insecureHint')"
        name="tenant-self-insecure"
        @update:model-value="setInsecure"
      />
      <p v-else-if="view.tenant.allow_insecure_auth" class="text-xs text-amber-700 dark:text-amber-300">{{ t('tenantSelf.insecureOn') }}</p>
      <p v-else class="text-xs text-zinc-500 dark:text-zinc-400 flex items-center gap-1">
        <ShieldCheck class="h-3.5 w-3.5" aria-hidden="true" /> {{ t('tenantSelf.guarded') }}
      </p>

      <div v-for="p in view.providers" :key="p.id" class="rounded-lg border border-zinc-200 p-3 dark:border-zinc-700 space-y-3" :data-testid="`tenant-provider-${p.id}`">
        <div class="flex items-start justify-between gap-3">
          <h3 class="text-sm font-semibold flex flex-wrap items-center gap-2">
            {{ title(p) }}
            <Badge :tone="stateTone(p.state)" dot>{{ t(`authProviders.status.${p.status}` as never) }}</Badge>
          </h3>
          <Toggle :model-value="draftOf(p).enabled" :label="t('common.enabled')" :name="`tenant-provider-enabled-${p.id}`" @update:model-value="(v) => (draftOf(p).enabled = v)" />
        </div>
        <p v-if="p.last_error" class="text-xs text-rose-600 break-words">{{ p.last_error }}</p>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Input
            :model-value="draftOf(p).label"
            :label="t('authProviders.label')"
            :hint="t('authProviders.labelHint')"
            class="sm:col-span-2"
            :name="`tenant-provider-label-${p.id}`"
            @update:model-value="(v) => (draftOf(p).label = String(v ?? ''))"
          />
          <ProviderFields
            :fields="fieldsOf(p)"
            :values="draftOf(p).values"
            :secrets-set="p.secrets_set"
            :set-by-upgrade="p.set_by_upgrade"
            :prefix="`tenant-${p.id}`"
            @update="(k: string, v: string | boolean) => (draftOf(p).values[k] = v)"
          />
        </div>
        <ProviderChecks v-if="results[p.id]" :result="results[p.id]!" :testid="`tenant-provider-test-${p.id}`" />
        <p v-if="refusals[p.id]" class="error-text" role="alert">{{ refusals[p.id] }}</p>
        <div class="flex items-center justify-between gap-2">
          <Button variant="outline" size="sm" :loading="busy === `test-${p.id}`" :data-testid="`tenant-provider-test-button-${p.id}`" @click="test(p)">
            <Activity class="h-4 w-4" /> {{ t('common.testNow') }}
          </Button>
          <span class="flex items-center gap-2">
            <Button variant="ghost" size="sm" :data-testid="`tenant-provider-delete-${p.id}`" @click="removeProvider(p)">
              <Trash2 class="h-4 w-4" /> {{ t('authProviders.delete') }}
            </Button>
            <Button size="sm" :loading="busy === p.id" :data-testid="`tenant-provider-save-${p.id}`" @click="save(p)">
              <Save class="h-4 w-4" /> {{ t('authProviders.saveApply') }}
            </Button>
          </span>
        </div>
      </div>

      <div class="flex flex-wrap gap-2">
        <Button
          v-for="d in view.drivers"
          :key="d"
          variant="outline"
          size="sm"
          :loading="busy === `add-${d}`"
          :data-testid="`tenant-provider-add-${d}`"
          @click="addProvider(d)"
        >
          <Plus class="h-4 w-4" /> {{ t('tenantSelf.addOwn', { kind: t(`authProviders.providers.${d}` as never) }) }}
        </Button>
      </div>

      <div v-if="view.shared_providers.length" class="space-y-1" data-testid="tenant-self-shared">
        <p class="text-sm font-medium">{{ t('tenantSelf.sharedTitle') }}</p>
        <ul class="text-sm space-y-1">
          <li v-for="s in view.shared_providers" :key="s.name" class="flex items-center gap-2">
            <Badge :tone="stateTone(s.state)" dot>{{ s.label || t(`authProviders.providers.${s.driver}` as never) }}</Badge>
            <span class="text-xs text-zinc-500">{{ s.name }}</span>
          </li>
        </ul>
        <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('tenantSelf.sharedHint') }}</p>
      </div>
    </div>

    <!-- ── addresses and own domains ── -->
    <div class="card card-body space-y-3" data-testid="tenant-self-domains">
      <h2 class="flex items-center gap-2 text-base font-semibold"><Globe class="h-4 w-4" /> {{ t('tenantSelf.domainsTitle') }}</h2>
      <p v-if="view.platform_subdomain" class="text-sm">
        {{ t('tenantSelf.platformAddress') }} <code class="tbl-mono" data-testid="tenant-self-subdomain"><bdi>{{ view.platform_subdomain }}</bdi></code>
      </p>
      <p v-else class="text-sm text-zinc-500 dark:text-zinc-400" data-testid="tenant-self-no-subdomain">{{ t('tenantSelf.noTenantDomain') }}</p>

      <template v-if="view.platform_subdomain">
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tenantSelf.domainsHint', { target: view.platform_subdomain }) }}</p>
        <form class="flex items-end gap-2 flex-wrap" @submit.prevent="addDomain">
          <Input v-model="newDomain" :label="t('tenantSelf.addDomain')" placeholder="files.acme.example" monospace class="min-w-64" :error="domainError || null" data-testid="tenant-self-domain-input" />
          <Button type="submit" variant="outline" :loading="busy === 'add-domain'" :disabled="!newDomain.trim()" data-testid="tenant-self-domain-add">
            <Plus class="h-4 w-4" /> {{ t('tenantSelf.add') }}
          </Button>
        </form>
        <DataTable
          table-id="admin.tenant.domains"
          :columns="domainColumns"
          :rows="view.domains"
          :empty="t('tenantSelf.noDomains')"
          row-key="id"
          :row-actions="(d: TenantDomain) => domainActions(d)"
          :row-actions-test-id="(d: TenantDomain) => `tenant-domain-actions-${d.id}`"
          @row-action="(key: string, d: TenantDomain) => onDomainAction(key, d)"
        >
          <template #cell-domain="{ row }">
            <span class="tbl-mono" :data-testid="`tenant-domain-${(row as TenantDomain).id}`"><bdi>{{ (row as TenantDomain).domain }}</bdi></span>
          </template>
          <template #cell-status="{ row }">
            <div><Badge :tone="statusTone((row as TenantDomain).status)">{{ t(`tenantSelf.status.${(row as TenantDomain).status}` as never) }}</Badge></div>
          </template>
          <template #cell-detail="{ row }">
            <span class="tbl-sub tbl-clamp" :title="detailOf(row as TenantDomain)" :data-testid="`tenant-domain-detail-${(row as TenantDomain).id}`">
              {{ detailOf(row as TenantDomain) }}
            </span>
          </template>
          <template #cell-certificate="{ row }">
            <span v-if="certSoon(row as TenantDomain)" class="text-amber-700 dark:text-amber-300" :data-testid="`tenant-domain-cert-soon-${(row as TenantDomain).id}`">
              {{ t('tenantSelf.ownCertificateSoon', { when: when((row as TenantDomain).tls_not_after) }) }}
            </span>
            <span v-else-if="(row as TenantDomain).own_certificate">
              {{ t('tenantSelf.ownCertificate', { when: when((row as TenantDomain).tls_not_after) }) }}
            </span>
            <span v-else-if="view.tls_mode === 'acme'" :data-testid="`tenant-domain-acme-${(row as TenantDomain).id}`">
              <span :class="(row as TenantDomain).acme?.state === 'failed' ? 'text-rose-700 dark:text-rose-300' : ''">{{ acmeText(row as TenantDomain) }}</span>
              <span
                v-if="(row as TenantDomain).acme?.state === 'failed' && (row as TenantDomain).acme?.reason"
                class="tbl-sub tbl-clamp block"
                :title="(row as TenantDomain).acme?.reason"
                :data-testid="`tenant-domain-acme-reason-${(row as TenantDomain).id}`"
              >{{ t('tenantSelf.acmeReason', { reason: (row as TenantDomain).acme?.reason ?? '' }) }}</span>
            </span>
            <span v-else>{{ t('tenantSelf.certByProxy') }}</span>
          </template>
        </DataTable>
        <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('tenantSelf.ftpsNote') }}</p>
      </template>
    </div>

    <Modal :model-value="certFor !== null" :title="t('tenantSelf.bringCertificate')" size="md" @update:model-value="(v: boolean) => { if (!v) certFor = null; }">
      <form v-if="certFor" class="space-y-3" @submit.prevent="saveCert">
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tenantSelf.certificateHint', { domain: certFor.domain }) }}</p>
        <Textarea v-model="certPem" :rows="5" :label="t('tenantSelf.certificatePem')" monospace name="tenant-cert-pem" />
        <Textarea v-model="keyPem" :rows="5" :label="t('tenantSelf.keyPem')" monospace name="tenant-key-pem" />
        <p v-if="certError" class="error-text" role="alert">{{ certError }}</p>
      </form>
      <template #footer>
        <Button variant="ghost" @click="certFor = null">{{ t('common.cancel') }}</Button>
        <Button :loading="busy === 'cert'" :disabled="!certPem.trim() || !keyPem.trim()" data-testid="tenant-cert-save" @click="saveCert">
          <Save class="h-4 w-4" /> {{ t('common.save') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
