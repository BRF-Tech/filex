// ONE language pack translates the whole product — explorer and admin panel —
// through ONE flat namespace of keys. These are the facts that make that true;
// if one stops being true, a pack's key becomes ambiguous and this fails.
//
// The format itself: docs/PLUGIN-KIT.md → "Writing a language pack". The
// catalogue a translator starts from: scripts/i18n-export.mjs (and the release
// asset / the running server's /admin/i18n/filex-catalogue-en.json, both built
// by scripts/lib/i18n-catalogue.mjs, which these tests exercise directly).
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  buildCatalogue,
  loadCatalogue,
  loadServerNotes,
  notifyWords,
  plainTokens,
  pluralCategories,
  tableOf,
} from '../../../scripts/lib/i18n-catalogue.mjs';

const ROOT = path.resolve(__dirname, '../../..');
const cat = loadCatalogue(ROOT);
const shared = Object.keys(cat.explorer).filter((k) => k in cat.admin);

function prefixes(keys: string[]): Set<string> {
  const out = new Set<string>();
  for (const k of keys) {
    const parts = k.split('.');
    for (let i = 1; i < parts.length; i++) out.add(parts.slice(0, i).join('.'));
  }
  return out;
}

describe('one flat namespace over two catalogues', () => {
  it('finds both catalogues (a scan of nothing would pass everything)', () => {
    expect(Object.keys(cat.explorer).length).toBeGreaterThan(1000);
    expect(Object.keys(cat.admin).length).toBeGreaterThan(1000);
    expect(shared.length).toBeGreaterThan(0);
  });

  it('a key the two SHARE has the same English in both — one translation must fit both screens', () => {
    // ⚠ `storages.fieldHelp.smbShare` was the one exception: the admin
    // panel's JSON held `\\nas\\media`, which decodes to ONE backslash, while
    // the explorer said `\\nas\media` — the right UNC path. The translator of
    // the Spanish pack found it; the source is fixed and this keeps it fixed.
    const differ = shared.filter((k) => cat.explorer[k] !== cat.admin[k]);
    expect(differ, differ.map((k) => `${k}\n  explorer: ${cat.explorer[k]}\n  admin:    ${cat.admin[k]}`).join('\n')).toEqual([]);
  });

  it('…and the same Turkish, so the two screens do not disagree in the one language we ship', () => {
    const differ = shared.filter((k) => cat.explorerTr[k] !== cat.adminTr[k]);
    expect(differ).toEqual([]);
  });

  it('a shared key uses no syntax only ONE of the two renderers understands', () => {
    // The admin panel's vue-i18n treats @ | { } specially and needs {'@'};
    // the explorer prints {'@'} as written. A shared string can use neither.
    const bad = shared.filter((k) => /[@|]|\{'/.test(cat.explorer[k]));
    expect(bad).toEqual([]);
  });

  it('no key of one catalogue is a dotted PREFIX of a key of the other', () => {
    // If `a.b` were a string in one table and `a.b.c` a key in the other, the
    // admin panel could not hold both once the flat pack is nested for
    // vue-i18n. The explorer has 54 such pairs WITHIN itself
    // (`toolbar.search` + `toolbar.search.placeholder`) — harmless, because
    // web/src/i18n folds only the admin panel's own keys into vue-i18n and
    // the explorer reads its table flat.
    const ex = Object.keys(cat.explorer);
    const ad = Object.keys(cat.admin);
    const adPre = prefixes(ad);
    const exPre = prefixes(ex);
    expect(ex.filter((k) => adPre.has(k))).toEqual([]);
    expect(ad.filter((k) => exPre.has(k))).toEqual([]);
  });

  it('every key has the shape the server accepts in a pack', () => {
    const all = [...new Set([...Object.keys(cat.explorer), ...Object.keys(cat.admin)])];
    const bad = all.filter((k) => !/^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$/.test(k) || new TextEncoder().encode(k).length > 128);
    expect(bad).toEqual([]);
  });
});

describe('the exported catalogue', () => {
  const built = buildCatalogue(ROOT, { where: false });

  it("is exactly a pack's ui_locales[<lang>] shape: one flat object of strings", () => {
    const entries = Object.entries(built.strings);
    expect(entries.length).toBe(new Set([...Object.keys(cat.explorer), ...Object.keys(cat.admin), ...Object.keys(cat.server)]).size);
    expect(entries.every(([k, v]) => typeof k === 'string' && typeof v === 'string')).toBe(true);
    expect(Object.keys(built.strings)).toEqual([...Object.keys(built.strings)].sort());
  });

  it('says for every key which table it belongs to and so which grammar applies', () => {
    for (const k of ['ctx.download', 'appPlugins.title', shared[0]]) {
      const row = built.context.keys[k];
      expect(row.in).toBe(tableOf(cat, k));
      expect(row.syntax).toBe({ explorer: 'plain', admin: 'vue-i18n', both: 'shared', server: 'server' }[row.in]);
      expect(row.tr).toBeTruthy();
    }
  });

  it('is deterministic — the web build embeds it and two builds of one tree must agree', () => {
    const again = buildCatalogue(ROOT, { where: false });
    expect(JSON.stringify(again.strings)).toBe(JSON.stringify(built.strings));
    expect(JSON.stringify(again.context)).toBe(JSON.stringify(built.context));
  });
});

// The text the SERVER writes — e-mails, the no-JS public pages, the install
// review, the notifications — is the catalogue's third table, under
// `server.`. A translator gets every key of it with the mail or page that
// shows it and what each placeholder holds, because a server string is read
// out of context: nobody sees a mail line next to the screen it belongs to.
describe('the server table', () => {
  const built = buildCatalogue(ROOT, { where: false });
  const server = Object.keys(cat.server);

  it('is there — the Go catalogue and the notification phrases (a scan of nothing passes everything)', () => {
    expect(server.length).toBeGreaterThan(150);
    expect(server.filter((k) => k.startsWith('server.notify.')).length).toBeGreaterThan(25);
    expect(server.filter((k) => k.startsWith('server.mail.')).length).toBeGreaterThan(30);
  });

  it('exports nothing a translator could only copy — a value that is ONE placeholder', () => {
    /* ⚠ Ten notification bodies were the whole string `{path}`, `{reason}` or
       `{body}`: no word in them, no order to choose, no punctuation. A pack
       had to carry ten entries it could only reproduce byte for byte, and one
       typo lost the path off a bell row. They are not exported any more; the
       renderer falls back to the same template (scripts/lib/i18n-catalogue.mjs). */
    const bare = server.filter((k) => /^\s*\{[A-Za-z0-9_]+\}\s*$/.test(cat.server[k]));
    expect(bare).toEqual([]);
    expect(cat.server['server.notify.file.uploaded.title'], 'the events are still exported').toBeTruthy();
    expect(cat.server['server.notify.file.uploaded.body'], 'and their bare `{path}` body is not').toBeUndefined();
  });

  it('says a word ONCE — the one a mail and a bell share comes from one key', () => {
    /* "Someone" and "a file" were `server.mail.drop_received.someone` and
       `server.mail.share.unnamed_file` AND `server.notify.word.someone` and
       `.unnamed`: one word, two keys, two chances to answer it differently.
       The catalogue holds them once, in the server's own JSON, so Go (the
       mail) and the browser (the bell) read the same string. */
    for (const gone of ['server.mail.drop_received.someone', 'server.mail.share.unnamed_file']) {
      expect(cat.server[gone], `${gone} is back`).toBeUndefined();
    }
    for (const w of ['someone', 'unnamed', 'appNotice', 'openWith']) {
      expect(cat.server[`server.notify.word.${w}`], w).toBeTruthy();
      expect(cat.serverTr[`server.notify.word.${w}`], w).toBeTruthy();
    }
  });

  it('…and the desktop shell’s offline copy of those words says the same thing', () => {
    /* web/src/lib/notificationText.ts keeps a `WORDS` table because the
       desktop main process has no catalogue to read. Two copies of a string
       drift; this is what stops them. */
    const words = notifyWords(path.join(ROOT, 'web/src/lib/notificationText.ts'));
    for (const lang of ['en', 'tr'] as const) {
      const table = lang === 'en' ? cat.server : cat.serverTr;
      for (const [w, v] of Object.entries(words[lang])) {
        expect(table[`server.notify.word.${w}`], `${lang} ${w}`).toBe(v);
      }
    }
  });

  it('lives alone under `server.` — no interface key is there, and none is a prefix of another table', () => {
    expect(server.every((k) => k.startsWith('server.'))).toBe(true);
    const ui = [...Object.keys(cat.explorer), ...Object.keys(cat.admin)];
    expect(ui.filter((k) => k === 'server' || k.startsWith('server.'))).toEqual([]);
  });

  it('has Turkish only for keys it has, with the same placeholders', () => {
    for (const [k, v] of Object.entries(cat.serverTr)) {
      expect(k in cat.server, k).toBe(true);
      const base = /_one$/.test(k) ? k.replace(/_one$/, '') : k;
      const allowed = new Set([...plainTokens(cat.server[k]), ...plainTokens(cat.server[base] ?? '')]);
      expect(plainTokens(v).filter((x: string) => !allowed.has(x)), k).toEqual([]);
    }
  });

  it('tells the translator WHERE every key appears and WHAT every placeholder holds', () => {
    const noAbout = server.filter((k) => !built.context.keys[k].about);
    const noVar = server.flatMap((k) =>
      plainTokens(cat.server[k])
        .filter((v: string) => !built.context.keys[k].vars?.[v])
        .map((v: string) => `${k} {${v}}`),
    );
    expect(noAbout).toEqual([]);
    expect(noVar).toEqual([]);
    // Notes for keys that no longer exist would describe nothing.
    const notes = loadServerNotes(ROOT);
    expect(Object.keys(notes.keys).filter((k) => !(k in cat.server))).toEqual([]);
  });

  it('marks the sentences about a count, and lists which forms each language needs', () => {
    expect(built.context.keys['server.mail.valid_days'].plural).toBe(true);
    expect(built.context.keys['server.mail.greeting'].plural).toBeUndefined();
    expect(built.context.plural_categories.ar).toEqual(['zero', 'one', 'two', 'few', 'many', 'other']);
    expect(built.context.plural_categories.ru).toEqual(['one', 'few', 'many', 'other']);
    // ⚠ Integers only: modern CLDR's Spanish `many` means an exact million.
    expect(built.context.plural_categories.es).toEqual(['one', 'other']);
    expect(pluralCategories('ja')).toEqual(['other']);
  });

  it('is written in the product\u2019s own words \u2014 "email", never "e-mail"', () => {
    /* ⚠ The notes a translator reads are product text too. The catalogue said
       "e-mail" in 64 entries (every `server.mail.*` key inherits its group's
       note) while every screen said "email" — and a translator who trusts the
       note over the string writes the hyphen into the language they are
       translating into. docs/CONTRIBUTING.md → Words. */
    const notes = [
      built.context.about,
      ...Object.values(built.context.keys).flatMap((k) => [k.about ?? '', ...Object.values(k.vars ?? {})]),
    ];
    const bad = notes.filter((n) => /[eE]-mails?\b/.test(n));
    expect(bad.slice(0, 5)).toEqual([]);
    expect(notes.some((n) => /email/i.test(n)), 'the scan reads nothing if no note mentions email at all').toBe(true);
  });

  it('names the build it describes, not a constant', () => {
    expect(built.context.filex).toMatch(/^\d+\.\d+\.\d+/);
  });
});
