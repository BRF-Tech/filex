// `--keep-windows-hidden` is a test switch: the app opens its windows and
// loads their pages as usual, but never puts one on screen. store-e2e.mjs
// starts the Store copy with it, on the desktop of whoever cuts the release —
// before it did, a filex window sat in front of them for the length of the
// run (2026-09-28, three times in one night).
//
// The switch only holds if EVERY show and focus goes through the two helpers
// in main.ts that honour it, and if no window is born visible. A new
// `win.show()` somewhere else would put a window back on that desktop, and the
// Store run would only notice if it happened to reach that window.
//
// Run:  node --experimental-strip-types --test desktop/test/hidden-windows.test.ts

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { classifyArgv } from '../src/openwith.ts';

const DESKTOP = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const SRC = path.join(DESKTOP, 'src');
const MAIN = readFileSync(path.join(SRC, 'main.ts'), 'utf8');

function stripComments(code: string): string {
  // Block comments keep their newlines so the reported line numbers stay true.
  return code
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ''))
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

/** Blanks the bodies of the two helpers — the only places allowed to call
 *  show() and focus() on a window. Newlines are kept for the line numbers. */
function withoutHelpers(code: string): string {
  return code.replace(/function (?:showWindow|focusWindow)\([^)]*\)[^{]*\{[\s\S]*?\n\}/g, (m) => m.replace(/[^\n]/g, ''));
}

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return sources(p);
    return /\.(ts|cts|mts)$/.test(e.name) ? [p] : [];
  });
}

// A show/focus on something that is a window: `win.`, `window.`, `…Window.`,
// `…Win.`, or straight off a BrowserWindow lookup
// (`BrowserWindow.fromWebContents(x)?.focus()`). Notifications also have a
// `.show()` (`n.show()`, `new Notification({…}).show()`) and are not windows.
const WINDOW_CALL =
  /(?:\b(?:win|window|\w*Window|\w*Win)|BrowserWindow\.\w+\([^)]*\))\??\.(?:show|showInactive|focus|moveTop)\s*\(/;

test('every window is shown and focused through the helpers that honour the switch', () => {
  const offenders: string[] = [];
  for (const file of sources(SRC)) {
    withoutHelpers(stripComments(readFileSync(file, 'utf8')))
      .split('\n')
      .forEach((line, i) => {
        if (WINDOW_CALL.test(line)) offenders.push(`${path.relative(DESKTOP, file)}:${i + 1}: ${line.trim()}`);
      });
  }
  assert.deepEqual(offenders, [], 'call showWindow()/focusWindow() instead');
});

test('the scan would catch the calls the helpers replaced', () => {
  for (const line of [
    'shellWindow.focus();',
    "shellWindow.once('ready-to-show', () => shellWindow?.show());",
    'mainWindow.show();',
    'already.window.show();',
    "win.once('ready-to-show', () => win.show());",
    'BrowserWindow.fromWebContents(e.sender)?.focus();',
  ]) {
    assert.ok(WINDOW_CALL.test(line), line);
  }
  for (const line of ['n.show();', 'new Notification({ title, body }).show();', '}).show();', 'this.opts.show(row, text, cb);']) {
    assert.ok(!WINDOW_CALL.test(line), line);
  }
});

test('both helpers stand down under the switch, and only the switch turns it on', () => {
  const code = stripComments(MAIN);
  assert.match(code, /const KEEP_WINDOWS_HIDDEN_FLAG = '--keep-windows-hidden';/);
  assert.match(code, /const keepWindowsHidden = process\.argv\.includes\(KEEP_WINDOWS_HIDDEN_FLAG\);/);
  // Nothing else assigns it: no environment variable, no saved preference.
  assert.equal(code.match(/\bkeepWindowsHidden\s*=/g)?.length, 1);
  for (const name of ['showWindow', 'focusWindow']) {
    const body = code.match(new RegExp(`function ${name}\\([^)]*\\)[^{]*\\{([\\s\\S]*?)\\n\\}`))?.[1] ?? '';
    assert.match(body, /if \(!win \|\| keepWindowsHidden\) return;/, `${name} must return early under the switch`);
  }
});

test('no window is born visible', () => {
  const code = stripComments(MAIN);
  const births = [...code.matchAll(/new BrowserWindow\(\{/g)];
  assert.ok(births.length >= 3, `expected the shell, main and document windows, found ${births.length}`);
  for (const m of births) {
    // The options object, by brace matching from its opening brace.
    let depth = 0;
    let end = m.index + m[0].length - 1;
    for (; end < code.length; end++) {
      if (code[end] === '{') depth++;
      else if (code[end] === '}' && --depth === 0) break;
    }
    const opts = code.slice(m.index, end + 1);
    const line = code.slice(0, m.index).split('\n').length;
    assert.match(opts, /\bshow:\s*false\b/, `main.ts:${line}: a BrowserWindow without show: false is on screen before showWindow() is ever asked`);
  }
});

test('the switch is a switch to the launch, not a document to open', () => {
  assert.deepEqual(
    classifyArgv(['C:\\filex\\filex.exe', '--remote-debugging-port=9300', '--keep-windows-hidden']),
    { deepLinks: [], files: [] },
  );
});

test('store-e2e starts the Store copy with the same switch', () => {
  const e2e = readFileSync(path.join(DESKTOP, 'scripts', 'store-e2e.mjs'), 'utf8');
  assert.match(e2e, /--remote-debugging-port=\$\{port\} --keep-windows-hidden/);
});
