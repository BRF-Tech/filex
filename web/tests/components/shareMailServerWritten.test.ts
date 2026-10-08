// The share mail and the share sheet are the SERVER's words (C2, 0.54).
//
// ⚠⚠ The dialog used to send the link's address, its PIN, the expiry it had
// asked for, whether the item was a folder and its size, and the server mailed
// them as given; it also wrote its own copy of the mail for the OS share
// sheet. Now the request names the LINK (its token), the addresses and - only
// when somebody picked one - the recipient's language (#191), nothing else -
// and the server writes the mail and the share
// sheet's text from the link itself. What the server says back (sent,
// refused) is shown as it said it.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import PermissionsModal from '@brftech/filex-core/src/modals/PermissionsModal.vue';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { requestFailure } from '@brftech/filex-core/src/lib/errorWords';

function fakeApi(opts: { refuse?: string } = {}) {
  const mails: Array<Record<string, unknown>> = [];
  const asked: Array<[string, string | undefined]> = [];
  let seq = 0;
  const api = {
    listPermissions: vi.fn(async () => ({ direct: [], inherited: [], storage_rbac: true })),
    listShares: vi.fn(async () => ({ shares: [] })),
    createShare: vi.fn(async (body: { kind?: string; password?: boolean }) => {
      const token = `tok${++seq}`;
      return {
        share: {
          uuid: token,
          token,
          url: `https://files.example/${body.kind === 'drop' ? 'd' : 's'}/${token}`,
          password_pin: body.password ? '48213377' : null,
          expires_at: null,
        },
      };
    }),
    revokeShare: vi.fn(async () => {}),
    shareMail: vi.fn(async (body: Record<string, unknown>) => {
      mails.push(body);
      if (opts.refuse) {
        throw requestFailure(503, JSON.stringify({ emailed: false, error: 'not_configured', message: opts.refuse }), 'en');
      }
      return { emailed: true, sent: body.emails, failed: [], pin_withheld: true, message: 'Sent, as the server says.' };
    }),
    shareMessage: vi.fn(async (share: string, locale?: string) => {
      asked.push([share, locale]);
      return { subject: `Subject of ${share}`, body: `Body of ${share}` };
    }),
    resolveEmail: vi.fn(async () => ({ found: false })),
    searchUsers: vi.fn(async () => ({ users: [] })),
  };
  return { api: api as unknown as FileApi, mails, asked };
}

function dialog(api: FileApi, props: Record<string, unknown> = {}) {
  return mount(PermissionsModal, {
    props: { path: 'depo://Projeler/rapor.pdf', isDir: false, locale: 'en', shareMaxTtlDays: 7, api, mailReady: true, ...props },
    attachTo: document.body,
  });
}

async function makeLinkWithPin(w: ReturnType<typeof dialog>) {
  await w.get('[data-testid="share-switch"]').trigger('click');
  await flushPromises();
  await w.get('[data-testid="share-options-toggle"]').trigger('click');
  await w.get('[data-testid="share-pin-switch"]').trigger('click');
  await w.get('[data-testid="share-create"]').trigger('click');
  await flushPromises();
}

// jsdom has no Web Share: the sheet's test gives navigator one for itself.
function giveShare(fn: (data: unknown) => Promise<void>) {
  Object.defineProperty(navigator, 'share', { value: fn, configurable: true, writable: true });
}
afterEach(() => {
  delete (navigator as unknown as { share?: unknown }).share;
});

