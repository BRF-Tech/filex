<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { stashDesktopHandoff } from '@/lib/desktopHandoff';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import {
  Lock,
  Mail,
  Github,
  Sparkles,
  HardDrive,
  Search,
  Share2,
  MonitorSmartphone,
  Blocks,
  ArrowRight,
  KeyRound,
  Eye,
  EyeOff,
  Check,
  Box,
} from 'lucide-vue-next';

import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { AuthApi } from '@/api/auth';
import { BrandingApi, type BrandingConfig } from '@/api/branding'; /* wiring:e1 */
import { accentButtonStyle } from '@/lib/accentButton';

import LogoMark from '@/components/LogoMark.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Checkbox from '@/components/ui/Checkbox.vue';

// gorunum:v1 — the sign-in card is painted from the product's own palette
// (`--fe-*`), and those variables are DECLARED by the core stylesheet. The
// admin bundle does not load it globally: measured on /admin/login before this
// change, `getComputedStyle(document.documentElement).getPropertyValue('--fe-bg')`
// came back as the empty string. Same import Home.vue already carries, for the
// same reason.
import '@brftech/filex-core/style.css';

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
const showPassword = ref(false);
const showSignIn = ref(false);
const localError = ref<string | null>(null);

const oidcEnabled = computed(() => caps.data.auth_drivers.includes('oidc'));
const localEnabled = computed(
  () => caps.data.auth_drivers.length === 0 || caps.data.auth_drivers.includes('local'),
);
const demoMode = computed(() => caps.data.demo_mode === true);
const demoUser = computed(() => caps.data.demo_user || 'demo@demo.com');
// ⚠ Both halves of the credentials come from the server (FILEX_DEMO_USER /
// FILEX_DEMO_PASS). The password used to be hardcoded here, so an operator
// who set FILEX_DEMO_PASS broke the CTA and the printed hint at once — the
// button submitted "demo" against a user whose password was no longer that.
// The fallback keeps installs that never set the variable working.
const demoPass = computed(() => caps.data.demo_pass || 'demo');

// SSO-first mode (FILEX_OIDC_AUTO_REDIRECT): unauthenticated visitors go
// straight to the IdP; the password form hides behind a "sign in with
// password" link (?local=1) for break-glass/admin logins.
const wantLocal = computed(() => route.query.local !== undefined);
const oidcError = computed(() => route.query.error === 'oidc');
const autoRedirect = computed(
  () => caps.data.oidc_auto_redirect === true && oidcEnabled.value && !demoMode.value,
);
// Recovery sign-in (FILEX_AUTH_RECOVERY_LOGIN, on by default): password sign-in
// is off on this server, but the administrator created at installation may
// still use it — for the day the identity provider is down. The form is not
// offered to everybody; it waits behind the same ?local=1 link, and says who it
// is for.
const recoveryLogin = computed(
  () => caps.data.auth_recovery_login === true && !localEnabled.value && !demoMode.value,
);
const showLocalForm = computed(
  () =>
    (localEnabled.value && (!autoRedirect.value || wantLocal.value)) ||
    (recoveryLogin.value && wantLocal.value),
);
// The link to the password form: offered on an SSO-first page, and on a page
// where only recovery can use it — one link, labelled for whichever it is.
const showPasswordLink = computed(
  () => !wantLocal.value && ((localEnabled.value && autoRedirect.value) || recoveryLogin.value),
);
const redirecting = ref(false);

/* wiring:e1 — settings-driven branding on the login screen: custom logo
   replaces the LogoMark, the display name becomes the wordmark beside it, and
   the accent colors the primary CTA (inline style — the token palette is the
   product's, the accent is the operator's). Fetch is public and best-effort:
   default look on failure. */
