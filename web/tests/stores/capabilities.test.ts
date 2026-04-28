// Tests for src/stores/capabilities.ts. The CapabilitiesApi is mocked so
// we can exercise fetch() success / failure paths without touching axios.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

vi.mock('@/api/capabilities', () => ({
  CapabilitiesApi: {
    fetch: vi.fn(),
  },
}));

import { useCapabilitiesStore } from '@/stores/capabilities';
import { CapabilitiesApi } from '@/api/capabilities';

describe('stores/capabilities', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('starts with safe defaults', () => {
    const store = useCapabilitiesStore();
    expect(store.loaded).toBe(false);
    expect(store.data.ffmpeg).toBe(false);
    expect(store.data.monaco).toBe(true);
    expect(store.data.storage_drivers).toEqual([]);
  });

  it('fetch() populates from the API', async () => {
    (CapabilitiesApi.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      version: '1.0.0',
      build: 'abc',
      ffmpeg: true,
      imagemagick: false,
      ghostscript: true,
      libreoffice: false,
      onlyoffice_url: 'https://docs.example.com',
      drawio_url: null,
      mermaid_url: null,
      monaco: true,
      storage_drivers: ['local', 's3'],
      auth_drivers: ['local'],
      db_driver: 'sqlite',
      search_enabled: true,
    });

    const store = useCapabilitiesStore();
    await store.fetch();

    expect(store.loaded).toBe(true);
    expect(store.data.ffmpeg).toBe(true);
    expect(store.data.storage_drivers).toEqual(['local', 's3']);
  });

  it('fetch() failure keeps defaults; loaded remains false', async () => {
    (CapabilitiesApi.fetch as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('boom'));
    const store = useCapabilitiesStore();
    await store.fetch();
    expect(store.loaded).toBe(false);
    expect(store.data.ffmpeg).toBe(false);
  });

  it('has() returns true for boolean-true keys', async () => {
    (CapabilitiesApi.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      version: '1',
      build: 'x',
      ffmpeg: true,
      imagemagick: false,
      ghostscript: false,
      libreoffice: false,
      onlyoffice_url: 'https://x',
      drawio_url: null,
      mermaid_url: null,
      monaco: false,
      storage_drivers: ['local'],
      auth_drivers: [],
      db_driver: 'sqlite',
      search_enabled: true,
    });
    const store = useCapabilitiesStore();
    await store.fetch();
    expect(store.has('ffmpeg')).toBe(true);
    expect(store.has('imagemagick')).toBe(false);
    expect(store.has('onlyoffice_url')).toBe(true);
    expect(store.has('drawio_url')).toBe(false);
    expect(store.has('storage_drivers')).toBe(true);
    expect(store.has('auth_drivers')).toBe(false);
  });

  it('repeated fetch() calls overwrite the cache', async () => {
    const fetchMock = CapabilitiesApi.fetch as ReturnType<typeof vi.fn>;
    fetchMock.mockResolvedValueOnce({
      version: '1',
      build: 'x',
      ffmpeg: false,
      imagemagick: false,
      ghostscript: false,
      libreoffice: false,
      onlyoffice_url: null,
      drawio_url: null,
      mermaid_url: null,
      monaco: true,
      storage_drivers: [],
      auth_drivers: [],
      db_driver: 'sqlite',
      search_enabled: false,
    });
    fetchMock.mockResolvedValueOnce({
      version: '2',
      build: 'y',
      ffmpeg: true,
      imagemagick: true,
      ghostscript: true,
      libreoffice: true,
      onlyoffice_url: null,
      drawio_url: null,
      mermaid_url: null,
      monaco: true,
      storage_drivers: ['s3'],
      auth_drivers: ['oidc'],
      db_driver: 'postgres',
      search_enabled: true,
    });
    const store = useCapabilitiesStore();
    await store.fetch();
    expect(store.data.version).toBe('1');
    await store.fetch();
    expect(store.data.version).toBe('2');
    expect(store.data.ffmpeg).toBe(true);
  });
});
