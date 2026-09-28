// A single encrypted file opened with the installation's ESCROW key — the
// same door, and the same rule, as an encrypted folder's
// (docs/E2E-ENCRYPTION.md → "Key escrow"): the browser proves it holds the
// private key (the server's challenge, opened with it) and the server tells
// the file's owner BEFORE anything is decrypted. An announcement that fails
// opens nothing. The composable is driven with a real `.fxe` sealed to the
// committed test key pair; the browser run is e2e/tests/173.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h, nextTick } from 'vue';

import { useE2eFiles } from '../../../packages/core/src/composables/useE2eFiles';
import { createFxe } from '../../../packages/core/src/lib/e2efile';
import { escrowKeyId } from '../../../packages/core/src/lib/e2ecrypto';
import { bytesStream, collectBytes } from '../../../packages/core/src/lib/e2estream';
import type { FileNode } from '../../../packages/core/src/types/FileNode';
import E2eFileUnlockModal from '../../../packages/core/src/components/E2eFileUnlockModal.vue';

import escrowTestKey from '../fixtures/escrow-testkey.json';

const PW = 'a file password, long enough';
const NAME = 'Bütçe 2027.xlsx';
const PLAIN = new TextEncoder().encode('gizli bütçe satırları\n'.repeat(20));
const PATH = 's://encrypted-1a2b3c4d.fxe';

const b64 = (u8: Uint8Array) => btoa(String.fromCharCode(...u8));

let fxeBytes: Uint8Array;
let kid = '';

async function makeFxe(escrow: boolean) {
  const created = await createFxe(NAME, PLAIN.length, bytesStream(PLAIN), PW, {
    escrowPublicKey: escrow ? escrowTestKey.public_spki_b64 : null,
  });
  return collectBytes(created.stream);
}

async function sealNonce(nonce: Uint8Array): Promise<string> {
  const der = Uint8Array.from(atob(escrowTestKey.public_spki_b64), (c) => c.charCodeAt(0));
  const pub = await crypto.subtle.importKey('spki', der, { name: 'RSA-OAEP', hash: 'SHA-256' }, false, ['encrypt']);
  return b64(new Uint8Array(await crypto.subtle.encrypt({ name: 'RSA-OAEP' }, pub, nonce)));
}

interface Calls {
  order: string[];
  nonce: Uint8Array;
  used: Array<{ path: string; id: string; nonce: string }>;
  challenged: string[];
}

function mount(opts: { usedFails?: boolean } = {}) {
  const calls: Calls = { order: [], nonce: crypto.getRandomValues(new Uint8Array(32)), used: [], challenged: [] };
  const openPreview = vi.fn(() => calls.order.push('preview'));
  const api = {
    authHeaders: async () => ({}),
    previewUrl: (p: string) => `http://filex.test/preview?path=${encodeURIComponent(p)}`,
    credentialsMode: () => 'same-origin',
    e2eEscrowChallenge: vi.fn(async (p: string) => {
      calls.challenged.push(p);
      calls.order.push('challenge');
      return { id: 'ch-1', challenge: await sealNonce(calls.nonce), kid };
    }),
    e2eEscrowUsed: vi.fn(async (payload: { path: string; id: string; nonce: string }) => {
      calls.order.push('used');
      calls.used.push(payload);
      if (opts.usedFails) throw new Error('503 notify is down');
      return { ok: true, notified: true };
    }),
  };
  let files!: ReturnType<typeof useE2eFiles>;
  const app = createApp(
    defineComponent({
      setup() {
        files = useE2eFiles({
          api: api as never,
          chunked: { uploadFile: async () => ({ id: 'x' }) as never, threshold: () => 8 << 20 },
          locale: () => 'en',
          t: (k) => k,
          toast: () => undefined,
          emitError: () => undefined,
          escrowPublicKey: () => escrowTestKey.public_spki_b64,
          escrowKid: () => kid,
          registerOp: () => undefined,
          reload: async () => undefined,
          openPreview,
          showRecoveryKey: () => undefined,
        } as never);
        return () => h('div');
      },
    }),
  );
  app.mount(document.createElement('div'));
  return { files, api, calls, openPreview, unmount: () => app.unmount() };
}

const node: FileNode = { path: PATH, basename: 'encrypted-1a2b3c4d.fxe', type: 'file', extension: 'fxe', size: 0 };

async function until(cond: () => boolean, what: string) {
  for (let i = 0; i < 200 && !cond(); i++) await new Promise((r) => setTimeout(r, 10));
  expect(cond(), what).toBe(true);
}