const branding = ref<BrandingConfig | null>(null);
/* issue #29 — the accent-filled button is designed per theme: its label is
   picked from the accent, and it draws an edge whenever the fill does not
   stand out from the card of the theme it is shown in (lib/accentButton).
   The theme is the `dark` class lib/theme.ts toggles on <html>; a theme switch
   in another tab or the OS repaints the button without a reload. */
const isDark = ref(typeof document !== 'undefined' && document.documentElement.classList.contains('dark'));
let themeObserver: MutationObserver | null = null;
if (typeof MutationObserver !== 'undefined' && typeof document !== 'undefined') {
  themeObserver = new MutationObserver(() => {
    isDark.value = document.documentElement.classList.contains('dark');
  });
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
}
onBeforeUnmount(() => themeObserver?.disconnect());
const accentStyle = computed(() => accentButtonStyle(branding.value?.accent, isDark.value));
/* issue #28 — the operator's own label for the SSO button; the translated
   default otherwise. */
const ssoLabel = computed(() => branding.value?.sso_label?.trim() || t('login.oidc'));
const wordmark = computed(() => branding.value?.name?.trim() || 'filex');
async function fetchBranding() {
  try {
    branding.value = await BrandingApi.get();
  } catch {
    branding.value = null;
  }
}

onMounted(async () => {
  void fetchBranding(); /* wiring:e1 */
  // Desktop hand-off: remember it NOW. The OIDC round-trip wipes the query
  // string, so reading these later would only ever work for password logins —
  // i.e. exactly the case the desktop flow does not need.
  stashDesktopHandoff(route.query.desktop_state, route.query.desktop_challenge);
  if (!caps.loaded) await caps.fetch();
  // Loop guards: never auto-redirect when the visitor explicitly asked for
  // the password form (?local=1), when the IdP round-trip just failed
  // (?error=... — redirecting again would loop), or when the tenant is
  // locked out (?maintenance=1). The OIDC callback itself is a backend
  // route, so the SPA never mounts on it.
  if (
    autoRedirect.value &&
    !wantLocal.value &&
    route.query.error === undefined &&
    route.query.maintenance === undefined
  ) {
    redirecting.value = true;
    startOidc();
  }
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
    password: demoPass.value,
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
  <!-- The zinc ground belongs to the demo landing only: the sign-in page
       paints its own from the fe-bg-elev token. -->
  <div class="min-h-screen" :class="demoMode ? 'bg-zinc-50 dark:bg-zinc-950' : ''">
    <!-- ⚠ A language switcher and a theme toggle used to float in the top-right
         corner here. Both were removed on the owner's instruction
         (2026-09-12): language and theme are profile settings, like the
         palette and the density, and a signed-out visitor has no profile to
         open — so the page decides for itself and gets it right.

         Measured rather than assumed, in both directions:
           • language — `getStoredLocale()` honours a previously stored
             `filex.locale` and otherwise follows `navigator.language`, so a
             Turkish browser lands on a Turkish form;
           • theme — `getStoredTheme()` returns 'auto' when nothing is stored
             and `applyStoredTheme()` resolves that through
             `prefers-color-scheme`, so an OS in dark mode gets a dark sign-in
             page. A stored choice still outranks the OS, both ways round.
         The only place either one is chosen is the user-settings panel. -->
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
          {{ t('demo.creds', { email: demoUser, password: demoPass }) }}
        </p>
        <p class="mt-1 text-xs text-zinc-500 dark:text-zinc-500">
          {{ t('demo.sandbox') }}
        </p>
      </div>

      <!-- Feature highlight grid. Six cards is a shop window, not an inventory:
           the areas that do not fit are named in the "and also" line under the
           grid rather than dropped, because a visitor who cannot see a feature
           listed assumes it does not exist. Keep both in step with the root
           README - this page is one of the surfaces the release documentation
           audit covers (docs/CONTRIBUTING.md, Release process step 3). -->
      <div class="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <HardDrive class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.storageTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.storageBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <Search class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.searchTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.searchBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <Share2 class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.shareTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.shareBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <MonitorSmartphone class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.desktopTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.desktopBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <Blocks class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.embedTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{{ t('demo.features.embedBody') }}</p>
        </div>
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 p-5">
          <Github class="h-6 w-6 text-brand-600 dark:text-brand-400" />
          <h3 class="mt-2 font-semibold text-zinc-900 dark:text-zinc-100">{{ t('demo.features.openTitle') }}</h3>
          <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">
            {{ t('demo.features.openBody') }}
            <a class="text-brand-600 dark:text-brand-400 hover:underline" href="https://github.com/BRF-Tech/filex" target="_blank" rel="noopener">github.com/BRF-Tech/filex</a>
          </p>
        </div>
      </div>

      <p class="mt-6 text-center text-xs leading-relaxed text-zinc-500 dark:text-zinc-500">
        {{ t('demo.alsoIncluded') }}
      </p>

      <!-- Optional sign-in form (hidden behind toggle) -->
      <div v-if="showSignIn" class="mx-auto mt-10 w-full max-w-md card p-6">
        <div class="flex flex-col items-center gap-2 mb-4">
          <h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{{ t('login.title') }}</h2>
          <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('demo.adminHint') }}</p>
        </div>
        <form v-if="localEnabled" class="space-y-3" @submit.prevent="submit">
          <Input v-model="email" type="text" autocomplete="username" required :label="t('login.identifier')" name="email" />
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
          {{ ssoLabel }}
        </Button>
      </div>

      <p class="mt-10 text-center text-xs text-zinc-500 dark:text-zinc-500 inline-flex items-center justify-center gap-1 w-full">
        <Mail class="h-3 w-3" /> filex {{ caps.data.version }}
      </p>
    </div>

    <!-- ─────────── Standard sign-in (non-demo) ─────────── -->
    <!-- gorunum:v1 — the card, its type scale, the filled fields and the
         decorative corners follow the reference shell, painted entirely from
         the fe-* tokens: no Tailwind palette name and no raw hex on this page.

         ⚠ The page keeps a deep bottom padding rather than sitting dead
         centre. The PWA / desktop banner is fixed to the bottom centre of the
         viewport and this card's submit button is the only thing in that same
         place: measured at 390x844 with a desktop user agent,
         `document.elementFromPoint` over the button returned the banner's hint
         text, so the click never reached the button. The card is centred in
         what is left, so the padding lifts it clear rather than adding visible
         whitespace. Guarded by web/cypress/e2e/91-install-banner-login.cy.ts. -->
    <div v-else class="lg">
      <!-- Decorative corners. A fixed, clipped layer: on a short viewport the
           card is taller than the screen and the page has to scroll, and
           `overflow:hidden` on the scrolling element would have cut the card
           off instead of the shapes. -->
      <div class="lg-deco" aria-hidden="true">
        <span class="lg-deco__a" />
        <span class="lg-deco__b" />
      </div>

      <div class="lg-col">
        <div class="lg-card" data-install-clear>
          <div class="lg-brand">
            <!-- wiring:e1 — branded logo (a custom logo replaces the mark) -->
            <img v-if="branding?.logo_url" :src="branding.logo_url" alt="" class="lg-brand__logo" />
            <LogoMark v-else class="lg-brand__mark" />
            <span class="lg-brand__word">{{ wordmark }}</span>
          </div>

          <h1 class="lg-title">{{ t('login.title') }}</h1>
          <p class="lg-subtitle">{{ t('login.subtitle') }}</p>

          <p v-if="redirecting" class="lg-redirecting">
            <span class="lg-spinner" aria-hidden="true" />
            {{ t('login.redirecting') }}
          </p>

          <template v-else>
            <p v-if="oidcError" role="alert" class="lg-alert">
              {{ t('login.errOidc') }}
            </p>

            <!-- SSO is the primary path whenever OIDC is configured. -->
            <button
              v-if="oidcEnabled"
              type="button"
              class="lg-btn lg-btn--primary lg-btn--sso"
              :style="accentStyle"
              @click="startOidc"
            >
              <KeyRound class="lg-i18" aria-hidden="true" />
              {{ ssoLabel }}
            </button>

            <div v-if="showLocalForm && oidcEnabled" class="lg-or">
              <span class="lg-or__line" />
              <span class="lg-or__word">{{ t('login.or') }}</span>
              <span class="lg-or__line" />
            </div>

            <p v-if="recoveryLogin && showLocalForm" role="note" class="lg-subtitle" data-testid="login-recovery-note">
              {{ t('login.recoveryNote') }}
            </p>

            <form v-if="showLocalForm" class="lg-form" @submit.prevent="submit">
              <label class="lg-label" for="email">{{ t('login.identifier') }}</label>
              <label class="lg-field lg-field--text" for="email">
                <Mail class="lg-field__icon" aria-hidden="true" />
                <input
                  id="email"
                  v-model="email"
                  name="email"
                  type="text"
                  autocomplete="username"
                  required
                  placeholder="admin@local"
                  class="lg-field__input"
                />
              </label>

              <label class="lg-label lg-label--next" for="password">{{ t('common.password') }}</label>
              <div class="lg-field">
                <label class="lg-field__grow" for="password">
                  <Lock class="lg-field__icon" aria-hidden="true" />
                  <input
                    id="password"
                    v-model="password"
                    name="password"
                    :type="showPassword ? 'text' : 'password'"
                    autocomplete="current-password"
                    required
                    class="lg-field__input lg-field__input--pw"
                  />
                </label>
                <!-- ⚠ The accessible name deliberately avoids the word
                     "password": e2e/helpers/auth.ts fills the field with
                     `getByLabel(/password|şifre/i)`, and a second control
                     answering to that name turns every sign-in in the suite
                     into a strict-mode violation. -->
                <button
                  type="button"
                  class="lg-eye"
                  :aria-label="showPassword ? t('login.hideChars') : t('login.showChars')"
                  :aria-pressed="showPassword"
                  @click="showPassword = !showPassword"
                >
                  <EyeOff v-if="showPassword" class="lg-i20" aria-hidden="true" />
                  <Eye v-else class="lg-i20" aria-hidden="true" />
                </button>
              </div>

              <div class="lg-2fa">
                <button type="button" class="lg-link" @click="showTotp = !showTotp">
                  {{ showTotp ? t('login.hide2fa') : t('login.use2fa') }}
                </button>
              </div>

              <template v-if="showTotp">
                <label class="lg-label" for="totp">{{ t('login.totpLabel') }}</label>
                <label class="lg-field lg-field--text" for="totp">
                  <KeyRound class="lg-field__icon" aria-hidden="true" />
                  <input
                    id="totp"
                    v-model="totp"
                    name="totp"
                    type="text"
                    inputmode="numeric"
                    autocomplete="one-time-code"
                    placeholder="123456"
                    class="lg-field__input"
                    aria-describedby="totp-hint"
                  />
                </label>
                <p id="totp-hint" class="lg-hint">{{ t('login.totpHint') }}</p>
              </template>

              <label class="lg-check">
                <button
                  type="button"
                  role="checkbox"
                  :aria-checked="remember"
                  class="lg-check__box"
                  :class="{ 'is-on': remember }"
                  @click="remember = !remember"
                >
                  <Check v-if="remember" class="lg-check__tick" aria-hidden="true" />
                </button>
                <span>{{ t('login.remember') }}</span>
              </label>

              <p v-if="localError" role="alert" class="lg-alert lg-alert--form">
                {{ localError }}
              </p>

              <button
                type="submit"
                class="lg-btn"
                :class="oidcEnabled ? 'lg-btn--outline' : 'lg-btn--primary'"
                :style="oidcEnabled ? undefined : accentStyle"
                :disabled="auth.loading"
                :aria-busy="auth.loading || undefined"
              >
                <span v-if="auth.loading" class="lg-spinner" aria-hidden="true" />
                <Lock v-else class="lg-i18" aria-hidden="true" />
                {{ t('login.submit') }}
              </button>
            </form>

            <div v-if="showPasswordLink" class="lg-localhint">
              <router-link
                :to="{ query: { ...route.query, local: '1' } }"
                class="lg-localhint__link"
                data-testid="login-password-link"
              >
                {{ recoveryLogin ? t('login.recovery') : t('login.local') }}
              </router-link>
            </div>

            <p v-if="!localEnabled && !oidcEnabled && !recoveryLogin" class="lg-alert lg-alert--form">
              No auth providers enabled. Set <code>AUTH_DRIVERS</code> in your env.
            </p>
          </template>
        </div>

        <p class="lg-version">
          <Box class="lg-i16" aria-hidden="true" /> filex {{ caps.data.version }}
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* ── page ───────────────────────────────────────────────────────────── */
.lg {
  position: relative;
  display: flex;
  min-height: 100vh;
  width: 100%;
  align-items: center;
  justify-content: center;
  /* See the template comment: the bottom padding is the install banner's
     room, not a design choice. `--filex-install-banner-h` is what the banner
     is actually standing on right now (InstallPrompt.vue publishes it, and
     zeroes it when it is dismissed) — so the card sits dead centre, exactly
     like the reference, the moment the banner is gone, and never has to guess
     at a constant that was only ever right on the day it was written.
     The top padding gives way first on a short viewport: the card cannot be
     both centred and clear of a 300px banner on an 800px screen, and being
     clear of it is what lets somebody sign in. */
  padding: 40px 16px calc(var(--filex-install-banner-h, 0px) + 24px);
  font-family: var(--fe-font);
  color: var(--fe-text);
  background: var(--fe-bg-elev);
}
@media (max-height: 960px) {
  .lg {
    padding-top: 12px;
  }
}

