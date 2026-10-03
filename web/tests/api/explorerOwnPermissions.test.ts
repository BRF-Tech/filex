// The explorer reads the account's permissions itself when its host passes
// none (the maintainer 2026-09-28, PR #75 review: "core kendi çeksin"). The web app
// passes them (Explore.vue); the desktop app and the work / fishapp embeds
// never did, so a role that denies Delete hid the button in the browser and
// showed it — to answer 403 — everywhere else. One surface, one behaviour:
// `useFileApi().myPermissions()` asks `/api/auth/me` through the SAME apiBase
// every other call uses (an embed's proxy included), and FileExplorer narrows
// with the answer.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { resolveEndpoints, useFileApi } from '@brftech/filex-core/src/composables/useFileApi';

afterEach(() => vi.unstubAllGlobals());

function stubMe(body: unknown, status = 200) {
  const calls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      calls.push(String(url));
      return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
    }),
  );
  return calls;
}

describe('the explorer asks for the account’s own permissions', () => {
  it('derives /api/auth/me from apiBase like every other endpoint', () => {
    expect(resolveEndpoints({ apiBase: 'https://files.example.com' } as never).me).toBe(
      'https://files.example.com/api/auth/me',
    );
    expect(resolveEndpoints({ apiBase: '/proxy/filex', me: '/custom/me' } as never).me).toBe('/custom/me');
  });

  it('returns the account-wide and in-folder permissions, and those that vary by folder', async () => {
    const calls = stubMe({
      user: { role: 'user' },
      permissions: ['files.download', 'files.create'],
      permissions_in_folders: ['files.delete'],
      permissions_by_folder: ['files.delete'],
    });
    const got = await useFileApi({ apiBase: '/proxy/filex' } as never).myPermissions();
    expect(calls).toEqual(['/proxy/filex/api/auth/me']);
    expect(got).toEqual({
      admin: false,
      permissions: ['files.download', 'files.create', 'files.delete'],
      byFolder: ['files.delete'],
    });
  });

  it('marks an administrator, whom the explorer does not narrow', async () => {
    stubMe({ user: { role: 'admin' }, permissions: ['admin.full'] });
    const got = await useFileApi({ apiBase: '' } as never).myPermissions();
    expect(got?.admin).toBe(true);
  });

  it('answers unknown (null) when the server sends no permissions', async () => {
    stubMe({ user: { role: 'user' } });
    expect(await useFileApi({ apiBase: '' } as never).myPermissions()).toBeNull();
  });

  it('is what FileExplorer narrows with when the host passes none', () => {
    const src = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
    // The host's list wins; the fetched one fills in only when there is none.
    expect(src).toMatch(/props\.config\.permissions \?\? fetchedPermissions\.value\?\.permissions/);
    // Every gate reads the one computed, never the prop directly.
    expect(src).not.toMatch(/props\.config\.permissionsByFolder\?\.includes|const held = props\.config\.permissions/);
    expect(src).toMatch(/onMounted\(loadOwnPermissions\)/);
  });
});
