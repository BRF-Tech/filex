// Telling the operating system which kinds of file filex opens - the kinds
// src/app-kinds.ts gathers from the apps on the person's servers.
//
// What each OS allows decides who says it (measured and read, 2026-10-16):
//
//   Windows (NSIS install)  while the app runs, under the user's own hive:
//       a ProgId of its own (filex.AppDocument), one OpenWithProgids value
//       per extension and the extension in Applications\filex.exe's
//       SupportedTypes, then SHChangeNotify(SHCNE_ASSOCCHANGED). Removed the
//       same way when the kind goes; the uninstaller takes back whatever is
//       left (build/installer.nsh).
//   Linux (.deb, .rpm, AUR, AppImage)  while the app runs, in the user's
//       data folder: a hidden desktop entry naming the media types, and for
//       an extension the system has no type for, a type of filex's own
//       (mime/packages/), then update-mime-database / update-desktop-database.
//   macOS, Microsoft Store, Snap, Flatpak  only at BUILD time: Info.plist's
//       CFBundleDocumentTypes, the MSIX manifest's FileTypeAssociation and the
//       snap's desktop file are fixed in the package (an MSIX's registry
//       writes are virtualised; a strict snap cannot write outside its own
//       folders). Those packages declare every kind the app store's catalog
//       opens (build/app-kinds.json, scripts/app-kinds.mjs), and the app
//       decides at the double-click whether a server opens it.
//
// ⚠ Never the DEFAULT. Every registration here adds filex to "Open with"; it
// never takes a kind from the app that opens it today (see main.ts "becoming
// the default handler"). On a desktop where nothing else opens a kind at all,
// filex is the only app offered for it - which is not taking it from anyone.
//
// ⚠ PURE: the text of every file and command is built here, so it is
// measured without Electron, a registry or a desktop
// (desktop/test/os-assoc.test.ts). main.ts writes and runs them.

import nodePath from 'node:path';

import { KIND_RE, MEDIA_TYPE_RE, OFFICE_EXTENSIONS } from './openwith.ts';
import { NEVER_REGISTERED } from './app-kinds.ts';
import type { StoreChannel } from './channel.ts';

/** Who tells this copy's OS what it opens. */
export type AssocRoute =
  /** The installed Windows app (NSIS): HKCU, while it runs. */
  | 'windows'
  /** A .deb, .rpm or AUR install: an entry in the user's applications folder. */
  | 'linux'
  /** An AppImage: the entry it writes for itself (channel.ts appImageDesktopEntry). */
  | 'linux-appimage'
  /** macOS, the Microsoft Store, a snap, a Flatpak: the package, at build time. */
  | 'build'
  /** A run from source, a test run, the portable .exe: nothing is registered. */
  | 'none';

/**
 * Which route this copy takes.
 *
 * ⚠ The portable .exe registers nothing: its promise is that deleting its
 * folder leaves nothing of it behind (portable.ts), and a registry entry
 * pointing at an .exe on a USB stick is exactly what it must not leave.
 * ⚠ A run from source or a test run neither (main.ts: a suite once pointed
 * the person's filex:// handler at a checkout).
 */
export function assocRouteFor(o: {
  platform: NodeJS.Platform;
  channel: StoreChannel | null;
  packaged: boolean;
  portable: boolean;
  testRun: boolean;
  appImage?: string | null;
}): AssocRoute {
  if (o.testRun || !o.packaged) return 'none';
  if (o.platform === 'darwin') return 'build';
  if (o.platform === 'win32') {
    if (o.channel === 'msstore') return 'build';
    return o.portable ? 'none' : 'windows';
  }
  if (o.platform === 'linux') {
    if (o.channel === 'snap' || o.channel === 'flatpak') return 'build';
    return o.appImage ? 'linux-appimage' : 'linux';
  }
  return 'none';
}

/** Only well-formed kinds reach a registry key or a desktop file. */
function cleanKinds(kinds: readonly string[]): string[] {
  return [...new Set(kinds.map((k) => String(k).toLowerCase()).filter((k) => KIND_RE.test(k)))].sort();
}

// ─────────────────────────── Windows ───────────────────────────

/**
 * The ProgId the kinds from the servers' apps open through. Not the
 * installer's `filex.OfficeDocument`: a ProgId's name is what Explorer shows
 * as a file's type once filex opens it by default, and a .psd is not an
 * "Office document".
 */
export const WIN_APP_PROGID = 'filex.AppDocument';

/** The kinds Windows (and a package's file-type list) can be told: one-part extensions. */
export function windowsKinds(kinds: readonly string[]): string[] {
  return cleanKinds(kinds).filter((k) => !k.includes('.'));
}

