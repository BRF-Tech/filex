<script setup lang="ts">
/* wiring:e1 — Branding settings page: identity fields for the public
   share/drop/PIN pages + the login screen, with a live preview card. */
import { computed, onBeforeUnmount, onMounted, reactive, ref, watchEffect } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, Palette, RotateCcw, Upload } from 'lucide-vue-next';

import { useSettingsStore } from '@/stores/settings';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatBytes } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Spinner from '@/components/ui/Spinner.vue';
import { PublicLinkPreview } from '@brftech/filex-core';
import { effectiveTheme } from '@/lib/theme';

const LOGO_MAX_BYTES = 256 * 1024;

const { t, locale } = useI18n();
const settings = useSettingsStore();
const toast = useToastStore();

const form = reactive({
  name: '',
  logo_url: '',
  accent: '',
  footer_text: '',
  hide_powered_by: false,
  sso_label: '',
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
  form.sso_label = (d['branding.sso_label'] as string) ?? '';
});

const accentValid = computed(() => /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(form.accent));
// gorunum:v1 — the stand-in when the operator has set no accent of their
// own is the PRODUCT's accent (#2f6ceb, `--fe-primary` light), not the old
// indigo. The placeholder below and the hex quoted by
// `settings.accentInvalid` are the same value on purpose: an example that
// disagrees with the preview teaches the operator the wrong colour.
const previewAccent = computed(() => (accentValid.value ? form.accent : '#2f6ceb'));

/** The preview in the light/dark this panel is in (the public page follows
 *  the visitor's own choice, then the instance default, then the system). */
const previewTheme = ref<'light' | 'dark'>(effectiveTheme());
let themeObserver: MutationObserver | null = null;
onMounted(() => {
  themeObserver = new MutationObserver(() => {
    previewTheme.value = document.documentElement.classList.contains('dark') ? 'dark' : 'light';
  });
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
});
onBeforeUnmount(() => themeObserver?.disconnect());

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
      'branding.sso_label': form.sso_label.trim(),
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
      'branding.sso_label': '',
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
            :placeholder="t('branding.logoPlaceholder')"
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
                placeholder="#2f6ceb"
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

        <!-- issue #28 — the SSO button's own label on the sign-in page. -->
        <Input
          :model-value="form.sso_label"
          :label="t('branding.ssoLabel')"
          :hint="t('branding.ssoLabelHelp')"
          :placeholder="t('login.oidc')"
          data-testid="branding-sso-label"
          @update:model-value="(v) => (form.sso_label = v as string)"
        />

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

      <!-- ── Live preview: the REAL public page (PublicLinkPreview) ── -->
      <!-- ⚠ It was a hand-drawn mock — a centred card, an icon and a
           full-width button — of a page that is left-aligned and plain
           (release-candidate sweep, 2026-09-21, QA #25). It is now the public
           shell and share body `/s/<token>` mounts, fed the unsaved values. -->
      <div class="card card-body space-y-3">
        <h2 class="text-sm font-semibold text-zinc-500 dark:text-zinc-400 uppercase tracking-wide">
          {{ t('branding.preview') }}
        </h2>
        <PublicLinkPreview
          :brand-name="form.name"
          :logo-url="form.logo_url"
          :accent="accentValid ? form.accent : ''"
          :footer-text="form.footer_text"
          :hide-powered-by="form.hide_powered_by"
          :locale="locale"
          :theme="previewTheme"
          :sample="{ name: t('branding.previewFileName'), size: 1_240_000, mime: 'application/pdf' }"
        />
        <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('branding.previewHelp') }}</p>
      </div>
    </div>
  </div>
</template>
