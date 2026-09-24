/**
 * serviceGate — what an action that needs an OPTIONAL external service
 * (ONLYOFFICE, draw.io, the converter…) becomes when that service is missing.
 *
 * ⚠⚠ The owner's rule, 2026-09-21, after `Config fetch 503: {"error":
 * "onlyoffice not configured"}` was shown to a person who had clicked "Open" on
 * a .docx on an install with no document server:
 *
 *     "disabled with a reason for administrators, hidden for everybody else"
 *
 * An administrator CAN fix it, so a greyed entry that says what is missing and
 * where to set it up is useful to them. For everybody else it is a button that
 * can never work and only invites a click, so it is not offered at all. And
 * nobody, ever, is shown a raw status or a JSON body.
 *
 * ⚠ One function, so every entry that depends on a service answers the same
 * way (filex lesson #67: a rule written twice drifts the first time one copy is
 * touched). "Is this caller an administrator" is the server's answer
 * (`capabilities.caller_admin` — the same checks the admin routes apply,
 * supertenant included), never a guess made in the browser.
 */

/** The fields a `ContextAction` takes from the gate. */
export interface ServiceGate {
  hidden?: boolean;
  disabled?: boolean;
  title?: string;
}

/**
 * `available` — the service is configured and healthy.
 * `callerAdmin` — this caller could go and configure it.
 * `reason` — the sentence an administrator reads (what is missing, where to
 * fix it), already translated.
 */
export function gateOnService(available: boolean, callerAdmin: boolean, reason: string): ServiceGate {
  if (available) return {};
  if (callerAdmin) return { disabled: true, title: reason };
  return { hidden: true };
}

/**
 * The extensions the document server opens. ⚠ The ONE list: the preview
 * modal decides "this is an office document" from it and the explorer's menu
 * decides "Open needs ONLYOFFICE" from it — two lists would let a .rtf be
 * previewed as office and still offered an Open that cannot work.
 */
/**
 * THE LEGACY CONVERTER — the iframe service behind `FILEX_CONVERT_URL` /
 * External services → Converter — against the Convert APP.
 *
 * ⚠⚠ ONE of everything. With both in place an administrator's menu on a .docx
 * read "Dönüştür" (the service, greyed, "not set up") right above
 * "Dönüştür…" (the app): two entries with one name and two behaviours (QA,
 * 2026-09-21). The release plan's rule, made unbreakable by construction:
 *
 *   - the Convert app is offered on this instance → the legacy entry is never
 *     offered, to anybody, on any file (not "on files the app does not take":
 *     the two would still meet on the next file);
 *   - the legacy service is not configured → not offered either, not even
 *     greyed for an administrator: its replacement is the app, and pointing
 *     at a retiring service to "set it up" sends them the wrong way;
 *   - configured but not answering → the usual split (gateOnService):
 *     greyed with the reason for an administrator, hidden for everybody else;
 *   - configured and answering → offered; an administrator reads that it is
 *     being retired and what replaces it.
 */
export interface LegacyConvertInput {
  /** The Convert app has an action on this instance (lib/pluginMenu `convertAppOffered`). */
  appOffered: boolean;
  /** The legacy service is switched on (or the host passed its own URL). */
  configured: boolean;
  /** …and it answers its health check. */
  healthy: boolean;
  callerAdmin: boolean;
  /** Why it is greyed: configured but not answering. */
  unhealthyReason: string;
  /** What an administrator reads on a working entry: it is being retired. */
  adminNote: string;
}

export function legacyConvertGate(o: LegacyConvertInput): ServiceGate {
  if (o.appOffered || !o.configured) return { hidden: true };
  const g = gateOnService(o.healthy, o.callerAdmin, o.unhealthyReason);
  if (g.hidden || g.disabled) return g;
  return o.callerAdmin ? { title: o.adminNote } : {};
}

export const OFFICE_EXTS: readonly string[] = [
  'docx',
  'doc',
  'xlsx',
  'xls',
  'pptx',
  'ppt',
  'odt',
  'ods',
  'odp',
  'rtf',
];

export function isOfficeExt(ext: string | null | undefined): boolean {
  return OFFICE_EXTS.includes(String(ext ?? '').toLowerCase());
}
