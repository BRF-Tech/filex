/**
 * usePublicLink — a visitor with a link and no account, whatever the link is.
 *
 * ⚠⚠ v3 §1: "one public surface, not three". A share (`/s/<token>`), a file
 * request (`/d/<token>`) and an app plugin's page (a share carrying a
 * `page_id`) used to be two rendering stacks and three layouts, so the PIN
 * box of a signature request looked nothing like the PIN box of a download
 * and neither carried the instance's own colours. They are the same walk:
 *
 *     info → (PIN when required) → the body → the states it can end in
 *
 * Only the BODY differs, and only the caller decides that. Everything here —
 * the fetch, the 401/404/410 reading, the PIN attempt and its lock, the
 * plugin conversation when the link carries one — is shared, because every
 * one of those is a place the three used to disagree.
 *
 * ⚠ Nothing is stored on this side: no session, no token copy, no PIN. The
 * unlock cookie is the server's and HttpOnly.
 *
 * ⚠ The endpoints are `/api/public/s/{token}` and `/api/public/d/{token}`;
 * the app-page walk (`/api/p/{token}`) is the same shape at a different
 * root, which is why the root is an argument. That is what lets `/p/*` be
 * retired without a second implementation living on for the transition.
 */
import { ref, type Ref } from 'vue';
import type { PluginRunResult, PluginSurface, PluginViewEventBody } from '../types/Plugins';
import type { PublicFailure, PublicRequestInfo, PublicShareInfo } from '../types/Public';
import { usePluginSurface, type PluginSurfaceStore } from './usePluginSurface';
import { foreignText } from '../lib/direction';
import { checkDropFiles, type DropRefusal } from '../lib/dropLimits';

export type PublicStatus =
  | 'loading'
  | 'pin'
  /** The body may be drawn (a file, a folder, an upload box, a surface). */
  | 'ready'
  /** A surface answered `202` — the job is queued and the visitor is done. */
  | 'accepted'
  | 'done'
  | 'not_found'
  | 'gone'
  | 'error';

export type PinFailure = '' | 'wrong' | 'locked';

/** A refused answer: the HTTP status and the server's `error` code. */
export class PublicLinkError extends Error {
  constructor(
    public status: number,
    public code: string,
    message?: string,
  ) {
    super(message || code || String(status));
    this.name = 'PublicLinkError';
  }
}

/** Builds the addresses of one link's walk. */
export type PublicRoot = (tail: string) => string;

function root(base: string | undefined, prefix: string, token: string): PublicRoot {
  const origin = (base ?? '').replace(/\/+$/, '');
  return (tail = '') => `${origin}${prefix}${encodeURIComponent(token)}${tail}`;
}

/** `/api/public/s/{token}` — a share (download, folder, or an app's page). */
export function shareRoot(base: string | undefined, token: string): PublicRoot {
  return root(base, '/api/public/s/', token);
}

/** `/api/public/d/{token}` — a file request. */
export function requestRoot(base: string | undefined, token: string): PublicRoot {
  return root(base, '/api/public/d/', token);
}

/**
 * `/api/p/{token}` — an app plugin's page.
 *
 * ⚠ Retired by v3 (§1: "`/p/*` — retired, 301 to the share"). Kept because
 * links already in people's mailboxes point at it and a 301 only helps a
 * browser that follows one; nothing new should be built on it.
 */
export function pageRoot(base: string | undefined, token: string): PublicRoot {
  return root(base, '/api/p/', token);
}

export interface PublicLinkOptions {
  /** API origin; empty = same origin. */
  base?: string;
  /** The visitor's language, sent as `Accept-Language` so a plugin answers in it. */
  locale: () => string;
  /** The words for an answer that carries nothing to show. */
  errorText: () => string;
  fetchImpl?: typeof fetch;
}

