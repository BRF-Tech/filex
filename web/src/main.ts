import { createApp } from 'vue';
import { createPinia } from 'pinia';

import App from './App.vue';
import router from './router';
import { i18n, applyStoredLocale } from './i18n';
import { applyStoredTheme } from './lib/theme';
import { useToastStore } from './stores/toast';
import { installAxiosInterceptors } from './api/client';

import './styles/main.css';

// Apply theme + locale before mount so we never flash the wrong palette.
applyStoredTheme();
applyStoredLocale();

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
