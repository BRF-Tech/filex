/**
 * usePublicPage — the `/p/<token>` walk, kept as a NAME.
 *
 * ⚠⚠ v3 §1 retired `/p/*`: an app plugin's public page is a real share now
 * (`/s/<token>` carrying a `page_id`), so the administrator revokes it in
 * **Shares** like every other link and there is one PIN implementation, one
 * expiry policy and one visit counter instead of two.
 *
 * ⚠ So this file holds no implementation any more, and that is the point.
 * The walk it used to own — info → PIN → view → events — turned out to be
 * the SAME walk a share and a file request make, and `usePublicLink` is that
 * one walk at whichever root the link lives under. Keeping a second copy
 * here "for the transition" is exactly how two public pages came to disagree
 * about their own PIN box in the first place.
 *
 * The exports survive because a host may be importing them from the package:
 * they are the old names pointed at the shared machinery, and nothing new
 * should reach for them.
 */
import type { PublicPageInfo } from '../types/Plugins';
import {
  PublicLinkError,
  pageRoot,
  publicLinkClient,
  usePublicLink,
  type PublicLinkOptions,
  type PublicStatus,
} from './usePublicLink';

export type { PinFailure } from './usePublicLink';

/** @deprecated The old name of {@link PublicLinkError}. */
export { PublicLinkError as PublicPageError };

/**
 * @deprecated `/p/` is retired (v3 §1). The status names moved with the
 * walk: what used to be `'surface'` is `'ready'`, because a share's body is a
 * document or a folder at least as often as it is a plugin's screen.
 */
export type PublicPageStatus = PublicStatus;

export type PublicPageClientOptions = PublicLinkOptions;

/** `/api/p/{token}` and its children. */
export function publicPageUrl(base: string | undefined, token: string, tail = ''): string {
  return pageRoot(base, token)(tail);
}

/** @deprecated Use `publicLinkClient(shareRoot(base, token), …)`. */
export function publicPageClient(token: string, opts: PublicPageClientOptions) {
  return publicLinkClient<PublicPageInfo>(pageRoot(opts.base, token), opts);
}

export type PublicPageClient = ReturnType<typeof publicPageClient>;

/** @deprecated Use `usePublicShare` — an app's public page IS a share (v3 §1.1). */
export function usePublicPage(token: string, opts: PublicPageClientOptions & { errorText: () => string }) {
  return usePublicLink<PublicPageInfo>(pageRoot(opts.base, token), { ...opts, hasSurface: () => true });
}

export type PublicPageStore = ReturnType<typeof usePublicPage>;
