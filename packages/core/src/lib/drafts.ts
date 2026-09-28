/**
 * drafts — a new document is a DRAFT until its first save (issue #71).
 *
 * "New document" writes a draft: a real file in the storage the person chose,
 * in their own drafts area (`<storage>://.filex-drafts/<user id>/<key>/<name>`,
 * the server's syspath.Drafts), which every editor opens as the file it is.
 * The server remembers where it is meant to go. "Save" moves it there — or,
 * when something already has its name, says so and offers `name (2).ext`;
 * "Discard" sends it to the trash; closing its editor keeps it in Drafts.
 *
 * ⚠ ONE client for every caller: the explorer (its Drafts view, the New
 * document dialog), the viewer/editor (PreviewModal — its Save button and its
 * close question) and so every host that mounts them: fm, the desktop app,
 * the embeds. Each hands over how it talks to the server (a `DraftsTransport`)
 * and nothing else, so no caller owns a second spelling of an endpoint.
 *
 * The endpoints are docs/API.md → Drafts.
 */
import { requestFailure, type RequestFailure } from './errorWords';

/** One draft, as the server answers it. */
export interface DraftDto {
  key: string;
  /** The file's name — the name it is meant to have. */
  name: string;
  /** Where the draft's bytes are (adapter-qualified): what an editor opens. */
  path: string;
  storage: string;
  /** Where it is meant to go, adapter-qualified: the folder, and the file. */
  target_dir: string;
  target: string;
  /** The New-document type it was made as (`txt`, `docx`, …). */
  type: string;
  size: number;
  mime?: string;
  created_at: string;
  modified_at?: string;
}

export interface DraftList {
  drafts: DraftDto[];
  count: number;
  limit: number;
}

/** What `save` came to. */
export type DraftSaveOutcome =
  | { saved: true; path: string; name: string; targetDir: string }
  /** Something already has the draft's name there. Nothing moved; the person
   *  is asked, and `save(key, suggested)` saves under the free name. */
  | { saved: false; taken: true; name: string; suggested: string; targetDir: string };

/**
 * How a caller reaches the server: a fetch that already carries the caller's
 * credentials (headers, cookies) — the explorer's `useFileApi`, the viewer's
 * `authHeaders` — plus the locale refusals are said in.
 */
export interface DraftsTransport {
  request(url: string, init?: RequestInit): Promise<Response>;
  locale?: string;
}

/** Refusal codes the server answers a draft request with (handlers/drafts.go). */
export const DRAFT_LIMIT = 'DRAFT_LIMIT';
export const DRAFT_TARGET_TAKEN = 'TARGET_TAKEN';
export const DRAFT_FOLDER_GONE = 'FOLDER_GONE';

/**
 * A field of a refusal's JSON body. ⚠ Not `err.code`: that is the body's
 * `error` field (lib/errorWords `refusalCode`), and the draft doors name their
 * refusal in a `code` field beside an English `error`.
 */
function refusalField(err: unknown, key: string): unknown {
  const detail = (err as RequestFailure | null)?.detail;
  if (!detail) return undefined;
  try {
    const body = JSON.parse(detail) as Record<string, unknown>;
    return body && typeof body === 'object' ? body[key] : undefined;
  } catch {
    return undefined;
  }
}

/** Is this failure "you already keep as many drafts as this server allows"? */
export function isDraftLimit(err: unknown): boolean {
  return refusalField(err, 'code') === DRAFT_LIMIT;
}

/** Is this failure "the folder this draft is meant for is not there any more"? */
export function isDraftFolderGone(err: unknown): boolean {
  return refusalField(err, 'code') === DRAFT_FOLDER_GONE;
}

/** The limit a DRAFT_LIMIT refusal names, when it names one. */
export function draftLimitOf(err: unknown): number | null {
  const n = refusalField(err, 'limit');
  return typeof n === 'number' && n > 0 ? n : null;
}

/**
 * The drafts API at `base` (`<api>/api/files/drafts`), over `transport`.
 *
 * Every refusal is thrown as the one RequestFailure every screen already says
 * in words (lib/errorWords) — except the answer `save` is FOR: a taken name is
 * not a failure but a question, and comes back as `{saved: false, taken}`.
 */
