// The signing round of 2026-09-21 — what the owner found in twenty minutes
// of using it, pinned where each thing is decided:
//
//   item 6  placing and dragging a box flickered, and a dragged box jumped to
//           the document's edge (SurfacePdfFields);
//   item 4  a date / text / tick box's owner is the one who FILLS it, not the
//           one who signs it, and "Anyone" never showed as chosen
//           (PdfFieldEditor);
//   item 3  a signature is drawn or typed, and only a typed one has a face;
//   item 2  what is printed under a signature is chosen per box, previewed in
//           the plugin's own words;
//   item 8  a required signature wears the same `*` as a required field
//           (SurfaceSignaturePad);
//   item 1  a home page has a menu of sections, and the page asks for the
//           one its address names (SurfaceSections, PluginPageView).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

const fakePage = {
  getViewport: ({ scale }: { scale: number }) => ({ width: 500 * scale, height: 700 * scale, rotation: 0 }),
  render: () => ({ promise: Promise.resolve() }),
};
const fakeDoc = { numPages: 1, getPage: async () => fakePage, destroy: vi.fn() };
const lib = { getDocument: vi.fn(() => ({ promise: Promise.resolve(fakeDoc) })) };

vi.mock('@brftech/filex-core/src/lib/pdfjsLoader', () => ({
  loadPdfjs: async () => lib,
  defaultPdfWorkerUrl: () => '',
  resetPdfjsLoader: () => {},
}));

import SurfacePdfFields from '@brftech/filex-core/src/components/plugin/nodes/SurfacePdfFields.vue';
import PdfFieldEditor from '@brftech/filex-core/src/components/plugin/nodes/PdfFieldEditor.vue';
import SurfaceSignaturePad from '@brftech/filex-core/src/components/plugin/nodes/SurfaceSignaturePad.vue';
import PluginPageView from '@brftech/filex-core/src/components/plugin/PluginPageView.vue';
import { mockCanvas2d } from '../fixtures/canvas2d';
import { actionIconSvg } from '@brftech/filex-core/src/lib/actionIcons';

const fetchMock = vi.fn();

beforeEach(() => {
  lib.getDocument.mockClear();
  fetchMock.mockReset();
  fetchMock.mockResolvedValue({ ok: true, status: 200, statusText: '', arrayBuffer: async () => new ArrayBuffer(8), text: async () => '' });
  vi.stubGlobal('fetch', fetchMock);
  mockCanvas2d();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const placed = [
  { id: 'sig-1', type: 'signature', page: 1, x: 0.2, y: 0.5, w: 0.2, h: 0.1, assignee: 'a' },
  { id: 'date-1', type: 'date', page: 1, x: 0.6, y: 0.5, w: 0.2, h: 0.05, assignee: 'a' },
];
const signers = [{ id: 'a', label: { en: 'Ann' } }];

/**
 * A page that MEASURES like a page: 500×700 at the origin while it is in
 * the document, 0×0 once it is not — which is what a real browser answers
 * for an element that was taken out of the page under a gesture.
 */
function measurablePages() {
  const orig = HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.classList.contains('fe-spdf__page')) {
      return this.isConnected
        ? ({ left: 0, top: 0, width: 500, height: 700, right: 500, bottom: 700, x: 0, y: 0 } as DOMRect)
        : ({ left: 0, top: 0, width: 0, height: 0, right: 0, bottom: 0, x: 0, y: 0 } as DOMRect);
    }
    return orig.call(this);
  });
}

function pointer(type: string, x: number, y: number) {
  const ev = new MouseEvent(type, { clientX: x, clientY: y, bubbles: true });
  window.dispatchEvent(ev);
}

async function placeStep(extra: Record<string, unknown> = {}) {
  const w = mount(SurfacePdfFields, {
    attachTo: document.body,
    props: { id: 'doc', src: { url: '/nda.pdf' }, mode: 'place', fields: placed, modelValue: placed, signers, locale: 'en', ...extra },
  });
  await vi.waitFor(() => expect(w.find('[data-testid="surface-pdf-fields"]').attributes('data-state')).toBe('ok'));
  return w;
}

