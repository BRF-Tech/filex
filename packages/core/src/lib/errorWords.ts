/**
 * errorWords — what a FAILURE says to a person. One place, every screen.
 *
 * ⚠⚠ The rule (owner, 2026-09-21, after the QA sweep): a failure is said as a
 * translated sentence that says what happened and, where it applies, what to
 * do. An administrator may see the technical detail as a SECOND line. A
 * regular user never sees an environment variable, a status code or a JSON
 * body. The sweep found all three on screen: "Config fetch 503: {…}", "set
 * FILEX_SECRET_KEY to issue S3 access keys" in a regular user's panel,
 * "save failed: 500 {…}" under an editor, "engine libreoffice is not
 * installed on this host" in the operations centre.
 *
 * So:
 *   - `requestFailure` turns an HTTP refusal into an Error whose `message` is
 *     already the sentence, with `status`, the refusal's `code` and the raw
 *     body (`detail`) riding along for whoever may see them. useFileApi's
 *     `jsonFetch` / `fetchBlob` / uploads and every component that calls
 *     `fetch` itself go through it — one table of codes, not one per screen.
 *   - `sayFailure` turns ANY caught failure into `{ text, detail? }`: the
 *     sentence for everybody, the raw words only when the caller may
 *     administer the instance (`caller_admin`).
 *   - `jobFailure` is the operations centre's case: the server classifies a
 *     failed app job (`error_code`), and an app's own words are shown as
 *     they are unless they look like plumbing (`looksTechnical`).
 */
import { foreignText } from './direction';
import { localeTable } from './uiLocales';

type Vars = Record<string, string | number>;

/**
 * A translator.
 *
 * ⚠⚠ `foreign` is how a failure carries its reader's DIRECTION. Half of what
 * this file puts on a screen is text filex did not write — the server's own
 * sentence, an app's own words — and none of it passes through
 * `useLocale().t` or the admin panel's post-translation hook, so in Arabic
 * none of it was isolated: a path, a URL or a token syntax inside the
 * sentence lost its neutral characters to the paragraph's direction
 * (lib/direction `foreignText`, measured on `server.token.scope_unknown`).
 * Threading a locale code through five signatures would have been five
 * chances to forget it; the translator already travels with every call, so it
 * carries the answer. A `t` that does not know (a test stub, a literal
 * fallback) simply passes the text through.
 */
export interface T {
  (key: string, vars?: Vars): string;
  /** Machine runs inside this language's sentences, isolated (RTL only). */
  foreign?: (text: string) => string;
}

/** Say `text` — which filex did not write — the way `t`'s language needs. */
function foreignWith(t: T | undefined, text: string): string {
  return typeof t?.foreign === 'function' ? t.foreign(text) : text;
}

/** A translator for a bare locale code — for callers with no `useLocale`. */
export function wordsIn(locale: string | undefined): T {
  const t: T = (key, vars = {}) => {
    const raw = localeTable(locale)[key] ?? key;
    const said = Object.entries(vars).reduce((acc, [k, v]) => acc.replaceAll(`{${k}}`, String(v)), raw);
    // ⚠ Isolated like `useLocale().t`'s output: this path never reached
    // `render()`, so an engine name or a size pair in a failure's sentence was
    // drawn raw in an Arabic panel while the same words elsewhere were right.
    return foreignText(locale, said);
  };
  t.foreign = (text: string) => foreignText(locale, text);
  return t;
}

/** An HTTP refusal, said. `message` is the sentence; the rest is for code
 *  that branches on it and for an administrator's second line. */
export interface RequestFailure extends Error {
  status?: number;
  /** The refusal's own code (`{"error":"read_only"}` → `read_only`), or ''. */
  code?: string;
  /** The raw body, clipped — NEVER shown to a regular user. */
  detail?: string;
  /** Marks an Error whose `message` is already the sentence. */
  said?: true;
  /** `admin_hint` — the fix, which the SERVER sends only to a caller who can
   *  act on it (handlers/tenantown.go `callerMayConfigureInstance`). */
  hint?: string;
}

const STATUS_KEYS: Record<number, string> = {
  400: 'err.status.400',
  401: 'err.status.401',
  403: 'err.status.403',
  404: 'err.status.404',
  409: 'err.status.409',
  413: 'err.status.413',
  415: 'err.status.415',
  422: 'err.status.422',
  429: 'err.status.429',
  500: 'err.status.500',
  501: 'err.status.501',
  /* ⚠ 502 and 504 are what an APP answers with (handlers/app_plugins.go
     callFail: any plugin error, and a plugin that ran out of time). */
  502: 'err.status.502',
  503: 'err.status.503',
  504: 'err.status.504',
};

