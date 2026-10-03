// The explorer shows the NEWEST listing asked for, and a reload goes where the
// newest folder load is going (packages/core/src/lib/listingTickets.ts).
//
// Measured in the 0.50 integration run (e2e 172): an upload into Eski/alt
// reached the disk, the person clicked "Eski" in the breadcrumb, then the
// upload answered and reloaded the folder still on screen. Eski answered
// first, alt last, and the explorer went back into alt - the click undone.
//
// FileExplorer is far too large to mount here, so the rule is measured on a
// small explorer that loads the way FileExplorer.load() does (address moves
// when the answer arrives, a reload passes no path), with answers held and
// released in the order the race needs; and the wiring test below proves
// FileExplorer goes through the same calls.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { listingTickets } from '../../../packages/core/src/lib/listingTickets';

/** An answer the test releases (or fails) when it wants. */
function held<T>() {
  let release!: (v: T) => void;
  let fail!: (e: Error) => void;
  const promise = new Promise<T>((r, j) => {
    release = r;
    fail = j;
  });
  return { promise, release, fail };
}

/** The shape of FileExplorer.load(): ask, wait, commit if still the newest. */
function miniExplorer(start: string) {
  const tickets = listingTickets();
  const asked: Array<{ folder: string; answer: ReturnType<typeof held<string[]>> }> = [];
  const state = { shown: start, rows: [] as string[] };
  function load(p?: string): Promise<void> {
    return tickets.follow(() => listing(p));
  }
  /** A load that starts another before its first await, as FileExplorer's
   *  does for a root with one storage. */
  function loadRootOfOne(): Promise<void> {
    return tickets.follow(async () => {
      tickets.begin(undefined, state.shown);
      return await load('Tek');
    });
  }
  async function listing(p?: string): Promise<void> {
    const { ticket, want } = tickets.begin(p, state.shown);
    tickets.going(want);
    const answer = held<string[]>();
    asked.push({ folder: want, answer });
    try {
      const rows = await answer.promise;
      if (!tickets.isNewest(ticket)) return;
      state.rows = rows;
      state.shown = want;
    } finally {
      tickets.end(ticket);
    }
  }
  async function openStarred(rowsOf: () => Promise<string[]>): Promise<void> {
    const ticket = tickets.view();
    state.shown = '.starred';
    const rows = await rowsOf();
    if (!tickets.isNewest(ticket)) return;
    state.rows = rows;
  }
  return { load, loadRootOfOne, openStarred, asked, state };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

describe('the explorer shows the newest listing asked for', () => {
  it('a reload that comes in while a navigation is out goes where the navigation goes, and the navigation stays', async () => {
    const x = miniExplorer('Eski/alt');
    const click = x.load('Eski'); // the breadcrumb
    const reload = x.load(); // the upload's reload, no path
    expect(x.asked.map((a) => a.folder), 'the reload asks for Eski, not the folder still on screen').toEqual(['Eski', 'Eski']);
    // The race as measured: the click's answer first, the reload's last.
    x.asked[0].answer.release(['alt', 'bir.txt']);
    await tick();
    x.asked[1].answer.release(['alt', 'bir.txt', 'iki.txt']);
    await Promise.all([click, reload]);
    expect(x.state.shown).toBe('Eski');
    expect(x.state.rows).toEqual(['alt', 'bir.txt', 'iki.txt']);
  });

  it('two navigations whose answers arrive in reverse order: the later click wins', async () => {
    const x = miniExplorer('');
    const a = x.load('A');
    const b = x.load('B');
    x.asked[1].answer.release(['b.txt']);
    await tick();
    x.asked[0].answer.release(['a.txt']);
    await Promise.all([a, b]);
    expect(x.state.shown, 'A answered last and is not what the person asked for last').toBe('B');
    expect(x.state.rows).toEqual(['b.txt']);
  });

  it('a folder answer that arrives after a panel view was opened does not paint over it', async () => {
    const x = miniExplorer('Eski');
    const folder = x.load('Eski/alt');
    const starred = held<string[]>();
    const view = x.openStarred(() => starred.promise);
    starred.release(['yildizli.txt']);
    await view;
    x.asked[0].answer.release(['üç.txt']);
    await folder;
    expect(x.state.shown).toBe('.starred');
    expect(x.state.rows).toEqual(['yildizli.txt']);
    // …and a reload now reloads the view's address, not the folder that was on its way.
    const again = x.load();
    expect(x.asked[1].folder).toBe('.starred');
    x.asked[1].answer.release(['yildizli.txt']);
    await again;
  });

  it('an awaited navigation resolves when its folder is on screen, though a reload overtook it', async () => {
    // e2e 174 as measured: "Encrypt with E2EE…" writes the key file, awaits
    // load(folder) and starts the conversion only if the explorer is IN the
    // folder by then. The key file's change event reloaded while that
    // listing was out; the reload went to the same folder with the newer
    // ticket, the dialog's answer committed nothing, and its await returned
    // with the explorer still on the parent.
    const x = miniExplorer('');
    const dialog = x.load('Arşiv');
    const reload = x.load(); // the realtime layer, no path
    expect(x.asked.map((a) => a.folder)).toEqual(['Arşiv', 'Arşiv']);
    let returned = false;
    void dialog.then(() => (returned = true));
    x.asked[0].answer.release(['Faturalar', 'not.txt']);
    await tick();
    await tick();
    expect(returned, 'the await must not return while the explorer is still on the parent').toBe(false);
    expect(x.state.shown).toBe('');
    x.asked[1].answer.release(['Faturalar', 'not.txt', '.filex-e2e.json']);
    await dialog;
    expect(x.state.shown, 'when the await returns, the folder is on screen').toBe('Arşiv');
    await reload;
  });

  it('a load that starts another before its own first await ends - with it', async () => {
    // The 0.50 final run: a root with one storage opens it from inside its own
    // load. Marked newest after it had started, the outer load waited for the
    // inner, the inner for the outer, and neither ended - the first load
    // never settled, and a notification waiting on it never opened the Trash.
    const x = miniExplorer('');
    const root = x.loadRootOfOne();
    expect(x.asked.map((a) => a.folder)).toEqual(['Tek']);
    x.asked[0].answer.release(['a.txt']);
    const outcome = await Promise.race([root.then(() => 'ended'), new Promise((r) => setTimeout(() => r('stuck'), 200))]);
    expect(outcome, 'the outer load must end').toBe('ended');
    expect(x.state.shown).toBe('Tek');
  });

  it("a newer load's failure is its own caller's; the older caller's own failure is still thrown", async () => {
    const x = miniExplorer('');
    const first = x.load('A');
    const second = x.load('B');
    const gone = new Error('B is gone');
    x.asked[0].answer.release(['a']);
    x.asked[1].answer.fail(gone);
    await expect(first).resolves.toBeUndefined();
    await expect(second).rejects.toBe(gone);

    const y = miniExplorer('');
    const alone = y.load('C');
    const broken = new Error('C failed');
    y.asked[0].answer.fail(broken);
    await expect(alone).rejects.toBe(broken);
  });

  it('with nothing in flight a reload reloads the folder on screen', async () => {
    const x = miniExplorer('Belgeler');
    const r = x.load();
    expect(x.asked[0].folder).toBe('Belgeler');
    x.asked[0].answer.release(['a']);
    await r;
    const r2 = x.load();
    expect(x.asked[1].folder, 'the finished load left nothing "on its way"').toBe('Belgeler');
    x.asked[1].answer.release(['a']);
    await r2;
  });
});

describe('FileExplorer goes through the tickets', () => {
  const src = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const body = (name: string) => {
    const at = src.indexOf(`async function ${name}(`);
    expect(at, name).toBeGreaterThan(-1);
    const end = src.indexOf('\n}\n', at);
    return src.slice(at, end);
  };

  it('load() hands its listing to tickets.follow', () => {
    expect(src).toMatch(
      /function load\(path\?: string\): Promise<void> \{\s*return tickets\.follow\(\(\) => loadListing\(path\)\);\s*\}/,
    );
  });

  it('loadListing() begins with a ticket and commits the listing only while it is the newest', () => {
    const load = body('loadListing');
    expect(load).toMatch(/const \{ ticket, want \} = tickets\.begin\(path, currentPath\.value \?\? ''\);/);
    expect(load).toMatch(/tickets\.going\(want\);/);
    const guard = load.indexOf('if (!isNewestLoad(ticket)) return;');
    const commit = load.indexOf('files.value = filterListing(resp.files);');
    const address = load.indexOf('currentPath.value = arrived;');
    expect(guard, 'the guard').toBeGreaterThan(-1);
    expect(commit, 'the rows').toBeGreaterThan(guard);
    expect(address, 'the address').toBeGreaterThan(guard);
    expect(load).toMatch(/tickets\.end\(ticket\);/);
  });

  it('every panel view takes a ticket and commits its rows only while it is the newest', () => {
    for (const name of ['loadNavView', 'loadTagView', 'loadTrash']) {
      const fn = body(name);
      expect(fn, name).toMatch(/const ticket = tickets\.view\(\);/);
      if (name !== 'loadNavView' || fn.includes('fetchNavRows')) {
        expect(fn, name).toMatch(/if \(!isNewestLoad\(ticket\)\) return;\s*\n\s*files\.value = /);
      }
    }
  });
});
