<script setup lang="ts">
import { computed, ref, watchEffect } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, ShieldCheck, ShieldOff } from 'lucide-vue-next';

import { AuthApi } from '@/api/auth';
import { useAuthStore } from '@/stores/auth';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { setStoredLocale, type Locale } from '@/i18n';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Modal from '@/components/ui/Modal.vue';
import CopyButton from '@/components/ui/CopyButton.vue';
import Badge from '@/components/ui/Badge.vue';

const { t, locale } = useI18n();
const auth = useAuthStore();
const toast = useToastStore();

const email = ref('');
const displayName = ref('');
const userLocale = ref<Locale>('en');
const timezone = ref('Europe/Istanbul');
const savingProfile = ref(false);

const currentPassword = ref('');
const newPassword = ref('');
const newPasswordConfirm = ref('');
const savingPassword = ref(false);

const totpEnabled = computed(() => auth.user?.totp_enabled ?? false);
const showTotpEnroll = ref(false);
const showTotpDisable = ref(false);
const totpQr = ref<string | null>(null);
const totpSecret = ref<string | null>(null);
const totpCode = ref('');
const totpBusy = ref(false);

const localeOptions = [
  { value: 'en', label: 'English' },
  { value: 'tr', label: 'Türkçe' },
];

watchEffect(() => {
  if (auth.user) {
    email.value = auth.user.email;
    displayName.value = auth.user.display_name;
    userLocale.value = (auth.user.locale as Locale) || 'en';
    timezone.value = auth.user.timezone ?? 'Europe/Istanbul';
  }
});

