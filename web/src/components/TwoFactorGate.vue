<script setup lang="ts">
/**
 * A permission rule requires two-factor authentication and this account has
 * not enrolled (backend internal/perm, Require 2FA). The server answers every
 * call but enrolment with 403 "2fa_required" until it does, so the app says
 * so over everything, and offers the one thing that helps: the Security
 * section of the account dialog, where TOTP is set up.
 */
import { computed, defineAsyncComponent, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { ShieldAlert } from 'lucide-vue-next';

import { useAuthStore } from '@/stores/auth';
import Button from '@/components/ui/Button.vue';

const UserSettingsModal = defineAsyncComponent(() => import('./UserSettingsModal.vue'));

const { t } = useI18n();
const auth = useAuthStore();
const showSettings = ref(false);

// Enrolling sets user.totp_enabled (UserSettingsModal.verifyTotp), which is
// what takes the gate down at once; /me is then read again for the rest.
const blocked = computed(() => auth.twoFactorRequired && auth.user?.totp_enabled !== true);

watch(showSettings, (open) => {
  if (!open && auth.twoFactorRequired && auth.user?.totp_enabled) void auth.fetchMe();
});
</script>

<template>
  <div
    v-if="blocked"
    class="fixed inset-0 z-[90] flex items-center justify-center bg-zinc-950/60 p-4"
    role="alertdialog"
    aria-modal="true"
    :aria-label="t('permissions.twoFactor.title')"
    data-testid="two-factor-gate"
  >
    <div class="card card-body max-w-md space-y-3 text-center">
      <ShieldAlert class="mx-auto h-8 w-8 text-amber-500" />
      <h2 class="text-lg font-semibold">{{ t('permissions.twoFactor.title') }}</h2>
      <p class="text-sm text-zinc-600 dark:text-zinc-300">{{ t('permissions.twoFactor.body') }}</p>
      <Button data-testid="two-factor-gate-setup" @click="showSettings = true">{{ t('permissions.twoFactor.action') }}</Button>
    </div>
    <UserSettingsModal v-if="showSettings" v-model="showSettings" initial-section="security" />
  </div>
</template>
