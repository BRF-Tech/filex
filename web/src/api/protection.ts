// koru:k3 — data-protection settings adapter.
//
// Frozen contract (koru wave #1):
//   GET   /api/admin/protection  → ProtectionSettings
//   PATCH /api/admin/protection  → partial {trash_retention_days?, versions_keep_n?,
//                                            share_max_ttl_days?, av_save_scan_window_minutes?,
//                                            av_max_scan_mb?, av_enabled?, av_mode?,
//                                            av_clamd_addr?}
//
// The antivirus block separates STATUS (what the server is doing) from
// SETTINGS (what an admin saved). They can disagree, and the disagreement is
// the point: see ProtectionAntivirus.
//
// The backend half ships in the same wave; when the endpoint is missing
// (older server → 404/405) the Protection view shows an "unsupported" band
// instead of erroring, so the SPA stays deployable ahead of the backend.
import { api } from './client';

export interface ProtectionAntivirus {
  /**
   * STATUS: scanning is switched on AND something is configured to reach
   * (a binary resolved, or a clamd address that parses). This is the badge,
   * NOT the toggle — the toggle is `scan_enabled`, and the two differ exactly
   * when someone has switched scanning on with nothing to reach.
   */
  enabled: boolean;
  /** What would answer a scan: "clamscan" | "clamdscan" | "clamd" | "". */
  binary: string;
  /** How ClamAV is reached in force: "binary" | "daemon" | "" (unavailable). */
  mode?: string;
  /** The clamd address in force; absent in binary mode. */
  address?: string;
  /**
   * A live probe — clamd answered PING, or the executable is still there.
   * ⚠ Separate from `enabled` on purpose: a configured daemon that is down
   * would otherwise render as a green light over a scanner that is passing
   * nothing, which is the failure this whole field exists to make visible.
   * `health` carries the reason when it is false.
   */
  reachable?: boolean;
  health?: string;
  /** clamd's VERSION reply, when it could be asked. */
  version?: string;
  /**
   * The stored antivirus configuration differs from what the running server
   * booted with, i.e. a restart would change behaviour. It goes false by
   * itself after that restart — which is what makes the "takes effect at the
   * next restart" message honest instead of decorative.
   */
  restart_pending?: boolean;
  /**
   * SETTING (writable): the on/off switch, `antivirus.enabled`.
   * ⚠⚠ Deferred — stored at once, in force at the next restart, BOTH
   * directions. Say so in the UI at the moment of the flip.
   */
  scan_enabled?: boolean;
  /** SETTING (writable): `antivirus.mode`. Deferred the same way. */
  scan_mode?: string;
  /** SETTING (writable): `antivirus.clamd_addr`. Deferred the same way. */
  clamd_addr?: string;
  /** The closed set of modes the API accepts, so this form cannot offer one
   *  the API refuses. */
  modes?: string[];
  /**
   * Minutes a save from the built-in text editor waits before its scan is
   * queued; further saves to the same file inside that window are absorbed
   * into that one scan. Editable, unlike `enabled`/`binary` which are env.
   * Absent on older servers — treat undefined as "field not supported".
   */
  save_scan_window_minutes?: number;
  /** Bounds the API enforces. Shipped with the value so this form does not
   *  keep a second copy of them that can drift. */
  save_scan_window_min?: number;
  save_scan_window_max?: number;
  /** Largest file that gets scanned, in MB. Bigger files are skipped, not
   *  failed. Editable; the next file scanned uses the new value. */
  max_scan_mb?: number;
  max_scan_mb_min?: number;
  max_scan_mb_max?: number;
}

export interface ProtectionSettings {
  /** Days a soft-deleted node survives in the trash before the purge job
   *  hard-deletes it. Backend floor is 1 (values <= 0 fall back to 30). */
  trash_retention_days: number;
  /** Versions kept per node by the daily cleanup. 0 = unlimited (no cleanup). */
  versions_keep_n: number;
  /** Longest life a NEW share link may be given, in days. 0 = no ceiling.
   *  Applies to links created from now on — existing links are never touched. */
  share_max_ttl_days: number;
  /** How many existing live links outlive the ceiling (reported only). */
  shares_over_max_ttl: number;
  antivirus: ProtectionAntivirus;
}

export interface ProtectionPatch {
  trash_retention_days?: number;
  versions_keep_n?: number;
  share_max_ttl_days?: number;
  av_save_scan_window_minutes?: number;
  av_max_scan_mb?: number;
  /** ⚠⚠ These three take effect at the NEXT RESTART, in both directions. */
  av_enabled?: boolean;
  av_mode?: string;
  av_clamd_addr?: string;
}

export const ProtectionApi = {
  async get(): Promise<ProtectionSettings> {
    const { data } = await api.get<ProtectionSettings>('/admin/protection');
    return data;
  },

  async update(patch: ProtectionPatch): Promise<ProtectionSettings> {
    const { data } = await api.patch<ProtectionSettings>('/admin/protection', patch);
    return data;
  },
};
