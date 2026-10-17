/**
 * The dictionaries an administrator adds to an app (Admin → Plugins → an
 * app's page → Dictionaries; backend wasmplugin/extras.go): where each file
 * goes, and the added files read back as one row per language.
 *
 * The office editor's spell checker and hyphenation ask for
 * `<package>/extra/dictionaries/<lang>/<file>.txt`, the file names being the
 * Document Server's (`pl_PL.aff`, `pl_PL.dic`, `hyph_pl_PL.dic`): the place
 * is the app's contract, and this file is the one place the admin panel
 * writes it. The server holds the same pattern and refuses any other path.
 */
import type { AppExtraCatalogItem, AppExtraFile, AppPluginManifest } from '@/api/appPlugins';

/** A language folder's name as the dictionaries are published: `pl_PL`,
 *  `de_DE`, `sr_Latn_RS` (the server's pattern, extras.go extraKinds). */
export const DICTIONARY_LANG = /^[A-Za-z_]{2,16}$/;

/** The three files of one language: the spell checker's affix and word
 *  lists, and the hyphenation patterns (optional). */
export type DictionaryPart = 'aff' | 'dic' | 'hyph';

export const DICTIONARY_PARTS: readonly DictionaryPart[] = ['aff', 'dic', 'hyph'];

/** Where a language's file goes under the app's extra/. */
export function dictionaryPath(lang: string, part: DictionaryPart): string {
  const name = part === 'hyph' ? `hyph_${lang}.dic` : `${lang}.${part}`;
  return `dictionaries/${lang}/${name}.txt`;
}

/** One language among the added files. */
export interface DictionaryRow extends Record<string, unknown> {
  lang: string;
  /** Which of the three files it has. */
  parts: DictionaryPart[];
  /** Together. */
  size: number;
  /** The newest file's time. */
  modified: string;
  /** Its files' paths under extra/: what removing the language removes. */
  paths: string[];
}

const DICTIONARY_FILE = /^dictionaries\/([A-Za-z_]{2,16})\/([A-Za-z_]{2,32})\.(aff|dic)\.txt$/;

/** The added files as one row per language, by language; files of other
 *  kinds are left out. */
export function dictionaryRows(files: readonly AppExtraFile[]): DictionaryRow[] {
  const byLang = new Map<string, DictionaryRow>();
  for (const f of files) {
    const m = DICTIONARY_FILE.exec(f.path);
    if (!m) continue;
    const [, lang, name, ext] = m;
    const part: DictionaryPart = ext === 'aff' ? 'aff' : name.startsWith('hyph_') ? 'hyph' : 'dic';
    const row = byLang.get(lang) ?? { lang, parts: [], size: 0, modified: '', paths: [] };
    if (!row.parts.includes(part)) row.parts.push(part);
    row.size += f.size;
    if (f.modified > row.modified) row.modified = f.modified;
    row.paths.push(f.path);
    byLang.set(lang, row);
  }
  for (const row of byLang.values()) row.parts.sort((a, b) => DICTIONARY_PARTS.indexOf(a) - DICTIONARY_PARTS.indexOf(b));
  return [...byLang.values()].sort((a, b) => a.lang.localeCompare(b.lang));
}

/** A language folder as a person reads it ("Polish (Poland)"); its own name
 *  when the browser cannot say it. */
export function dictionaryLanguageName(lang: string, locale: string): string {
  const tag = lang.replace(/_/g, '-');
  try {
    const name = new Intl.DisplayNames([locale], { type: 'language' }).of(tag);
    return name && name.toLowerCase() !== tag.toLowerCase() ? name : lang;
  } catch {
    return lang;
  }
}

/**
 * The app names this kind of added file in its manifest (`ui.extras`, filex
 * 0.57) and reads its own package - the only apps that are given such files.
 * An app that names none (draw.io) has no Dictionaries section.
 */
export function declaresExtra(manifest: Pick<AppPluginManifest, 'ui'> | null | undefined, kind: string): boolean {
  const ui = manifest?.ui;
  return !!ui?.package_fetch && !!ui.extras && Object.prototype.hasOwnProperty.call(ui.extras, kind);
}

/** One language of the section: added, offered by the release's catalog, or both. */
export interface DictionaryTableRow extends DictionaryRow {
  /** The catalog offers it (one click installs it). */
  offered: boolean;
  /** Every file of the catalog's item is added (`partial`: some are). */
  installed: boolean;
  partial: boolean;
  /** The catalog's size when nothing is added yet. */
  offeredSize: number;
  license?: string;
}

/** The added languages and the catalog's, one row each, by language. */
export function dictionaryTableRows(added: readonly DictionaryRow[], offered: readonly AppExtraCatalogItem[]): DictionaryTableRow[] {
  const byLang = new Map<string, DictionaryTableRow>();
  for (const r of added) {
    byLang.set(r.lang, { ...r, offered: false, installed: true, partial: false, offeredSize: 0 });
  }
  for (const it of offered) {
    const have = byLang.get(it.id);
    if (have) {
      byLang.set(it.id, { ...have, offered: true, installed: it.installed, partial: !!it.partial, offeredSize: it.size, license: it.license });
    } else {
      byLang.set(it.id, {
        lang: it.id, parts: [], size: 0, modified: '', paths: [],
        offered: true, installed: false, partial: false, offeredSize: it.size, license: it.license,
      });
    }
  }
  return [...byLang.values()].sort((a, b) => a.lang.localeCompare(b.lang));
}
