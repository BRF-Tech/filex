// What a notification SAYS, in the reader's language.
//
// ⚠ The bug this module exists for: a row's title is written once, on the
// server, in whatever language that server was configured with — and for eight
// of the eleven file events it is not written at all, so `notify.Service.Send`
// substitutes the event id. A browser notification measured on 2026-09-12 came
// out as `{title: "file.uploaded", body: "/Documents/measure-me.txt"}`. The
// wire format was on somebody's screen.
//
// These tests pin the two halves of the fix: the sentence comes from the
// reader's catalogue, and no metadata shape can put a placeholder, a dangling
// dash or a raw event id back in front of a person.
import { describe, expect, it } from 'vitest';

import {
  fillTemplate,
  notificationVars,
  renderNotification,
  type NotificationLike,
} from '@/lib/notificationText';

/** A row shaped the way the server really stores a file event. */
const uploaded: NotificationLike = {
  event: 'file.uploaded',
  title: 'file.uploaded', // what Send substitutes when the emitter set none
  body: 'Belgeler/rapor.pdf',
  meta: {
    origin: 'manager',
    node: { storage_id: 3, path: 'Belgeler/rapor.pdf', name: 'rapor.pdf', size: 1234 },
    actor: { id: 7, email: 'burak@example.com' },
    target: { kind: 'file', storage: 'team', path: 'Belgeler/rapor.pdf' },
  },
  target: { kind: 'file', storage: 'team', path: 'Belgeler/rapor.pdf' },
};

describe('renderNotification', () => {
  it('says the same fact in each reader language, from one row', () => {
    expect(renderNotification(uploaded, 'en')).toEqual({
      title: 'New file: rapor.pdf',
      body: 'Belgeler/rapor.pdf',
    });
    expect(renderNotification(uploaded, 'tr')).toEqual({
      title: 'Yeni dosya: rapor.pdf',
      body: 'Belgeler/rapor.pdf',
    });
  });

  it('never repeats the server title when that title is the event id', () => {
    // The regression in one line: the old surfaces did `n.title || n.event`.
    for (const lang of ['en', 'tr'] as const) {
      expect(renderNotification(uploaded, lang).title).not.toContain('file.uploaded');
    }
  });

  it('uses a real server title for an event outside the catalogue', () => {
    // The legacy operational alarms (disk_full, update_available, …) are not
    // subscribable events and carry a genuine sentence. Dropping to the raw id
    // for those would be a regression dressed as consistency.
    expect(
      renderNotification(
        { event: 'disk_full', title: 'Disk almost full', body: '/data at 96%' },
        'tr',
      ),
    ).toEqual({ title: 'Disk almost full', body: '/data at 96%' });
  });

  it('prefers the friendly switch label over the raw id for an unphrased event', () => {
    expect(
      renderNotification({ event: 'future.thing', title: 'future.thing', body: 'a/b' }, 'en', {
        fallbackLabel: 'Something new happens',
      }).title,
    ).toBe('Something new happens');
  });

  it('falls all the way to the event id only when there is nothing else', () => {
    expect(renderNotification({ event: 'future.thing' }, 'en').title).toBe('future.thing');
  });

  // ── the shapes the emitters actually produce ────────────────────────────

  it('reads the move from meta.from/meta.to', () => {
    const moved: NotificationLike = {
      event: 'file.moved',
      title: 'file.moved',
      body: 'Yeni/rapor.pdf',
      meta: {
        origin: 'manager',
        from: 'Eski/rapor.pdf',
        to: 'Yeni/rapor.pdf',
        node: { path: 'Yeni/rapor.pdf', name: 'rapor.pdf' },
      },
    };
    expect(renderNotification(moved, 'tr')).toEqual({
      title: 'Taşındı: rapor.pdf',
      body: 'Eski/rapor.pdf → Yeni/rapor.pdf',
    });
  });

  it('describes archive completion without falling back to a generic new-file event', () => {
    expect(renderNotification({
      event: 'archive.created',
      title: 'archive.created',
      body: 'Archives/backup.7z',
      meta: { path: 'Archives/backup.7z', node: { path: 'Archives/backup.7z', name: 'backup.7z' } },
    }, 'en')).toEqual({
      title: 'Archive created: backup.7z',
      body: 'Archives/backup.7z',
    });
    expect(renderNotification({
      event: 'archive.extracted',
      title: 'archive.extracted',
      meta: { path: 'Restored', count: 1 },
    }, 'en')).toEqual({
      title: 'Extraction completed',
      body: '1 file extracted to Restored',
    });
  });

  it('names the uploader, or says "someone" when the visitor typed nothing', () => {
    const drop = (uploader: string, count: number): NotificationLike => ({
      event: 'drop.received',
      title: 'New file upload',
      body: 'ignored',
      meta: { folder: 'Gelen', count, uploader, node: { path: 'Gelen', name: 'Gelen' } },
    });
    // ⚠ drop.go sets `uploader` to "" when the form field was blank — the only
    // meta field in the catalogue that is routinely present AND empty.
    expect(renderNotification(drop('', 3), 'tr')).toEqual({
      title: '3 dosya geldi',
      body: 'Birisi → Gelen',
    });
    expect(renderNotification(drop('Ayşe', 2), 'en')).toEqual({
      title: '2 files received',
      body: 'Ayşe → Gelen',
    });
    // English inflects after a number; Turkish does not ("1 dosya", "3 dosya").
    expect(renderNotification(drop('Ayşe', 1), 'en').title).toBe('1 file received');
    expect(renderNotification(drop('Ayşe', 1), 'tr').title).toBe('1 dosya geldi');
  });

  it('drops the separator with the field it was holding', () => {
    // file.infected reads "{signature} — {path}". A scan result with no
    // signature must not render "— Belgeler/rapor.pdf".
    const infected: NotificationLike = {
      event: 'file.infected',
      title: 'Infected file detected',
      body: 'Belgeler/rapor.pdf: ',
      meta: { quarantined: false, node: { path: 'Belgeler/rapor.pdf', name: 'rapor.pdf' } },
    };
    expect(renderNotification(infected, 'en').body).toBe('Belgeler/rapor.pdf');
  });

  it('stands on its own when share.created resolved no node', () => {
    // share.go sets Node only when the row resolved, and then Body is "" too.
    const share: NotificationLike = {
      event: 'share.created',
      title: 'share.created',
      body: '',
      meta: { kind: 'download', has_pin: false },
    };
    expect(renderNotification(share, 'tr')).toEqual({
      title: 'Paylaşım bağlantısı oluşturuldu',
      body: '',
    });
  });

  it('flattens a multi-line comment into one line', () => {
    const comment: NotificationLike = {
      event: 'comment.added',
      title: 'comment.added',
      body: 'Belgeler/rapor.pdf',
      meta: {
        comment_id: 12,
        body: 'ilk satır\n\nikinci satır',
        node: { path: 'Belgeler/rapor.pdf', name: 'rapor.pdf' },
      },
    };
    // A newline in an OS toast body renders as a gap, and a bell row clips.
    expect(renderNotification(comment, 'tr').body).toBe('ilk satır ikinci satır');
  });

  it('never prints an e-mail address', () => {
    // meta.actor.email is the only identifier any actor-bearing event carries.
    // A toast is readable over a shoulder; an address is not a display name.
    for (const lang of ['en', 'tr'] as const) {
      const out = renderNotification(uploaded, lang);
      expect(`${out.title} ${out.body}`).not.toContain('burak@example.com');
    }
  });
});

