<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, Activity } from 'lucide-vue-next';

import { AuthProvidersApi } from '@/api/auth-providers';
import type { AuthProvider } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Input from '@/components/ui/Input.vue';
import Textarea from '@/components/ui/Textarea.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
const toast = useToastStore();

type FieldType = 'text' | 'secret' | 'bool';
interface FieldDef {
  key: string;
  type: FieldType;
  required?: boolean;
  placeholder?: string;
  monospace?: boolean;
}

// Per-provider config schema. Keys mirror the backend driver Init() config
// keys (stored as `auth.<provider>.<key>` settings). Providers listed with an
// empty array (local, api-token) have no extra config — only the on/off
// toggle. A provider missing from this map falls back to a raw JSON editor so
// the page never loses the ability to configure a future driver.
const PROVIDER_FIELDS: Record<string, FieldDef[]> = {
  oidc: [
    { key: 'issuer', type: 'text', required: true, placeholder: 'https://auth.example.com/realms/main', monospace: true },
    { key: 'client_id', type: 'text', required: true },
    { key: 'client_secret', type: 'secret' },
    { key: 'redirect_url', type: 'text', required: true, placeholder: 'https://files.example.com/api/auth/oidc/callback', monospace: true },
    { key: 'scopes', type: 'text', placeholder: 'openid profile email' },
    { key: 'role_claim', type: 'text', placeholder: 'realm_access.roles', monospace: true },
    { key: 'admin_group', type: 'text', placeholder: 'filex-admin' },
  ],
  ldap: [
    { key: 'url', type: 'text', required: true, placeholder: 'ldaps://dc.example.com:636', monospace: true },
    { key: 'base_dn', type: 'text', required: true, placeholder: 'dc=example,dc=com', monospace: true },
    { key: 'bind_dn', type: 'text', placeholder: 'cn=svc,dc=example,dc=com', monospace: true },
    { key: 'bind_password', type: 'secret' },
    { key: 'user_filter', type: 'text', placeholder: '(mail=%s)', monospace: true },
    { key: 'email_attr', type: 'text', placeholder: 'mail' },
    { key: 'start_tls', type: 'bool' },
  ],
  'proxy-header': [
    { key: 'trusted_proxies', type: 'text', required: true, placeholder: '10.0.0.0/8, 172.16.0.0/12', monospace: true },
    { key: 'header_user', type: 'text', placeholder: 'X-Auth-User', monospace: true },
    { key: 'header_email', type: 'text', placeholder: 'X-Auth-Email', monospace: true },
    { key: 'header_name', type: 'text', placeholder: 'X-Auth-Name', monospace: true },
    { key: 'header_roles', type: 'text', placeholder: 'X-Auth-Roles', monospace: true },
    { key: 'admin_role', type: 'text', placeholder: 'admin' },
    { key: 'auto_provision', type: 'bool' },
  ],
  local: [],
  'api-token': [],
};

interface Draft {
  enabled: boolean;
  values: Record<string, string | boolean>;
  secretSet: Record<string, boolean>; // a secret is already stored server-side
  present: Record<string, boolean>; // key existed in config (so we can clear it)
  raw: string | null; // fallback raw JSON for unknown providers
}

const items = ref<AuthProvider[]>([]);
const loading = ref(false);
const drafts = reactive<Record<string, Draft>>({});
const savingId = ref<string | null>(null);
const testingId = ref<string | null>(null);

function isKnown(id: string): boolean {
  return Object.prototype.hasOwnProperty.call(PROVIDER_FIELDS, id);
}

function fields(id: string): FieldDef[] {
  return PROVIDER_FIELDS[id] ?? [];
}

function fieldLabel(key: string): string {
  const k = `authProviders.fields.${key}`;
  const label = t(k as never);
  return label === k ? key : label;
}

function parseBool(v: unknown): boolean {
  return v === true || v === 'true' || v === '1' || v === 'yes' || v === 'on';
}

function ensureDraft(p: AuthProvider): Draft {
  if (!drafts[p.id]) {
    const cfg = (p.config_redacted ?? p.config ?? {}) as Record<string, unknown>;
    const known = isKnown(p.id);
    const values: Record<string, string | boolean> = {};
    const secretSet: Record<string, boolean> = {};
    const present: Record<string, boolean> = {};
    if (known) {
      for (const f of fields(p.id)) {
        const has = Object.prototype.hasOwnProperty.call(cfg, f.key);
        present[f.key] = has;
        if (f.type === 'bool') {
          values[f.key] = parseBool(cfg[f.key]);
        } else if (f.type === 'secret') {
          // Stored secrets come back redacted as "***" — keep the input blank
          // and only mark that one is set so the user knows it's configured.
          secretSet[f.key] = cfg[f.key] === '***' || (has && cfg[f.key] != null && cfg[f.key] !== '');
          values[f.key] = '';
        } else {
          values[f.key] = has && cfg[f.key] != null ? String(cfg[f.key]) : '';
        }
      }
    }
    drafts[p.id] = {
      enabled: p.enabled,
      values,
      secretSet,
      present,
      raw: known ? null : JSON.stringify(cfg, null, 2),
    };
  }
  return drafts[p.id];
}

