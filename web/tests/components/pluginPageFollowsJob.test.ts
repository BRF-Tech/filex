// PluginPageView follows the job it queued (filex #78).
//
// The complaint: "İmza sihirbazı çalışmıyor gibi gözüküyor." A DOCX sent for
// signature opens the signing app's page, which says it has to be a PDF first
// and offers "Convert to PDF". Pressed, the conversion was queued and the page
// said "The job is queued ... you will be told when it lands" - and nothing
// ever told anybody: the explorer in the other tab announces only the jobs IT
// queued. The PDF landed in silence; the wizard never went on to it; a failed
// conversion was not said either.
//
// What has to stay true, measured on the mounted page with the ops list
// answering the way the server does:
//   · the job ended with an `open` on its output -> this tab goes there (a
//     `page` screen on its own address, anything else through the explorer);
//   · it ended with nothing to open -> the page says it is done, in the app's
//     own words;
//   · it failed -> the page says why, in the person's words;
//   · while it runs, the job card is the one it always was.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PluginPageView from '@brftech/filex-core/src/components/plugin/PluginPageView.vue';
import type { PluginSurface } from '@brftech/filex-core/src/types/Plugins';

const office: PluginSurface = {
  title: { en: 'Request signatures - contract.docx' },
  state: { view: 'request', office: true },
  nodes: [{ type: 'text', props: { text: { en: 'This has to be a PDF first' } } }],
  actions: [{ id: 'convert', label: { en: 'Convert to PDF' }, primary: true }],
};

const queued = { id: 41, kind: 'plugin-action', status: 'pending', plugin: 'sign', action: 'convert' };

/** The server, as far as this page talks to it. `ops` is the ops list's answer. */
function server(ops: () => Record<string, unknown>[]) {
  return {
    endpoints: { opsList: '/api/files/ops' },
    withScreenLang: (u: string) => u,
    jsonFetch: vi.fn(async () => ({ ops: ops() })),
    pluginView: vi.fn(async () => ({ surface: office })),
    pluginViewEvent: vi.fn(async () => ({ op: queued })),
    // `request` is a whole page: the menu row that opens it says so.
    pluginActions: vi.fn(async () => ({
      actions: [{ plugin: 'sign', id: 'request', key: 'plugin:sign/request', view: 'request', view_placement: 'page' }],
      views: [],
    })),
  };
}

function open(api: ReturnType<typeof server>) {
  return mount(PluginPageView, {
    props: {
      locale: 'en' as const,
      api: api as never,
      plugin: 'sign',
      view: 'request',
      path: 'docs://contract.docx',
      backHref: '/admin/explore',
      opsHref: '/admin/explore',
      mountBase: '/admin/',
    },
  });
}

async function convert(w: ReturnType<typeof open>) {
  await flushPromises();
  await w.find('[data-testid="plugin-page-action-convert"]').trigger('click');
  await flushPromises();
  await flushPromises();
}

let assign: ReturnType<typeof vi.fn>;
beforeEach(() => {
  assign = vi.fn();
  vi.spyOn(window.location, 'assign').mockImplementation(assign as never);
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe('PluginPageView follows the job it queued', () => {
  it('a finished job that opens a screen on its output carries the page there, in this tab', async () => {
    const w = open(
      server(() => [
        {
          ...queued,
          status: 'ok',
          message: 'contract.pdf is ready.',
          outputs: [{ path: 'docs://contract.pdf' }],
          open: { path: 'docs://contract.pdf', view: 'request' },
        },
      ]),
    );
    await convert(w);
    // The wizard goes on ON THE PDF: the page screen's own address, here.
    expect(assign).toHaveBeenCalledWith('/admin/apps/sign/request?path=docs%3A%2F%2Fcontract.pdf');
    expect(w.find('[data-testid="plugin-page-queued"]').attributes('data-job')).toBe('opening');
    expect(w.find('[data-testid="plugin-page-job-title"]').text()).toBe('Opening the result');
    w.unmount();
  });

  it('a screen that is not a page is reached through the explorer', async () => {
    const w = open(
      server(() => [
        { ...queued, status: 'ok', outputs: [{ path: 'docs://contract.pdf' }], open: { path: 'docs://contract.pdf', view: 'status' } },
      ]),
    );
    await convert(w);
    expect(assign).toHaveBeenCalledTimes(1);
    const href = String(assign.mock.calls[0][0]);
    expect(href.startsWith('/admin/explore?')).toBe(true);
    expect(href).toContain('select=docs%3A%2F%2Fcontract.pdf');
    expect(href).toContain('app=sign');
    expect(href).toContain('appView=status');
    w.unmount();
  });

  it("a finished job with nothing to open says so, in the app's own words", async () => {
    const w = open(server(() => [{ ...queued, status: 'ok', message: 'contract.pdf is ready.' }]));
    await convert(w);
    expect(assign).not.toHaveBeenCalled();
    const card = w.find('[data-testid="plugin-page-queued"]');
    expect(card.attributes('data-job')).toBe('done');
    expect(w.find('[data-testid="plugin-page-job-title"]').text()).toBe('The job is done');
    expect(w.find('[data-testid="plugin-page-job-text"]').text()).toBe('contract.pdf is ready.');
    w.unmount();
  });

  it('a failed job says why, on the page that queued it', async () => {
    const w = open(
      server(() => [
        {
          ...queued,
          status: 'failed',
          error: 'LibreOffice could not turn “contract.docx” into a PDF. Convert it to PDF yourself and sign that.',
          error_code: 'app',
          // A failed job's row never carries an open the client would follow.
          open: { path: 'docs://contract.pdf', view: 'request' },
        },
      ]),
    );
    await convert(w);
    expect(assign).not.toHaveBeenCalled();
    expect(w.find('[data-testid="plugin-page-queued"]').attributes('data-job')).toBe('failed');
    expect(w.find('[data-testid="plugin-page-job-title"]').text()).toBe('The job did not finish');
    const said = w.find('[data-testid="plugin-page-job-text"]');
    expect(said.attributes('role')).toBe('alert');
    expect(said.text()).toContain('LibreOffice could not turn');
    w.unmount();
  });

  it('while the job runs, the card is the queued card it always was', async () => {
    const w = open(server(() => [{ ...queued, status: 'running' }]));
    await convert(w);
    expect(assign).not.toHaveBeenCalled();
    const card = w.find('[data-testid="plugin-page-queued"]');
    expect(card.exists()).toBe(true);
    expect(card.attributes('data-job')).toBe('running');
    expect(w.find('[data-testid="plugin-page-job-title"]').text()).toBe('The job is queued');
    expect(w.find('[data-testid="plugin-page-ops"]').attributes('href')).toBe('/admin/explore');
    w.unmount();
  });

  it("another person's job in the same list moves nothing", async () => {
    // An administrator's list carries everybody's rows; only the job THIS
    // page queued is followed.
    const w = open(
      server(() => [
        { ...queued, status: 'running' },
        { id: 77, kind: 'plugin-action', status: 'ok', plugin: 'sign', open: { path: 'docs://theirs.pdf', view: 'request' } },
      ]),
    );
    await convert(w);
    expect(assign).not.toHaveBeenCalled();
    w.unmount();
  });
});
