// The kinds of file "Open with filex" takes, gathered over every server this
// computer is signed in to - and which of them opens a given file.
//
// The owner, 2026-10-16: filex on a computer opens a document from its disk
// when one of the person's servers can - an office document where a server
// has the Office editor app or a Document Server, a .psd where a server has
// an app that opens PSD files. So the list the operating system is told is
// not a list the desktop keeps: it is what the apps on those servers open
// (openwith.ts serverAppsFrom, read by openwith-io.ts readServerApps), kept
// here per account and handed to os-assoc.ts, which tells the OS. A server
// that installs an app widens it, one that removes it narrows it, an account
// signed out or removed takes its kinds with it.
//
// ⚠ PURE (and fs-only for the record on disk), like openwith.ts: no Electron
// import, so node:test drives it (desktop/test/app-kinds.test.ts).

import fs from 'node:fs';
import nodePath from 'node:path';

import {
  OFFICE_EXTENSIONS,
  OFFICE_MIME_TYPES,
  extensionOf,
  serverOpener,
  type ServerApps,
  type ServerOpener,
} from './openwith.ts';

/** What one account's server opens, as last read. */
export interface ServerRecord extends ServerApps {
  accountId: string;
  serverUrl: string;
  /** When it was read (Date.now()). */
  readAt: number;
}

/**
 * Kinds never handed to the operating system, whatever an app says it opens:
 * programs, shortcuts, installers and scripts the shell runs. Adding "Open
 * with filex" to one of them takes nothing away, but a server - any server
 * this computer signs in to - has no business putting an entry beside the
 * way Windows or a desktop starts a program. The file still opens from filex
 * itself; only the OS is not told.
 */
export const NEVER_REGISTERED: ReadonlySet<string> = new Set([
  'exe', 'com', 'bat', 'cmd', 'scr', 'pif', 'cpl', 'msc', 'msi', 'msp', 'msu', 'lnk', 'url', 'reg', 'inf',
  'ps1', 'psm1', 'vbs', 'vbe', 'jse', 'wsf', 'wsh', 'hta', 'dll', 'sys', 'drv', 'ocx', 'jar',
  'appx', 'appxbundle', 'msix', 'msixbundle', 'appinstaller', 'application', 'appref-ms',
  'desktop', 'appimage', 'deb', 'rpm', 'snap', 'flatpak', 'flatpakref', 'run',
  'app', 'dmg', 'pkg', 'command', 'workflow', 'kext',
]);

/** At most this many kinds are handed to the OS, however many servers name. */
export const MAX_REGISTERED = 256;

const OFFICE_SET: ReadonlySet<string> = new Set<string>(OFFICE_EXTENSIONS);
const OFFICE_MEDIA: ReadonlySet<string> = new Set(Object.values(OFFICE_MIME_TYPES));

/**
 * The extensions the OS is told filex opens, BESIDE the office documents the
 * installers declare (OFFICE_EXTENSIONS): every one an app on any of the
 * servers opens and was not switched off for, sorted, never a program
 * (NEVER_REGISTERED), at most MAX_REGISTERED.
 */
export function registrableKinds(records: readonly ServerRecord[]): string[] {
  const out = new Set<string>();
  for (const r of records) {
    for (const a of r.apps) {
      for (const e of a.ext) {
        if (OFFICE_SET.has(e) || NEVER_REGISTERED.has(e) || a.off.includes(e)) continue;
        // A program by its last part too: `setup.exe` as a two-part kind.
        if (NEVER_REGISTERED.has(e.slice(e.lastIndexOf('.') + 1))) continue;
        out.add(e);
      }
    }
  }
  return [...out].sort().slice(0, MAX_REGISTERED);
}

/**
 * The media types an app names exactly (`image/vnd.adobe.photoshop`, not a
 * family like `image/*`), beside its extensions: only a Linux desktop entry
 * can say those. A family is never registered - it would put filex under
 * "Open with" for every image. The office ones are declared by the packages.
 */
export function registrableMediaTypes(records: readonly ServerRecord[]): string[] {
  const out = new Set<string>();
  for (const r of records) {
    for (const a of r.apps) {
      for (const m of a.mime) {
        if (m.endsWith('/*') || OFFICE_MEDIA.has(m) || m.startsWith('x-scheme-handler/')) continue;
        out.add(m);
      }
    }
  }
  return [...out].sort().slice(0, MAX_REGISTERED);
}

/** One server that opens a file, and how. */
export interface Candidate extends ServerOpener {
  accountId: string;
}

/**
 * The servers that open `fileName`, one entry each, in the order the
 * accounts are listed (`accountOrder`; a record of an account no longer
 * there is left out). Each server opens it its own way (openwith.ts
 * serverOpener): its Document Server, or its first app for the kind.
 */
export function candidatesFor(
  fileName: string,
  records: readonly ServerRecord[],
  accountOrder: readonly string[],
): Candidate[] {
  const out: Candidate[] = [];
  for (const id of accountOrder) {
    const r = records.find((x) => x.accountId === id);
    if (!r) continue;
    const o = serverOpener(r, fileName);
    if (o) out.push({ accountId: id, ...o });
  }
  return out;
}

/** A choice the person asked filex to remember for a kind. */
export interface RememberedChoice {
  accountId: string;
  app: string | null;
}