describe('a .fxe opened with the escrow key', () => {
  beforeEach(async () => {
    kid = await escrowKeyId(escrowTestKey.public_spki_b64);
    fxeBytes = await makeFxe(true);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(fxeBytes.slice())),
    );
    let n = 0;
    vi.spyOn(URL, 'createObjectURL').mockImplementation(() => `blob:fxe-${++n}`);
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined);
  }, 60_000);

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('the dialog learns the escrow door applies to this file before anything is typed', async () => {
    const { files, unmount } = mount();
    await files.open(node);
    expect(files.unlockTarget.value?.path).toBe(PATH);
    await until(() => files.unlockEscrow.value === 'available', 'escrow state read from the header');
    expect(files.unlockEscrowKid.value).toBe(kid);
    unmount();
  });

  it('announces first (challenge → owner told), THEN decrypts and opens', async () => {
    const { files, calls, openPreview, unmount } = mount();
    await files.open(node);
    await files.submitUnlock({ escrowKey: escrowTestKey.private_pkcs8_b64 });
    expect(files.unlockError.value).toBeNull();
    expect(calls.challenged).toEqual([PATH]);
    expect(calls.used).toEqual([{ path: PATH, id: 'ch-1', nonce: b64(calls.nonce) }]);
    expect(calls.order, 'the owner is told before the file is opened').toEqual(['challenge', 'used', 'preview']);
    expect(openPreview).toHaveBeenCalledTimes(1);
    expect(files.realName(PATH)).toBe(NAME);
    unmount();
  }, 60_000);

  it('an announcement that fails opens nothing and keeps no key', async () => {
    const { files, calls, openPreview, unmount } = mount({ usedFails: true });
    await files.open(node);
    await files.submitUnlock({ escrowKey: escrowTestKey.private_pkcs8_b64 });
    expect(calls.used).toHaveLength(1);
    expect(files.unlockError.value).toBe('e2e.fxe.escrow_notify_failed');
    expect(openPreview).not.toHaveBeenCalled();
    expect(files.realName(PATH), 'the key is not kept in the ring').toBeUndefined();
    expect(files.unlockTarget.value?.path, 'the dialog stays open').toBe(PATH);
    unmount();
  }, 60_000);

  it('a key that is not a private key, or not the one this file was sealed to, asks the server nothing', async () => {
    const { files, calls, openPreview, unmount } = mount();
    await files.open(node);
    await files.submitUnlock({ escrowKey: 'not a key' });
    expect(files.unlockError.value).toBe('e2e.recover.bad_escrow_key');
    const other = await crypto.subtle.generateKey(
      { name: 'RSA-OAEP', modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: 'SHA-256' },
      true,
      ['encrypt', 'decrypt'],
    );
    const pkcs8 = new Uint8Array(await crypto.subtle.exportKey('pkcs8', other.privateKey));
    await files.submitUnlock({ escrowKey: b64(pkcs8) });
    expect(files.unlockError.value).toBe('e2e.fxe.wrong_escrow');
    expect(calls.challenged).toEqual([]);
    expect(openPreview).not.toHaveBeenCalled();
    unmount();
  }, 60_000);

  it('a file sealed without escrow says so, and offers no escrow door', async () => {
    fxeBytes = await makeFxe(false);
    const { files, unmount } = mount();
    await files.open(node);
    await until(() => files.unlockEscrow.value === 'predates', 'no escrow slot');
    unmount();
  }, 60_000);
});

describe('E2eFileUnlockModal — the escrow door', () => {
  function modal(props: Record<string, unknown>) {
    const submitted: unknown[] = [];
    const el = document.createElement('div');
    document.body.appendChild(el);
    const app = createApp({
      render: () =>
        h(E2eFileUnlockModal, {
          open: true,
          locale: 'en',
          intent: 'open',
          fileName: NAME,
          hasRecovery: true,
          ...props,
          onSubmit: (p: unknown) => submitted.push(p),
        }),
    });
    app.mount(el);
    return {
      submitted,
      q: (id: string) => document.body.querySelector(`[data-testid="${id}"]`) as HTMLElement | null,
      unmount: () => {
        app.unmount();
        el.remove();
      },
    };
  }

  it('offered when the file was sealed to this installation; says the owner is told; submits the key', async () => {
    const m = modal({ escrowState: 'available', escrowKid: 'abcd1234' });
    await nextTick();
    const toggle = m.q('fxe-unlock-escrow-toggle');
    expect(toggle, 'the escrow door is offered').not.toBeNull();
    toggle!.click();
    await nextTick();
    expect(m.q('fxe-unlock-escrow-warn')?.textContent).toContain('The file’s owner will be told');
    expect(m.q('fxe-unlock-escrow-warn')?.textContent).toContain('abcd1234');
    const ta = m.q('fxe-unlock-escrow') as HTMLTextAreaElement;
    ta.value = 'PKCS8KEY';
    ta.dispatchEvent(new Event('input'));
    await nextTick();
    m.q('fxe-unlock-submit')!.click();
    await nextTick();
    expect(m.submitted).toEqual([{ escrowKey: 'PKCS8KEY' }]);
    m.unmount();
  });

  it('not offered without a slot for this installation — and says why', async () => {
    const off = modal({ escrowState: 'off' });
    await nextTick();
    expect(off.q('fxe-unlock-escrow-toggle')).toBeNull();
    expect(off.q('fxe-unlock-escrow-note')).toBeNull();
    off.unmount();
    const pre = modal({ escrowState: 'predates' });
    await nextTick();
    expect(pre.q('fxe-unlock-escrow-toggle')).toBeNull();
    expect(pre.q('fxe-unlock-escrow-note'), 'not in front of everyone who types a password').toBeNull();
    pre.q('fxe-unlock-toggle')!.click();
    await nextTick();
    expect(pre.q('fxe-unlock-escrow-note')?.textContent, 'said where another way in is looked for').toContain('escrow');
    pre.unmount();
  });
});
