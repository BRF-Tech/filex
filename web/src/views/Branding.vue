<script setup lang="ts">
/* wiring:e1 — Branding settings page: identity fields for the public
   share/drop/PIN pages + the login screen, with a live preview card. */
import { computed, onMounted, reactive, ref, watchEffect } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, Palette, RotateCcw, Upload } from 'lucide-vue-next';

import { useSettingsStore } from '@/stores/settings';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Spinner from '@/components/ui/Spinner.vue';
import LogoMark from '@/components/LogoMark.vue';

const LOGO_MAX_BYTES = 256 * 1024;

const { t } = useI18n();
const settings = useSettingsStore();
const toast = useToastStore();

const form = reactive({
  name: '',
  logo_url: '',
  accent: '',
  footer_text: '',
  hide_powered_by: false,
});
const logoError = ref('');
const fileInput = ref<HTMLInputElement | null>(null);

watchEffect(() => {
  const d = settings.data;
  form.name = (d['branding.name'] as string) ?? '';
  form.logo_url = (d['branding.logo_url'] as string) ?? '';
  form.accent = (d['branding.accent'] as string) ?? '';
  form.footer_text = (d['branding.footer_text'] as string) ?? '';
  form.hide_powered_by = String(d['branding.hide_powered_by'] ?? '') === 'true';
});

const accentValid = computed(() => /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(form.accent));
const previewAccent = computed(() => (accentValid.value ? form.accent : '#4f46e5'));

function pickLogo() {
  fileInput.value?.click();
}

function onLogoFile(e: Event) {
  logoError.value = '';
  const input = e.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  if (!file.type.startsWith('image/')) {
    logoError.value = t('branding.logoNotImage');
    return;
  }
  const reader = new FileReader();
  reader.onload = () => {
    const uri = String(reader.result ?? '');
    // The backend caps the stored data URI at 256KB — mirror it client-side
    // so the save doesn't 400 after the fact.
    if (uri.length > LOGO_MAX_BYTES) {
      logoError.value = t('branding.logoTooBig');
      return;
    }
    form.logo_url = uri;
  };
  reader.readAsDataURL(file);
}