export function publicLinkClient<TInfo>(url: PublicRoot, opts: PublicLinkOptions) {
  const doFetch = (...args: Parameters<typeof fetch>) => (opts.fetchImpl ?? fetch)(...args);

  async function call<T>(tail: string, init: { method?: string; body?: unknown } = {}): Promise<{ status: number; body: T }> {
    const headers: Record<string, string> = { Accept: 'application/json', 'Accept-Language': opts.locale() };
    if (init.body !== undefined) headers['Content-Type'] = 'application/json';
    const res = await doFetch(url(tail), {
      method: init.method ?? 'GET',
      headers,
      credentials: 'same-origin',
      cache: 'no-store',
      body: init.body !== undefined ? JSON.stringify(init.body) : undefined,
    });
    let body: unknown = null;
    try {
      body = await res.json();
    } catch {
      body = null;
    }
    if (!res.ok) {
      const b = (body ?? {}) as { error?: string; message?: string };
      throw new PublicLinkError(res.status, b.error ?? '', b.message);
    }
    return { status: res.status, body: body as T };
  }

  return {
    /** What the link is and what the visitor may see of it. */
    info: (query = '') => call<TInfo>(query).then((r) => r.body),
    pin: (pin: string) => call<TInfo>('/pin', { method: 'POST', body: { pin } }).then((r) => r.body),
    /**
     * The app's opening screen.
     *
     * ⚠ There is no `/view` route: an open is `POST /event
     * {"event":"open"}`, because the opening surface and every later one are
     * the same call to the app. One endpoint, one code path in the plugin.
     */
    open: () =>
      call<{ surface?: PluginSurface }>('/event', { method: 'POST', body: { event: 'open', state: {}, data: {} } }).then(
        (r) => r.body,
      ),
    /** `{surface}`, or `{op}` when the server queued a job (202). */
    event: async (body: Omit<PluginViewEventBody, 'path' | 'storage_id'>): Promise<PluginRunResult> => {
      const r = await call<{ surface?: PluginSurface; accepted?: boolean; job_id?: string }>('/event', {
        method: 'POST',
        body,
      });
      if (r.status === 202 || r.body?.accepted) {
        return { op: { accepted: true, job_id: r.body?.job_id }, job_id: r.body?.job_id };
      }
      return { surface: r.body.surface as PluginSurface };
    },
    /** Where the browser loads an exposed copy from (`pub:N`). */
    fileUrl: (ref: string) => url(`/file/${encodeURIComponent(ref)}`),
    /**
     * The same copy, asked for as a DOWNLOAD — the Download button's link.
     * ⚠ Kept apart from `fileUrl` because only this one counts as a download
     * (handlers/public_api.go serveExposed): the page's own viewer loads the
     * document to SHOW it, and the owner ruled on 2026-09-21 that looking at
     * a signing link is not downloading it.
     */
    fileDownloadUrl: (ref: string) => url(`/file/${encodeURIComponent(ref)}?download=1`),
    url,
  };
}

export type PublicLinkClient<T> = ReturnType<typeof publicLinkClient<T>>;

/**
 * The walk itself.
 *
 * `TInfo` is the shape the body needs (`PublicShareInfo`,
 * `PublicRequestInfo`); this composable only reads the four facts every
 * public link has — does it want a PIN, is it unlocked, has it a surface,
 * and is it still alive.
 */
/**
 * What `usePublicLink` hands back.
 *
 * ⚠⚠ Written out rather than inferred (`ReturnType<typeof …>`). The returned
 * object carries the surface conversation, whose own type names Vue's
 * internal ref helpers, and an inferred declaration cannot name them from
 * outside this package: `vue-tsc` failed the package build with TS2742 — "the
 * inferred type cannot be named without a reference to @vue/shared" — and the
 * published `.d.ts` was built anyway, with a type consumers could not use.
 * A written type is also the contract: adding a field here is a decision.
 */
export interface PublicLinkStore<
  TInfo extends { needs_pin?: boolean; unlocked?: boolean; expired?: boolean; revoked?: boolean; locked?: boolean },
> {
  status: Ref<PublicStatus>;
  info: Ref<TInfo | null>;
  reason: Ref<PublicFailure>;
  pinFailure: Ref<PinFailure>;
  lockMessage: Ref<string>;
  pinBusy: Ref<boolean>;
  failure: Ref<string>;
  toast: Ref<string>;
  jobId: Ref<string>;
  /** The plugin surface on this link, when it carries one. */
  conv: PluginSurfaceStore;
  /** (Re-)read the link. `query` is appended verbatim (`?path=…`). */
  load: (query?: string) => Promise<void>;
  submitPin: (pin: string) => Promise<void>;
  /** Where the browser loads an exposed copy from (`pub:N`). */
  fileUrl: (ref: string) => string;
  /** The same copy as a download — the only fetch that counts as one. */
  fileDownloadUrl: (ref: string) => string;
  resolveFile: (ref: string) => { url: string; name?: string; mime?: string };
  url: PublicRoot;
}