async function load() {
  loading.value = true;
  try {
    items.value = await AuthProvidersApi.list();
    // Drop stale drafts so a reload re-hydrates from fresh server state
    // (e.g. a secret that just became "set").
    for (const k of Object.keys(drafts)) delete drafts[k];
    for (const p of items.value) ensureDraft(p);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

async function save(p: AuthProvider) {
  savingId.value = p.id;
  try {
    const d = ensureDraft(p);
    let config: Record<string, unknown> = {};
    if (isKnown(p.id)) {
      for (const f of fields(p.id)) {
        const v = d.values[f.key];
        if (f.type === 'bool') {
          config[f.key] = !!v;
        } else if (f.type === 'secret') {
          // Only send a secret the admin actually typed; blank keeps the
          // existing value (never echo the "***" placeholder back).
          const s = String(v ?? '').trim();
          if (s !== '') config[f.key] = s;
        } else {
          const s = String(v ?? '');
          // Send when non-empty, or to clear a previously-set value.
          if (s !== '' || d.present[f.key]) config[f.key] = s;
        }
      }
    } else {
      try {
        config = d.raw ? (JSON.parse(d.raw) as Record<string, unknown>) : {};
      } catch (err) {
        toast.error(`Invalid JSON: ${(err as Error).message}`);
        return;
      }
    }
    await AuthProvidersApi.update(p.id, { enabled: d.enabled, config });
    toast.success(t('authProviders.savedOk'));
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingId.value = null;
  }
}

async function test(p: AuthProvider) {
  testingId.value = p.id;
  try {
    const res = await AuthProvidersApi.test(p.id);
    if (res.ok) toast.success(t('authProviders.testOk'));
    else toast.warn(res.error ?? t('authProviders.testFail'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    testingId.value = null;
  }
}

const stateTone = (s: AuthProvider['status']) => {
  if (s === 'ok') return 'emerald';
  if (s === 'misconfigured') return 'rose';
  return 'zinc';
};

onMounted(load);
</script>

<template>
  <div class="space-y-4 max-w-3xl">
    <div>
      <h1 class="text-xl font-semibold">{{ t('authProviders.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('authProviders.subtitle') }}</p>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div v-else class="space-y-3">
      <div v-for="p in items" :key="p.id" class="card card-body space-y-3">
        <div class="flex items-start justify-between gap-3">
          <div>
            <h2 class="text-sm font-semibold flex items-center gap-2">
              {{ t(`authProviders.providers.${p.id}` as any) }}
              <Badge :tone="stateTone(p.status)" dot>{{ p.status }}</Badge>
            </h2>
            <p v-if="p.last_error" class="text-xs text-rose-600 mt-1 font-mono">
              {{ p.last_error }}
            </p>
          </div>
          <Toggle v-model="ensureDraft(p).enabled" :label="t('common.enabled')" />
        </div>

        <!-- Known provider with editable fields -->
        <div v-if="fields(p.id).length" class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <template v-for="f in fields(p.id)" :key="f.key">
            <div v-if="f.type === 'bool'" class="sm:col-span-2 pt-1">
              <Toggle
                :model-value="(ensureDraft(p).values[f.key] as boolean)"
                :label="fieldLabel(f.key)"
                @update:model-value="(v) => (ensureDraft(p).values[f.key] = v)"
              />
            </div>
            <Input
              v-else
              :model-value="(ensureDraft(p).values[f.key] as string)"
              :type="f.type === 'secret' ? 'password' : 'text'"
              :label="fieldLabel(f.key)"
              :required="f.required"
              :placeholder="f.type === 'secret' && ensureDraft(p).secretSet[f.key] ? '••••••••' : f.placeholder"
              :hint="f.type === 'secret' && ensureDraft(p).secretSet[f.key] ? t('authProviders.secretSet') : undefined"
              :monospace="f.monospace"
              autocomplete="off"
              @update:model-value="(v) => (ensureDraft(p).values[f.key] = (v ?? '') as string)"
            />
          </template>
        </div>

        <!-- Known provider with no extra config (local, api-token) -->
        <p v-else-if="isKnown(p.id)" class="text-xs text-zinc-500">
          {{ t('authProviders.noConfig') }}
        </p>

        <!-- Unknown provider — raw JSON fallback -->
        <Textarea
          v-else
          :model-value="(ensureDraft(p).raw as string)"
          :rows="6"
          :label="t('common.details')"
          monospace
          @update:model-value="(v) => (ensureDraft(p).raw = v as string)"
        />

        <div class="flex items-center justify-between pt-1 gap-2">
          <Button
            variant="outline"
            size="sm"
            :loading="testingId === p.id"
            @click="test(p)"
          >
            <Activity class="h-4 w-4" />
            {{ t('common.testNow') }}
          </Button>
          <Button size="sm" :loading="savingId === p.id" @click="save(p)">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>
