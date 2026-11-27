import { describe, expect, it } from 'vitest';
import { loginReturnTarget } from '@/lib/loginReturn';

const nonce = '0123456789abcdef'.repeat(4);

describe('loginReturnTarget', () => {
  it.each(['', '/filex', '/nested/filex'])('resumes only the configured backend authorization path under %s', (base) => {
    const target = `${base}/api/oauth/authorize?request=${nonce}`;
    expect(loginReturnTarget(target, base)).toEqual({ target, browser: true });
  });

  it.each([
    undefined, ['/', '/api/oauth/authorize'],
    'https://evil.test/api/oauth/authorize', '//evil.test/api/oauth/authorize',
    `/api/oauth/authorize?request=${nonce}&redirect=https://evil.test`,
    `/api/oauth/authorize?request=${nonce}&request=${nonce}`,
    `/api/oauth/authorize?request=${nonce}#evil`,
    '/api/oauth/authorize?request=short',
    `/api/oauth/authorize?request=${nonce}g`,
    `/api/oauth/authorize?request=%30${nonce.slice(1)}`,
    `/api/oauth/%61uthorize?request=${nonce}`,
    `/else/../api/oauth/authorize?request=${nonce}`,
    `/api\\oauth/authorize?request=${nonce}`,
    `/api/oauth/authorize?request=${nonce}\n`,
    `/filex/api/oauth/authorize?request=${nonce}`,
  ])('sends an unsafe or malformed continuation to the SPA home: %s', (target) => {
    expect(loginReturnTarget(target, '')).toEqual({ target: '/', browser: false });
  });

  it('does not accept a root continuation when served under a base path', () => {
    expect(loginReturnTarget(`/api/oauth/authorize?request=${nonce}`, '/filex'))
      .toEqual({ target: '/', browser: false });
  });

  it.each(['/', '/files?folder=abc', '/settings#profile'])('keeps ordinary SPA return routes: %s', (target) => {
    expect(loginReturnTarget(target, '')).toEqual({ target, browser: false });
  });
});
