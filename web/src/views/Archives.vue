<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { Archive, CheckCircle2, Save, TestTube2, XCircle } from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';
import { archiveFormatLabel } from '@brftech/filex-core';
import { ArchivesApi, type ArchiveProvider, type ArchiveSettings } from '@/api/archives';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Toggle from '@/components/ui/Toggle.vue';

const { t } = useI18n();
const toast = useToastStore();
const loading = ref(true);
const saving = ref(false);
const testing = ref(false);
const error = ref('');
const settings = ref<ArchiveSettings | null>(null);
const maxMiB = ref(20480);

const formatOptions = computed(() => (settings.value?.allowed_formats || []).map((value) => ({ value, label: archiveFormatLabel(value) })));

async function load() {
  loading.value = true;
  error.value = '';
  try {
    settings.value = await ArchivesApi.get();
    maxMiB.value = Math.round(settings.value.max_expanded_bytes / 1048576);
  } catch (err) {
    error.value = extractError(err, t('errors.generic'));
  } finally {
    loading.value = false;
  }
}

function toggleFormat(format: string) {
  if (!settings.value) return;
  const formats = settings.value.allowed_formats;
  if (formats.includes(format)) {
    if (formats.length === 1 || settings.value.default_format === format) return;
    settings.value.allowed_formats = formats.filter((item) => item !== format);
  } else {
    settings.value.allowed_formats = [...formats, format];
  }
}

async function save() {
  if (!settings.value) return;
  saving.value = true;
  error.value = '';
  try {
    settings.value = await ArchivesApi.update({
      enabled: settings.value.enabled,
      default_format: settings.value.default_format,
      allowed_formats: settings.value.allowed_formats,
      max_entries: Number(settings.value.max_entries),
      max_expanded_bytes: Number(maxMiB.value) * 1048576,
      timeout_seconds: Number(settings.value.timeout_seconds),
    });
    maxMiB.value = Math.round(settings.value.max_expanded_bytes / 1048576);
    toast.success(t('archivesAdmin.saved'));
  } catch (err) {
    error.value = extractError(err, t('errors.generic'));
  } finally {
    saving.value = false;
  }
}

async function testProvider() {
  testing.value = true;
  error.value = '';
  try {
    await ArchivesApi.test();
    toast.success(t('archivesAdmin.testOk'));
    await load();
  } catch (err) {
    error.value = extractError(err, t('archivesAdmin.testFailed'));
  } finally {
    testing.value = false;
  }
}

function formatList(formats: string[]): string {
  return formats.map(archiveFormatLabel).join(', ');
}

/* The wire names ("builtin-zip", "sevenzip") are identifiers, not labels. */
function providerName(provider: ArchiveProvider): string {
  if (provider.name === 'builtin-zip') return t('archivesAdmin.providerBuiltin');
  if (provider.name === 'sevenzip') return t('archivesAdmin.providerSevenZip');
  return provider.name;
}

/* The server's reason is English; what the admin needs to know is which
 * 7-Zip to install, said in the page's language. */
function providerProblem(provider: ArchiveProvider): string {
  if (provider.available) return '';
  if (provider.name === 'sevenzip' && provider.required_version) {
    return t('archivesAdmin.sevenZipMissing', { version: provider.required_version });
  }
  return provider.error || '';
}

onMounted(load);
</script>

<template>
  <div class="max-w-3xl space-y-4">
    <div class="flex items-center gap-2">
      <Archive class="h-6 w-6 text-brand-600 dark:text-brand-400" />
      <div>
        <h1 class="text-xl font-semibold">{{ t('archivesAdmin.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('archivesAdmin.subtitle') }}</p>
      </div>
    </div>

    <div v-if="loading" class="flex justify-center py-16"><Spinner /></div>
    <div v-else-if="!settings" class="rounded-lg border border-rose-200 bg-rose-50 p-4 text-sm text-rose-700">{{ error }}</div>
    <template v-else>
      <div v-if="error" class="rounded-lg border border-rose-200 bg-rose-50 p-3 text-sm text-rose-700">{{ error }}</div>

      <section class="card card-body space-y-5">
        <Toggle v-model="settings.enabled" :label="t('archivesAdmin.enabled')" :description="t('archivesAdmin.enabledHint')" />
        <div>
          <div class="label-base mb-2">{{ t('archivesAdmin.formats') }}</div>
          <div class="flex flex-wrap gap-4">
            <Checkbox
              v-for="format in ['zip', '7z', 'tar', 'tar.gz', 'tar.bz2', 'tar.xz']"
              :key="format"
              :name="`archive-format-${format}`"
              :model-value="settings.allowed_formats.includes(format)"
              :label="archiveFormatLabel(format)"
              @update:model-value="toggleFormat(format)"
            />
          </div>
        </div>
        <Select v-model="settings.default_format" :options="formatOptions" :label="t('archivesAdmin.defaultFormat')" />
        <div class="grid gap-4 sm:grid-cols-3">
          <Input v-model="settings.max_entries" type="number" :min="1" :label="t('archivesAdmin.maxEntries')" />
          <Input v-model="maxMiB" type="number" :min="1" :label="t('archivesAdmin.maxSize')" />
          <Input v-model="settings.timeout_seconds" type="number" :min="10" :label="t('archivesAdmin.timeout')" />
        </div>
        <div class="flex justify-end">
          <Button :loading="saving" @click="save"><Save class="h-4 w-4" />{{ t('common.save') }}</Button>
        </div>
      </section>

      <section class="card card-body space-y-4">
        <div class="flex items-center justify-between gap-4">
          <div>
            <h2 class="font-semibold text-zinc-900 dark:text-zinc-100">{{ t('archivesAdmin.providers') }}</h2>
            <p class="text-sm text-zinc-500">{{ t('archivesAdmin.providersHint') }}</p>
          </div>
          <Button variant="secondary" :loading="testing" @click="testProvider"><TestTube2 class="h-4 w-4" />{{ t('archivesAdmin.test') }}</Button>
        </div>
        <div v-for="provider in settings.providers" :key="provider.name" class="rounded-lg border border-zinc-200 p-4 dark:border-zinc-700">
          <div class="flex items-center gap-2">
            <CheckCircle2 v-if="provider.available" class="h-5 w-5 text-emerald-500" />
            <XCircle v-else class="h-5 w-5 text-zinc-400" />
            <strong :data-testid="`archive-provider-${provider.name}`">{{ providerName(provider) }}</strong>
          </div>
          <p class="mt-1 text-xs text-zinc-500">{{ t('archivesAdmin.creates', { formats: formatList(provider.create_formats) }) }}</p>
          <p class="text-xs text-zinc-500">{{ t('archivesAdmin.extracts', { formats: formatList(provider.extract_formats) }) }}</p>
          <p v-if="provider.version" class="mt-1 text-xs text-zinc-500">{{ provider.version }}</p>
          <p v-if="providerProblem(provider)" class="mt-1 text-xs text-amber-600">{{ providerProblem(provider) }}</p>
        </div>
        <p class="text-xs text-zinc-500">{{ t('archivesAdmin.binaryHint') }}</p>
      </section>
    </template>
  </div>
</template>
