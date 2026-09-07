import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { CapabilitiesApi } from '@/api/capabilities';
import type { Capabilities } from '@/api/types';
import { applyServerDefaultLocale } from '@/i18n';

const EMPTY: Capabilities = {
  version: '0.0.0',
  build: 'dev',
  ffmpeg: false,
  imagemagick: false,
  ghostscript: false,
  libreoffice: false,
  onlyoffice_url: null,
  drawio_url: null,
  monaco: true,
  storage_drivers: [],
  auth_drivers: [],
  db_driver: 'sqlite',
  search_enabled: false,
  oidc_auto_redirect: false,
  demo_mode: false,
  demo_user: '',
  demo_pass: '',
};

export const useCapabilitiesStore = defineStore('capabilities', () => {
  const data = ref<Capabilities>(EMPTY);
  const loading = ref(false);
  const loaded = ref(false);

  async function fetch(): Promise<void> {
    loading.value = true;
    try {
      const res = await CapabilitiesApi.fetch();
      // Merge over EMPTY so a backend that omits a field (e.g. older
      // builds without auth_drivers) doesn't leave the field undefined
      // and crash callers that read `.length` / `.includes(...)`.
      data.value = { ...EMPTY, ...res };
      loaded.value = true;
      applyServerDefaultLocale(data.value.default_locale);
    } catch {
      // Capabilities are best-effort. Keep defaults if backend isn't ready yet.
    } finally {
      loading.value = false;
    }
  }

  // ⚠ ONE definition of "this instance is a public demo, so writing is
  // refused". The server is the authority — api/demo_guard.go answers 403 on
  // every admin mutation — and this is the UI's copy of the same fact, so a
  // page can stop offering a control it knows will be refused. A view that
  // asks the question its own way is a view that will disagree with the
  // server on the next release.
  const demoReadOnly = computed(() => data.value.demo_mode === true);

  function has(key: keyof Capabilities): boolean {
    const v = data.value[key];
    if (typeof v === 'boolean') return v;
    if (typeof v === 'string') return v.length > 0;
    if (Array.isArray(v)) return v.length > 0;
    return Boolean(v);
  }

  return { data, loading, loaded, demoReadOnly, fetch, has };
});
