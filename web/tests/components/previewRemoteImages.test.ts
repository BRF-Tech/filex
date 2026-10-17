// @vitest-environment jsdom
//
// ⚠ jsdom for DOMPurify (previewSanitize.test.ts says why).
//
// A picture from another server in a file's preview waits behind a
// placeholder (lib/sanitizeHtml RemoteImages) and the person decides
// (composables/useRemoteImages, modals/RemoteImageDialog): a click asks in
// filex's own dialog, which names the server; "Load image" draws that one,
// without a referrer; "Load all" every one of the file, for this opening; the
// setting "Load remote images in previews" draws them without asking. The
// same gate in the explorer's preview, a notebook on its own and an app's
// release notes on the admin panel.
//
// jsdom loads no picture: "drawn" here is an <img> with that address in the
// page. e2e 280-preview-remote-images measures the requests in a browser.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import IpynbViewer from '@brftech/filex-core/src/viewers/IpynbViewer.vue';
import ReleaseNotes from '@/components/plugins/ReleaseNotes.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { remoteImagesOn, setRemoteImages } from '@brftech/filex-core/src/lib/remoteImages';
import { currentPrefs } from '@brftech/filex-core/src/lib/prefs';
import { answerAccountPrefs } from '../helpers/accountPrefs';
import { teardownDom } from '../helpers/teardown';

answerAccountPrefs();

const CHART = 'https://charts.example/q3.png';
const PIXEL = 'https://tracker.example/p.gif?file=readme';

const README = [
  '# Report',
  '',
  `![Q3 chart](${CHART})`,
  '',
  `<img src="${PIXEL}" alt="" width="1" height="1">`,
  '',
  `[![build](${CHART})](https://ci.example/run/1)`,
  '',
  '![logo](data:image/png;base64,iVBORw0KGgo=)',
].join('\n');

const mdNode = (basename: string): FileNode =>
  ({ path: `main://${basename}`, basename, type: 'file', extension: 'md', size: 100 }) as FileNode;

async function preview(text: string, locale = 'en'): Promise<HTMLElement> {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(text, { status: 200 })));
  mount(PreviewModal, {
    attachTo: document.body,
    props: {
      open: true,
      locale,
      file: mdNode('README.md'),
      openMode: 'view',
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
    },
  });
  await vi.waitFor(
    async () => {
      await flushPromises();
      expect(document.querySelector('.fe-preview__md h1')).not.toBeNull();
    },
    { timeout: 5000 },
  );
  return document.querySelector('.fe-preview__md') as HTMLElement;
}

const remoteImgs = (root: ParentNode): string[] =>
  Array.from(root.querySelectorAll('img'))
    .map((i) => i.getAttribute('src') ?? '')
    .filter((s) => !s.startsWith('data:'));
const placeholders = (root: ParentNode, src?: string): HTMLElement[] =>
  Array.from(root.querySelectorAll<HTMLElement>(src ? `[data-remote-image="${src}"]` : '[data-remote-image]'));
const dialog = (): HTMLElement | null => document.querySelector('[data-testid="remote-image-dialog"]');
const button = (id: string): HTMLButtonElement => document.querySelector(`[data-testid="${id}"]`) as HTMLButtonElement;

afterEach(async () => {
  await teardownDom();
  vi.unstubAllGlobals();
});

