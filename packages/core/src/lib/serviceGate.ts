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

import { isExternalUsable, type ExternalServiceStatus } from '../types/FileNode';

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

/*
 * "Is this an office document" is not answered here any more: the list that
 * stood here (ten extensions) disagreed with the explorer's own (nine) and
 * with the server's (`.docm`, `.xlsm`, `.pptm`, `.ppsx`, `.xlsb` were not
 * office documents to the menu). The server publishes the rule
 * (`capabilities.edit_kinds`) and lib/serverRules `isOfficeExt` reads it
 * (filex #211, audit B2).
 */

/** What the capabilities answer says about the document server. */
export interface OnlyOfficeCaps {
  onlyoffice_url?: string | null;
  external?: { onlyoffice?: ExternalServiceStatus };
}

/**
 * ONLYOFFICE is configured and answering, by the capabilities answer: the
 * probe did not fail (`external.onlyoffice`) and there is an address. The
 * explorer reads the same two fields (FileExplorer effectiveOnlyOfficeBase),
 * so "Open with ONLYOFFICE" means the same thing in Settings.
 */
export function onlyOfficeUsable(caps: OnlyOfficeCaps | null | undefined): boolean {
  if (!caps) return false;
  const st = caps.external?.onlyoffice;
  if (st && !isExternalUsable(st)) return false;
  return !!caps.onlyoffice_url;
}
