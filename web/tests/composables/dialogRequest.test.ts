// The request a dialog sends (rename, new folder, delete, delete permanently):
// busy while it is on its way, and its refusal said where it will be read.
//
// ⚠ #59 kept one busy flag and one error per dialog, set by whichever request
// was last to answer. A dialog that closed on success stayed "busy" through
// the listing read after it, so the next Rename opened on ANOTHER file came up
// already saying "Renaming…" with its button shut; and a refusal that came
// after its dialog had closed was written into the closed dialog's error, where
// nobody would ever read it (lesson #498: it must be a toast).
import { describe, expect, it, vi } from 'vitest';
import { nextTick, ref } from 'vue';

import { useDialogRequest } from '@brftech/filex-core/src/composables/useDialogRequest';

function rig() {
  const open = ref(false);
  const said: string[] = [];
  const req = useDialogRequest(open, { say: (m) => said.push(m) });
  return { open, said, req };
}

describe('a dialog’s request', () => {
  it('is busy while on its way and takes no second order', async () => {
    const { open, req } = rig();
    open.value = true;
    await nextTick();
    const ticket = req.begin();
    expect(ticket).not.toBeNull();
    expect(req.busy.value).toBe(true);
    expect(req.begin(), 'a second press while the first runs').toBeNull();
    req.end(ticket!);
    expect(req.busy.value).toBe(false);
  });

  it('shows a refusal in the dialog that asked, while it is still open', async () => {
    const { open, said, req } = rig();
    open.value = true;
    await nextTick();
    const ticket = req.begin()!;
    req.refuse(ticket, 'You do not have permission to do this.');
    expect(req.error.value).toBe('You do not have permission to do this.');
    expect(said).toEqual([]);
  });

  it('says a refusal that comes after its dialog closed as a toast, not into the closed dialog', async () => {
    const { open, said, req } = rig();
    open.value = true;
    await nextTick();
    const ticket = req.begin()!;
    open.value = false;
    await nextTick();
    req.refuse(ticket, 'Something with that name is already there.');
    expect(said).toEqual(['Something with that name is already there.']);
    expect(req.error.value).toBeNull();
  });

  it('does not show an old request’s busy state or refusal against a dialog opened since', async () => {
    const { open, said, req } = rig();
    open.value = true;
    await nextTick();
    const first = req.begin()!;
    // The dialog closed (its answer came, the listing is being read again)…
    open.value = false;
    await nextTick();
    // …and opened again on another item before the first request settled.
    open.value = true;
    await nextTick();
    expect(req.busy.value, 'the new dialog came up busy').toBe(false);
    expect(req.error.value).toBeNull();
    req.refuse(first, 'Could not rename “a.txt”.');
    expect(req.error.value, 'the old refusal landed in the new dialog').toBeNull();
    expect(said).toEqual(['Could not rename “a.txt”.']);
    const second = req.begin()!;
    req.end(first);
    expect(req.busy.value, 'the old request freed the new one’s button').toBe(true);
    req.end(second);
    expect(req.busy.value).toBe(false);
  });

  it('reports whether its dialog is still the one on screen', async () => {
    const { open, req } = rig();
    const onScreen = vi.fn();
    open.value = true;
    await nextTick();
    const ticket = req.begin()!;
    onScreen(req.inDialog(ticket));
    open.value = false;
    await nextTick();
    onScreen(req.inDialog(ticket));
    expect(onScreen.mock.calls).toEqual([[true], [false]]);
  });
});