/** A status's words. ⚠ An unlisted status is NOT printed as "Error (418)":
 *  a number is not a sentence, and it is the one thing this file exists to
 *  keep off the screen. */
export function statusWords(status: number, locale: string | undefined): string {
  return statusWordsWith(status, wordsIn(locale));
}

function statusWordsWith(status: number, t: T): string {
  return t(STATUS_KEYS[status] ?? (status >= 500 ? 'err.status.500' : 'err.status.other'));
}

/**
 * Whether a status's own words tell a person something the caller's words
 * would not: a refusal they can act on (401 sign in, 403 not allowed, 404
 * gone, 409 conflict, 413 too large, 429 slow down…). A 5xx, or a status with
 * no words of its own, says only "it failed" — the caller's sentence for what
 * it was doing ("Your changes could not be saved") says more.
 */
export function statusIsTelling(status: number): boolean {
  return status >= 400 && status < 500 && status in STATUS_KEYS;
}

/**
 * Refusals a person can act on, by the code or words the server answers with.
 *
 * ⚠⚠ A read-only storage answers in two shapes — `409 {"error":"read_only"}`
 * from the app and public-API paths, `403 {"error":"storage is read-only"}`
 * from the manager's mutating verbs — and by status alone they read "Already
 * exists / conflict" and "You are not allowed to do this" (QA, 2026-09-21).
 * `no_secret_key` is the S3/SSH key endpoints on an install with no
 * encryption key: its server message names an environment variable, which a
 * regular user must never be shown.
 */
const CODE_WORDS: ReadonlyArray<readonly [RegExp, string]> = [
  [/^read_only$|read-only/i, 'err.read_only'],
  [/^no_secret_key$/, 'err.no_secret_key'],
  [/^quota_exceeded$|quota exceeded|quota: exceeded/i, 'err.quota'],
];

