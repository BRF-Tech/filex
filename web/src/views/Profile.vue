<script setup lang="ts">
import { computed, ref, watchEffect } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, ShieldCheck, ShieldOff } from 'lucide-vue-next';

import { AuthApi } from '@/api/auth';
import { useAuthStore } from '@/stores/auth';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { setStoredLocale, type Locale } from '@/i18n';
import { downscaleImageToDataURL } from '@/lib/image';

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
// The short login name. It is what the connection protocols use (SFTP, FTPS,
// and the S3/NFS credential pages), because an `@` in a login does not survive
// an rclone or WinSCP config file. Sign-in accepts either this or the e-mail.
const username = ref('');
const displayName = ref('');
const userLocale = ref<Locale>('en');
const timezone = ref('Europe/Istanbul');
const savingProfile = ref(false);

// ── profile picture ──
// Stored on the account, which is what makes it show up everywhere: the file
// explorer's collaboration strip draws it for this user on every client of the
// account — this browser session, the desktop app, and any API key minted under
// it — instead of the initials it fell back to before.
const avatarUrl = ref('');
const avatarError = ref('');
const avatarInput = ref<HTMLInputElement | null>(null);
const AVATAR_MAX_PX = 160;
// Mirrors the server's cap (handlers.avatarMaxBytes) so a picture is resized to
// fit here rather than 400-ing after the fact.
const AVATAR_MAX_BYTES = 48 * 1024;

const avatarInitial = computed(() =>
  (displayName.value || email.value || '?').trim().charAt(0).toUpperCase(),
);

async function onAvatarFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0];
  if (!file) return;
  avatarError.value = '';
  if (!file.type.startsWith('image/')) {
    avatarError.value = t('profile.avatar.notImage');
    return;
  }
  try {
    avatarUrl.value = await downscaleImageToDataURL(file, {
      maxPx: AVATAR_MAX_PX,
      maxBytes: AVATAR_MAX_BYTES,
    });
  } catch {
    avatarError.value = t('profile.avatar.failed');
  } finally {
    // Let the same file be picked again after a failure.
    if (avatarInput.value) avatarInput.value.value = '';
  }
}

const currentPassword = ref('');
const newPassword = ref('');
const newPasswordConfirm = ref('');
const savingPassword = ref(false);

const totpEnabled = computed(() => auth.user?.totp_enabled ?? false);
const showTotpEnroll = ref(false);
const showTotpDisable = ref(false);
const totpQr = ref<string | null>(null);
const totpSecret = ref<string | null>(null);
const totpRecoveryCodes = ref<string[]>([]);
const totpRecoveryCodesText = computed(() => totpRecoveryCodes.value.join('\n'));
const totpCode = ref('');
const totpBusy = ref(false);

const localeOptions = [
  { value: 'en', label: 'English' },
  { value: 'tr', label: 'Türkçe' },
];

watchEffect(() => {
  if (auth.user) {
    email.value = auth.user.email;
    username.value = auth.user.username ?? '';
    displayName.value = auth.user.display_name;
    userLocale.value = (auth.user.locale as Locale) || 'en';
    timezone.value = auth.user.timezone ?? 'Europe/Istanbul';
    avatarUrl.value = auth.user.avatar_url ?? '';
  }
});

async function saveProfile() {
  savingProfile.value = true;
  try {
    const u = await AuthApi.updateProfile({
      email: email.value.trim(),
      username: username.value.trim().toLowerCase(),
      display_name: displayName.value.trim(),
      locale: userLocale.value,
      timezone: timezone.value,
      avatar_url: avatarUrl.value,
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
    totpRecoveryCodes.value = res.recovery_codes ?? [];
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
    totpRecoveryCodes.value = [];
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

      <!-- Profile picture: also the face shown in the file explorer's
           collaboration strip, for every client of this account. -->
      <div>
        <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
          {{ t('profile.avatar.label') }}
        </label>
        <div class="flex items-center gap-3">
          <img
            v-if="avatarUrl"
            :src="avatarUrl"
            :alt="t('profile.avatar.label')"
            class="h-14 w-14 rounded-full object-cover border border-zinc-200 dark:border-zinc-700"
          />
          <span
            v-else
            class="flex h-14 w-14 items-center justify-center rounded-full bg-zinc-200 dark:bg-zinc-700 text-lg font-semibold text-zinc-600 dark:text-zinc-200"
            aria-hidden="true"
          >{{ avatarInitial }}</span>
          <Button type="button" variant="outline" size="sm" @click.prevent="avatarInput?.click()">
            {{ t('profile.avatar.upload') }}
          </Button>
          <Button
            v-if="avatarUrl"
            type="button"
            variant="ghost"
            size="sm"
            @click.prevent="avatarUrl = ''"
          >
            {{ t('common.remove') }}
          </Button>
          <input ref="avatarInput" type="file" accept="image/*" class="hidden" @change="onAvatarFile" />
        </div>
        <p class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{{ t('profile.avatar.help') }}</p>
        <p v-if="avatarError" class="mt-1 text-xs text-rose-600 dark:text-rose-400">{{ avatarError }}</p>
      </div>

      <Input v-model="email" type="email" :label="t('common.email')" required />
      <div>
        <Input v-model="username" :label="t('profile.username.label')" autocomplete="username" />
        <p class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{{ t('profile.username.help') }}</p>
      </div>
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
        data-testid="totp-qr"
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
      <!-- Recovery codes. The server generates and stores these on enroll and
           returns them ONCE; if we don't render them here the user has ten
           valid codes they have never seen, and losing the authenticator app
           means losing the account. -->
      <div
        v-if="totpRecoveryCodes.length"
        data-testid="totp-recovery-codes"
        class="mt-4 rounded-md border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 p-3"
      >
        <div class="flex items-start justify-between gap-2">
          <p class="text-xs font-medium text-amber-900 dark:text-amber-200">
            {{ t('profile.totp.recoveryTitle') }}
          </p>
          <CopyButton :value="totpRecoveryCodesText" size="xs" />
        </div>
        <p class="mt-1 text-xs text-amber-800 dark:text-amber-300">
          {{ t('profile.totp.recoveryHint') }}
        </p>
        <ul class="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 font-mono text-xs">
          <li v-for="c in totpRecoveryCodes" :key="c" class="select-all">{{ c }}</li>
        </ul>
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