describe('notificationVars', () => {
  it('recovers a name from the path when node.name is missing', () => {
    const v = notificationVars(
      { event: 'file.deleted', body: 'A/B/c.txt', meta: { node: { path: 'A/B/c.txt' } } },
      'en',
    );
    expect(v.name).toBe('c.txt');
    expect(v.path).toBe('A/B/c.txt');
  });

  it('falls back to the target, then to the body, for the path', () => {
    expect(notificationVars({ event: 'x', target: { kind: 'file', path: 'T/x' } }, 'en').path).toBe(
      'T/x',
    );
    expect(notificationVars({ event: 'x', body: 'B/y' }, 'en').path).toBe('B/y');
  });

  it('reads the target nested inside meta when the row has no top-level one', () => {
    // The in-app row hydrates `target`, but a webhook-shaped object nests it in
    // meta — and the desktop reads whatever the API gave it.
    const v = notificationVars(
      { event: 'x', meta: { target: { kind: 'dir', storage: 's', path: 'M/z' } } },
      'en',
    );
    expect(v.path).toBe('M/z');
  });

  it('survives a meta that is not an object', () => {
    for (const meta of [null, undefined, 'nope', 42, []]) {
      expect(() => notificationVars({ event: 'x', meta }, 'en')).not.toThrow();
    }
  });
});

describe('fillTemplate', () => {
  it('leaves no placeholder behind for a field that is not there', () => {
    expect(fillTemplate('{a} — {b}', { a: 'one' })).toBe('one');
    expect(fillTemplate('{a} — {b}', { b: 'two' })).toBe('two');
    expect(fillTemplate('{a} → {b}', {})).toBe('');
    expect(fillTemplate('Moved: {name}', { name: '' })).toBe('Moved');
  });
});

