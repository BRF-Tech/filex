// routes.go, read as text: every route the server registers, as `METHOD
// /path`. Shared by the tests that hold a document to the router
// (docs/backendRoutes.test.ts for docs/BACKEND.md's headings,
// docs/openapiRoutes.test.ts for backend/internal/api/openapi.json).
//
// HOW: comments are blanked, string literals kept, and the nesting of
// `r.Route("/prefix", func(r chi.Router) {...})` and `r.Group(func(r
// chi.Router) {...})` is followed brace by brace, so `r.With(write).Post("/copy",
// ...)` inside `r.Route("/api/files", ...)` becomes `POST /api/files/copy`. A
// route whose path is not a string literal (a prefix held in a variable, a
// handler that registers its own sub-router) is not counted, so it can never
// make a wrong document pass.

export type Route = { method: string; path: string; line: number };

export const ANY = '*';
export const METHOD_CALLS: Record<string, string> = {
  Get: 'GET',
  Post: 'POST',
  Put: 'PUT',
  Patch: 'PATCH',
  Delete: 'DELETE',
  Head: 'HEAD',
  Options: 'OPTIONS',
  Connect: 'CONNECT',
  Trace: 'TRACE',
  Handle: ANY,
  HandleFunc: ANY,
  Mount: ANY,
};

/**
 * Two views of the Go source, both the same length as the source so an index
 * means the same place in all three: `code` has comments blanked, `masked`
 * has string and rune literal CONTENTS blanked as well (the quotes stay), so
 * a brace inside "/{id}" does not count as a block.
 */
export function goViews(src: string): { code: string; masked: string } {
  const code = src.split('');
  const masked = src.split('');
  const blank = (arr: string[], i: number) => {
    if (arr[i] !== '\n') arr[i] = ' ';
  };
  let i = 0;
  while (i < src.length) {
    const c = src[i];
    const n = src[i + 1];
    if (c === '/' && n === '/') {
      while (i < src.length && src[i] !== '\n') {
        blank(code, i);
        blank(masked, i);
        i++;
      }
      continue;
    }
    if (c === '/' && n === '*') {
      const end = src.indexOf('*/', i + 2);
      const stop = end < 0 ? src.length : end + 2;
      for (; i < stop; i++) {
        blank(code, i);
        blank(masked, i);
      }
      continue;
    }
    if (c === '"' || c === "'") {
      i++;
      while (i < src.length && src[i] !== c && src[i] !== '\n') {
        if (src[i] === '\\') {
          blank(masked, i);
          i++;
        }
        blank(masked, i);
        i++;
      }
      i++;
      continue;
    }
    if (c === '`') {
      i++;
      while (i < src.length && src[i] !== '`') {
        blank(masked, i);
        i++;
      }
      i++;
      continue;
    }
    i++;
  }
  return { code: code.join(''), masked: masked.join('') };
}

/** The string literal starting at `at` (skipping spaces), or null. */
export function literalAt(code: string, at: number): { value: string; end: number } | null {
  let i = at;
  while (code[i] === ' ' || code[i] === '\t' || code[i] === '\n' || code[i] === '\r') i++;
  if (code[i] !== '"') return null;
  const close = code.indexOf('"', i + 1);
  if (close < 0) return null;
  // The paths in routes.go hold no escapes; a literal that does is not a path.
  const value = code.slice(i + 1, close);
  if (value.includes('\\')) return null;
  return { value, end: close + 1 };
}

