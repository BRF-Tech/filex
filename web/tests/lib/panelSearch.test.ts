// The admin panel's search, as rules (core lib/panelSearch, task #168,
// docs/ADMIN-PANEL.md → Search).
//
// ⚠ Same reasoning as fileFilters.test.ts: the rules live in
// @brftech/filex-core, the core package has no test runner of its own, and
// these are pure functions - so they are exercised here, in the app that
// ships them.
//
// What is pinned is the owner's acceptance list:
//   - "Uygulamalar", "apps" and "uygulama" put the Apps page first;
//   - "convert" finds the installed Convert app, a person called Convert and
//     at most three files;
//   - `file:rapor` finds files and nothing else;
//   - a label answers in the interface's language and in English, with the
//     Turkish letters and case folded;
// and the prefixes, which are commands and must not swallow ordinary words.
import { describe, expect, it } from 'vitest';

import { foldText } from '@brftech/filex-core/src/lib/fileFilters';
import {
  PANEL_SEARCH_FILES_CAP,
  PANEL_SEARCH_PREFIX_ORDER,
  panelScore,
  panelSearchGroups,
  panelSearchRows,
  panelWords,
  parsePanelQuery,
  withPanelPrefix,
  type PanelSearchItem,
} from '@brftech/filex-core/src/lib/panelSearch';

/* A Turkish panel, as the host builds it: the label in Turkish, the English
   label as a term, the synonyms as aliases. */
const PAGES: PanelSearchItem[] = [
  { id: 'page:users', kind: 'page', label: 'Kullanıcılar', terms: ['Users'], aliases: ['hesaplar', 'kişiler', 'accounts'] },
  { id: 'page:plugins', kind: 'page', label: 'Eklentiler', terms: ['Plugins'], aliases: ['uygulamalar', 'uzantılar', 'mağaza', 'apps', 'extensions', 'store'] },
  { id: 'tab:plugins:apps', kind: 'page', label: 'Uygulamalar', detail: 'Eklentiler', terms: ['Apps'] },
  { id: 'tab:plugins:defaults', kind: 'page', label: 'Varsayılan uygulamalar', detail: 'Eklentiler', terms: ['Default apps'] },
  { id: 'page:auth-providers', kind: 'page', label: 'Kimlik sağlayıcılar', terms: ['Identity providers'], aliases: ['LDAP', 'Active Directory', 'SSO', 'OIDC'] },
  { id: 'tab:auth-providers:ldap', kind: 'page', label: 'LDAP / Active Directory', detail: 'Kimlik sağlayıcılar' },
  { id: 'page:login-security', kind: 'page', label: 'Giriş güvenliği', terms: ['Sign-in security'] },
  { id: 'page:encryption', kind: 'page', label: 'Şifreleme', terms: ['Encryption'] },
  { id: 'setting:require-2fa', kind: 'setting', label: 'İki adımlı doğrulama zorunlu', terms: ['Require two-factor authentication'], aliases: ['2FA', 'TOTP'] },
  { id: 'setting:trash-retention', kind: 'setting', label: 'Çöp kutusu saklama', terms: ['Trash retention'] },
  { id: 'page:app-convert', kind: 'app', label: 'Dönüştürmeler', detail: 'Uygulamalar' },
];

/* What the server and the file index answered for "convert". */
const FOUND: PanelSearchItem[] = [
  { id: 'app:convert', kind: 'app', label: 'Convert', terms: ['Dönüştür', 'convert'], found: true },
  { id: 'user:12', kind: 'user', label: 'Convert Bot', detail: 'bot@example.com', found: true },
  { id: 'file:1:/a/convert-notes.txt', kind: 'file', label: 'convert-notes.txt', found: true },
  { id: 'file:1:/b/convert.md', kind: 'file', label: 'convert.md', found: true },
  { id: 'file:1:/c/how-to-convert.pdf', kind: 'file', label: 'how-to-convert.pdf', found: true },
  { id: 'file:1:/d/convert.log', kind: 'file', label: 'convert.log', found: true },
];

