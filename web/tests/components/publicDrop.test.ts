// The public drop page (`/d/<token>`) — what a stranger sent a file request sees.
//
// ⚠⚠ What the release-candidate sweep found (QA, 2026-09-21, #19):
//   · "Ask uploader name" is ON by default, and the page had no name field —
//     the setting did nothing and every submission arrived as `<date>_anon`;
//   · a file the link does not take (a .txt on a PDF-only link) was SENT, the
//     server refused it, and the page printed "The app returned an error" —
//     the app-plugin runtime's sentence, on a page with no app on it;
//   · every dropped file went up in a request of its own, so one drop of
//     three files made three submission folders and slipped past the
//     per-submission file cap.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PublicLink from '@/views/public/PublicLink.vue';
import { resetLocales } from '@brftech/filex-core';
import { en } from '@brftech/filex-core/src/locales/en';

function answer(status: number, body: unknown) {
  return { ok: status >= 200 && status < 300, status, statusText: '', json: async () => body, text: async () => JSON.stringify(body) };
}

const fetchMock = vi.fn();
type Sent = { url: string; files: string[]; name: FormDataEntryValue | null };
let sent: Sent[] = [];
let reply: { status: number; body: unknown } = { status: 200, body: { ok: true } };

class FakeXHR {
  upload = { onprogress: null as ((e: ProgressEvent) => void) | null };
  status = 0;
  responseText = '';
  withCredentials = false;
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private url = '';
  open(_m: string, url: string) {
    this.url = url;
  }
  setRequestHeader() {}
  send(form: FormData) {
    sent.push({ url: this.url, files: form.getAll('file').map((f) => (f as File).name), name: form.get('uploader_name') });
    this.status = reply.status;
    this.responseText = JSON.stringify(reply.body);
    this.onload?.();
  }
}

function dropInfo(limits: Record<string, unknown>) {
  fetchMock.mockImplementation(async (url: string) => {
    if (url === '/api/public/d/tok123')
      return answer(200, { kind: 'drop', folder: 'Invoices', needs_pin: false, unlocked: true, uploads_left: null, limits });
    return answer(404, { error: 'not_found' });
  });
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('XMLHttpRequest', FakeXHR as unknown as typeof XMLHttpRequest);
  sent = [];
  reply = { status: 200, body: { ok: true } };
  resetLocales();
  try {
    localStorage.clear();
  } catch {
    /* happy-dom always has it */
  }
});
afterEach(() => vi.unstubAllGlobals());

async function openDrop() {
  const w = mount(PublicLink, { props: { kind: 'request', token: 'tok123', locale: 'en' } });
  await vi.waitFor(() => expect(w.find('[data-testid="public-request-drop"]').exists()).toBe(true));
  return w;
}

function pick(w: ReturnType<typeof mount>, files: File[]) {
  const input = w.get('[data-testid="public-request-input"]');
  Object.defineProperty(input.element, 'files', { value: files, configurable: true });
  return input.trigger('change');
}

const pdf = (name: string) => new File(['%PDF'], name, { type: 'application/pdf' });

describe('the name the link asks for', () => {
  it('is asked, above the drop area, and travels with the files', async () => {
    dropInfo({ max_files: 5, max_file_size_mb: 5, allowed_ext: ['pdf'], ask_name: true });
    const w = await openDrop();
    const field = w.get('[data-testid="public-request-name"]');
    expect(field.text()).toContain(en['public.your_name']);
    // Above the drop area: dropping a file sends it, so the name has to come first.
    const html = w.html();
    expect(html.indexOf('public-request-name')).toBeLessThan(html.indexOf('public-request-drop'));

    await w.get('[data-testid="public-request-name-input"]').setValue('  Ayşe Yılmaz ');
    await pick(w, [pdf('a.pdf'), pdf('b.pdf')]);
    await flushPromises();
    expect(sent).toHaveLength(1);
    expect(sent[0].name).toBe('Ayşe Yılmaz');
  });

  it('is not asked when the link does not ask, and nothing is sent in its place', async () => {
    dropInfo({ max_files: 5, max_file_size_mb: 5, allowed_ext: [], ask_name: false });
    const w = await openDrop();
    expect(w.find('[data-testid="public-request-name"]').exists()).toBe(false);
    await pick(w, [pdf('a.pdf')]);
    await flushPromises();
    expect(sent[0].name).toBeNull();
  });
});

