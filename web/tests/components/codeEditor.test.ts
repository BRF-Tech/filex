// CodeEditor (core, #322): filex's code box - the file preview's Monaco with
// a line's error marked, a textarea when Monaco is not in the build. Monaco
// itself does not run in jsdom, so a stand-in records what the component asks
// of it; that is the contract (the options are the preview's, the marker is
// on the line the host names, an edit reaches the host).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

const fake = vi.hoisted(() => {
  const state = {
    monaco: null as null | Record<string, unknown>,
    created: [] as Array<Record<string, unknown>>,
    markers: [] as unknown[][],
    decorations: [] as unknown[][],
    revealed: [] as number[],
    listener: null as null | (() => void),
    value: '',
  };
  function make() {
    const model = {
      getLineCount: () => state.value.split('\n').length,
      getLineMaxColumn: (n: number) => (state.value.split('\n')[n - 1] ?? '').length + 1,
      getFullModelRange: () => ({ all: true }),
    };
    const editor = {
      getValue: () => state.value,
      getModel: () => model,
      onDidChangeModelContent: (fn: () => void) => {
        state.listener = fn;
      },
      deltaDecorations: (_old: string[], d: unknown[]) => {
        state.decorations.push(d);
        return d.map((_, i) => `d${i}`);
      },
      revealLineInCenterIfOutsideViewport: (n: number) => state.revealed.push(n),
      pushUndoStop: () => {},
      executeEdits: (_s: string, edits: Array<{ text: string }>) => {
        state.value = edits[0].text;
      },
      updateOptions: () => {},
      dispose: () => {},
      getSelection: () => ({}),
      focus: () => {},
    };
    state.monaco = {
      editor: {
        create: (_el: unknown, opts: Record<string, unknown>) => {
          state.created.push(opts);
          state.value = String(opts.value ?? '');
          return editor;
        },
        setModelMarkers: (_m: unknown, _o: string, list: unknown[]) => state.markers.push(list),
        setModelLanguage: () => {},
        setTheme: () => {},
      },
      MarkerSeverity: { Error: 8 },
      Range: class {
        constructor(public a: number, public b: number, public c: number, public d: number) {}
      },
    };
    return state.monaco;
  }
  return { state, make };
});

vi.mock('@brftech/filex-core/src/composables/useMonacoLoader', async (orig) => {
  const real = await orig<typeof import('@brftech/filex-core/src/composables/useMonacoLoader')>();
  return { ...real, ensureMonaco: async () => fake.state.monaco };
});

import CodeEditor from '@brftech/filex-core/src/components/CodeEditor.vue';

afterEach(() => {
  fake.state.monaco = null;
  fake.state.created = [];
  fake.state.markers = [];
  fake.state.decorations = [];
  fake.state.revealed = [];
});

describe('CodeEditor', () => {
  it('is the preview’s Monaco: same options, the language and theme asked for', async () => {
    fake.make();
    const w = mount(CodeEditor, { props: { modelValue: '<p>{{ x }}</p>', language: 'liquid', theme: 'dark', testid: 'src' } });
    await flushPromises();
    expect(w.find('[data-testid="src"]').attributes('data-editor')).toBe('monaco');
    const o = fake.state.created[0];
    expect(o).toMatchObject({ value: '<p>{{ x }}</p>', language: 'liquid', theme: 'vs-dark', automaticLayout: true, fontSize: 13, glyphMargin: true });
    expect(o.minimap).toEqual({ enabled: false });
  });

  it('marks the line the host names, and clears the mark when it is gone', async () => {
    fake.make();
    const w = mount(CodeEditor, { props: { modelValue: 'a\nb\n{{ oops }}\nd', errorLine: 3, errorMessage: 'Liquid error (line 3)' } });
    await flushPromises();
    const last = fake.state.markers.at(-1) as Array<Record<string, unknown>>;
    expect(last).toHaveLength(1);
    expect(last[0]).toMatchObject({ startLineNumber: 3, endLineNumber: 3, severity: 8, message: 'Liquid error (line 3)', endColumn: 11 });
    expect(fake.state.revealed).toContain(3);
    expect((fake.state.decorations.at(-1) as unknown[]).length).toBe(1);

    await w.setProps({ errorLine: null, errorMessage: '' });
    expect(fake.state.markers.at(-1)).toEqual([]);
    expect(fake.state.decorations.at(-1)).toEqual([]);
  });

  it('a line past the end marks nothing', async () => {
    fake.make();
    mount(CodeEditor, { props: { modelValue: 'one line', errorLine: 9 } });
    await flushPromises();
    expect(fake.state.markers.at(-1)).toEqual([]);
  });

  it('an edit in the editor reaches the host', async () => {
    fake.make();
    const w = mount(CodeEditor, { props: { modelValue: 'old' } });
    await flushPromises();
    fake.state.value = 'new';
    fake.state.listener?.();
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['new']);
  });

  it('without Monaco in the build: a textarea with the same value, test id and events', async () => {
    const w = mount(CodeEditor, { props: { modelValue: 'plain', testid: 'src' } });
    await flushPromises();
    const root = w.find('[data-testid="src"]');
    expect(root.attributes('data-editor')).toBe('textarea');
    const ta = root.find('textarea');
    expect((ta.element as HTMLTextAreaElement).value).toBe('plain');
    await ta.setValue('typed');
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['typed']);
  });
});
