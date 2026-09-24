// The admin app-plugins client speaks exactly the routes and bodies in
// docs/APP-PLUGINS-API.md — URL, method, query and body per call.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const calls: Array<{ method: string; url: string; body?: unknown; cfg?: unknown }> = [];

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string, cfg?: unknown) => {
      calls.push({ method: 'get', url, cfg });
      if (url === '/admin/app-plugins') return { data: { runtime: { enabled: true, arch_ok: true, engines: { ffmpeg: true } }, plugins: [] } };
      if (url.endsWith('/settings')) return { data: { values: { api_key: '***' } } };
      if (url.endsWith('/overrides')) return { data: { actions: [{ id: 'sign', enabled: false, applies: null, admin_only: true }] } };
      if (url.endsWith('/logs')) return { data: { lines: [{ seq: 1, ts: 't', level: 'info', msg: 'hi' }], next: 2 } };
      return { data: { id: 1, name: 'sign' } };
    }),
    post: vi.fn(async (url: string, body?: unknown, cfg?: unknown) => {
      calls.push({ method: 'post', url, body, cfg });
      return { data: { id: 1, name: 'sign' } };
    }),
    put: vi.fn(async (url: string, body?: unknown) => {
      calls.push({ method: 'put', url, body });
      return { data: {} };
    }),
    patch: vi.fn(async (url: string, body?: unknown) => {
      calls.push({ method: 'patch', url, body });
      return { data: { id: 1, enabled: false } };
    }),
    delete: vi.fn(async (url: string) => {
      calls.push({ method: 'delete', url });
      return { data: undefined };
    }),
  },
}));

import { AppPluginsApi, INSTALL_TIMEOUT_MS, appPluginError, type AppPluginField } from '@/api/appPlugins';
import { api } from '@/api/client';
import { pluginLabelOf, storageFieldOf } from '@brftech/filex-core';