export type CandidatePick =
  /** No server opens it. */
  | { kind: 'none' }
  /** One does, or the person's remembered choice still does. */
  | { kind: 'one'; candidate: Candidate; remembered: boolean }
  /** More than one does: the person is asked. */
  | { kind: 'ask'; candidates: Candidate[] };

/**
 * Which server opens a file: the one there is; the remembered choice while
 * that server still opens the kind the same way; otherwise the person is
 * asked. A remembered choice that no longer holds (the app was removed, the
 * account signed out) is not followed - the person is asked again.
 */
export function pickCandidate(candidates: readonly Candidate[], remembered?: RememberedChoice | null): CandidatePick {
  if (!candidates.length) return { kind: 'none' };
  if (remembered) {
    const hit = candidates.find((c) => c.accountId === remembered.accountId && c.app === remembered.app);
    if (hit) return { kind: 'one', candidate: hit, remembered: true };
  }
  if (candidates.length === 1) return { kind: 'one', candidate: candidates[0]!, remembered: false };
  return { kind: 'ask', candidates: [...candidates] };
}

/** The kind remembered choices are filed under: the file's extension. */
export function choiceKey(fileName: string): string {
  return extensionOf(fileName);
}

/** A label in `locale`, else English, else any. */
export function labelIn(label: Readonly<Record<string, string>> | undefined, locale: string): string {
  if (!label) return '';
  return label[locale] ?? label.en ?? Object.values(label)[0] ?? '';
}

/** One kind as Settings lists it: the extension and who opens it. */
export interface KindRow {
  ext: string;
  openers: Array<{ accountId: string; plugin: string; view: string; label: Record<string, string> }>;
}

/** Every kind the servers' apps open, office documents included, for Settings. */
export function kindRows(records: readonly ServerRecord[]): KindRow[] {
  const rows = new Map<string, KindRow>();
  for (const r of records) {
    for (const a of r.apps) {
      for (const e of a.ext) {
        if (a.off.includes(e)) continue;
        const row = rows.get(e) ?? { ext: e, openers: [] };
        row.openers.push({ accountId: r.accountId, plugin: a.plugin, view: a.view, label: a.label });
        rows.set(e, row);
      }
    }
  }
  return [...rows.values()].sort((x, y) => (x.ext < y.ext ? -1 : x.ext > y.ext ? 1 : 0));
}

/**
 * The records to keep after a round of reading: the new answer for every
 * server that gave one, the last one for a server that could not be asked
 * (`kept`: its account is still here and signed in), none for an account
 * that is gone or signed out.
 */
export function mergeRecords(
  previous: readonly ServerRecord[],
  fresh: readonly ServerRecord[],
  live: ReadonlySet<string>,
): ServerRecord[] {
  const out: ServerRecord[] = [];
  for (const id of live) {
    const r = fresh.find((x) => x.accountId === id) ?? previous.find((x) => x.accountId === id);
    if (r) out.push(r);
  }
  return out;
}

// ─────────────────────────── the record on disk ───────────────────────────

/** What the OS was told, by whom (os-assoc.ts AssocRoute). */
export interface RegisteredKinds {
  route: string;
  /** Extensions (Windows, Linux). */
  kinds: string[];
  /** Media types written to a Linux desktop entry. */
  types: string[];
  /** Media types filex defined itself, for extensions the system had none for (Linux). */
  own: string[];
  /** The media types the apps named themselves, as they were when written (Linux). */
  media?: string[];
}

export interface AppKindsFile {
  version: 1;
  servers: ServerRecord[];
  choices: Record<string, RememberedChoice>;
  registered: RegisteredKinds | null;
}

export function emptyAppKinds(): AppKindsFile {
  return { version: 1, servers: [], choices: {}, registered: null };
}

/**
 * The record of what the servers open, the choices the person asked filex to
 * remember and what the OS was told - one small JSON file in the app's data
 * folder. Not a secret (kinds and app names); the token never goes in it.
 *
 * ⚠ Kept so that a kind is taken back from the OS even when the app learns
 * of it at its next start (an account removed while it was closed), and so
 * a double-click at a cold start, before any server answered, already knows
 * which server opens the kind.
 */
export class AppKindsStore {
  // A plain field, not a constructor parameter property (see SessionStore).
  private readonly file: string;

  constructor(file: string) {
    this.file = file;
  }

  load(): AppKindsFile {
    try {
      const j = JSON.parse(fs.readFileSync(this.file, 'utf8')) as Partial<AppKindsFile>;
      if (j?.version !== 1) return emptyAppKinds();
      return {
        version: 1,
        servers: Array.isArray(j.servers) ? j.servers.filter((s) => s && typeof s.accountId === 'string' && Array.isArray(s.apps)) : [],
        choices: j.choices && typeof j.choices === 'object' ? j.choices : {},
        registered: j.registered && Array.isArray(j.registered.kinds) ? j.registered : null,
      };
    } catch {
      return emptyAppKinds();
    }
  }

  save(f: AppKindsFile): void {
    fs.mkdirSync(nodePath.dirname(this.file), { recursive: true });
    // Temp + rename: a record cut short by a crash would forget what the OS
    // was told, and that is what removing a kind again needs.
    const tmp = this.file + '.part';
    fs.writeFileSync(tmp, JSON.stringify(f, null, 2));
    fs.renameSync(tmp, this.file);
  }
}
