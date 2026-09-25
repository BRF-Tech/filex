// #57 — an app's screen wears the theme. The one pairing a static guard
// (tests/quality/surfaceTokens.test.ts) cannot see: a box card's number sits
// on the SIGNER's colour, which is an identity colour and the same in every
// theme, so the ink on it has to come from that colour — not from
// `--fe-text-on-primary`, which is dark in dark mode and measured 3.5:1 on
// the first signer's blue (operator theme, dark, 2026-09-25).
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';

vi.mock('@brftech/filex-core/src/lib/pdfjsLoader', () => ({
  loadPdfjs: async () => null,
  defaultPdfWorkerUrl: () => '',
  resetPdfjsLoader: () => {},
}));

import SurfacePdfFields from '@brftech/filex-core/src/components/plugin/nodes/SurfacePdfFields.vue';
import { SIGNER_PALETTE } from '@brftech/filex-core/src/lib/pdfFields';
import { inkOn } from '@brftech/filex-core/src/composables/usePublicBranding';
import { mockCanvas2d } from '../fixtures/canvas2d';

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, arrayBuffer: async () => new ArrayBuffer(8), text: async () => '' })));
  mockCanvas2d();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const fields = [
  { id: 'a1', type: 'signature', page: 1, x: 0.1, y: 0.1, w: 0.3, h: 0.06, assignee: 'a' },
  { id: 'b1', type: 'signature', page: 1, x: 0.1, y: 0.2, w: 0.3, h: 0.06, assignee: 'b' },
  { id: 'c1', type: 'date', page: 1, x: 0.1, y: 0.3, w: 0.2, h: 0.03, assignee: 'c' },
  { id: 'd1', type: 'text', page: 1, x: 0.1, y: 0.4, w: 0.2, h: 0.03, assignee: 'd' },
];
const signers = [
  { id: 'a', label: 'Ann' },
  { id: 'b', label: 'Bob' },
  // A plugin may name its own colours: a pale one wants dark ink…
  { id: 'c', label: 'Cem', color: '#fde68a' },
  // …and one that is not a hex colour gets no ink of its own (the token stands).
  { id: 'd', label: 'Dee', color: 'teal' },
];

function cardStyle(w: ReturnType<typeof mount>, id: string): string {
  return w.find(`[data-testid="surface-pdf-card-${id}"]`).attributes('style') ?? '';
}

describe('SurfacePdfFields — a card’s number reads on its signer’s colour', () => {
  it('the ink is derived from the colour, whatever the theme', async () => {
    const w = mount(SurfacePdfFields, {
      props: { id: 'doc', src: { url: '/nda.pdf' }, mode: 'define', fields, signers, locale: 'en', theme: 'dark' },
    });
    await vi.waitFor(() => expect(w.find('[data-testid="surface-pdf-card-a1"]').exists()).toBe(true));

    expect(cardStyle(w, 'a1')).toContain(`--spdf-color: ${SIGNER_PALETTE[0]}`);
    expect(cardStyle(w, 'a1')).toContain(`--spdf-ink: ${inkOn(SIGNER_PALETTE[0])}`);
    expect(cardStyle(w, 'b1')).toContain(`--spdf-ink: ${inkOn(SIGNER_PALETTE[1])}`);
    // White on the palette's blue; dark on a pale yellow — the colour decides.
    expect(inkOn(SIGNER_PALETTE[0])).toBe('#ffffff');
    expect(cardStyle(w, 'c1')).toContain(`--spdf-ink: ${inkOn('#fde68a')}`);
    expect(inkOn('#fde68a')).not.toBe('#ffffff');
    expect(cardStyle(w, 'd1')).toContain('--spdf-color: teal');
    expect(cardStyle(w, 'd1')).not.toContain('--spdf-ink');
  });
});
