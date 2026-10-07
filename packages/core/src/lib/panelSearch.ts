/**
 * The admin panel's search, as data and rules (task #168, docs/ADMIN-PANEL.md
 * → Search): what a typed query means, which items answer it and in what
 * order they are drawn. `components/PanelSearch.vue` draws it; the host (the
 * admin panel's top bar, web/src/components/AdminSearch.vue) hands over the
 * items and gets the chosen one back.
 *
 * ⚠ It knows nothing about routes, permissions or the server. The host passes
 * only what the reader may open: the menu's own pages and settings (the menu's
 * visibility rule, never a second one), the server's hits (each from a list
 * the reader's role already lets them read) and the file index's hits.
 *
 * The owner's decisions (2026-10-06):
 *   - files are searched only on demand: without a prefix at most the first
 *     three, and a row that turns the search into `file:<words>`;
 *   - a prefix narrows to one kind: `file:`, `user:`, `app:`, `key:`,
 *     `group:`, `storage:`, `setting:`;
 *   - a label answers in the interface's language AND in English, and to its
 *     synonyms ("LDAP" finds Identity providers), with Turkish letters and
 *     case folded (`foldText`, the rule a file name is found by).
 *
 * Pure: no DOM, and Vue only for the type of a host's icon component.
 */
import type { Component } from 'vue';

import { foldText } from './fileFilters';

/** What an item is, and the group it is drawn under. */
export type PanelSearchKind = 'page' | 'setting' | 'app' | 'user' | 'group' | 'key' | 'storage' | 'share' | 'file';

/**
 * The order the groups are drawn in. Fixed rather than by best score: a
 * person learns where to look, and the panel's own places come before the
 * records in it. One exception: a group whose best row is NAMED what was
 * typed comes first (`panelSearchGroups`).
 */
export const PANEL_SEARCH_KINDS: readonly PanelSearchKind[] = [
  'page',
  'setting',
  'app',
  'user',
  'group',
  'key',
  'storage',
  'share',
  'file',
];

/**
 * The prefixes, and the kind each narrows to. English words in every
 * language, like the explorer's `tag:`: a prefix is a command, and one
 * spelling is what a guide and a colleague can say.
 */
export const PANEL_SEARCH_PREFIXES: Readonly<Record<string, PanelSearchKind>> = {
  file: 'file',
  user: 'user',
  app: 'app',
  key: 'key',
  group: 'group',
  storage: 'storage',
  setting: 'setting',
};

/** The prefixes in the order the hint line shows them. */
export const PANEL_SEARCH_PREFIX_ORDER: readonly string[] = ['file', 'user', 'app', 'key', 'group', 'storage', 'setting'];

/** How many rows a group shows without a prefix, and with one. */
export const PANEL_SEARCH_GROUP_CAP = 6;
export const PANEL_SEARCH_FILES_CAP = 3;
export const PANEL_SEARCH_PREFIXED_CAP = 25;

/** One row the search can offer. */
export interface PanelSearchItem {
  /** Unique across every kind (`page:users`, `user:12`, `file:3:/a.txt`). */
  id: string;
  kind: PanelSearchKind;
  /** What the row says, in the interface's language. */
  label: string;
  /** The second line: where it lives, an address, an owner. */
  detail?: string;
  /**
   * Other names it answers to and does not show: its English label, another
   * language's name of an app, a person's username.
   */
  terms?: readonly string[];
  /**
   * Synonyms ("LDAP" for Identity providers): weaker than a name, so a row
   * NAMED what was typed comes before a row that is only LIKE it.
   */
  aliases?: readonly string[];
  /** A glyph from the HOST's icon set, drawn as a component (the menu's own). */
  icon?: Component;
  /** Or a glyph from the core icon library, by name (`lib/actionIcons`). */
  iconName?: string;
  /** A file row's folder-ness: a file row wears the listing's own tile. */
  isDir?: boolean;
  /**
   * Found by a search that already matched it (the server's, the file
   * index's): drawn even when the words are not in what the row SAYS - a
   * file found by its content, a person found by an address the row does not
   * print. Ranked by the same rule as every other row.
   */
  found?: boolean;
  /** Whatever the host needs to go there; handed back untouched. */
  target?: unknown;
}

/** One of a person's recent searches, as the host's store keeps it. */
export interface PanelRecentSearch {
  id: number | string;
  query: string;
}

/**
 * Where the recent searches live - the server, per person (migration 00090):
 * the host wires these to its API. Every call may fail; the panel then shows
 * the list it had.
 */
export interface PanelRecentStore {
  list(): Promise<PanelRecentSearch[]>;
  add(query: string): Promise<void>;
  remove(id: PanelRecentSearch['id']): Promise<void>;
  clear(): Promise<void>;
}

/** A typed query, taken apart. */
export interface PanelSearchQuery {
  /** The kind a prefix narrows to, or null for none. */
  kind: PanelSearchKind | null;
  /** The prefix as a word (`file`), '' for none. */
  prefix: string;
  /** What is searched for, the prefix taken off, trimmed. */
  text: string;
  /** `text` folded and split into words. */
  words: string[];
}

/** One group as it is drawn. */
export interface PanelSearchGroup {
  kind: PanelSearchKind;
  items: PanelSearchItem[];
  /** How many matched before the group was cut to its cap. */
  total: number;
}

/** `text` folded (`foldText`) and split into words. */
export function panelWords(text: string): string[] {
  return foldText(text ?? '')
    .split(/\s+/)
    .filter((w) => w.length > 0);
}

/**
 * The query taken apart. A prefix counts only as one of the known words
 * followed by a colon at the very start (`file:rapor`, `User: ada`); any
 * other `word:` is searched for as it is, so `C:` or `12:30` is not lost.
 */