/**
 * The extensions a PACKAGE declares beside the office documents its own
 * entry already names (macOS, the Store, the snap - scripts/app-kinds.mjs):
 * one-part, never an office one, never a program (NEVER_REGISTERED).
 */
export function packageKinds(exts: readonly string[]): string[] {
  const office = new Set<string>(OFFICE_EXTENSIONS);
  return windowsKinds(exts).filter((e) => !office.has(e) && !NEVER_REGISTERED.has(e));
}

/** A string as a .reg file writes it, quotes included. */
function regString(s: string): string {
  return '"' + s.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
}

/**
 * The .reg document that registers `add` and takes back `drop` - imported
 * with `reg import` in ONE process (main.ts), rather than two `reg add` per
 * kind.
 *
 *   HKCU\Software\Classes\filex.AppDocument               the ProgId, its icon and open verb
 *   HKCU\Software\Classes\.<ext>\OpenWithProgids          filex.AppDocument = ""   (Explorer's "Open with")
 *   HKCU\Software\Classes\Applications\<exe>\SupportedTypes  .<ext> = ""          ("Choose another app")
 *
 * Both halves are the installer's own (build/installer.nsh filexClaimExt);
 * neither touches the extension's default ProgId. A kind dropped loses both
 * values; with nothing left the ProgId goes too.
 *
 * ⚠ The open verb's "%1" is quoted: a document under "C:\Users\Ada
 * Lovelace\…" would otherwise reach the app as several arguments.
 * ⚠ The file is written UTF-16LE with a byte order mark (windowsRegBytes):
 * "Windows Registry Editor Version 5.00" is read as UTF-16, and a profile
 * path with "ş" in it must survive.
 */
export function windowsAssocReg(o: {
  exe: string;
  exeName: string;
  add: readonly string[];
  drop: readonly string[];
  /** The ProgId's name: what Explorer calls a file filex opens by default. */
  typeName: string;
}): string {
  if (!/^[A-Za-z0-9._ -]{1,64}\.exe$/i.test(o.exeName)) throw new Error('not an executable name: ' + o.exeName);
  // ⚠ One-part extensions only: Windows types a file by its LAST extension,
  // so a `.tar.gz` key would never be read, and a `.gz` one would offer
  // filex for every gzip file.
  const add = windowsKinds(o.add);
  const drop = windowsKinds(o.drop).filter((k) => !add.includes(k));
  const classes = 'HKEY_CURRENT_USER\\Software\\Classes';
  const progid = classes + '\\' + WIN_APP_PROGID;
  const supported = classes + '\\Applications\\' + o.exeName + '\\SupportedTypes';
  const lines: string[] = ['Windows Registry Editor Version 5.00', ''];
  if (add.length) {
    lines.push(`[${progid}]`, `@=${regString(o.typeName)}`, '');
    lines.push(`[${progid}\\DefaultIcon]`, `@=${regString(o.exe + ',0')}`, '');
    lines.push(`[${progid}\\shell\\open\\command]`, `@=${regString('"' + o.exe + '" "%1"')}`, '');
    for (const k of add) lines.push(`[${classes}\\.${k}\\OpenWithProgids]`, `${regString(WIN_APP_PROGID)}=""`, '');
  }
  for (const k of drop) lines.push(`[${classes}\\.${k}\\OpenWithProgids]`, `${regString(WIN_APP_PROGID)}=-`, '');
  if (add.length || drop.length) {
    lines.push(`[${supported}]`);
    for (const k of add) lines.push(`${regString('.' + k)}=""`);
    for (const k of drop) lines.push(`${regString('.' + k)}=-`);
    lines.push('');
  }
  if (!add.length) lines.push(`[-${progid}]`, '');
  return lines.join('\r\n');
}

/** The .reg document's bytes: UTF-16LE with a byte order mark. */
export function windowsRegBytes(doc: string): Buffer {
  return Buffer.concat([Buffer.from([0xff, 0xfe]), Buffer.from(doc, 'utf16le')]);
}

/**
 * The value names `reg query <key>` printed (`    .psd    REG_SZ    `), the
 * way the kinds filex registered are read back: what is really there, not
 * only what the record says - an update's uninstaller clears SupportedTypes,
 * a person may have cleaned the registry by hand.
 */
export function regQueryValueNames(stdout: string): string[] {
  const out: string[] = [];
  for (const line of String(stdout).split(/\r?\n/)) {
    const m = /^ {4}(\S.*?) {4}REG_[A-Z_]+(?: {4}.*)?$/.exec(line);
    if (m && m[1] !== '(Default)') out.push(m[1]);
  }
  return out;
}

