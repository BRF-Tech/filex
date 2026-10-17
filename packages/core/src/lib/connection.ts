/**
 * connection — ONE notice for a connection that is down, however many
 * requests fall over while it is.
 *
 * ⚠⚠ THE STORM THIS EXISTS TO STOP. A page that has lost the server is right
 * to say so, and it used to say so once per failing request: the web app's
 * axios interceptor raised `errors.network` as a toast from every refusal
 * with no response. The explorer alone fires several calls a second — the
 * listing, a thumbnail per row, the bell's 15 s poll, the pending-operations
 * poll — so the corner filled with copies of one sentence and the person was
 * left dismissing a stack of them (owner, 2026-09-24: "attığı tüm istekler
 * için ayrı ayrı atıyor o yüzden çok fazla popover çıkıyor").
 *
 * A connection that is down is ONE FACT ABOUT THE PAGE, not one fact per
 * request. So:
 *
 *   - a BACKGROUND request — anything the person did not start: a listing, a
 *     thumbnail, a poll — is folded. It raises the shared notice and says
 *     nothing of its own, however many of them fail;
 *   - an ACTION the person is waiting on — an upload, a rename, a save, any
 *     write — still reports individually. ⚠ Folding those into a generic
 *     "offline" would be worse than the storm: the person is waiting to be
 *     told whether the thing they did happened;
 *   - the notice CLEARS ITSELF the moment any request gets an answer. Nobody
 *     should have to dismiss a stale one.
 *
 * ⚠ No new polling, and no change to any existing cadence. This module never
 * makes a request of its own: it is told what happened by whoever was already
 * talking to the server (the web app's axios interceptor, core's `jsonFetch`
 * and its upload XHR), so recovery is noticed by the next call the page was
 * going to make anyway.
 *
 * ⚠⚠ Shared on purpose — one rule for the web app, the desktop shell and an
 * embedded explorer. A per-surface copy is how two of them come to disagree
 * about what "offline" looks like.
 *
 * ⚠ `navigator.onLine` is deliberately NOT consulted. It answers "this
 * machine has a network", not "filex answers", and it is true for every way
 * this actually happens — the server stopped, the tunnel dropped, the laptop
 * woke on a captive-portal Wi-Fi. Observed failures are the only evidence.
 */
import { computed, ref, type ComputedRef } from 'vue';

/** Did the person start this request, or did the page? */
export type RequestKind = 'background' | 'action';

/** What the caller should do about a failure it just had. */
export type FailureVerdict = 'folded' | 'say';

const downSince = ref(0);
const folded = ref(0);

/** Is the server unreachable right now, as far as this page has seen? */
export const connectionDown: ComputedRef<boolean> = computed(() => downSince.value !== 0);

/** When it first went (epoch ms), or 0. For a caller that wants to wait. */
export const connectionDownSince: ComputedRef<number> = computed(() => downSince.value);

/**
 * How many background failures the shared notice is standing in for. The
 * notice does not print it — a number that climbs is the storm again, in one
 * line — but a test can assert that many failures made one notice.
 */
export const connectionFolded: ComputedRef<number> = computed(() => folded.value);

/**
 * THE PAGE'S OWN FAILURES ARE NOT A LOST CONNECTION (task #260).
 *
 * Two kinds of request end with no answer while the server is perfectly
 * reachable, and neither may raise the notice:
 *
 *   - one the page CANCELLED - an AbortController, axios' cancel
 *     (`ERR_CANCELED`, or `ECONNABORTED` "Request aborted" for an XHR the
 *     browser aborted). A timeout is NOT one of them: axios says ECONNABORTED
 *     for that too, with "timeout of N ms exceeded", and a server that does
 *     not answer in time is exactly what the notice is for;
 *   - one the BROWSER dropped because the page is being left. WebKit fails
 *     every request in flight with a bare "Load failed" TypeError the moment
 *     a navigation starts - indistinguishable from a dead network by its
 *     error alone - and it did so on the 0.56.3 GitHub gate: the person's
 *     explorer was opened while the start page was still asking for things,
 *     and the strip said "The server could not be reached" (e2e 179, webkit).
 *     So the page's own departure is remembered (`beforeunload`/`pagehide`)
 *     and a failure in the next LEAVE_GRACE_MS is the departure's, not the
 *     network's. The grace is short on purpose: a `beforeunload` that does
 *     not end in leaving (a download link, a cancelled prompt) must not mute
 *     a real outage for long, and `pageshow` (back from the bfcache) ends it.
 */
