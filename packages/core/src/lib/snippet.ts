/* bul:s3 */
/**
 * Snippet segment parser — the search backend returns content snippets as
 * PLAIN TEXT with matched words wrapped in «guillemets» (contract: no HTML
 * ever crosses the wire). The UI renders highlights by splitting the string
 * into segments and emitting each one as a TEXT node (a <mark> wrapper for
 * matches) — never via innerHTML, so a file whose content contains markup
 * can't inject anything into the page.
 */

export interface SnippetSegment {
  /** Literal text of this segment. Always rendered as a text node. */
  text: string;
  /** True when the segment was «wrapped» — render inside <mark>. */
  match: boolean;
}

/**
 * Split a `«»`-annotated snippet into render segments. An unpaired `«`
 * degrades gracefully: the rest of the string is treated as plain text.
 */
export function snippetSegments(snippet: string): SnippetSegment[] {
  const out: SnippetSegment[] = [];
  if (!snippet) return out;
  const re = /«([^«»]*)»/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(snippet)) !== null) {
    if (m.index > last) out.push({ text: snippet.slice(last, m.index), match: false });
    if (m[1]) out.push({ text: m[1], match: true });
    last = m.index + m[0].length;
  }
  if (last < snippet.length) out.push({ text: snippet.slice(last), match: false });
  return out;
}

/** Matched-in values the search contract allows on a hit. */
export type SearchMatched = 'name' | 'content' | 'both';

/** True when the hit matched (at least partly) inside file CONTENT. */
export function matchedInContent(matched: unknown): boolean {
  return matched === 'content' || matched === 'both';
}
