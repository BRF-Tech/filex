import { createApp } from 'vue';
import { createPinia } from 'pinia';

import App from './App.vue';
import router from './router';
import { i18n, applyStoredLocale } from './i18n';
import { applyStoredTheme } from './lib/theme';
import { loadCustomCss } from './lib/customCss';
import { useToastStore } from './stores/toast';
import { installAxiosInterceptors } from './api/client';
import { initRuntimeConfig } from './api/runtimeConfig';

import './styles/main.css';
// gorunum:v3-shell — the product's look, loaded ONCE for the whole app.
//
// ⚠⚠ It used to be imported per view (Explore, Home), which meant the `--fe-*`
// tokens simply did not exist on any route that mounted neither — measured on
// /login, where `--fe-bg` computed to the empty string, so no token on that
// page could have worked and every rule written against one silently did
// nothing. The shell is not one view's stylesheet any more; the admin chrome,
// the settings modal, the sign-in page and the install banner all paint with
// these tokens.
//
// ⚠ Repeat imports elsewhere are harmless (one module, resolved once) but
// redundant — this is the line that guarantees it, not they.
import '@brftech/filex-core/style.css';

// Pick up any injected runtime config (Electron preload sets the API base +
// token before the bundle boots). No-op in the plain web build. Must run before
// the first request fires from the router guard.
initRuntimeConfig();

// Apply theme + locale before mount so we never flash the wrong palette.
applyStoredTheme();
applyStoredLocale();

// gorunum:v1 — the operator's own stylesheet (admin Settings -> Custom CSS),
// carried on the public /api/branding boot payload. Deliberately NOT awaited:
// blocking the mount on a network round-trip would trade a moment of default
// styling for a moment of blank page. Same trade the branded document title
// already makes.
void loadCustomCss();

const app = createApp(App);
const pinia = createPinia();

app.use(pinia);
app.use(router);
app.use(i18n);

// Wire axios -> router (401 redirect) and toast (network error surfacing)
// after Pinia + Router are attached so stores resolve.
installAxiosInterceptors({
  router,
  onUnauthorized: () => {
    // Preserve the interrupted location so login lands the user back where
    // they were headed. On a cold-load deep link vue-router already carries
    // the #<folder> hash in fullPath; after in-app navigation the explorer
    // writes it via replaceState behind the router's back — append it only
    // in that case or the hash doubles up.
    const current = router.currentRoute.value;
    let redirect = current.fullPath;
    if (!current.hash && window.location.hash) redirect += window.location.hash;
    router.push(
      redirect && redirect !== '/'
        ? { name: 'login', query: { redirect } }
        : { name: 'login' },
    );
  },
  onError: (msg) => {
    const toast = useToastStore();
    toast.error(msg);
  },
});

app.mount('#app');
