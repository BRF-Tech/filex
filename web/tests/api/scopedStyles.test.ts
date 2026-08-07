// Guards the bug that made the share dialog render as raw unstyled HTML in
// EVERY embedded surface — the desktop app, work.brf.sh, fishapp, olivov.
//
// Vue's `<style scoped>` compiles to `.cls[data-v-HASH]`, and the hash is
// stamped onto the DOM by the component's own compilation. The web-component
// build compiles the SFCs from source while importing the CSS that a DIFFERENT
// build produced, so the two hashes are unrelated and every scoped rule is dead.
//
// Measured in the packaged desktop app on 2026-08-07:
//   DOM element  : data-v-b9443460
//   CSS rule     : .fx-perm-modal[data-v-cc21190e]
//   matches      : false
//   computed     : position static, background transparent, radius 0
//
// 16 of 42 components were affected: the share/permissions dialog, the convert
// dialog, the presence bar, star/tag/recent controls and nine viewers. Nothing
// errored — they just silently lost their styling wherever the component was
// embedded rather than run as the SPA.
//
// The class names are all prefixed (fx-/fe-/filex-), so `scoped` was buying
// nothing that the naming did not already provide.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = path.join(dir, name);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

const vueFiles = walk(CORE_SRC).filter((f) => f.endsWith('.vue'));

describe('component styles survive the web-component build', () => {
  it('no component uses <style scoped>', () => {
    const offenders = vueFiles
      .filter((f) => /<style[^>]*\sscoped/.test(readFileSync(f, 'utf8')))
      .map((f) => path.relative(CORE_SRC, f));

    expect(
      offenders,
      'scoped styles are silently dropped in the web-component build — use the existing fx-/fe- class prefixes instead',
    ).toEqual([]);
  });

  it('no component relies on scoped-only CSS syntax', () => {
    const offenders: string[] = [];
    for (const f of vueFiles) {
      const src = readFileSync(f, 'utf8');
      const style = src.includes('<style') ? src.slice(src.indexOf('<style')) : '';
      // :deep() / :global() / ::v-deep only mean anything inside a scoped block.
      // Left behind after unscoping, they are invalid selectors that kill the
      // whole rule.
      if (/(:deep\(|::v-deep|:global\()/.test(style)) {
        offenders.push(path.relative(CORE_SRC, f));
      }
    }
    expect(offenders, 'these are scoped-only escape hatches; unscoped they break the rule they are in').toEqual([]);
  });

  it('the guard is looking at real components', () => {
    // Without this, a bad path would make both tests above pass vacuously.
    expect(vueFiles.length).toBeGreaterThan(30);
  });
});
