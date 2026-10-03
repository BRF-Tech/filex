/**
 * officeDiagnosis - what the editor says after ONLYOFFICE's "Download failed"
 * (issue #80).
 *
 * "Download failed." is the document server's one sentence for two different
 * failures, and its error code (-4) does not tell them apart:
 *
 * 1. the document server could not download the document from filex, or
 * 2. the browser could not load the converted copy back from the document
 *    server (`<document-server>/cache/files/.../Editor.bin`, an address the
 *    document server builds from the X-Forwarded-Proto / -Host headers the
 *    proxy in front of it passes).
 *
 * filex knows which: its fetch endpoint saw the document server's request, or
 * did not, and knows what it answered. The server keeps that per document
 * (`GET /api/files/onlyoffice/diagnose`, for whoever may open the document),
 * and the editor asks right after the error and says one of three sentences.
 *
 * ⚠ Shared by every surface that opens the editor (the explorer, the
 * standalone editor tab, the desktop's document windows) through PreviewModal:
 * one rule, written once.
 */

/** ONLYOFFICE's `errorCode` for "Download failed" (c_oAscError.ID.DownloadError). */
export const OFFICE_DOWNLOAD_ERROR = -4;

/** The two channels ONLYOFFICE tells its host about trouble on. */
export type OfficeEventChannel = 'onError' | 'onWarning';

/**
 * Does this ONLYOFFICE event leave an editor nobody can use?
 *
 * ⚠⚠ THE ONE PLACE this is decided, and the document server decides it: its
 * editor (web-apps `controller/Main.js` onError) reports an error of level
 * Critical through the host's `onError`, and every other one through
 * `onWarning` (`common/Gateway.js` reportError / reportWarning). A critical
 * one is drawn in a dialog that cannot be closed and the editor under it is
 * dead: "Download failed" (-4), a bad or expired token (-20, -21), access
 * denied (-23), a document it cannot open or convert, its database, too
 * many users, an unsupported browser. A warning is drawn in a dialog that
 * closes, and the editor goes on. Read from ONLYOFFICE Docs 9.4.0 and
 * measured with it (issue #80, the 0.50 final run).
 *
 * So the channel is the rule, not a list of codes: a code list would be a
 * copy of the document server's own decision, and go stale with its next
 * release.
 */
export function officeEventEndsTheEditor(channel: OfficeEventChannel): boolean {
  return channel === 'onError';
}

export type OfficeFetchVerdict = 'served' | 'not_requested' | 'refused';

/** The server's answer (onlyoffice.Diagnosis). */
export interface OfficeDiagnosis {
  verdict: OfficeFetchVerdict;
  opened_at?: string;
  fetch?: { at: string; status: number; reason_code?: string; reason?: string };
  /** Whose memory: `this_process` (one replica's, see docs/ONLYOFFICE.md). */
  scope?: string;
}

/**
 * The error code in ONLYOFFICE's `onError` event (`{ data: { errorCode,
 * errorDescription } }`), or null when it carries none.
 */
export function officeErrorCode(err: unknown): number | null {
  const d = (err as { data?: unknown } | null)?.data;
  if (d && typeof d === 'object') {
    const code = (d as { errorCode?: unknown }).errorCode;
    if (typeof code === 'number') return code;
    if (typeof code === 'string' && /^-?\d+$/.test(code)) return Number(code);
  }
  return null;
}

const XML_ENTITIES: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", '#39': "'" };

/**
 * The document server's error description as the text the page shows.
 * ONLYOFFICE writes some of them with markup: Docs 9.4 says "The document
 * security token is not correctly formed.<br>Please contact your Document
 * Server administrator." (measured in e2e/realenv, issue #80 S2 and S5), and
 * the fallback, which shows text, showed the `<br>` as words. A line break
 * becomes a space, any other tag goes, the XML entities are read.
 */
export function officeErrorText(description: string): string {
  return description
    .replace(/<br\s*\/?>/gi, ' ')
    .replace(/<[^>]*>/g, '')
    .replace(/&(amp|lt|gt|quot|apos|#39);/g, (_m, name: string) => XML_ENTITIES[name] ?? '')
    .replace(/\s+/g, ' ')
    .trim();
}

/**
 * The diagnosis endpoint, beside the editor configuration endpoint every host
 * already hands the viewer (`…/api/files/onlyoffice/config` →
 * `…/api/files/onlyoffice/diagnose`). Deriving it keeps every host - and every
 * embedder's proxy prefix - working without a second setting. A configuration
 * endpoint that does not end in `/onlyoffice/config` (an embedder's own
 * route) has no known sibling: null, and the editor shows the document
 * server's own sentence as before.
 */
export function officeDiagnoseEndpoint(configEndpoint: string | null | undefined): string | null {
  if (!configEndpoint) return null;
  const m = /^(.*\/onlyoffice\/)config(\?.*)?$/.exec(configEndpoint);
  return m ? `${m[1]}diagnose` : null;
}

/** The refusal reasons the editor words itself; anything else reads the server's English. */
const REASON_KEYS: Record<string, string> = {
  bad_link: 'viewer.office_fetch_reason.bad_link',
  signature_expired: 'viewer.office_fetch_reason.signature_expired',
  signature_bad: 'viewer.office_fetch_reason.signature_bad',
  not_found: 'viewer.office_fetch_reason.not_found',
  storage_unavailable: 'viewer.office_fetch_reason.storage_unavailable',
  body_unavailable: 'viewer.office_fetch_reason.body_unavailable',
  object_missing: 'viewer.office_fetch_reason.object_missing',
  read_failed: 'viewer.office_fetch_reason.read_failed',
};

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/**
 * The sentence for a diagnosis, in the reader's language. Null for an answer
 * this build does not know (a newer server's verdict): the editor then keeps
 * the document server's own sentence rather than guessing.
 */
export function officeDiagnosisText(d: OfficeDiagnosis | null | undefined, t: Translate): string | null {
  if (!d) return null;
  switch (d.verdict) {
    case 'served':
      return t('viewer.office_download_served');
    case 'not_requested':
      return t('viewer.office_download_not_requested');
    case 'refused': {
      const code = d.fetch?.reason_code ?? '';
      const key = REASON_KEYS[code];
      const reason = key ? t(key) : (d.fetch?.reason ?? '').trim() || String(d.fetch?.status ?? '');
      return t('viewer.office_download_refused', { reason });
    }
    default:
      return null;
  }
}

/**
 * Ask the server what it saw for `path`. Null when it could not say (no
 * endpoint, an error, a non-JSON answer): the caller keeps the document
 * server's sentence.
 */
export async function fetchOfficeDiagnosis(
  endpoint: string | null,
  path: string,
  init: { headers?: Record<string, string>; credentials?: RequestCredentials } = {},
): Promise<OfficeDiagnosis | null> {
  if (!endpoint || !path) return null;
  const sep = endpoint.includes('?') ? '&' : '?';
  try {
    const res = await fetch(`${endpoint}${sep}path=${encodeURIComponent(path)}`, {
      method: 'GET',
      headers: init.headers,
      credentials: init.credentials ?? 'same-origin',
    });
    if (!res.ok) return null;
    const body = (await res.json()) as OfficeDiagnosis;
    return body && typeof body.verdict === 'string' ? body : null;
  } catch {
    return null;
  }
}