.lg-deco {
  position: fixed;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
}
.lg-deco__a,
.lg-deco__b {
  position: absolute;
  display: block;
  background: var(--fe-bg-selected);
  transform: rotate(12deg);
}
.lg-deco__a {
  top: -96px;
  left: -96px;
  height: 288px;
  width: 288px;
  border-radius: 64px;
}
.lg-deco__b {
  right: -96px;
  bottom: -128px;
  height: 384px;
  width: 384px;
  border-radius: 80px;
}

.lg-col {
  position: relative;
  width: 100%;
  max-width: 672px;
}

/* ── card ───────────────────────────────────────────────────────────── */
.lg-card {
  /* The vertical rhythm collapses on a short viewport. The full card is
     taller than 800px screens leave once the fixed install banner has its
     288px at the bottom, and a sign-in button under a banner is worse than a
     tighter card. Measured, not guessed: at 1280x800 the banner's own box
     starts at y=512 and the full-size card would put the submit's centre at
     570. */
  --lg-pad-y: 48px;
  --lg-mark: 64px;
  --lg-word: 44px;
  --lg-title: 32px;
  --lg-gap: 32px;
  --lg-field-h: 60px;

  padding: var(--lg-pad-y) 32px;
  border-radius: 20px;
  background: var(--fe-bg);
  box-shadow: var(--fe-shadow);
}
@media (min-width: 640px) {
  .lg-card {
    padding-left: 88px;
    padding-right: 88px;
  }
}
/* The short-viewport compaction lives in the unscoped block at the bottom of
   this file — it has to reach an ancestor (<html>), which a scoped rule
   cannot. */

