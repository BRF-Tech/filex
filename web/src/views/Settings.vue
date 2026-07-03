<script setup lang="ts">
import { computed, onMounted, reactive, ref, watchEffect } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, Mail } from 'lucide-vue-next';

import { useSettingsStore } from '@/stores/settings';
import { useToastStore } from '@/stores/toast';
import { useAuthStore } from '@/stores/auth';
import { api, extractError } from '@/api/client';
import type { SettingsMap } from '@/api/types';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
const settings = useSettingsStore();
const toast = useToastStore();
const auth = useAuthStore();

const form = reactive<SettingsMap>({
  site_name: '',
  public_url: '',
  sync_interval_seconds: 300,
  log_level: 'info',
  default_locale: 'en',
  default_timezone: 'Europe/Istanbul',
});

watchEffect(() => {
  Object.assign(form, settings.data);
});

async function save() {
  try {
    // Send only the keys this page owns. `form` is hydrated from the full
    // settings map (watchEffect → Object.assign), so spreading it would
    // echo every unrelated key — including auth.* secrets — back to the
    // store on every save. Patch the managed subset explicitly instead.
    await settings.update({
      site_name: form.site_name,
      public_url: form.public_url,
      sync_interval_seconds: form.sync_interval_seconds,
      log_level: form.log_level,
      default_locale: form.default_locale,
      default_timezone: form.default_timezone,
    });
    toast.success(t('settings.savedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

// ── SMTP (e-posta) — davet/paylaşım bildirimleri buradan gönderilir ──
const smtp = reactive({ host: '', port: '587', tls: 'starttls', from: '', username: '', password: '' });
const smtpTesting = ref(false);
const smtpTestMsg = ref('');
const smtpVerified = computed(() => !!settings.data['smtp.verified_at']);
const smtpPwSet = computed(() => !!settings.data['smtp.password']);
const tlsOptions = [
  { value: 'starttls', label: 'STARTTLS (587)' },
  { value: 'tls', label: 'TLS (465)' },
  { value: 'none', label: 'None' },
];
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
async function sendSmtpTest() {
  const to = smtpTestTo.value.trim();
  if (!to || !to.includes('@')) {
    smtpTestMsg.value = t('settings.smtp.enterEmail');
    return;
  }
  smtpTesting.value = true;
  smtpTestMsg.value = '';
  try {
    // Real end-to-end send to the chosen address. Client-side timeout as a
    // backstop — the backend also bounds the SMTP dial.
    const { data } = await api.post<{ ok: boolean; sent?: boolean; stage?: string; error?: string }>(
      '/admin/settings/smtp-test', { to }, { timeout: 45000 },
    );
    if (data.ok && data.sent) {
      smtpTestMsg.value = `${t('settings.smtp.testOk')} → ${to}`;
      smtpTestAsking.value = false;
    } else if (data.ok) {
      smtpTestMsg.value = t('settings.smtp.testOk');
    } else {
      smtpTestMsg.value = `${t('settings.smtp.testFail')}: ${data.error ?? ''}`;
    }
    await settings.fetch();
  } catch (e: unknown) {
    smtpTestMsg.value = extractError(e, t('errors.generic'));
  } finally {
    smtpTesting.value = false;
  }
}

const logLevels = [
  { value: 'debug', label: 'debug' },
  { value: 'info', label: 'info' },
  { value: 'warn', label: 'warn' },
  { value: 'error', label: 'error' },
];

const localeOptions = [
  { value: 'en', label: 'English' },
  { value: 'tr', label: 'Türkçe' },
];

onMounted(() => settings.fetch());
</script>

<template>
  <div class="space-y-4 max-w-2xl">
    <div>
      <h1 class="text-xl font-semibold">{{ t('settings.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('settings.subtitle') }}</p>
    </div>

    <div v-if="settings.loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
    <form v-else class="card card-body space-y-3" @submit.prevent="save">
      <Input
        :model-value="form.site_name as string | undefined"
        :label="t('settings.siteName')"
        @update:model-value="(v) => (form.site_name = v as string)"
      />
      <Input
        :model-value="form.public_url as string | undefined"
        :label="t('settings.publicUrl')"
        :hint="t('settings.publicUrlHelp')"
        placeholder="https://files.example.com"
        monospace
        @update:model-value="(v) => (form.public_url = v as string)"
      />
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Input
          :model-value="form.sync_interval_seconds as number | undefined"
          type="number"
          :min="30"
          :step="30"
          :label="t('settings.syncInterval')"
          :hint="t('settings.syncIntervalHelp')"
          @update:model-value="(v) => (form.sync_interval_seconds = v as number)"
        />
        <Select
          :model-value="form.log_level as string | undefined"
          :options="logLevels"
          :label="t('settings.logLevel')"
          @update:model-value="(v) => (form.log_level = v as 'debug' | 'info' | 'warn' | 'error')"
        />
      </div>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Select
          :model-value="form.default_locale as string | undefined"
          :options="localeOptions"
          :label="t('settings.defaultLocale')"
          @update:model-value="(v) => (form.default_locale = v as 'en' | 'tr')"
        />
        <Input
          :model-value="form.default_timezone as string | undefined"
          :label="t('settings.defaultTimezone')"
          monospace
          @update:model-value="(v) => (form.default_timezone = v as string)"
        />
      </div>
      <div class="flex justify-end pt-2">
        <Button type="submit" :loading="settings.saving">
          <Save class="h-4 w-4" />
          {{ t('common.save') }}
        </Button>
      </div>
    </form>

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
        <Input :model-value="smtp.password" type="password" :label="t('settings.smtp.password')" :placeholder="smtpPwSet ? '••••••• (kayıtlı)' : ''" :hint="t('settings.smtp.passwordHelp')" @update:model-value="(v) => (smtp.password = v as string)" />
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
          <span v-if="smtpTestMsg" class="text-xs text-zinc-500 dark:text-zinc-400">{{ smtpTestMsg }}</span>
          <div class="ml-auto flex gap-2">
            <Button type="button" variant="outline" @click.prevent="askSmtpTest">{{ t('settings.smtp.test') }}</Button>
            <Button type="submit" :loading="settings.saving"><Save class="h-4 w-4" />{{ t('common.save') }}</Button>
          </div>
        </div>
      </div>
    </form>
  </div>
</template>
