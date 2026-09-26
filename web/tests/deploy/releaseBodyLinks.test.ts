// What the shop-window check requests when it looks for dead links in release
// bodies (scripts/release-body-links.mjs). The lines are the ones v0.47.0 was
// published with: its deploy gate failed on example.com URLs that sit in code
// and on a Store listing that refuses HEAD (2026-09-27).
import { describe, expect, it } from 'vitest';

import { RETRY_WITH_GET, linksInReleaseBody } from '../../../scripts/release-body-links.mjs';

describe('links in a release body', () => {
  it('a URL in an inline code span is an example, not a link', () => {
    const body = [
      '> `FILEX_PUBLIC_URL` has a path (`https://example.com/filex`) and the proxy',
      '- **filex runs under a sub-path — `https://example.com/filex/` behind a',
      '    with `FILEX_PUBLIC_URL=https://example.com/filex`, whose path is then the',
    ].join('\n');
    expect(linksInReleaseBody(body)).toEqual([]);
  });

  it('a URL in a fenced block is not a link either', () => {
    expect(linksInReleaseBody('```\ncurl https://example.com/filex/api\n```')).toEqual([]);
  });

  it('markdown links and bare URLs are kept, trailing punctuation dropped', () => {
    const body = [
      '  [filex File Manager](https://apps.microsoft.com/detail/9PKXDJLVZWXW): the',
      'Docs: https://docs.filex.sh/guide/.',
    ].join('\n');
    expect(linksInReleaseBody(body)).toEqual([
      'https://apps.microsoft.com/detail/9PKXDJLVZWXW',
      'https://docs.filex.sh/guide/',
    ]);
  });

  it('a link next to a code span on the same line still counts', () => {
    expect(linksInReleaseBody('set `FOO=1`, see https://docs.filex.sh/x')).toEqual(['https://docs.filex.sh/x']);
  });

  it('an address with an ellipsis describes a shape and is skipped', () => {
    expect(linksInReleaseBody('the broken https://github.com/…/filex/-/issues form')).toEqual([]);
  });

  it('the same link twice is requested once', () => {
    expect(linksInReleaseBody('https://filex.sh and https://filex.sh')).toEqual(['https://filex.sh']);
  });

  it('a HEAD refused with 403 or 405 is asked again with GET, a 404 is not', () => {
    expect(RETRY_WITH_GET.has(403)).toBe(true);
    expect(RETRY_WITH_GET.has(405)).toBe(true);
    expect(RETRY_WITH_GET.has(404)).toBe(false);
    expect(RETRY_WITH_GET.has(410)).toBe(false);
  });
});
