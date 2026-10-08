// pendingAnswers: the server questions a menu's rows are waiting on, and a
// bounded wait for them (#196).
//
// Some rows of the explorer's right-click menu depend on an answer from the
// server that is asked only when the menu is built: "Encrypt with E2EE…" asks
// `POST /api/files/e2e/allowed` for the row's path, and the folder-dependent
// permissions ask `allowedAt`. Until the answer lands the row is hidden. The
// menu used to open at once and draw the row when the answer came back, which
// pushed every row below it one place down while the person was aiming: in the
// 0.53 release run (Firefox, e2e 115) the answer took 101 ms and landed between
// the press and the release of a click on "Tags", which then hit "Star". On a
// slow server the same thing puts a person's click on a different verb, a
// destructive one included.
//
// The explorer now counts these questions here and opens a row's menu once
// none is outstanding, or after a short ceiling, whichever comes first. A
// question that is never answered costs the ceiling, not a menu that never
// opens; whatever lands after the menu is open is the menu's business
// (lib/heldMenuRows: nothing moves, late rows are added at its end).
export interface PendingAnswers {
  /** One question has been asked. Call the returned function once it is
   *  answered, failed or dropped; calling it again does nothing. */
  start(): () => void;
  /** Resolves `true` as soon as no question is outstanding (at once when none
   *  is), or `false` once `capMs` has passed with some still out. */
  settled(capMs: number): Promise<boolean>;
  /** Questions outstanding right now. */
  readonly count: number;
}

export function pendingAnswers(): PendingAnswers {
  let count = 0;
  let waiters: Array<() => void> = [];

  function wake(): void {
    if (count > 0 || waiters.length === 0) return;
    const now = waiters;
    waiters = [];
    for (const w of now) w();
  }

  return {
    start() {
      count++;
      let done = false;
      return () => {
        if (done) return;
        done = true;
        count--;
        wake();
      };
    },
    settled(capMs: number) {
      if (count === 0) return Promise.resolve(true);
      return new Promise<boolean>((resolve) => {
        let timer: ReturnType<typeof setTimeout> | undefined;
        const answered = () => {
          if (timer !== undefined) clearTimeout(timer);
          resolve(true);
        };
        waiters.push(answered);
        timer = setTimeout(() => {
          waiters = waiters.filter((w) => w !== answered);
          resolve(false);
        }, Math.max(0, capMs));
      });
    },
    get count() {
      return count;
    },
  };
}
