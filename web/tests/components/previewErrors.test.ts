// The viewer says a failure in words — not "404 Not Found", not
// "save failed: 500 {…}", not "Monaco mount fail: …" (QA, 2026-09-21).
// An administrator gets the raw words as a second line; nobody else does.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import { wordsIn } from '@brftech/filex-core/src/lib/errorWords';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const tr = wordsIn('tr');
const json = {
  path: 'depo://ayarlar.json',
  basename: 'ayarlar.json',
  type: 'file',
  extension: 'json',
  size: 120,
  last_modified: 1_757_376_000,
} as FileNode;

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

function viewer(canConfigure: boolean) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('{"error":"open /var/lib/filex/x: permission denied"}', { status: 500, statusText: 'Internal Server Error' })),
  );
  return mount(PreviewModal, {
    props: {
      open: true,
      locale: 'tr',
      file: json,
      openMode: 'view',
      canConfigure,
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
    },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe('a file that cannot be loaded', () => {
  it('a person reads the sentence, and nothing of the server’s plumbing', async () => {
    const w = viewer(false);
    await settle();
    const text = w.text();
    expect(text).toContain(tr('err.status.500'));
    expect(text).not.toMatch(/\b500\b|Internal Server Error|permission denied|\{/);
    expect(w.find('[data-testid="preview-error-detail"]').exists()).toBe(false);
  });

  it('an administrator also reads the raw words, as a second line', async () => {
    const w = viewer(true);
    await settle();
    expect(w.text()).toContain(tr('err.status.500'));
    expect(w.get('[data-testid="preview-error-detail"]').text()).toContain('permission denied');
  });
});
