// The explorer's one dialog (packages/core/src/modals/Modal.vue) and the
// frame every app plugin's screen opens in (PluginViewModal), measured on
// the three things a person does to a dialog: press Escape, click outside
// it, and expect the cursor to be in it.
//
// ⚠ Every case MOUNTS the dialog open. That is how the explorer creates a
// plugin's screen (`v-if` + `:open="true"`), and it is exactly the case
// that was broken: the keys and the focus were wired in a non-immediate
// `watch` on `open`, so a dialog born open never heard Escape and never took
// the focus, and an absent `closeOnBackdrop` was cast to `false`, so no
// dialog closed on an outside click (release-candidate sweep, 2026-09-21:
// the converter stayed up on Escape while the share dialog closed).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import Modal from '@brftech/filex-core/src/modals/Modal.vue';
import PluginViewModal from '@brftech/filex-core/src/components/plugin/PluginViewModal.vue';
import type { PluginSurface } from '@brftech/filex-core/src/types/Plugins';

const mounted: VueWrapper[] = [];

function dialog(props: Record<string, unknown> = {}, body = '<input data-testid="first" />') {
  const w = mount(Modal, {
    props: { open: true, title: 'Rename', ...props },
    slots: { default: body, actions: '<button data-testid="ok">OK</button>' },
    attachTo: document.body,
  });
  mounted.push(w);
  return w;
}

function escape() {
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
}

/** A real click: the pointer goes down and comes up on `down`/`up`. */
function click(down: Element, up: Element = down) {
  // jsdom has no PointerEvent everywhere; the listener reads only the target.
  down.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
  up.dispatchEvent(new MouseEvent('click', { bubbles: true }));
}

describe('core Modal — a dialog mounted open', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    while (mounted.length) mounted.pop()!.unmount();
    vi.useRealTimers();
  });

  it('closes on Escape', () => {
    const w = dialog();
    escape();
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('closes on a click outside the card, not on one inside it', () => {
    const w = dialog();
    const backdrop = w.find('.fe-modal__backdrop').element;
    const card = w.find('.fe-modal__card').element;
    click(card);
    expect(w.emitted('close')).toBeUndefined();
    click(backdrop);
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('does not close when a drag that began inside the card ends outside it', () => {
    const w = dialog();
    // Selecting text in a field and letting go over the backdrop fires
    // `click` on the backdrop; closing then would throw away the typing.
    click(w.find('[data-testid="first"]').element, w.find('.fe-modal__backdrop').element);
    expect(w.emitted('close')).toBeUndefined();
  });

  it('keeps a dialog that must be answered open on an outside click', () => {
    const w = dialog({ closeOnBackdrop: false });
    click(w.find('.fe-modal__backdrop').element);
    expect(w.emitted('close')).toBeUndefined();
  });

  it('moves the focus into the dialog — to the body, not the header ×', () => {
    const opener = document.createElement('button');
    document.body.appendChild(opener);
    opener.focus();
    const w = dialog();
    vi.advanceTimersByTime(50);
    expect(document.activeElement).toBe(w.find('[data-testid="first"]').element);
    // …and gives it back when the dialog goes away while still open
    w.unmount();
    mounted.splice(mounted.indexOf(w), 1);
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it('answers Escape with the dialog in FRONT only', () => {
    const back = dialog({ title: 'Back' });
    const front = dialog({ title: 'Front' });
    escape();
    expect(front.emitted('close')).toHaveLength(1);
    expect(back.emitted('close')).toBeUndefined();
  });

  it('never closes the standalone editor on Escape (chromeless: the tab IS the dialog)', () => {
    const w = dialog({ chromeless: true });
    escape();
    click(w.find('.fe-modal__backdrop').element);
    expect(w.emitted('close')).toBeUndefined();
  });
});

describe('PluginViewModal — the frame every app screen opens in', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    while (mounted.length) mounted.pop()!.unmount();
    vi.useRealTimers();
  });

  function frame(surface: PluginSurface) {
    const w = mount(PluginViewModal, {
      props: {
        open: true,
        locale: 'tr',
        api: { pluginViewEvent: vi.fn() } as never,
        plugin: 'convert',
        view: 'options',
        surface,
        path: 'depo://a.jpg',
      },
      attachTo: document.body,
    });
    mounted.push(w);
    return w;
  }

  const wizardStep: PluginSurface = {
    title: { en: 'Convert', tr: 'Dönüştür' },
    nodes: [
      {
        type: 'form',
        props: { fields: [{ key: 'target_image', type: 'select', label: 'Görsel', options: [{ value: 'png', label: 'PNG' }] }] },
      },
    ],
    actions: [
      { id: 'cancel', label: { en: 'Cancel', tr: 'Vazgeç' } },
      { id: 'submit', label: { en: 'Next', tr: 'İleri' }, primary: true },
    ],
  };

  it('closes on Escape and on an outside click, and takes the focus', () => {
    const w = frame(wizardStep);
    vi.advanceTimersByTime(50);
    expect(w.find('[data-testid="plugin-view"]').element.contains(document.activeElement)).toBe(true);
    escape();
    expect(w.emitted('close')).toHaveLength(1);
    click(w.find('.fe-modal__backdrop').element);
    expect(w.emitted('close')).toHaveLength(2);
  });

  it('draws no Close of its own beside the screen\'s own way out', () => {
    const w = frame(wizardStep);
    const labels = w.findAll('.fe-modal__actions button').map((b) => b.text());
    expect(labels).toEqual(['Vazgeç', 'İleri']);
    expect(w.find('[data-testid="plugin-view-close"]').exists()).toBe(false);
  });

  it('still offers Close on a screen that brought no buttons', () => {
    const w = frame({ title: { en: 'Done', tr: 'Bitti' }, nodes: [{ type: 'text', props: { text: { en: 'ok', tr: 'tamam' } } }] });
    const close = w.find('[data-testid="plugin-view-close"]');
    expect(close.exists()).toBe(true);
    expect(close.text()).toBe('Kapat');
    close.trigger('click');
    expect(w.emitted('close')).toHaveLength(1);
  });
});
