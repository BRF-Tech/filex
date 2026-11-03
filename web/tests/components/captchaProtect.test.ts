// The bot check's one rule for every form (#294): composables/useCaptcha
// `protect`, and the two public forms that send by it (a link's PIN, a file
// request's upload pass).
//
//   token          sent once, the widget starts over after EVERY send
//   409 fallback   solved in the worker, the same request sent once more
//                  with `captcha_local`
//   422            the server's sentence under the widget, the widget anew,
//                  and NO fallback (it is for an outage only)
//   script blocked the server is asked for a challenge; it has one only when
//                  it cannot reach the provider either
//
// The worker is the shipped text run in this thread (helpers/captchaWorker);
// the challenge is a vector from the shared file, so the solution sent is
// the one the server's own test accepts.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import { useCaptcha, useCaptchaConfig } from '@brftech/filex-core/src/composables/useCaptcha';
import { PublicLinkError, publicCaptchaReply, usePublicRequest, usePublicShare } from '@brftech/filex-core/src/composables/usePublicLink';
import CaptchaWidget from '@brftech/filex-core/src/components/CaptchaWidget.vue';
import PublicPinGate from '@brftech/filex-core/src/components/public/PublicPinGate.vue';
import type { CaptchaChallenge, CaptchaFields, CaptchaPublicConfig } from '@brftech/filex-core/src/lib/captcha';
import { en } from '@brftech/filex-core/src/locales/en';
import { inThreadWorker } from '../helpers/captchaWorker';

const vectors = JSON.parse(
  readFileSync(resolve(__dirname, '../../../backend/internal/captcha/testdata/captcha-local-vectors.json'), 'utf8'),
) as { vectors: Array<{ id: string; challenge: CaptchaChallenge; solution: { payload_base64: string } }> };
const P2 = vectors.vectors.find((v) => v.id === 'P2')!;

function config(over: Partial<CaptchaPublicConfig> = {}): CaptchaPublicConfig {
  return {
    provider: 'turnstile',
    site_key: '1x00000000000000000000AA',
    script: 'https://challenges.cloudflare.com/turnstile/v0/api.js',
    fallback: 'local',
    forms: ['login', 'demo_login', 'share_password', 'file_request'],
    misconfigured: false,
    fallback_active: false,
    local_field: 'captcha_local',
    token_field: 'captcha_token',
    ...over,
  };
}

/** Turnstile as far as the composable uses it; each render/reset hands out the next token. */
function fakeTurnstile(opts: { auto?: boolean } = {}) {
  let n = 0;
  const widgets = new Map<string, Record<string, (...a: unknown[]) => unknown>>();
  const t = {
    renders: [] as Record<string, unknown>[],
    resets: 0,
    removed: 0,
    deliver(id = 'w1') {
      widgets.get(id)?.callback(`tok-${++n}`);
    },
    fail(id = 'w1') {
      widgets.get(id)?.['error-callback']();
    },
    render(_el: HTMLElement, o: Record<string, unknown>) {
      const id = `w${widgets.size + 1}`;
      widgets.set(id, o as Record<string, (...a: unknown[]) => unknown>);
      t.renders.push(o);
      if (opts.auto !== false) queueMicrotask(() => t.deliver(id));
      return id;
    },
    reset(id: string) {
      t.resets++;
      if (opts.auto !== false) queueMicrotask(() => t.deliver(id));
    },
    remove(id: string) {
      t.removed++;
      widgets.delete(id);
    },
  };
  return t;
}

