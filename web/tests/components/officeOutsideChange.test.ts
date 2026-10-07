// The office document changed OUTSIDE the editor while it was open (#184).
//
// The case that started it: a spreadsheet opened from a computer with "Open with
// filex", an agent rewriting it on disk, and the filex window still showing
// the old one (and its next save going over the agent's work). The desktop
// app now notices the change on disk; this file measures what the EDITOR does
// with it - the same component on the web, in the desktop's window and in the
// embeds (PreviewModal), so the rule is written once:
//
//   · nothing unsaved in the editor → the new version is loaded in place,
//     with a note;
//   · an edit in the editor (or a save the host holds) → the three-way
//     question, and nothing reloads, nothing is answered, until the person
//     chooses.
//
// Task #92: every case runs three times - with api.js in the page, with the
// editor in its frame on the interface origin, and with it in its frame on
// the document server's own origin (FILEX_ONLYOFFICE_FRAME_ORIGIN, fm's
// setup) - helpers/officeEditors. The rule does not depend on where the
// editor runs.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import OutsideChangeModal from '@brftech/filex-core/src/modals/OutsideChangeModal.vue';
import { outsideAction } from '@brftech/filex-core/src/lib/outsideChange';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { OFFICE_MODES, installOfficeEditors, type OfficeEditors } from '../helpers/officeEditors';

const OPENED = 'depo://.filex-open/aaaaaaaaaaaa-Bütçe.xlsx';
const NEXT = 'depo://.filex-open/bbbbbbbbbbbb-Bütçe.xlsx';

const xlsx = {
  path: OPENED,
  basename: 'aaaaaaaaaaaa-Bütçe.xlsx',
  type: 'file',
  extension: 'xlsx',
  size: 9_000,
  last_modified: 1_757_376_000,
} as FileNode;

const CONFIG = '/api/files/onlyoffice/config';

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

