// The ONE public shell (v3 §1), state by state and body by body.
//
// ⚠⚠ What this file is really guarding: a share, a file request and an app
// plugin's page used to be two rendering stacks and three layouts, so the PIN
// box of a signature request looked nothing like the PIN box of a download
// and neither carried the instance's own name or colour. Every assertion
// below that names a `data-testid` shared between the three kinds is there to
// make a future split visible — if one of them starts drawing its own
// heading, its own PIN form or its own "link is gone", it stops matching.
//
// Nothing here asks for a session: every request a public page makes goes to
// `/api/public/…` (plus the branding fetch) and nowhere else.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';

import PublicLink from '@/views/public/PublicLink.vue';
import { resetLocales } from '@brftech/filex-core';

function answer(status: number, body: unknown) {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: '',
    json: async () => body,
    text: async () => JSON.stringify(body),
  };
}

/** A fetch that answers by URL, so the order of the page's calls is not a test fixture. */
function router(routes: Record<string, unknown>, fallback: number = 404) {
  return vi.fn(async (url: string) => {
    for (const [key, body] of Object.entries(routes)) {
      if (url === key) return answer(200, body);
    }
    return answer(fallback, { error: 'not_found' });
  });
}

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  resetLocales();
  try {
    localStorage.clear();
  } catch {
    /* jsdom always has it; a private window would not */
  }
});
afterEach(() => vi.unstubAllGlobals());

function open(kind: 'share' | 'request', token = 'tok123') {
  return mount(PublicLink, { props: { kind, token } });
}