export function draftsClient(base: string, transport: DraftsTransport) {
  const root = base.replace(/\/+$/, '');
  const one = (key: string) => `${root}/${encodeURIComponent(key)}`;

  async function call<T>(url: string, init: RequestInit = {}): Promise<{ status: number; body: T | null; text: string }> {
    const res = await transport.request(url, {
      ...init,
      headers: { 'Content-Type': 'application/json', ...((init.headers as Record<string, string>) ?? {}) },
    });
    const text = await res.text().catch(() => '');
    let body: T | null = null;
    if (text) {
      try {
        body = JSON.parse(text) as T;
      } catch {
        body = null;
      }
    }
    return { status: res.status, body, text };
  }

  function fail(status: number, text: string): never {
    throw requestFailure(status, text, transport.locale);
  }

  return {
    /**
     * New document, as a draft (the New document dialog's request): the same
     * body as `action=newfile`, answered with the same fields — `path` is the
     * draft's — plus the draft itself. A refusal throws; DRAFT_LIMIT is the
     * one the dialog says in its own words (isDraftLimit).
     */
    async create(
      path: string,
      name: string,
      type: string,
      opts: { exactName?: boolean } = {},
    ): Promise<{ path: string; name: string; ext: string; size: number; mime: string; draft: DraftDto }> {
      const r = await call<{ path: string; name: string; ext: string; size: number; mime: string; draft: DraftDto }>(root, {
        method: 'POST',
        body: JSON.stringify(opts.exactName ? { path, name, type, exact_name: true } : { path, name, type }),
      });
      if (r.status !== 201 || !r.body?.draft) fail(r.status, r.text);
      return r.body;
    },
    /** Every draft of the caller's, across every storage they can see. */
    async list(): Promise<DraftList> {
      const r = await call<DraftList>(root);
      if (r.status !== 200 || !r.body) fail(r.status, r.text);
      return { drafts: r.body.drafts ?? [], count: r.body.count ?? 0, limit: r.body.limit ?? 0 };
    },
    /** How many — the navigation panel's badge. */
    async count(): Promise<{ count: number; limit: number }> {
      const r = await call<{ count: number; limit: number }>(`${root}/count`);
      if (r.status !== 200 || !r.body) fail(r.status, r.text);
      return { count: r.body.count ?? 0, limit: r.body.limit ?? 0 };
    },
    /** One draft — what an editor on a draft's path asks. */
    async get(key: string): Promise<DraftDto> {
      const r = await call<DraftDto>(one(key));
      if (r.status !== 200 || !r.body) fail(r.status, r.text);
      return r.body;
    },
    /**
     * Save the draft where it is meant to go — under `as` when given (only
     * ever the `suggested` name a taken answer offered).
     */
    async save(key: string, as?: string): Promise<DraftSaveOutcome> {
      const r = await call<{
        path?: string;
        name?: string;
        target_dir?: string;
        code?: string;
        suggested?: string;
      }>(`${one(key)}/save`, { method: 'POST', body: JSON.stringify(as ? { as } : {}) });
      if (r.status === 200 && r.body?.path) {
        return { saved: true, path: r.body.path, name: r.body.name ?? '', targetDir: r.body.target_dir ?? '' };
      }
      if (r.status === 409 && r.body?.code === DRAFT_TARGET_TAKEN && r.body.suggested) {
        return {
          saved: false,
          taken: true,
          name: r.body.name ?? '',
          suggested: r.body.suggested,
          targetDir: r.body.target_dir ?? '',
        };
      }
      fail(r.status, r.text);
    },
    /** Discard it: the draft goes to the trash (restorable until it ages out). */
    async discard(key: string): Promise<void> {
      const r = await call<unknown>(one(key), { method: 'DELETE' });
      if (r.status !== 200) fail(r.status, r.text);
    },
  };
}

export type DraftsClient = ReturnType<typeof draftsClient>;

/**
 * The folder a draft is meant for, as a person reads it: `docs / Reports`,
 * the storage first. `target_dir` is adapter-qualified (`docs://Reports`).
 */
export function draftFolderLabel(targetDir: string): string {
  const at = targetDir.indexOf('://');
  const storage = at >= 0 ? targetDir.slice(0, at) : '';
  const rel = (at >= 0 ? targetDir.slice(at + 3) : targetDir).replace(/^\/+|\/+$/g, '');
  return [storage, ...rel.split('/').filter(Boolean)].filter(Boolean).join(' / ');
}
