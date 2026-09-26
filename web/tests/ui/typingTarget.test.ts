// Typing in the code editor is typing, not a shortcut.
//
// Measured in Chromium while building #56: in the viewer's text editor,
// "MIT License" arrived as "MLcene". Monaco 0.55 types through the browser's
// EditContext API there, so the focused element is not a <textarea> but a
// `<div class="native-edit-context" role="textbox">`. The explorer's shortcut
// guard only knew INPUT / TEXTAREA / SELECT / contenteditable, so I toggled
// the info panel, S starred the file and Space opened quick look — each
// keystroke taken from the document being written. QuickLook's own guard was
// a second copy of the same check with the same hole.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h, ref } from 'vue';
import { mount } from '@vue/test-utils';
import { useKeyboardShortcuts } from '@brftech/filex-core/src/composables/useKeyboardShortcuts';
import { isTypingTarget } from '@brftech/filex-core/src/lib/typingTarget';
import QuickLook from '@brftech/filex-core/src/components/QuickLook.vue';

afterEach(() => {
  document.body.innerHTML = '';
});

/** Monaco's focused element in Chromium (EditContext), inside its editor. */
function editContextTarget(): HTMLElement {
  const editor = document.createElement('div');
  editor.className = 'monaco-editor';
  const ctx = document.createElement('div');
  ctx.className = 'native-edit-context';
  ctx.setAttribute('role', 'textbox');
  ctx.tabIndex = 0;
  editor.appendChild(ctx);
  document.body.appendChild(editor);
  return ctx;
}

function press(target: HTMLElement, key: string) {
  target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
}

function harness() {
  const handlers = {
    onToggleInspector: vi.fn(),
    onStar: vi.fn(),
    onQuickLook: vi.fn(),
    onGoUp: vi.fn(),
  };
  const Host = defineComponent({
    setup() {
      const root = ref<HTMLElement | null>(null);
      useKeyboardShortcuts(root, handlers);
      return () => h('div', { ref: root });
    },
  });
  const w = mount(Host, { attachTo: document.body });
  return { w, handlers };
}

describe('the explorer’s shortcuts', () => {
  it('fire on the page (the harness measures something)', () => {
    const { w, handlers } = harness();
    const plain = document.createElement('div');
    document.body.appendChild(plain);
    press(plain, 'i');
    press(plain, 's');
    press(plain, 'Backspace');
    expect(handlers.onToggleInspector).toHaveBeenCalledTimes(1);
    expect(handlers.onStar).toHaveBeenCalledTimes(1);
    expect(handlers.onGoUp).toHaveBeenCalledTimes(1);
    w.unmount();
  });

  it('stay quiet while the code editor has the keyboard', () => {
    const { w, handlers } = harness();
    const ctx = editContextTarget();
    for (const key of ['I', 'i', 's', 'S', ' ', 'Backspace']) press(ctx, key);
    expect(handlers.onToggleInspector).not.toHaveBeenCalled();
    expect(handlers.onGoUp, 'Backspace deletes a character, it does not leave the folder').not.toHaveBeenCalled();
    expect(handlers.onStar).not.toHaveBeenCalled();
    expect(handlers.onQuickLook).not.toHaveBeenCalled();
    w.unmount();
  });
});

describe('isTypingTarget', () => {
  it('knows every place a person types', () => {
    expect(isTypingTarget(document.createElement('input'))).toBe(true);
    expect(isTypingTarget(document.createElement('textarea'))).toBe(true);
    expect(isTypingTarget(editContextTarget())).toBe(true);
    const box = document.createElement('div');
    box.setAttribute('role', 'textbox');
    expect(isTypingTarget(box)).toBe(true);
    expect(isTypingTarget(document.createElement('div'))).toBe(false);
    expect(isTypingTarget(null)).toBe(false);
  });
});

describe('quick look', () => {
  it('does not take the arrow keys from the code editor', async () => {
    const w = mount(QuickLook, {
      attachTo: document.body,
      props: {
        open: true,
        locale: 'en',
        file: { basename: 'a.txt', path: 'demo://a.txt', type: 'file', size: 1 } as never,
        previewUrl: (p: string) => p,
        downloadUrl: (p: string) => p,
      },
      global: { stubs: { PreviewModal: true } },
    });
    await w.vm.$nextTick();
    press(editContextTarget(), 'ArrowDown');
    expect(w.emitted('nav')).toBeUndefined();
    const plain = document.createElement('div');
    document.body.appendChild(plain);
    press(plain, 'ArrowDown');
    expect(w.emitted('nav')).toEqual([[1]]);
    w.unmount();
  });
});
