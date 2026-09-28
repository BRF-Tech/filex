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
});