/* …and for "rapor": a file, and a person the server found too. */
const FOUND_RAPOR: PanelSearchItem[] = [
  { id: 'file:1:/e/rapor.xlsx', kind: 'file', label: 'rapor.xlsx', found: true },
  { id: 'user:9', kind: 'user', label: 'Rapor Bot', found: true },
];

const search = (q: string, items: PanelSearchItem[] = PAGES) => panelSearchGroups(items, parsePanelQuery(q));
const first = (q: string, items?: PanelSearchItem[]) => panelSearchRows(search(q, items))[0]?.id;

describe('the query: words and prefixes', () => {
  it('folds Turkish letters and case the way a file name is found (foldText)', () => {
    expect(panelWords('  Kullanıcı  AYARLARI ')).toEqual(['kullanici', 'ayarlari']);
    expect(panelWords('ŞİFRE Güvenliği çöp')).toEqual(['sifre', 'guvenligi', 'cop']);
    expect(foldText('İSTANBUL')).toBe('istanbul');
  });

  it('a known prefix narrows to its kind, in any case and with or without a space', () => {
    expect(parsePanelQuery('file:rapor')).toMatchObject({ kind: 'file', prefix: 'file', text: 'rapor', words: ['rapor'] });
    expect(parsePanelQuery('User: Ada')).toMatchObject({ kind: 'user', prefix: 'user', text: 'Ada' });
    expect(parsePanelQuery('  key :ci-bot')).toMatchObject({ kind: 'key', text: 'ci-bot' });
    for (const p of ['file', 'user', 'app', 'key', 'group', 'storage', 'setting']) {
      expect(parsePanelQuery(`${p}:x`).kind, p).not.toBeNull();
    }
    expect(PANEL_SEARCH_PREFIX_ORDER).toEqual(['file', 'user', 'app', 'key', 'group', 'storage', 'setting']);
  });

  it('any other word before a colon is searched for as it is', () => {
    expect(parsePanelQuery('C:\\data')).toMatchObject({ kind: null, prefix: '' });
    expect(parsePanelQuery('12:30')).toMatchObject({ kind: null, text: '12:30' });
    expect(parsePanelQuery('page:users').kind).toBeNull();
  });

  it('a prefix is put on and taken off without losing the words', () => {
    expect(withPanelPrefix('convert', 'file')).toBe('file:convert');
    expect(withPanelPrefix('user:ada', 'file')).toBe('file:ada');
    expect(withPanelPrefix('file:ada', '')).toBe('ada');
  });
});

describe('the owner’s acceptance list', () => {
  it('"Uygulamalar", "apps" and "uygulama" put the Apps page first', () => {
    for (const q of ['Uygulamalar', 'apps', 'uygulama', 'UYGULAMALAR', 'Apps']) {
      expect(first(q), q).toBe('tab:plugins:apps');
    }
  });

  it('"convert" finds the installed app, the person and at most three files', () => {
    const groups = search('convert', [...PAGES, ...FOUND]);
    expect(groups.map((g) => g.kind)).toEqual(['app', 'user', 'file']);
    expect(groups.find((g) => g.kind === 'app')!.items[0].id).toBe('app:convert');
    expect(groups.find((g) => g.kind === 'user')!.items.map((i) => i.id)).toEqual(['user:12']);
    const files = groups.find((g) => g.kind === 'file')!;
    expect(files.items).toHaveLength(PANEL_SEARCH_FILES_CAP);
    expect(PANEL_SEARCH_FILES_CAP).toBe(3);
    expect(files.total).toBe(4);
  });

  it('`file:rapor` finds files and nothing else', () => {
    expect(search('rapor', [...PAGES, ...FOUND_RAPOR]).map((g) => g.kind)).toEqual(['user', 'file']);
    const groups = search('file:rapor', [...PAGES, ...FOUND_RAPOR]);
    expect(groups.map((g) => g.kind)).toEqual(['file']);
    expect(groups[0].items.map((i) => i.id)).toEqual(['file:1:/e/rapor.xlsx']);
  });

  it('with a prefix the kind shows more than its usual few', () => {
    const groups = search('file:convert', FOUND);
    expect(groups[0].items).toHaveLength(4);
  });
});

