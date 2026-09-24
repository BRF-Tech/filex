// A notification in a language pack's language.
//
// The phrases the bell, the browser pop-up and the desktop app print are
// built in English and Turkish (lib/notificationText.ts). Measured
// 2026-09-22: a Spanish screen's bell said "1 file received" — the renderer
// knew two languages and read everything else as English. A pack now carries
// the phrases as the server catalogue's `server.notify.*` keys, per key, with
// plural forms by CLDR category.
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { NOTIFICATION_PHRASES, renderNotification } from '@/lib/notificationText';
import { loadNotifyTables } from '../../../scripts/lib/i18n-catalogue.mjs';

const drop = (count: number, uploader = '') => ({
  event: 'drop.received',
  title: 'New file upload',
  body: '',
  meta: { folder: 'Facturas', count, uploader, node: { name: 'Facturas', path: '/Facturas' } },
});

describe('a pack language in the bell', () => {
  const es = {
    'server.notify.drop.received.title': '{count} archivos recibidos',
    'server.notify.drop.received.title_one': 'Un archivo recibido',
    'server.notify.word.someone': 'Alguien',
    'server.notify.file.uploaded.title': 'Archivo nuevo: {name}',
    'nav.files': 'Archivos', // an interface key: ignored here
  };

  it("speaks the pack's words, its singular for one, and the English phrase where it has none", () => {
    expect(renderNotification(drop(1), 'en', { strings: es, lang: 'es' })).toEqual({
      title: 'Un archivo recibido',
      body: 'Alguien → Facturas', // the English body, with the pack's word for nobody
    });
    expect(renderNotification(drop(3, 'Lucía'), 'en', { strings: es, lang: 'es' }).title).toBe('3 archivos recibidos');
    const upload = { event: 'file.uploaded', meta: { node: { name: 'a.pdf', path: '/Docs/a.pdf' } } };
    expect(renderNotification(upload, 'en', { strings: es, lang: 'es' })).toEqual({ title: 'Archivo nuevo: a.pdf', body: '/Docs/a.pdf' });
    const deleted = { event: 'file.deleted', meta: { node: { name: 'a.pdf', path: '/Docs/a.pdf' } } };
    expect(renderNotification(deleted, 'en', { strings: es, lang: 'es' }).title, 'untranslated: English').toBe('File deleted: a.pdf');
  });

  it('takes the form of the count’s CLDR category — six in Arabic', () => {
    const ar = {
      'server.notify.drop.received.title_zero': 'لم يصل أي ملف',
      'server.notify.drop.received.title_one': 'وصل ملف واحد',
      'server.notify.drop.received.title_two': 'وصل ملفان',
      'server.notify.drop.received.title_few': 'وصلت {count} ملفات',
      'server.notify.drop.received.title_many': 'وصل {count} ملفًا',
      'server.notify.drop.received.title': 'وصل {count} ملف',
    };
    const got = [0, 1, 2, 3, 11, 100].map((n) => renderNotification(drop(n), 'en', { strings: ar, lang: 'ar' }).title);
    expect(got).toEqual(['لم يصل أي ملف', 'وصل ملف واحد', 'وصل ملفان', 'وصلت 3 ملفات', 'وصل 11 ملفًا', 'وصل 100 ملف']);
  });

  it('without a pack, nothing changes', () => {
    expect(renderNotification(drop(1), 'en').title).toBe('1 file received');
    expect(renderNotification(drop(4), 'tr').title).toBe('4 dosya geldi');
  });
});

describe('the catalogue reads these phrases as server keys', () => {
  const t = loadNotifyTables(path.resolve(__dirname, '../../src/lib/notificationText.ts'));

  /** A value a translator could only copy: the whole string is one placeholder. */
  const bare = (v: string) => /^\s*\{[A-Za-z0-9_]+\}\s*$/.test(v);

  it('one key per phrase field, per language — the same words this file renders', () => {
    for (const [event, byLang] of Object.entries(NOTIFICATION_PHRASES)) {
      expect(t.en[`server.notify.${event}.title`]).toBe(bare(byLang.en.title) ? undefined : byLang.en.title);
      expect(t.tr[`server.notify.${event}.body`]).toBe(bare(byLang.tr.body) ? undefined : byLang.tr.body);
    }
    expect(t.en['server.notify.drop.received.title_one']).toBe('{count} file received');
  });

  it('a phrase that is ONLY a placeholder is not exported — there is nothing to translate', () => {
    /* ⚠ Ten of these: `{path}` for five file events, `{reason}`, `{body}`,
       `{folder}`, `{error}` and the app notice's `{notice_title}`. A pack had
       to carry ten entries it could only reproduce byte for byte, and one typo
       lost the path off a bell row. The renderer falls back to the same
       template, so nothing on screen changes. */
    expect(t.en['server.notify.file.uploaded.body']).toBeUndefined();
    expect(t.en['server.notify.plugin.notice.title']).toBeUndefined();
    expect(t.en['server.notify.file.uploaded.title'], 'the titles are still there').toBe('New file: {name}');
    expect(Object.values(t.en).filter(bare)).toEqual([]);
    expect(Object.values(t.tr).filter(bare)).toEqual([]);
  });

  it('the fallback words are the server catalogue’s, not a copy of their own', () => {
    /* "Someone" and "a file" are also what a MAIL says, so they live in
       backend/internal/srvtext/locales/*.json and Go and the browser read one
       string. This loader no longer invents `server.notify.word.*` keys of its
       own (web/tests/i18n/langPackCatalogue.test.ts holds the two together). */
    expect(t.en['server.notify.word.someone']).toBeUndefined();
  });
});
