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
  onUnauthorized: () => router.push({ name: 'login' }),
  onError: (msg) => {
    const toast = useToastStore();
    toast.error(msg);
  },
});

app.mount('#app');
