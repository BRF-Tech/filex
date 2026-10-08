// "Request encryption…" (wiring:e2 policy; backend internal/e2epolicy). Where
// the tenant's policy wants an administrator's approval before anything new is
// encrypted, the explorer offers a request instead of encrypting — from the
// New folder dialog, a folder's menu and a file's menu — through ONE dialog.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import E2eRequestModal from '@brftech/filex-core/src/components/E2eRequestModal.vue';
import NewFolderModal from '@brftech/filex-core/src/modals/NewFolderModal.vue';
import { requestFailure, wordsIn } from '@brftech/filex-core/src/lib/errorWords';
import { unmountAll } from '../helpers/teardown';

const en = wordsIn('en');
const tr = wordsIn('tr');

/** What the server says for a refusal's reason (srvtext server.e2e.not_allowed.*). */
const POLICY_OFF_TR = 'Bir yönetici şifrelemeyi kapattı.';

/** A stand-in for `useFileApi().e2eRequest`: records what was sent, answers as the server does. */
function sender(refuse?: string, said = POLICY_OFF_TR) {
  const sent: unknown[] = [];
  const api = {
    e2eRequest: vi.fn(async (body: unknown) => {
      sent.push(body);
      if (refuse) {
        throw requestFailure(403, JSON.stringify({ error: 'e2e_not_allowed', reason: refuse, message: said }), 'tr');
      }
      return { request: { id: 9, path: 'docs://Muhasebe', status: 'pending' }, created: true };
    }),
  };
  return { api, sent };
}

/** The same stand-in, refusing with any answer the request endpoint gives. */
function refusing(status: number, body: unknown, locale: string) {
  return {
    e2eRequest: vi.fn(async () => {
      throw requestFailure(status, JSON.stringify(body), locale);
    }),
  };
}

function dialog(props: Record<string, unknown>) {
  return mount(E2eRequestModal, {
    props: { open: true, locale: 'en', path: 'docs://Muhasebe', kind: 'folder', name: 'Muhasebe', ...props } as never,
    attachTo: document.body,
  });
}

const reasonBox = () => document.body.querySelector('textarea') as HTMLTextAreaElement;
function typeReason(value: string) {
  const box = reasonBox();
  box.value = value;
  box.dispatchEvent(new Event('input'));
}
const send = () => (document.body.querySelector('[data-testid="e2e-request-send"]') as HTMLButtonElement).click();
const alertText = () => document.body.querySelector('[data-testid="e2e-request-error"]')?.textContent?.trim() ?? '';