export function parsePanelQuery(raw: string): PanelSearchQuery {
  const s = (raw ?? '').replace(/^\s+/, '');
  const m = /^([A-Za-z]+)\s*:/.exec(s);
  if (m) {
    const word = m[1].toLowerCase();
    const kind = PANEL_SEARCH_PREFIXES[word];
    if (kind) {
      const text = s.slice(m[0].length).trim();
      return { kind, prefix: word, text, words: panelWords(text) };
    }
  }
  const text = s.trim();
  return { kind: null, prefix: '', text, words: panelWords(text) };
}

/** `raw` with its prefix replaced by `prefix` ('' takes it off). */
export function withPanelPrefix(raw: string, prefix: string): string {
  const q = parsePanelQuery(raw);
  return prefix ? `${prefix}:${q.text}` : q.text;
}

/** Is the character before position `i` a word boundary? */
function startsWord(hay: string, i: number): boolean {
  if (i === 0) return true;
  return !/[\p{L}\p{N}]/u.test(hay[i - 1]);
}

/** How well one folded word answers one folded text, scaled by `weight`. */
function wordScore(hay: string, word: string, weight: number): number {
  if (!hay) return 0;
  const i = hay.indexOf(word);
  if (i < 0) return 0;
  if (i === 0) return weight;
  // A later word of the text: "security" in "Sign-in security".
  if (startsWord(hay, i)) return Math.round(weight * 0.8);
  return Math.round(weight * 0.5);
}

/**
 * How well an item answers the words: -1 for not at all, higher for better.
 *
 * Every word has to be somewhere - the label, a term, an alias or the detail
 * line - and each counts for where it was found: the start of the label most,
 * a term less, an alias less again, the detail line least. The whole query
 * being the label (or a term, or an alias) outright wins over everything, so
 * "Uygulamalar", "apps" and "uygulama" put the Apps page first.
 *
 * A row the server or the file index already matched (`found`) is never -1:
 * what it was found by may not be on the row.
 */
export function panelScore(item: PanelSearchItem, words: readonly string[]): number {
  if (words.length === 0) return 0;
  const label = foldText(item.label ?? '');
  const terms = (item.terms ?? []).map((x) => foldText(x));
  const aliases = (item.aliases ?? []).map((x) => foldText(x));
  const detail = foldText(item.detail ?? '');
  let score = 0;
  for (const w of words) {
    let best = wordScore(label, w, 100);
    for (const term of terms) best = Math.max(best, wordScore(term, w, 70));
    for (const alias of aliases) best = Math.max(best, wordScore(alias, w, 50));
    best = Math.max(best, wordScore(detail, w, 25));
    if (best <= 0) return item.found ? 1 : -1;
    score += best;
  }
  const phrase = words.join(' ');
  if (label === phrase) score += 300;
  else if (terms.includes(phrase)) score += 200;
  else if (aliases.includes(phrase)) score += 150;
  else if (label.startsWith(phrase)) score += 80;
  // A shorter label is the closer match of two that start alike.
  score -= Math.min(label.length, 60) / 10;
  return score;
}

/**
 * Is the whole query this item's name - its label or one of its terms (not
 * a synonym)? "Çöp kutusu saklama" is the setting's name, not a page's.
 */
export function panelExact(item: PanelSearchItem, words: readonly string[]): boolean {
  if (words.length === 0) return false;
  const phrase = words.join(' ');
  if (foldText(item.label ?? '') === phrase) return true;
  return (item.terms ?? []).some((x) => foldText(x) === phrase);
}

/**
 * The groups for a query, each sorted by score and cut to its cap. A group
 * whose best row is NAMED what was typed (`panelExact`) comes first - the
 * setting "Trash retention" before the Trash page that merely mentions
 * retention - and the rest follow in `PANEL_SEARCH_KINDS` order. A prefix
 * keeps its own kind alone. An empty query has no groups (the panel shows the
 * recent searches instead).
 */
export function panelSearchGroups(
  items: readonly PanelSearchItem[],
  query: PanelSearchQuery,
  caps: Partial<Record<PanelSearchKind, number>> = {},
): PanelSearchGroup[] {
  if (query.words.length === 0) return [];
  const byKind = new Map<PanelSearchKind, Array<{ item: PanelSearchItem; score: number }>>();
  for (const item of items) {
    if (query.kind && item.kind !== query.kind) continue;
    const score = panelScore(item, query.words);
    if (score < 0) continue;
    const list = byKind.get(item.kind) ?? [];
    list.push({ item, score });
    byKind.set(item.kind, list);
  }
  const named: PanelSearchGroup[] = [];
  const rest: PanelSearchGroup[] = [];
  for (const kind of PANEL_SEARCH_KINDS) {
    const list = byKind.get(kind);
    if (!list || list.length === 0) continue;
    // Stable: equal scores keep the order the host gave (the menu's order).
    list.sort((a, b) => b.score - a.score);
    const cap = caps[kind] ?? (query.kind ? PANEL_SEARCH_PREFIXED_CAP : kind === 'file' ? PANEL_SEARCH_FILES_CAP : PANEL_SEARCH_GROUP_CAP);
    const group = { kind, items: list.slice(0, cap).map((x) => x.item), total: list.length };
    (panelExact(list[0].item, query.words) ? named : rest).push(group);
  }
  return [...named, ...rest];
}

/** Every row of the groups, in the order they are drawn (the keyboard's order). */
export function panelSearchRows(groups: readonly PanelSearchGroup[]): PanelSearchItem[] {
  return groups.flatMap((g) => g.items);
}