describe('the public shell — one frame for every link', () => {
  it('wears the instance’s own name, logo and accent, and says so in the footer', async () => {
    fetchMock.mockImplementation(
      router({
        '/api/public/branding': {
          name: 'Acme Files',
          logo_url: 'https://acme.example/logo.png',
          accent: '#1f8a4c',
          footer_text: 'Acme Inc.',
          hide_powered_by: false,
        },
        '/api/public/s/tok123': {
          kind: 'file',
          needs_pin: false,
          unlocked: true,
          node: { name: 'contract.pdf', size: 2048, mime: 'application/pdf' },
        },
      }),
    );
    const w = open('share');
    await vi.waitFor(() => expect(w.find('[data-testid="public-share-file"]').exists()).toBe(true));

    // ⚠ Whitelabel means whitelabel: the page a stranger is sent does not say
    // "filex" anywhere once the instance has renamed itself.
    expect(w.find('[data-testid="public-brand"]').text()).toContain('Acme Files');
    expect(w.find('[data-testid="public-brand"] img').attributes('src')).toBe('https://acme.example/logo.png');
    expect(w.find('[data-testid="public-footer-powered"]').text()).toBe('Served by Acme Files');
    expect(w.find('[data-testid="public-footer-text"]').text()).toBe('Acme Inc.');
    expect(w.text()).not.toContain('filex');

    // The accent lands on the SHELL, not on :root — the same component is
    // mounted inside other pages and must not repaint its host.
    const style = w.find('[data-testid="public-page"]').attributes('style') ?? '';
    expect(style).toContain('--fe-primary: #1f8a4c');
    expect(style).toContain('--fe-text-on-primary');
  });

  it('the bytes come from /s/<token>, not from a second download route', async () => {
    // ⚠⚠ `/s/<token>` has always served the file and still does: the SPA is
    // what a JavaScript browser gets when it NAVIGATES there, and
    // `?download=1` / `?zip=1` are the escapes that mean "the thing, not the
    // page" (handlers/public_shell.go). A download route under `/api/` would
    // be a second thing to keep working.
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': {
          kind: 'file',
          needs_pin: false,
          unlocked: true,
          node: { name: 'contract.pdf', size: 2048 },
        },
      }),
    );
    const file = open('share');
    await vi.waitFor(() => expect(file.find('[data-testid="public-share-download"]').exists()).toBe(true));
    expect(file.find('[data-testid="public-share-download"]').attributes('href')).toBe('/s/tok123?download=1');

    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': {
          kind: 'folder',
          needs_pin: false,
          unlocked: true,
          node: { name: 'Invoices' },
        },
      }),
    );
    const folder = open('share');
    await vi.waitFor(() => expect(folder.find('[data-testid="public-share-folder"]').exists()).toBe(true));
    expect(folder.find('[data-testid="public-share-zip"]').attributes('href')).toBe('/s/tok123?zip=1');
    // ⚠ The server sends no listing for a folder share yet, so the shell
    // offers the walk rather than pretending it has one. When `entries`
    // start arriving the list is drawn instead — see the next case.
    expect(folder.find('[data-testid="public-share-browse"]').attributes('href')).toBe('/s/tok123?nojs=1');
  });

  it('draws a folder listing the moment the server sends one', async () => {
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': {
          kind: 'folder',
          needs_pin: false,
          unlocked: true,
          node: { name: 'Invoices' },
          path: '',
          entries: [
            { name: '2026', path: '2026', is_dir: true },
            { name: 'note.txt', path: 'note.txt', is_dir: false, size: 12 },
          ],
        },
        '/api/public/s/tok123?path=2026': {
          kind: 'folder',
          needs_pin: false,
          unlocked: true,
          node: { name: 'Invoices' },
          path: '2026',
          entries: [{ name: 'jan.pdf', path: '2026/jan.pdf', is_dir: false, size: 900 }],
        },
      }),
    );
    const w = open('share');
    await vi.waitFor(() => expect(w.find('[data-testid="public-share-entries"]').exists()).toBe(true));
    expect(w.find('[data-testid="public-entry-note.txt"]').attributes('href')).toBe('/s/tok123/f/note.txt?download=1');

    await w.find('[data-testid="public-entry-2026"]').trigger('click');
    await vi.waitFor(() => expect(w.find('[data-testid="public-entry-jan.pdf"]').exists()).toBe(true));
    // Walking in is a reload of the SAME link at another path — one token,
    // one expiry, one visit counter.
    expect(fetchMock.mock.calls.map((c) => c[0])).toContain('/api/public/s/tok123?path=2026');
  });

  it('the PIN gate is the shell’s: one wording, one lockout, for every kind', async () => {
    fetchMock.mockImplementation(
      router({ '/api/public/d/tok123': { kind: 'drop', folder: 'Invoices', needs_pin: true, unlocked: false } }),
    );
    const w = open('request');
    await vi.waitFor(() => expect(w.find('[data-testid="public-page-pin"]').exists()).toBe(true));
    expect(w.find('[data-testid="public-page"]').attributes('data-state')).toBe('pin');

    fetchMock.mockImplementationOnce(async () => answer(401, { error: 'pin_wrong' }));
    await w.find('[data-testid="public-page-pin-input"]').setValue('0000');
    await w.find('[data-testid="public-page-pin"]').trigger('submit');
    await vi.waitFor(() => expect(w.find('[data-testid="public-page-pin-error"]').exists()).toBe(true));
    expect(w.find('[data-testid="public-page-pin-error"]').text()).toBe('That PIN is not right');
    // A rejected code is cleared, so the next attempt starts from empty.
    expect((w.find('[data-testid="public-page-pin-input"]').element as HTMLInputElement).value).toBe('');

    fetchMock.mockImplementationOnce(async () =>
      answer(429, { error: 'locked', message: 'too many wrong PINs' }),
    );
    await w.find('[data-testid="public-page-pin-input"]').setValue('9999');
    await w.find('[data-testid="public-page-pin"]').trigger('submit');
    await vi.waitFor(() =>
      expect(w.find('[data-testid="public-page-pin-error"]').text()).toContain('Too many wrong PINs'),
    );
    expect((w.find('[data-testid="public-page-pin-input"]').element as HTMLInputElement).disabled).toBe(true);
  });

  it('a file request drops files and reports each one’s progress', async () => {
    fetchMock.mockImplementation(
      router({
        '/api/public/d/tok123': {
          kind: 'drop',
          folder: 'Invoices',
          needs_pin: false,
          unlocked: true,
          uploads_left: 2,
          limits: { max_files: 5, max_file_size_mb: 5, allowed_ext: ['pdf'], ask_name: false },
        },
      }),
    );
    const sent: Array<Record<string, unknown>> = [];
    class FakeXHR {
      upload = { onprogress: null as ((e: ProgressEvent) => void) | null };
      status = 200;
      responseText = '{}';
      withCredentials = false;
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      open(method: string, url: string) {
        sent.push({ method, url });
      }
      setRequestHeader() {}
      send() {
        this.upload.onprogress?.({ lengthComputable: true, loaded: 5, total: 10 } as ProgressEvent);
        this.onload?.();
      }
    }
    vi.stubGlobal('XMLHttpRequest', FakeXHR as unknown as typeof XMLHttpRequest);

    const w = open('request');
    await vi.waitFor(() => expect(w.find('[data-testid="public-request-drop"]').exists()).toBe(true));
    // The limits are stated BEFORE the person picks, not after a two-minute
    // upload is refused.
    expect(w.find('[data-testid="public-request-limits"]').text()).toContain('PDF');
    expect(w.find('[data-testid="public-request-limits"]').text()).toContain('2 more files');

    const file = new File(['x'], 'invoice.pdf', { type: 'application/pdf' });
    await (w.findComponent({ name: 'PublicRequestBody' }) as never as { vm: unknown }) &&
      w.findComponent({ name: 'PublicRequestBody' }).vm.$emit('files', [file]);
    await vi.waitFor(() => expect(w.find('[data-testid="public-request-uploads"]').exists()).toBe(true));
    expect(sent[0]).toEqual({ method: 'POST', url: '/api/public/d/tok123/upload' });
    expect(w.find('[data-testid="public-request-uploads"]').text()).toContain('invoice.pdf');
  });

  it('an app’s page is a share, drawn by the same surface renderer', async () => {
    const surface = {
      title: { en: 'Sign the NDA' },
      nodes: [{ id: 'intro', type: 'text', props: { text: { en: 'Please sign below' } } }],
      actions: [{ id: 'sign', label: { en: 'Sign' }, primary: true }],
    };
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': {
          kind: 'app',
          needs_pin: false,
          unlocked: true,
          subject: 'NDA with Acme',
          app: {
            plugin: 'sign',
            page: 'sign',
            title: { en: 'Sign the NDA' },
            files: [{ ref: 'pub:0', name: 'NDA.pdf', size: 2048, mime: 'application/pdf' }],
          },
        },
        // ⚠ There is no `/view`: the opening screen is `POST /event
        // {"event":"open"}`, so the app has one code path for the first
        // surface and every later one.
        '/api/public/s/tok123/event': { surface },
      }),
    );
    const w = open('share');
    await vi.waitFor(() => expect(w.find('[data-testid="public-page-action-sign"]').exists()).toBe(true));
    expect(w.find('[data-testid="public-page-title"]').text()).toBe('Sign the NDA');
    expect(w.find('[data-testid="public-page-subject"]').text()).toBe('NDA with Acme');
    expect(w.text()).toContain('Please sign below');
    // The Download button asks for a DOWNLOAD (`?download=1`), which is the
    // only fetch of an exposed copy that counts as one — the page's viewer
    // loads the same copy without it (the owner, 2026-09-21: looking at a
    // signing link is not downloading it).
    expect(w.find('[data-testid="public-page-files"] a').attributes('href')).toBe(
      '/api/public/s/tok123/file/pub%3A0?download=1',
    );

    fetchMock.mockImplementationOnce(async () => answer(202, { accepted: true, job_id: 'j1' }));
    await w.find('[data-testid="public-page-action-sign"]').trigger('click');
    await vi.waitFor(() => expect(w.find('[data-testid="public-page-accepted"]').exists()).toBe(true));
  });

  it('expired, revoked, 404 and a URL with no token are the SAME sentence', async () => {
    // ⚠⚠ An expired link answers 200 with `expired: true`, NOT a 410 — a
    // client that only reads the status code would draw an empty download
    // page. And the four are deliberately indistinguishable to the visitor:
    // "expired" tells somebody guessing tokens that this one existed.
    fetchMock.mockImplementation(
      router({ '/api/public/s/tok123': { kind: 'file', needs_pin: false, unlocked: false, expired: true } }),
    );
    const gone = open('share');
    await vi.waitFor(() => expect(gone.find('[data-testid="public-page-unavailable"]').exists()).toBe(true));
    const words = gone.find('[data-testid="public-page-unavailable"]').text();

    fetchMock.mockImplementation(
      router({ '/api/public/s/tok123': { kind: 'file', needs_pin: true, unlocked: false, revoked: true } }),
    );
    const spent = open('share');
    await vi.waitFor(() => expect(spent.find('[data-testid="public-page-unavailable"]').exists()).toBe(true));
    expect(spent.find('[data-testid="public-page-unavailable"]').text()).toBe(words);
    // ...and a revoked link never shows its PIN box: there is nothing behind it.
    expect(spent.find('[data-testid="public-page-pin"]').exists()).toBe(false);

    fetchMock.mockImplementation(async () => answer(404, { error: 'not_found' }));
    const missing = open('share');
    await vi.waitFor(() => expect(missing.find('[data-testid="public-page-unavailable"]').exists()).toBe(true));
    expect(missing.find('[data-testid="public-page-unavailable"]').text()).toBe(words);

    const before = fetchMock.mock.calls.length;
    const none = open('share', '');
    await vi.waitFor(() => expect(none.find('[data-testid="public-page-unavailable"]').exists()).toBe(true));
    // No token, no LINK fetch. (The shell still asks for the branding it
    // paints with and the languages it offers — those are about the page,
    // not about a token nobody handed us.)
    expect(fetchMock.mock.calls.slice(before).map((c) => String(c[0])).filter((u) => u.includes('/api/public/s/'))).toEqual([]);
  });

  it('expiry and the visit count are said out loud, not discovered', async () => {
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': {
          kind: 'file',
          needs_pin: false,
          unlocked: true,
          node: { name: 'contract.pdf' },
          expires_at: '2030-01-02T03:04:05Z',
          visits_left: 1,
        },
      }),
    );
    const w = open('share');
    await vi.waitFor(() => expect(w.find('[data-testid="public-page-meta"]').exists()).toBe(true));
    const meta = w.find('[data-testid="public-page-meta"]').text();
    expect(meta).toContain('stops working');
    // ⚠ The singular form, not "1 visits left".
    expect(meta).toContain('1 visit left');
  });

  it('the language picker offers what the INSTANCE has, including an app’s language', async () => {
    // ⚠ The list rides on the BRANDING answer — the one public statement
    // about how this instance presents itself, and the one the shell already
    // fetches. A route of its own would be a second unauthenticated round
    // trip on every public page saying half of what this one says.
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': { kind: 'file', needs_pin: false, unlocked: true, node: { name: 'contract.pdf' } },
        // ⚠ The SERVER's shape: rows without strings on the branding answer,
        // the strings of one language on their own route (it used to be typed
        // here with inline strings — the browser's belief, never the wire).
        '/api/public/branding': {
          name: '',
          locales: ['ar', 'en', 'tr'],
          ui_locales: [{ code: 'ar', source: 'plugin', plugin: 'sign', rtl: true }],
        },
        '/api/public/ui-locales/ar': { code: 'ar', strings: { 'ctx.download': 'تحميل' } },
      }),
    );
    const w = open('share');
    await vi.waitFor(() => expect(w.find('[data-testid="public-language-ar"]').exists()).toBe(true));
    // The language an app added is marked with the app it came from.
    expect(w.find('[data-testid="public-language-ar"]').text()).toContain('sign');

    await w.find('[data-testid="public-language-ar"]').trigger('click');
    // Its own string is used once fetched; everything it did not translate
    // falls back to English rather than rendering as a raw key.
    await vi.waitFor(() => expect(w.find('[data-testid="public-share-download"]').text()).toBe('تحميل'));
    expect(w.find('[data-testid="public-page-unavailable"]').exists()).toBe(false);
  });

  /* ────────────────────────────────────────────────────────────────────
   * The look, as structure.
   *
   * ⚠⚠ Unifying the pages levelled them DOWN (owner, 2026-09-23: "iki sayfa
   * aynı olsun dediğim için ikisini de kötü hale çevirmişsin"). These are
   * the parts of the restored shape that can be measured without a browser;
   * the pixels — card width, the ground behind it, the full-width button —
   * are measured in e2e/tests/127-public-look.spec.ts.
   * ──────────────────────────────────────────────────────────────────── */

  it('the instance’s mark stands ABOVE the card, not inside it', async () => {
    fetchMock.mockImplementation(
      router({
        '/api/public/branding': { name: 'Acme Files', logo_url: 'https://acme.example/logo.png' },
        '/api/public/s/tok123': { kind: 'file', needs_pin: false, unlocked: true, node: { name: 'a.pdf' } },
      }),
    );
    const w = open('share');
    await vi.waitFor(() => expect(w.find('[data-testid="public-share-file"]').exists()).toBe(true));
    const brand = w.find('[data-testid="public-brand"]').element;
    const card = w.find('.fe-ppage__card').element;
    expect(card.contains(brand)).toBe(false);
    expect(brand.parentElement).toBe(w.find('[data-testid="public-page"]').element);
  });

  it('the card is as wide as the body needs, and a gate is always the narrow one', async () => {
    // a document → gate · a folder → form until it has a listing, then wide ·
    // a file request → form (an app's screen is `wide`; it needs a server).
    const cases: Array<[string, Record<string, unknown>, string]> = [
      ['share', { kind: 'file', needs_pin: false, unlocked: true, node: { name: 'a.pdf' } }, 'gate'],
      // ⚠ A folder with nothing to LIST is the middle card, not the wide
      // one: 880px holding a heading and two buttons is a worse page.
      ['share', { kind: 'folder', needs_pin: false, unlocked: true, node: { name: 'Invoices' } }, 'form'],
      [
        'share',
        {
          kind: 'folder',
          needs_pin: false,
          unlocked: true,
          node: { name: 'Invoices' },
          entries: [{ name: '2026', path: '2026', is_dir: true }],
        },
        'wide',
      ],
      ['request', { kind: 'drop', needs_pin: false, unlocked: true, folder: 'Invoices' }, 'form'],
    ];
    for (const [kind, body, want] of cases) {
      fetchMock.mockImplementation(router({ [`/api/public/${kind === 'share' ? 's' : 'd'}/tok123`]: body }));
      const w = open(kind as 'share' | 'request');
      await vi.waitFor(() =>
        expect(w.find('[data-testid="public-page"]').attributes('data-state')).toBe('ready'),
      );
      expect(w.find('[data-testid="public-page"]').attributes('data-layout')).toBe(want);
      expect(w.find('.fe-ppage__card').classes()).toBeTruthy();
      expect(w.find('[data-testid="public-page"]').classes()).toContain(`fe-ppage--${want}`);
    }

    // ⚠ …and every state the shell owns overrides it. A PIN box in front of a
    // folder share is still the 400px centred card, not an 880px one with a
    // four-digit field adrift in it.
    fetchMock.mockImplementation(
      router({ '/api/public/s/tok123': { kind: 'folder', needs_pin: true, unlocked: false, node: { name: 'Invoices' } } }),
    );
    const gate = open('share');
    await vi.waitFor(() => expect(gate.find('[data-testid="public-page-pin"]').exists()).toBe(true));
    expect(gate.find('[data-testid="public-page"]').attributes('data-layout')).toBe('gate');
  });

  it('every card opens with ONE badge, and the badge says which state it is', async () => {
    const seen: Array<[Record<string, unknown>, string]> = [
      [{ kind: 'file', needs_pin: true, unlocked: false }, 'lock'],
      [{ kind: 'file', needs_pin: false, unlocked: false, expired: true }, 'alert'],
      [{ kind: 'file', needs_pin: false, unlocked: true, node: { name: 'a.pdf' } }, 'file'],
      [{ kind: 'folder', needs_pin: false, unlocked: true, node: { name: 'Invoices' } }, 'folder'],
    ];
    for (const [body, want] of seen) {
      fetchMock.mockImplementation(router({ '/api/public/s/tok123': body }));
      const w = open('share');
      // ⚠ Waited for by the badge it SHOULD end on: the shell opens on the
      // spinner badge while the link is being fetched, so a bare "a badge
      // exists" wait reads `loading` every time and measures nothing.
      await vi.waitFor(() =>
        expect(w.find('[data-testid="public-page-badge"]').attributes('data-badge')).toBe(want),
      );
      // ONE, not one per state — two badges in a card is how the old pages
      // started disagreeing with each other.
      expect(w.findAll('[data-testid="public-page-badge"]').length).toBe(1);
    }
  });

  it('a document’s name is printed ONCE', async () => {
    // ⚠ The heading IS the file's name. The body used to repeat it under
    // itself — "brand-guidelines.pdf" over "brand-guidelines.pdf · 791 B".
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': {
          kind: 'file',
          needs_pin: false,
          unlocked: true,
          node: { name: 'brand-guidelines.pdf', size: 791 },
        },
      }),
    );
    const w = open('share');
    await vi.waitFor(() => expect(w.find('[data-testid="public-share-file"]').exists()).toBe(true));
    expect(w.text().split('brand-guidelines.pdf').length - 1).toBe(1);
    // The size is still said, just not with the name glued in front of it.
    expect(w.find('[data-testid="public-share-file"]').text()).toContain('791');
  });

  it('a file request says what the page IS before it asks for anything', async () => {
    fetchMock.mockImplementation(
      router({
        '/api/public/d/tok123': {
          kind: 'drop',
          needs_pin: false,
          unlocked: true,
          folder: 'Invoices',
          limits: { max_file_size_mb: 20, allowed_ext: ['pdf'] },
        },
      }),
    );
    const w = open('request');
    await vi.waitFor(() => expect(w.find('[data-testid="public-request-drop"]').exists()).toBe(true));
    // "blind drop", said rather than discovered — the sentence the Go page
    // opened on and the unified shell dropped.
    expect(w.find('[data-testid="public-request-lead"]').text()).toContain('hidden from you');
    // The limits read as separate facts, not as one run-on sentence.
    const limits = w.find('[data-testid="public-request-limits"]').text();
    expect(limits).toContain('·');
  });

  it('an unbranded instance gets the product’s mark; a renamed one never does', async () => {
    // ⚠ The reference screen ends on a small mark beside one modest line, and
    // an instance that has not branded itself should still have one. The mark
    // is core's LogoMark — the one copy of that path data (dup-scan
    // `brand-mark`) — drawn, never re-typed.
    fetchMock.mockImplementation(
      router({ '/api/public/s/tok123': { kind: 'file', needs_pin: false, unlocked: true, node: { name: 'a.pdf' } } }),
    );
    const stock = open('share');
    await vi.waitFor(() => expect(stock.find('[data-testid="public-share-file"]').exists()).toBe(true));
    expect(stock.find('[data-testid="public-footer-powered"] svg').exists()).toBe(true);
    expect(stock.find('[data-testid="public-brand"] svg').exists()).toBe(true);

    // ⚠⚠ …and it disappears the moment the instance is somebody else's. Our
    // folder-and-tick beside "Served by Acme Files" is the whitelabel leak
    // v3 §1.2 exists to stop; a renamed instance with no logo of its own gets
    // its NAME and nothing else.
    fetchMock.mockImplementation(
      router({
        '/api/public/branding': { name: 'Acme Files' },
        '/api/public/s/tok123': { kind: 'file', needs_pin: false, unlocked: true, node: { name: 'a.pdf' } },
      }),
    );
    const renamed = open('share');
    await vi.waitFor(() => expect(renamed.find('[data-testid="public-brand"]').text()).toContain('Acme Files'));
    expect(renamed.find('[data-testid="public-footer-powered"] svg').exists()).toBe(false);
    expect(renamed.find('[data-testid="public-brand"] svg').exists()).toBe(false);
    // The instance's OWN logo is drawn in both places when it has one.
    fetchMock.mockImplementation(
      router({
        '/api/public/branding': { name: 'Acme Files', logo_url: 'https://acme.example/logo.png' },
        '/api/public/s/tok123': { kind: 'file', needs_pin: false, unlocked: true, node: { name: 'a.pdf' } },
      }),
    );
    const logo = open('share');
    await vi.waitFor(() =>
      expect(logo.find('[data-testid="public-brand"] img').attributes('src')).toBe('https://acme.example/logo.png'),
    );
    expect(logo.find('[data-testid="public-footer-powered"] img').attributes('src')).toBe(
      'https://acme.example/logo.png',
    );
  });

  it('a locked gate does not name what is behind it', async () => {
    // ⚠ Confirmed 2026-09-23: printing the file's name above the PIN box tells
    // somebody who cannot open the link what it holds. Same reasoning as the
    // one sentence for expired / used up / withdrawn.
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': {
          kind: 'file',
          needs_pin: true,
          unlocked: false,
          node: { name: 'redundancy-list.pdf' },
        },
      }),
    );
    const w = open('share');
    await vi.waitFor(() => expect(w.find('[data-testid="public-page-pin"]').exists()).toBe(true));
    expect(w.find('[data-testid="public-page-title"]').exists()).toBe(false);
    expect(w.text()).not.toContain('redundancy-list.pdf');
  });
});
