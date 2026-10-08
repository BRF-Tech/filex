/**
 * 215-oo-callback-trust - what ONLYOFFICE's save callback is trusted for
 * (filex 0.54, docs/ONLYOFFICE.md → Security notes), against the running
 * server. Nothing here needs the document server to edit anything:
 *
 *   1. the editor configuration's token - handed to everybody who opens a
 *      document, a look included - does not sign a save, in the body or in
 *      the Authorization header: the file stays as it was, and the address
 *      the request named is never asked;
 *   2. a callback signed with the instance's secret for one document's
 *      session, posted for another document (`?node=` is not signed), writes
 *      nothing there;
 *   3. nor does one whose saved document is not on the document server's
 *      address - filex never asks that address;
 *   4. a callback shaped like the document server's own ("closed with no
 *      change", its token in the Authorization header wrapping the fields in
 *      `payload`, with iat and exp) is answered {"error":0};
 *   5. "keep the outside version" is taken only from the session's own
 *      editor: another administrator who may modify the file is refused
 *      `not_your_session`, even with the editor's token.
 *
 * ⚠ Needs ONLYOFFICE configured (capabilities external.onlyoffice "ok", the
 * harness's FILEX_ONLYOFFICE_URL / FILEX_ONLYOFFICE_JWT, as in 199); 2-4 sign
 * with FILEX_ONLYOFFICE_JWT and are skipped without it. Without a document
 * server the whole spec is skipped.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { createHmac } from 'node:crypto';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';

const CONFIG = '/api/files/onlyoffice/config';
const SESSION = '/api/files/onlyoffice/session';
const ORIGINAL = 'PK not really a docx - 215';

/** ONLYOFFICE is configured and answering, by the server's own probe. */
async function documentServer(api: APIRequestContext): Promise<boolean> {
  const res = await api.get('/api/files/capabilities');
  if (!res.ok()) return false;
  const caps = (await res.json()) as { external?: Record<string, { state?: string }> };
  return caps.external?.onlyoffice?.state === 'ok';
}

/** An HS256 token, the way the document server signs one. */
function sign(claims: Record<string, unknown>, secret: string): string {
  const enc = (o: unknown) => Buffer.from(JSON.stringify(o)).toString('base64url');
  const head = `${enc({ alg: 'HS256', typ: 'JWT' })}.${enc(claims)}`;
  return `${head}.${createHmac('sha256', secret).update(head).digest('base64url')}`;
}

/** iat and exp as Docs signs them (outbox.expires: five minutes). */
function timed(claims: Record<string, unknown>): Record<string, unknown> {
  const now = Math.floor(Date.now() / 1000);
  return { ...claims, iat: now, exp: now + 300 };
}

interface Opened {
  key: string;
  token: string;
  node: number;
}

/** The editor configuration of path, in mode, as api is handed it. */
async function open(api: APIRequestContext, path: string, mode: 'edit' | 'view'): Promise<Opened> {
  let body: { config?: { token?: string; document?: { key?: string }; editorConfig?: { callbackUrl?: string } } } = {};
  // The storage's first walk may not have listed the file yet.
  await expect
    .poll(
      async () => {
        const res = await api.post(CONFIG, { data: { path, mode } });
        if (res.ok()) body = await res.json();
        return res.status();
      },
      { timeout: 20_000 },
    )
    .toBe(200);
  const cb = body.config?.editorConfig?.callbackUrl ?? '';
  return {
    key: body.config?.document?.key ?? '',
    token: body.config?.token ?? '',
    node: Number(new URL(cb).searchParams.get('node')),
  };
}

