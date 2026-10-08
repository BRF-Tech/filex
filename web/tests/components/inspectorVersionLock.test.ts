// A version restore of a document an app has frozen (the signing app, while
// signatures are collected) is refused with 423 and the app's name and reason
// (handlers.lockedAnswer). Since 0.54 (#209, audit A3) the SERVER says it, in
// the reader's language (lockedMessage, srvtext server.applock.*): the
// inspector prints that sentence, never the English "locked by app sign:
// <path>" it used to replace with words of its own.
import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';

import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';

/** The server's 423, in the language it was asked in. */
const SAID = {
  en: 'sign locked this file: imzalar toplanıyor',
  tr: 'sign bu dosyayı kilitledi: imzalar toplanıyor',
} as const;
let refusal: Error = new Error('unset');
function refuse(locale: keyof typeof SAID): Error {
  return Object.assign(new Error(SAID[locale]), {
    status: 423,
    detail: JSON.stringify({
      error: 'locked',
      message: SAID[locale],
      plugin: 'sign',
      reason: 'imzalar toplanıyor',
      path: 'Sozlesmeler/NDA.docx',
    }),
  });
}

const api = {
  listShares: vi.fn(async () => {
    throw new Error('no shares here');
  }),
  listVersions: vi.fn(async () => [
    { id: 3, node_id: 6, version_n: 2, size: 10, created_at: '2026-09-20T10:00:00Z' },
  ]),
  listPermissions: vi.fn(async () => {
    throw new Error('no grants here');
  }),
  listComments: vi.fn(async () => {
    throw new Error('no comments here');
  }),
  createShare: vi.fn(),
  restoreVersion: vi.fn(async () => {
    throw refusal;
  }),
  snapshotVersion: vi.fn(),
  addComment: vi.fn(),
  deleteComment: vi.fn(),
};

const NODE = {
  id: 6,
  path: 'demo://Sozlesmeler/NDA.docx',
  basename: 'NDA.docx',
  type: 'file' as const,
  extension: 'docx',
  size: 20,
  last_modified: 1_757_000_000_000,
};

describe('InspectorPanel — restoring a frozen document', () => {
  for (const [locale, words] of [
    ['en', 'sign'],
    ['tr', 'sign'],
  ] as const) {
    it(`names the app that froze it (${locale})`, async () => {
      refusal = refuse(locale);
      const w = mount(InspectorPanel, {
        props: { api: api as never, nodes: [NODE], dirLabel: 'demo', dirCount: 1, locale },
        attachTo: document.body,
      });
      const restore = () => w.findAll('button').find((b) => /^(Restore|Geri yükle)$/.test(b.text().trim()));
      // Versions live on the Activity tab.
      const activity = () => w.findAll('button').find((b) => /^(Activity|Etkinlik)$/.test(b.text().trim()));
      await vi.waitFor(() => expect(activity()).toBeTruthy());
      await activity()!.trigger('click');
      await vi.waitFor(() => expect(restore()).toBeTruthy());
      await restore()!.trigger('click');
      const confirm = () => w.findAll('button').find((b) => /^(Confirm|Onayla)$/.test(b.text().trim()));
      await vi.waitFor(() => expect(confirm()).toBeTruthy());
      await confirm()!.trigger('click');
      await vi.waitFor(() => expect(w.emitted('toast')).toBeTruthy());
      const said = String(w.emitted('toast')!.at(-1)![0]);
      expect(said).toContain(words);
      expect(said).toContain(SAID[locale]);
      expect(said).toContain('imzalar toplanıyor');
      expect(said).not.toContain('locked by app');
      expect(said).not.toContain('Sozlesmeler/');
      w.unmount();
    });
  }
});
