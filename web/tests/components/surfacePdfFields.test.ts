// SurfacePdfFields — the boxes over a one-page mock document: in fill mode
// the signer's own fields are interactive and everybody else's are grey
// outlines with the signer's name; a tick, a date and a signature go back as
// `{fields: [{id, value}]}`. In edit mode the palette is the node's `types`
// and a deleted field leaves the whole array that goes back. pdf.js is
// mocked at the loader seam; a document that fails to load is an error node.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';

const fakePage = {
  getViewport: ({ scale }: { scale: number }) => ({ width: 612 * scale, height: 792 * scale, rotation: 0 }),
  render: () => ({ promise: Promise.resolve() }),
};
const fakeDoc = { numPages: 1, getPage: async () => fakePage, destroy: vi.fn() };
const lib = { getDocument: vi.fn(() => ({ promise: Promise.resolve(fakeDoc) })) };
let libAvailable = true;

vi.mock('@brftech/filex-core/src/lib/pdfjsLoader', () => ({
  loadPdfjs: async () => (libAvailable ? lib : null),
  defaultPdfWorkerUrl: () => '',
  resetPdfjsLoader: () => {},
}));

import SurfacePdfFields from '@brftech/filex-core/src/components/plugin/nodes/SurfacePdfFields.vue';
import { todayIso } from '@brftech/filex-core/src/lib/pdfFields';
import { mockCanvas2d } from '../fixtures/canvas2d';

const fetchMock = vi.fn();

