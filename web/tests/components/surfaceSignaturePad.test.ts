// SurfaceSignaturePad — a drawn stroke, a typed name or an uploaded picture
// all end as `{png_b64, mode}` under the node's id, and Clear ends as null.
// The canvas is mocked: happy-dom has no 2D context, and what is pinned
// here is the value on the wire, not the pixels.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import SurfaceSignaturePad from '@brftech/filex-core/src/components/plugin/nodes/SurfaceSignaturePad.vue';
import { fitFontSize, fitWithin, padSize, signatureModes, stripDataUrl, uploadVerdict } from '@brftech/filex-core/src/lib/signaturePad';
import { mockCanvas2d } from '../fixtures/canvas2d';

let ctx: ReturnType<typeof mockCanvas2d>;

beforeEach(() => {
  ctx = mockCanvas2d();
});
afterEach(() => vi.restoreAllMocks());

function draw(extra: Record<string, unknown> = {}) {
  return mount(SurfaceSignaturePad, { props: { id: 'sig', locale: 'en', modelValue: null, ...extra } });
}

describe('SurfaceSignaturePad', () => {
  it('a stroke emits a png_b64 value in draw mode', async () => {
    const w = draw();
    const c = w.find('[data-testid="surface-signature-canvas"]');
    expect(c.exists()).toBe(true);
    await c.trigger('pointerdown', { clientX: 10, clientY: 10, pointerId: 1 });
    await c.trigger('pointermove', { clientX: 40, clientY: 20, pointerId: 1 });
    await c.trigger('pointerup', { pointerId: 1 });
    expect(ctx.stroke).toHaveBeenCalled();
    expect(w.emitted('update:modelValue')).toEqual([[{ png_b64: 'QUJDRA==', mode: 'draw' }]]);
  });

  it('the pad is DPR-aware: the backing store is the pad size times the ratio', () => {
    Object.defineProperty(window, 'devicePixelRatio', { value: 2, configurable: true });
    const w = draw({ width: 300, height: 100 });
    const c = w.find('[data-testid="surface-signature-canvas"]').element as HTMLCanvasElement;
    expect(c.width).toBe(600);
    expect(c.height).toBe(200);
    expect(ctx.scale).toHaveBeenCalledWith(2, 2);
    Object.defineProperty(window, 'devicePixelRatio', { value: 1, configurable: true });
  });

  it('typing a name renders it and emits mode "type"; emptying it emits null', async () => {
    const w = draw();
    await w.find('[data-testid="surface-signature-mode-type"]').trigger('click');
    const input = w.find('[data-testid="surface-signature-typed"]');
    await input.setValue('Ayşe Yılmaz');
    await flushPromises();
    expect(ctx.fillText).toHaveBeenCalledWith('Ayşe Yılmaz', 12, expect.any(Number));
    // ⚠ The FACE travels with the value: the plugin stamps the PDF from this
    // object alone and never sees the screen, so a signature typed in Caveat
    // that arrived as a bare string came out in whatever the stamper
    // defaulted to.
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([
      { png_b64: 'QUJDRA==', mode: 'type', font: 'caveat' },
    ]);
    await input.setValue('');
    await flushPromises();
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([null]);
  });

  it('another face re-renders the same name and travels with it', async () => {
    const w = draw();
    await w.find('[data-testid="surface-signature-mode-type"]').trigger('click');
    await w.find('[data-testid="surface-signature-typed"]').setValue('Ayşe');
    await flushPromises();
    ctx.font = '';
    await w.find('[data-testid="surface-signature-font-source-serif"]').trigger('click');
    await flushPromises();
    expect(String(ctx.font)).toContain('Source Serif 4');
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([
      { png_b64: 'QUJDRA==', mode: 'type', font: 'source-serif' },
    ]);
  });

  it('a drawn signature carries NO face — it is handwriting, not text', async () => {
    const w = draw();
    const canvas = w.find('[data-testid="surface-signature-canvas"]');
    await canvas.trigger('pointerdown', { clientX: 10, clientY: 10, pointerId: 1 });
    await canvas.trigger('pointerup', { pointerId: 1 });
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([{ png_b64: 'QUJDRA==', mode: 'draw' }]);
  });

  it('only the modes the node names are offered, in the catalogue order', () => {
    const w = draw({ modes: ['upload', 'draw'] });
    const tabs = w.findAll('[role="tab"]').map((b) => b.attributes('data-testid'));
    expect(tabs).toEqual(['surface-signature-mode-draw', 'surface-signature-mode-upload']);
    const one = draw({ modes: ['type'] });
    expect(one.findAll('[role="tab"]')).toHaveLength(0);
    expect(one.find('[data-testid="surface-signature-typed"]').exists()).toBe(true);
  });

  it('Clear is offered once there is a value and emits null', async () => {
    const w = draw({ modelValue: { png_b64: 'QUJD', mode: 'draw' } });
    const clear = w.find('[data-testid="surface-signature-clear"]');
    expect((clear.element as HTMLButtonElement).disabled).toBe(false);
    await clear.trigger('click');
    expect(w.emitted('update:modelValue')).toEqual([[null]]);
    expect((draw().find('[data-testid="surface-signature-clear"]').element as HTMLButtonElement).disabled).toBe(true);
  });

  it('an upload is refused by type and by size before anything is read', async () => {
    const w = draw({ modes: ['upload'] });
    const input = w.find('[data-testid="surface-signature-file"]');
    const big = new File([new Uint8Array(201 * 1024)], 'sig.png', { type: 'image/png' });
    Object.defineProperty(input.element, 'files', { value: [big], configurable: true });
    await input.trigger('change');
    expect(w.text()).toContain('larger than 200 KB');
    const gif = new File([new Uint8Array(10)], 'sig.gif', { type: 'image/gif' });
    Object.defineProperty(input.element, 'files', { value: [gif], configurable: true });
    await input.trigger('change');
    expect(w.text()).toContain('Only PNG or JPEG');
    expect(w.emitted('update:modelValue')).toBeUndefined();
  });
});

describe('lib/signaturePad', () => {
  it('modes, sizes, verdicts and fitting are what the catalogue says', () => {
    expect(signatureModes(undefined)).toEqual(['draw', 'type', 'upload']);
    expect(signatureModes(['type', 'x'])).toEqual(['type']);
    expect(padSize(undefined, undefined)).toEqual({ width: 480, height: 160 });
    expect(padSize(5000, 5)).toEqual({ width: 600, height: 60 });
    expect(uploadVerdict({ type: 'image/jpeg', size: 200 * 1024 })).toBe('ok');
    expect(uploadVerdict({ type: 'image/jpeg', size: 200 * 1024 + 1 })).toBe('too_big');
    expect(uploadVerdict({ type: 'image/webp', size: 10 })).toBe('bad_type');
    expect(fitWithin(1200, 400, 600, 200)).toEqual({ width: 600, height: 200 });
    expect(fitWithin(300, 300, 600, 200)).toEqual({ width: 200, height: 200 });
    expect(fitWithin(100, 50, 600, 200)).toEqual({ width: 100, height: 50 });
    expect(stripDataUrl('data:image/png;base64,QUJD')).toBe('QUJD');
    expect(stripDataUrl('')).toBe('');
    expect(fitFontSize((px) => px * 10, 300, 160)).toBe(30);
  });
});