/** What follows the first argument: a `func(` body, or something else. */
export function opensFuncBody(masked: string, from: number): number {
  const rest = masked.slice(from, from + 80);
  const m = /^\s*,?\s*func\s*\(\s*r\s+chi\.Router\s*\)\s*\{/.exec(rest);
  return m ? from + m[0].length - 1 : -1;
}

export function joinPath(parts: string[]): string {
  const joined = parts.join('').replace(/\/{2,}/g, '/');
  if (joined.length > 1 && joined.endsWith('/')) return joined.slice(0, -1);
  return joined || '/';
}

export function parseRoutes(src: string): Route[] {
  const { code, masked } = goViews(src);
  // Call sites: `.Route(`, `.Group(` and every registering method.
  const call = /\.(Route|Group|Get|Post|Put|Patch|Delete|Head|Options|Connect|Trace|Handle|HandleFunc|Mount|Method|MethodFunc)\(/g;
  type Pending = { brace: number; prefix: string | null };
  const pending: Pending[] = [];
  const events: { at: number; kind: string; argAt: number }[] = [];
  for (let m = call.exec(masked); m; m = call.exec(masked)) {
    // Only the router: `r.Get(`, or a chain ending in `)` such as
    // `r.With(write).Post(`. `q.Get("...")` on a query is not a route.
    const before = masked.slice(Math.max(0, m.index - 40), m.index);
    if (!/(^|[^A-Za-z0-9_])r$/.test(before) && !before.endsWith(')')) continue;
    events.push({ at: m.index, kind: m[1], argAt: m.index + m[0].length });
  }

  const routes: Route[] = [];
  const stack: { level: number; prefix: string | null }[] = [];
  let depth = 0;
  let cursor = 0;
  const lineOf = (i: number) => src.slice(0, i).split('\n').length;
  const advanceTo = (limit: number) => {
    for (; cursor < limit; cursor++) {
      const ch = masked[cursor];
      if (ch === '{') {
        depth++;
        const p = pending.findIndex((x) => x.brace === cursor);
        if (p >= 0) {
          stack.push({ level: depth, prefix: pending[p].prefix });
          pending.splice(p, 1);
        }
      } else if (ch === '}') {
        depth--;
        while (stack.length && stack[stack.length - 1].level > depth) stack.pop();
      }
    }
  };
  const currentPrefix = (): string | null => {
    const parts: string[] = [];
    for (const s of stack) {
      if (s.prefix === null) return null;
      parts.push(s.prefix);
    }
    return parts.join('');
  };

  for (const ev of events) {
    advanceTo(ev.at);
    if (ev.kind === 'Group') {
      const brace = opensFuncBody(masked, ev.argAt);
      if (brace >= 0) pending.push({ brace, prefix: '' });
      continue;
    }
    if (ev.kind === 'Route') {
      const lit = literalAt(code, ev.argAt);
      const after = lit ? lit.end : ev.argAt;
      const brace = opensFuncBody(masked, after);
      if (brace >= 0) pending.push({ brace, prefix: lit && lit.value.startsWith('/') ? lit.value : null });
      continue;
    }
    let method: string;
    let pathLit: { value: string; end: number } | null;
    if (ev.kind === 'Method' || ev.kind === 'MethodFunc') {
      const methodLit = literalAt(code, ev.argAt);
      let afterMethod: number;
      if (methodLit) {
        method = methodLit.value.toUpperCase();
        afterMethod = methodLit.end;
      } else {
        const mm = /^\s*http\.Method([A-Za-z]+)/.exec(code.slice(ev.argAt, ev.argAt + 40));
        if (!mm) continue;
        method = mm[1].toUpperCase();
        afterMethod = ev.argAt + mm[0].length;
      }
      const comma = code.indexOf(',', afterMethod);
      pathLit = comma < 0 ? null : literalAt(code, comma + 1);
    } else {
      method = METHOD_CALLS[ev.kind];
      pathLit = literalAt(code, ev.argAt);
    }
    if (!pathLit || !pathLit.value.startsWith('/')) continue;
    const prefix = currentPrefix();
    if (prefix === null) continue;
    routes.push({ method, path: joinPath([prefix, pathLit.value]), line: lineOf(ev.at) });
  }
  return routes;
}

export const isParam = (s: string) => /^\{[^}]+\}$/.test(s);

export function segmentsMatch(doc: string[], route: string[]): boolean {
  for (let i = 0; i < route.length; i++) {
    const r = route[i];
    if (r === '*') return doc.length >= i;
    const d = doc[i];
    if (d === undefined) return false;
    if (isParam(r)) {
      if (!isParam(d)) return false;
      continue;
    }
    if (r !== d) return false;
  }
  return doc.length === route.length;
}