.lg-brand {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 16px;
}
.lg-brand__mark,
.lg-brand__logo {
  height: var(--lg-mark);
  width: var(--lg-mark);
  flex-shrink: 0;
}
.lg-brand__logo {
  width: auto;
  max-width: 220px;
  object-fit: contain;
}
.lg-brand__word {
  min-width: 0;
  font-size: var(--lg-word);
  font-weight: 700;
  line-height: 1;
  letter-spacing: -0.025em;
  color: var(--fe-text);
  overflow-wrap: anywhere;
}

.lg-title {
  margin: var(--lg-gap) 0 0;
  text-align: center;
  font-size: var(--lg-title);
  font-weight: 700;
  line-height: 1;
  letter-spacing: -0.025em;
  color: var(--fe-text);
}
.lg-subtitle {
  margin: 12px 0 0;
  text-align: center;
  font-size: var(--fe-text-md);
  color: var(--fe-text-muted);
}

.lg-redirecting {
  margin: var(--lg-gap) 0 0;
  text-align: center;
  font-size: var(--fe-text-md);
  color: var(--fe-text-muted);
}
.lg-spinner {
  display: inline-block;
  height: 14px;
  width: 14px;
  margin-right: 6px;
  vertical-align: -2px;
  border: 2px solid currentColor;
  border-right-color: transparent;
  border-radius: 999px;
  opacity: 0.6;
  animation: lg-spin 0.7s linear infinite;
}
@keyframes lg-spin {
  to {
    transform: rotate(360deg);
  }
}