type Route = (init?: RequestInit) => { status: number; body: unknown };
function server(routes: Record<string, Route>) {
  const calls: Array<{ url: string; body: unknown }> = [];
  const f = vi.fn(async (url: string, init?: RequestInit) => {
    const path = new URL(url, 'http://x').pathname;
    calls.push({ url: path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    const r = routes[path];
    if (!r) return new Response('{}', { status: 404 });
    const { status, body } = r(init);
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  });
  return { fetchImpl: f as unknown as typeof fetch, calls };
}

function store(fetchImpl: typeof fetch, form: 'login' | 'share_password' | 'file_request' = 'login') {
  return useCaptcha({ form, locale: () => 'en', theme: () => 'dark', fetchImpl, createWorker: inThreadWorker, settleMs: 200 });
}

/** A send that answers like a public call: the replies in order, then 200. */
function sends(...replies: Array<{ status: number; body: Record<string, unknown> }>) {
  const seen: CaptchaFields[] = [];
  const fn = async (f: CaptchaFields) => {
    seen.push(f);
    const r = replies.shift();
    if (r) throw new PublicLinkError(r.status, String(r.body.error ?? ''), String(r.body.message ?? ''), r.body);
    return 'ok';
  };
  return { fn, seen };
}

let turnstile: ReturnType<typeof fakeTurnstile>;
beforeEach(() => {
  turnstile = fakeTurnstile();
  (window as unknown as { turnstile: unknown }).turnstile = turnstile;
});
afterEach(() => {
  delete (window as unknown as { turnstile?: unknown }).turnstile;
});

describe('a form that is not protected', () => {
  it('sends as it is, with nothing added, and draws no widget', async () => {
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config({ provider: 'none', forms: [] }) }) });
    const c = store(s.fetchImpl);
    const send = sends();
    await expect(c.protect(send.fn, publicCaptchaReply)).resolves.toBe('ok');
    expect(send.seen).toEqual([{}]);
    expect(c.active.value).toBe(false);
    expect(c.state.value).toBe('off');
  });

  it('a form the server does not list is not protected either', async () => {
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config({ forms: ['login'] }) }) });
    const c = store(s.fetchImpl, 'file_request');
    await c.load();
    expect(c.active.value).toBe(false);
  });

  it('an older server with no config route is the check switched off', async () => {
    const s = server({});
    const c = store(s.fetchImpl);
    await c.load();
    expect(c.active.value).toBe(false);
  });
});

describe('the widget', () => {
  it('is drawn with the page’s key, mode and language, in the flexible size', async () => {
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config() }) });
    const c = store(s.fetchImpl);
    await c.attach(document.createElement('div'));
    expect(turnstile.renders[0]).toMatchObject({ sitekey: '1x00000000000000000000AA', theme: 'dark', language: 'en', size: 'flexible' });
    await flushPromises();
    expect(c.state.value).toBe('solved');
  });

  it('a token is good for ONE request: the widget starts over after every send, whatever the answer', async () => {
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config() }) });
    const c = store(s.fetchImpl);
    await c.attach(document.createElement('div'));
    await flushPromises();
    const send = sends({ status: 401, body: { error: 'pin_wrong' } });
    await expect(c.protect(send.fn, publicCaptchaReply)).rejects.toMatchObject({ status: 401 });
    await flushPromises();
    await c.protect(send.fn, publicCaptchaReply);
    expect(send.seen).toEqual([{ captcha_token: 'tok-1' }, { captcha_token: 'tok-2' }]);
    expect(turnstile.resets).toBe(2);
    // a wrong PIN is not the check's to talk about
    expect(c.message.value).toBe('');
  });

  it('waits for a token still on its way instead of sending without one', async () => {
    turnstile = fakeTurnstile({ auto: false });
    (window as unknown as { turnstile: unknown }).turnstile = turnstile;
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config() }) });
    const c = useCaptcha({ form: 'login', locale: () => 'en', fetchImpl: s.fetchImpl, settleMs: 5000 });
    await c.attach(document.createElement('div'));
    const send = sends();
    const p = c.protect(send.fn, publicCaptchaReply);
    await flushPromises();
    expect(send.seen).toEqual([]);
    turnstile.deliver();
    await p;
    expect(send.seen).toEqual([{ captcha_token: 'tok-1' }]);
  });

  it('is taken away when its form goes', async () => {
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config() }) });
    const c = store(s.fetchImpl);
    await c.attach(document.createElement('div'));
    c.detach();
    expect(turnstile.removed).toBe(1);
  });
});