beforeEach(() => {
  libAvailable = true;
  fetchMock.mockReset();
  fetchMock.mockResolvedValue({ ok: true, status: 200, statusText: '', arrayBuffer: async () => new ArrayBuffer(8), text: async () => '' });
  vi.stubGlobal('fetch', fetchMock);
  mockCanvas2d();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const fields = [
  { id: 'sig-1', type: 'signature', page: 1, x: 0.1, y: 0.8, w: 0.3, h: 0.06, assignee: 'a', required: true },
  { id: 'sig-2', type: 'signature', page: 1, x: 0.5, y: 0.8, w: 0.3, h: 0.06, assignee: 'b' },
  { id: 'chk-1', type: 'checkbox', page: 1, x: 0.1, y: 0.2, w: 0.03, h: 0.02, assignee: 'a' },
  { id: 'date-1', type: 'date', page: 1, x: 0.1, y: 0.3, w: 0.2, h: 0.03, assignee: 'a' },
  { id: 'text-1', type: 'text', page: 1, x: 0.1, y: 0.4, w: 0.3, h: 0.03 },
];
const signers = [
  { id: 'a', label: { en: 'Ann', tr: 'Ayşe' } },
  { id: 'b', label: { en: 'Bob' }, color: '#123456' },
];

function draw(extra: Record<string, unknown> = {}) {
  return mount(SurfacePdfFields, {
    props: { id: 'doc', src: { url: '/nda.pdf' }, mode: 'fill', fields, signers, signer: 'a', locale: 'en', ...extra },
  });
}

async function ready(w: ReturnType<typeof draw>) {
  await vi.waitFor(() => expect(w.find('[data-testid="surface-pdf-fields"]').attributes('data-state')).toBe('ok'));
}

describe('SurfacePdfFields — fill mode', () => {
  it('draws the page with the boxes as percentages, mine interactive and the others grey outlines', async () => {
    const w = draw();
    await ready(w);
    expect(fetchMock).toHaveBeenCalledWith('/nda.pdf', expect.objectContaining({ credentials: 'same-origin' }));
    expect(w.findAll('.fe-spdf__page')).toHaveLength(1);
    const mine = w.find('[data-testid="surface-pdf-field-sig-1"]');
    expect(mine.classes()).toContain('is-mine');
    expect(mine.attributes('role')).toBe('button');
    expect(mine.attributes('style')).toContain('left: 10%');
    expect(mine.attributes('style')).toContain('top: 80%');
    expect(mine.text()).toContain('Tap to sign');
    const theirs = w.find('[data-testid="surface-pdf-field-sig-2"]');
    expect(theirs.classes()).toContain('is-foreign');
    expect(theirs.attributes('role')).toBeUndefined();
    expect(theirs.text()).toContain('Bob');
    expect(theirs.attributes('style')).toContain('#123456');
    // An unassigned field is anybody's.
    expect(w.find('[data-testid="surface-pdf-field-text-1"]').classes()).toContain('is-mine');
    expect(w.find('[data-testid="surface-pdf-text-text-1"]').exists()).toBe(true);
  });

  it('a tick, a date and a text go back as {fields: [{id, value}]} — the signer’s own only', async () => {
    const w = draw();
    await ready(w);
    await w.find('[data-testid="surface-pdf-field-chk-1"]').trigger('click');
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([{ fields: [{ id: 'chk-1', value: true }] }]);
    await w.find('[data-testid="surface-pdf-field-date-1"]').trigger('click');
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([{ fields: [{ id: 'chk-1', value: true }, { id: 'date-1', value: todayIso() }] }]);
    await w.find('[data-testid="surface-pdf-text-text-1"]').setValue('Ada');
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([
      { fields: [{ id: 'chk-1', value: true }, { id: 'date-1', value: todayIso() }, { id: 'text-1', value: 'Ada' }] },
    ]);
    // A second tap on the tick clears it.
    await w.find('[data-testid="surface-pdf-field-chk-1"]').trigger('click');
    expect((w.emitted('update:modelValue')?.at(-1)?.[0] as { fields: { id: string }[] }).fields.map((f) => f.id)).toEqual(['date-1', 'text-1']);
  });

  it('a signature field opens the pad inline; its picture becomes the value', async () => {
    const w = draw();
    await ready(w);
    expect(w.find('[data-testid="surface-pdf-pad"]').exists()).toBe(false);
    await w.find('[data-testid="surface-pdf-field-sig-1"]').trigger('click');
    const pad = w.find('[data-testid="surface-pdf-pad"]');
    expect(pad.exists()).toBe(true);
    expect(pad.text()).toContain('Your Signature');
    const inner = w.findComponent({ name: 'SurfaceSignaturePad' });
    inner.vm.$emit('update:modelValue', { png_b64: 'QUJD', mode: 'draw' });
    await w.vm.$nextTick();
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([{ fields: [{ id: 'sig-1', value: 'QUJD' }] }]);
    expect(w.find('[data-testid="surface-pdf-field-sig-1"] img').attributes('src')).toBe('data:image/png;base64,QUJD');
    await w.find('[data-testid="surface-pdf-pad-done"]').trigger('click');
    expect(w.find('[data-testid="surface-pdf-pad"]').exists()).toBe(false);
  });

  it('another signer’s fields are never interactive, even when tapped', async () => {
    const w = draw({ signer: 'b' });
    await ready(w);
    await w.find('[data-testid="surface-pdf-field-chk-1"]').trigger('click');
    expect(w.emitted('update:modelValue')).toBeUndefined();
    expect(w.find('[data-testid="surface-pdf-field-sig-2"]').classes()).toContain('is-mine');
    expect(w.find('[data-testid="surface-pdf-field-sig-1"]').text()).toContain('Ann');
  });

  it('values that arrived with the surface are laid back over the boxes', async () => {
    const w = draw({ modelValue: { fields: [{ id: 'chk-1', value: true }, { id: 'date-1', value: '2026-09-19' }] } });
    await ready(w);
    expect(w.find('[data-testid="surface-pdf-field-chk-1"]').classes()).toContain('is-filled');
    expect(w.find('[data-testid="surface-pdf-field-date-1"]').text()).toBe('2026-09-19');
  });
});

describe('SurfacePdfFields — edit mode', () => {
  it('the palette is the node’s `types`; selecting and deleting a field sends the whole array', async () => {
    const w = draw({ mode: 'edit', signer: undefined, types: ['signature', 'date'] });
    await ready(w);
    expect(w.findAll('.fe-spdf__palette button').map((b) => b.text())).toEqual(['Signature', 'Date']);
    // Every box is drawn with its signer's name in edit mode; none is "mine".
    expect(w.find('[data-testid="surface-pdf-field-sig-1"]').text()).toContain('Ann');
    expect(w.findAll('.is-mine')).toHaveLength(0);
    await w.find('[data-testid="surface-pdf-field-sig-2"]').trigger('pointerdown', { clientX: 5, clientY: 5 });
    window.dispatchEvent(new Event('pointerup'));
    await w.vm.$nextTick();
    expect(w.find('[data-testid="surface-pdf-selected"]').exists()).toBe(true);
    // ⚠⚠ v3 §2 — buttons, not a dropdown: the signers are readable without
    // opening anything, and the chosen one says so through aria-checked.
    expect(w.find('[data-testid="surface-pdf-assignee-b"]').attributes('aria-checked')).toBe('true');
    expect(w.find('[data-testid="surface-pdf-assignee-a"]').attributes('aria-checked')).toBe('false');
    expect(w.findAll('.fe-spdf__props select')).toHaveLength(0);
    await w.find('[data-testid="surface-pdf-delete"]').trigger('click');
    const sent = w.emitted('update:modelValue')?.at(-1)?.[0] as Array<{ id: string }>;
    expect(sent.map((f) => f.id)).toEqual(['sig-1', 'chk-1', 'date-1', 'text-1']);
    expect(w.find('[data-testid="surface-pdf-field-sig-2"]').exists()).toBe(false);
  });

  it('re-assigning and marking required change the array too', async () => {
    const w = draw({ mode: 'edit', signer: undefined });
    await ready(w);
    await w.find('[data-testid="surface-pdf-field-text-1"]').trigger('pointerdown', { clientX: 5, clientY: 5 });
    window.dispatchEvent(new Event('pointerup'));
    await w.vm.$nextTick();
    await w.find('[data-testid="surface-pdf-assignee-a"]').trigger('click');
    await w.find('[data-testid="surface-pdf-required"]').setValue(true);
    const sent = w.emitted('update:modelValue')?.at(-1)?.[0] as Array<{ id: string; assignee?: string; required?: boolean }>;
    expect(sent.find((f) => f.id === 'text-1')).toMatchObject({ assignee: 'a', required: true });
  });
});

describe('SurfacePdfFields — sources and failure', () => {
  it('`src.ref` goes through the host’s resolver; `src.path` through the authenticated preview fetch', async () => {
    const fileUrl = vi.fn((ref: string) => ({ url: `/api/p/tok/file/${ref}` }));
    const w = draw({ src: { ref: 'pub:0' }, fileUrl });
    await ready(w);
    expect(fileUrl).toHaveBeenCalledWith('pub:0');
    expect(fetchMock).toHaveBeenCalledWith('/api/p/tok/file/pub:0', expect.anything());

    const fetchBlob = vi.fn(async () => ({ url: 'blob:x', blob: new Blob([new Uint8Array(4)]), mime: 'application/pdf' }));
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    const p = draw({ src: { path: 'docs://nda.pdf' }, api: { fetchBlob } });
    await ready(p);
    expect(fetchBlob).toHaveBeenCalledWith('docs://nda.pdf');
  });

  it('a document that will not load is an error node, never a blank', async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 404, statusText: 'Not Found', text: async () => '' });
    const w = draw();
    await vi.waitFor(() => expect(w.find('[data-testid="surface-pdf-error"]').exists()).toBe(true));
    expect(w.text()).toContain('The document could not be loaded');

    libAvailable = false;
    fetchMock.mockResolvedValue({ ok: true, status: 200, arrayBuffer: async () => new ArrayBuffer(8) });
    const nolib = draw();
    await vi.waitFor(() => expect(nolib.find('[data-testid="surface-pdf-error"]').exists()).toBe(true));

    const nosrc = draw({ src: {} });
    await vi.waitFor(() => expect(nosrc.find('[data-testid="surface-pdf-error"]').exists()).toBe(true));
  });
});

