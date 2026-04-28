<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Lock, Mail, Github } from 'lucide-vue-next';

import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { AuthApi } from '@/api/auth';

import LogoMark from '@/components/LogoMark.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import LocaleSwitcher from '@/components/LocaleSwitcher.vue';
import DarkModeToggle from '@/components/DarkModeToggle.vue';

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const auth = useAuthStore();
const caps = useCapabilitiesStore();

const email = ref('');
const password = ref('');
const totp = ref('');
const remember = ref(true);
const showTotp = ref(false);
const localError = ref<string | null>(null);

const oidcEnabled = computed(() => caps.data.auth_drivers.includes('oidc'));
const localEnabled = computed(
  () => caps.data.auth_drivers.length === 0 || caps.data.auth_drivers.includes('local'),
);

onMounted(() => {
  if (!caps.loaded) caps.fetch();
});

async function submit() {
  localError.value = null;
  const ok = await auth.login({
    email: email.value.trim(),
    password: password.value,
    remember: remember.value,
    totp: totp.value || undefined,
  });
  if (ok) {
    const redirect = (route.query.redirect as string) || '/';
    router.push(redirect);
  } else {
    localError.value = auth.error ?? t('login.errGeneric');
  }
}

function startOidc() {
  window.location.href = AuthApi.oidcStartUrl(
    'oidc',
    (route.query.redirect as string) || '/admin/',
  );
}
</script>

<template>
  <div
    class="min-h-screen flex flex-col items-center justify-center bg-zinc-50 dark:bg-zinc-950 px-4"
  >
    <div class="absolute right-4 top-4 flex items-center gap-1.5">
      <DarkModeToggle />
      <LocaleSwitcher />
    </div>

    <div class="card w-full max-w-md p-6">
      <div class="flex flex-col items-center gap-2 mb-5">
        <LogoMark class="h-12 w-12" />
        <h1 class="text-xl font-semibold text-zinc-900 dark:text-zinc-100">
          {{ t('login.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400 text-center">
          {{ t('login.subtitle') }}
        </p>
      </div>

      <form v-if="localEnabled" class="space-y-3" @submit.prevent="submit">
        <Input
          v-model="email"
          type="email"
          autocomplete="username"
          required
          :label="t('common.email')"
          placeholder="admin@local"
          name="email"
        />
        <Input
          v-model="password"
          type="password"
          autocomplete="current-password"
          required
          :label="t('common.password')"
          name="password"
        />

        <div class="text-right">
          <button
            type="button"
            class="text-xs text-brand-600 hover:underline dark:text-brand-400"
            @click="showTotp = !showTotp"
          >
            {{ showTotp ? t('common.hide') : t('common.show') }} 2FA
          </button>
        </div>

        <Input
          v-if="showTotp"
          v-model="totp"
          type="text"
          inputmode="numeric"
          autocomplete="one-time-code"
          :hint="t('login.totpHint')"
          name="totp"
          placeholder="123456"
        />

        <Checkbox v-model="remember" :label="t('login.remember')" />

        <p
          v-if="localError"
          class="text-sm text-rose-600 dark:text-rose-400 bg-rose-50 dark:bg-rose-500/10 rounded-md px-3 py-2"
        >
          {{ localError }}
        </p>

        <Button type="submit" :loading="auth.loading" block>
          <Lock class="h-4 w-4" />
          {{ t('login.submit') }}
        </Button>
      </form>

      <div v-if="localEnabled && oidcEnabled" class="my-4 flex items-center gap-2">
        <span class="flex-1 border-t border-zinc-200 dark:border-zinc-800" />
        <span class="text-xs uppercase text-zinc-500">{{ t('login.or') }}</span>
        <span class="flex-1 border-t border-zinc-200 dark:border-zinc-800" />
      </div>

      <Button v-if="oidcEnabled" variant="outline" block @click="startOidc">
        <Github class="h-4 w-4" />
        {{ t('login.oidc') }}
      </Button>

      <p
        v-if="!localEnabled && !oidcEnabled"
        class="mt-4 text-center text-sm text-rose-600 dark:text-rose-400"
      >
        No auth providers enabled. Set <code class="font-mono">AUTH_DRIVERS</code> in your env.
      </p>
    </div>

    <p class="mt-4 text-xs text-zinc-500 dark:text-zinc-400 inline-flex items-center gap-1">
      <Mail class="h-3 w-3" /> filex {{ caps.data.version }}
    </p>
  </div>
</template>