async function save() {
  if (form.accent && !accentValid.value) {
    toast.error(t('branding.accentInvalid'));
    return;
  }
  try {
    await settings.update({
      'branding.name': form.name.trim(),
      'branding.logo_url': form.logo_url.trim(),
      'branding.accent': form.accent.trim(),
      'branding.footer_text': form.footer_text.trim(),
      'branding.hide_powered_by': form.hide_powered_by ? 'true' : 'false',
    });
    toast.success(t('settings.savedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

async function resetAll() {
  try {
    await settings.update({
      'branding.name': '',
      'branding.logo_url': '',
      'branding.accent': '',
      'branding.footer_text': '',
      'branding.hide_powered_by': 'false',
    });
    logoError.value = '';
    toast.success(t('branding.resetOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

onMounted(() => settings.fetch());
</script>

<template>
  <div class="space-y-4 max-w-5xl">
    <div>
      <h1 class="text-xl font-semibold flex items-center gap-2">
        <Palette class="h-5 w-5" /> {{ t('branding.title') }}
      </h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('branding.subtitle') }}</p>
    </div>

    <div v-if="settings.loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div v-else class="grid gap-4 lg:grid-cols-2 items-start">
      <!-- ── Form ── -->
      <form class="card card-body space-y-3" @submit.prevent="save">
        <Input
          :model-value="form.name"
          :label="t('branding.name')"
          :hint="t('branding.nameHelp')"
          :placeholder="'Acme Cloud'"
          @update:model-value="(v) => (form.name = v as string)"
        />

        <div>
          <Input
            :model-value="form.logo_url"
            :label="t('branding.logo')"
            :hint="t('branding.logoHelp')"
            placeholder="https://… veya data:image/…"
            monospace
            @update:model-value="(v) => (form.logo_url = v as string)"
          />
          <div class="mt-2 flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" @click.prevent="pickLogo">
              <Upload class="h-3.5 w-3.5" />
              {{ t('branding.logoUpload') }}
            </Button>
            <Button
              v-if="form.logo_url"
              type="button"
              variant="ghost"
              size="sm"
              @click.prevent="form.logo_url = ''"
            >
              {{ t('common.remove') }}
            </Button>
            <input ref="fileInput" type="file" accept="image/*" class="hidden" @change="onLogoFile" />
          </div>
          <p v-if="logoError" class="mt-1 text-xs text-rose-600 dark:text-rose-400">{{ logoError }}</p>
        </div>

        <div>
          <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
            {{ t('branding.accent') }}
          </label>
          <div class="flex items-center gap-2">
            <input
              type="color"
              :value="previewAccent"
              class="h-9 w-12 cursor-pointer rounded border border-zinc-200 dark:border-zinc-700 bg-transparent p-0.5"
              :aria-label="t('branding.accent')"
              @input="(e) => (form.accent = (e.target as HTMLInputElement).value)"
            />
            <div class="flex-1">
              <Input
                :model-value="form.accent"
                placeholder="#4f46e5"
                monospace
                @update:model-value="(v) => (form.accent = v as string)"
              />
            </div>
            <Button v-if="form.accent" type="button" variant="ghost" size="sm" @click.prevent="form.accent = ''">
              {{ t('common.remove') }}
            </Button>
          </div>
          <p class="mt-1 text-xs" :class="form.accent && !accentValid ? 'text-rose-600 dark:text-rose-400' : 'text-zinc-500 dark:text-zinc-400'">
            {{ form.accent && !accentValid ? t('branding.accentInvalid') : t('branding.accentHelp') }}
          </p>
        </div>

        <Input
          :model-value="form.footer_text"
          :label="t('branding.footerText')"
          :hint="t('branding.footerHelp')"
          @update:model-value="(v) => (form.footer_text = v as string)"
        />

        <Checkbox v-model="form.hide_powered_by" :label="t('branding.hidePoweredBy')" />
        <p class="text-xs text-zinc-500 dark:text-zinc-400 -mt-1">{{ t('branding.hidePoweredByHelp') }}</p>

        <div class="flex justify-between items-center pt-2">
          <Button type="button" variant="outline" @click.prevent="resetAll">
            <RotateCcw class="h-4 w-4" />
            {{ t('branding.reset') }}
          </Button>
          <Button type="submit" :loading="settings.saving">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <!-- ── Live preview (public share page mock) ── -->
      <div class="card card-body space-y-3">
        <h2 class="text-sm font-semibold text-zinc-500 dark:text-zinc-400 uppercase tracking-wide">
          {{ t('branding.preview') }}
        </h2>
        <div
          class="rounded-xl border border-zinc-200 dark:border-zinc-800 p-6 flex flex-col items-center gap-4"
          style="background: linear-gradient(160deg, #f5f7fb, #e9edf4)"
        >
          <div v-if="form.logo_url || form.name" class="flex items-center gap-2.5 max-w-full">
            <img v-if="form.logo_url" :src="form.logo_url" alt="" class="h-8 max-w-[160px] object-contain" />
            <span v-if="form.name" class="font-bold text-zinc-900 break-words">{{ form.name }}</span>
          </div>
          <div class="w-full max-w-xs rounded-2xl bg-white border border-zinc-200 shadow-lg p-6 text-center">
            <div
              class="mx-auto mb-3 grid h-12 w-12 place-items-center rounded-full"
              :style="{ backgroundColor: previewAccent + '22', color: previewAccent }"
            >
              <svg viewBox="0 0 24 24" class="h-6 w-6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3.5 7a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7z"/></svg>
            </div>
            <div class="text-sm font-semibold text-zinc-900">{{ t('branding.previewFile') }}</div>
            <div class="mt-0.5 text-xs text-zinc-500">rapor-2026.pdf · 1.2 MB</div>
            <button
              type="button"
              class="mt-4 w-full rounded-lg py-2.5 text-sm font-semibold text-white"
              :style="{ backgroundColor: previewAccent }"
            >
              {{ t('branding.previewDownload') }}
            </button>
          </div>
          <div class="flex flex-col items-center gap-1.5">
            <div v-if="form.footer_text" class="text-xs text-zinc-500 text-center break-words max-w-xs">
              {{ form.footer_text }}
            </div>
            <div v-if="!form.hide_powered_by" class="inline-flex items-center gap-1.5 text-xs text-zinc-500">
              <LogoMark class="h-4 w-4" />
              <span>{{ t('branding.poweredByLine') }}</span>
            </div>
          </div>
        </div>
        <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('branding.previewHelp') }}</p>
      </div>
    </div>
  </div>
</template>