describe('AppPluginsApi', () => {
  beforeEach(() => {
    calls.length = 0;
  });

  it('list: GET /admin/app-plugins, with defaults for what the server leaves out', async () => {
    const res = await AppPluginsApi.list();
    expect(calls[0]).toMatchObject({ method: 'get', url: '/admin/app-plugins' });
    expect(res.runtime).toEqual({ enabled: true, arch_ok: true, disabled_reason: '', requires_signature: false, engines: { ffmpeg: true }, engine_names: {} });
    expect(res.plugins).toEqual([]);
  });

  it('dry run: the same body with ?dry_run=1 and no permissions granted', async () => {
    await AppPluginsApi.dryRun({ kind: 'github', repo: 'BRF-Tech/filex-sign', ref: 'v1.0.0' });
    expect(calls[0]).toMatchObject({
      method: 'post',
      url: '/admin/app-plugins',
      body: { github_repo: 'BRF-Tech/filex-sign', ref: 'v1.0.0', permissions: [] },
      cfg: { params: { dry_run: 1 } },
    });
  });

  it('install from GitHub: JSON body with the granted permissions', async () => {
    await AppPluginsApi.install({ kind: 'github', repo: 'o/n' }, ['files:read']);
    expect(calls[0]).toMatchObject({
      method: 'post',
      url: '/admin/app-plugins',
      body: { github_repo: 'o/n', ref: '', permissions: ['files:read'] },
    });
    // A real install: no dry_run query (its only config is the longer wait).
    expect((calls[0].cfg as { params?: unknown } | undefined)?.params).toBeUndefined();
  });

  it('install from a URL: url + manifest_url + sha256', async () => {
    await AppPluginsApi.install({ kind: 'url', url: 'https://h/p.wasm', manifest_url: 'https://h/m.json', sha256: 'ab' }, ['a']);
    expect(calls[0].body).toEqual({ url: 'https://h/p.wasm', manifest_url: 'https://h/m.json', sha256: 'ab', permissions: ['a'] });
  });

  it('install from files: multipart with wasm, manifest, signature and a JSON grant field', async () => {
    const wasm = new File([new Uint8Array([0, 97, 115, 109])], 'p.wasm');
    const manifest = new File(['{}'], 'filex-app.json');
    await AppPluginsApi.install({ kind: 'upload', wasm, manifest, signature: 'sig' }, ['files:read', 'net:tsa']);
    const body = calls[0].body as FormData;
    expect(body).toBeInstanceOf(FormData);
    expect(body.get('wasm')).toBe(wasm);
    expect(body.get('manifest')).toBe(manifest);
    expect(body.get('signature')).toBe('sig');
    expect(JSON.parse(String(body.get('grant')))).toEqual({ permissions: ['files:read', 'net:tsa'] });
  });

  // ⚠⚠ The server compiles the module before it answers; a 20 MB module took
  // 29 s on a busy machine (2026-09-21) and the client's 30 s default cut the
  // wizard off with "timeout of 30000ms exceeded" mid-install.
  it('install and upgrade wait for the compile, not the 30 s default', async () => {
    await AppPluginsApi.install({ kind: 'github', repo: 'o/n' }, ['files:read']);
    await AppPluginsApi.upgrade(3, { kind: 'github', repo: 'o/n' }, ['x']);
    await AppPluginsApi.dryRun({ kind: 'github', repo: 'o/n' });
    expect(calls[0].cfg).toMatchObject({ timeout: INSTALL_TIMEOUT_MS });
    expect(calls[1].cfg).toMatchObject({ timeout: INSTALL_TIMEOUT_MS });
    expect(INSTALL_TIMEOUT_MS).toBeGreaterThanOrEqual(120_000);
    // A dry run compiles nothing: it keeps the ordinary budget.
    expect((calls[2].cfg as { timeout?: number } | undefined)?.timeout).toBeUndefined();
  });

  it('get / enable / delete / upgrade hit the row routes', async () => {
    await AppPluginsApi.get(3);
    await AppPluginsApi.setEnabled(3, false);
    await AppPluginsApi.remove(3);
    await AppPluginsApi.upgrade(3, { kind: 'github', repo: 'o/n' }, ['x']);
    await AppPluginsApi.upgradeDryRun(3, { kind: 'github', repo: 'o/n' });
    expect(calls.map((c) => [c.method, c.url])).toEqual([
      ['get', '/admin/app-plugins/3'],
      ['patch', '/admin/app-plugins/3'],
      ['delete', '/admin/app-plugins/3'],
      ['post', '/admin/app-plugins/3/upgrade'],
      ['post', '/admin/app-plugins/3/upgrade'],
    ]);
    expect(calls[1].body).toEqual({ enabled: false });
    expect(calls[3].body).toEqual({ github_repo: 'o/n', ref: '', permissions: ['x'] });
    expect(calls[4].cfg).toEqual({ params: { dry_run: 1 } });
  });

  it('settings and overrides: GET unwraps, PUT wraps', async () => {
    expect(await AppPluginsApi.getSettings(3)).toEqual({ api_key: '***' });
    await AppPluginsApi.putSettings(3, { api_key: '***', tsa_url: 'https://tsa' });
    expect(await AppPluginsApi.getOverrides(3)).toEqual([{ id: 'sign', enabled: false, applies: null, admin_only: true }]);
    await AppPluginsApi.putOverrides(3, [{ id: 'sign', enabled: true, applies: { kind: 'file', ext: ['pdf'] }, admin_only: false }]);
    expect(calls[1]).toMatchObject({ method: 'put', url: '/admin/app-plugins/3/settings', body: { values: { api_key: '***', tsa_url: 'https://tsa' } } });
    expect(calls[3]).toMatchObject({ method: 'put', url: '/admin/app-plugins/3/overrides', body: { actions: [{ id: 'sign', enabled: true, admin_only: false }] } });
  });

  it('logs: ?after=N and the cursor comes back', async () => {
    const res = await AppPluginsApi.logs(3, 12);
    expect(calls[0]).toMatchObject({ method: 'get', url: '/admin/app-plugins/3/logs', cfg: { params: { after: 12 } } });
    expect(res).toEqual({ lines: [{ seq: 1, ts: 't', level: 'info', msg: 'hi' }], next: 2 });
  });
});