/**
 * The extensions filex itself registered, read from what `reg query` said of
 * SupportedTypes: everything but the installer's office documents (`office`),
 * well-formed, without the dot.
 */
export function registeredWindowsKinds(names: readonly string[], office: readonly string[]): string[] {
  const own = new Set(office.map((e) => e.toLowerCase()));
  return windowsKinds(names.map((n) => n.replace(/^\./, '')).filter((n) => !own.has(n.toLowerCase())));
}

/**
 * The PowerShell that tells Explorer associations changed
 * (SHChangeNotify(SHCNE_ASSOCCHANGED)) - what the installer does after its
 * own registration. Without it the new entry shows in "Open with" only after
 * the next sign-in. Handed over as -EncodedCommand: nothing in it is quoted
 * by a command line on the way.
 */
export function windowsAssocChangedArgs(): string[] {
  const script = [
    "$sig = '[DllImport(\"shell32.dll\")] public static extern void SHChangeNotify(int e, uint f, System.IntPtr a, System.IntPtr b);'",
    '$t = Add-Type -MemberDefinition $sig -Name ShellNotify -Namespace FilexAssoc -PassThru',
    '$t::SHChangeNotify(0x08000000, 0, [System.IntPtr]::Zero, [System.IntPtr]::Zero)',
  ].join('; ');
  return ['-NoProfile', '-NonInteractive', '-EncodedCommand', Buffer.from(script, 'utf16le').toString('base64')];
}

// ─────────────────────────── Linux ───────────────────────────

/** The media types a desktop gives one extension, and their weight. */
export interface GlobHit {
  weight: number;
  types: string[];
}

/** The media types filex defines itself start with this. */
export const OWN_TYPE_PREFIX = 'application/x-filex-';

/** filex's own type for an extension no system type covers. */
export function ownMediaType(ext: string): string {
  return OWN_TYPE_PREFIX + ext.toLowerCase();
}

/**
 * The extension globs of one shared-mime-info `globs2` file (`weight:type:glob`
 * per line): extension → the types of its highest weight. A glob that is not
 * a plain `*.<ext>` and a case-sensitive one (`:cs`) are left out, and so
 * are filex's own types - what decides is whether the SYSTEM knows the
 * extension.
 */
export function parseGlobs2(text: string): Map<string, GlobHit> {
  const out = new Map<string, GlobHit>();
  for (const line of String(text).split(/\r?\n/)) {
    if (!line || line.startsWith('#')) continue;
    const [w, type, glob, flags] = line.split(':');
    if (!type || !glob || (flags ?? '').split(',').includes('cs')) continue;
    if (type.startsWith(OWN_TYPE_PREFIX) || !MEDIA_TYPE_RE.test(type)) continue;
    const m = /^\*\.([^*?[\]\\/]+)$/.exec(glob);
    if (!m) continue;
    const ext = m[1]!.toLowerCase();
    const weight = Number(w);
    if (!Number.isFinite(weight)) continue;
    addGlob(out, ext, weight, [type]);
  }
  return out;
}

function addGlob(into: Map<string, GlobHit>, ext: string, weight: number, types: readonly string[]): void {
  const hit = into.get(ext);
  if (!hit || weight > hit.weight) into.set(ext, { weight, types: [...types] });
  else if (weight === hit.weight) for (const t of types) if (!hit.types.includes(t)) hit.types.push(t);
}

/** Several globs2 files (the user's, then every data dir's) as one. */
export function mergeGlobs(maps: ReadonlyArray<Map<string, GlobHit>>): Map<string, GlobHit> {
  const out = new Map<string, GlobHit>();
  for (const m of maps) for (const [ext, hit] of m) addGlob(out, ext, hit.weight, hit.types);
  return out;
}

/**
 * The globs2 files a desktop reads, in the spec's order: $XDG_DATA_HOME
 * (default ~/.local/share), then every $XDG_DATA_DIRS entry (default
 * /usr/local/share:/usr/share).
 */
export function globs2Files(env: Readonly<Record<string, string | undefined>>, home: string): string[] {
  const dataHome = env.XDG_DATA_HOME || nodePath.posix.join(home, '.local', 'share');
  const dirs = (env.XDG_DATA_DIRS || '/usr/local/share:/usr/share').split(':').filter(Boolean);
  return [dataHome, ...dirs].map((d) => nodePath.posix.join(d, 'mime', 'globs2'));
}