describe('a label answers in two languages and to its synonyms', () => {
  it('the interface’s label and the English one', () => {
    expect(first('kullanici')).toBe('page:users');
    expect(first('users')).toBe('page:users');
    expect(first('identity providers')).toBe('page:auth-providers');
    expect(first('sign-in')).toBe('page:login-security');
  });

  it('Turkish letters folded both ways', () => {
    expect(first('sifreleme')).toBe('page:encryption');
    expect(first('ŞİFRELEME')).toBe('page:encryption');
    expect(first('giris guvenligi')).toBe('page:login-security');
    expect(first('cop kutusu')).toBe('setting:trash-retention');
  });

  it('"LDAP" finds Identity providers, the page before its tab', () => {
    const rows = panelSearchRows(search('ldap')).map((r) => r.id);
    expect(rows.slice(0, 2)).toEqual(['page:auth-providers', 'tab:auth-providers:ldap']);
  });

  it('"2fa" finds the setting by its synonym', () => {
    expect(first('2fa')).toBe('setting:require-2fa');
  });

  it('a name outranks a synonym: the row NAMED what was typed comes first', () => {
    const tab = PAGES.find((p) => p.id === 'tab:plugins:apps')!;
    const plugins = PAGES.find((p) => p.id === 'page:plugins')!;
    expect(panelScore(tab, panelWords('apps'))).toBeGreaterThan(panelScore(plugins, panelWords('apps')));
  });

  it('every word must be somewhere; a row the server found stays even when its words are not on it', () => {
    expect(search('kullanici zzz')).toEqual([]);
    const byEmail: PanelSearchItem = { id: 'user:3', kind: 'user', label: 'Ada Lovelace', found: true };
    expect(panelSearchRows(search('ada@example.com', [byEmail])).map((r) => r.id)).toEqual(['user:3']);
  });

  it('no words, no groups (the panel shows the recent searches instead)', () => {
    expect(search('')).toEqual([]);
    expect(search('   ')).toEqual([]);
    expect(search('file:')).toEqual([]);
  });
});

describe('the groups are drawn in a fixed order, the kinds the panel owns first', () => {
  it('pages, settings, apps, then the records', () => {
    const items: PanelSearchItem[] = [
      { id: 'file:x', kind: 'file', label: 'güvenlik.pdf', found: true },
      { id: 'user:x', kind: 'user', label: 'Güvenlik Ekibi', found: true },
      { id: 'setting:x', kind: 'setting', label: 'Güvenlik ayarı' },
      // Every row must hold the typed word and none may be NAMED it, or the
      // group order is not what is measured. ("Giriş güvenliği" does not hold
      // "guvenlik": the search folds letters, it does not stem k/ğ.)
      { id: 'page:x', kind: 'page', label: 'Güvenlik ve giriş' },
    ];
    expect(search('guvenlik', items).map((g) => g.kind)).toEqual(['page', 'setting', 'user', 'file']);
  });

  it('except a group whose best row is NAMED what was typed: it comes first', () => {
    const items: PanelSearchItem[] = [
      { id: 'page:trash', kind: 'page', label: 'Çöp kutusu', detail: 'Silinen dosyalar, saklama süresi dolana kadar', terms: ['Trash'] },
      { id: 'setting:trash-retention', kind: 'setting', label: 'Çöp kutusu saklama', terms: ['Trash retention'] },
    ];
    expect(search('çöp kutusu saklama', items).map((g) => g.kind)).toEqual(['setting', 'page']);
    expect(search('trash retention', items).map((g) => g.kind)).toEqual(['setting']);
    // A synonym is not a name: the fixed order holds.
    const viaSynonym: PanelSearchItem[] = [
      { id: 'page:x', kind: 'page', label: 'Roller', aliases: ['2FA'] },
      { id: 'setting:y', kind: 'setting', label: 'İki adımlı doğrulama zorunlu', aliases: ['2FA'] },
    ];
    expect(search('2fa', viaSynonym).map((g) => g.kind)).toEqual(['page', 'setting']);
  });
});
