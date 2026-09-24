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
 * A request got no answer at all. Raises the shared notice and says whether
 * this particular failure is the caller's to report.
 *
 * ⚠ An ACTION raises the notice too. The connection is down either way, and
 * the person's own message ("could not be renamed") does not explain why.
 */
export function noteRequestFailed(kind: RequestKind, now: number = Date.now()): FailureVerdict {
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