function boxX(w: ReturnType<typeof mount>, id: string): number {
  const style = w.find(`[data-testid="surface-pdf-field-${id}"]`).attributes('style') ?? '';
  return Number(/left: ([\d.]+)%/.exec(style)?.[1] ?? NaN) / 100;
}

describe('item 6 — placing and dragging', () => {
  it('a surface that comes back with the same document does not reload it', async () => {
    const w = await placeStep();
    expect(lib.getDocument).toHaveBeenCalledTimes(1);
    // Every `change` answer hands the node a NEW src object with the same
    // strings. Reloading on that was the flicker: "Loading…", every canvas
    // thrown away and drawn again, after every placement.
    await w.setProps({ src: { url: '/nda.pdf' }, fields: placed.map((f) => ({ ...f })) });
    await flushPromises();
    expect(lib.getDocument).toHaveBeenCalledTimes(1);
    expect(w.find('[data-testid="surface-pdf-fields"]').attributes('data-state')).toBe('ok');
    // ...while a different document still is one.
    await w.setProps({ src: { url: '/other.pdf' } });
    await flushPromises();
    expect(lib.getDocument).toHaveBeenCalledTimes(2);
    w.unmount();
  });

  it('a box dragged over a page that is re-drawn under the gesture stays where it was — never the edge', async () => {
    measurablePages();
    const w = await placeStep();
    const x0 = boxX(w, 'sig-1');
    await w.find('[data-testid="surface-pdf-field-sig-1"]').trigger('pointerdown', { clientX: 150, clientY: 385 });
    pointer('pointermove', 175, 385);
    await w.vm.$nextTick();
    expect(boxX(w, 'sig-1')).toBeCloseTo(x0 + 25 / 500, 5);
    // The page element is taken out from under the gesture — what a reload
    // did. A detached element measures 0×0, which used to read every pointer
    // position as the far corner and pin the box to the document's edge.
    const page = w.find('.fe-spdf__page').element as HTMLElement;
    const parent = page.parentElement!;
    parent.removeChild(page);
    pointer('pointermove', 400, 600);
    await w.vm.$nextTick();
    parent.appendChild(page);
    const x = boxX(w, 'sig-1');
    expect(x).toBeCloseTo(x0 + 25 / 500, 5);
    expect(x).toBeLessThan(1 - 0.2 - 1e-6);
    pointer('pointerup', 400, 600);
    w.unmount();
  });

  it('an echo that arrives mid-drag does not snap the box back under the pointer', async () => {
    measurablePages();
    const w = await placeStep();
    const x0 = boxX(w, 'sig-1');
    await w.find('[data-testid="surface-pdf-field-sig-1"]').trigger('pointerdown', { clientX: 150, clientY: 385 });
    pointer('pointermove', 200, 385);
    await w.vm.$nextTick();
    // The debounced `change` answer for an EARLIER edit lands now, carrying
    // the box where it was before this gesture.
    await w.setProps({ fields: placed.map((f) => ({ ...f })), modelValue: placed.map((f) => ({ ...f })) });
    await w.vm.$nextTick();
    expect(boxX(w, 'sig-1')).toBeCloseTo(x0 + 50 / 500, 5);
    pointer('pointerup', 200, 385);
    await w.vm.$nextTick();
    const sent = w.emitted('update:modelValue')?.at(-1)?.[0] as Array<{ id: string; x: number }>;
    expect(sent.find((f) => f.id === 'sig-1')!.x).toBeCloseTo(x0 + 50 / 500, 5);
    w.unmount();
  });

  it('a document loaded on the define step is drawn when the place step shows it', async () => {
    // The same node lives through both steps. Its document finishes loading
    // on `define`, where no page exists; the pages arrive with `place`, and
    // they must be drawn then — they used to stay blank once the reload that
    // accidentally drew them was gone.
    // An observer that reports every page as on screen the moment it is
    // watched, as a browser does for a page in view.
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(private cb: (e: Array<{ isIntersecting: boolean; target: Element }>) => void) {}
        observe(el: Element) {
          this.cb([{ isIntersecting: true, target: el }]);
        }
        unobserve() {}
        disconnect() {}
      },
    );
    const w = mount(SurfacePdfFields, {
      attachTo: document.body,
      props: { id: 'doc', src: { url: '/nda.pdf' }, mode: 'define', fields: placed, modelValue: placed, signers, locale: 'en' },
    });
    await vi.waitFor(() => expect(w.find('[data-testid="surface-pdf-fields"]').attributes('data-state')).toBe('ok'));
    expect(w.find('.fe-spdf__page').exists()).toBe(false);
    await w.setProps({ mode: 'place' });
    await vi.waitFor(() => expect(w.find('.fe-spdf__page canvas.fe-spdf__canvas').exists()).toBe(true));
    expect(lib.getDocument).toHaveBeenCalledTimes(1);
    w.unmount();
  });

  it('the place step keeps ONE strip of constant height, whatever it is saying', async () => {
    const withWaiting = [...placed, { id: 'text-1', type: 'text', page: 1, x: 0.1, y: 0.1, w: 0.2, h: 0.04, placed: false }];
    const w = await placeStep({ fields: withWaiting, modelValue: withWaiting });
    const strip = () => w.find('[data-testid="surface-pdf-strip"]');
    expect(strip().exists()).toBe(true);
    expect(w.find('[data-testid="surface-pdf-idle-hint"]').exists()).toBe(true);
    await w.find('[data-testid="surface-pdf-pending-text-1"]').trigger('click');
    expect(strip().find('[data-testid="surface-pdf-place-hint"]').exists()).toBe(true);
    // The hint lives IN the strip now, never above it as a paragraph of its
    // own that came and went.
    expect(w.findAll('[data-testid="surface-pdf-place-hint"]')).toHaveLength(1);
    w.unmount();
  });
});