// ⚠ An "open with filex" save, as the server now sends it (personview.go): the
// ORIGINAL document's name, no path (it lives on the person's computer, where
// no storage path names it), `meta.open_with`. Before, the same save read
// "File changed: a1b2c3d4e5f6-Bütçe Özeti.xlsx" over "/.filex-open/…" —
// measured in the bell on 2026-09-21.
describe('an open-with save', () => {
  const row: NotificationLike = {
    event: 'file.updated',
    body: '',
    meta: { node: { storage_id: 1, path: '', name: 'Bütçe Özeti.xlsx' }, open_with: true, origin: 'onlyoffice' },
  };

  it('names the document and says where it is, in both languages', () => {
    expect(renderNotification(row, 'en')).toEqual({
      title: 'File changed: Bütçe Özeti.xlsx',
      body: 'Opened with the filex desktop app',
    });
    expect(renderNotification(row, 'tr')).toEqual({
      title: 'Dosya değişti: Bütçe Özeti.xlsx',
      body: 'filex masaüstü uygulamasıyla açıldı',
    });
  });

  it('never reaches for a body, whatever the row carries', () => {
    const stale = { ...row, body: '/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx' };
    for (const loc of ['en', 'tr'] as const) {
      const text = renderNotification(stale, loc);
      expect(`${text.title} ${text.body}`).not.toContain('.filex-open');
    }
  });

  it('an infected working copy warns about the document itself', () => {
    const infected: NotificationLike = {
      event: 'file.infected',
      meta: { node: { path: '', name: 'Plan.docx' }, open_with: true, signature: 'Eicar-Test-Signature' },
    };
    expect(renderNotification(infected, 'tr')).toEqual({
      title: 'Plan.docx dosyasında virüs bulundu',
      body: 'Eicar-Test-Signature — filex masaüstü uygulamasıyla açıldı',
    });
  });
});

// An app's notice names the app the way the reader knows it. Measured in the
// release-candidate sweep (2026-09-21): "sign: “sözleşme.pdf” imzanızı
// bekliyor…" — `sign` is the install id; the side panel calls it "İmzalar".
describe('an app notice', () => {
  const base = {
    event: 'plugin.notice',
    title: 'Signature requested',
    meta: {
      plugin: 'sign',
      title_en: 'Signature requested',
      title_tr: 'İmza istendi',
      body_en: '“contract.pdf” is waiting for your signature',
      body_tr: '“sözleşme.pdf” imzanızı bekliyor',
    },
  };

  it("prints the app's label in the reader's language, never its install id", () => {
    const row = { ...base, meta: { ...base.meta, plugin_label_en: 'Signatures', plugin_label_tr: 'İmzalar' } };
    expect(renderNotification(row, 'tr').body).toBe('İmzalar: “sözleşme.pdf” imzanızı bekliyor');
    expect(renderNotification(row, 'en').body).toBe('Signatures: “contract.pdf” is waiting for your signature');
  });

  it('leaves the prefix out on a row recorded before labels existed', () => {
    const text = renderNotification(base, 'tr');
    expect(text.body).toBe('“sözleşme.pdf” imzanızı bekliyor');
    expect(text.body).not.toMatch(/^sign\b/);
  });
});

// 2026-09-22: a German account's bell said "e-Signature: “dummy.pdf” is
// waiting for your signature…" although the app had written the notice in
// German too. `locale` is only the built-in table ('en' for German); the
// app's own text is looked up in the reader's language first.
describe("an app notice in the reader's own language", () => {
  const german = {
    event: 'plugin.notice',
    title: 'Signature requested',
    meta: {
      plugin: 'sign',
      plugin_label_en: 'e-Signature',
      plugin_label_tr: 'e-İmza',
      plugin_label_de: 'E-Signatur',
      title_en: 'Signature requested',
      title_tr: 'İmza istendi',
      title_de: 'Unterschrift angefordert',
      body_en: '“contract.pdf” is waiting for your signature',
      body_tr: '“sözleşme.pdf” imzanızı bekliyor',
      body_de: '„vertrag.pdf“ wartet auf Ihre Unterschrift',
    },
  };

  it('speaks it when the app wrote it', () => {
    expect(renderNotification(german, 'en', { lang: 'de' })).toEqual({
      title: 'Unterschrift angefordert',
      body: 'E-Signatur: „vertrag.pdf“ wartet auf Ihre Unterschrift',
    });
    // A regional tag finds its base language.
    expect(renderNotification(german, 'en', { lang: 'de-AT' }).title).toBe('Unterschrift angefordert');
  });

  it('falls back to English for a language the app did not write', () => {
    expect(renderNotification(german, 'en', { lang: 'fr' })).toEqual({
      title: 'Signature requested',
      body: 'e-Signature: “contract.pdf” is waiting for your signature',
    });
    // …and the built-in tables still answer for themselves.
    expect(renderNotification(german, 'tr', { lang: 'tr' }).body).toBe('e-İmza: “sözleşme.pdf” imzanızı bekliyor');
  });
});
