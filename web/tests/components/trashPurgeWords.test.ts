// "Delete permanently" in the explorer's Trash asks in its own words.
//
// ⚠ The Trash's menu offered "Delete permanently", and the dialog it opened
// said "N items will be moved to trash" — about items already there — and
// then nothing was deleted at all: the explorer showed a retention notice
// instead. The dialog now says what the button does.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import DeleteConfirmModal from '@brftech/filex-core/src/modals/DeleteConfirmModal.vue';
import { en } from '@brftech/filex-core/src/locales/en';

function dialog(props: Record<string, unknown>) {
  return mount(DeleteConfirmModal, { props: { open: true, locale: 'en', count: 3, ...props }, attachTo: document.body });
}

describe('the delete dialog', () => {
  it('asks to delete for good when the items are already in the trash', () => {
    const w = dialog({ permanent: true });
    const text = document.body.textContent ?? '';
    expect(text).toContain(en['modal.delete.permanent_title']);
    expect(text).toContain(en['modal.delete.permanent_message'].replace('{count}', '3'));
    expect(text).not.toContain('moved to trash');
    expect(w.findAll('button').map((b) => b.text())).toContain(en['modal.delete.permanent_confirm']);
    w.unmount();
    document.body.innerHTML = '';
  });

  it('still says "moved to trash" for an ordinary delete', () => {
    const w = dialog({});
    expect(document.body.textContent ?? '').toContain('moved to trash');
    w.unmount();
    document.body.innerHTML = '';
  });
});