/* ── form ───────────────────────────────────────────────────────────── */
.lg-form {
  margin-top: var(--lg-gap);
}

.lg-label {
  display: block;
  font-size: var(--fe-text-xs);
  font-weight: 600;
  color: var(--fe-text-muted);
}
.lg-label--next {
  margin-top: 24px;
}

.lg-field {
  margin-top: 8px;
  display: flex;
  align-items: center;
  height: var(--lg-field-h);
  padding: 0 20px;
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-md);
  background: var(--fe-bg-hover);
}
.lg-field--text {
  cursor: text;
}
.lg-field:focus-within {
  border-color: var(--fe-primary);
  box-shadow: 0 0 0 1px var(--fe-primary);
}
.lg-field__grow {
  display: flex;
  align-items: center;
  height: 100%;
  min-width: 0;
  flex: 1;
  cursor: text;
}
.lg-field__icon {
  height: 20px;
  width: 20px;
  flex-shrink: 0;
  color: var(--fe-text-muted);
}
.lg-field__input {
  margin-left: 16px;
  min-width: 0;
  flex: 1;
  border: 0;
  background: transparent;
  font-family: inherit;
  font-size: var(--fe-text-md);
  color: var(--fe-text);
}
.lg-field__input--pw {
  margin-right: 16px;
}
.lg-field__input:focus {
  outline: none;
}
.lg-field__input::placeholder {
  color: var(--fe-text-muted);
}

