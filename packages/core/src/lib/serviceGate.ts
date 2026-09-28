/**
 * serviceGate — what an action that needs an OPTIONAL external service
 * (ONLYOFFICE, draw.io…) becomes when that service is missing.
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
