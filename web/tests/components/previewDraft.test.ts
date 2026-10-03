// A draft's editor (issue #71) — PreviewModal on a path in the drafts area.
//
// The viewer finds the draft by its PATH (every host opens it on a path and
// nothing else), asks the server where it is meant to go, and then:
//   · draws the bar with that place and the Save that puts it there;
//   · on close asks Save to disk / Keep in Drafts / Discard — never just closes;
//   · on a taken name asks "save as name (2).ext?" and saves only that name;
//   · keeps editing the document where the Save put it;
//   · asks the browser's own leave-page question while it holds edits.
// An ordinary file gets none of it.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { teardownDom } from '../helpers/teardown';

const KEY = '0123456789abcdef';
const DRAFT_PATH = `docs://.filex-drafts/7/${KEY}/notes.md`;

const DRAFT = {
  key: KEY,
  name: 'notes.md',
  path: DRAFT_PATH,
  storage: 'docs',
  target_dir: 'docs://Documents',
  target: 'docs://Documents/notes.md',
  type: 'md',
  size: 0,
  created_at: '2026-09-27T08:00:00Z',
};

const node = (path: string): FileNode =>
  ({ path, basename: path.slice(path.lastIndexOf('/') + 1), type: 'file', extension: 'md', size: 0 }) as FileNode;

/** Lets the pending work run. With the timers frozen it runs what is due NOW
 *  and never moves the clock: a test that walks time does so itself. */
const settle = async () => {
  for (let i = 0; i < 6; i++) {
    if (vi.isFakeTimers()) await vi.advanceTimersByTimeAsync(0);
    else await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

/** Which control has the focus, by its test id (null: one without). */
const focused = () => document.activeElement?.getAttribute('data-testid') ?? null;

interface Call {
  url: string;
  method: string;
  body: unknown;
}

/** A server: the draft, its content, and scripted answers for Save. */
function server(saveAnswers: Array<{ status: number; body: unknown }> = []) {
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? 'GET').toUpperCase();
    let body: unknown = undefined;
    try {
      body = init?.body ? JSON.parse(String(init.body)) : undefined;
    } catch {
      body = init?.body;
    }
    calls.push({ url, method, body });
    if (url === `/api/files/drafts/${KEY}` && method === 'GET') return new Response(JSON.stringify(DRAFT), { status: 200 });
    if (url === `/api/files/drafts/${KEY}` && method === 'DELETE') return new Response('{"ok":true,"trashed":true}', { status: 200 });
    if (url === `/api/files/drafts/${KEY}/save`) {
      const a = saveAnswers.shift() ?? { status: 500, body: {} };
      return new Response(JSON.stringify(a.body), { status: a.status });
    }
    if (url.startsWith('/api/files/drafts/')) return new Response('{"error":"draft not found"}', { status: 404 });
    if (url === '/api/files/save-text') return new Response('{"ok":true}', { status: 200 });
    return new Response('# half a thought\n', { status: 200 });
  });
  vi.stubGlobal('fetch', fetchMock);
  return { calls };
}

