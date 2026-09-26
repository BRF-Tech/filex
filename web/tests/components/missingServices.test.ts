// An optional service that is not there — ONLYOFFICE, draw.io — is said in
// words, and split by who is reading (the owner, 2026-09-21): an
// ADMINISTRATOR sees the entry greyed, with where to set it up ("External
// services" — the screen's real name); EVERYBODY ELSE is not offered it.
//
// ⚠ Before this: "Open" on a .docx opened a tab that printed
// `Config fetch 503: {"error":"onlyoffice not configured"}`, and a person who
// only wanted to look at a diagram was told to set FILEX_DRAWIO_URL.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { setTimeout as sleep } from 'node:timers/promises';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import { gateOnService, isOfficeExt } from '@brftech/filex-core/src/lib/serviceGate';
import { en } from '@brftech/filex-core/src/locales/en';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const docx = {
  path: 'depo://letter.docx',
  basename: 'letter.docx',
  type: 'file',
  extension: 'docx',
  size: 38_000,
  last_modified: 1_757_376_000,
} as FileNode;
const drawio = { ...docx, path: 'depo://diagram.drawio', basename: 'diagram.drawio', extension: 'drawio' } as FileNode;

// The diagram viewer's module arrives late here, as it did on CI (2026-09-26).
// It is compiled on first use, and on a busy runner it arrived after this file
// had finished and its page had been torn down. The previews its tests had
// left open mounted it anyway, and its `window.addEventListener` threw "window
// is not defined": an unhandled error, which fails the whole run. The delay is
// Node's timer, not the page's, so it runs on after the test that started it.
vi.mock('@brftech/filex-core/src/viewers/DrawioViewer.vue', async (importOriginal) => {
  await sleep(30);
  return importOriginal();
});

/** The office loader waits one macrotask for its container before it asks. */
const settle = async () => {
  await new Promise((r) => setTimeout(r, 0));
  await flushPromises();
  await new Promise((r) => setTimeout(r, 0));
  await flushPromises();
};

/** Every preview a test opened. Closed when the test ends: one left open
 *  mounts its viewer whenever the viewer's module arrives (see the last test). */
const opened: VueWrapper[] = [];

function viewer(props: Record<string, unknown>) {
  const w = mount(PreviewModal, {
    props: {
      open: true,
      locale: 'en',
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
      ...props,
    },
  });
  opened.push(w);
  return w;
}

afterEach(() => {
  opened.splice(0).forEach((w) => w.unmount());
});

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({ ok: true, status: 200, statusText: 'OK', text: async () => '' }),
  );
});

describe('gateOnService — the one rule every menu entry uses', () => {
  it('available: offered as is', () => {
    expect(gateOnService(true, false, 'why')).toEqual({});
    expect(gateOnService(true, true, 'why')).toEqual({});
  });
  it('missing, administrator: greyed, with the reason', () => {
    expect(gateOnService(false, true, 'set it up')).toEqual({ disabled: true, title: 'set it up' });
  });
  it('missing, anybody else: not offered at all', () => {
    expect(gateOnService(false, false, 'set it up')).toEqual({ hidden: true });
  });
  it('knows which extensions are office documents', () => {
    expect(isOfficeExt('DOCX')).toBe(true);
    expect(isOfficeExt('pdf')).toBe(false);
  });
});

describe('previewing an office document with no ONLYOFFICE', () => {
  it('says so in words — to a person, without the operator’s homework', async () => {
    const w = viewer({ file: docx, onlyOfficeBase: null, onlyOfficeConfigEndpoint: '/api/files/onlyoffice/config' });
    await settle();
    const text = w.get('[data-testid="office-fallback"]').text();
    expect(text).toContain(en['viewer.office_unconfigured']);
    expect(text).not.toContain('External services');
  });

  it('tells an administrator where to set it up', async () => {
    const w = viewer({ file: docx, onlyOfficeBase: null, canConfigure: true });
    await settle();
    const text = w.get('[data-testid="office-fallback"]').text();
    expect(text).toContain(en['viewer.office_unconfigured_admin']);
    expect(text).toContain('External services');
  });

  it('a server that answers 503 "not configured" is the same sentence — never the raw status', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
        text: async () => '{"error":"onlyoffice not configured"}',
        json: async () => ({ error: 'onlyoffice not configured' }),
      }),
    );
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const w = viewer({
      file: docx,
      onlyOfficeBase: 'https://docs.example.com',
      onlyOfficeConfigEndpoint: '/api/files/onlyoffice/config',
    });
    await flushPromises();
    await settle();
    const text = w.get('[data-testid="office-fallback"]').text();
    expect(text).toContain(en['viewer.office_unconfigured']);
    expect(text).not.toMatch(/503|Config fetch|not configured/);
  });
});

describe('the Edit pencil on a diagram with no draw.io', () => {
  const pencil = (w: ReturnType<typeof viewer>) =>
    w.findAll('button.fe-viewer__act').filter((b) => (b.attributes('title') ?? '').includes('draw.io'));

  it('is not offered to a person who cannot set draw.io up', () => {
    const w = viewer({ file: drawio, openMode: 'view', drawioUrl: null, newTabEnabled: true });
    expect(pencil(w)).toHaveLength(0);
  });

  it('is greyed for an administrator, with the reason', () => {
    const w = viewer({ file: drawio, openMode: 'view', drawioUrl: null, canConfigure: true, newTabEnabled: true });
    const [p] = pencil(w);
    expect(p).toBeDefined();
    expect(p.attributes('disabled')).toBeDefined();
    expect(p.attributes('title')).toContain('External services');
  });
});

describe('a test that opens a preview', () => {
  it('leaves nothing behind that mounts once it has ended', async () => {
    const listen = vi.spyOn(window, 'addEventListener');
    await sleep(100);
    await flushPromises();
    const late = listen.mock.calls.filter(([type]) => type === 'message');
    listen.mockRestore();
    expect(late, 'a diagram viewer mounted after the test that opened it had ended').toEqual([]);
  });
});