export function usePublicLink<
  TInfo extends { needs_pin?: boolean; unlocked?: boolean; expired?: boolean; revoked?: boolean; locked?: boolean },
>(
  url: PublicRoot,
  opts: PublicLinkOptions & {
    /** True when this link's body is an app plugin's surface. */
    hasSurface?: (info: TInfo) => boolean;
  },
): PublicLinkStore<TInfo> {
  const client = publicLinkClient<TInfo>(url, opts);

  const status = ref<PublicStatus>('loading');
  // ⚠ Annotated, not inferred: `ref<TInfo|null>` of a GENERIC unwraps to a
  // type that cannot be written down outside this package (TS2742), and
  // the store's declared shape has to match what callers actually get.
  const info = ref(null) as Ref<TInfo | null>;
  const pinFailure = ref<PinFailure>('');
  /** The server's own words for a lock (`429 locked`), when it sends any. */
  const lockMessage = ref('');
  const jobId = ref('');
  const toast = ref('');
  /** A failure outside the conversation (network, 5xx). */
  const failure = ref('');
  /** Why there is nothing to show, for a caller that wants to say more. */
  const reason = ref<PublicFailure>('');

  const conv = usePluginSurface(
    {
      api: { pluginViewEvent: (_plugin, _view, body) => client.event(body) },
      plugin: '',
      view: '',
      locale: opts.locale,
      errorText: opts.errorText,
    },
    {
      onOp: (op) => {
        jobId.value = String((op as { job_id?: string }).job_id ?? '');
        status.value = 'accepted';
      },
      onDone: () => {
        if (status.value !== 'accepted') status.value = 'done';
      },
      onToast: (m) => {
        toast.value = m;
      },
    },
  );

  /**
   * ⚠ 404 and 410 both land on "this link is not available", and the two
   * states are kept apart only for the caller's own logging. A visitor is
   * never told WHICH — "expired" confirms that the link existed, which is
   * one bit more than somebody guessing tokens should get.
   */
  function fail(e: unknown): void {
    const err = e as PublicLinkError;
    if (err?.status === 404) {
      status.value = 'not_found';
      reason.value = 'not_found';
    } else if (err?.status === 410) {
      status.value = 'gone';
      reason.value = 'gone';
    } else {
      status.value = 'error';
      reason.value = 'error';
      // ⚠ An app's own error text never met the catalogue — isolate it.
      failure.value = foreignText(opts.locale(), String((e as Error)?.message ?? e)) || opts.errorText();
    }
  }

  async function openSurface(): Promise<void> {
    try {
      const res = await client.open();
      const s = res?.surface;
      if (!s) {
        status.value = 'error';
        failure.value = opts.errorText();
        return;
      }
      conv.setSurface(s);
      status.value = s.done ? 'done' : 'ready';
    } catch (e) {
      if ((e as PublicLinkError)?.status === 401) {
        status.value = 'pin';
        return;
      }
      fail(e);
    }
  }

/**
   * Is this link dead?
   *
   * ⚠⚠ A link that ran out answers 200 with the ordinary shape and
   * `expired` / `revoked` set — NOT a 410. That is deliberate on the server
   * side (a visitor who was given a link is entitled to be told it is over),
   * and it means a client that only checks the status code renders an empty
   * download page instead of an explanation. Both flags mean the same thing
   * to the person reading the page, which is why they collapse here and are
   * kept apart in the payload.
   */
  function dead(i: TInfo | null): boolean {
    return i?.expired === true || i?.revoked === true;
  }

  /** The first call: the facts, then the PIN form or the body. */
  async function load(query = ''): Promise<void> {
    status.value = 'loading';
    failure.value = '';
    reason.value = '';
    try {
      info.value = await client.info(query);
    } catch (e) {
      fail(e);
      return;
    }
    if (dead(info.value as TInfo)) {
      status.value = 'gone';
      reason.value = 'gone';
      return;
    }
    if (info.value?.needs_pin && !info.value?.unlocked) {
      status.value = 'pin';
      // A gate shut by too many wrong answers says so before the first try,
      // rather than letting somebody spend an attempt finding out.
      pinFailure.value = info.value?.locked === true ? 'locked' : '';
      return;
    }
    if (opts.hasSurface?.(info.value as TInfo)) {
      await openSurface();
      return;
    }
    status.value = 'ready';
  }

  const pinBusy = ref(false);

  /** `401 pin_wrong` → `wrong`, `429 locked` → `locked`; on success straight to the body. */
  async function submitPin(pin: string): Promise<void> {
    if (pinBusy.value) return;
    pinFailure.value = '';
    lockMessage.value = '';
    pinBusy.value = true;
    try {
      // ⚠ Trimmed. A PIN is usually pasted out of a message, and a trailing
      // space is not a wrong code — refusing it spends one of the attempts
      // before the lockout on something the person cannot even see.
      info.value = await client.pin(pin.trim());
      if (dead(info.value as TInfo)) {
        status.value = 'gone';
        reason.value = 'gone';
      } else if (opts.hasSurface?.(info.value as TInfo)) {
        await openSurface();
      } else {
        status.value = 'ready';
      }
    } catch (e) {
      const err = e as PublicLinkError;
      if (err?.status === 429) {
        pinFailure.value = 'locked';
        // ⚠ The server's own rate-limit sentence — isolate it.
        lockMessage.value = foreignText(opts.locale(), err.message ?? '');
      } else if (err?.status === 401 || err?.status === 403) {
        pinFailure.value = 'wrong';
      } else {
        fail(e);
      }
    } finally {
      pinBusy.value = false;
    }
  }

  return {
    status,
    info,
    reason,
    pinFailure,
    lockMessage,
    pinBusy,
    failure,
    toast,
    jobId,
    conv,
    load,
    submitPin,
    fileUrl: client.fileUrl,
    fileDownloadUrl: client.fileDownloadUrl,
    /**
     * A `FileRefResolver` for the surface renderer: the URL, plus the name
     * and type the link's own `files[]` already told us.
     *
     * ⚠ The renderer needs the mime to decide how to draw the thing (a
     * picture, an embedded PDF, a link). Handing it a bare URL makes every
     * exposed copy render as "something to download", which is what a
     * `preview` node is specifically not for.
     */
    resolveFile: (ref: string) => {
      const files = (info.value as { app?: { files?: Array<{ ref: string; name?: string; mime?: string }> } } | null)
        ?.app?.files;
      const f = files?.find((x) => x.ref === ref);
      return { url: client.fileUrl(ref), name: f?.name, mime: f?.mime };
    },
    url: client.url,
  };
}