// Pages down first (in-flight work lands, pages unmount, <body> empties),
// while this file's mocks still answer; only then are the mocks taken away.
afterEach(async () => {
  await teardownDom();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function editor(path: string, extra: Record<string, unknown> = {}) {
  const w = mount(PreviewModal, {
    attachTo: document.body,
    props: {
      open: true,
      locale: 'en',
      file: node(path),
      openMode: 'edit',
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${encodeURIComponent(p)}`,
      saveTextEndpoint: '/api/files/save-text',
      draftsEndpoint: '/api/files/drafts',
      shareEnabled: true,
      ...extra,
    },
  });
  await settle();
  return w;
}

const q = (sel: string) => document.querySelector<HTMLElement>(sel);

describe('an ordinary file is not a draft', () => {
  it('has no draft bar, and closes when asked', async () => {
    const s = server();
    const w = await editor('docs://Documents/notes.md');
    expect(w.find('[data-testid="draft-bar"]').exists()).toBe(false);
    await w.get('[data-testid="viewer-close"]').trigger('click');
    expect(w.emitted('close')).toHaveLength(1);
    // It never even asked whether it was one.
    expect(s.calls.some((c) => c.url.startsWith('/api/files/drafts'))).toBe(false);
  });

  it('says its size in the header, an empty file’s "0 B" included', async () => {
    server();
    const w = await editor('docs://Documents/notes.md');
    expect(w.get('.fe-viewer__meta').text()).toContain('0 B');
  });

  it('a path shaped like a draft that the server does not know is edited as the file it is', async () => {
    server();
    const w = await editor(`docs://.filex-drafts/7/${'f'.repeat(16)}/x.md`);
    expect(w.find('[data-testid="draft-bar"]').exists()).toBe(false);
  });
});

describe('a draft’s editor', () => {
  it('says where the draft will be saved, and offers no star and no share', async () => {
    server();
    const w = await editor(DRAFT_PATH);
    const bar = w.get('[data-testid="draft-bar"]');
    expect(bar.text()).toContain('Draft');
    expect(bar.text()).toContain('notes.md');
    expect(w.get('[data-testid="draft-bar-target"]').text()).toBe('will be saved to docs / Documents');
    expect(w.find('[data-testid="draft-save"]').exists()).toBe(true);
    expect(w.find('.fe-viewer__act--star').exists()).toBe(false);
    expect(w.findAll('.fe-viewer__act').some((b) => b.attributes('title') === 'Share')).toBe(false);
  });

  // The header used to say "0 B" - the size the draft had when New document
  // made it - and kept saying it after the app had written 4.47 KB into the
  // draft and after its Save (filextext, 0.48.0). The viewer has no live
  // figure for a document being written, so it names none.
  it('names no size in the header, while it is a draft and after its Save', async () => {
    server([
      { status: 200, body: { ok: true, path: 'docs://Documents/notes.md', name: 'notes.md', target_dir: 'docs://Documents' } },
    ]);
    const w = await editor(DRAFT_PATH);
    const meta = () => (w.find('.fe-viewer__meta').exists() ? w.get('.fe-viewer__meta').text() : '');
    expect(w.find('[data-testid="draft-bar"]').exists()).toBe(true);
    expect(meta()).not.toContain('0 B');

    const area = w.get('textarea.fe-preview__md-split-input');
    await area.setValue('# typed into the draft\n');
    await area.trigger('input');
    await w.get('[data-testid="draft-save"]').trigger('click');
    await settle();
    expect(w.find('[data-testid="draft-saved-note"]').exists()).toBe(true);
    expect(meta()).not.toContain('0 B');
  });

  // #85: after its Save the viewer shows a file, not a draft - the same row,
  // moved to where the Save put it - and a file has Share and the star. Both
  // stayed away: the viewer still answered "draft" from the path it had been
  // opened on, and Share would have named that path, which no longer exists.
  it('after its Save, offers Share and the star again, and shares the saved file', async () => {
    server([
      { status: 200, body: { ok: true, path: 'docs://Documents/notes.md', name: 'notes.md', target_dir: 'docs://Documents' } },
    ]);
    const w = await editor(DRAFT_PATH, { file: { ...node(DRAFT_PATH), id: 42 } });
    const share = () => w.find('[data-testid="viewer-share"]');
    expect(w.find('[data-testid="draft-bar"]').exists()).toBe(true);
    expect(share().exists()).toBe(false);
    expect(w.find('.fe-viewer__act--star').exists()).toBe(false);

    await w.get('[data-testid="draft-save"]').trigger('click');
    await settle();
    expect(w.find('[data-testid="draft-saved-note"]').exists()).toBe(true);
    expect(share().exists(), 'Share is back on the saved file').toBe(true);
    expect(w.find('.fe-viewer__act--star').exists(), 'the star is back on the saved file').toBe(true);

    await share().trigger('click');
    const sent = w.emitted('share')?.[0]?.[0] as FileNode | undefined;
    expect(sent?.path).toBe('docs://Documents/notes.md');
    expect(sent?.basename).toBe('notes.md');
    expect(sent?.id).toBe(42);
  });

  it('closing asks what should become of it — and “Keep in Drafts” is the default, changing nothing', async () => {
    const s = server();
    const w = await editor(DRAFT_PATH);
    await w.get('[data-testid="viewer-close"]').trigger('click');
    await settle();
    expect(w.emitted('close')).toBeUndefined();
    const dialog = q('[data-testid="draft-close-dialog"]');
    expect(dialog).not.toBeNull();
    expect(q('[data-testid="draft-close-save"]')?.textContent?.trim()).toBe('Save to disk');
    expect(q('[data-testid="draft-close-discard"]')?.textContent?.trim()).toBe('Discard');
    await vi.waitFor(() => expect(focused()).toBe('draft-close-keep'));

    q('[data-testid="draft-close-keep"]')!.click();
    await settle();
    expect(w.emitted('close')).toHaveLength(1);
    expect(s.calls.some((c) => c.method === 'DELETE' || c.url.endsWith('/save'))).toBe(false);
  });

  // ⚠ The viewer is a dialog too, and Modal focuses a dialog 30 ms after it
  // opens. A close question that opened before the viewer's 30 ms were up
  // lost "Keep in Drafts" to the viewer's text box when that timer fired, and
  // got it back from its own timer 30 ms later: the check above failed on the
  // v0.48.0 release run and on a v0.48.1 CI run (focus: the textarea), and 2
  // of 100 real-timer loops failed on a dev machine. Here the clock is
  // frozen and walked one millisecond at a time, so no machine is too fast or
  // too slow for it.
  it.each([5, 15, 25, 40])(
    'nothing takes the focus off “Keep in Drafts” while the question is up (closed %i ms after the editor opened)',
    async (closeAt) => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
      server();
      const w = await editor(DRAFT_PATH);
      await vi.advanceTimersByTimeAsync(closeAt);
      await w.get('[data-testid="viewer-close"]').trigger('click');
      await settle();
      expect(focused()).toBe('draft-close-keep');
      const lost: string[] = [];
      for (let ms = 1; ms <= 60; ms++) {
        await vi.advanceTimersByTimeAsync(1);
        if (focused() !== 'draft-close-keep') {
          const el = document.activeElement;
          lost.push(`+${ms} ms: ${el?.tagName.toLowerCase()}.${el?.className}`);
        }
      }
      expect(lost).toEqual([]);
    },
  );

  it('Escape takes back the question, not the draft', async () => {
    server();
    const w = await editor(DRAFT_PATH);
    await w.get('[data-testid="viewer-close"]').trigger('click');
    await settle();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
    expect(q('[data-testid="draft-close-dialog"]')).toBeNull();
    expect(w.emitted('close')).toBeUndefined();
  });

  it('Discard sends it to the trash, and says so', async () => {
    const s = server();
    const w = await editor(DRAFT_PATH);
    await w.get('[data-testid="viewer-close"]').trigger('click');
    await settle();
    q('[data-testid="draft-close-discard"]')!.click();
    await settle();
    expect(s.calls.find((c) => c.method === 'DELETE')?.url).toBe(`/api/files/drafts/${KEY}`);
    expect(w.emitted('draft-discarded')).toHaveLength(1);
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('Save beside a taken name asks first, and saves exactly the name agreed to', async () => {
    const s = server([
      { status: 409, body: { code: 'TARGET_TAKEN', name: 'notes.md', suggested: 'notes (2).md', target_dir: 'docs://Documents' } },
      { status: 200, body: { ok: true, path: 'docs://Documents/notes (2).md', name: 'notes (2).md', target_dir: 'docs://Documents' } },
    ]);
    const w = await editor(DRAFT_PATH);
    await w.get('[data-testid="draft-save"]').trigger('click');
    await settle();
    const ask = q('[data-testid="draft-taken-dialog"]');
    expect(ask?.textContent).toContain('docs / Documents already has a file called notes.md');
    expect(q('[data-testid="draft-taken-confirm"]')?.textContent?.trim()).toBe('Save as notes (2).md');
    expect(w.emitted('draft-saved')).toBeUndefined();

    q('[data-testid="draft-taken-confirm"]')!.click();
    await settle();
    const saves = s.calls.filter((c) => c.url.endsWith('/save'));
    expect(saves.map((c) => c.body)).toEqual([{}, { as: 'notes (2).md' }]);
    expect(w.emitted('draft-saved')?.[0]?.[0]).toEqual({
      path: 'docs://Documents/notes (2).md',
      name: 'notes (2).md',
      targetDir: 'docs://Documents',
    });
    // It is a file now: the bar says where it went, and closing just closes.
    expect(w.get('[data-testid="draft-saved-note"]').text()).toContain('notes (2).md was saved to docs / Documents.');
    expect(w.find('[data-testid="draft-save"]').exists()).toBe(false);
    await w.get('[data-testid="viewer-close"]').trigger('click');
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('declining the question leaves it a draft', async () => {
    const s = server([
      { status: 409, body: { code: 'TARGET_TAKEN', name: 'notes.md', suggested: 'notes (2).md', target_dir: 'docs://Documents' } },
    ]);
    const w = await editor(DRAFT_PATH);
    await w.get('[data-testid="draft-save"]').trigger('click');
    await settle();
    q('[data-testid="draft-taken-cancel"]')!.click();
    await settle();
    expect(q('[data-testid="draft-taken-dialog"]')).toBeNull();
    expect(s.calls.filter((c) => c.url.endsWith('/save'))).toHaveLength(1);
    expect(w.find('[data-testid="draft-save"]').exists()).toBe(true);
  });

  it('what is typed goes into the draft — by its whole path — and, after the Save, into the saved file', async () => {
    const s = server([
      { status: 200, body: { ok: true, path: 'docs://Documents/notes.md', name: 'notes.md', target_dir: 'docs://Documents' } },
    ]);
    const w = await editor(DRAFT_PATH);
    const area = w.get('textarea.fe-preview__md-split-input');
    await area.setValue('# a whole thought\n');
    await area.trigger('input');
    // Save flushes what the editor holds into the draft first.
    await w.get('[data-testid="draft-save"]').trigger('click');
    await settle();
    const texts = s.calls.filter((c) => c.url === '/api/files/save-text');
    expect(texts[0]?.body).toEqual({ path: DRAFT_PATH, content: '# a whole thought\n' });
    const saveIdx = s.calls.findIndex((c) => c.url.endsWith('/save'));
    expect(s.calls.indexOf(texts[0])).toBeLessThan(saveIdx);

    await area.setValue('# a whole thought, saved\n');
    await area.trigger('keydown', { key: 's', ctrlKey: true });
    await settle();
    const after = s.calls.filter((c) => c.url === '/api/files/save-text');
    expect(after[after.length - 1]?.body).toEqual({ path: 'docs://Documents/notes.md', content: '# a whole thought, saved\n' });
  });

  it('asks the browser’s own question before the page goes while it holds edits — and not before', async () => {
    server();
    const w = await editor(DRAFT_PATH);
    const before = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(before);
    expect(before.defaultPrevented).toBe(false);

    const area = w.get('textarea.fe-preview__md-split-input');
    await area.setValue('typed');
    await area.trigger('input');
    const leaving = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(leaving);
    expect(leaving.defaultPrevented).toBe(true);
  });

  it('an ordinary file never asks the leave-page question', async () => {
    server();
    const w = await editor('docs://Documents/notes.md');
    const area = w.get('textarea.fe-preview__md-split-input');
    await area.setValue('typed');
    await area.trigger('input');
    const leaving = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(leaving);
    expect(leaving.defaultPrevented).toBe(false);
  });

  it('in Turkish, with the real letters', async () => {
    server();
    const w = await editor(DRAFT_PATH, { locale: 'tr' });
    expect(w.get('[data-testid="draft-bar-target"]').text()).toBe('docs / Documents konumuna kaydedilecek');
    await w.get('[data-testid="viewer-close"]').trigger('click');
    await settle();
    expect(q('[data-testid="draft-close-keep"]')?.textContent?.trim()).toBe('Taslaklarda tut');
    expect(q('[data-testid="draft-close-discard"]')?.textContent?.trim()).toBe('Çöpe at');
    expect(q('[data-testid="draft-close-dialog"]')?.textContent).toContain('henüz oraya kaydedilmedi');
  });
});
