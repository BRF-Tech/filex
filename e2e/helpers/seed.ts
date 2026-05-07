import type { APIRequestContext } from '@playwright/test';
import { apiLogin, ADMIN_EMAIL, ADMIN_PASSWORD } from './auth';

/**
 * Seed a local-driver storage so the file tests have somewhere to play.
 * Returns the storage row from the API.
 */
export async function seedLocalStorage(
  request: APIRequestContext,
  name = 'e2e-local',
  mountPath = '/tmp/filex-e2e-storage',
) {
  await apiLogin(request);
  const res = await request.post('/api/admin/storages', {
    data: {
      name,
      driver: 'local',
      mount_path: mountPath,
      // model.Storage's JSON tag is `config` (not `config_json`) and the
      // field is json.RawMessage — pass an object, not a string.
      config: { root: mountPath, path: 'fileman' },
      sync_mode: 'fsnotify',
      sync_interval_s: 0,
      enabled: true,
      read_only: false,
    },
  });
  if (!res.ok()) throw new Error(`seedLocalStorage failed: ${res.status()} ${await res.text()}`);
  return res.json();
}

/**
 * Best-effort cleanup — removes any storage with the given name. The
 * tests share a single DB so cleanup between runs avoids drift.
 */
export async function dropStorageByName(request: APIRequestContext, name: string) {
  await apiLogin(request);
  const list = await request.get('/api/admin/storages');
  if (!list.ok()) return;
  const items: Array<{ id: number; name: string }> = await list.json();
  for (const item of items) {
    if (item.name === name) {
      await request.delete(`/api/admin/storages/${item.id}`);
    }
  }
}

/**
 * Builds a Bearer-token-bearing APIRequestContext that survives across
 * tests. Per-test `request` fixtures are fresh contexts so cookies set
 * by `apiLogin` in beforeAll don't leak — this helper turns a token
 * obtained once at suite start into a self-contained authed context.
 *
 * Caller is responsible for `dispose()` (typically in `afterAll`).
 */
export async function newAuthedRequest(
  playwright: { request: { newContext(opts: object): Promise<APIRequestContext> } },
  baseURL: string,
  email = ADMIN_EMAIL,
  password = ADMIN_PASSWORD,
): Promise<APIRequestContext> {
  const ctx = await playwright.request.newContext({ baseURL });
  const login = await ctx.post('/api/auth/login', { data: { email, password } });
  if (!login.ok()) {
    throw new Error(`newAuthedRequest login failed: ${login.status()} ${await login.text()}`);
  }
  const { token } = await login.json();
  await ctx.dispose();
  return playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
}

/**
 * Polls /api/files/ops/{id} until the op reaches a terminal state
 * ("ok" / "failed" / "partial") or the deadline elapses.
 *
 * The per-verb endpoints (POST /api/files/copy etc.) return 202 with a
 * pending op handle; this helper is the standard wait-for-completion
 * shim used by tests that need the side effect to land before they
 * assert on the storage state.
 */
export async function waitForOp(
  request: APIRequestContext,
  opID: number,
  timeoutMs = 10_000,
): Promise<{ id: number; status: string; error?: string; total?: number; done?: number }> {
  const deadline = Date.now() + timeoutMs;
  const terminal = new Set(['ok', 'failed', 'partial', 'done']);
  let last: { id: number; status: string; error?: string; total?: number; done?: number } = {
    id: opID,
    status: 'unknown',
  };
  while (Date.now() < deadline) {
    const res = await request.get(`/api/files/ops/${opID}`);
    if (res.ok()) {
      last = await res.json();
      if (last.status && terminal.has(last.status)) return last;
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`waitForOp(${opID}) timed out — last status=${JSON.stringify(last)}`);
}

/**
 * Find a node id by uploading the listing and matching basename. Useful
 * when you uploaded a file and want its DB id without parsing the
 * upload response (which projects the parent dir, not the new file).
 */
export async function findNodeIdByBasename(
  request: APIRequestContext,
  storagePath: string,
  basename: string,
): Promise<number | null> {
  const res = await request.get(
    `/api/files/manager?action=index&path=${encodeURIComponent(storagePath)}`,
  );
  if (!res.ok()) return null;
  const body: { files?: Array<{ id?: number; basename?: string }> } = await res.json();
  const f = (body.files ?? []).find((x) => x.basename === basename);
  return f?.id ?? null;
}