describe('a Markdown preview holds back pictures from other servers', () => {
  it('draws placeholders, not the pictures; a data: picture is drawn', async () => {
    const md = await preview(README);
    expect(remoteImgs(md)).toEqual([]);
    expect(placeholders(md).map((p) => p.getAttribute('data-remote-host'))).toEqual([
      'charts.example',
      'tracker.example',
      'charts.example',
    ]);
    expect(md.querySelector('img')?.getAttribute('src')).toMatch(/^data:image\/png/);
    expect(placeholders(md)[0].textContent).toContain('Q3 chart');
    expect(placeholders(md)[0].textContent).toContain('Remote image');
    expect(dialog()).toBeNull();
  });

  it('a click asks in filex’s own dialog, naming the server; Load image draws that picture without a referrer', async () => {
    const confirmSpy = vi.fn(() => true);
    vi.stubGlobal('confirm', confirmSpy);
    const md = await preview(README);
    placeholders(md, CHART)[0].click();
    await flushPromises();
    expect(dialog()).not.toBeNull();
    expect(document.querySelector('[data-testid="remote-image-host"]')?.textContent).toBe('charts.example');
    expect(dialog()?.textContent).toContain('tells that server that this file was opened, and your IP address');
    // The settings are wired (answerAccountPrefs): the dialog says where.
    expect(document.querySelector('[data-testid="remote-image-hint"]')?.textContent).toContain('Load remote images in previews');
    // Cancel is the first answer: the one Enter takes, loading nothing.
    expect(dialog()?.closest('.fe-modal__card')?.querySelector('.fe-modal__actions button')?.textContent?.trim()).toBe('Cancel');
    button('remote-image-load').click();
    await flushPromises();
    expect(dialog()).toBeNull();
    // Both placeholders of that address are drawn; the other server's waits.
    const drawn = Array.from(md.querySelectorAll<HTMLImageElement>(`img[src="${CHART}"]`));
    expect(drawn).toHaveLength(2);
    for (const img of drawn) expect(img.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(drawn[0].getAttribute('alt')).toBe('Q3 chart');
    expect(placeholders(md).map((p) => p.getAttribute('data-remote-image'))).toEqual([PIXEL]);
    expect(confirmSpy).not.toHaveBeenCalled();
  });

  it('Cancel loads nothing', async () => {
    const md = await preview(README);
    placeholders(md, PIXEL)[0].click();
    await flushPromises();
    button('remote-image-cancel').click();
    await flushPromises();
    expect(dialog()).toBeNull();
    expect(remoteImgs(md)).toEqual([]);
    expect(placeholders(md)).toHaveLength(3);
  });

  it('Load all draws every picture of the file, keeping its size', async () => {
    const md = await preview(README);
    placeholders(md, PIXEL)[0].click();
    await flushPromises();
    expect(button('remote-image-load-all')).not.toBeNull();
    button('remote-image-load-all').click();
    await flushPromises();
    expect(placeholders(md)).toEqual([]);
    expect(remoteImgs(md).sort()).toEqual([CHART, CHART, PIXEL].sort());
    const pixel = md.querySelector(`img[src="${PIXEL}"]`)!;
    expect(pixel.getAttribute('width')).toBe('1');
    expect(pixel.getAttribute('referrerpolicy')).toBe('no-referrer');
  });

  it('a placeholder inside a link asks; it does not follow the link', async () => {
    const md = await preview(README);
    const inLink = placeholders(md, CHART).find((p) => p.closest('a'))!;
    expect(inLink).toBeTruthy();
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true });
    inLink.dispatchEvent(ev);
    await flushPromises();
    expect(ev.defaultPrevented).toBe(true);
    expect(dialog()).not.toBeNull();
  });

  it('Enter on a placeholder asks, as a click does', async () => {
    const md = await preview(README);
    placeholders(md, PIXEL)[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    await flushPromises();
    expect(document.querySelector('[data-testid="remote-image-host"]')?.textContent).toBe('tracker.example');
  });

  it('with the setting on, the pictures are drawn without asking, without a referrer', async () => {
    setRemoteImages(true);
    expect(currentPrefs().remoteImages).toBe('on');
    expect(remoteImagesOn()).toBe(true);
    const md = await preview(README);
    expect(placeholders(md)).toEqual([]);
    expect(remoteImgs(md).sort()).toEqual([CHART, CHART, PIXEL].sort());
    for (const img of Array.from(md.querySelectorAll('img:not([src^="data:"])'))) {
      expect(img.getAttribute('referrerpolicy')).toBe('no-referrer');
    }
  });

  it('the setting turned on while the preview is open draws what was waiting', async () => {
    const md = await preview(README);
    expect(placeholders(md)).toHaveLength(3);
    setRemoteImages(true);
    await flushPromises();
    expect(placeholders(md)).toEqual([]);
    expect(remoteImgs(md)).toHaveLength(3);
  });

  it('speaks Turkish with Turkish letters', async () => {
    const md = await preview(README, 'tr');
    expect(placeholders(md)[0].textContent).toContain('Uzak görsel');
    placeholders(md)[0].click();
    await flushPromises();
    expect(dialog()?.closest('.fe-modal__card')?.textContent).toContain('Uzak görsel yüklensin mi?');
    expect(button('remote-image-load').textContent?.trim()).toBe('Görseli yükle');
    expect(button('remote-image-load-all').textContent?.trim()).toBe('Bu dosyadaki tüm uzak görselleri yükle');
  });
});

describe('a notebook on its own asks itself', () => {
  const nb = {
    nbformat: 4,
    metadata: { kernelspec: { language: 'python' } },
    cells: [
      { cell_type: 'markdown', source: [`![plot](${CHART})`] },
      {
        cell_type: 'code',
        source: ['show()'],
        execution_count: 1,
        outputs: [{ output_type: 'display_data', data: { 'text/html': [`<img src="${PIXEL}" alt="beacon">`] } }],
      },
      { cell_type: 'markdown', source: ['CELL-END'] },
    ],
  };

  it('Markdown cells and HTML outputs hold their pictures; a click asks; Load image draws it', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(nb), { status: 200 })));
    const w = mount(IpynbViewer, { attachTo: document.body, props: { url: '/nb.ipynb', ext: 'ipynb', locale: 'en' } });
    await vi.waitFor(
      async () => {
        await flushPromises();
        expect(w.text()).toContain('CELL-END');
      },
      { timeout: 5000 },
    );
    const root = w.element as HTMLElement;
    expect(remoteImgs(root)).toEqual([]);
    expect(placeholders(root)).toHaveLength(2);
    placeholders(root, PIXEL)[0].click();
    await flushPromises();
    expect(document.querySelector('[data-testid="remote-image-host"]')?.textContent).toBe('tracker.example');
    button('remote-image-load').click();
    await flushPromises();
    expect(root.querySelector(`img[src="${PIXEL}"]`)?.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(placeholders(root, CHART)).toHaveLength(1);
  });
});

describe("an app's release notes on the admin panel", () => {
  it('hold a picture from the release behind a placeholder that asks', async () => {
    const w = mount(ReleaseNotes, {
      props: { notes: `## 1.2.0\n\n![screenshot](${CHART})`, testid: 'notes-md' },
      attachTo: document.body,
    });
    await vi.waitFor(
      async () => {
        await flushPromises();
        expect(w.find('[data-testid="notes-md"] h2').exists()).toBe(true);
      },
      { timeout: 5000 },
    );
    const notes = w.find('[data-testid="notes-md"]').element as HTMLElement;
    expect(remoteImgs(notes)).toEqual([]);
    placeholders(notes, CHART)[0].click();
    await flushPromises();
    expect(document.querySelector('[data-testid="remote-image-host"]')?.textContent).toBe('charts.example');
    button('remote-image-load').click();
    await flushPromises();
    expect(notes.querySelector(`img[src="${CHART}"]`)?.getAttribute('referrerpolicy')).toBe('no-referrer');
  });
});
