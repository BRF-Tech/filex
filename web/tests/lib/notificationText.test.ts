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
