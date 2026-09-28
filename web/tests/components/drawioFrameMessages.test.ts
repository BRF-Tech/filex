// draw.io runs in another site's frame. The viewer takes its messages from
// THAT frame only, at draw.io's configured origin, and addresses its own
// messages to that origin rather than to whatever the frame happens to hold.
//
// A message event carries who sent it (`source`, `origin`); a field inside
// the payload (`source: 'convert-embed'`, `{event: 'save'}`) is only what the
// sender chose to write, so it cannot be what decides.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import DrawioViewer from '@brftech/filex-core/src/viewers/DrawioViewer.vue';

const mounted: VueWrapper[] = [];

afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount());
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

const settle = async () => {
  for (let i = 0; i < 3; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

function message(data: unknown, source: Window | null, origin: string) {
  window.dispatchEvent(new MessageEvent('message', { data, source, origin }));
}

// ── draw.io ─────────────────────────────────────────────────────────────────

const DRAWIO = 'https://draw.example';

async function openDrawio(drawioUrl = `${DRAWIO}/`) {
  const saves: Array<{ path: string; content: string }> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'POST') {
        saves.push(JSON.parse(String(init.body)));
        return new Response('{}', { status: 200 });
      }
      return new Response('<mxfile>one</mxfile>', { status: 200 });
    }),
  );
  const w = mount(DrawioViewer, {
    props: {
      url: '/api/files/manager?action=preview&path=docs%3A%2F%2Fplan.drawio',
      filePath: 'docs://plan.drawio',
      ext: 'drawio',
      drawioUrl,
      saveUrl: '/api/files/save-text',
    },
    attachTo: document.body,
  });
  mounted.push(w);
  await settle();
  const frame = w.get('iframe').element as HTMLIFrameElement;
  const posted: Array<{ msg: { action?: string; xml?: string }; target: string }> = [];
  vi.spyOn(frame.contentWindow!, 'postMessage').mockImplementation(((msg: string, target: string) => {
    posted.push({ msg: JSON.parse(msg), target });
  }) as never);
  return { w, frame: frame.contentWindow!, posted, saves };
}

describe('draw.io', () => {
  it('answers its own frame, and addresses draw.io’s origin — not "*"', async () => {
    const { frame, posted } = await openDrawio();
    message(JSON.stringify({ event: 'init' }), frame, DRAWIO);
    expect(posted).toHaveLength(1);
    expect(posted[0].msg.action).toBe('load');
    expect(posted[0].msg.xml).toBe('<mxfile>one</mxfile>');
    expect(posted[0].target).toBe(DRAWIO);
  });

  it('saves what its own frame sends', async () => {
    const { frame, saves } = await openDrawio();
    message(JSON.stringify({ event: 'save', xml: '<mxfile>two</mxfile>' }), frame, DRAWIO);
    await settle();
    expect(saves).toEqual([{ path: 'docs://plan.drawio', content: '<mxfile>two</mxfile>' }]);
  });

  it('ignores the same messages from any other window', async () => {
    const { posted, saves } = await openDrawio();
    // The page itself, or anything else that can reach this window.
    message(JSON.stringify({ event: 'init' }), window, DRAWIO);
    message(JSON.stringify({ event: 'save', xml: '<mxfile>not this</mxfile>' }), window, window.location.origin);
    message(JSON.stringify({ event: 'autosave', xml: '<mxfile>nor this</mxfile>' }), null, DRAWIO);
    await settle();
    expect(posted).toEqual([]);
    expect(saves).toEqual([]);
  });

  it('ignores its own frame speaking from another origin (it navigated away)', async () => {
    const { frame, posted, saves } = await openDrawio();
    message(JSON.stringify({ event: 'init' }), frame, 'https://elsewhere.example');
    message(JSON.stringify({ event: 'save', xml: '<mxfile>x</mxfile>' }), frame, 'https://elsewhere.example');
    await settle();
    expect(posted).toEqual([]);
    expect(saves).toEqual([]);
  });

  it('a draw.io served under this site’s own path is this site’s origin', async () => {
    const { frame, posted } = await openDrawio('/drawio');
    message(JSON.stringify({ event: 'init' }), frame, window.location.origin);
    expect(posted).toHaveLength(1);
    expect(posted[0].target).toBe(window.location.origin);
  });
});
