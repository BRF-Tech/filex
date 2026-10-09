// sec055 S5: a storage plugin build's signature is over the build's name,
// version, platform and SHA-256, so an install or upgrade that carries a
// signature also says which version it was signed as. Without it only the
// old sha256-only form can verify (taken in 0.55, refused from 0.56).
import { beforeEach, describe, expect, it, vi } from 'vitest';

const posts: Array<{ url: string; body: unknown }> = [];

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async () => ({ data: {} })),
    post: vi.fn(async (url: string, body?: unknown) => {
      posts.push({ url, body });
      return { data: {} };
    }),
    patch: vi.fn(async () => ({ data: {} })),
    delete: vi.fn(async () => ({ data: {} })),
  },
}));

import { PluginsApi } from '@/api/plugins';

beforeEach(() => {
  posts.length = 0;
});

describe('a signed storage plugin build names its version', () => {
  it('an upload sends the version beside the signature', async () => {
    await PluginsApi.upload('myfs', new File(['x'], 'myfs'), 'ab'.repeat(64), '', '1.3.0');
    const form = posts[0].body as FormData;
    expect(form.get('signature')).toBe('ab'.repeat(64));
    expect(form.get('version')).toBe('1.3.0');
  });

  it('an install from an address sends it in the body', async () => {
    await PluginsApi.fromUrl('myfs', 'https://example.com/myfs', 'cd'.repeat(32), 'ab'.repeat(64), '', '1.3.0');
    expect(posts[0].body).toMatchObject({ name: 'myfs', signature: 'ab'.repeat(64), version: '1.3.0' });
  });

  it('an upgrade sends it too', async () => {
    await PluginsApi.upgrade(3, new File(['x'], 'myfs'), 'ab'.repeat(64), '1.4.0');
    const form = posts[0].body as FormData;
    expect(posts[0].url).toBe('/admin/plugins/3/upgrade');
    expect(form.get('version')).toBe('1.4.0');
  });

  it('no version, no field: the server then checks the old form only', async () => {
    await PluginsApi.fromUrl('myfs', 'https://example.com/myfs', 'cd'.repeat(32), 'ab'.repeat(64));
    expect(posts[0].body).not.toHaveProperty('version');
  });
});