function fieldsOf(body: string): Record<string, unknown> {
  try {
    const parsed = JSON.parse(body) as unknown;
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? (parsed as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

/** The `error` field of a JSON refusal body, or ''. */
export function refusalCode(body: string): string {
  const v = fieldsOf(body).error;
  return typeof v === 'string' ? v : '';
}

/** The words for a refusal: what the server SAID when a person can act on it,
 *  the status's words otherwise. */
export function refusalWords(status: number, body: string, locale: string | undefined): string {
  return refusalWordsWith(status, body, wordsIn(locale));
}

function refusalWordsWith(status: number, body: string, t: T): string {
  const code = refusalCode(body);
  for (const [re, key] of CODE_WORDS) if (code && re.test(code)) return t(key);
  return statusWordsWith(status, t);
}

/** An HTTP refusal as an Error carrying its own sentence. */
export function requestFailure(status: number, body: string, locale: string | undefined): RequestFailure {
  const err = new Error(refusalWords(status, body, locale)) as RequestFailure;
  err.status = status;
  err.code = refusalCode(body);
  err.detail = body.slice(0, 300);
  err.said = true;
  const hint = fieldsOf(body).admin_hint;
  if (typeof hint === 'string' && hint) err.hint = hint;
  return err;
}

/**
 * What to print for a refusal on a screen that used to print the server's
 * `error` field as it came (the connection panels: keys, tokens, exports).
 *
 * A known code is said in the person's language (`CODE_WORDS`); a server
 * sentence written for a person ("public key is not a valid OpenSSH key") is
 * kept, because the status's words would lose what went wrong; plumbing and
 * bare codes fall back to the status's words. ⚠ This replaced five copies of
 * one `messageOf` that printed the field whatever it held — including an
 * environment variable to a regular user.
 */
export function serverWords(err: unknown, locale?: string): string {
  // ⚠ `locale` is the reader's: what comes back may be the SERVER's sentence,
  // which never passed through a translator, and an Arabic panel needs its
  // machine runs isolated (lib/direction `foreignText`).
  const say = (text: string): string => foreignText(locale, text);
  const e = err as Partial<RequestFailure> | null | undefined;
  if (!e || e.said !== true) return say(e instanceof Error ? e.message : err ? String(err) : '');
  const code = e.code ?? '';
  if (code && CODE_WORDS.some(([re]) => re.test(code))) return say(e.message ?? '');
  const f = fieldsOf(e.detail ?? '');
  const own = typeof f.message === 'string' && f.message ? f.message : code;
  if (own && !looksTechnical(own) && !/^[a-z0-9_]+$/.test(own)) return say(own);
  return say(e.message ?? '');
}

/** The request never got an answer (offline, DNS, CORS, a dropped socket). */
export function networkFailure(locale: string | undefined, cause?: unknown): RequestFailure {
  const err = new Error(wordsIn(locale)('err.network')) as RequestFailure;
  err.detail = cause instanceof Error ? cause.message : cause ? String(cause) : '';
  err.said = true;
  return err;
}

/**
 * True for text a person should not be shown as the sentence: a JSON body, a
 * status line, an environment variable, a Go error chain, a stack trace, a
 * file path on the server.
 *
 * ⚠ It guards words that came from somewhere we do not control — an app's
 * own error text above all. A real sentence ("Bu belge zaten imzalanmış")
 * passes; plumbing does not. It is deliberately greedy: a human sentence
 * mistaken for plumbing costs the person the app's wording (they still read
 * a sentence), plumbing mistaken for a sentence is the defect.
 */
export function looksTechnical(s: string): boolean {
  const v = s.trim();
  if (!v) return false;
  return (
    /^[[{]/.test(v) || // a JSON body
    /\b[1-5]\d\d\b\s+[A-Z][a-z]+/.test(v) || // "500 Internal Server Error"
    /\b[A-Z][A-Z0-9]+_[A-Z0-9_]+\b/.test(v) || // FILEX_SECRET_KEY
    /\b(panic|goroutine|stack trace|Traceback|at [\w$.]+ \()/i.test(v) ||
    /^[\w.-]+: [\w.-]+(: |$)/.test(v) || // "protocolauth: no secret key…"
    /(^|\s)\/(?:[\w.-]+\/){2,}/.test(v) || // /var/lib/filex/…
    /\b[a-z][\w.-]*: [^:()]{1,60}: [a-z][\w.-]*: /i.test(v) || // a Go error chain "a: b: c: d"
    /\b(ECONNREFUSED|ETIMEDOUT|EOF|nil pointer|dial tcp|x509|context deadline exceeded)\b/i.test(v)
  );
}

/** `"<sentence> (<plumbing>)"` → `[sentence, plumbing]`; anything else → `[raw, '']`. */
export function splitTechnicalTail(raw: string): [string, string] {
  const m = /^(.*\S)\s*\(([^()]*)\)\s*$/.exec(raw);
  if (m && looksTechnical(m[2])) return [m[1], m[2]];
  return [raw, ''];
}

/** A failure, said: the sentence for everybody, the raw words for an admin. */
export interface SaidFailure {
  text: string;
  /** Present only when the caller may administer the instance. */
  detail?: string;
}

/**
 * Say a caught failure.
 *
 * A `RequestFailure` already carries its sentence (`message`); anything else —
 * a thrown JS error, a library's message — is said as `fallback`, because its
 * text was written for a developer. The raw words go to the console as well,
 * where the person debugging looks for them.
 */
export function sayFailure(
  err: unknown,
  fallback: string,
  opts: {
    callerAdmin?: boolean;
    /** The caller's own translator. A viewer knows `t` but not the locale
     *  code a refusal was built with, so the refusal is said again in its
     *  words rather than in whatever language the request was made in. */
    t?: T;
  } = {},
): SaidFailure {
  const e = err as Partial<RequestFailure> | null | undefined;
  const said = e?.said === true;
  const raw = said ? (e?.detail ?? '') : e instanceof Error ? e.message : err ? String(err) : '';
  const text = !said
    ? fallback
    : opts.t && typeof e?.status === 'number'
      ? refusalWordsWith(e.status, e.detail ?? '', opts.t)
      : e?.message || fallback;
  if (!said && raw && typeof console !== 'undefined') console.warn('[filex]', raw);
  // ⚠ Both lines are isolated for the reader's direction: `text` may be the
  // fallback the caller wrote (already said by ITS `t`, and isolation is
  // idempotent) and `detail` is the raw server words — a JSON body, a path,
  // an error chain, all of it machine text an Arabic line would scramble.
  const shown = foreignWith(opts.t, text);
  const detail = foreignWith(opts.t, raw);
  return opts.callerAdmin && raw && raw !== text ? { text: shown, detail } : { text: shown };
}

/** What the server says about a failed app job (ops row `error_code`). */
export type JobErrorCode =
  | 'timeout'
  | 'out_of_memory'
  | 'crashed'
  | 'app_removed'
  | 'action_removed'
  | 'engine_missing'
  | 'cancelled'
  | 'app';

/**
 * A failed operation, said — the operations centre and its toast.
 *
 * `error_code` comes from the server (wasmplugin DecorateOps), which knows
 * the failures it produced itself; the raw `error` stays the admin's second
 * line. With no code (a copy / move, or an older server), known refusals are
 * recognised by their words (`CODE_WORDS`) and anything else is `fallback`.
 */
/** A queue row, as usePendingOps normalises it. */
export interface FailedOpLike {
  op_type?: string;
  label?: string;
  action?: string;
  error_message?: string | null;
  error_code?: string | null;
  error_engine?: string | null;
}

/**
 * A failed QUEUE row, said — the one call the tray and the explorer's toast
 * share, so the two can never say different things about one failure.
 * ⚠ Before it, the toast passed the row itself and read `op.error` — a field
 * a queue row does not have (it is `error_message`) — and always said the
 * fallback while the tray beside it said the app's words.
 */
export function opFailure(op: FailedOpLike, t: T, opts: { callerAdmin?: boolean } = {}): SaidFailure {
  const isApp = op.op_type === 'plugin' || op.op_type === 'plugin-action';
  const fallback = isApp
    ? t('plugin.failed', { label: op.label || op.action || t('opc.kind.plugin') })
    : t('toast.failed');
  return jobFailure(
    { error: op.error_message, error_code: op.error_code, error_engine: op.error_engine },
    fallback,
    t,
    opts,
  );
}

export function jobFailure(
  op: { error?: string | null; error_code?: string | null; error_engine?: string | null },
  fallback: string,
  t: T,
  opts: { callerAdmin?: boolean } = {},
): SaidFailure {
  const raw = (op.error ?? '').trim();
  const admin = opts.callerAdmin === true;
  /** ⚠ The APP's own words and the raw failure both come from outside the
   *  catalogue, so both are isolated for the reader's direction (`T.foreign`,
   *  lib/direction `foreignText`): an app's sentence ends in a library error
   *  in brackets, and an engine's name, a path or a `a: b: c` chain drawn in
   *  an Arabic line loses its neutral characters to the paragraph. */
  const say = (text: string): string => foreignWith(t, text);
  const withDetail = (text: string): SaidFailure =>
    admin && raw && raw !== text ? { text: say(text), detail: say(raw) } : { text: say(text) };
  const engine = op.error_engine || '';
  switch (op.error_code) {
    case 'timeout':
      return withDetail(t('opc.err.timeout'));
    case 'out_of_memory':
      return withDetail(t('opc.err.out_of_memory'));
    case 'crashed':
      return withDetail(t('opc.err.crashed'));
    case 'app_removed':
      return withDetail(t('opc.err.app_removed'));
    case 'action_removed':
      return withDetail(t('opc.err.action_removed'));
    case 'cancelled':
      return { text: t('opc.status.aborted') };
    case 'engine_missing':
      return withDetail(t(admin ? 'opc.err.engine_missing_admin' : 'opc.err.engine_missing', { engine }));
    case 'app': {
      // The app's own words, meant for the person (wasmplugin CallError:
      // "safe for a user; the guest's own words") — unless they are plumbing.
      // ⚠ An app often ends its sentence with the library's error in
      // brackets: "hiçbir dosya dönüştürülemedi — broken.docx: dönüşüm
      // başarısız oldu (office: not a zip package: zip: not a valid zip
      // file)" was on a regular user's screen (2026-09-21). The sentence is
      // kept, the bracket goes to the admin's second line.
      const [sentence, tail] = splitTechnicalTail(raw);
      if (sentence && !looksTechnical(sentence)) {
        return tail && admin ? { text: say(sentence), detail: say(raw) } : { text: say(sentence) };
      }
      return withDetail(fallback);
    }
    default:
      break;
  }
  for (const [re, key] of CODE_WORDS) if (raw && re.test(raw)) return withDetail(t(key));
  return withDetail(fallback);
}
