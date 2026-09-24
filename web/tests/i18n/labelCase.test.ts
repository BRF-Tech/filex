// One label, one spelling.
//
// ⚠ Found by the v0.41.0 screenshot pass (2026-09-14): the embed with no
// navigation panel offered "New Folder" while the panel's menu, the shortcut
// list and every other surface said "New folder" — the same command under two
// names on neighbouring screens. The catalogue had four such pairs ("Copy Path"
// / "Copy path", "Go To Path" / "Go to path", "Open in New Tab" / "Open in new
// tab"), and Turkish carried the same split ("Yeni Klasör" / "Yeni klasör").
// Sentence case is the house style; the rule here is only the part a machine
// can hold: two labels of two or more words that differ ONLY in case are one
// label written twice, and must be written the same way.
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { loadCatalogue } from '../../../scripts/lib/i18n-catalogue.mjs';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';

function caseTwins(cat: Record<string, string>, tag: string): string[] {
  const seen = new Map<string, Map<string, string>>();
  for (const [key, value] of Object.entries(cat)) {
    if (value.trim().split(/\s+/).length < 2 || value.length > 40) continue;
    // The first letter is not the question: "access key" is a fragment set
    // inside a sentence and "Access key" the same words as a label. Case
    // INSIDE the label is ("New Folder" / "New folder").
    const shape = value.charAt(0).toLocaleLowerCase(tag) + value.slice(1);
    const folded = value.toLocaleLowerCase(tag);
    const spellings = seen.get(folded) ?? new Map<string, string>();
    if (!spellings.has(shape)) spellings.set(shape, key);
    seen.set(folded, spellings);
  }
  return [...seen.values()]
    .filter((s) => s.size > 1)
    .map((s) => [...s.entries()].map(([v, k]) => `${k} = "${cat[k]}"`).join('  vs  '));
}

describe('core catalogue: a label is spelled one way', () => {
  it('en', () => {
    expect(caseTwins(en, 'en-US')).toEqual([]);
  });
  it('tr', () => {
    expect(caseTwins(tr, 'tr-TR')).toEqual([]);
  });
  it('the toolbar says what the menu says', () => {
    expect(en['toolbar.new_folder']).toBe(en['drive.new.folder']);
    expect(tr['toolbar.new_folder']).toBe(tr['drive.new.folder']);
  });
});

/* ── Title Case ───────────────────────────────────────────────────────────
 * ⚠ QA, 2026-09-21: "Çöpe At", "Geri Al", "Geri Getir", "Kalıcı Olarak Sil",
 * "Kısayol Ayarları" beside "Yeni klasör" and "Paylaşım bağlantısı"; English
 * had "Delete Permanently", "Keyboard Shortcuts", "This Week". The twin rule
 * above only catches a label written BOTH ways — a label that exists once, in
 * Title Case, passed it. Sentence case is the house style (docs/CONTRIBUTING.md
 * → Words), so a short label whose later words are capitalised is flagged
 * unless the capital belongs to a name.
 *
 * Both catalogues — the explorer's and the admin panel's — in both languages.
 */

/** Words that are names in their own right: a product, a key, a place in the
 *  interface quoted by name, a language. Not "words we like capitalised". */
const NAMES = new Set([
  'Trash', 'Home', 'Webhooks', 'Test', // a screen or button quoted by its name
  'Esc', 'Claude', 'Keycloak', 'Authentik', 'Hetzner', 'Ubuntu', 'Apple', 'Silicon',
  'Active', 'Directory', 'Sunday', 'English', 'İngilizce',
  'Acme', 'Cloud', 'Bulut', // the placeholder's made-up company name
  // …and its made-up person, in the file-request page's "your name" box
  // (public.your_name_ph). A sample name is a name.
  'Alex', 'Smith', 'Ahmet', 'Yılmaz',
]);

function titleCased(value: string, tag: string): string[] | null {
  const v = value.trim();
  if (v.length > 40 || /[.:!?]$/.test(v)) return null;
  // A quoted label and anything after an arrow are another screen's words.
  const text = v
    .replace(/[“"«][^”"»]*[”"»]/g, ' ')
    .replace(/→.*$/, ' ')
    .replace(/\{[^}]*\}/g, ' ');
  const words = text.split(/\s+/).filter(Boolean);
  while (words.length && !/\p{L}/u.test(words[0])) words.shift();
  if (words.length < 2 || words.length > 5) return null;
  const odd = words.slice(1).filter((w) => {
    const bare = w.replace(/^[([“"'‘+—–-]+|[)\]”"'’,;…]+$/g, '');
    if (!bare || !/^\p{Lu}/u.test(bare)) return false;
    if (bare === bare.toLocaleUpperCase(tag)) return false; // an acronym
    if (/\d|[./@_+-]/.test(bare)) return false; // Ctrl+S, a file name
    const root = bare.replace(/['’].*$/, '');
    if (/^\p{Lu}{2,}s?$/u.test(root)) return false; // URLs, URL'leri
    if (/^\p{Lu}\p{Ll}+\p{Lu}/u.test(bare)) return false; // WebDAV, MinIO
    return !NAMES.has(root);
  });
  return odd.length ? odd : null;
}

describe('a label is in sentence case', () => {
  const cat = loadCatalogue(path.resolve(__dirname, '../../..')) as Record<string, Record<string, string>>;
  for (const [lang, table, tag] of [
    ['en', { ...cat.explorer, ...cat.admin }, 'en-US'],
    ['tr', { ...cat.explorerTr, ...cat.adminTr }, 'tr-TR'],
  ] as const) {
    it(lang, () => {
      const found = Object.entries(table)
        .map(([k, v]) => [k, v, titleCased(v, tag)] as const)
        .filter(([, , odd]) => odd)
        .map(([k, v, odd]) => `${k} = "${v}" (${odd!.join(', ')})`);
      expect(found).toEqual([]);
    });
  }

  it('the rule still sees a Title Case label (a detector that finds nothing proves nothing)', () => {
    expect(titleCased('Kalıcı Olarak Sil', 'tr-TR')).toEqual(['Olarak', 'Sil']);
    expect(titleCased('Delete Permanently', 'en-US')).toEqual(['Permanently']);
    expect(titleCased('Move to Trash', 'en-US')).toBeNull();
    expect(titleCased('Use path-style URLs (Hetzner, MinIO)', 'en-US')).toBeNull();
  });
});
