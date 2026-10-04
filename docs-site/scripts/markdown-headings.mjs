/**
 * markdown-headings.mjs - the ONE reader of a docs page's headings.
 *
 * Two places need to know which lines of a page are headings: the release gate
 * (scripts/release/checks.mjs `headingsOf`, "is the site a stale snapshot?")
 * and the Releases page generator (fetch-releases.mjs `headingIds`, "does this
 * #fragment still land?"). They each had their own fence tracker; the second
 * one closed a fence on ANY line starting with the same character, so a `#`
 * line inside a code block could be a heading for one and not for the other
 * (task #148, found by #142 / lesson #964).
 *
 * It lives in docs-site/scripts, not scripts/release, because the docs server
 * runs fetch-releases.mjs from a snapshot that is guaranteed to hold docs-site
 * and docs, and nothing else.
 *
 * CommonMark fence rules: a run of three or more backticks or tildes, indented
 * at most three spaces, opens a fence; a run of the same character at least as
 * long, with nothing after it, closes it; a fence never closed runs to the end
 * of the page. A backtick run with another backtick on its line is inline code,
 * not a fence. YAML front matter at the top of a page is not part of the page.
 */

/**
 * Every heading line outside a code fence and front matter:
 * `[{ level, text }]`, `text` being the raw markdown after the `#`s with an
 * optional closing run of `#` removed.
 */
export function headingLines(markdown) {
  const lines = String(markdown).split(/\r\n|\n|\r/);
  let i = 0;
  if (lines[0] === '---') {
    const end = lines.findIndex((l, n) => n > 0 && /^---\s*$/.test(l));
    if (end > 0) i = end + 1;
  }
  const out = [];
  let fence = null;
  for (; i < lines.length; i++) {
    const line = lines[i];
    if (fence) {
      const close = /^ {0,3}(`{3,}|~{3,})[ \t]*$/.exec(line);
      if (close && close[1][0] === fence[0] && close[1].length >= fence.length) fence = null;
      continue;
    }
    const open = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (open && !(open[1][0] === '`' && open[2].includes('`'))) {
      fence = open[1];
      continue;
    }
    const h = /^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$/.exec(line);
    if (h && h[2] !== '') out.push({ level: h[1].length, text: h[2] });
  }
  return out;
}
