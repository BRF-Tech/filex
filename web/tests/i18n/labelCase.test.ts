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
import { describe, expect, it } from 'vitest';
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
