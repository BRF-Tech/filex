// Types for hostile-names.mjs (the one list of hostile file names, task #238).

export interface HostileName {
  readonly id: string;
  readonly name: string;
  readonly why: string;
  readonly tags: readonly string[];
  readonly refused: boolean;
  readonly windowsInvalid: boolean;
  readonly twin: string;
  readonly twinKind: '' | 'case' | 'nfc-nfd';
}

export interface HostilePath {
  readonly id: string;
  readonly path: string;
  readonly depth: number;
  readonly leaf: string;
  readonly why: string;
}

export const HOSTILE_NAMES_FILE: string;
export const HOSTILE_NAMES: readonly HostileName[];
export const HOSTILE_PATHS: readonly HostilePath[];
export const CREATABLE_NAMES: readonly HostileName[];
export const REFUSED_NAMES: readonly HostileName[];
export function hostileName(id: string): HostileName;
export function hostileNamesWithout(names: readonly HostileName[], ...tags: string[]): HostileName[];
export function utf8Bytes(s: string): number;