/**
 * Where the BYTES of a share live.
 *
 * ⚠ Not under `/api/`. `/s/<token>` is the address that has always served
 * the file, and it still does — the SPA shell is what a JavaScript browser
 * gets when it NAVIGATES there (`handlers/public_shell.go`), while
 * `?download=1` and `?zip=1` are among the escapes that mean "I am asking for
 * the thing, not for a page". Building a second download route under `/api/`
 * would be a second thing to keep working.
 */
export function shareDownloadUrl(base: string | undefined, token: string, opts: { zip?: boolean; path?: string } = {}): string {
  const origin = (base ?? '').replace(/\/+$/, '');
  const t = encodeURIComponent(token);
  if (opts.path) return `${origin}/s/${t}/f/${opts.path.split('/').map(encodeURIComponent).join('/')}?download=1`;
  return `${origin}/s/${t}?${opts.zip ? 'zip=1' : 'download=1'}`;
}

/** The no-JS page, for whatever the shell cannot draw yet (a folder walk). */
export function shareNoJsUrl(base: string | undefined, token: string): string {
  return `${(base ?? '').replace(/\/+$/, '')}/s/${encodeURIComponent(token)}?nojs=1`;
}

/** A share: a file, a folder, or an app plugin's page. */
export function usePublicShare(token: string, opts: PublicLinkOptions) {
  const link = usePublicLink<PublicShareInfo>(shareRoot(opts.base, token), {
    ...opts,
    hasSurface: (i) => i?.kind === 'app',
  });
  return {
    ...link,
    downloadUrl: (o: { zip?: boolean; path?: string } = {}) => shareDownloadUrl(opts.base, token, o),
    noJsUrl: () => shareNoJsUrl(opts.base, token),
  };
}

