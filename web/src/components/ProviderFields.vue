<script setup lang="ts">
/**
 * The form of a sign-in provider's settings, drawn from the server's schema
 * (authsetup.Schema): one box per field, a switch for a yes/no field, a
 * multi-line box for a pasted PEM. ONE drawing for every page that edits a
 * provider - Admin → Identity providers and a tenant's own providers
 * (docs/TENANT-ADMIN.md) - so a field looks and reads the same wherever it is
 * set.
 *
 * A secret is never sent to the browser: its box starts empty and says
 * whether one is set; left empty, the stored one is kept.
 */
import { useI18n } from 'vue-i18n';
import type { AuthProviderField } from '@/api/types';
import Input from '@/components/ui/Input.vue';
import Textarea from '@/components/ui/Textarea.vue';
import Toggle from '@/components/ui/Toggle.vue';

const props = defineProps<{
  fields: AuthProviderField[];
  values: Record<string, string | boolean>;
  secretsSet?: Record<string, boolean>;
  /** Names the boxes (`auth-field-<prefix>-<key>`) for tests and labels. */
  prefix: string;
  disabled?: boolean;
  /** Fields whose value the upgrade chose (`set_by_upgrade`): their line
   *  says so first (`authProviders.upgradeHints.<key>`). */
  setByUpgrade?: string[];
}>();
const emit = defineEmits<{ (e: 'update', key: string, value: string | boolean): void }>();

const { t, te } = useI18n();

/**
 * How a field is drawn - the list of fields and their kinds is the server's.
 * A text field with no placeholder here shows the server's default for it
 * (`AuthProviderField.default`): an empty box means that value.
 */
const PRESENTATION: Record<string, { placeholder?: string; monospace?: boolean; inputmode?: string }> = {
  issuer: { placeholder: 'https://auth.example.com/realms/main', monospace: true },
  redirect_url: { placeholder: 'https://files.example.com/api/auth/oidc/callback', monospace: true },
  scopes: { placeholder: 'groups offline_access' },
  role_claim: { placeholder: 'realm_access.roles', monospace: true },
  admin_group: { placeholder: 'filex-admin' },
  url: { placeholder: 'ldaps://dc.example.com:636', monospace: true },
  base_dn: { placeholder: 'dc=example,dc=com', monospace: true },
  bind_dn: { placeholder: 'cn=svc,dc=example,dc=com', monospace: true },
  user_filter: { placeholder: '(mail=%s)', monospace: true },
  email_attr: { placeholder: 'mail' },
  ca_file: { placeholder: '/etc/filex/ldap-ca.pem', monospace: true },
  ca_pem: { placeholder: '-----BEGIN CERTIFICATE-----', monospace: true },
  trusted_proxies: { placeholder: '10.0.0.0/8, 172.16.0.0/12', monospace: true },
  header_user: { placeholder: 'X-Auth-User', monospace: true },
  header_email: { placeholder: 'X-Auth-Email', monospace: true },
  header_name: { placeholder: 'X-Auth-Name', monospace: true },
  header_roles: { placeholder: 'X-Auth-Roles', monospace: true },
  admin_role: { placeholder: 'admin' },
  group_attr: { monospace: true },
  pamtester_path: { monospace: true },
  service: { monospace: true },
  sudo_path: { monospace: true },
  // Whole numbers the schema keeps as text: the box stays text, the keyboard
  // is numeric and the hint says the range (authProviders.fieldHints).
  timeout_seconds: { inputmode: 'numeric' },
  max_concurrent: { inputmode: 'numeric' },
  email_domain: { placeholder: 'corp.example', monospace: true },
};

function fieldLabel(key: string): string {
  const k = `authProviders.fields.${key}`;
  return te(k) || te(k, 'en') ? t(k as never) : key;
}

/** What an empty box means: a stored secret, the example, or the server's default. */
function placeholder(f: AuthProviderField): string | undefined {
  if (f.kind === 'secret' && props.secretsSet?.[f.key]) return '••••••••';
  return PRESENTATION[f.key]?.placeholder ?? (f.kind === 'text' && f.default ? f.default : undefined);
}

/**
 * The line under a box (or a switch): a stored secret, the redirect default,
 * or the field's own hint - for a switch such as show_refusal_reason, the
 * warning of what turning it on costs.
 */
function hint(f: AuthProviderField): string | undefined {
  if (f.kind === 'secret' && props.secretsSet?.[f.key]) return t('authProviders.secretSet');
  if (f.key === 'redirect_url') return t('authProviders.redirectDefault');
  // Most fields have no hint: asked with te() first, so a missing one is not
  // looked up (and warned about) in every language on every render.
  const k = `authProviders.fieldHints.${f.key}`;
  const own = te(k) || te(k, 'en') ? t(k as never) : undefined;
  // A value the upgrade chose (trust_email of an OIDC that existed before
  // 0.50): that comes first, then what the field does.
  const u = `authProviders.upgradeHints.${f.key}`;
  if (props.setByUpgrade?.includes(f.key) && (te(u) || te(u, 'en'))) {
    return own ? `${t(u as never)} ${own}` : t(u as never);
  }
  return own;
}
</script>

<template>
  <template v-for="f in fields" :key="f.key">
    <div v-if="f.kind === 'bool'" class="sm:col-span-2 pt-1">
      <Toggle
        :model-value="values[f.key] as boolean"
        :label="fieldLabel(f.key)"
        :description="hint(f)"
        :name="`auth-field-${prefix}-${f.key}`"
        :disabled="disabled"
        @update:model-value="(v) => emit('update', f.key, v)"
      />
    </div>
    <Textarea
      v-else-if="f.kind === 'multiline'"
      :model-value="values[f.key] as string"
      :rows="4"
      :label="fieldLabel(f.key)"
      :name="`auth-field-${prefix}-${f.key}`"
      :placeholder="placeholder(f)"
      :hint="hint(f)"
      :disabled="disabled"
      monospace
      class="sm:col-span-2"
      @update:model-value="(v) => emit('update', f.key, String(v ?? ''))"
    />
    <Input
      v-else
      :model-value="values[f.key] as string"
      :type="f.kind === 'secret' ? 'password' : 'text'"
      :label="fieldLabel(f.key)"
      :required="f.required"
      :name="`auth-field-${prefix}-${f.key}`"
      :placeholder="placeholder(f)"
      :hint="hint(f)"
      :monospace="PRESENTATION[f.key]?.monospace"
      :inputmode="PRESENTATION[f.key]?.inputmode"
      :disabled="disabled"
      autocomplete="off"
      @update:model-value="(v) => emit('update', f.key, String(v ?? ''))"
    />
  </template>
</template>
