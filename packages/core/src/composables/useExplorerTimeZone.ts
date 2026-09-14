/**
 * useExplorerTimeZone — what ONE explorer instance knows about the clock, fed
 * into `lib/timezone`'s resolver.
 *
 * An explorer knows two of the four tiers and nobody else can know them for
 * it: the zone its host configured (`config.timeZone`, tier `host`) and the
 * account behind the credential it holds (tier `account`). The viewer tier is
 * the browser's and the device tier is `Intl`'s; neither needs an instance.
 *
 * How the account is learned — nothing new on the server:
 *
 *   1. the credential's KIND, from `GET /api/files/capabilities`
 *      (`caller_kind`, migration 00030), which the explorer already fetches —
 *      or `config.callerKind`, which a host may set to answer sooner and which
 *      wins over the server's, exactly as it does for the panel's surfaces;
 *   2. for a person, `GET /api/auth/me` — the route every role may call, open
 *      to a bearer token (MiddlewareWithToken) and to cross-origin callers
 *      (CORS allows `Authorization`), and already used by the Connections
 *      panel for the caller's own e-mail. `user.timezone` is the zone.
 *
 * ⚠⚠ An `app` token skips the tier. It authenticates AS its owner, so
 * `/api/auth/me` would happily answer with that owner's zone — and the embeds
 * we run authenticate every visitor with ONE such token. Imposing its owner's
 * clock on all of them is the exact thing the owner ruled out.
 *
 * ⚠ Unknown kind also skips. When capabilities never lands (an old server, a
 * host that routes nothing but the manager), nobody can say whether the key is
 * a person's, and guessing "person" would be guessing the dangerous way.
 *
 * ⚠ Every failure is silent and falls through: a 401, a 404 from a host that
 * does not proxy `/api/auth/me`, a network error. The tier is withdrawn and
 * the browser's zone answers. A clock must never throw.
 */

import { onBeforeUnmount, provide, watch, type Ref } from 'vue';
import type { ExplorerConfig } from '../types/ExplorerConfig';
import type { Capabilities } from '../types/FileNode';
import { EXPLORER_CLOCK, releaseTimeZoneOwner, setAccountTimeZone, setHostTimeZone } from '../lib/timezone';

export interface ExplorerTimeZoneDeps {
  config: () => ExplorerConfig;
  /** The explorer's capabilities answer; `null` until (and unless) it lands. */
  capabilities: Ref<Capabilities | null>;
  /** `GET /api/auth/me` with this explorer's credential. */
  fetchMe: () => Promise<{ user?: { timezone?: unknown } | null } | undefined>;
}

export function useExplorerTimeZone(deps: ExplorerTimeZoneDeps): symbol {
  const owner = Symbol('filex-explorer');
  // Every component under this explorer reads its dates on this owner's
  // tiers, not the page's newest (lib/timezone, timeZoneSourcesFor).
  provide(EXPLORER_CLOCK, owner);

  watch(
    () => deps.config().timeZone,
    (tz) => setHostTimeZone(owner, tz),
    { immediate: true },
  );

  const kind = (): 'user' | 'app' | undefined =>
    deps.config().callerKind ?? deps.capabilities.value?.caller_kind;

  let asked = false;
  watch(
    kind,
    (k) => {
      if (k !== 'user') {
        // `app`: nobody's account. Unknown: cannot tell. Either way, withdraw.
        setAccountTimeZone(owner, null);
        return;
      }
      if (asked) return;
      asked = true;
      deps
        .fetchMe()
        .then((body) => {
          // The kind may have changed while the request was out.
          if (kind() !== 'user') return;
          const tz = body?.user?.timezone;
          setAccountTimeZone(owner, typeof tz === 'string' ? tz : null);
        })
        .catch(() => setAccountTimeZone(owner, null));
    },
    { immediate: true },
  );

  onBeforeUnmount(() => releaseTimeZoneOwner(owner));
  return owner;
}