describe('one drop, one submission', () => {
  it('every file of one drop goes up in ONE request', async () => {
    dropInfo({ max_files: 5, max_file_size_mb: 5, allowed_ext: [], ask_name: false });
    const w = await openDrop();
    await pick(w, [pdf('a.pdf'), pdf('b.pdf'), pdf('c.pdf')]);
    await flushPromises();
    expect(sent).toHaveLength(1);
    expect(sent[0]).toMatchObject({ url: '/api/public/d/tok123/upload', files: ['a.pdf', 'b.pdf', 'c.pdf'] });
    expect(w.get('[data-testid="public-request-uploads"]').text()).toContain(en['public.upload_done']);
  });
});

describe('refused before it is sent, in words', () => {
  it('a type the link does not take is not sent; the rest of the drop still is', async () => {
    dropInfo({ max_files: 5, max_file_size_mb: 5, allowed_ext: ['pdf'], ask_name: false });
    const w = await openDrop();
    await pick(w, [new File(['x'], 'notes.txt', { type: 'text/plain' }), pdf('invoice.pdf')]);
    await flushPromises();
    expect(sent).toHaveLength(1);
    expect(sent[0].files).toEqual(['invoice.pdf']);
    const list = w.get('[data-testid="public-request-uploads"]').text();
    expect(list).toContain('notes.txt');
    expect(list).toContain(en['public.refused_ext']);
    expect(list).not.toContain(en['plugin.view.error']);
  });

  it('a file over the size limit is not sent — measured the way the SERVER measures (MiB)', async () => {
    dropInfo({ max_files: 5, max_file_size_mb: 1, allowed_ext: [], ask_name: false });
    const w = await openDrop();
    const justUnder = new File([new Uint8Array(1024 * 1024)], 'ok.bin');
    const over = new File([new Uint8Array(1024 * 1024 + 1)], 'big.bin');
    await pick(w, [justUnder, over]);
    await flushPromises();
    expect(sent[0].files).toEqual(['ok.bin']);
    expect(w.get('[data-testid="public-request-uploads"]').text()).toContain('Not sent — larger than 1 MB.');
  });

  it('more files than the submission may carry: the first ones go, the rest say why', async () => {
    dropInfo({ max_files: 2, max_file_size_mb: 5, allowed_ext: [], ask_name: false });
    const w = await openDrop();
    await pick(w, [pdf('a.pdf'), pdf('b.pdf'), pdf('c.pdf')]);
    await flushPromises();
    expect(sent[0].files).toEqual(['a.pdf', 'b.pdf']);
    expect(w.get('[data-testid="public-request-uploads"]').text()).toContain(
      'Not sent — at most 2 files can be sent now.',
    );
  });
});

describe('a refusal the server makes', () => {
  it('shows the server’s own sentence', async () => {
    dropInfo({ max_files: 5, max_file_size_mb: 5, allowed_ext: [], ask_name: false });
    reply = { status: 507, body: { error: 'quota_exceeded', message: 'The folder behind this link is out of space.' } };
    const w = await openDrop();
    await pick(w, [pdf('a.pdf')]);
    await flushPromises();
    expect(w.get('[data-testid="public-request-uploads"]').text()).toContain('The folder behind this link is out of space.');
  });

  it('with no sentence (an older server), says it could not be sent — never the app runtime’s words', async () => {
    dropInfo({ max_files: 5, max_file_size_mb: 5, allowed_ext: [], ask_name: false });
    reply = { status: 503, body: { error: 'storage_unavailable' } };
    const w = await openDrop();
    await pick(w, [pdf('a.pdf')]);
    await flushPromises();
    const list = w.get('[data-testid="public-request-uploads"]').text();
    expect(list).toContain(en['public.upload_failed']);
    expect(list).not.toContain(en['plugin.view.error']);
  });
});

it('names the folder once — the heading is already its name', async () => {
  dropInfo({ max_files: 5, max_file_size_mb: 5, allowed_ext: [], ask_name: false });
  const w = await openDrop();
  expect(w.find('[data-testid="public-request-folder"]').exists()).toBe(false);
  expect(w.text()).toContain('Invoices');
});
