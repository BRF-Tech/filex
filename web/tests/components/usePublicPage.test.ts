// usePublicPage — the RETIRED `/p/<token>` walk, which is now the shared one
// (`usePublicLink`) pointed at the old root, so links already in people's
// mailboxes keep opening.
//
// info → PIN (401 wrong, 429 locked) → view → events; a 202 is "received",
// `done` is "finished", 404/410 are "not available". No session anywhere.
//
// ⚠ The status a drawable body has is `ready`, not `surface`: a share's body
// is a document or a folder at least as often as it is a plugin's screen, and
// there is one set of status names for all three now (v3 §1).
import { describe, expect, it, vi } from 'vitest';

import { publicPageClient, publicPageUrl, usePublicPage, PublicPageError } from '@brftech/filex-core/src/composables/usePublicPage';
import type { PluginSurface } from '@brftech/filex-core/src/types/Plugins';

function answer(status: number, body: unknown) {
  return { ok: status >= 200 && status < 300, status, statusText: '', json: async () => body, text: async () => JSON.stringify(body) };
}

const locked = { plugin: 'sign', page: 'sign', title: { en: 'Sign the NDA', tr: 'Gizlilik sözleşmesini imzala' }, subject: 'NDA.pdf', needs_pin: true, unlocked: false, app: { plugin: 'sign', page: 'sign', files: [] } };
const open = { ...locked, unlocked: true, app: { plugin: 'sign', page: 'sign', files: [{ ref: 'pub:0', name: 'NDA.pdf', size: 1234, mime: 'application/pdf' }] } };
const surface: PluginSurface = {
  title: { en: 'Sign' },
  state: { step: 1 },
  nodes: [{ id: 'ok', type: 'text', props: { text: 'Hello' } }],
  actions: [{ id: 'sign', label: { en: 'Sign' }, primary: true }],
};

function store(fetchMock: ReturnType<typeof vi.fn>) {
  return usePublicPage('tok123', { locale: () => 'tr', errorText: () => 'err', fetchImpl: fetchMock as unknown as typeof fetch });
}

describe('publicPageClient', () => {
  it('builds the five routes on the same origin and encodes the ref', () => {
    expect(publicPageUrl('', 'abc')).toBe('/api/p/abc');
    expect(publicPageUrl('https://x.example/', 'abc', '/view')).toBe('https://x.example/api/p/abc/view');
    const c = publicPageClient('abc', { locale: () => 'en' });
    expect(c.fileUrl('pub:0')).toBe('/api/p/abc/file/pub%3A0');
  });

  it('sends the visitor’s language, same-origin credentials, and never caches', async () => {
    const f = vi.fn(async () => answer(200, open));
    const c = publicPageClient('abc', { locale: () => 'tr', fetchImpl: f as unknown as typeof fetch });
    await c.info();
    expect(f).toHaveBeenCalledWith('/api/p/abc', expect.objectContaining({ method: 'GET', credentials: 'same-origin', cache: 'no-store' }));
    const init = f.mock.calls[0][1] as RequestInit;
    expect((init.headers as Record<string, string>)['Accept-Language']).toBe('tr');
    expect(init.body).toBeUndefined();
  });

  it('a refusal is a PublicPageError with the status and the server’s code', async () => {
    const f = vi.fn(async () => answer(410, { error: 'gone' }));
    const c = publicPageClient('abc', { locale: () => 'en', fetchImpl: f as unknown as typeof fetch });
    await expect(c.info()).rejects.toMatchObject({ status: 410, code: 'gone' });
    await expect(c.info()).rejects.toBeInstanceOf(PublicPageError);
  });

  it('a 202 from /event is the "job queued" branch, not a surface', async () => {
    const f = vi.fn(async () => answer(202, { accepted: true, job_id: 'j1' }));
    const c = publicPageClient('abc', { locale: () => 'en', fetchImpl: f as unknown as typeof fetch });
    const r = await c.event({ event: 'submit', action_id: 'sign', state: {}, data: { values: {} } });
    expect(r).toEqual({ op: { accepted: true, job_id: 'j1' }, job_id: 'j1' });
    expect(f.mock.calls[0][0]).toBe('/api/p/abc/event');
    expect(JSON.parse((f.mock.calls[0][1] as RequestInit).body as string)).toEqual({ event: 'submit', action_id: 'sign', state: {}, data: { values: {} } });
  });
});

