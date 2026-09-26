/**
 * The request a dialog sends — Rename, New folder, Delete, Delete permanently —
 * from the press to the answer.
 *
 *   - `busy` while it is on its way: the dialog is Modal's `busy` (it cannot be
 *     closed under the request) and its button takes no second press;
 *   - a refusal goes INTO the dialog while that dialog is still the one on
 *     screen, and is said as a toast (`say`) once it is not (lesson #498: an
 *     error for a dialog that is not open must never go into its error ref,
 *     where nobody reads it);
 *   - every opening of the dialog is a new session: a request from an earlier
 *     one neither shows its busy state nor its refusal against the new target,
 *     and does not free the new one's button when it ends.
 *
 * ⚠ #59 kept one busy flag and one error ref per dialog, written by whichever
 * request answered last. A rename that succeeded closed its dialog and then
 * read the listing again with the flag still up, so a Rename opened on
 * another file in that time came up already saying "Renaming…".
 */
import { ref, watch, type Ref } from 'vue';

export interface DialogRequest {
  /** The request of the dialog on screen is on its way. */
  busy: Ref<boolean>;
  /** Its refusal, shown in the dialog. */
  error: Ref<string | null>;
  /** Starts a request: its ticket, or null while one is already on its way. */
  begin(): number | null;
  /** The request `ticket` has its answer. */
  end(ticket: number): void;
  /**
   * The request `ticket` was refused. Shown in its dialog while that dialog is
   * the one on screen (true); otherwise handed to `say`, when there is one
   * (false either way — the caller says it itself when it passed no `say`).
   */
  refuse(ticket: number, message: string): boolean;
  /** Is the dialog that sent `ticket` still the one on screen? */
  inDialog(ticket: number): boolean;
}

export function useDialogRequest(open: Ref<boolean>, opts: { say?: (message: string) => void } = {}): DialogRequest {
  const busy = ref(false);
  const error = ref<string | null>(null);
  let session = 0;

  watch(open, (now, before) => {
    if (!now || before) return;
    session++;
    busy.value = false;
    error.value = null;
  });

  function inDialog(ticket: number): boolean {
    return ticket === session && open.value;
  }

  return {
    busy,
    error,
    begin() {
      if (busy.value) return null;
      busy.value = true;
      error.value = null;
      return session;
    },
    end(ticket) {
      if (ticket === session) busy.value = false;
    },
    refuse(ticket, message) {
      if (inDialog(ticket)) {
        error.value = message;
        return true;
      }
      opts.say?.(message);
      return false;
    },
    inDialog,
  };
}
