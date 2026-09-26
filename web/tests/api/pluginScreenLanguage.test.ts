// Every app call names the language the screen is drawn in (`?lang=`).
//
// An embedded explorer (`<filex-explorer>` with `config.locale: 'tr'`) draws
// Turkish for whoever the host page signed in — including an account whose
// own language is English. The server answered app screens in the ACCOUNT's
// language (Accept-Language ranks below it on purpose: for any other client
// it is only the language the browser was installed in), and an app picks
// its plain strings — a form field's label and help, a select's options — by
// that language. So the signing app's Turkish popup asked "Identity" with an
// English help line under it (2026-09-26). The screen's language has to reach
// the server as an explicit choice, on the opening GET, on every event and on
// a run: backend pluginLang reads it first.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { usePendingOps } from '@brftech/filex-core/src/composables/usePendingOps';

afterEach(() => vi.unstubAllGlobals());

function record() {
  const asked: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      asked.push(url);
      return new Response(JSON.stringify({ surface: { nodes: [] } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
  return asked;
}

function langOf(url: string): string | null {
  return new URL(url, 'https://x.invalid').searchParams.get('lang');
}

describe('app calls carry the language on screen', () => {
  it('names it on the opening view, every event and a run', async () => {
    const asked = record();
    const api = useFileApi({
      apiBase: 'https://files.example.com',
      auth: { kind: 'bearer', token: 't' },
      locale: 'tr',
    });
    await api.pluginView('sign', 'request', 'docs://teklif.docx');
    await api.pluginView('sign', 'envelopes', undefined, 'requested');
    await api.pluginViewEvent('sign', 'request', { event: 'submit', action_id: 'convert', state: {} });
    await api.pluginActionRun('sign', 'request', { paths: ['docs://teklif.docx'] });

    expect(asked).toHaveLength(4);
    for (const url of asked) expect(langOf(url), url).toBe('tr');
    // The view's own query is kept.
    expect(new URL(asked[0]).searchParams.get('path')).toBe('docs://teklif.docx');
    expect(new URL(asked[1]).searchParams.get('section')).toBe('requested');
  });

  it('follows the screen, not the browser', async () => {
    const asked = record();
    const api = useFileApi({ apiBase: '', locale: 'en' });
    await api.pluginViewEvent('sign', 'request', { event: 'open', state: {} });
    expect(langOf(asked[0])).toBe('en');
  });

  it("appends to a host's own endpoint template that already has a query", async () => {
    const asked = record();
    const api = useFileApi({
      apiBase: '',
      locale: 'tr',
      pluginViewEvent: '/proxy/filex?route=views/{plugin}/{view}/event',
    });
    await api.pluginViewEvent('sign', 'request', { event: 'open', state: {} });
    const u = new URL(asked[0], 'https://x.invalid');
    expect(u.searchParams.get('route')).toBe('views/sign/request/event');
    expect(u.searchParams.get('lang')).toBe('tr');
  });

  // The operations tray reads an app job's label and result in the reader's
  // language (backend wasmplugin/jobtext.go): the tray's poll names it too.
  it('names it on the operations tray’s poll', async () => {
    const asked: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        asked.push(url);
        return new Response(JSON.stringify({ ops: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }),
    );
    const config = { apiBase: 'https://files.example.com', locale: 'tr' as const };
    const tray = usePendingOps(config, useFileApi(config));
    tray.startPolling();
    await vi.waitFor(() => expect(asked.length).toBeGreaterThan(0));
    tray.stopPolling();
    expect(new URL(asked[0]).pathname).toBe('/api/files/ops');
    expect(langOf(asked[0])).toBe('tr');
  });
});