function editor(field: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  return mount(PdfFieldEditor, {
    props: {
      field: { page: 1, x: 0.1, y: 0.1, w: 0.2, h: 0.05, ...field } as never,
      signers: [{ id: 'a', label: 'Ann' }, { id: 'b', label: 'Bob' }],
      locale: 'tr',
      ...extra,
    },
  });
}

describe('item 4 — who fills a box, and "Anyone"', () => {
  it('a date, a text and a tick are FILLED by somebody; a signature is signed', () => {
    for (const type of ['date', 'text', 'checkbox']) {
      expect(editor({ id: 'x', type }).find('[data-testid="surface-pdf-assignee-label"]').text()).toBe('Dolduran');
    }
    for (const type of ['signature', 'initials']) {
      expect(editor({ id: 'x', type }).find('[data-testid="surface-pdf-assignee-label"]').text()).toBe('İmzacı');
    }
    const en = editor({ id: 'x', type: 'date' }, { locale: 'en' });
    expect(en.find('[data-testid="surface-pdf-assignee-label"]').text()).toBe('Filled by');
  });

  it('a box that belongs to anyone shows "Anyone" pressed', () => {
    const w = editor({ id: 'x', type: 'text' });
    const pressed = w.findAll('[data-testid^="surface-pdf-assignee-"][aria-checked="true"]');
    expect(pressed).toHaveLength(1);
    expect(pressed[0].text()).toBe('Herkes');
  });

  it('choosing "Anyone" leaves no assignee on the box', async () => {
    const w = editor({ id: 'x', type: 'text', assignee: 'a' });
    await w.find('[data-testid="surface-pdf-assignee-*"]').trigger('click');
    const patched = w.emitted('patch')?.at(-1)?.[0] as Record<string, unknown>;
    expect('assignee' in patched).toBe(false);
  });
});

