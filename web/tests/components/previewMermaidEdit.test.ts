// #216: Edit on a Mermaid diagram (.mmd / .mermaid).
//
// The server saves a Mermaid diagram as text (internal/editkind lists `mmd`
// and `mermaid`, published as `capabilities.edit_kinds`), and the preview
// offered Edit on it. But Edit opened the Mermaid VIEWER again, which only
// draws the diagram and has no save: `viewerProps` handed `saveUrl` to the
// draw.io viewer alone. The person pressed Edit, changed nothing they could
// keep, and closed it.
//
// Now Edit opens the diagram's text in the built-in text editor, and Save
// sends it through save-text - the same way as any other text file. Which
// files that is stays the server's answer (isTextEditable); this client only
// knows that its Mermaid viewer draws and does not edit.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import { en } from '@brftech/filex-core/src/locales/en';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

/* A stand-in for Monaco (an optional peer the test build does not carry): it
 * keeps the text it was given and hands back whatever the test "typed". */
const monaco = vi.hoisted(() => {
  const state = { value: '', created: [] as Array<Record<string, unknown>> };
  const editor = {
    getValue: () => state.value,
    setValue: (v: string) => {
      state.value = v;
    },
    addCommand: () => undefined,
    onDidChangeModelContent: () => ({ dispose: () => undefined }),
    dispose: () => undefined,
  };
  const ns = {
    editor: {
      create: (_el: unknown, opts: Record<string, unknown>) => {
        state.created.push(opts);
        state.value = String(opts.value ?? '');
        return editor;
      },
    },
    KeyMod: { CtrlCmd: 2048 },
    KeyCode: { KeyS: 49 },
  };
  return { state, ns };
});

vi.mock('@brftech/filex-core/src/composables/useMonacoLoader', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  ensureMonaco: async () => monaco.ns,
  getMonaco: () => monaco.ns,
}));

const SOURCE = 'graph TD\n  A-->B\n';

const diagram = (basename: string, extension: string): FileNode =>
  ({
    path: `depo://diagrams/${basename}`,
    basename,
    type: 'file',
    extension,
    mime_type: 'text/plain',
    size: SOURCE.length,
    last_modified: 1_757_376_000,
  }) as FileNode;

interface Call {
  url: string;
  method: string;
  body: unknown;
}
let calls: Call[] = [];

beforeEach(() => {
  calls = [];
  monaco.state.value = '';
  monaco.state.created = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = (init?.method ?? 'GET').toUpperCase();
      let body: unknown;
      try {
        body = init?.body ? JSON.parse(String(init.body)) : undefined;
      } catch {
        body = init?.body;
      }
      calls.push({ url, method, body });
      if (url === '/api/files/save-text') return new Response('{"ok":true}', { status: 200 });
      return new Response(SOURCE, { status: 200 });
    }),
  );
});

const opened: VueWrapper[] = [];
afterEach(() => {
  opened.splice(0).forEach((w) => w.unmount());
  vi.unstubAllGlobals();
});

const settle = async () => {
  for (let i = 0; i < 6; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

function preview(file: FileNode, openMode: 'view' | 'edit') {
  const w = mount(PreviewModal, {
    attachTo: document.body,
    props: {
      open: true,
      locale: 'en',
      openMode,
      newTabEnabled: true,
      file,
      previewUrl: (p: string) => `/preview?path=${encodeURIComponent(p)}`,
      downloadUrl: (p: string) => `/download?path=${encodeURIComponent(p)}`,
      saveTextEndpoint: '/api/files/save-text',
    },
  });
  opened.push(w);
  return w;
}

describe('a Mermaid diagram is edited as its text (#216)', () => {
  it.each([
    ['flow.mmd', 'mmd'],
    ['flow.mermaid', 'mermaid'],
  ])('%s: Edit opens the text editor on the source, and Save writes it through save-text', async (name, ext) => {
    const w = preview(diagram(name, ext), 'edit');
    await settle();

    // The text editor, not the viewer that only draws.
    expect(w.find('.fe-preview__viewer').exists(), 'the Mermaid viewer has no save').toBe(false);
    expect(w.find('.fe-preview__code-wrap').exists()).toBe(true);
    expect(monaco.state.created).toHaveLength(1);
    expect(monaco.state.created[0]).toMatchObject({ value: SOURCE, readOnly: false });
    // The badge names a diagram's source, not "plaintext" (0.56 shots).
    expect(w.find('[data-testid="code-lang"]').text()).toBe('mermaid');

    // What the person typed is what is saved, to the file in view.
    monaco.state.value = 'graph TD\n  A-->C\n';
    const save = w.findAll('button').find((b) => b.text() === en['viewer.save']);
    expect(save, 'a Save button').toBeTruthy();
    await save!.trigger('click');
    await settle();
    const saved = calls.filter((c) => c.url === '/api/files/save-text' && c.method === 'POST');
    expect(saved).toHaveLength(1);
    expect(saved[0].body).toEqual({ path: `depo://diagrams/${name}`, content: 'graph TD\n  A-->C\n' });
  });

  it('looking at it still draws the diagram, with Edit offered', async () => {
    const w = preview(diagram('flow.mmd', 'mmd'), 'view');
    await settle();
    expect(w.find('.fe-preview__code-wrap').exists()).toBe(false);
    const edit = w.findAll('button.fe-viewer__act').filter((b) => b.attributes('title') === en['viewer.edit']);
    expect(edit).toHaveLength(1);
  });
});
