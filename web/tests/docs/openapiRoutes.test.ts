// backend/internal/api/openapi.json, the OpenAPI description of `/api/files`
// and `/api/ai`, is held to backend/internal/api/routes.go in BOTH directions:
// every operation in it is a route the server registers, and every `/api/files`
// and `/api/ai` route the server registers is in it.
//
// ⚠ Why (task #119, API/MCP coverage audit 2026-09-28): there was no schema at
// all, and the one hand-kept description, docs/BACKEND.md, had named four
// routes for several releases that answered 404 (task #117's gate,
// backendRoutes.test.ts, now refuses that). A schema is worse than none when
// it describes a route that is gone, and nearly as bad when a new route never
// reaches it - an integrator generating a client from it would not know the
// route exists. The first check is that gate applied to the schema; the second
// is what keeps it whole.
//
// WHEN IT FIRES: a route was added, removed or renamed in routes.go under
// /api/files or /api/ai. Add (or remove) the operation in openapi.json with
// the same method and path; a `{param}` matches a `{param}` of any name. Do
// not add an allow list. The admin API (`/api/admin`, `/api/ai/admin`) is not
// in the schema and not asked about.
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import { ANY, parseRoutes, segmentsMatch, type Route } from '../helpers/goRoutes';

const REPO = path.resolve(__dirname, '../../..');
const ROUTES_GO = path.join(REPO, 'backend/internal/api/routes.go');
const SPEC = path.join(REPO, 'backend/internal/api/openapi.json');

const HTTP_METHODS = ['get', 'put', 'post', 'delete', 'options', 'head', 'patch', 'trace'];

type Parameter = { name: string; in: string; required?: boolean };
type Operation = { operationId?: string; parameters?: Parameter[]; responses?: Record<string, unknown> };
type Spec = { openapi: string; paths: Record<string, Record<string, Operation>> };
type Op = { method: string; path: string; op: Operation };

const inScope = (p: string) =>
  (p === '/api/files' || p.startsWith('/api/files/') || p === '/api/ai' || p.startsWith('/api/ai/')) &&
  !p.startsWith('/api/ai/admin');

const segs = (p: string) => p.split('/').filter(Boolean);

function operations(spec: Spec): Op[] {
  const out: Op[] = [];
  for (const [p, item] of Object.entries(spec.paths)) {
    for (const [m, op] of Object.entries(item)) {
      if (HTTP_METHODS.includes(m)) out.push({ method: m.toUpperCase(), path: p, op });
    }
  }
  return out;
}

function served(op: { method: string; path: string }, routes: Route[]): boolean {
  return routes.some((r) => (r.method === ANY || r.method === op.method) && segmentsMatch(segs(op.path), segs(r.path)));
}

function described(route: Route, ops: Op[]): boolean {
  return ops.some((o) => (route.method === ANY || o.method === route.method) && segmentsMatch(segs(o.path), segs(route.path)));
}

describe('backend/internal/api/openapi.json describes exactly the /api/files and /api/ai routes', () => {
  const routes = parseRoutes(readFileSync(ROUTES_GO, 'utf8'));
  const spec = JSON.parse(readFileSync(SPEC, 'utf8')) as Spec;
  const ops = operations(spec);
  const ours = routes.filter((r) => inScope(r.path));

  it('reads both sides (a parser or a schema that finds nothing would pass everything)', () => {
    expect(ours.length).toBeGreaterThan(90);
    expect(ops.length).toBeGreaterThan(90);
    // A route that is not served is refused: the four BACKEND.md once named.
    for (const [m, p] of [
      ['GET', '/api/files/raw'],
      ['POST', '/api/files/mkdir'],
    ] as const) {
      expect(served({ method: m, path: p }, routes), `${m} ${p}`).toBe(false);
    }
    // The method counts, and a literal is not a parameter.
    expect(served({ method: 'DELETE', path: '/api/files/copy' }, routes)).toBe(false);
    expect(served({ method: 'GET', path: '/api/files/ops/cancel' }, routes)).toBe(false);
  });

  it('is OpenAPI 3.1, every operation is named once, and every path parameter is declared', () => {
    expect(spec.openapi).toMatch(/^3\.1\./);
    const ids = new Map<string, string>();
    const problems: string[] = [];
    for (const o of ops) {
      const where = `${o.method} ${o.path}`;
      const id = o.op.operationId;
      if (!id) problems.push(`${where}: no operationId`);
      else if (ids.has(id)) problems.push(`${where}: operationId ${id} also on ${ids.get(id)}`);
      else ids.set(id, where);
      if (!o.op.responses || Object.keys(o.op.responses).length === 0) problems.push(`${where}: no responses`);
      const declared = new Set((o.op.parameters ?? []).filter((p) => p.in === 'path').map((p) => p.name));
      for (const name of o.path.match(/\{([^}]+)\}/g)?.map((x) => x.slice(1, -1)) ?? []) {
        if (!declared.has(name)) problems.push(`${where}: path parameter {${name}} not declared`);
      }
    }
    // Two templates that differ only in their parameters' names are one path.
    const shapes = new Map<string, string>();
    for (const p of Object.keys(spec.paths)) {
      const shape = p.replace(/\{[^}]+\}/g, '{}');
      if (shapes.has(shape)) problems.push(`${p} and ${shapes.get(shape)} are the same path`);
      shapes.set(shape, p);
    }
    expect(problems).toEqual([]);
  });

  it('names only routes the server registers, and only /api/files and /api/ai', () => {
    const wrong = ops
      .filter((o) => !inScope(o.path) || !served(o, routes))
      .map((o) => `${o.method} ${o.path}`);
    expect(wrong, 'in openapi.json but not a registered /api/files or /api/ai route').toEqual([]);
  });

  it('leaves no /api/files or /api/ai route out', () => {
    const missing = ours.filter((r) => !described(r, ops)).map((r) => `routes.go:${r.line} ${r.method} ${r.path}`);
    expect(missing, 'registered in routes.go but not in openapi.json').toEqual([]);
  });
});