/** One file on its way up, as the drop box shows it. */
export interface PublicUpload {
  name: string;
  size: number;
  /** 0..100, or -1 while the browser has not said anything yet. */
  percent: number;
  /**
   * `refused` — never sent: the link does not take it (its type, its size, one
   * file too many). Said before the transfer, not after it.
   */
  state: 'sending' | 'done' | 'failed' | 'refused';
  error?: string;
}

/** What the person on the drop page adds to the files. */
export interface PublicUploadOptions {
  /** Their name, when the link asks for it (`limits.ask_name`). */
  name?: string;
  /**
   * The words for a file this page will not send. Handed in by the page
   * because the page has the catalogue; the rule that decides lives in
   * `lib/dropLimits`.
   */
  refusedText?: (reason: DropRefusal) => string;
}

/**
 * A file request: the drop box an outsider uploads into.
 *
 * The upload is XHR and not `fetch` for one reason: `fetch` has no upload
 * progress. A stranger sending a 300 MB file to somebody else's server with
 * no indication that anything is happening will reload the page, and the
 * transfer starts over.
 *
 * ⚠⚠ ONE request per drop, carrying every file of it. The server makes ONE
 * submission folder per request (`<date>_<name>`, drop.go) and counts
 * `max_files` per request; sending the files one by one made three folders out
 * of one drop of three files, and let a person past the per-submission cap by
 * simply sending them separately. The no-JavaScript page always sent them
 * together.
 */
export function usePublicRequest(token: string, opts: PublicLinkOptions) {
  const link = usePublicLink<PublicRequestInfo>(requestRoot(opts.base, token), opts);
  const uploads = ref<PublicUpload[]>([]);

  function patchRange(from: number, to: number, p: Partial<PublicUpload>): void {
    uploads.value = uploads.value.map((u, n) => (n >= from && n < to ? { ...u, ...p } : u));
  }

  function send(files: File[], from: number, name: string): Promise<void> {
    const to = from + files.length;
    return new Promise<void>((resolve) => {
      let xhr: XMLHttpRequest;
      try {
        xhr = new XMLHttpRequest();
      } catch {
        patchRange(from, to, { state: 'failed', error: opts.errorText() });
        resolve();
        return;
      }
      xhr.open('POST', link.url('/upload'));
      xhr.withCredentials = true;
      xhr.setRequestHeader('Accept', 'application/json');
      xhr.setRequestHeader('Accept-Language', opts.locale());
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && e.total > 0) patchRange(from, to, { percent: Math.round((e.loaded / e.total) * 100) });
      };
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          patchRange(from, to, { state: 'done', percent: 100 });
        } else {
          // ⚠ The server says why, in the visitor's language (`message`,
          // drop.go `refuse`). An older server sends only the code; the
          // page's own "could not be sent" is then the honest fallback.
          let msg = '';
          try {
            msg = String((JSON.parse(xhr.responseText) as { message?: string })?.message ?? '');
          } catch {
            msg = '';
          }
          patchRange(from, to, { state: 'failed', error: msg || opts.errorText() });
        }
        resolve();
      };
      xhr.onerror = () => {
        patchRange(from, to, { state: 'failed', error: opts.errorText() });
        resolve();
      };
      const form = new FormData();
      for (const f of files) form.append('file', f, f.name);
      if (name) form.append('uploader_name', name);
      xhr.send(form);
    });
  }

  /** Everything the person dropped: what the link takes goes up in one request, the rest is said. */
  async function upload(files: File[] | FileList, o: PublicUploadOptions = {}): Promise<void> {
    const info = link.info.value;
    const { accepted, refused } = checkDropFiles(Array.from(files), info?.limits, info?.uploads_left ?? null);
    const said = o.refusedText ?? (() => opts.errorText());
    uploads.value = [
      ...uploads.value,
      ...refused.map(({ file, reason }) => ({
        name: file.name,
        size: file.size,
        percent: -1,
        state: 'refused' as const,
        error: said(reason),
      })),
    ];
    if (!accepted.length) return;
    const from = uploads.value.length;
    uploads.value = [
      ...uploads.value,
      ...accepted.map((f) => ({ name: f.name, size: f.size, percent: -1, state: 'sending' as const })),
    ];
    await send(accepted, from, (o.name ?? '').trim());
    await link.load();
  }

  return { ...link, uploads, upload };
}