describe('the server cannot reach the provider (409 fallback)', () => {
  it('solves the challenge and sends the same request once more, with captcha_local', async () => {
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config() }) });
    const c = store(s.fetchImpl);
    await c.attach(document.createElement('div'));
    await flushPromises();
    const send = sends({ status: 409, body: { error: 'captcha_fallback', captcha: 'fallback', challenge: P2.challenge } });
    await expect(c.protect(send.fn, publicCaptchaReply)).resolves.toBe('ok');
    expect(send.seen).toHaveLength(2);
    expect(send.seen[0]).toEqual({ captcha_token: 'tok-1' });
    // the vector's own solution: what the server's test accepts
    expect(send.seen[1]).toEqual({ captcha_local: P2.solution.payload_base64 });
    expect(c.solveMs.value).not.toBeNull();
  });

  it('sends ONCE more, not in a loop, when the second answer is a fallback again', async () => {
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config() }) });
    const c = store(s.fetchImpl);
    await c.attach(document.createElement('div'));
    await flushPromises();
    const again = { status: 409, body: { error: 'captcha_fallback', captcha: 'fallback', challenge: P2.challenge } };
    const send = sends(again, again);
    await expect(c.protect(send.fn, publicCaptchaReply)).rejects.toMatchObject({ status: 409 });
    expect(send.seen).toHaveLength(2);
  });
});

describe('the check said no (422)', () => {
  it('shows the server’s sentence, starts the widget over, and does not fall back', async () => {
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config() }) });
    const c = store(s.fetchImpl);
    await c.attach(document.createElement('div'));
    await flushPromises();
    const send = sends({ status: 422, body: { error: 'captcha_failed', captcha: 'invalid_token', message: 'Doğrulama başarısız.' } });
    await expect(c.protect(send.fn, publicCaptchaReply)).rejects.toMatchObject({ status: 422 });
    expect(send.seen).toHaveLength(1);
    expect(c.message.value).toBe('Doğrulama başarısız.');
    expect(c.messageKind.value).toBe('error');
    expect(turnstile.resets).toBeGreaterThanOrEqual(1);
    expect(s.calls.some((x) => x.url.endsWith('/local/challenge'))).toBe(false);
  });

  it('a sentence-less refusal reads the catalogue’s', async () => {
    const s = server({ '/api/captcha/config': () => ({ status: 200, body: config() }) });
    const c = store(s.fetchImpl);
    await c.attach(document.createElement('div'));
    await flushPromises();
    const send = sends({ status: 422, body: { captcha: 'missing_token' } });
    await expect(c.protect(send.fn, publicCaptchaReply)).rejects.toBeTruthy();
    expect(c.message.value).toBe(en['captcha.invalid']);
  });
});

describe('the widget cannot load here', () => {
  it('a blocked script: the server can reach the provider, so no fallback - the person is told', async () => {
    delete (window as unknown as { turnstile?: unknown }).turnstile;
    const s = server({
      '/api/captcha/config': () => ({ status: 200, body: config({ script: 'https://blocked.invalid/api.js' }) }),
      '/api/captcha/local/challenge': () => ({ status: 409, body: { captcha: 'provider' } }),
    });
    const c = store(s.fetchImpl);
    const el = document.createElement('div');
    const head = vi.spyOn(document.head, 'appendChild').mockImplementation((node) => {
      queueMicrotask(() => (node as HTMLScriptElement).onerror?.(new Event('error')));
      return node;
    });
    await c.attach(el);
    head.mockRestore();
    expect(c.state.value).toBe('error');
    expect(c.message.value).toBe(en['captcha.widget_failed']);
  });

  it('a misconfigured server: no widget, the challenge straight away, solved before the send', async () => {
    const s = server({
      '/api/captcha/config': () => ({ status: 200, body: config({ misconfigured: true }) }),
      '/api/captcha/local/challenge': () => ({ status: 200, body: { captcha: 'fallback', challenge: P2.challenge } }),
    });
    const c = store(s.fetchImpl);
    await c.attach(document.createElement('div'));
    expect(turnstile.renders).toHaveLength(0);
    expect(c.state.value).toBe('solved');
    const send = sends();
    await c.protect(send.fn, publicCaptchaReply);
    expect(send.seen).toEqual([{ captcha_local: P2.solution.payload_base64 }]);
    // spent: the next send asks for a new challenge first
    await c.protect(send.fn, publicCaptchaReply);
    expect(s.calls.filter((x) => x.url.endsWith('/local/challenge'))).toHaveLength(2);
  });
});

