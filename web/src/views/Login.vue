<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import {
  Lock,
  Mail,
  Github,
  Sparkles,
  ShieldCheck,
  GitBranch,
  Bell,
  ListChecks,
  FolderTree,
  ArrowRight,
} from 'lucide-vue-next';

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
const showSignIn = ref(false);
const localError = ref<string | null>(null);

const oidcEnabled = computed(() => caps.data.auth_drivers.includes('oidc'));
const localEnabled = computed(
  () => caps.data.auth_drivers.length === 0 || caps.data.auth_drivers.includes('local'),
);
const demoMode = computed(() => caps.data.demo_mode === true);
const demoUser = computed(() => caps.data.demo_user || 'demo@demo.com');

onMounted(async () => {
  if (!caps.loaded) await caps.fetch();
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

async function openDemo() {
  // Auto-submit the documented demo creds, then hand off to the
  // standalone /explore page (FileExplorer Web Component) — NOT
  // the admin dashboard. Demo visitors should see the actual file
  // browser, not the operator panel.
  localError.value = null;
  const ok = await auth.login({
    email: demoUser.value,
    password: 'demo',
    remember: true,
  });
  if (ok) {
    router.push({ name: 'explore' });
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
  <div class="min-h-screen bg-zinc-50 dark:bg-zinc-950">
    <div class="absolute right-4 top-4 flex items-center gap-1.5 z-10">
      <DarkModeToggle />
      <LocaleSwitcher />
    </div>

    <!-- ─────────── Demo mode landing ─────────── -->
    <div v-if="demoMode" class="mx-auto max-w-5xl px-4 py-10 sm:py-16">
      <div class="text-center">
        <LogoMark class="mx-auto h-14 w-14" />
        <div class="mt-3 inline-flex items-center gap-1.5 rounded-full bg-brand-50 dark:bg-brand-500/10 px-3 py-1 text-xs font-medium text-brand-700 dark:text-brand-300">
          <Sparkles class="h-3.5 w-3.5" />
          {{ t('demo.badge') }}
        </div>
        <h1 class="mt-4 text-3xl sm:text-4xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">
          {{ t('demo.title') }}
        </h1>
        <p class="mt-3 max-w-2xl mx-auto text-sm text-zinc-600 dark:text-zinc-400">
          {{ t('demo.subtitle') }}
        </p>

        <div class="mt-6 flex flex-col sm:flex-row items-center justify-center gap-3">
          <Button size="lg" variant="primary" @click="openDemo" :loading="auth.loading">
            <Sparkles class="h-4 w-4" />
            {{ t('demo.openCta') }}
            <ArrowRight class="h-4 w-4" />
          </Button>
          <button
            type="button"
            class="text-sm text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 underline-offset-2 hover:underline"
            @click="showSignIn = !showSignIn"
          >
            {{ showSignIn ? t('demo.hideSignIn') : t('demo.showSignIn') }}
          </button>
        </div>
        <p class="mt-2 text-xs text-zinc-500 dark:text-zinc-500">
          {{ t('demo.creds', { email: demoUser, password: 'demo' }) }}
        </p>
      </div>

      <!-- Feature highlight grid -->
      <div class="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <FolderTree class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.storageTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.storageBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <GitBranch class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.replicaTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.replicaBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <ListChecks class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.queueTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.queueBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <Bell class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.notifyTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.notifyBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <ShieldCheck class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.authTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.authBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <Github class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.openTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">
            {{ t('demo.features.openBody') }}
            <a class="text-brand-600 dark:text-brand-400 hover:underline" href="https://github.com/brf-tech/filex" target="_blank" rel="noopener">github.com/brf-tech/filex</a>
          </p>
        </div>
      </div>

      <!-- Optional sign-in form (hidden behind toggle) -->
      <div v-if="showSignIn" class="mx-auto mt-10 w-full max-w-md card p-6">
        <div class="flex flex-col items-center gap-2 mb-4">
          <h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{{ t('login.title') }}</h2>
          <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('demo.adminHint') }}</p>
        </div>
        <form v-if="localEnabled" class="space-y-3" @submit.prevent="submit">
          <Input v-model="email" type="email" autocomplete="username" required :label="t('common.email')" name="email" />
          <Input v-model="password" type="password" autocomplete="current-password" required :label="t('common.password')" name="password" />
          <Checkbox v-model="remember" :label="t('login.remember')" />
          <p v-if="localError" class="text-sm text-rose-600 dark:text-rose-400 bg-rose-50 dark:bg-rose-500/10 rounded-md px-3 py-2">
            {{ localError }}
          </p>
          <Button type="submit" :loading="auth.loading" block>
            <Lock class="h-4 w-4" />
            {{ t('login.submit') }}
          </Button>
        </form>
        <Button v-if="oidcEnabled" variant="outline" block class="mt-3" @click="startOidc">
          <Github class="h-4 w-4" />
          {{ t('login.oidc') }}
        </Button>
      </div>

      <p class="mt-10 text-center text-xs text-zinc-500 dark:text-zinc-500 inline-flex items-center justify-center gap-1 w-full">
        <Mail class="h-3 w-3" /> filex {{ caps.data.version }}
      </p>
    </div>

    <!-- ─────────── Standard sign-in (non-demo) ─────────── -->
    <div v-else class="min-h-screen flex flex-col items-center justify-center px-4">
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
          <Input v-model="email" type="email" autocomplete="username" required :label="t('common.email')" placeholder="admin@local" name="email" />
          <Input v-model="password" type="password" autocomplete="current-password" required :label="t('common.password')" name="password" />

          <div class="text-right">
            <button
              type="button"
              class="text-xs text-brand-600 hover:underline dark:text-brand-400"
              @click="showTotp = !showTotp"
            >
              {{ showTotp ? t('common.hide') : t('common.show') }} 2FA
            </button>
          </div>

          <Input v-if="showTotp" v-model="totp" type="text" inputmode="numeric" autocomplete="one-time-code" :hint="t('login.totpHint')" name="totp" placeholder="123456" />

          <Checkbox v-model="remember" :label="t('login.remember')" />

          <p v-if="localError" class="text-sm text-rose-600 dark:text-rose-400 bg-rose-50 dark:bg-rose-500/10 rounded-md px-3 py-2">
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

        <p v-if="!localEnabled && !oidcEnabled" class="mt-4 text-center text-sm text-rose-600 dark:text-rose-400">
          No auth providers enabled. Set <code class="font-mono">AUTH_DRIVERS</code> in your env.
        </p>
      </div>

      <p class="mt-4 text-xs text-zinc-500 dark:text-zinc-400 inline-flex items-center gap-1">
        <Mail class="h-3 w-3" /> filex {{ caps.data.version }}
      </p>
    </div>
  </div>
</template>
