// #196 - a row's right-click menu opens on the answers its rows depend on,
// and since the second round it no longer waits for them on the network.
//
// The 0.53 release run (e2e 115-tag-kinds, Firefox): the explorer opened a
// file's menu at once and asked the server only then whether the file may be
// encrypted (POST /api/files/e2e/allowed); "Encrypt with E2EE…" was drawn when
// the answer landed, 101 ms later, and pushed every row below it down between
// the press and the release of a click on "Tags" - which landed on "Star".
//
// The first round made the menu wait for those answers, up to 400 ms. The
// maintainers' rule since (2026-10-08): that wait is not acceptable. The
// answers are asked when a folder is listed, one batched request per question
// for every row and the remembered rows around it (prefetchMenuAnswers), kept
// per person and storage (lib/menuAnswers), refreshed when the live socket
// says `access.changed`, and a menu reads them at once. Only an answer nothing
// is known about holds a menu, at most ~40 ms.
//
// FileExplorer.vue is never mounted whole; its wiring is pinned by reading it,
// as e2ePolicyWiring.test.ts does. The store is web/tests/lib/menuAnswers.test.ts,
// the socket web/tests/lib/realtimeAccess.test.ts, what happens once the menu
// is open (nothing moves) web/tests/ui/contextMenuHoldsRows.test.ts, and the
// browser measurement e2e/tests/213-menu-rows-stay.spec.ts.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const explorer = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

/** One top-level function of the explorer's script, whole. ⚠ Ends at a line
 *  that is exactly `}`. */
function fn(name: string): string {
  const body = explorer.match(new RegExp(`(?:async )?function ${name}\\([\\s\\S]*?\\n\\}\\n`))?.[0] ?? '';
  expect(body, `${name} was not found`).not.toBe('');
  return body;
}

