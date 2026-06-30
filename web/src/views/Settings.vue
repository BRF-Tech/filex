<script setup lang="ts">
import { onMounted, reactive, watchEffect } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save } from 'lucide-vue-next';

import { useSettingsStore } from '@/stores/settings';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { SettingsMap } from '@/api/types';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
const settings = useSettingsStore();
const toast = useToastStore();

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
  </div>
</template>
