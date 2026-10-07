// The notification digest as the reader reads it (backend notify/digest.go):
// "{count} notifications" over one line per folder - "Rapor: 12 dosya
// eklendi; Fotoğraflar: 3 dosya çöp kutusuna taşındı, 1 yorum". The bell, the
// browser's pop-up and the desktop app all say it through this one renderer
// (packages/core lib/notificationText.ts), so they cannot say it three ways.
//
// ⚠ Red before the digest: DIGEST_PARTS does not exist, and a
// `notification.digest` row rendered its raw event id.
import { describe, expect, it } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

import { DIGEST_PARTS, renderNotification, type NotificationLike } from '@brftech/filex-core/src/lib/notificationText';

const SRV = path.resolve(__dirname, '../../../backend/internal/srvtext/locales');
const PREFIX = 'server.notify.digest.';

function serverParts(lang: 'en' | 'tr'): Record<string, string> {
  const cat = JSON.parse(fs.readFileSync(path.join(SRV, `${lang}.json`), 'utf8')) as Record<string, string>;
  return Object.fromEntries(
    Object.entries(cat)
      .filter(([k]) => k.startsWith(PREFIX))
      .map(([k, v]) => [k.slice(PREFIX.length), v]),
  );
}

function digest(meta: Record<string, unknown>): NotificationLike {
  return { event: 'notification.digest', title: 'x new notifications', body: 'server words', meta, target: null };
}

const TWO_FOLDERS = {
  count: 34,
  groups: [
    {
      storage: 'ekip',
      path: 'Rapor',
      name: 'Rapor',
      count: 30,
      counts: { 'file.uploaded': 30 },
      parts: [{ key: 'file_uploaded', count: 30 }],
    },
    {
      storage: 'ekip',
      path: 'Fotograflar',
      name: 'Fotoğraflar',
      count: 4,
      counts: { 'file.trashed': 3, 'comment.added': 1 },
      parts: [
        { key: 'file_trashed', count: 3 },
        { key: 'comment_added', count: 1 },
      ],
    },
  ],
};

describe('the digest phrases', () => {
  // ⚠ The words are the SERVER catalogue's (the digest's email says them too);
  // this table is the desktop shell's offline copy and must not drift.
  for (const lang of ['en', 'tr'] as const) {
    it(`are the server catalogue's words in ${lang}`, () => {
      const fromServer = serverParts(lang);
      expect(Object.keys(fromServer).length, 'read no server.notify.digest.* keys').toBeGreaterThan(10);
      expect(DIGEST_PARTS[lang]).toEqual(fromServer);
    });
  }
});

describe('a digest', () => {
  it('says folder by folder what changed, in each reader language', () => {
    expect(renderNotification(digest(TWO_FOLDERS), 'tr')).toEqual({
      title: '34 bildirim',
      body: 'Rapor: 30 dosya eklendi; Fotoğraflar: 3 dosya çöp kutusuna taşındı, 1 yorum',
    });
    expect(renderNotification(digest(TWO_FOLDERS), 'en')).toEqual({
      title: '34 notifications',
      body: 'Rapor: 30 files added; Fotoğraflar: 3 files moved to the trash, 1 comment',
    });
  });

  it('says one of a kind in the singular', () => {
    const one = {
      count: 2,
      groups: [
        {
          storage: 'ekip',
          path: 'Rapor',
          name: 'Rapor',
          count: 2,
          parts: [
            { key: 'file_uploaded', count: 1 },
            { key: 'comment_added', count: 1 },
          ],
        },
      ],
    };
    expect(renderNotification(digest(one), 'en')).toEqual({ title: '2 notifications', body: 'Rapor: 1 file added, 1 comment' });
  });

  it('a digest of one says what that one row says', () => {
    const one = {
      count: 1,
      groups: [{ storage: 'ekip', path: 'Rapor', name: 'Rapor', count: 1, parts: [{ key: 'file_uploaded', count: 1 }] }],
      item: {
        event: 'file.uploaded',
        title: 'file.uploaded',
        body: 'Rapor/a.pdf',
        meta: { node: { path: 'Rapor/a.pdf', name: 'a.pdf' } },
      },
    };
    expect(renderNotification(digest(one), 'en')).toEqual({ title: 'New file: a.pdf', body: 'Rapor/a.pdf' });
    expect(renderNotification(digest(one), 'tr').title).toBe('Yeni dosya: a.pdf');
  });

  it('never prints the name of a folder whose name is encrypted', () => {
    const cipher = 'Kasa/AbCdEfGhIjKlMnOpQrStUvWx';
    const locked = {
      count: 2,
      groups: [
        {
          storage: 'ekip',
          path: cipher,
          name: '',
          encrypted: true,
          e2e_root: 'ekip://Kasa',
          count: 2,
          parts: [{ key: 'file_uploaded', count: 2 }],
        },
      ],
    };
    const shown = renderNotification(digest(locked), 'en');
    expect(shown.body).toBe('🔒 Encrypted item: 2 files added');
    expect(shown.body).not.toContain('AbCdEf');
    // Where the reader's explorer has the folder unlocked, its own name.
    const named = renderNotification(digest(locked), 'en', {
      e2eName: (wire) => (wire === `ekip://${cipher}` ? { name: 'Projeler', path: 'Kasa/Projeler' } : null),
    });
    expect(named.body).toBe('Projeler: 2 files added');
  });

  it('sums up the folders it does not name and the rows that name no folder', () => {
    const many = { ...TWO_FOLDERS, count: 40, more_folders: 3, other_parts: [{ key: 'admin', count: 2 }] };
    expect(renderNotification(digest(many), 'en').body).toBe(
      'Rapor: 30 files added; Fotoğraflar: 3 files moved to the trash, 1 comment; 3 more folders; 2 administrator alerts',
    );
    expect(renderNotification(digest(many), 'tr').body).toBe(
      'Rapor: 30 dosya eklendi; Fotoğraflar: 3 dosya çöp kutusuna taşındı, 1 yorum; 3 klasör daha; 2 yönetici uyarısı',
    );
  });

  it('reads a part it does not know as "other", never as its key', () => {
    const unknown = {
      count: 2,
      groups: [{ storage: 'ekip', path: 'Rapor', name: 'Rapor', count: 2, parts: [{ key: 'some_later_kind', count: 2 }] }],
    };
    expect(renderNotification(digest(unknown), 'en').body).toBe('Rapor: 2 other notifications');
  });

  it('a language pack translates the title and the parts', () => {
    const strings = {
      'server.notify.notification.digest.title': '{count} notificaciones',
      'server.notify.digest.file_uploaded': '{count} archivos añadidos',
      'server.notify.digest.file_uploaded_one': '{count} archivo añadido',
    };
    const shown = renderNotification(digest(TWO_FOLDERS), 'en', { strings, lang: 'es' });
    expect(shown.title).toBe('34 notificaciones');
    expect(shown.body.startsWith('Rapor: 30 archivos añadidos; ')).toBe(true);
    // A phrase the pack lacks falls back to the built-in table, per phrase.
    expect(shown.body).toContain('3 files moved to the trash');
  });
});
