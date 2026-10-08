/**
 * serverRules — the rules the server publishes for the clients to look things
 * up in (`/api/files/capabilities` → `edit_kinds`, `limits`), held once for
 * every surface on the page (filex #211).
 *
 * ⚠⚠ The server's work is done on the server. Whether a file is an office
 * document or text a person edits, how long a tag or a comment may be, how
 * much an app may keep: each of these was a list or a number written out a
 * second time here, and each copy had drifted or could drift the first time
 * one side changed. The preview offered Edit on `.graphql`, which save-text
 * refused; `.properties` and `Makefile`, which save-text saves, had no Edit;
 * `.docm` was not an office document to the explorer; a tag's 64 counted
 * UTF-16 units where the server counts characters. Now the server decides
 * (backend internal/editkind, capabilities_rules.go) and this module only
 * holds its answer.
 *
 * Fed by whoever fetches the capabilities (core `useFileApi.capabilities`,
 * the admin app's store), so a component reads it without being handed it.
 * Until an answer has arrived nothing is office and nothing is text — the
 * same as a server that says nothing, which offers no Edit rather than a
 * guess.
 */
import { shallowRef } from 'vue';

/** `capabilities.edit_kinds` (backend editkind.Kinds). */
export interface EditKinds {
  /** Extensions the document server opens and edits. */
  office: string[];
  /** Extensions the built-in editor opens and save-text saves. */
  text: string[];
  /** Whole file names (lower case) that are text by name — `makefile`, `.gitignore`. */
  text_names: string[];
  /** Media types that make a file whose name says nothing text: `text/`… */
  text_mime_prefixes: string[];
  /** …and these exactly. */
  text_mimes: string[];
}

/** `capabilities.limits`: the numbers an input is held to before it is sent.
 *  The server enforces each again. */
export interface ServerLimits {
  /** A tag name, in characters. */
  tag_max_runes?: number;
  /** A comment, in characters. */
  comment_max_runes?: number;
  /** An encryption request's reason, in characters (the rest is cut). */
  e2e_request_reason_max_runes?: number;
  /** What one app's interface keeps for a person, as JSON bytes. */
  app_state_max_bytes?: number;
  /** One chunk of an app interface's save, in bytes. */
  app_ui_save_chunk_bytes?: number;
}

/**
 * `capabilities.event_off[event]` (backend capabilities_rules.go, #211 audit
 * B16): a notification event that cannot happen on this instance - what it
 * waits for, whether THIS caller could switch that on, and the sentence that
 * says so, in the reader's language. An event absent from the map can happen.
 */
export interface EventOff {
  /** `antivirus` | `escrow` | `app_plugins` | `e2e_approval`. */
  reason: string;
  /** The caller could switch it on: greyed with `text` for them, not offered
   *  to anybody else (lib/serviceGate `gateOnService`). */
  fixable: boolean;
  text: string;
}

interface Lookup {
  office: ReadonlySet<string>;
  text: ReadonlySet<string>;
  names: ReadonlySet<string>;
  prefixes: readonly string[];
  mimes: ReadonlySet<string>;
}

const lower = (xs: unknown): string[] =>
  Array.isArray(xs) ? xs.filter((x): x is string => typeof x === 'string').map((x) => x.toLowerCase()) : [];

const kinds = shallowRef<Lookup | null>(null);
const limits = shallowRef<ServerLimits>({});

/**
 * Take what a capabilities answer says. A field the answer leaves out keeps
 * what is held (an answer from an older server, or a partial host stub, does
 * not wipe a real one).
 */
export function takeServerRules(caps: { edit_kinds?: Partial<EditKinds> | null; limits?: ServerLimits | null } | null | undefined): void {
  if (!caps) return;
  const k = caps.edit_kinds;
  if (k && typeof k === 'object') {
    kinds.value = {
      office: new Set(lower(k.office)),
      text: new Set(lower(k.text)),
      names: new Set(lower(k.text_names)),
      prefixes: lower(k.text_mime_prefixes),
      mimes: new Set(lower(k.text_mimes)),
    };
  }
  const l = caps.limits;
  if (l && typeof l === 'object') limits.value = { ...l };
}

/** Has the server said how files are edited yet? */
export function editKindsKnown(): boolean {
  return kinds.value !== null;
}

/** Does a file of this extension open in the document server? */
export function isOfficeExt(ext: string | null | undefined): boolean {
  return kinds.value?.office.has(String(ext ?? '').toLowerCase()) ?? false;
}

/** Is this media type text a person edits as text (a name that says nothing,
 *  `LICENSE`, #56)? */
export function isTextualMime(mime: string | null | undefined): boolean {
  const k = kinds.value;
  if (!k) return false;
  const m = String(mime ?? '').split(';')[0].trim().toLowerCase();
  if (!m) return false;
  return k.mimes.has(m) || k.prefixes.some((p) => m.startsWith(p));
}

/** The last path segment, lower case. */
function baseOf(name: string): string {
  const parts = name.replace(/[\\/]+$/, '').split(/[\\/]/);
  return (parts[parts.length - 1] ?? '').toLowerCase();
}

/** The extension after the last dot, the way the server reads it (Go's
 *  `path.Ext`): `.gitignore` has the extension `gitignore`. */
function extOfName(base: string): string {
  const dot = base.lastIndexOf('.');
  return dot >= 0 ? base.slice(dot + 1) : '';
}

/**
 * Is this file one the built-in editor opens and save-text saves? By its
 * name (extension, or a name that is text on its own), else by the server's
 * mime for its bytes — the two halves of save-text's own check.
 */
export function isTextEditable(file: { basename?: string | null; extension?: string | null; mime_type?: string | null } | null | undefined): boolean {
  const k = kinds.value;
  if (!k || !file) return false;
  const base = baseOf(String(file.basename ?? ''));
  const ext = String(file.extension ?? extOfName(base)).toLowerCase();
  if (ext && k.text.has(ext)) return true;
  if (base && k.names.has(base)) return true;
  return isTextualMime(file.mime_type);
}

/** A number the server holds an input to; undefined when it has not said. */
export function serverLimit(name: keyof ServerLimits): number | undefined {
  const v = limits.value[name];
  return typeof v === 'number' && v > 0 ? v : undefined;
}

/** How many characters (Unicode code points, as the server counts them). */
export function runeCount(s: string): number {
  return Array.from(s).length;
}

/** `s` cut to `max` characters, never inside a surrogate pair. */
export function clipRunes(s: string, max: number | undefined): string {
  if (!max) return s;
  const chars = Array.from(s);
  return chars.length > max ? chars.slice(0, max).join('') : s;
}

/** Tests only: forget what the server said. */
export function resetServerRules(): void {
  kinds.value = null;
  limits.value = {};
}
