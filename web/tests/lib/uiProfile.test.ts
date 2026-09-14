// Two profiles, and one answer for everything that is not one of them.
//
// ⚠⚠ Why this file exists. `uiProfile` is the one config field that arrives
// from OUTSIDE the type system twice over: from an embedder's `config` object,
// which may come from a JavaScript codebase with no compiler at all, and from
// a `ui-profile="…"` attribute, which is a string the DOM hands over
// unexamined. So "what does an unrecognised value mean" is not a theoretical
// question — and until this module existed, the answer was whichever branch
// each reader's `===` chain happened to fall through to.
//
// It matters right now because a name was RETIRED. `'drive'` shipped as a
// third profile in v0.32.0, became an alias of `'simple'` in v0.40.0, and the
// owner's ruling on 2026-09-13 was to take it out ("kaldıralım direk"). filex
// is open source: we cannot know who is still passing it. What they now get is
// pinned here rather than left to a fall-through — and the console line that
// tells them so is pinned with it, because a silent change of UI is the one
// outcome nobody can act on.
import { describe, expect, it, beforeEach, vi, afterEach } from 'vitest';

import {
  DEFAULT_UI_PROFILE,
  UI_PROFILES,
  __resetUiProfileWarnings,
  resolveUiProfile,
} from '@brftech/filex-core';

beforeEach(() => {
  __resetUiProfileWarnings();
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe('resolveUiProfile', () => {
  it('knows exactly two names', () => {
    expect([...UI_PROFILES]).toEqual(['standard', 'simple']);
    expect(DEFAULT_UI_PROFILE).toBe('standard');
  });

  it('passes both of them through untouched', () => {
    expect(resolveUiProfile('standard')).toBe('standard');
    expect(resolveUiProfile('simple')).toBe('simple');
  });

  it('treats "nothing was passed" as the default, in silence', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    for (const v of [undefined, null, '']) expect(resolveUiProfile(v)).toBe('standard');
    expect(warn).not.toHaveBeenCalled();
  });

  it('gives a retired or mistyped name the DEFAULT, and says so once', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});

    // ⚠⚠ THE DECISION. An embed still passing `'drive'` gets the FULL
    // explorer, not the reduced one — it gains the tab strip, the split pane,
    // the gallery mode and the connection guides, and the fix is one word
    // (`'simple'`). Mapping it onto `'simple'` here would be the alias again
    // wearing a resolver's coat, and it would mean a plain typo silently
    // REDUCED somebody's UI: a failure that looks like features going missing
    // and points at nothing.
    expect(resolveUiProfile('drive')).toBe('standard');
    expect(resolveUiProfile('drve')).toBe('standard');
    expect(resolveUiProfile(7)).toBe('standard');

    // One line per distinct value — this runs inside a `computed` that re-runs
    // whenever the config object changes, so "once" is what makes it readable.
    expect(warn).toHaveBeenCalledTimes(3);
    resolveUiProfile('drive');
    expect(warn).toHaveBeenCalledTimes(3);

    // The line has to carry the offending value and the way out of it, or it
    // costs a bisect instead of a search.
    const said = warn.mock.calls.map((c) => String(c[0])).join('\n');
    expect(said).toContain('"drive"');
    expect(said).toContain('"simple"');
    expect(said).toContain('uiProfile');
  });
});

describe('every surface that describes the API says the name was removed', () => {
  // Not prose-policing. Each of these is a place somebody LOOKS UP what to
  // pass, and the failure this guards is specific: a page that still lists
  // `'drive'` beside `'simple'` recommends the one value this release stopped
  // honouring, and a reader following it would get the FULL explorer in front
  // of the users it was reduced for — silently, because nothing errors.
  //
  // So the rule is not "never say the word". A migration note has to say it.
  // The rule is that any PARAGRAPH mentioning it also says it was removed,
  // which is exactly the difference between an offer and a way out.
  const files = [
    'packages/core/src/types/ExplorerConfig.ts',
    'packages/core/README.md',
    'packages/react/README.md',
    'packages/webcomponent/README.md',
    'docs/INTEGRATION.md',
    'docs/README.md',
  ];

  it('never mentions it without saying so', async () => {
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const root = resolve(__dirname, '../../..');
    const offenders: string[] = [];
    for (const rel of files) {
      const text = readFileSync(resolve(root, rel), 'utf8');
      // Paragraphs, not lines: markdown and JSDoc both wrap, so a line rule
      // would fire on whichever half the word landed in.
      for (const para of text.split(/\n\s*\n/)) {
        if (!/['"`]drive['"`]/.test(para)) continue;
        if (/remove/i.test(para)) continue;
        offenders.push(`${rel}: ${para.replace(/\s+/g, ' ').slice(0, 120)}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it('carries no third value in the type or in the two resolvers', async () => {
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const root = resolve(__dirname, '../../..');
    // The declaration itself takes the exported union rather than spelling one
    // inline — an inline union is how the type and `resolveUiProfile` came to
    // be able to disagree about how many profiles there are.
    const cfg = readFileSync(resolve(root, 'packages/core/src/types/ExplorerConfig.ts'), 'utf8');
    expect(cfg).toContain('uiProfile?: UiProfile;');
    // And nothing branches on the retired name any more.
    for (const rel of [
      'packages/core/src/FileExplorer.vue',
      'packages/webcomponent/src/index.ts',
    ]) {
      expect(readFileSync(resolve(root, rel), 'utf8')).not.toMatch(/['"]drive['"]/);
    }
  });
});