describe('E2eRequestModal', () => {
  it('says what it is for and names its one field, in both languages', () => {
    const { api } = sender();
    const w = dialog({ api, locale: 'tr' });
    expect(document.body.textContent).toContain('“Muhasebe” için şifreleme iste');
    expect(document.body.textContent).toContain(tr('e2e.request.lead_folder'));
    expect(reasonBox().closest('label')?.textContent?.trim()).toBe('Gerekçe');
    w.unmount();
  });

  // Operator decision 2026-10-03: the kinds are separate and an approval
  // opens only its own. The dialog says, before anybody asks, which one this
  // is: the folder where it is, a new folder in it, or one file.
  it('says what the approval will open, one sentence per kind', () => {
    const { api } = sender();
    const lead = () => document.body.querySelector('[data-testid="e2e-request-lead"]')?.textContent?.trim();
    for (const [kind, key] of [
      ['folder', 'e2e.request.lead_folder'],
      ['new_folder', 'e2e.request.lead'],
      ['file', 'e2e.request.lead_file'],
    ] as const) {
      const w = dialog({ api, kind });
      expect(lead(), kind).toBe(en(key));
      w.unmount();
    }
    // In place: this folder, and not a folder inside it. New: one new folder
    // inside it, not the folder itself.
    expect(en('e2e.request.lead_folder')).toContain('encrypt this folder where it is, and not a folder inside it');
    expect(en('e2e.request.lead')).toContain('one new encrypted folder directly inside it');
    expect(en('e2e.request.lead')).not.toContain('the folder itself');
    expect(tr('e2e.request.lead_folder')).toContain('olduğu yerde');
    expect(tr('e2e.request.lead')).toContain('yeni bir şifreli klasör');
  });

  it('a file is told it is about that file', () => {
    const { api } = sender();
    const w = dialog({ api, kind: 'file', name: 'Bordro.xlsx', path: 'docs://Muhasebe/Bordro.xlsx' });
    expect(document.body.textContent).toContain(en('e2e.request.lead_file'));
    expect(document.body.textContent).not.toContain(en('e2e.request.lead'));
    w.unmount();
  });

  // The server files a file's request under the folder its `.fxe` lands in
  // (backend e2epolicy requests.go: the name a `.fxe` is stored under cannot be
  // known when the person asks), and the approval opens ONE encryption there —
  // any one. So it is for the folder, not for the file that was clicked.
  it('a file’s approval is said to be for one new encrypted file in its folder, not for this file', () => {
    expect(en('e2e.request.lead_file')).toContain('the folder this file is in');
    expect(en('e2e.request.lead_file')).toContain('one new encrypted file');
    expect(en('e2e.request.lead_file')).not.toContain('you and this file');
    expect(tr('e2e.request.lead_file')).toContain('bulunduğu klasör');
    expect(tr('e2e.request.lead_file')).not.toContain('bu dosya içindir');
    expect(tr('e2e.request.lead_file')).not.toBe(en('e2e.request.lead_file'));
  });

  // It shows the sentence and never an administrator's second line, so it takes
  // no flag for one.
  it('takes no administrator flag', () => {
    const declared = Object.keys((E2eRequestModal as unknown as { props: Record<string, unknown> }).props);
    expect(declared).toEqual(expect.arrayContaining(['open', 'locale', 'api', 'path', 'kind', 'name']));
    expect(declared).not.toContain('callerAdmin');
  });

  it('asks for a reason before it sends anything', async () => {
    const { api } = sender();
    const w = dialog({ api });
    typeReason('   ');
    send();
    await flushPromises();
    expect(api.e2eRequest).not.toHaveBeenCalled();
    expect(alertText()).toBe(en('e2e.request.reason_required'));
    expect(w.emitted('sent')).toBeUndefined();
    w.unmount();
  });

  it('sends the path, the kind and the reason, and hands the answer to the host', async () => {
    const { api, sent } = sender();
    const w = dialog({ api });
    typeReason('  Bordro dosyaları, yalnız muhasebe görsün  ');
    send();
    await flushPromises();
    expect(sent).toEqual([{ path: 'docs://Muhasebe', kind: 'folder', reason: 'Bordro dosyaları, yalnız muhasebe görsün' }]);
    expect(w.emitted('sent')?.[0]?.[0]).toMatchObject({ created: true, request: { id: 9 } });
    w.unmount();
  });

  // ⚠ 0.54 (#209): the sentence is the SERVER's (`message`, in the reader's
  // language); the dialog no longer words a reason or a code itself.
  it('says the server’s refusal inside the dialog, in the server’s words', async () => {
    const { api } = sender('policy_off');
    const w = dialog({ api, locale: 'tr' });
    typeReason('Bordro');
    send();
    await flushPromises();
    expect(alertText()).toBe(POLICY_OFF_TR);
    expect(w.emitted('sent')).toBeUndefined();
    w.unmount();
  });

  // The listing the person asked from can be stale: what was a folder is a file
  // now, or the other way round (400 kind_mismatch); or the policy, or an
  // approval, changed what may be asked for (400 not_requestable). The dialog
  // says it in words — never as a bare "Bad request".
  it('says a request for what changed since the listing in the server’s words', async () => {
    const said = {
      en: 'This item has changed since the folder was listed. Refresh the folder and try again.',
      tr: 'Bu öğe, klasör listelendiğinden beri değişmiş. Klasörü yenileyip tekrar deneyin.',
    };
    for (const [locale, words] of [['en', en], ['tr', tr]] as const) {
      const body = { error: 'kind_mismatch', message: said[locale] };
      const w = dialog({ api: refusing(400, body, locale), locale });
      typeReason('Bordro');
      send();
      await flushPromises();
      expect(alertText()).toBe(said[locale]);
      expect(alertText()).not.toBe(words('err.status.400'));
      expect(w.emitted('sent')).toBeUndefined();
      unmountAll();
    }
  });

  it('says why nothing can be requested any more - the server’s sentence for the rule’s reason, or that it changed', async () => {
    const changed =
      'Burada yapabilecekleriniz klasör listelendiğinden beri değişmiş. Şimdi nelerin sunulduğunu görmek için klasörü yenileyin.';
    const cases: Array<Record<string, unknown>> = [
      { error: 'not_requestable', message: POLICY_OFF_TR, answer: 'denied', reason: 'policy_off' },
      { error: 'not_requestable', message: changed, answer: 'allowed' },
    ];
    for (const body of cases) {
      dialog({ api: refusing(400, body, 'tr'), locale: 'tr' });
      typeReason('Bordro');
      send();
      await flushPromises();
      expect(alertText()).toBe(body.message);
      expect(alertText()).not.toBe(tr('err.status.400'));
      unmountAll();
    }
  });
});

describe('the New folder dialog, where encrypting needs an approval', () => {
  const links = () =>
    Array.from(document.body.querySelectorAll('.fe-e2e-optlink')).map((b) => (b.textContent ?? '').trim());

  it('offers to ask instead of to create, and hands the choice to the host', async () => {
    const w = mount(NewFolderModal, {
      props: { open: true, locale: 'en', encryptedOption: false, encryptedRequest: true },
      attachTo: document.body,
    });
    expect(links()).toEqual(['Request an encrypted folder…']);
    (document.body.querySelector('[data-testid="e2e-request-option"]') as HTMLButtonElement).click();
    await w.vm.$nextTick();
    expect(w.emitted('request-encrypted')).toHaveLength(1);
    expect(w.emitted('encrypted')).toBeUndefined();
    w.unmount();
  });

  it('offers neither where encrypting here is denied', () => {
    const w = mount(NewFolderModal, {
      props: { open: true, locale: 'tr', encryptedOption: false },
      attachTo: document.body,
    });
    expect(links()).toEqual([]);
    w.unmount();
  });

  it('keeps today’s link where encrypting is allowed — the two are one line, never both', () => {
    const w = mount(NewFolderModal, {
      props: { open: true, locale: 'tr', encryptedOption: true, encryptedRequest: true },
      attachTo: document.body,
    });
    expect(links()).toEqual(['Şifreli klasör oluştur…']);
    w.unmount();
  });
});