describe('appPluginError', () => {
  it('reads the code and the missing list off an axios refusal', () => {
    const err = { isAxiosError: true, response: { status: 400, data: { error: 'permissions_incomplete', missing: ['net:tsa'] } } };
    expect(appPluginError(err)).toMatchObject({ code: 'permissions_incomplete', missing: ['net:tsa'], message: '' });
    expect(appPluginError(new Error('x'))).toBeNull();
  });

  // ⚠ The SERVER's own bytes (handlers/testdata/wire/app-plugin-fetch-failed
  // .json, written by app_plugins_wire_test.go): a refused repository fetch
  // carries why and what, and the wizard words it from those — never from
  // the English `message`.
  it('reads why and what off a refused fetch (the server\'s own answer)', () => {
    const data = JSON.parse(
      readFileSync(path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire/app-plugin-fetch-failed.json'), 'utf8'),
    );
    const got = appPluginError({ isAxiosError: true, response: { status: 502, data } });
    expect(got).toMatchObject({
      code: 'fetch_failed',
      reason: 'manifest_not_found',
      where: 'BRF-Tech/yok-boyle-bir-depo',
      refs: ['main', 'master'],
      status: 404,
    });
  });
});

describe('one mapper for a plugin field', () => {
  // ⚠⚠ There was a `fieldToStorageField` in this module — a second mapper
  // beside the core package's `storageFieldOf`, used by the admin's app
  // settings screen alone. It is gone, and the admin screen calls the same
  // one every plugin surface calls, so a manifest field cannot mean a
  // dropdown on one screen and a row of buttons on another.
  const map = (f: AppPluginField) => storageFieldOf(f, 'en');

  it('maps the wire Field onto the storage form field', () => {
    expect(map({ key: 'k', type: 'text', label: 'Notes' })).toMatchObject({ key: 'k', type: 'string', multiline: true, secret: false, required: false });
    expect(map({ key: 'api_key', type: 'password', label: 'Key', secret: true })).toMatchObject({ type: 'password', secret: true });
    expect(map({ key: 'n', type: 'int', label: 'N', min: 1, max: 9 })).toMatchObject({ type: 'int', min: 1, max: 9 });
  });

  it('carries what the old one dropped and drops what the contract deleted', () => {
    const sel = map({ key: 'kinds', type: 'select', multi: true, options: [{ value: 'pdf' }] });
    expect(sel).toMatchObject({ choice: true, multi: true });
    // `advanced` is not a key of the mapped field at all — not `false`,
    // absent: `wire.Field` has no `Advanced` and the server drops it at parse.
    expect('advanced' in map({ key: 'x', type: 'string', advanced: true } as AppPluginField)).toBe(false);
  });
});

/**
 * ⚠⚠ The SERVER's own answers, not fixtures typed from this client.
 *
 * backend/internal/api/handlers/testdata/wire/*.json are written and checked
 * by the Go test app_plugins_wire_test.go from the exact values the handlers
 * send. Both Apps defects of v0.43.0 hid behind fixtures typed the client's
 * way — the detail's `plugin` envelope read flat (a signed app drawn
 * "Unsigned"), and a permission's reason typed `string` while the wire sends
 * `{en, tr}` (the install review printed raw JSON). These cases fail the day
 * the client and the server stop describing the same bytes.
 */
describe('AppPluginsApi against the server\'s own answers', () => {
  const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
  const wire = (name: string) => JSON.parse(readFileSync(path.join(WIRE, name), 'utf8'));

  it('get: the app\'s facts come out of the `plugin` envelope', async () => {
    const answer = wire('app-plugin-detail.json');
    vi.mocked(api.get).mockResolvedValueOnce({ data: answer });
    const d = await AppPluginsApi.get(7);
    expect(d.name).toBe(answer.plugin.name);
    expect(d.version).toBe(answer.plugin.version);
    expect(d.signed).toBe(true);
    expect(d.sha256).toBe(answer.plugin.sha256);
    expect(pluginLabelOf(d.label, 'tr')).toBe(answer.plugin.label.tr);
    expect(pluginLabelOf(d.description, 'en')).toBe(answer.plugin.description.en);
  });

  it('get: `permissions` stays the ids, and the reviewed rows arrive as `permission_rows`', async () => {
    const answer = wire('app-plugin-detail.json');
    vi.mocked(api.get).mockResolvedValueOnce({ data: answer });
    const d = await AppPluginsApi.get(7);
    // ⚠ On the wire both are called `permissions`, one level apart. Spread
    // naively the rows won, and `detail.permissions` — typed string[] — held
    // objects that print as JSON wherever a badge falls back to it.
    expect(d.permissions).toEqual(answer.plugin.permissions);
    for (const id of d.permissions) expect(typeof id).toBe('string');
    expect(d.granted).toEqual(answer.granted);
    expect(d.permission_rows).toEqual(answer.permissions);
    const withReason = (d.permission_rows ?? []).filter((r) => r.reason);
    expect(withReason.length).toBeGreaterThan(0);
    for (const r of withReason) {
      // The real shape: an object of languages, read through the one helper.
      expect(r.reason).toEqual({ en: expect.any(String), tr: expect.any(String) });
      expect(pluginLabelOf(r.reason, 'tr')).toBe((r.reason as Record<string, string>).tr);
    }
  });

  it('list: a row\'s label and description are objects of languages', async () => {
    const answer = wire('app-plugin-list.json');
    vi.mocked(api.get).mockResolvedValueOnce({ data: answer });
    const { plugins } = await AppPluginsApi.list();
    expect(plugins).toHaveLength(1);
    expect(plugins[0].label).toEqual({ en: expect.any(String), tr: expect.any(String) });
    expect(plugins[0].description).toEqual({ en: expect.any(String), tr: expect.any(String) });
    expect(plugins[0].permissions.every((p) => typeof p === 'string')).toBe(true);
  });

  it('dry run: the review rows carry `reason` as an object, `label` as words', async () => {
    const answer = wire('app-plugin-dry-run.json');
    vi.mocked(api.post).mockResolvedValueOnce({ data: answer });
    const dry = await AppPluginsApi.dryRun({ kind: 'github', repo: 'BRF-Tech/filex-sign' });
    for (const row of dry.permissions) {
      expect(typeof row.label).toBe('string');
      if (row.reason !== undefined) expect(typeof row.reason).toBe('object');
    }
    expect(dry.signed).toBe(true);
    expect(dry.wasm_bytes).toBeGreaterThan(0);
  });
});
