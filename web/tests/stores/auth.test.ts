// Pinia store tests for src/stores/auth.ts. AuthApi is mocked module-wide
// so the store sees deterministic responses; we never reach axios.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

vi.mock('@/api/auth', () => ({
  AuthApi: {
    me: vi.fn(),
    login: vi.fn(),
    handoff: vi.fn(),
    logout: vi.fn(),
    oidcStartUrl: vi.fn((p: string = 'oidc', r: string = '/admin/') => `/api/auth/oidc/start?provider=${p}&return_to=${r}`),
  },
}));

// Signing somebody in attaches their view document (columns, sort, the
// default folder view), which core then reads with a raw fetch of
// GET /api/files/manager/view-prefs. Here it is only seen being attached;
// nothing reaches the network.
vi.mock('@brftech/filex-core', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@brftech/filex-core')>()),
  attachViewPrefsHttp: vi.fn(),
}));

import { attachViewPrefsHttp } from '@brftech/filex-core';
import { useAuthStore } from '@/stores/auth';
import en from '@/locales/en.json';
import { AuthApi } from '@/api/auth';

describe('stores/auth', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('starts unauthenticated', () => {
    const store = useAuthStore();
    expect(store.isAuthenticated).toBe(false);
    expect(store.isAdmin).toBe(false);
    expect(store.user).toBeNull();
  });

  it('login(success) populates user + persists bearer token', async () => {
    (AuthApi.login as ReturnType<typeof vi.fn>).mockResolvedValue({
      user: {
        id: 1,
        email: 'admin@x',
        display_name: 'Admin',
        role: 'admin',
        created_at: '',
        updated_at: '',
      },
      token: 'token-abc',
    });
    (AuthApi.me as ReturnType<typeof vi.fn>).mockResolvedValue({
      user: {
        id: 1,
        email: 'admin@x',
        display_name: 'Admin',
        role: 'admin',
        created_at: '',
        updated_at: '',
      },
      permissions: ['*'],
    });

    const store = useAuthStore();
    const ok = await store.login({ email: 'admin@x', password: 'pw' });
    expect(ok).toBe(true);
    expect(store.user?.email).toBe('admin@x');
    expect(store.isAdmin).toBe(true);
    expect(store.permissions).toContain('*');
    expect(sessionStorage.getItem('filex.bearer')).toBe('token-abc');
    expect(attachViewPrefsHttp, "the person's view document is attached").toHaveBeenCalledTimes(1);
  });

  it('login(failure) sets error + leaves user null', async () => {
    (AuthApi.login as ReturnType<typeof vi.fn>).mockRejectedValue({
      response: { data: { error: 'invalid credentials' } },
    });
    const store = useAuthStore();
    const ok = await store.login({ email: 'x', password: 'y' });
    expect(ok).toBe(false);
    // extractError() falls back to the catalogue's login.errGeneric for plain rejection objects
    // because they aren't axios.isAxiosError(err) → true. Real axios errors
    // would surface 'invalid credentials' from response.data.error, but the
    // test mock doesn't carry the isAxiosError flag.
    expect(store.error).toBe(en.login.errGeneric);
    expect(store.user).toBeNull();
  });

  // A realm typed on the platform page of a multi-tenant server, for a tenant
  // with an address of its own (#128): signed in, but no session HERE - the
  // store keeps nothing and hands the page the ticket to carry.
  it('login(handoff) keeps no session and holds the ticket for the page', async () => {
    sessionStorage.removeItem('filex.bearer');
    (AuthApi.login as ReturnType<typeof vi.fn>).mockResolvedValue({
      handoff: { origin: 'https://files.acme.test', code: 'T' },
    });
    const store = useAuthStore();
    const ok = await store.login({ email: 'alex', password: 'pw', realm: 'acme' });
    expect(ok).toBe(false);
    expect(store.handoff).toEqual({ origin: 'https://files.acme.test', code: 'T' });
    expect(store.user).toBeNull();
    expect(store.error).toBeNull();
    expect(sessionStorage.getItem('filex.bearer')).toBeNull();
    expect(AuthApi.me).not.toHaveBeenCalled();
  });

  it('redeemHandoff opens the session on this address', async () => {
    const user = { id: 7, email: 'alex@acme.local', display_name: 'Alex', role: 'user', created_at: '', updated_at: '' };
    (AuthApi.handoff as ReturnType<typeof vi.fn>).mockResolvedValue({ user, token: 'tok-h' });
    (AuthApi.me as ReturnType<typeof vi.fn>).mockResolvedValue({ user, permissions: [] });
    const store = useAuthStore();
    expect(await store.redeemHandoff('T')).toBe(true);
    expect(AuthApi.handoff).toHaveBeenCalledWith('T');
    expect(store.user?.email).toBe('alex@acme.local');
    expect(sessionStorage.getItem('filex.bearer')).toBe('tok-h');

    (AuthApi.handoff as ReturnType<typeof vi.fn>).mockRejectedValue({ response: { status: 401 } });
    expect(await store.redeemHandoff('T')).toBe(false);
  });

  it('fetchMe(401) returns null without error', async () => {
    (AuthApi.me as ReturnType<typeof vi.fn>).mockRejectedValue({ response: { status: 401 } });
    const store = useAuthStore();
    const u = await store.fetchMe();
    expect(u).toBeNull();
    expect(store.user).toBeNull();
    expect(store.ready).toBe(true);
  });

  it('fetchMe(200) hydrates user + permissions', async () => {
    (AuthApi.me as ReturnType<typeof vi.fn>).mockResolvedValue({
      user: { id: 9, email: 'u@x', display_name: 'U', role: 'editor', created_at: '', updated_at: '' },
      permissions: ['files.read'],
    });
    const store = useAuthStore();
    const u = await store.fetchMe();
    expect(u?.id).toBe(9);
    expect(store.permissions).toEqual(['files.read']);
  });

  it('logout clears local state + bearer', async () => {
    sessionStorage.setItem('filex.bearer', 'oldtok');
    (AuthApi.logout as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

    const store = useAuthStore();
    // Stuff in some user state directly
    store.$patch({
      user: { id: 1, email: 'x@x', display_name: 'x', role: 'admin', created_at: '', updated_at: '' },
      permissions: ['*'],
    });
    await store.logout();
    expect(store.user).toBeNull();
    expect(store.permissions).toEqual([]);
    expect(sessionStorage.getItem('filex.bearer')).toBeNull();
  });

  // An SSO session has an IdP half too: the server hands back where to end it.
  it('logout resolves to the IdP end-session URL when the server returns one', async () => {
    (AuthApi.logout as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      logout_url: 'https://idp.example.test/logout?id_token_hint=x',
    });
    const store = useAuthStore();
    store.$patch({
      user: { id: 1, email: 'x@x', display_name: 'x', role: 'user', created_at: '', updated_at: '' },
    });

    const next = await store.logout('/drive/login');

    expect(next).toBe('https://idp.example.test/logout?id_token_hint=x');
    expect(AuthApi.logout).toHaveBeenCalledWith('/drive/login');
    expect(store.user).toBeNull();
  });

  it('logout resolves to null when signing out is local only', async () => {
    (AuthApi.logout as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: true });
    const store = useAuthStore();
    expect(await store.logout()).toBeNull();
  });

  it('logout swallows API errors and still clears local state', async () => {
    (AuthApi.logout as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('network'));
    const store = useAuthStore();
    store.$patch({
      user: { id: 1, email: 'x@x', display_name: 'x', role: 'admin', created_at: '', updated_at: '' },
    });
    await store.logout();
    expect(store.user).toBeNull();
  });

  it('can() short-circuits true for admin regardless of permission list', () => {
    const store = useAuthStore();
    store.$patch({
      user: { id: 1, email: 'a', display_name: 'A', role: 'admin', created_at: '', updated_at: '' },
      permissions: [],
    });
    expect(store.can('totally.fictional.permission')).toBe(true);
  });

  it('can() honors permissions for non-admin', () => {
    const store = useAuthStore();
    store.$patch({
      user: { id: 2, email: 'b', display_name: 'B', role: 'viewer', created_at: '', updated_at: '' },
      permissions: ['files.read'],
    });
    expect(store.can('files.read')).toBe(true);
    expect(store.can('files.write')).toBe(false);
  });

  it('exposes oidcStartUrl helper', () => {
    // Just check that the AuthApi shim is importable & callable.
    expect(typeof AuthApi.oidcStartUrl).toBe('function');
    expect(AuthApi.oidcStartUrl()).toContain('/api/auth/oidc/start');
  });
});