describe('item 3 — drawn or typed, and the face only for typed', () => {
  it('a drawn signature asks no face; a typed one does', async () => {
    const drawn = editor({ id: 's', type: 'signature' });
    expect(drawn.find('[data-testid="surface-pdf-style-drawn"]').attributes('aria-checked')).toBe('true');
    expect(drawn.find('[data-testid="surface-pdf-fonts"]').exists()).toBe(false);
    await drawn.find('[data-testid="surface-pdf-style-typed"]').trigger('click');
    expect((drawn.emitted('patch')?.at(-1)?.[0] as Record<string, unknown>).style).toBe('typed');

    const typed = editor({ id: 's', type: 'signature', style: 'typed' });
    expect(typed.find('[data-testid="surface-pdf-fonts"]').exists()).toBe(true);
    // Text keeps its face; a tick never had one.
    expect(editor({ id: 't', type: 'text' }).find('[data-testid="surface-pdf-fonts"]').exists()).toBe(true);
    expect(editor({ id: 'c', type: 'checkbox' }).find('[data-testid="surface-pdf-style"]').exists()).toBe(false);
  });

  it('the signer’s pad offers exactly what was chosen', () => {
    const typed = mount(SurfaceSignaturePad, {
      props: { id: 'p', modes: ['type'], font: 'source-serif', fonts: ['source-serif'], modelValue: null, locale: 'en' },
    });
    expect(typed.find('[data-testid="surface-signature-mode-draw"]').exists()).toBe(false);
    expect(typed.find('[data-testid="surface-signature-typed"]').exists()).toBe(true);
    // One face chosen by the requester: no picker to override it.
    expect(typed.find('[data-testid="surface-signature-fonts"]').exists()).toBe(false);
    const drawn = mount(SurfaceSignaturePad, { props: { id: 'p', modes: ['draw', 'upload'], modelValue: null, locale: 'en' } });
    expect(drawn.find('[data-testid="surface-signature-mode-type"]').exists()).toBe(false);
  });
});

describe('item 2 — what is printed under a signature', () => {
  const catalogue = [
    { id: 'name', label: { en: "Signer's name", tr: 'İmzacının adı' }, default: true, examples: { a: 'Ann Lee', '*': 'Whoever signs' } },
    { id: 'date', label: { en: 'Date and time', tr: 'Tarih ve saat' }, default: true, examples: { '*': 'Signed 2026-09-21 14:05 UTC' } },
    { id: 'ip', label: { en: 'IP address', tr: 'IP adresi' }, examples: { '*': 'IP address: given when they sign' } },
  ];

  it('a box shows the plugin’s defaults, previewed in the plugin’s own words', () => {
    const w = editor({ id: 's', type: 'signature', assignee: 'a' }, { stampLines: catalogue, locale: 'en' });
    expect(w.find('[data-testid="surface-pdf-line-name"]').attributes('aria-pressed')).toBe('true');
    expect(w.find('[data-testid="surface-pdf-line-ip"]').attributes('aria-pressed')).toBe('false');
    const preview = w.find('[data-testid="surface-pdf-lines-preview"]').text();
    expect(preview).toContain('Ann Lee');
    expect(preview).toContain('Signed 2026-09-21 14:05 UTC');
    expect(preview).not.toContain('IP address');
  });

  it('adding and removing lines is the box’s own choice — and "none" stays none', async () => {
    const w = editor({ id: 's', type: 'signature' }, { stampLines: catalogue, locale: 'en' });
    await w.find('[data-testid="surface-pdf-line-ip"]').trigger('click');
    expect((w.emitted('patch')?.at(-1)?.[0] as { lines: string[] }).lines).toEqual(['name', 'date', 'ip']);
    const none = editor({ id: 's', type: 'signature', lines: [] }, { stampLines: catalogue, locale: 'en' });
    expect(none.find('[data-testid="surface-pdf-lines-preview"]').text()).toContain('Nothing is printed');
  });

  it('a date or a text box is offered no lines at all', () => {
    expect(editor({ id: 'd', type: 'date' }, { stampLines: catalogue }).find('[data-testid="surface-pdf-lines"]').exists()).toBe(false);
  });
});

describe('item 8 — a required signature says so', () => {
  it('wears the same label and star a required field does', () => {
    const w = mount(SurfaceSignaturePad, {
      props: { id: 'p', label: 'Yetkili imzası', required: true, modelValue: null, locale: 'tr' },
    });
    const label = w.find('[data-testid="surface-signature-label"]');
    expect(label.classes()).toContain('fe-cfield__label');
    expect(label.text()).toBe('Yetkili imzası*');
    expect(label.find('.fe-cfield__req').exists()).toBe(true);
    const optional = mount(SurfaceSignaturePad, { props: { id: 'p', label: 'Paraf', modelValue: null, locale: 'tr' } });
    expect(optional.find('.fe-cfield__req').exists()).toBe(false);
  });
});