// ── Rules and faces (v2) ─────────────────────────────────────────────────
//
// ⚠⚠ A `text` field's rule is enforced WHILE the person types, not at submit:
// an invalid signature page that only says so on the way out is a page that
// gets signed twice. And the rule and the face TRAVEL with the value, because
// the stamping plugin reads that array and never sees this screen.
describe('SurfacePdfFields — a text field’s rule and face', () => {
  const ruled = [
    // ⚠ v3 §3.3 — no `date` rule any more: a date is a FIELD TYPE. What is
    // left for a text box is free / number / e-mail plus the length bounds.
    { id: 'text-1', type: 'text', page: 1, x: 0.1, y: 0.4, w: 0.3, h: 0.03, label: 'E-posta', rule: { kind: 'email' } },
    { id: 'text-2', type: 'text', page: 1, x: 0.1, y: 0.5, w: 0.3, h: 0.03, rule: { kind: 'number', max: 4 } },
  ];

  it('shapes the keystrokes and writes the shaped value back into the box', async () => {
    const w = draw({ fields: ruled, signers: [], signer: undefined });
    await ready(w);
    const mail = w.find<HTMLInputElement>('[data-testid="surface-pdf-text-text-1"]');
    // The shaping happens as it is typed — a space in an address is dropped
    // at the keystroke, not argued about on the way out.
    await mail.setValue(' a b@c.de ');
    expect(mail.element.value).toBe('ab@c.de');
    // v3 §3.1 — the box carries its NAME, so the person filling it is told
    // what it is for rather than that it is "Text".
    expect(mail.attributes('placeholder')).toBe('E-posta');
    expect(mail.attributes('aria-label')).toBe('E-posta');
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([
      { fields: [{ id: 'text-1', value: 'ab@c.de', label: 'E-posta', rule: { kind: 'email' } }] },
    ]);

    const num = w.find<HTMLInputElement>('[data-testid="surface-pdf-text-text-2"]');
    await num.setValue('12ab345');
    expect(num.element.value).toBe('1234');
    expect(num.attributes('inputmode')).toBe('numeric');
    expect(num.attributes('maxlength')).toBe('4');
  });

  it('says what is wrong under the box, not inside it', async () => {
    const w = draw({ fields: ruled, signers: [], signer: undefined });
    await ready(w);
    // A hint before anything is typed; the verdict once there is something to
    // judge. Never a message inside a field a few millimetres tall.
    expect(w.find('[data-testid="surface-pdf-rule-text-1"]').text()).toContain('Email');
    await w.find('[data-testid="surface-pdf-text-text-1"]').setValue('not-an-address');
    const said = w.find('[data-testid="surface-pdf-rule-text-1"]');
    expect(said.classes()).toContain('is-bad');
    expect(said.attributes('role')).toBe('alert');
  });

  it('an empty box is never WRONG — that is what `required` is for', async () => {
    const w = draw({ fields: ruled, signers: [], signer: undefined });
    await ready(w);
    await w.find('[data-testid="surface-pdf-text-text-1"]').setValue('');
    expect(w.find('[data-testid="surface-pdf-rule-text-1"]').classes()).not.toContain('is-bad');
  });

  it('the author picks the rule and the face, and both go into fields[]', async () => {
    const w = draw({ mode: 'edit', fields: ruled, signers: [], signer: undefined });
    await ready(w);
    await w.find('[data-testid="surface-pdf-field-text-1"]').trigger('pointerdown');
    // A click is a press AND a release: the editor waits for the release, so a
    // box being dragged is not pushed down the page by it (item 6).
    window.dispatchEvent(new Event('pointerup'));
    await w.vm.$nextTick();
    expect(w.find('[data-testid="surface-pdf-selected"]').exists()).toBe(true);

    await w.find('[data-testid="surface-pdf-rule-email"]').trigger('click');
    await w.find('[data-testid="surface-pdf-rule-min"]').setValue('5');
    await w.find('[data-testid="surface-pdf-font-source-serif"]').trigger('click');

    const out = w.emitted('update:modelValue')?.at(-1)?.[0] as Array<Record<string, unknown>>;
    const edited = out.find((f) => f.id === 'text-1')!;
    expect(edited.rule).toEqual({ kind: 'email', min: 5 });
    expect(edited.font).toBe('source-serif');
  });

  // ⚠ The layout was the one thing about a date box nobody could answer:
  // the plugin sent the catalogue, the editor drew no control for it, and
  // the plugin's own default was the only layout anybody ever got.
  it('the author picks the date layout, and it goes into fields[]', async () => {
    const w = draw({
      mode: 'edit',
      fields: [{ id: 'date-1', type: 'date', page: 1, x: 0.1, y: 0.3, w: 0.2, h: 0.03 }],
      signers: [],
      signer: undefined,
      formats: [
        { id: 'DD.MM.YYYY', label: { en: 'DD.MM.YYYY' }, example: '31.12.2000' },
        { id: 'MM/DD/YYYY', label: { en: 'MM/DD/YYYY' }, example: '12/31/2000' },
        { id: 'YYYY-MM-DD', label: { en: 'YYYY-MM-DD' }, example: '2000-12-31' },
      ],
    });
    await ready(w);
    await w.find('[data-testid="surface-pdf-field-date-1"]').trigger('pointerdown');
    // A click is a press AND a release: the editor waits for the release, so a
    // box being dragged is not pushed down the page by it (item 6).
    window.dispatchEvent(new Event('pointerup'));
    await w.vm.$nextTick();

    // Buttons, never a dropdown — and each one shows what a date looks like
    // written that way.
    expect(w.findAll('[data-testid^="surface-pdf-format-"]')).toHaveLength(3);
    expect(w.find('[data-testid="surface-pdf-editor"]').find('select').exists()).toBe(false);
    expect(w.find('[data-testid="surface-pdf-format-MM/DD/YYYY"]').text()).toContain('12/31/2000');

    await w.find('[data-testid="surface-pdf-format-MM/DD/YYYY"]').trigger('click');
    const out = w.emitted('update:modelValue')?.at(-1)?.[0] as Array<Record<string, unknown>>;
    expect(out.find((f) => f.id === 'date-1')!.format).toBe('MM/DD/YYYY');
  });

  it('no catalogue, no layout control — there is nothing to choose between', async () => {
    const w = draw({
      mode: 'edit',
      fields: [{ id: 'date-1', type: 'date', page: 1, x: 0.1, y: 0.3, w: 0.2, h: 0.03 }],
      signers: [],
      signer: undefined,
    });
    await ready(w);
    await w.find('[data-testid="surface-pdf-field-date-1"]').trigger('pointerdown');
    // A click is a press AND a release: the editor waits for the release, so a
    // box being dragged is not pushed down the page by it (item 6).
    window.dispatchEvent(new Event('pointerup'));
    await w.vm.$nextTick();
    expect(w.find('[data-testid^="surface-pdf-format-"]').exists()).toBe(false);
    // …and a date box is never asked what text it accepts.
    expect(w.find('[data-testid="surface-pdf-rule-any"]').exists()).toBe(false);
  });

  it('a checkbox is offered no face — there is nothing to draw in one', async () => {
    const w = draw({
      mode: 'edit',
      fields: [{ id: 'chk-1', type: 'checkbox', page: 1, x: 0.1, y: 0.2, w: 0.03, h: 0.02 }],
      signers: [],
      signer: undefined,
    });
    await ready(w);
    await w.find('[data-testid="surface-pdf-field-chk-1"]').trigger('pointerdown');
    // A click is a press AND a release: the editor waits for the release, so a
    // box being dragged is not pushed down the page by it (item 6).
    window.dispatchEvent(new Event('pointerup'));
    await w.vm.$nextTick();
    expect(w.find('[data-testid="surface-pdf-fonts"]').exists()).toBe(false);
    // …and no rule either: a boolean has nothing to accept.
    expect(w.find('[data-testid="surface-pdf-rule"]').exists()).toBe(false);
  });
});