describe('usePublicPage', () => {
  it('info → PIN form → wrong → locked → right → view → surface → 202 accepted', async () => {
    const f = vi.fn();
    f.mockResolvedValueOnce(answer(200, locked));
    const s = store(f);
    await s.load();
    expect(s.status.value).toBe('pin');
    expect(s.info.value?.title?.tr).toBe('Gizlilik sözleşmesini imzala');

    f.mockResolvedValueOnce(answer(401, { error: 'pin_wrong' }));
    await s.submitPin('0000');
    expect(s.status.value).toBe('pin');
    expect(s.pinFailure.value).toBe('wrong');
    expect(f.mock.calls[1][0]).toBe('/api/p/tok123/pin');
    expect(JSON.parse((f.mock.calls[1][1] as RequestInit).body as string)).toEqual({ pin: '0000' });

    f.mockResolvedValueOnce(answer(429, { error: 'locked', message: 'too many wrong PINs; try again in a few minutes' }));
    await s.submitPin('0001');
    expect(s.pinFailure.value).toBe('locked');
    expect(s.lockMessage.value).toContain('too many');

    f.mockResolvedValueOnce(answer(200, open));
    f.mockResolvedValueOnce(answer(200, { surface }));
    await s.submitPin(' 1234 ');
    expect(JSON.parse((f.mock.calls[3][1] as RequestInit).body as string)).toEqual({ pin: '1234' });
    expect(f.mock.calls[4][0]).toBe('/api/p/tok123/event');
    expect(s.status.value).toBe('ready');
    expect(s.pinFailure.value).toBe('');
    expect(s.conv.current.value?.nodes[0].id).toBe('ok');
    expect(s.info.value?.app?.files?.[0].ref).toBe('pub:0');
    expect(s.resolveFile('pub:0')).toEqual({ url: '/api/p/tok123/file/pub%3A0', name: 'NDA.pdf', mime: 'application/pdf' });

    f.mockResolvedValueOnce(answer(202, { accepted: true, job_id: 'j9' }));
    await s.conv.press(surface.actions![0]);
    expect(f.mock.calls[5][0]).toBe('/api/p/tok123/event');
    expect(JSON.parse((f.mock.calls[5][1] as RequestInit).body as string)).toMatchObject({ event: 'submit', action_id: 'sign', state: { step: 1 } });
    expect(s.status.value).toBe('accepted');
    expect(s.jobId.value).toBe('j9');
  });

  it('an unlocked page goes straight to the surface; `done` is the completion state', async () => {
    const f = vi.fn();
    f.mockResolvedValueOnce(answer(200, { ...open, needs_pin: false }));
    f.mockResolvedValueOnce(answer(200, { surface }));
    const s = store(f);
    await s.load();
    expect(s.status.value).toBe('ready');
    f.mockResolvedValueOnce(answer(200, { surface: { ...surface, done: true, toast: { en: 'Thanks', tr: 'Teşekkürler' } } }));
    await s.conv.press(surface.actions![0]);
    expect(s.status.value).toBe('done');
    expect(s.toast.value).toBe('Teşekkürler');
  });

  it('an open that answers 401 pin_required falls back to the PIN form (cookie gone)', async () => {
    const f = vi.fn();
    f.mockResolvedValueOnce(answer(200, { ...open, needs_pin: true, unlocked: true }));
    f.mockResolvedValueOnce(answer(401, { error: 'pin_required' }));
    const s = store(f);
    await s.load();
    expect(s.status.value).toBe('pin');
  });

  it('410 is gone, 404 is not found, anything else is an error with the words', async () => {
    const gone = store(vi.fn(async () => answer(410, { error: 'gone' })));
    await gone.load();
    expect(gone.status.value).toBe('gone');
    const missing = store(vi.fn(async () => answer(404, { error: 'not_found' })));
    await missing.load();
    expect(missing.status.value).toBe('not_found');
    const broken = store(vi.fn(async () => answer(500, { error: 'boom' })));
    await broken.load();
    expect(broken.status.value).toBe('error');
    expect(broken.failure.value).toBe('boom');
  });
});
