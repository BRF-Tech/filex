// `--fe-overlay-top` — where every window-sized layer the explorer draws begins.
//
// Owner, 2026-09-27: the desktop app's title bar works ALWAYS — minimise,
// maximise, close and the drag strip answer the first click even with a
// dialog open. The window is frameless, its title bar is page content, and a
// layer drawn over it takes its clicks. The layers therefore stop at the
// host's strip (core base.css `.fe-modal__backdrop` says why a z-index was
// not enough: the share scrim was at 10000, the column menu's catcher at
// 2147483000, the settings dialog in the browser's top layer).
//
// This file holds the rule where a new layer would break it: every `fixed`
// rule in core that covers the window starts at the variable, bar the ones
// named below with their reason. The desktop half is measured in Electron
// (desktop/scripts/titlebar-e2e.mjs); happy-dom lays nothing out.
import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';

const CORE = path.resolve(__dirname, '../../../packages/core/src');

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = path.join(dir, n);
    return statSync(p).isDirectory() ? files(p) : /\.(css|vue)$/.test(n) ? [p] : [];
  });
}

/** Every rule in a file's CSS, with the last line of its selector. */
function rules(file: string): { sel: string; body: string }[] {
  let css = readFileSync(file, 'utf8');
  if (file.endsWith('.vue')) css = css.split('<style').slice(1).join('<style');
  css = css.replace(/\/\*[\s\S]*?\*\//g, '');
  const out: { sel: string; body: string }[] = [];
  for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    out.push({ sel: m[1].trim().split('\n').pop()!.trim(), body: m[2] });
  }
  return out;
}

/* The ones that keep `inset: 0`, and why that is safe:
   · the context menu's click catcher — the menu inside it is placed
     `absolute` from the pointer, so moving the catcher would move the menu;
     it sits at z-index 80, under a host bar that is above the page (the
     desktop's is at 220);
   · the onboarding tour — its dim, spot and card are `fixed` themselves and
     sit at 96; it is a guide over the file list, not a window. */
const ALLOWED = new Set(['.fe-ctx-backdrop', '.fe-tour', '.fe-tour__dim']);

function stripComments(src: string): string {
  return src.replace(/<!--[\s\S]*?-->/g, '').replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
}

describe('every window-sized layer in core starts at --fe-overlay-top', () => {
  const layers = files(CORE).flatMap((f) =>
    rules(f)
      .filter((r) => /position:\s*fixed/.test(r.body) && /inset:\s*(0|var\(--fe-overlay-top)/.test(r.body))
      .map((r) => ({ file: path.relative(CORE, f), ...r })),
  );

  it('finds the layers it is about (the scan itself works)', () => {
    const names = layers.map((l) => l.sel);
    for (const s of ['.fe-modal__backdrop', '.fe-share__scrim', '.fe-overlay', '.fe-colmenu__backdrop', '.fx-nall']) {
      expect(names, s).toContain(s);
    }
  });

  it('each one begins at the host’s strip, or is named above with its reason', () => {
    const bad = layers.filter(
      (l) => !ALLOWED.has(l.sel) && !/inset:\s*var\(--fe-overlay-top,\s*0px\)\s+0\s+0\s*;/.test(l.body),
    );
    expect(bad.map((l) => `${l.file}: ${l.sel}`)).toEqual([]);
  });

  it('no core component opens a native modal <dialog> — the browser’s top layer is above any host bar', () => {
    const offenders = files(CORE).filter(
      (f) => f.endsWith('.vue') && /<dialog\b|\.showModal\(/.test(stripComments(readFileSync(f, 'utf8'))),
    );
    expect(offenders.map((f) => path.relative(CORE, f))).toEqual([]);
  });

  it('the cards inside those layers leave room for the strip too', () => {
    const base = readFileSync(path.join(CORE, 'styles/base.css'), 'utf8');
    expect(base).toMatch(/\.fe-modal__card \{[^}]*max-height: calc\(100vh - 32px - var\(--fe-overlay-top, 0px\)\)/);
    const dlg = readFileSync(path.join(CORE, 'components/UserSettingsDialog.vue'), 'utf8');
    expect(dlg).toMatch(
      /\.fe\.fx-us \{[^}]*height: min\(620px, calc\(100vh - 32px - var\(--fe-overlay-top, 0px\)\)\)/,
    );
  });

  it('the desktop window sets it to the height of its own title bar — one number for both', () => {
    const html = readFileSync(path.resolve(__dirname, '../../../desktop/ui/app.html'), 'utf8');
    expect(html).toMatch(/--fe-overlay-top: var\(--titlebar-h\);/);
    expect(html).toMatch(/#titlebar \{\s*flex: 0 0 var\(--titlebar-h\);/);
  });

  // #93 - the document windows (a file's own window: the viewer, the
  // open-with editor) reserved their bar with an `!important` patch on the
  // chromeless viewer's padding. It moved the viewer and nothing else: its
  // backdrop and any dialog in that window still began under the bar. They
  // state the strip through the same contract now, and nothing in the desktop
  // shell overrides core's layout rules with `!important`. The geometry is
  // measured in Electron (titlebar-e2e.mjs, section 4).
  it('the desktop document windows reserve their bar and their note through the variables, no !important patch', () => {
    // Raw text, not stripComments(): main.ts has globs like '**/*' in strings,
    // which a comment stripper would read as the start of a block comment.
    const code = readFileSync(path.resolve(__dirname, '../../../desktop/src/main.ts'), 'utf8');
    expect(code).toMatch(/:root\{--fe-overlay-top:\$\{H\}px\}/);
    expect(code).toMatch(/:root\{--fe-overlay-bottom:' \+ h \+ 'px\}/);
    const patches = [...code.matchAll(/\.fe-[\w-]+\{[^}]*!important/g)].map((m) => m[0]);
    expect(patches).toEqual([]);
  });

  it('the full-window viewer fits between the two strips; both default to 0 for every other host', () => {
    const vars = readFileSync(path.join(CORE, 'styles/variables.css'), 'utf8');
    expect(vars).toMatch(/:where\(:root\) \{[^}]*--fe-overlay-top: 0px;[^}]*--fe-overlay-bottom: 0px;/);
    const chromeless = rules(path.join(CORE, 'styles/base.css'));
    const card = chromeless.find((r) => r.sel === '.fe-modal__card--chromeless');
    expect(card?.body).toMatch(
      /height: calc\(100vh - var\(--fe-overlay-top, 0px\) - var\(--fe-overlay-bottom, 0px\)\);/,
    );
    const backdrop = chromeless.find((r) => r.sel === '.fe-modal__backdrop--chromeless');
    expect(backdrop?.body).toMatch(/bottom: var\(--fe-overlay-bottom, 0px\);/);
  });
});