.lg-eye {
  display: flex;
  flex-shrink: 0;
  padding: 0;
  border: 0;
  background: none;
  color: var(--fe-text-muted);
  cursor: pointer;
}
.lg-eye:hover {
  color: var(--fe-text);
}

.lg-2fa {
  margin-top: 12px;
  text-align: right;
}
.lg-link {
  padding: 0;
  border: 0;
  background: none;
  font-family: inherit;
  font-size: var(--fe-text-md);
  color: var(--fe-primary);
  cursor: pointer;
}
.lg-link:hover {
  text-decoration: underline;
}

.lg-hint {
  margin: 6px 0 0;
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}

.lg-check {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 24px;
  font-size: var(--fe-text-md);
  color: var(--fe-text);
  cursor: pointer;
}
.lg-check__box {
  position: relative;
  display: inline-flex;
  height: 20px;
  width: 20px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border: 1.5px solid var(--fe-border-strong);
  border-radius: 5px;
  background: var(--fe-bg);
  cursor: pointer;
}
.lg-check__box.is-on {
  border-color: var(--fe-primary);
  background: var(--fe-primary);
  color: var(--fe-text-on-primary);
}
.lg-check__tick {
  height: 14px;
  width: 14px;
  stroke-width: 3;
}

.lg-alert {
  margin: 20px 0 0;
  padding: 10px 12px;
  border: 1px solid var(--fe-danger);
  border-radius: var(--fe-radius-sm);
  background: var(--fe-bg-elev);
  font-size: var(--fe-text-md);
  color: var(--fe-danger);
}
.lg-alert--form {
  margin: 24px 0 0;
}
.lg-alert code {
  font-family: var(--fe-font-mono);
}