describe.each(OFFICE_MODES)('the office document changed outside the editor (#184), editor %s', (mode) => {
  let editors: OfficeEditors;
  /** Every config asked for: the path it was asked for. */
  let asked: string[] = [];
  const opened: VueWrapper[] = [];

  afterEach(() => {
    opened.splice(0).forEach((w) => w.unmount());
    editors.uninstall();
  });

  beforeEach(() => {
    asked = [];
    editors = installOfficeEditors(mode);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: { body?: string }) => {
        if (url === CONFIG) {
          const path = JSON.parse(String(init?.body ?? '{}')).path as string;
          asked.push(path);
          return {
            ok: true,
            status: 200,
            json: async () => ({
              documentServerUrl: 'https://docs.example.com',
              config: { document: { key: 'key:' + path } },
              ...editors.answer(),
            }),
            text: async () => '',
          };
        }
        return { ok: true, status: 200, json: async () => ({}), text: async () => '' };
      }),
    );
  });

  async function openEditor(locale: 'en' | 'tr' = 'en') {
    const w = mount(PreviewModal, {
      attachTo: document.body,
      props: {
        open: true,
        locale,
        file: xlsx,
        previewUrl: (p: string) => `/preview?path=${p}`,
        downloadUrl: (p: string) => `/download?path=${p}`,
        onlyOfficeBase: 'https://docs.example.com',
        onlyOfficeConfigEndpoint: CONFIG,
        chromeless: true,
        outsideChange: null,
      },
    });
    opened.push(w);
    await settle();
    expect(editors.created, 'the editor is on the page').toHaveLength(1);
    return w;
  }

  const dialog = () => document.querySelector('[data-testid="outside-change-dialog"]');
  const note = () => document.querySelector('[data-testid="outside-note"]')?.textContent?.trim() ?? '';
  const click = async (id: string) => {
    (document.querySelector(`[data-testid="${id}"]`) as HTMLButtonElement | null)?.click();
    await settle();
  };

  it('nothing unsaved here: the new version loads in place, on the path the host names, with a note', async () => {
    const w = await openEditor();
    await w.setProps({ outsideChange: { seq: 1, path: NEXT } });
    await settle();

    expect(dialog(), 'no question when nothing would be lost').toBeNull();
    expect(editors.destroyed(), 'the old editor went').toBe(1);
    expect(asked, 'the new editor was configured for the new version').toEqual([OPENED, NEXT]);
    expect(editors.created).toEqual(['key:' + OPENED, 'key:' + NEXT]);
    expect(editors.frames(), 'one editor on the page, not two').toBe(1);
    expect(note()).toMatch(/updated outside filex.*new version is loaded/i);
    expect(w.emitted('outside-resolved')?.[0]?.[0]).toEqual({
      seq: 1,
      choice: 'reloaded',
      path: NEXT,
      key: 'key:' + OPENED,
      origin: 'host',
    });
  });

  it('an edit in the editor: the question, and nothing reloads or is answered until the person chooses', async () => {
    const w = await openEditor();
    await editors.state(true);
    await settle();
    expect(w.emitted('office-edited')?.[0]).toEqual([true]);

    await w.setProps({ outsideChange: { seq: 1, path: NEXT } });
    await settle();

    expect(dialog(), 'the three-way question is on screen').not.toBeNull();
    expect(editors.destroyed(), 'the edited editor is still there').toBe(0);
    expect(editors.created).toHaveLength(1);
    expect(w.emitted('outside-resolved'), 'nothing was answered for the person').toBeUndefined();
  });

  it('"edited" is sticky: ONLYOFFICE\'s data:false means sent to the document server, not saved to the file', async () => {
    const w = await openEditor();
    await editors.state(true);
    await editors.state(false);
    await w.setProps({ outsideChange: { seq: 1 } });
    await settle();
    expect(dialog(), 'the edit would have been thrown away by a reload').not.toBeNull();
    expect(editors.destroyed()).toBe(0);
  });

  it('a save the host holds asks too, even with no edit in the editor', async () => {
    const w = await openEditor();
    await w.setProps({ outsideChange: { seq: 1, path: NEXT, pending: true } });
    await settle();
    expect(dialog()).not.toBeNull();
    expect(editors.destroyed()).toBe(0);
  });

  it('"Keep the outside version": the edits here go, the new version loads', async () => {
    const w = await openEditor();
    await editors.state(true);
    await w.setProps({ outsideChange: { seq: 4, path: NEXT } });
    await settle();
    await click('outside-keep-theirs');

    expect(dialog()).toBeNull();
    expect(editors.destroyed()).toBe(1);
    expect(asked).toEqual([OPENED, NEXT]);
    expect(w.emitted('outside-resolved')?.[0]?.[0]).toMatchObject({ seq: 4, choice: 'theirs', path: NEXT });
    expect(w.emitted('office-edited')?.at(-1), 'the new editor has no edits').toEqual([false]);
  });

  it('"Write mine" and "Keep both": the editor stays as it is, the answer goes to the host', async () => {
    for (const [button, choice] of [
      ['outside-write-mine', 'mine'],
      ['outside-keep-both', 'both'],
    ] as const) {
      editors.uninstall();
      editors = installOfficeEditors(mode);
      asked = [];
      const w = await openEditor();
      await editors.state(true);
      await w.setProps({ outsideChange: { seq: 1, path: NEXT } });
      await settle();
      await click(button);

      expect(dialog(), choice).toBeNull();
      expect(editors.destroyed(), choice + ': the edited editor stays').toBe(0);
      expect(asked, choice).toEqual([OPENED]);
      expect(w.emitted('outside-resolved')?.[0]?.[0], choice).toEqual({
        seq: 1,
        choice,
        path: OPENED,
        key: 'key:' + OPENED,
        origin: 'host',
      });
      expect(note(), choice).not.toBe('');
      w.unmount();
      opened.splice(opened.indexOf(w), 1);
    }
  });

  it('Escape puts the question away without answering it; the line under the bar brings it back', async () => {
    const w = await openEditor();
    await editors.state(true);
    await w.setProps({ outsideChange: { seq: 1, path: NEXT } });
    await settle();
    w.findComponent(OutsideChangeModal).vm.$emit('dismiss');
    await settle();

    expect(dialog()).toBeNull();
    expect(w.emitted('outside-resolved'), 'a dismissed question is not an answer').toBeUndefined();
    expect(document.querySelector('[data-testid="outside-pending"]')).not.toBeNull();
    await click('outside-choose');
    expect(dialog()).not.toBeNull();
  });

  it('one change is handled once, however often the host hands it over', async () => {
    const w = await openEditor();
    await w.setProps({ outsideChange: { seq: 1, path: NEXT } });
    await settle();
    await w.setProps({ outsideChange: { seq: 1, path: NEXT } });
    await settle();
    expect(editors.created).toHaveLength(2);
    expect(w.emitted('outside-resolved')).toHaveLength(1);
  });

  it('the question in Turkish, with the document\'s own name (no working-copy prefix)', async () => {
    const w = await openEditor('tr');
    await editors.state(true);
    await w.setProps({ outsideChange: { seq: 1, path: NEXT } });
    await settle();
    const text = dialog()?.closest('.fe-modal__card, [role="dialog"]')?.textContent ?? document.body.textContent ?? '';
    expect(text).toContain('Dışarıdakini koru');
    expect(text).toContain('Benimkini yaz');
    expect(text).toContain('İkisini de tut');
    expect(text).toContain('Bütçe.xlsx');
    expect(text).not.toContain('aaaaaaaaaaaa-');
  });
});

describe('the rule itself', () => {
  it('reload only when nothing of the editor would be lost', () => {
    expect(outsideAction(false, {})).toBe('reload');
    expect(outsideAction(true, {})).toBe('ask');
    expect(outsideAction(false, { pending: true })).toBe('ask');
  });
});