describe('the share mail names the link, nothing about it', () => {
  it('a download link: {share, emails} and not one field more — no url, PIN, expiry, kind, size or language', async () => {
    const { api, mails } = fakeApi();
    const w = dialog(api, { size: 123456 });
    await flushPromises();
    await makeLinkWithPin(w);

    const box = w.get('[data-testid="share-mail-row"] input');
    await box.setValue('ayse@example.com, mehmet@example.com');
    await box.trigger('keyup', { key: 'Enter' });
    await flushPromises();

    expect(mails).toHaveLength(1);
    // ⚠ #191: no `locale` - the sender's screen language is not the
    // recipient's; with nothing picked the server writes in its own.
    expect(mails[0]).toStrictEqual({ share: 'tok2', emails: ['ayse@example.com', 'mehmet@example.com'] });
    // The server's own sentence is what the composer reads.
    expect(w.text()).toContain('Sent, as the server says.');
    w.unmount();
  });

  it('an upload link: the same two fields — the server knows it is a file request', async () => {
    const { api, mails } = fakeApi();
    const w = dialog(api, { path: 'depo://Projeler', isDir: true });
    await flushPromises();
    await w.get('[data-testid="share-drop-toggle"]').trigger('click');
    await w.get('[data-testid="drop-create"]').trigger('click');
    await flushPromises();

    const box = w.get('.fe-share__mailrow input');
    await box.setValue('musteri@example.com');
    await box.trigger('keyup', { key: 'Enter' });
    await flushPromises();

    expect(mails).toHaveLength(1);
    expect(mails[0]).toStrictEqual({ share: 'tok1', emails: ['musteri@example.com'] });
    w.unmount();
  });

  it('a refusal reads as the server wrote it', async () => {
    const { api } = fakeApi({ refuse: 'Email is not set up or not verified - share the link yourself.' });
    const w = dialog(api);
    await flushPromises();
    await w.get('[data-testid="share-switch"]').trigger('click');
    await flushPromises();
    await w.get('[data-testid="share-options-toggle"]').trigger('click');
    const box = w.get('[data-testid="share-mail-row"] input');
    await box.setValue('ayse@example.com');
    await box.trigger('keyup', { key: 'Enter' });
    await flushPromises();

    expect(w.text()).toContain('Email is not set up or not verified - share the link yourself.');
    w.unmount();
  });
});

describe('the share sheet gets the words the server wrote', () => {
  it('asks the server for the link it just made and hands the sheet exactly that', async () => {
    const share = vi.fn(async () => {});
    giveShare(share);
    const { api, asked } = fakeApi();
    const w = dialog(api);
    await flushPromises();
    await w.get('[data-testid="share-switch"]').trigger('click');
    await flushPromises();
    await w.get('[data-testid="share-options-toggle"]').trigger('click');
    await flushPromises();

    // No language picked for the recipient: the server's own (#191).
    expect(asked).toEqual([['tok1', undefined]]);
    await w.get('[data-testid="share-sheet"]').trigger('click');
    await flushPromises();
    expect(share).toHaveBeenCalledWith({ title: 'Subject of tok1', text: 'Body of tok1' });
    w.unmount();
  });
});

// #191 (the maintainers' rule, 2026-10-08): a mail to somebody with no
// account here is in the language picked for them, else the server's - the
// sender's own screen language is not a choice made for the recipient.
describe("the recipient's language is picked, never assumed", () => {
  it('a language picked in the dialog goes with the mail and asks the share sheet again in it', async () => {
    // The sheet's words are asked for only where the browser has a share
    // sheet (PermissionsModal canShare).
    giveShare(vi.fn(async () => {}));
    const { api, mails, asked } = fakeApi();
    const w = dialog(api, { locale: 'tr' });
    await flushPromises();
    await makeLinkWithPin(w);
    expect(asked.at(-1)).toEqual(['tok2', undefined]);

    const vm = w.findComponent({ name: 'ChoiceSelect' });
    expect(vm.exists()).toBe(true);
    const picker = w.findAllComponents({ name: 'ChoiceSelect' }).find((c) => c.props('testid') === 'share-mail-lang');
    expect(picker, 'the share mail row offers the recipient language').toBeTruthy();
    picker!.vm.$emit('update:modelValue', 'en');
    await flushPromises();
    expect(asked.at(-1)).toEqual(['tok2', 'en']);

    const box = w.get('[data-testid="share-mail-row"] input');
    await box.setValue('friend@example.com');
    await box.trigger('keyup', { key: 'Enter' });
    await flushPromises();
    expect(mails.at(-1)).toStrictEqual({ share: 'tok2', emails: ['friend@example.com'], locale: 'en' });
    w.unmount();
  });

  it('the source sends no screen language as the recipient\'s', () => {
    const src = readFileSync(
      path.resolve(__dirname, '../../../packages/core/src/modals/PermissionsModal.vue'),
      'utf8',
    );
    expect(src).not.toMatch(/shareMail\(\{[^}]*locale: localeCode/);
    expect(src).not.toMatch(/shareMessage\(token, localeCode/);
    expect(src).not.toMatch(/locale: resolveLocale\(props\.locale\)/);
  });
});

describe('no copy of the mail is left in the dialog', () => {
  it('the source builds no message of its own', () => {
    const src = readFileSync(
      path.resolve(__dirname, '../../../packages/core/src/modals/PermissionsModal.vue'),
      'utf8',
    );
    for (const gone of ['access.ui.mail_hello', 'access.ui.mail_pin', 'access.ui.drop_invited', 'expires_days', 'is_dir:', 'function shareBody', 'function dropBody']) {
      expect(src, gone).not.toContain(gone);
    }
  });
});