/* ── buttons ────────────────────────────────────────────────────────── */
.lg-btn {
  display: inline-flex;
  width: 100%;
  height: var(--fe-h-lg);
  align-items: center;
  justify-content: center;
  gap: 8px;
  margin-top: 24px;
  padding: 0 16px;
  border: 1px solid transparent;
  border-radius: var(--fe-radius-md);
  font-family: inherit;
  font-size: var(--fe-text-md);
  font-weight: 500;
  line-height: 1;
  cursor: pointer;
  user-select: none;
}
.lg-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.lg-btn--sso {
  margin-top: var(--lg-gap);
}
.lg-btn--primary {
  background: var(--fe-primary);
  border-color: var(--fe-primary);
  color: var(--fe-text-on-primary);
}
.lg-btn--primary:hover:not(:disabled) {
  background: var(--fe-primary-hover);
  border-color: var(--fe-primary-hover);
}
.lg-btn--outline {
  background: var(--fe-bg);
  border-color: var(--fe-border-strong);
  color: var(--fe-text);
}
.lg-btn--outline:hover:not(:disabled) {
  background: var(--fe-bg-hover);
}

.lg-or {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 20px 0;
}
.lg-or__line {
  flex: 1;
  border-top: 1px solid var(--fe-border);
}
.lg-or__word {
  font-size: var(--fe-text-xs);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--fe-text-muted);
}

.lg-localhint {
  margin-top: 20px;
  text-align: center;
}
.lg-localhint__link {
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
  text-decoration: none;
}
.lg-localhint__link:hover {
  color: var(--fe-text);
  text-decoration: underline;
}

/* ── version line ───────────────────────────────────────────────────── */
.lg-version {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  margin: 24px 0 0;
  font-size: var(--fe-text-xs);
  color: var(--fe-text-muted);
}

.lg-i16 {
  height: 16px;
  width: 16px;
}
.lg-i18 {
  height: 18px;
  width: 18px;
}
.lg-i20 {
  height: 20px;
  width: 20px;
}

/* The admin shell paints every focus ring in its own brand blue
   (styles/main.css `@layer base`). On this page the accent is the product's
   primary, so the ring follows it. */
.lg :focus-visible {
  outline: 2px solid var(--fe-primary);
  outline-offset: 2px;
}
</style>

<!-- ⚠ Unscoped on purpose, and this is the measured reason: Vue's scoped
     transform compiles `:global(html.x) .lg-card` down to `html.x` alone — it
     drops the descendant half. The rule then set the variables on <html>, where
     `.lg-card`'s own declarations outranked them, and the card never changed
     size. Measured on the dev server: the submit button stayed at y=552 at
     1280x800 and the banner still covered it. The class name is the
     component's own prefix, so an unscoped rule here reaches nothing else. -->
<style>
/* Only when the install banner is actually standing at the bottom of the
   viewport (InstallPrompt.vue puts the class on <html> and takes it off when
   the banner is dismissed). The card is the reference's size everywhere else,
   including on a phone once the banner is gone — the compaction is the price
   of the banner, not of the small screen.

   Measured at 1280x800: the banner's box starts at y=500, and the full-size
   card puts the submit button's centre at y=572. */
@media (max-height: 880px) {
  html.filex-install-banner .lg-card {
    --lg-pad-y: 28px;
    --lg-mark: 44px;
    --lg-word: 30px;
    --lg-title: 24px;
    --lg-gap: 18px;
    --lg-field-h: 48px;
  }
}
</style>
