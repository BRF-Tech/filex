// The dialog's own close button speaks the language on screen.
//
// Every core dialog is drawn by modals/Modal.vue, whose × carries its
// accessible name (`modal.close`). Modal read the language from its OWN
// `locale` prop and fell back to English, and not one of the twenty core
// dialogs passed it — so in a Turkish explorer (an embed's popup included)
// a screen reader said "Close" over a Turkish app screen (2026-09-26, the
// signing app's popup inside an embedded explorer). A dialog now inherits the
// language of the explorer it sits in; an explicit `locale` still wins.
import { describe, expect, it } from 'vitest';
import { defineComponent, h, provide } from 'vue';
import { mount } from '@vue/test-utils';

import Modal from '@brftech/filex-core/src/modals/Modal.vue';
import PluginViewModal from '@brftech/filex-core/src/components/plugin/PluginViewModal.vue';
import { EXPLORER_LOCALE } from '@brftech/filex-core/src/composables/useLocale';

function closeLabel(): string | null {
  const btn = document.body.querySelector('.fe-modal__close');
  return btn ? btn.getAttribute('aria-label') : null;
}

describe('a dialog’s close button', () => {
  it('is Turkish in a Turkish app popup', () => {
    const w = mount(PluginViewModal, {
      attachTo: document.body,
      props: {
        open: true,
        locale: 'tr',
        api: { pluginViewEvent: async () => ({}) } as never,
        plugin: 'sign',
        view: 'request',
        surface: { title: { en: 'Request signatures', tr: 'İmza iste' }, nodes: [] },
        path: 'docs://teklif.docx',
      },
    });
    expect(closeLabel()).toBe('Kapat');
    w.unmount();
  });

  it('inherits the explorer’s language when the dialog names none', () => {
    const Host = defineComponent({
      setup() {
        provide(EXPLORER_LOCALE, () => 'tr');
        return () => h(Modal, { open: true, title: 'Yeniden adlandır' }, () => 'x');
      },
    });
    const w = mount(Host, { attachTo: document.body });
    expect(closeLabel()).toBe('Kapat');
    w.unmount();
  });

  it('keeps an explicit language over the explorer’s', () => {
    const Host = defineComponent({
      setup() {
        provide(EXPLORER_LOCALE, () => 'tr');
        return () => h(Modal, { open: true, title: 'Rename', locale: 'en' }, () => 'x');
      },
    });
    const w = mount(Host, { attachTo: document.body });
    expect(closeLabel()).toBe('Close');
    w.unmount();
  });
});
