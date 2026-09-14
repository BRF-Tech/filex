/**
 * downloadSelection — hand the browser ONE archive for a set of files.
 *
 * # What was there before
 *
 * `downloadFile(n)` is `window.open(api.downloadUrl(n.path), '_blank')` — one
 * tab, one body, one file. Five selected rows would have been five
 * `window.open` calls, and the browser stops the second one: they are popups
 * once the user gesture has been spent. That is the whole reason the selection
 * bar HID Download the moment a second row was selected, rather than doing
 * something bad with it.
 *
 * # Why two steps
 *
 * A download has to be a NAVIGATION. `fetch()` + `URL.createObjectURL(blob)`
 * buffers the entire archive in the tab before a single byte reaches disk,
 * which is exactly what a 4 GB selection cannot survive. Only a navigation
 * streams.
 *
 * But a navigation is a GET: it carries its arguments in the URL, where 300
 * selected paths do not fit, and it carries no `Authorization` header, which is
 * how the embedded hosts (work.example.com, fishapp) authenticate. So the server
 * splits it — an authenticated POST that resolves and authorizes the member
 * list and returns a short ticket URL, then a plain navigation to that URL.
 * `POST /api/files/archive/download` → `GET /z/<ticket>`.
 *
 * # Why an iframe and not window.open
 *
 * ⚠ The navigation happens AFTER an await. The user's gesture token is spent by
 * then, so `window.open` at that point is a popup and gets blocked — the same
 * failure the whole feature exists to avoid, moved one step later where it is
 * harder to notice. `location.href = url` is not blocked, but if the server
 * ever answers with a text error instead of an attachment the app navigates
 * away and the user loses their place. A hidden iframe is neither: an
 * attachment response is handed to the download manager, and an error body
 * lands somewhere invisible.
 */

import type { FileApi } from '../composables/useFileApi';

/** What the mint returns. Mirrors `archiveDownloadInfo` in the Go handler. */
export interface ArchiveTicket {
  /** Path to navigate to. Server-relative, e.g. `/z/<token>`. */
  url: string;
  ticket: string;
  /** Filename the browser will save, e.g. `Faturalar.zip`. */
  name: string;
  /** How many files the archive will contain, after the server expanded any
   *  selected folders and dropped what the caller may not read. */
  files: number;
  /** Uncompressed total. NOT the archive's size — see the Go handler on why
   *  there is no such number until the last member is deflated. */
  bytes: number;
  expires_at: string;
}

/**
 * The mint endpoint for a given manager URL.
 *
 * Derived by swapping the trailing `/manager` segment, which is how every
 * route outside the endpoint map is built in useFileApi (`/permissions`,
 * `/versions`, `/comments`, `/ws-ticket`). Doing it this way rather than
 * hardcoding `/api/files/...` is what keeps a cross-origin embed working: the
 * host configured `apiBase`, and the archive call has to land on the same
 * origin as the rest of the explorer.
 */
export function archiveTicketUrl(managerUrl: string): string {
  const base = managerUrl.split('?')[0];
  return base.replace(/\/manager$/, '/archive/download');
}

/**
 * Where the ticket URL should be fetched from.
 *
 * The server answers a server-relative `/z/<token>`, which is correct for the
 * same-origin case and wrong for an embed whose API lives on another host. The
 * origin comes from the manager URL, which is the one thing that is always
 * right.
 */
export function absoluteTicketUrl(managerUrl: string, ticketUrl: string): string {
  if (/^https?:\/\//i.test(ticketUrl)) return ticketUrl;
  const base = managerUrl.split('?')[0];
  const m = /^(https?:\/\/[^/]+)/i.exec(base);
  return m ? m[1] + ticketUrl : ticketUrl;
}

/**
 * Ask the server to prepare an archive of `paths` and return the ticket.
 *
 * Throws the same shaped error every other call in useFileApi throws — a
 * localized `message` plus `status`/`detail` — so a caller can toast
 * `err.message` and branch on `err.status` (409 = nothing readable in the
 * selection, 413 = too many files).
 */
export async function requestArchive(
  api: Pick<FileApi, 'jsonFetch' | 'endpoints'>,
  paths: string[],
  name?: string,
): Promise<ArchiveTicket> {
  const url = archiveTicketUrl(api.endpoints.manager);
  return api.jsonFetch<ArchiveTicket>(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(name ? { paths, name } : { paths }),
  });
}

/**
 * How long a download iframe is left in the document.
 *
 * It only has to outlive the moment the browser reads the response headers and
 * hands the body to its download manager; the transfer itself continues after
 * the element is gone. Sixty seconds is slack for a storage that takes a while
 * to produce the first byte, and is short enough that a session of repeated
 * downloads does not accumulate frames.
 */
export const DOWNLOAD_FRAME_TTL_MS = 60_000;

/**
 * Navigate to `url` in a way that saves a file instead of moving the page.
 *
 * Exported separately from `downloadArchive` so a caller that already has a
 * ticket (a retry, a test) can trigger it, and so the DOM half can be replaced
 * in an environment that has no document.
 */
export function triggerFileNavigation(url: string, doc: Document = document): void {
  const frame = doc.createElement('iframe');
  frame.hidden = true;
  frame.setAttribute('aria-hidden', 'true');
  frame.style.display = 'none';
  frame.src = url;
  doc.body.appendChild(frame);
  setTimeout(() => frame.remove(), DOWNLOAD_FRAME_TTL_MS);
}

/**
 * Mint an archive for `paths` and start downloading it.
 *
 * Resolves with the ticket once the download has been STARTED — not once it has
 * finished, which the page cannot observe. A caller that wants to say "12 files"
 * in a toast reads it off the returned ticket.
 */
export async function downloadArchive(
  api: Pick<FileApi, 'jsonFetch' | 'endpoints'>,
  paths: string[],
  opts: { name?: string; doc?: Document } = {},
): Promise<ArchiveTicket> {
  const ticket = await requestArchive(api, paths, opts.name);
  triggerFileNavigation(
    absoluteTicketUrl(api.endpoints.manager, ticket.url),
    opts.doc ?? document,
  );
  return ticket;
}
