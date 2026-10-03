/**
 * Which of the listings in flight the explorer shows: the NEWEST asked for.
 *
 * ⚠⚠ A folder's address (`currentPath`) moves when its listing answers, and
 * two listings can be out at once. Measured in the 0.50 integration run (e2e
 * 172): an upload into Eski/alt reached the disk, the person clicked "Eski" in
 * the breadcrumb (its listing went out), and then the upload answered and
 * reloaded the folder still on screen - Eski/alt. Eski answered first, alt
 * last, and the explorer went back into alt: the click was undone.
 *
 * So every loader takes a ticket when it starts and commits nothing - rows,
 * address, selection, error state - unless no newer ticket was taken since
 * (`isNewest`). A reload with no path (an action's refresh, the realtime
 * layer, Refresh) goes where the newest folder load is GOING, not to the
 * folder still on screen (`begin`). A panel view (Starred, Recent, a tag, the
 * trash) moves its address at once (lesson #608) and takes a ticket too
 * (`view`), so a folder's answer that arrives after it cannot paint over it.
 *
 * ⚠⚠ And `await load(x)` resolves when the explorer SHOWS the newest listing
 * (`follow`), not when its own answer was thrown away. Measured in the 0.50
 * final run (e2e 174, then forced in the browser): "Encrypt with E2EE…" on an
 * existing folder writes the key file, awaits `load(folder)` and starts the
 * conversion only if the explorer is in that folder by then. The key file's
 * own change event made the realtime layer reload while that listing was
 * out; the reload went to the same folder and took the newer ticket, so the
 * dialog's listing committed nothing and its `await` returned with the
 * explorer still on the parent. The reload committed a moment later, the
 * folder was on screen, and the conversion had never started: every file
 * stayed plaintext until someone pressed Continue. Every caller that awaits
 * a load and then reads the rows or the address (a job's output, a palette
 * hit, a new document) stood on the same gap.
 */
export interface ListingTickets {
  /** A load starts: its ticket, and where it goes - the path asked for, else
   *  where the newest folder load is going, else the folder on screen. */
  begin(path: string | undefined, shown: string): { ticket: number; want: string };
  /** The load is a folder listing on its way to `want`. */
  going(want: string): void;
  /** A panel view starts: no folder is on its way any more. */
  view(): number;
  /** Was no newer load started since this ticket was taken? */
  isNewest(ticket: number): boolean;
  /** The load ended; when it was the newest, nothing is on its way. */
  end(ticket: number): void;
  /**
   * Start a folder load (`start`) and resolve when it has ended AND every
   * folder load started since it has ended too - the listing on screen is
   * then the newest asked for. Its own failure is thrown; a later load's is
   * that load's caller's to see.
   *
   * ⚠ A thunk, not a started promise: this load is marked newest BEFORE it
   * starts. A load can start another before its own first await (a root with
   * one storage opens that storage; an unreachable view falls back to the
   * root). Marked after starting, the outer load was taken for newer than
   * the one it started, the inner one waited for the outer, the outer waited
   * for the inner, and neither ever resolved: the 0.50 final run's 111 (a
   * notification that never opened the Trash, behind a first load that never
   * ended) and 121.
   */
  follow(start: () => Promise<void>): Promise<void>;
}

export function listingTickets(): ListingTickets {
  let latest = 0;
  let goingTo: string | null = null;
  let newestRun: Promise<void> = Promise.resolve();
  return {
    begin(path, shown) {
      latest += 1;
      return { ticket: latest, want: path ?? goingTo ?? shown };
    },
    going(want) {
      goingTo = want;
    },
    view() {
      goingTo = null;
      latest += 1;
      return latest;
    },
    isNewest(ticket) {
      return ticket === latest;
    },
    end(ticket) {
      if (ticket === latest) goingTo = null;
    },
    async follow(start) {
      // This load's place in the order, settled when its own work ends -
      // never when its follow ends, so no load ever waits for an older one.
      let ended!: () => void;
      const mine = new Promise<void>((r) => (ended = r));
      newestRun = mine;
      try {
        await start();
      } finally {
        ended();
      }
      let awaited = mine;
      while (newestRun !== awaited) {
        awaited = newestRun;
        await awaited;
      }
    },
  };
}
