<script setup lang="ts">
import { computed, onMounted, reactive, ref, watchEffect } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, Mail } from 'lucide-vue-next';

import { useSettingsStore } from '@/stores/settings';
import { useToastStore } from '@/stores/toast';
import { useAuthStore } from '@/stores/auth';
import { api, extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Spinner from '@/components/ui/Spinner.vue';
import ThumbLimitsCard from '@/components/tools/ThumbLimitsCard.vue';
import SearchSettingsCard from '@/components/SearchSettingsCard.vue';
import InstanceSettingsCard from '@/components/settings/InstanceSettingsCard.vue';

const { t } = useI18n();
const settings = useSettingsStore();
const toast = useToastStore();
const auth = useAuthStore();

// ⚠ The "Site name" field is gone (task #258): the instance has ONE name,
// its Display name on Admin → Settings → Branding, which the mails read too
// (handlers/grants.go `siteName`). An old `site_name` row is carried over at
// boot (server/seed.go `upgradeSiteName`).
//
// ⚠ Only keys this page can actually change. `public_url`,
// `sync_interval_seconds`, `log_level`, `default_locale` and
// `default_timezone` used to live here as free-form keys: they saved, the row
// was written - and no code read any of them.

// ── SMTP (email) — invite/share notifications are sent from here ──
const smtp = reactive({ host: '', port: '587', tls: 'starttls', from: '', username: '', password: '' });
const smtpTesting = ref(false);
const smtpTestMsg = ref('');
/** The raw SMTP error behind `smtpTestMsg` — this screen is an
 *  administrator's, so it is shown, but as a second line and not as the
 *  answer (QA, 2026-09-21: the Go error WAS the answer). */
const smtpTestDetail = ref('');
const smtpVerified = computed(() => !!settings.data['smtp.verified_at']);
const smtpPwSet = computed(() => !!settings.data['smtp.password']);
// ⚠ computed, and "None" in words: the third option printed the English
// "None" in every language (release-candidate sweep, 2026-09-21), and a plain
// const would have kept the language the page was opened in.
const tlsOptions = computed(() => [
  { value: 'starttls', label: 'STARTTLS (587)' },
  { value: 'tls', label: 'TLS (465)' },
  { value: 'none', label: t('settings.smtp.encryptionNone') },
]);
watchEffect(() => {
  const d = settings.data;
  smtp.host = (d['smtp.host'] as string) ?? '';
  smtp.port = (d['smtp.port'] as string) ?? '587';
  smtp.tls = (d['smtp.tls'] as string) ?? 'starttls';
  smtp.from = (d['smtp.from'] as string) ?? '';
  smtp.username = (d['smtp.username'] as string) ?? '';
});
async function saveSmtp() {
  const patch: Record<string, unknown> = {
    'smtp.host': smtp.host,
    'smtp.port': smtp.port,
    'smtp.tls': smtp.tls,
    'smtp.from': smtp.from,
    'smtp.username': smtp.username,
  };
  if (smtp.password) patch['smtp.password'] = smtp.password;
  try {
    await settings.update(patch);
    smtp.password = '';
    toast.success(t('settings.savedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}
// Test flow: clicking "Test et" reveals an address prompt (prefilled with the
// admin's own email, else the sender) and only sends once confirmed — so the
// operator chooses where the real test message lands.
const smtpTestAsking = ref(false);
const smtpTestTo = ref('');
function askSmtpTest() {
  smtpTestMsg.value = '';
  smtpTestTo.value = auth.user?.email || smtp.from.trim() || '';
  smtpTestAsking.value = true;
}
const SMTP_REASONS = ['not_configured', 'host', 'connect', 'tls', 'starttls', 'auth', 'recipient', 'rejected', 'other'];
function smtpReasonKey(reason: string | undefined): string {
  return reason && SMTP_REASONS.includes(reason) ? reason : 'other';
}
async function sendSmtpTest() {
  const to = smtpTestTo.value.trim();
  if (!to || !to.includes('@')) {
    smtpTestMsg.value = t('settings.smtp.enterEmail');
    return;
  }
  smtpTesting.value = true;
  smtpTestMsg.value = '';
  smtpTestDetail.value = '';
  try {
    // Real end-to-end send to the chosen address. Client-side timeout as a
    // backstop — the backend also bounds the SMTP dial.
    const { data } = await api.post<{ ok: boolean; sent?: boolean; stage?: string; reason?: string; error?: string }>(
      '/admin/settings/smtp-test', { to }, { timeout: 45000 },
    );
    if (data.ok && data.sent) {
      smtpTestMsg.value = `${t('settings.smtp.testOk')} → ${to}`;
      smtpTestAsking.value = false;
    } else if (data.ok) {
      smtpTestMsg.value = t('settings.smtp.testOk');
    } else {
      // The server names the reason (mailer.Reason); an older server that
      // does not is `other`, never the raw error as the sentence.
      smtpTestMsg.value = t(`settings.smtp.reason.${smtpReasonKey(data.reason)}`);
      smtpTestDetail.value = data.error ?? '';
    }
    await settings.fetch();
  } catch (e: unknown) {
    smtpTestMsg.value = extractError(e, t('errors.generic'));
  } finally {
    smtpTesting.value = false;
  }
}

// ── tablo:t3 — the instance default folder view MOVED to Appearance ──
//
// A default look is a look: it sits beside the default theme on Admin →
// Settings → Appearance (components/settings/FolderViewCard.vue, task #258).

// ── tema:v1 — the operator's own stylesheet MOVED to admin → Appearance ──
//
// ⚠⚠ It is not a settings field any more, and the move is the point rather
// than tidying. The sheet has to be editable from a screen it cannot itself
// restyle, and Settings is not that screen: it is one route among many, wears
// whatever the sheet says, and has no reason to suspend it. Appearance
// (views/Appearance.vue) takes the <style> element OUT of the document while
// it is open, which is the only guard that survives `:root { display: none }`.
// Editing the sheet from here would have quietly reintroduced the lock-out.

onMounted(() => settings.fetch());
</script>

<template>
  <div class="space-y-4 max-w-2xl">
    <div>
      <h1 class="text-xl font-semibold">{{ t('settings.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('settings.subtitle') }}</p>
    </div>

    <div v-if="settings.loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
    <p v-else class="text-xs text-zinc-500 dark:text-zinc-400">
      {{ t('settings.envOnlyNote') }}
    </p>


    <!-- The server's defaults: what lived in the environment alone until
         0.57 (the default language, the sync interval, unfinished uploads,
         the file cache, the thumbnail cache's upkeep - task #258). -->
    <InstanceSettingsCard v-if="!settings.loading" group="general" />

    <!-- The thumbnail settings live here and only here (task #258); Admin →
         Maintenance → Tools → Thumbnail repair links to this card. -->
    <ThumbLimitsCard v-if="!settings.loading" id="thumb-limits" />

    <!-- The search settings (task #261), placed by the settings hub (#258):
         Files & storage → Search index links to this card. -->
    <SearchSettingsCard v-if="!settings.loading" id="search-settings" />

    <!-- SMTP / e-posta -->
    <form v-if="!settings.loading" class="card card-body space-y-3" @submit.prevent="saveSmtp">
      <div class="flex items-center justify-between gap-2">
        <h2 class="text-base font-semibold flex items-center gap-2">
          <Mail class="h-4 w-4" /> {{ t('settings.smtp.title') }}
        </h2>
        <span
          class="text-xs px-2 py-0.5 rounded-full"
          :class="smtpVerified ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300' : 'bg-zinc-100 text-zinc-500 dark:bg-zinc-800'"
        >{{ smtpVerified ? t('settings.smtp.verified') : t('settings.smtp.unverified') }}</span>
      </div>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('settings.smtp.help') }}</p>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Input :model-value="smtp.host" :label="t('settings.smtp.host')" placeholder="mail.example.com" monospace @update:model-value="(v) => (smtp.host = v as string)" />
        <Input :model-value="smtp.port" :label="t('settings.smtp.port')" placeholder="587" monospace @update:model-value="(v) => (smtp.port = v as string)" />
      </div>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Select :model-value="smtp.tls" :options="tlsOptions" :label="t('settings.smtp.tls')" @update:model-value="(v) => (smtp.tls = v as string)" />
        <Input :model-value="smtp.from" :label="t('settings.smtp.from')" placeholder="noreply@example.com" monospace @update:model-value="(v) => (smtp.from = v as string)" />
      </div>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Input :model-value="smtp.username" :label="t('settings.smtp.username')" :hint="t('settings.smtp.usernameHelp')" monospace @update:model-value="(v) => (smtp.username = v as string)" />
        <Input :model-value="smtp.password" type="password" :label="t('settings.smtp.password')" :placeholder="smtpPwSet ? t('settings.smtp.passwordSaved') : ''" :hint="t('settings.smtp.passwordHelp')" @update:model-value="(v) => (smtp.password = v as string)" />
      </div>
      <div class="space-y-2 pt-2">
        <div v-if="smtpTestAsking" class="flex flex-wrap items-end gap-2 rounded-md border border-zinc-200 dark:border-zinc-700 p-3">
          <div class="flex-1 min-w-[200px]">
            <Input
              :model-value="smtpTestTo"
              type="email"
              :label="t('settings.smtp.testTo')"
              placeholder="you@example.com"
              monospace
              @update:model-value="(v) => (smtpTestTo = v as string)"
              @keyup.enter.prevent="sendSmtpTest"
            />
          </div>
          <Button type="button" variant="primary" :loading="smtpTesting" @click.prevent="sendSmtpTest">{{ t('settings.smtp.send') }}</Button>
          <Button type="button" variant="ghost" @click.prevent="smtpTestAsking = false">{{ t('common.cancel') }}</Button>
        </div>
        <div class="flex items-center gap-3">
          <span v-if="smtpTestMsg" class="text-xs text-zinc-500 dark:text-zinc-400" data-testid="smtp-test-msg">{{ smtpTestMsg }}</span>
          <span
            v-if="smtpTestDetail"
            class="block text-[11px] font-mono text-zinc-400 dark:text-zinc-500 break-all"
            data-testid="smtp-test-detail"
          >{{ smtpTestDetail }}</span>
          <div class="ms-auto flex gap-2">
            <Button type="button" variant="outline" @click.prevent="askSmtpTest">{{ t('settings.smtp.test') }}</Button>
            <Button type="submit" :loading="settings.saving"><Save class="h-4 w-4" />{{ t('common.save') }}</Button>
          </div>
        </div>
      </div>
    </form>
  </div>
</template>
