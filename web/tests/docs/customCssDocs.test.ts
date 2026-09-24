// No living document may still say the operator stylesheet rides
// `GET /api/branding`.
//
// ⚠⚠ This one is not a tidiness check. The stylesheet was MOVED off that
// payload because `/api/branding` is public — the sign-in page fetches it
// before a session exists — so the sheet reached anonymous visitors and the
// very form they sign in with. It is served from `GET /api/me/custom-css`
// behind authentication now, and the `custom_css` field was DELETED from the
// branding payload rather than blanked, so an old client fails loudly instead
// of quietly rendering nothing (`backend/.../custom_css.go`,
// `custom_css_serve.go`, and `e2e/tests/98-custom-theme.spec.ts` which asserts
// both an anonymous 401 and `Object.keys(branding)` not containing it).
//
// A page describing the old shape is therefore not merely stale: it tells an
// operator that a stylesheet they paste in will style the sign-in page and be
// public, which is a claim about a security boundary that has moved. A page
// that lies about a boundary is worse than one that says nothing — so this
// test exists to make the prose breakable, because `check-links`, the
// docs-site build and the anchor checker all pass happily on a page that is
// beautifully formatted and false.
//
// ⚠ WHAT IS DELIBERATELY NOT SCANNED: `CHANGELOG.md`, `docs/RELEASES.md`,
// `docs-site/data/releases.json` and the `HANDOVER-*` files. Those are the
// historical record. The v0.41.0 entry saying the sheet "is served with the
// branding payload … the sign-in page included" was TRUE of v0.41.0, and
// rewriting history to match the present is how a changelog stops being
// usable for working out which version changed what. `docs/RELEASES.md` is
// additionally a generated file ("GENERATED FILE — do not edit by hand"), so
// a hand-edit there would be overwritten on the next fetch anyway.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '../../..');

/** Files that describe the product AS IT IS. */
function livingDocs(): string[] {
  const out: string[] = [];
  const docs = path.join(REPO, 'docs');
  for (const name of readdirSync(docs)) {
    const p = path.join(docs, name);
    if (statSync(p).isDirectory()) continue; // docs/handovers — history
    if (!name.endsWith('.md')) continue;
    if (name === 'RELEASES.md') continue; // generated archive, see the header
    out.push(p);
  }
  for (const name of readdirSync(REPO)) {
    if (!name.endsWith('.md')) continue;
    if (name === 'CHANGELOG.md' || name.startsWith('HANDOVER-')) continue;
    out.push(path.join(REPO, name));
  }
  for (const pkg of ['core', 'react', 'webcomponent']) {
    out.push(path.join(REPO, 'packages', pkg, 'README.md'));
  }
  out.push(path.join(REPO, 'desktop', 'README.md'));
  return out.filter((p) => statSync(p).isFile());
}

/**
 * The exact phrasings the retired design was written in. Regexes rather than a
 * "mentions both words" rule on purpose: the corrected pages legitimately name
 * the old payload in order to say the sheet is NOT on it any more, and a rule
 * that cannot tell a correction from the claim it corrects would either fire
 * on every fix or on nothing.
 */
const RETIRED: Array<[name: string, re: RegExp]> = [
  ['"it rides this payload"', /\brides this payload\b/i],
  ['"delivered on the public GET /api/branding payload"', /delivered on the public\s+`?GET \/api\/branding`?\s+payload/i],
  ['"/api/branding … carries the operator\'s custom stylesheet"', /carries the operator's custom stylesheet and the SSO button label/i],
  ['the editor under Settings', /\*\*Settings\s*(->|→)\s*Custom CSS\*\*/i],
  ['"applies … on every page, including the login screen"', /on every page\*\*,?\s*\n?\s*including\s*\n?\s*the login screen/i],
  ['a 60-second cache window for the sheet', /`Cache-Control: public, max-age=60`[^.]*previous sheet/i],
];

describe('documentation about the operator stylesheet', () => {
  const files = livingDocs();

  it('finds documents to scan at all', () => {
    // A path that silently resolved to nothing would make every assertion
    // below vacuously true — the most comfortable kind of dead gate.
    expect(files.length).toBeGreaterThan(30);
    expect(files.some((f) => f.endsWith('INTEGRATION.md'))).toBe(true);
    expect(files.some((f) => f.endsWith('API.md'))).toBe(true);
    expect(files.some((f) => f.endsWith('BACKEND.md'))).toBe(true);
  });

  it('⚠⚠ no living page still describes the retired, public shape', () => {
    const offenders: string[] = [];
    for (const f of files) {
      const src = readFileSync(f, 'utf8');
      for (const [name, re] of RETIRED) {
        if (re.test(src)) offenders.push(`${path.relative(REPO, f)} → ${name}`);
      }
    }
    expect(offenders, `documents describing the pre-move custom CSS:\n${offenders.join('\n')}`).toEqual([]);
  });

  it('INTEGRATION.md names the authenticated endpoint, the gate and the screen', () => {
    const src = readFileSync(path.join(REPO, 'docs/INTEGRATION.md'), 'utf8');
    expect(src).toContain('GET /api/me/custom-css');
    expect(src).toContain('ui.custom_css_enabled');
    expect(src).toContain('/admin/appearance');
    // The heading itself must not be renamed: CHANGELOG.md and
    // docs/RELEASES.md both deep-link `INTEGRATION.md#operator-custom-css`,
    // and those two are history and will not be rewritten to follow it.
    expect(src).toContain('#### Operator custom CSS');
  });

  it('API.md no longer advertises the field on the public payload', () => {
    const src = readFileSync(path.join(REPO, 'docs/API.md'), 'utf8');
    expect(src).not.toMatch(/`GET \/api\/branding`\s*→\s*`custom_css`/);
    expect(src).toContain('`GET /api/me/custom-css`');
  });

  it('BACKEND.md documents the route it calls itself the complete reference for', () => {
    const src = readFileSync(path.join(REPO, 'docs/BACKEND.md'), 'utf8');
    expect(src).toContain('### `GET /api/me/custom-css`');
  });

  it('⚠ the SPA type does not promise a field the wire no longer sends', () => {
    // `BrandingConfig.custom_css: string` outlived the payload: a required
    // string that arrives `undefined`, with every reader silently styling
    // nothing and TypeScript agreeing that all is well.
    const src = readFileSync(path.join(REPO, 'web/src/api/branding.ts'), 'utf8');
    expect(src).not.toMatch(/^\s*custom_css\s*[?]?\s*:/m);
  });
});