export const LEAVE_GRACE_MS = 3000;

let leftAt = 0;

/** The page has started to go away (the listeners below; a test calls it). */
export function notePageLeaving(now: number = Date.now()): void {
  leftAt = now || 1;
}

/** The page is here after all (`pageshow`, a test). */
export function notePageStaying(): void {
  leftAt = 0;
}

if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('beforeunload', () => notePageLeaving());
  window.addEventListener('pagehide', () => notePageLeaving());
  window.addEventListener('pageshow', () => notePageStaying());
}

/** Is `cause` a cancellation, as opposed to a request nobody answered? */
export function wasCancelled(cause: unknown): boolean {
  const e = cause as { name?: unknown; code?: unknown; message?: unknown } | null | undefined;
  if (!e || typeof e !== 'object') return false;
  if (e.name === 'AbortError' || e.name === 'CanceledError' || e.code === 'ERR_CANCELED') return true;
  return e.code === 'ECONNABORTED' && typeof e.message === 'string' && /aborted/i.test(e.message);
}

/** Did this failure come from the page itself (cancelled, or the page is being left)? */
export function failureIsThePagesOwn(cause?: unknown, now: number = Date.now()): boolean {
  if (wasCancelled(cause)) return true;
  return leftAt !== 0 && now - leftAt < LEAVE_GRACE_MS;
}

/**
 * A request got no answer at all. Raises the shared notice and says whether
 * this particular failure is the caller's to report.
 *
 * ⚠ An ACTION raises the notice too. The connection is down either way, and
 * the person's own message ("could not be renamed") does not explain why.
 *
 * ⚠ A failure that is the page's own (`failureIsThePagesOwn`: cancelled, or
 * the page is being left) raises nothing and is not the caller's to report
 * either: the person cancelled it or is already somewhere else.
 */
export function noteRequestFailed(kind: RequestKind, now: number = Date.now(), cause?: unknown): FailureVerdict {
  if (failureIsThePagesOwn(cause, now)) return 'folded';
  if (downSince.value === 0) downSince.value = now || 1;
  if (kind === 'background') {
    folded.value += 1;
    return 'folded';
  }
  return 'say';
}

/**
 * A request got an answer — any answer. ⚠ Including a 4xx or a 5xx: the
 * server spoke, so the connection is not what is wrong, and a notice that
 * said otherwise would be lying while the person reads a real refusal.
 */
export function noteRequestSucceeded(): void {
  if (downSince.value === 0 && folded.value === 0) return;
  downSince.value = 0;
  folded.value = 0;
}

/** Forget everything. For tests, and for a surface being torn down. */
export function resetConnectionNotice(): void {
  downSince.value = 0;
  folded.value = 0;
  leftAt = 0;
}

/**
 * The kind a request of this HTTP method counts as.
 *
 * ⚠ The method, not the URL. A read is the page keeping itself up to date and
 * can be folded; a write is something the person pressed and is waiting on.
 * The one rule both request layers apply, so the web app and an embedded
 * explorer cannot classify the same call differently.
 */
export function kindOfMethod(method: string | undefined): RequestKind {
  const m = String(method ?? 'get').toLowerCase();
  return m === 'get' || m === 'head' || m === 'options' ? 'background' : 'action';
}
