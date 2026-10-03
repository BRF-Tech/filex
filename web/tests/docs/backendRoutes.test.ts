// Every `### METHOD /path` heading in docs/BACKEND.md names a route the server
// really registers in backend/internal/api/routes.go.
//
// ⚠ Why (task #117, 2026-09-28): four headings had pointed at routes that did
// not exist for several releases - `GET /api/files/raw`, `POST
// /api/files/mkdir`, `POST /api/share/:token/verify` and `GET
// /api/share/:token/download` all answered 404 when measured against v0.48.1.
// An integrator reads the heading, sends the request, gets the router's own
// "404 page not found" and has nothing to go on. check-links, the docs-site
// build and the anchor checker all pass on such a page, because it is well
// formed; only a comparison with the router catches it.
//
// HOW: routes.go is read as text (no Go toolchain needed here): comments are
// blanked, string literals kept, and the nesting of `r.Route("/prefix",
// func(r chi.Router) {...})` and `r.Group(func(r chi.Router) {...})` is
// followed brace by brace, so `r.With(write).Post("/copy", ...)` inside
// `r.Route("/api/files", ...)` becomes `POST /api/files/copy`. A route whose
// path is not a string literal (a prefix held in a variable, a handler that
// registers its own sub-router) is not counted, so it can never make a wrong
// heading pass.
//
// A heading matches when the method agrees (or the route is a Handle/Mount,
// which takes every method) and every segment agrees: a `{param}` (`:param` in
// the doc is the same thing) matches a `{param}` of any name, a literal only
// the same literal, and a route's trailing `*` the rest. The query string is
// not part of the route (`?action=rename` is the manager's verb). A heading
// that starts with `…/x` continues the previous code span of the same heading
// (`GET /a/{b}` · `POST …/event`), or, standing alone, matches any route that
// ends in `/x`.
//
// WHEN IT FIRES: either the route is gone (rewrite the section to the route
// that does the job, or delete it) or the heading is misspelt. Do not add an
// allow list: a documented route that is not served is exactly what this
// test exists to refuse.
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import { ANY, joinPath, parseRoutes, segmentsMatch, type Route } from '../helpers/goRoutes';

const REPO = path.resolve(__dirname, '../../..');
const ROUTES_GO = path.join(REPO, 'backend/internal/api/routes.go');
const BACKEND_MD = path.join(REPO, 'docs/BACKEND.md');

type DocRoute = { methods: string[]; path: string; suffixOnly: boolean; line: number; heading: string };

const METHOD_WORD = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)(\|(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS))*$/;

function normaliseDocPath(p: string): string {
  let out = p.split('?')[0];
  out = out.replace(/:([A-Za-z_][A-Za-z0-9_]*)/g, '{$1}');
  if (out.length > 1 && out.endsWith('/')) out = out.slice(0, -1);
  return out;
}

function parseDocHeadings(md: string): DocRoute[] {
  const out: DocRoute[] = [];
  const lines = md.split('\n');
  let inFence = false;
  lines.forEach((raw, idx) => {
    if (/^\s*```/.test(raw)) inFence = !inFence;
    if (inFence) return;
    const h = /^#{2,4}\s+(.*)$/.exec(raw);
    if (!h) return;
    const spans = [...h[1].matchAll(/`([^`]+)`/g)].map((m) => m[1].trim());
    let base: string | null = null;
    for (const span of spans) {
      const sm = /^(\S+)\s+(\S+)/.exec(span);
      if (!sm || !METHOD_WORD.test(sm[1])) continue;
      const methods = sm[1].split('|');
      const rawPath = sm[2];
      if (rawPath.startsWith('…')) {
        const rest = normaliseDocPath(rawPath.slice(1));
        if (base) {
          out.push({ methods, path: joinPath([base, rest]), suffixOnly: false, line: idx + 1, heading: raw });
        } else {
          out.push({ methods, path: rest, suffixOnly: true, line: idx + 1, heading: raw });
        }
        continue;
      }
      if (!rawPath.startsWith('/')) continue;
      const p = normaliseDocPath(rawPath);
      base = p;
      out.push({ methods, path: p, suffixOnly: false, line: idx + 1, heading: raw });
    }
  });
  return out;
}

function served(doc: { methods: string[]; path: string; suffixOnly: boolean }, routes: Route[], method: string): boolean {
  const docSegs = doc.path.split('/').filter(Boolean);
  return routes.some((r) => {
    if (r.method !== ANY && r.method !== method) return false;
    const routeSegs = r.path.split('/').filter(Boolean);
    if (doc.suffixOnly) {
      if (routeSegs.length < docSegs.length) return false;
      return segmentsMatch(docSegs, routeSegs.slice(routeSegs.length - docSegs.length));
    }
    return segmentsMatch(docSegs, routeSegs);
  });
}

describe('docs/BACKEND.md names only routes the server registers', () => {
  const routes = parseRoutes(readFileSync(ROUTES_GO, 'utf8'));
  const headings = parseDocHeadings(readFileSync(BACKEND_MD, 'utf8'));

  it('reads routes.go (a parser that finds nothing would pass everything)', () => {
    expect(routes.length).toBeGreaterThan(300);
    const has = (m: string, p: string) => routes.some((r) => r.method === m && r.path === p);
    // One from each kind of nesting: a bare route, a Route block, a With()
    // chain inside nested Route blocks, and a group inside /api/admin.
    expect(has('GET', '/s/{token}')).toBe(true);
    expect(has('POST', '/api/files/copy')).toBe(true);
    expect(has('POST', '/api/files/plugins/actions/{plugin}/{action}/run')).toBe(true);
    expect(has('PUT', '/api/admin/storages/order')).toBe(true);
  });

  it('reads the headings, and the matcher refuses what is not served', () => {
    expect(headings.length).toBeGreaterThan(80);
    // The four the denial was about: none of them may ever match again.
    for (const [m, p] of [
      ['GET', '/api/files/raw'],
      ['POST', '/api/files/mkdir'],
      ['POST', '/api/share/{token}/verify'],
      ['GET', '/api/share/{token}/download'],
    ] as const) {
      expect(served({ methods: [m], path: p, suffixOnly: false }, routes, m), `${m} ${p}`).toBe(false);
    }
    // A literal is not a parameter, and the method counts.
    expect(served({ methods: ['GET'], path: '/api/files/ops/cancel', suffixOnly: false }, routes, 'GET')).toBe(false);
    expect(served({ methods: ['DELETE'], path: '/api/files/copy', suffixOnly: false }, routes, 'DELETE')).toBe(false);
  });

  it('every `### METHOD /path` heading is a registered route', () => {
    const missing: string[] = [];
    for (const h of headings) {
      for (const m of h.methods) {
        if (!served(h, routes, m)) missing.push(`BACKEND.md:${h.line} ${m} ${h.suffixOnly ? '…' : ''}${h.path}`);
      }
    }
    expect(missing, 'documented but not registered in routes.go').toEqual([]);
  });
});
