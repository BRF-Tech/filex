// Every "Sign out" in the app goes through lib/signOut.
//
// Red proof for the defect this replaced: signing out dropped filex's own
// session and pushed /login, whose SSO-first mode started SSO on its own — and
// the IdP, whose session was still open, signed the same account straight
// back in without a form (measured on Keycloak 26: ~0.5 s). Nobody could
// switch accounts, and on a shared computer the next person got the previous
// one's files.
import { describe, it, expect, vi } from 'vitest';

import { signOut, signInPage } from '@/lib/signOut';

function fakeRouter(base: string) {
  return { options: { history: { base } }, push: vi.fn().mockResolvedValue(undefined) };
}

describe('signInPage', () => {
  it('is the sign-in page of the front door this document was served from', () => {
    expect(signInPage(fakeRouter('/drive'))).toBe('/drive/login');
    expect(signInPage(fakeRouter('/admin'))).toBe('/admin/login');
    expect(signInPage(fakeRouter(''))).toBe('/admin/login');
  });
});

describe('signOut', () => {
  it('continues to the IdP when the server says there is an IdP session to end', async () => {
    const auth = { logout: vi.fn().mockResolvedValue('https://idp.example.test/logout?id_token_hint=x') };
    const router = fakeRouter('/drive');
    const go = vi.fn();

    await signOut(auth, router, go);

    expect(auth.logout).toHaveBeenCalledWith('/drive/login');
    expect(go).toHaveBeenCalledWith('https://idp.example.test/logout?id_token_hint=x');
    expect(router.push).not.toHaveBeenCalled();
  });

  it('otherwise lands on sign-in marked signed out, so SSO does not start by itself', async () => {
    const auth = { logout: vi.fn().mockResolvedValue(null) };
    const router = fakeRouter('/admin');
    const go = vi.fn();

    await signOut(auth, router, go);

    expect(auth.logout).toHaveBeenCalledWith('/admin/login');
    expect(go).not.toHaveBeenCalled();
    expect(router.push).toHaveBeenCalledWith({ name: 'login', query: { signed_out: '1' } });
  });
});