describe('a link’s PIN box (share_password)', () => {
  function shareServer(pin: () => { status: number; body: unknown }) {
    return server({
      '/api/captcha/config': () => ({ status: 200, body: config() }),
      '/api/public/s/tok': () => ({ status: 200, body: { kind: 'file', needs_pin: true, unlocked: false } }),
      '/api/public/s/tok/pin': pin,
    });
  }

  it('sends the PIN with the token, and a wrong PIN starts the widget over', async () => {
    const s = shareServer(() => ({ status: 401, body: { error: 'pin_wrong' } }));
    const pinCaptcha = store(s.fetchImpl, 'share_password');
    const link = usePublicShare('tok', { locale: () => 'en', errorText: () => 'err', fetchImpl: s.fetchImpl, captcha: { pin: pinCaptcha } });
    await link.load();
    await pinCaptcha.attach(document.createElement('div'));
    await flushPromises();
    await link.submitPin(' 1234 ');
    expect(s.calls.find((x) => x.url.endsWith('/pin'))?.body).toEqual({ pin: '1234', captcha_token: 'tok-1' });
    expect(link.pinFailure.value).toBe('wrong');
    expect(turnstile.resets).toBe(1);
  });

  it('a refusal of the CHECK is not a wrong PIN', async () => {
    const s = shareServer(() => ({ status: 422, body: { error: 'captcha_required', captcha: 'missing_token', message: 'Verify first.' } }));
    const pinCaptcha = store(s.fetchImpl, 'share_password');
    const link = usePublicShare('tok', { locale: () => 'en', errorText: () => 'err', fetchImpl: s.fetchImpl, captcha: { pin: pinCaptcha } });
    await link.load();
    await link.submitPin('1234');
    expect(link.pinFailure.value).toBe('');
    expect(link.status.value).toBe('pin');
    expect(pinCaptcha.message.value).toBe('Verify first.');
  });

  it('the gate draws the widget between the box and the button when the form is protected', async () => {
    const s = shareServer(() => ({ status: 200, body: {} }));
    const pinCaptcha = store(s.fetchImpl, 'share_password');
    await pinCaptcha.load();
    const w = mount(PublicPinGate, { props: { locale: 'en', captcha: pinCaptcha } });
    await flushPromises();
    const box = w.find('[data-testid="public-page-pin-input"]').element;
    const check = w.find('[data-testid="captcha"]');
    const button = w.find('[data-testid="public-page-pin-submit"]').element;
    expect(check.exists()).toBe(true);
    expect(box.compareDocumentPosition(check.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(check.element.compareDocumentPosition(button) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(check.attributes('data-captcha-form')).toBe('share_password');
    w.unmount();
  });
});

describe('a file request’s drop (file_request)', () => {
  class FakeXHR {
    static last: FakeXHR | null = null;
    headers: Record<string, string> = {};
    upload = { onprogress: null as unknown, onload: null as (() => void) | null };
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    status = 0;
    responseText = '';
    withCredentials = false;
    open() {}
    setRequestHeader(k: string, v: string) {
      this.headers[k] = v;
    }
    send() {
      FakeXHR.last = this;
      queueMicrotask(() => {
        this.status = 200;
        this.responseText = '{"ok":true}';
        this.onload?.();
      });
    }
  }
  beforeEach(() => {
    FakeXHR.last = null;
    vi.stubGlobal('XMLHttpRequest', FakeXHR);
  });
  afterEach(() => vi.unstubAllGlobals());

  function dropServer(pass: () => { status: number; body: unknown }) {
    return server({
      '/api/captcha/config': () => ({ status: 200, body: config() }),
      '/api/public/d/tok': () => ({ status: 200, body: { kind: 'request', limits: {} } }),
      '/api/public/d/tok/captcha': pass,
    });
  }

  it('asks for the pass right before the upload and carries it in X-Captcha-Pass', async () => {
    const s = dropServer(() => ({ status: 200, body: { pass: 'p-1', expires_at: '2026-11-03T12:00:00Z' } }));
    const up = store(s.fetchImpl, 'file_request');
    const d = usePublicRequest('tok', { locale: () => 'en', errorText: () => 'err', fetchImpl: s.fetchImpl, captcha: { upload: up } });
    await d.load();
    await up.attach(document.createElement('div'));
    await flushPromises();
    await d.upload([new File(['x'], 'a.pdf')]);
    expect(s.calls.find((x) => x.url.endsWith('/captcha'))?.body).toEqual({ captcha_token: 'tok-1' });
    expect(FakeXHR.last?.headers['X-Captcha-Pass']).toBe('p-1');
    expect(d.uploads.value[0].state).toBe('done');
  });

  it('a refused check sends nothing and leaves no failed rows (its sentence is under the widget)', async () => {
    const s = dropServer(() => ({ status: 422, body: { error: 'captcha_failed', captcha: 'invalid_token', message: 'No.' } }));
    const up = store(s.fetchImpl, 'file_request');
    const d = usePublicRequest('tok', { locale: () => 'en', errorText: () => 'err', fetchImpl: s.fetchImpl, captcha: { upload: up } });
    await d.load();
    await up.attach(document.createElement('div'));
    await flushPromises();
    await d.upload([new File(['x'], 'a.pdf')]);
    expect(FakeXHR.last).toBeNull();
    expect(d.uploads.value).toEqual([]);
    expect(up.message.value).toBe('No.');
  });

  it('an unprotected drop box asks for no pass at all', async () => {
    const s = server({
      '/api/captcha/config': () => ({ status: 200, body: config({ forms: ['login'] }) }),
      '/api/public/d/tok': () => ({ status: 200, body: { kind: 'request', limits: {} } }),
    });
    const up = store(s.fetchImpl, 'file_request');
    const d = usePublicRequest('tok', { locale: () => 'en', errorText: () => 'err', fetchImpl: s.fetchImpl, captcha: { upload: up } });
    await d.load();
    await d.upload([new File(['x'], 'a.pdf')]);
    expect(s.calls.some((x) => x.url.endsWith('/captcha'))).toBe(false);
    expect(FakeXHR.last?.headers['X-Captcha-Pass']).toBeUndefined();
  });
});

describe('CaptchaWidget', () => {
  it('leaves how long the fallback took on the element, for the end-to-end timing', async () => {
    const s = server({
      '/api/captcha/config': () => ({ status: 200, body: config({ misconfigured: true }) }),
      '/api/captcha/local/challenge': () => ({ status: 200, body: { captcha: 'fallback', challenge: P2.challenge } }),
    });
    const conf = useCaptchaConfig({ fetchImpl: s.fetchImpl });
    const c = useCaptcha({ form: 'login', locale: () => 'en', fetchImpl: s.fetchImpl, config: conf, createWorker: inThreadWorker });
    await c.load();
    const w = mount(CaptchaWidget, { props: { captcha: c, locale: 'en' } });
    await vi.waitFor(() => expect(w.find('[data-testid="captcha"]').attributes('data-captcha-state')).toBe('solved'));
    expect(w.find('[data-testid="captcha"]').attributes('data-captcha-solve-ms')).toMatch(/^\d+$/);
    expect(w.find('[role="status"]').text()).toBe(en['captcha.fallback_done']);
    w.unmount();
  });
});
