// The docs site must slug heading anchors exactly the way github.com does.
//
// ⚠⚠ Every `docs/*.md` page is read on two surfaces. The repo README links
// `docs/X.md#section` relatively and both npm package READMEs link at
// `github.com/BRF-Tech/filex/blob/main/docs/MCP.md#…`, so an anchor has to
// land on GitHub AND on docs.filex.sh. It did not: VitePress's default
// slugify disagreed with GitHub on every heading holding `&`, `/`, an em
// dash, a dot, an apostrophe or a leading number, which killed 98 in-page
// links on the site while they still worked on GitHub (measured 2026-09-05).
// `docs-site/.vitepress/github-slug.mjs` is the fix; this test is why nobody
// can "tidy" that regex back into divergence.
//
// Every `want` below was MEASURED, not derived: each heading was POSTed to
// api.github.com/markdown and the id read out of the `user-content-…`
// attribute GitHub returned. If you change the rule, re-measure — do not
// reason about it.

import { describe, expect, it } from 'vitest';
// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore -- plain ESM helper shared with the VitePress config
import { githubSlug } from '../../../docs-site/.vitepress/github-slug.mjs';

const measured: Array<[heading: string, want: string]> = [
  // Em dash: deleted, and the two spaces around it become two hyphens.
  ['Token kinds — `user` vs `app`', 'token-kinds--user-vs-app'],
  // `&` and `/` behave the same way, which is where most of the 98 came from.
  ['Backup & restore', 'backup--restore'],
  ['Failure modes & troubleshooting', 'failure-modes--troubleshooting'],
  ['Linux (davfs2 / GNOME / KDE)', 'linux-davfs2--gnome--kde'],
  // A leading number keeps its digit. VitePress's default prefixed `_`.
  ['1. Create a token in filex', '1-create-a-token-in-filex'],
  // Dots vanish rather than becoming separators.
  ['config.yaml', 'configyaml'],
  ['Folders created before v0.31', 'folders-created-before-v031'],
  // Underscores survive: they are word characters.
  ['Install-time settings (`FILEX_INSTALLATION_*`)', 'install-time-settings-filex_installation_'],
  // Apostrophes vanish; they do NOT split the word.
  ["What happens if it's not configured", 'what-happens-if-its-not-configured'],
  // ⚠ A non-breaking hyphen (U+2011) is not a hyphen to this rule — it is
  // deleted outright, so `Read‑only` slugs as `readonly`. Five headings in
  // docs/ carried one; the anchors pointing at them were dead on BOTH
  // surfaces and invisible in every editor. Do not put one in a heading.
  ['Read‑only mounts', 'readonly-mounts'],
];

describe('github heading slugs', () => {
  for (const [heading, want] of measured) {
    it(`slugs ${JSON.stringify(heading)}`, () => {
      expect(githubSlug(heading)).toBe(want);
    });
  }

  it('leaves a plain heading alone', () => {
    expect(githubSlug('Getting started')).toBe('getting-started');
  });
});