async function saveProfile() {
  savingProfile.value = true;
  try {
    const u = await AuthApi.updateProfile({
      email: email.value.trim(),
      display_name: displayName.value.trim(),
      locale: userLocale.value,
      timezone: timezone.value,
    });
    auth.user = u;
    setStoredLocale(userLocale.value);
    locale.value = userLocale.value;
    toast.success(t('profile.saved'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingProfile.value = false;
  }
}

async function changePassword() {
  if (newPassword.value !== newPasswordConfirm.value) {
    toast.warn(t('errors.validationFailed'));
    return;
  }
  savingPassword.value = true;
  try {
    await AuthApi.changePassword(currentPassword.value, newPassword.value);
    currentPassword.value = '';
    newPassword.value = '';
    newPasswordConfirm.value = '';
    toast.success(t('profile.passwordChanged'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingPassword.value = false;
  }
}

async function startTotp() {
  totpBusy.value = true;
  try {
    const res = await AuthApi.enrollTotp();
    totpSecret.value = res.secret;
    totpQr.value = res.qr_svg;
    showTotpEnroll.value = true;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    totpBusy.value = false;
  }
}

async function verifyTotp() {
  totpBusy.value = true;
  try {
    await AuthApi.verifyTotp(totpCode.value);
    if (auth.user) auth.user.totp_enabled = true;
    showTotpEnroll.value = false;
    totpCode.value = '';
    totpQr.value = null;
    totpSecret.value = null;
    toast.success('2FA enabled');
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    totpBusy.value = false;
  }
}

async function disableTotp() {
  totpBusy.value = true;
  try {
    await AuthApi.disableTotp(totpCode.value);
    if (auth.user) auth.user.totp_enabled = false;
    showTotpDisable.value = false;
    totpCode.value = '';
    toast.success('2FA disabled');
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    totpBusy.value = false;
  }
}
</script>

<template>
  <div class="space-y-6 max-w-2xl">
    <div>
      <h1 class="text-xl font-semibold">{{ t('profile.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('profile.subtitle') }}</p>
    </div>

    <!-- Account -->
    <form class="card card-body space-y-3" @submit.prevent="saveProfile">
      <h2 class="text-sm font-semibold uppercase tracking-wide text-zinc-500">
        {{ t('profile.section.account') }}
      </h2>
      <Input v-model="email" type="email" :label="t('common.email')" required />
      <Input v-model="displayName" :label="t('users.fields.displayName')" required />
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Select
          :model-value="userLocale"
          :options="localeOptions"
          :label="t('profile.locale')"
          @update:model-value="(v) => (userLocale = v as Locale)"
        />
        <Input v-model="timezone" :label="t('profile.timezone')" placeholder="Europe/Istanbul" />
      </div>
      <div class="flex justify-end pt-2">
        <Button type="submit" :loading="savingProfile">
          <Save class="h-4 w-4" />
          {{ t('common.save') }}
        </Button>
      </div>
    </form>

    <!-- Password -->
    <form class="card card-body space-y-3" @submit.prevent="changePassword">
      <h2 class="text-sm font-semibold uppercase tracking-wide text-zinc-500">
        {{ t('profile.section.security') }}
      </h2>
      <Input
        v-model="currentPassword"
        type="password"
        :label="t('common.currentPassword')"
        autocomplete="current-password"
        required
      />
      <Input
        v-model="newPassword"
        type="password"
        :label="t('common.newPassword')"
        autocomplete="new-password"
        required
      />
      <Input
        v-model="newPasswordConfirm"
        type="password"
        :label="`${t('common.newPassword')} (${t('common.confirm')})`"
        autocomplete="new-password"
        required
      />
      <div class="flex justify-end pt-2">
        <Button type="submit" :loading="savingPassword">
          <Save class="h-4 w-4" />
          {{ t('common.save') }}
        </Button>
      </div>
    </form>

    <!-- TOTP -->
    <div class="card card-body space-y-3">
      <div class="flex items-start justify-between gap-3">
        <div>
          <h2 class="text-sm font-semibold uppercase tracking-wide text-zinc-500">
            {{ t('profile.totp.title') }}
          </h2>
          <p class="text-xs text-zinc-500 mt-1">
            <Badge :tone="totpEnabled ? 'emerald' : 'zinc'" dot>
              {{ totpEnabled ? t('profile.totp.enabled') : t('profile.totp.disabled') }}
            </Badge>
          </p>
        </div>
        <div>
          <Button v-if="!totpEnabled" variant="outline" @click="startTotp" :loading="totpBusy">
            <ShieldCheck class="h-4 w-4" />
            {{ t('profile.totp.enable') }}
          </Button>
          <Button v-else variant="outline" @click="showTotpDisable = true">
            <ShieldOff class="h-4 w-4" />
            {{ t('profile.totp.disable') }}
          </Button>
        </div>
      </div>
    </div>

    <!-- Enroll modal -->
    <Modal v-model="showTotpEnroll" :title="t('profile.totp.title')" size="md">
      <p class="text-sm mb-3 text-zinc-600 dark:text-zinc-400">
        {{ t('profile.totp.scanHint') }}
      </p>
      <div
        v-if="totpQr"
        class="flex flex-col items-center gap-3 rounded-md border border-zinc-200 dark:border-zinc-800 bg-white p-4"
        v-html="totpQr"
      />
      <div v-if="totpSecret" class="mt-3 flex items-center gap-2">
        <code
          class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-xs font-mono"
        >
          {{ totpSecret }}
        </code>
        <CopyButton :value="totpSecret" />
      </div>
      <Input
        v-model="totpCode"
        :label="t('profile.totp.code')"
        inputmode="numeric"
        autocomplete="one-time-code"
        class="mt-3"
      />
      <template #footer>
        <Button variant="ghost" @click="showTotpEnroll = false">{{ t('common.cancel') }}</Button>
        <Button :loading="totpBusy" @click="verifyTotp">{{ t('common.confirm') }}</Button>
      </template>
    </Modal>

    <Modal v-model="showTotpDisable" :title="t('profile.totp.disable')" size="sm">
      <Input
        v-model="totpCode"
        :label="t('profile.totp.code')"
        inputmode="numeric"
        autocomplete="one-time-code"
      />
      <template #footer>
        <Button variant="ghost" @click="showTotpDisable = false">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="totpBusy" @click="disableTotp">
          {{ t('common.confirm') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
