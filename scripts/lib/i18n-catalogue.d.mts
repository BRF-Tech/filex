// Types for i18n-catalogue.mjs (read by web/vite.config.ts and web tests).
export type Table = 'explorer' | 'admin' | 'both' | 'server';
export interface Catalogue {
  explorer: Record<string, string>;
  explorerTr: Record<string, string>;
  admin: Record<string, string>;
  adminTr: Record<string, string>;
  server: Record<string, string>;
  serverTr: Record<string, string>;
}
export interface KeyContext {
  in: Table;
  syntax: 'plain' | 'vue-i18n' | 'shared' | 'server';
  tr?: string;
  where?: string[];
  about?: string;
  vars?: Record<string, string>;
  plural?: boolean;
}
export interface ServerNotes {
  groups: Record<string, string>;
  keys: Record<string, string>;
  vars: Record<string, string>;
  key_vars: Record<string, Record<string, string>>;
}
export declare function loadCoreTable(file: string): Record<string, string>;
export declare function flatten(obj: object, prefix?: string, out?: Record<string, string>): Record<string, string>;
export declare function loadAdminTable(file: string): Record<string, string>;
export declare function loadNotifyTables(file: string): { en: Record<string, string>; tr: Record<string, string> };
export declare function notifyWords(file: string): Record<'en' | 'tr', Record<string, string>>;
export declare function loadCatalogue(root: string): Catalogue;
export declare function loadServerNotes(root: string): ServerNotes;
export declare function serverNote(
  notes: ServerNotes,
  key: string,
  english: string,
  isKey?: (k: string) => boolean,
): { about: string; vars: Record<string, string> };
export declare function tableOf(cat: Catalogue, key: string): Table | '';
export declare const SYNTAX_OF: Record<Table, KeyContext['syntax']>;
export declare const CLDR_ORDER: string[];
export declare const COUNT_VARS: string[];
export declare const COMMON_LANGUAGES: string[];
export declare function plainTokens(text: string): string[];
export declare function pluralBaseOf(key: string, has: (k: string) => boolean): string | undefined;
export declare function pluralCategories(lang: string): string[];
export declare function isPluralKey(cat: Catalogue, key: string): boolean;
export declare function whereKeysAppear(root: string, keys: string[]): Map<string, Set<string>>;
export declare function buildVersion(root: string): string;
export declare function buildCatalogue(
  root: string,
  opts?: { where?: boolean; langs?: string[] },
): {
  strings: Record<string, string>;
  context: {
    filex: string;
    about: string;
    plural_categories: Record<string, string[]>;
    keys: Record<string, KeyContext>;
  };
  cat: Catalogue;
};
export declare function catalogueFiles(root: string, opts?: { where?: boolean; langs?: string[] }): Record<string, string>;
