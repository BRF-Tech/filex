// The links a release body actually links to, for the shop-window check's
// dead-link pass (scripts/check-shop-window.mjs → checkReleasePages).
//
// ⚠ Code is not a link. v0.47.0's notes show the sub-path setting as
// `FILEX_PUBLIC_URL=https://example.com/filex`; GitHub renders that as code and
// links nothing, but a regex over the raw body requested example.com and
// failed the release on two 404s the page does not have (2026-09-27). Fenced
// blocks and inline code spans are therefore removed before extracting.
//
// ⚠ An address with an ellipsis in it is prose describing a URL shape, not a
// link: v0.36.0's notes quote the broken `https://github.com/…/filex/-/issues`
// form they fixed (measured on v0.41.0).

/** @param {string | null | undefined} body @returns {string[]} */
export function linksInReleaseBody(body) {
  const text = (body ?? '').replace(/```[\s\S]*?```/g, '').replace(/`[^`\n]*`/g, '');
  const out = new Set();
  for (const m of text.match(/https?:\/\/[^\s)>\]]+/g) ?? []) {
    if (m.includes('…')) continue;
    out.add(m.replace(/[.,;:`]+$/, ''));
  }
  return [...out];
}

// ⚠ Some hosts refuse HEAD but answer GET: apps.microsoft.com gives 403 to any
// HEAD, and to a GET it gives 200 for a listed product and 410 for a missing
// one (measured 2026-09-27). A 403 or 405 to HEAD is asked again with GET, so
// a dead Store link still fails and a live one no longer does.
export const RETRY_WITH_GET = new Set([403, 405]);