describe('a row menu opens on its answers (#196)', () => {
  it('there is one door to the menu, and it waits only for answers nothing is known about', () => {
    // Every opening goes through openCtxMenu: a second `show(` beside it would
    // be a menu that opens on nothing.
    expect(explorer.match(/ctxRef\.value\?\.show\(/g) ?? []).toHaveLength(1);
    const open = fn('openCtxMenu');
    expect(open).toMatch(/ctxRef\.value\?\.show\(at, targets\);/);
    // The rows are listed first: that is what reads (and, when unknown, asks)
    // their answers.
    const list = open.indexOf('void contextActions.value;');
    const wait = open.indexOf('await rowAnswers.settled(MENU_ANSWER_WAIT_MS);');
    const show = open.indexOf('ctxRef.value?.show(');
    expect(list, 'lists the rows').toBeGreaterThan(-1);
    expect(wait, 'waits for an unknown answer').toBeGreaterThan(list);
    expect(show, 'shows after the wait').toBeGreaterThan(wait);
    expect(open).toMatch(/if \(rowAnswers\.count > 0 && typeof window !== 'undefined'\) \{/);
    expect(open).toMatch(/if \(targets\.length > 0\) \{/);
    expect(open).toMatch(/if \(seq !== ctxOpenSeq\) return;/);
    expect(open).toMatch(/addEventListener\('pointerdown', drop, true\)/);
    expect(open).toMatch(/removeEventListener\('pointerdown', drop, true\)/);
  });

  it('the wait is about 40 ms, not 400: a menu must not wait on the network', () => {
    const ms = Number(explorer.match(/const MENU_ANSWER_WAIT_MS = (\d+);/)?.[1]);
    expect(ms).toBeGreaterThan(0);
    expect(ms).toBeLessThanOrEqual(50);
  });

  it('every opening on rows goes through it: the listing, the split pane, Home’s cards', () => {
    expect(fn('onContextTarget')).toMatch(/await openCtxMenu\(\{ clientX: ev\.clientX, clientY: ev\.clientY \}, targets\);/);
    expect(fn('onPaneMenu')).toMatch(/await openCtxMenu\(\{ clientX: ev\.clientX, clientY: ev\.clientY \}, paneCtxTargets\.value\);/);
  });

  it('only a question nothing is known about holds a menu; a remembered answer is read at once and asked again behind it', () => {
    const e2e = fn('askE2eAnswers');
    expect(e2e).toMatch(/const unknown = missing\.some\(\(a\) => e2eKnownAt\(a\.path, a\.kind\) === undefined\);/);
    expect(e2e).toMatch(/const answered = unknown \? rowAnswers\.start\(\) : \(\) => \{\};/);
    // Asked again only when the remembered answer is not fresh.
    expect(e2e).toMatch(/!menuAnswers\.fresh\(`e:\$\{a\.kind\}`, a\.path\) && !e2eAsking\.has\(k\)/);
    // Released only after the answer is in both caches, so a waiting menu reads it.
    expect(e2e.indexOf('e2eAnswers.value = next;')).toBeLessThan(e2e.indexOf('answered();'));
    expect(e2e).toMatch(/menuAnswers\.put\(`e:\$\{a\.kind\}`, a\.path, next\[k\], !failed\);/);
    expect(e2e).toMatch(/\} finally \{\s*answered\(\);\s*\}/);

    const perms = fn('askFolderAllowed');
    expect(perms).toMatch(/const answered = missing\.some\(\(p\) => folderHeldAt\(p\) === undefined\) \? rowAnswers\.start\(\) : \(\) => \{\};/);
    expect(perms).toMatch(/!menuAnswers\.fresh\('p', p\) && !folderAsking\.has\(p\)/);
    expect(perms).toMatch(/menuAnswers\.put\('p', p, next\[p\], ok\);/);
    expect(perms).toMatch(/\.finally\(answered\);/);

    // The desktop's keep state: only its first answer holds a menu.
    expect(fn('refreshKept')).toMatch(/const answered = keptKnown \? \(\) => \{\} : rowAnswers\.start\(\);[\s\S]*keptKnown = true;[\s\S]*\} finally \{\s*answered\(\);\s*\}/);
  });

  it('a menu reads the remembered answer while a fresh one is on its way', () => {
    expect(fn('e2eAnswerAt')).toMatch(/return e2eKnownAt\(path, kind\) \?\? 'denied';/);
    expect(fn('e2eKnownAt')).toMatch(/e2eAnswers\.value\[e2eAskKey\(path, kind\)\] \?\? \(menuAnswers\.peek\(`e:\$\{kind\}`, path\)/);
    expect(fn('permHeldAt')).toMatch(/return paths\.every\(\(x\) => folderHeldAt\(x\)\?\.includes\(p\) \?\? false\);/);
    expect(fn('folderHeldAt')).toMatch(/folderAllowed\.value\[path\] \?\? \(menuAnswers\.peek\('p', path\)/);
  });
});

describe('the answers are asked before any menu opens (#196)', () => {
  it('a listing asks every row, once it is on screen, instead of forgetting what it knew', () => {
    const listing = fn('loadListing');
    expect(listing, 'the answers are not thrown away at every read any more').not.toMatch(/forgetE2eAnswers\(\);/);
    expect(listing).toMatch(/currentPath\.value = arrived;\s*\/\/ #196[^\n]*\n\s*prefetchMenuAnswers\(\);/);
    // Without a live socket nothing would say they went stale: every read
    // starts a new era.
    expect(listing).toMatch(/if \(!realtime\.connected\.value\) menuAnswers\.stale\(\);/);
  });

  it('one batched request per question: the folder, its rows, and the remembered rows around it', () => {
    const pre = fn('prefetchMenuAnswers');
    expect(pre).toMatch(/add\('p', here\);\s*add\('e:new_folder', here\);/);
    expect(pre).toMatch(/for \(const n of files\.value\.slice\(0, MENU_PREFETCH_MAX\)\)/);
    expect(pre).toMatch(/if \(n\.type === 'dir'\) add\('e:folder', n\.path\);\s*else if \(!isFxeRow\(n\)\) add\('e:file', n\.path\);/);
    expect(pre).toMatch(/for \(const a of menuAnswers\.around\(here\)\) add\(a\.q, a\.path\);/);
    expect(pre).toMatch(/askFolderAllowed\(\[\.\.\.perms\]\.slice\(0, 1000\)\)/);
    expect(pre).toMatch(/void askE2eAnswers\(\[\.\.\.e2e\.values\(\)\]\.slice\(0, 1000\)\)/);
    // ⚠ Never a path below a vault's folder: the server never hears of one.
    expect(pre).toMatch(/vault\.rootOf\(here\)\) return;/);
    expect(pre).toMatch(/vault\.isVaultRow\(n\)\) continue;/);
    // Nothing for the views that are not a folder.
    expect(pre).toMatch(/if \(trashMode\.value \|\| navView\.value \|\| notFoundPath\.value \|\| loadError\.value\) return;/);
    const max = Number(explorer.match(/const MENU_PREFETCH_MAX = (\d+);/)?.[1]);
    expect(max).toBeGreaterThan(0);
    expect(max).toBeLessThanOrEqual(1000);
  });

  it('the store belongs to the person: attached after the rules are known, released on unmount', () => {
    const attach = fn('attachMenuAnswers');
    expect(attach).toMatch(/await Promise\.all\(\[loadCapabilities\(\), ownPermissionsAsked\]\);/);
    expect(attach).toMatch(/\[origin, who\.id, who\.provider_id \?\? '', rootFloor\]\.join\('\|'\)/);
    expect(attach).toMatch(/menuAnswers\.attach\(scope, menuSig\(\)\);/);
    expect(explorer).toMatch(/onMounted\(\(\) => void attachMenuAnswers\(\)\);/);
    expect(explorer).toMatch(/onBeforeUnmount\(\(\) => menuAnswers\.dispose\(\)\);/);
    // A rule set that changes drops that family's remembered answers.
    expect(fn('forgetE2eAnswers')).toMatch(/menuAnswers\.sign\('e', menuSig\(\)\.e\);/);
    expect(explorer).toMatch(/folderAllowed\.value = \{\};\s*menuAnswers\.sign\('p', menuSig\(\)\.p\);/);
  });
});

describe('the live socket keeps them fresh (#196)', () => {
  it('access.changed: stale, own permissions read again, the folder listed again (spread when everybody heard it)', () => {
    expect(explorer).toMatch(/onAccess: \(news\) => onAccessChanged\(news\)/);
    const on = fn('onAccessChanged');
    expect(on).toMatch(/menuAnswers\.stale\(\);/);
    expect(on).toMatch(/if \(news\.resync\) \{\s*prefetchMenuAnswers\(\);\s*return;\s*\}/);
    expect(on).toMatch(/loadOwnPermissions\(\);\s*void load\(\);/);
    expect(on).toMatch(/news\.all \? Math\.random\(\) \* ACCESS_SPREAD_MS : 0/);
  });
});