describe('item 1 — a home page with a menu of sections', () => {
  const home = (section: string) => ({
    title: { en: 'Signatures' },
    section,
    sections: [
      { id: 'to-sign', label: { en: 'Waiting for my signature' }, count: 2 },
      { id: 'requested', label: { en: 'I asked for these' }, count: 0 },
      { id: 'about', label: { en: 'How it works' } },
    ],
    nodes: [{ type: 'text', props: { text: { en: `section ${section}` } } }],
  });

  it('draws the menu, asks for the section its address names, and follows the address', async () => {
    const pluginView = vi.fn(async (_p: string, _v: string, _path?: string, section?: string) => ({ surface: home(section ?? 'to-sign') }));
    const w = mount(PluginPageView, {
      props: { locale: 'en' as const, api: { pluginView, pluginViewEvent: vi.fn() } as never, plugin: 'sign', view: 'envelopes', frame: 'embedded' as const, section: 'requested' },
    });
    await flushPromises();
    expect(pluginView).toHaveBeenLastCalledWith('sign', 'envelopes', undefined, 'requested');
    expect(w.find('[data-testid="surface-section-requested"]').attributes('aria-selected')).toBe('true');
    expect(w.find('[data-testid="surface-section-count-to-sign"]').text()).toBe('2');
    expect(w.find('[data-testid="surface-section-count-about"]').exists()).toBe(false);

    // A choice is told to the host (which keeps the address) and asked for.
    await w.find('[data-testid="surface-section-to-sign"]').trigger('click');
    await flushPromises();
    expect(w.emitted('section')?.at(-1)).toEqual(['to-sign']);
    expect(pluginView).toHaveBeenLastCalledWith('sign', 'envelopes', undefined, 'to-sign');
    const calls = pluginView.mock.calls.length;
    // The host echoing that choice back is not a second request...
    await w.setProps({ section: 'to-sign' });
    await flushPromises();
    expect(pluginView.mock.calls.length).toBe(calls);
    // ...but Back — the address moving on its own — is.
    await w.setProps({ section: 'requested' });
    await flushPromises();
    expect(pluginView).toHaveBeenLastCalledWith('sign', 'envelopes', undefined, 'requested');
    expect(w.text()).toContain('section requested');
    // A page opened WITH a section never rewrites the address.
    expect(w.emitted('section')?.some((e) => (e[1] as { replace?: boolean } | undefined)?.replace)).toBeFalsy();
  });

  it('writes the section it landed on into the address, without a step in history', async () => {
    // Opened bare (the side bar's row): the plugin shows its first choice,
    // and the address has to say which — or choosing that same entry from
    // the menu changes nothing, and a copied link lands wherever the
    // default happens to be (measured in the e2e round).
    const pluginView = vi.fn(async (_p: string, _v: string, _path?: string, section?: string) => ({ surface: home(section ?? 'requested') }));
    const w = mount(PluginPageView, {
      props: { locale: 'en' as const, api: { pluginView, pluginViewEvent: vi.fn() } as never, plugin: 'sign', view: 'envelopes', frame: 'embedded' as const },
    });
    await flushPromises();
    expect(pluginView).toHaveBeenLastCalledWith('sign', 'envelopes', undefined);
    expect(w.emitted('section')?.at(-1)).toEqual(['requested', { replace: true }]);
    const calls = pluginView.mock.calls.length;
    // The host writing it back is not a second request.
    await w.setProps({ section: 'requested' });
    await flushPromises();
    expect(pluginView.mock.calls.length).toBe(calls);
  });
});

// The app's `sign` icon is in the shared catalogue (2026-09-21, a tester: the
// sidebar and every menu showed the Signatures app with the generic plugin
// piece, because the key it names was not drawn anywhere).
describe('the e-signature app icon', () => {
  it('is drawn, and is not the generic plugin piece', () => {
    const sign = actionIconSvg('sign');
    expect(sign).not.toBe('');
    expect(sign).not.toBe(actionIconSvg('plugin'));
  });
});