/** What a desktop entry says, and the types filex has to define first. */
export interface LinuxTypes {
  /** The entry's MimeType=, sorted. */
  types: string[];
  /** filex's own types (ownMediaType) for the extensions the system has none for. */
  own: Array<{ ext: string; type: string }>;
}

/**
 * What the apps that open an extension say about its media type, by
 * extension: the types their own `applies.mime` names, or null when one of
 * them names none - then nothing narrows that extension (typesOfKind). An
 * extension an app is switched off for is not its.
 */
export function typeHintsOf(apps: ReadonlyArray<{ ext: readonly string[]; mime: readonly string[]; off?: readonly string[] }>): Record<string, string[] | null> {
  const out: Record<string, string[] | null> = {};
  for (const a of apps) {
    for (const ext of a.ext) {
      if (a.off?.includes(ext)) continue;
      if (!a.mime.length) {
        out[ext] = null;
        continue;
      }
      if (out[ext] === null) continue;
      out[ext] = [...new Set([...(out[ext] ?? []), ...a.mime])];
    }
  }
  return out;
}

/**
 * The media types a desktop gives an extension (`types`, the system's - every
 * one of its highest weight), narrowed to the ones the apps that open it name
 * (`hint`, typeHintsOf; a family `font/*` names each of its own). `.otf` is
 * both `font/otf` and an OpenDocument formula template to shared-mime-info:
 * an app that says it opens `font/otf` is registered for fonts alone, and is
 * not offered for a formula template. No hint, or one that names none of the
 * system's types: every type, as the app said nothing that tells them apart.
 */
export function typesOfKind(types: readonly string[], hint: readonly string[] | null | undefined): string[] {
  if (!hint?.length) return [...types];
  const named = types.filter((t) => hint.some((h) => h === t || (h.endsWith('/*') && t.startsWith(h.slice(0, -1)))));
  return named.length ? named : [...types];
}

/**
 * The media types for `kinds` (extensions) and `mediaTypes` (named by an
 * app): the system's own for an extension it knows (`known`, parseGlobs2 -
 * every type of its highest weight, as the file manager may pick any of
 * them, narrowed by what the apps say they open: `hints`, typesOfKind),
 * otherwise filex's own; less the ones `declared` already (the package's own
 * entry says the office documents and the filex:// link).
 *
 * ⚠ filex's own type is defined only where the SYSTEM has none for the
 * extension, and with a glob weight below the system's (linuxMimePackage):
 * a second opinion about what an extension is would move files the system
 * already types away from the apps that open them.
 */
export function linuxTypesFor(
  kinds: readonly string[],
  mediaTypes: readonly string[],
  known: ReadonlyMap<string, GlobHit>,
  declared: ReadonlySet<string> = new Set(),
  hints: Readonly<Record<string, string[] | null>> = {},
): LinuxTypes {
  const types = new Set<string>();
  const own: Array<{ ext: string; type: string }> = [];
  for (const ext of cleanKinds(kinds)) {
    const hit = known.get(ext);
    if (hit?.types.length) {
      for (const t of typesOfKind(hit.types, hints[ext])) types.add(t);
    } else {
      const t = ownMediaType(ext);
      own.push({ ext, type: t });
      types.add(t);
    }
  }
  for (const m of mediaTypes) {
    const t = String(m).toLowerCase();
    if (MEDIA_TYPE_RE.test(t) && !t.endsWith('/*')) types.add(t);
  }
  for (const d of declared) types.delete(d);
  return { types: [...types].sort(), own };
}

/** The glob weight of filex's own types: below shared-mime-info's 50. */
export const OWN_GLOB_WEIGHT = 10;

/**
 * The shared-mime-info package that defines filex's own types
 * (`$XDG_DATA_HOME/mime/packages/<name>.xml`), or null when there are none
 * and the file should not exist.
 */
export function linuxMimePackage(own: ReadonlyArray<{ ext: string; type: string }>): string | null {
  const rows = own.filter((o) => KIND_RE.test(o.ext) && MEDIA_TYPE_RE.test(o.type));
  if (!rows.length) return null;
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<!-- Written by filex: kinds of file the apps on your filex servers open that this system had no type for. filex rewrites it, and removes it when no app opens them. -->',
    '<mime-info xmlns="http://www.freedesktop.org/standards/shared-mime-info">',
    ...rows.flatMap((o) => [
      `  <mime-type type="${o.type}">`,
      `    <comment>${o.ext.toUpperCase()} file</comment>`,
      `    <glob pattern="*.${o.ext}" weight="${OWN_GLOB_WEIGHT}"/>`,
      '  </mime-type>',
    ]),
    '</mime-info>',
    '',
  ].join('\n');
}