test.describe.serial('what the document server callback is trusted for', () => {
  let api: APIRequestContext;
  let anon: APIRequestContext;
  let other: APIRequestContext | null = null;
  let store = '';
  let mount = '';
  let mine = '';
  let theirs = '';
  let ds = false;
  const secret = process.env.FILEX_ONLYOFFICE_JWT ?? '';
  const dsURL = (process.env.FILEX_ONLYOFFICE_URL ?? '').replace(/\/+$/, '');
  // Somewhere that is not the document server: counts who asks it.
  let elsewhere: Server;
  let elsewhereURL = '';
  let asked = 0;

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-oocb-${tag}-${Date.now()}`;
    mount = `/tmp/filex-${store}`;
    mine = `mine-${tag}.docx`;
    theirs = `theirs-${tag}.docx`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    // The document server has no session with filex: nothing signed in.
    anon = await playwright.request.newContext({ baseURL });
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, mine), ORIGINAL);
    writeFileSync(join(root, theirs), ORIGINAL);
    await seedLocalStorage(api, store, mount);
    ds = await documentServer(api);

    elsewhere = createServer((_req, res) => {
      asked++;
      res.end('ATTACKER BYTES');
    });
    await new Promise<void>((resolve) => elsewhere.listen(0, '127.0.0.1', resolve));
    elsewhereURL = `http://127.0.0.1:${(elsewhere.address() as AddressInfo).port}/saved.docx`;
  });

  test.afterAll(async () => {
    await new Promise<void>((resolve) => elsewhere.close(() => resolve()));
    await dropStorageByName(api, store).catch(() => undefined);
    await other?.dispose();
    await anon.dispose();
    await api.dispose();
  });

  const onDisk = (name: string) => readFileSync(join(storageRoot(mount), name), 'utf8');
  const callback = (node: number, data: Record<string, unknown>, bearer?: string) =>
    anon.post(`/api/files/onlyoffice/callback?node=${node}`, {
      data,
      headers: bearer ? { Authorization: `Bearer ${bearer}` } : undefined,
    });

  test("the editor configuration's token does not sign a save", async () => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run');
    const look = await open(api, `${store}://${mine}`, 'view');
    expect(look.token).not.toBe('');
    const fields = { key: look.key, status: 2, url: elsewhereURL, filetype: 'docx' };

    for (const [how, res] of [
      ['in the body', await callback(look.node, { ...fields, token: look.token })],
      ['in the Authorization header', await callback(look.node, fields, look.token)],
    ] as const) {
      expect(res.status(), how).toBe(200);
      expect((await res.json()).error, `the editor's token signed a save ${how}`).toBe(1);
    }
    expect(onDisk(mine)).toBe(ORIGINAL);
    expect(asked, 'filex fetched the address the forged callback named').toBe(0);
  });

  test("another document's session writes nothing here", async () => {
    test.skip(!ds || !secret || !dsURL, 'needs ONLYOFFICE and its secret (FILEX_ONLYOFFICE_JWT, FILEX_ONLYOFFICE_URL)');
    const session = await open(api, `${store}://${mine}`, 'edit');
    const target = await open(api, `${store}://${theirs}`, 'view');
    const fields = { key: session.key, status: 2, url: `${dsURL}/cache/files/data/x/output.docx`, filetype: 'docx', users: ['1'] };
    const res = await callback(target.node, { ...fields, token: sign(timed(fields), secret) });
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(body.error).toBe(1);
    expect(String(body.message)).toContain('not for this document');
    expect(onDisk(theirs)).toBe(ORIGINAL);
  });

  test("a saved document off the document server's address is never fetched", async () => {
    test.skip(!ds || !secret, 'needs ONLYOFFICE and its secret (FILEX_ONLYOFFICE_JWT)');
    const session = await open(api, `${store}://${mine}`, 'edit');
    const fields = { key: session.key, status: 2, url: elsewhereURL, filetype: 'docx', users: ['1'] };
    const res = await callback(session.node, { ...fields, token: sign(timed(fields), secret) });
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(body.error).toBe(1);
    expect(String(body.message)).toContain("not on the document server's address");
    expect(asked).toBe(0);
    expect(onDisk(mine)).toBe(ORIGINAL);
  });

  test("the document server's own callback is answered", async () => {
    test.skip(!ds || !secret, 'needs ONLYOFFICE and its secret (FILEX_ONLYOFFICE_JWT)');
    const session = await open(api, `${store}://${mine}`, 'edit');
    // Docs' default: the body is the fields, the token is in the header.
    const fields = { key: session.key, status: 4, users: ['1'], actions: [{ type: 0, userid: '1' }] };
    const res = await callback(session.node, fields, sign(timed({ payload: fields }), secret));
    expect(res.status()).toBe(200);
    expect(await res.json()).toEqual({ error: 0 });
    expect(onDisk(mine)).toBe(ORIGINAL);
  });

  test("only the session's own editor keeps the outside version", async ({ playwright, baseURL }, info) => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run');
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    const email = `oocb-${tag}-${Date.now()}@example.com`;
    const password = 'oocb-215-pw-2026';
    const made = await api.post('/api/admin/users', { data: { email, password, role: 'admin' } });
    expect(made.ok(), await made.text()).toBeTruthy();
    const peer = await newAuthedRequest(playwright, baseURL ?? '', email, password);
    other = peer;

    const path = `${store}://${mine}`;
    const session = await open(api, path, 'edit');

    for (const data of [
      { path, key: session.key, action: 'theirs' },
      { path, key: session.key, action: 'theirs', token: session.token },
    ]) {
      const res = await peer.post(SESSION, { data });
      expect(res.status(), 'another administrator answered for the session').toBe(403);
      expect(await res.json()).toEqual({ error: 'not_your_session' });
    }

    const res = await api.post(SESSION, { data: { path, key: session.key, action: 'theirs', token: session.token } });
    expect(res.status()).toBe(200);
  });
});
