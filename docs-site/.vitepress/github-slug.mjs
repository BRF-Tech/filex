// GitHub's heading-anchor slug rule, so this site's anchors match the ones the
// same markdown gets when it is read on github.com.
//
// ⚠⚠ Why the site follows GitHub and not the other way round: every
// `docs/*.md` page is read on BOTH surfaces. The repo README links
// `docs/X.md#section` relatively (GitHub resolves those against its own
// rendering), and both npm package READMEs link absolutely at
// `github.com/BRF-Tech/filex/blob/main/docs/MCP.md#…`. The in-page tables of
// contents in `docs/` were written against GitHub and landed there. VitePress's
// default slugify disagreed with GitHub on every heading containing `&`, `/`,
// an em dash, a dot, an apostrophe or a leading number — 98 links, measured
// 2026-09-05, that worked on GitHub and died on docs.filex.sh. Rewriting the
// 98 links to VitePress's ids would only have moved the breakage to GitHub, so
// the slug rule moved instead.
//
// The rule is html-pipeline's TOC filter, which is what github.com runs:
// downcase, delete every character that is not a word character, a hyphen or a
// space, then turn spaces into hyphens. Note what it does NOT do — it does not
// collapse the run of hyphens left behind by a deleted `&` or em dash, which
// is why `## Backup & restore` is `#backup--restore` with two.
//
// Duplicate headings get `-1`, `-2` … from markdown-it-anchor, matching GitHub.
//
// ⚠ What the switch cost, measured after the fact (both commits built, the
// emitted `id=` attributes diffed page by page): 245 of 666 headings on
// docs.filex.sh changed spelling, none disappeared. No `<a id>` aliases were
// added for the old ones, and the reasoning is written down beside the anchor
// check in `docs/CONTRIBUTING.md` -- read it before touching this rule again.
//
// Verified against api.github.com/markdown; the fixtures are in
// `web/tests/docs/anchorSlug.test.ts` and cover every punctuation shape that
// occurs in these docs. Change this function only with a fresh measurement —
// guessing at it is what produced the divergence in the first place.
export function githubSlug(str) {
  return str
    .toLowerCase()
    .replace(/[^\p{L}\p{M}\p{Nd}\p{Pc}\- ]/gu, '')
    .replace(/ /g, '-');
}
