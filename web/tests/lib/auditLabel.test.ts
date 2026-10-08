// The gate that keeps audit rows readable.
//
// The dashboard's "Recent activity" card printed the wire names the server
// stores - `user.update` over `admin@… · user:12` - in English on a Turkish
// panel. Since 0.54 the SERVER says every row (handlers/audit_label.go: a
// translated resource and verb joined by one phrase, `label` and
// `target_label` on each row, and the "What" filter's `resources`) in the
// screen's language; the panel composes nothing. This file reads the Go that
// WRITES the rows and fails when a fixed action name or target type has no
// words in either language of the SERVER catalogue
// (backend/internal/srvtext/locales), so a new audit case cannot ship as a raw
// name. How a row reads (the phrase, the AI mark, the target's kind and name)
// is held by handlers/audit_label_test.go.
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const here = path.dirname(fileURLToPath(import.meta.url));
const MIDDLEWARE = path.resolve(here, '../../../backend/internal/auth/audit_middleware.go');
const LABELS = path.resolve(here, '../../../backend/internal/api/handlers/audit_label.go');

/** Every `return "<action>", "<target type>", …` the middleware writes.
 *  ⚠ Names may carry digits (`e2e.folder_cleanup`): a pattern of `[a-z_]` alone
 *  never sees them, and what it never sees it never holds to a label. */
function backendPairs(source: string): Array<{ action: string; target: string }> {
  const re = /return\s+"([a-z0-9_.]+)",\s*"([a-z0-9_]*)",/g;
  const out: Array<{ action: string; target: string }> = [];
  for (let m = re.exec(source); m; m = re.exec(source)) out.push({ action: m[1], target: m[2] });
  return out;
}

const SERVER: Record<'en' | 'tr', Record<string, string>> = {
  en: JSON.parse(fs.readFileSync(path.resolve(here, '../../../backend/internal/srvtext/locales/en.json'), 'utf8')),
  tr: JSON.parse(fs.readFileSync(path.resolve(here, '../../../backend/internal/srvtext/locales/tr.json'), 'utf8')),
};

const keyOf = (s: string) => s.replace(/[.-]/g, '_');

/** `ai.file.move` → resource `ai.file`, verb `move` (handlers auditSplit). */
function splitAction(action: string): { resource: string; verb: string } {
  const i = action.lastIndexOf('.');
  if (i <= 0) return { resource: action, verb: '' };
  return { resource: action.slice(0, i), verb: action.slice(i + 1) };
}

const pairs = backendPairs(fs.readFileSync(MIDDLEWARE, 'utf8'));

describe('audit labels', () => {
  it('reads the middleware (a parse that finds nothing proves nothing)', () => {
    expect(pairs.length).toBeGreaterThan(30);
  });

  for (const lang of ['en', 'tr'] as const) {
    const has = (k: string) => typeof SERVER[lang][k] === 'string' && SERVER[lang][k] !== '';

    it(`every fixed action has a ${lang} resource and verb`, () => {
      const missing: string[] = [];
      for (const { action } of pairs) {
        const { resource, verb } = splitAction(action);
        if (!has(`server.audit.resource.${keyOf(resource)}`)) missing.push(`resource ${resource} (${action})`);
        if (!has(`server.audit.verb.${keyOf(verb)}`)) missing.push(`verb ${verb} (${action})`);
      }
      expect([...new Set(missing)]).toEqual([]);
    });

    it(`every target type has a ${lang} name`, () => {
      const missing = [...new Set(pairs.map((p) => p.target).filter((x) => x && !has(`server.audit.target.${x}`)))];
      expect(missing).toEqual([]);
    });
  }

  it('the middleware pattern reads a name with a digit in it (none is written there today)', () => {
    expect(backendPairs('return "e2e.folder_cleanup", "node", nil')).toEqual([{ action: 'e2e.folder_cleanup', target: 'node' }]);
  });

  it('the panel keeps no copy of the words, and no composer', () => {
    for (const cat of [en, tr] as Array<{ audit: Record<string, unknown> }>) {
      for (const gone of ['resource', 'verb', 'target', 'phrase', 'viaAi']) expect(cat.audit[gone], `audit.${gone}`).toBeUndefined();
    }
    expect(fs.existsSync(path.resolve(here, '../../src/lib/auditLabel.ts'))).toBe(false);
  });
});

// ── every OTHER writer of audit rows ─────────────────────────────────────
//
// ⚠ The middleware's fixed cases are not the whole log. Release-candidate
// sweep, 2026-09-21: the Panel printed "app plugins: oluşturuldu" — the
// generic `/api/admin/<segment>.<verb>` fallback for a segment the catalogue
// had never heard of — and handlers write rows of their own
// (`app_plugin.action_run`, `share.pin_revealed`…). These read the Go that
// produces those names too, so a new admin route or a new handler row cannot
// ship as a raw name.

const BACKEND = path.resolve(here, '../../../backend');
const ROUTES = path.join(BACKEND, 'internal/api/routes.go');

/** First path segments under /api/admin that take a mutating verb — each
 *  becomes `<segment>.<create|update|delete>` through the fallback. Chi's
 *  nesting (`r.Route`, `r.Group`) is followed by brace depth. */
function adminSegments(src: string): string[] {
  const start = src.indexOf('r.Route("/api/admin", func(r chi.Router) {');
  if (start < 0) return [];
  const tok = /r(?:\.With\((?:[^()]|\([^()]*\))*\))?\.(Route|Get|Post|Put|Patch|Delete|Mount|Method)\("(\/[^"]*)"|r\.Group\(/y;
  const stack: Array<{ prefix: string; depth: number }> = [];
  const segs = new Set<string>();
  let depth = 0;
  for (let pos = src.indexOf('{', start); pos < src.length; pos++) {
    const c = src[pos];
    if (c === '{') depth++;
    else if (c === '}') {
      depth--;
      while (stack.length && stack[stack.length - 1].depth > depth) stack.pop();
      if (depth === 0) break;
    } else if (c === 'r') {
      tok.lastIndex = pos;
      const m = tok.exec(src);
      if (!m) continue;
      if (m[0].startsWith('r.Group')) stack.push({ prefix: '', depth: depth + 1 });
      else {
        const full = stack.map((s) => s.prefix).join('') + m[2];
        const seg = full.replace(/^\/+/, '').split('/')[0];
        if (['Post', 'Put', 'Patch', 'Delete', 'Mount'].includes(m[1]) && seg && !seg.startsWith('{')) segs.add(seg);
        if (m[1] === 'Route') stack.push({ prefix: m[2], depth: depth + 1 });
      }
      pos = tok.lastIndex - 1;
    }
  }
  return [...segs];
}

/** Every Go file under backend/internal, test files and testdata excluded. */
function goSources(): string[] {
  const out: string[] = [];
  const walk = (dir: string) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      if (e.isDirectory()) {
        if (e.name !== 'testdata') walk(path.join(dir, e.name));
      } else if (e.name.endsWith('.go') && !e.name.endsWith('_test.go')) {
        out.push(fs.readFileSync(path.join(dir, e.name), 'utf8'));
      }
    }
  };
  walk(path.join(BACKEND, 'internal'));
  return out;
}

/** Actions and target types handlers write themselves: `AuditEntry{… Action:
 *  "x.y", TargetType: "z"}`, the constants `AuditAction… = "x.y"` and
 *  `AuditTarget… = "z"` (the encryption policy's rows, e2epolicy/audit.go), and
 *  the app-plugin admin's `h.Audit(ctx, uid, "x.y", …)`.
 *
 *  ⚠ Every name may carry a digit. These read `[a-z_]` only until 2026-10-01,
 *  and `e2e.folder_cleanup`, `e2e.fxe_header_rewritten`, `e2e.key_file_rewritten`
 *  (v0.48.0) and all seven `e2e_policy.*` / `e2e_request.*` / `e2e_tenant.*`
 *  rows were invisible to them: three of those reached a Turkish panel as the
 *  raw English verb and nothing failed. */
function handlerRows(): { actions: string[]; targets: string[] } {
  const actions = new Set<string>();
  const targets = new Set<string>();
  for (const src of goSources()) {
    if (!/AuditEntry\{|AuditAction|AuditTarget|\.Audit\(/.test(src)) continue;
    for (const m of src.matchAll(/Action:\s*"([a-z0-9_]+\.[a-z0-9_.]+)"/g)) actions.add(m[1]);
    for (const m of src.matchAll(/AuditAction\w*\s*=\s*"([a-z0-9_]+\.[a-z0-9_.]+)"/g)) actions.add(m[1]);
    for (const m of src.matchAll(/\.Audit\([^,]+,[^,]+,\s*"([a-z0-9_]+\.[a-z0-9_.]+)"/g)) actions.add(m[1]);
    for (const m of src.matchAll(/TargetType:\s*"([a-z0-9_]+)"/g)) targets.add(m[1]);
    for (const m of src.matchAll(/AuditTarget\w*\s*=\s*"([a-z0-9_]+)"/g)) targets.add(m[1]);
  }
  return { actions: [...actions], targets: [...targets] };
}

const segments = adminSegments(fs.readFileSync(ROUTES, 'utf8'));
const written = handlerRows();

/** The catalogue keys whose wire resource is not the key itself, as
 *  handlers/audit_label.go spells them (auditWireResource). */
function wireResources(src: string): Record<string, string[]> {
  const block = /var auditWireResource = map\[string\]\[\]string\{([\s\S]*?)\n\}/.exec(src)?.[1] ?? '';
  const out: Record<string, string[]> = {};
  for (const m of block.matchAll(/"([a-z0-9_]+)":\s*\{([^}]*)\}/g)) {
    out[m[1]] = [...m[2].matchAll(/"([^"]+)"/g)].map((x) => x[1]);
  }
  return out;
}

describe('audit labels — every writer, not only the middleware', () => {
  it('reads the routes and the handlers (a parse that finds nothing proves nothing)', () => {
    expect(segments.length).toBeGreaterThan(15);
    expect(segments).toContain('app-plugins');
    expect(written.actions).toContain('share.pin_revealed');
    // Names with a digit in them: these scans read `[a-z_]` only until 2026-10-01,
    // and the encryption rows were never seen (see handlerRows).
    expect(written.actions).toContain('e2e_policy.update');
    expect(written.actions).toContain('e2e.folder_cleanup');
    expect(written.targets).toContain('e2e_policy');
  });

  for (const lang of ['en', 'tr'] as const) {
    const has = (k: string) => typeof SERVER[lang][k] === 'string' && SERVER[lang][k] !== '';

    it(`every admin route segment has a ${lang} resource name`, () => {
      const missing = segments.filter((s) => !has(`server.audit.resource.${keyOf(s)}`));
      expect(missing).toEqual([]);
    });

    it(`every action a handler writes has a ${lang} resource and verb`, () => {
      const missing: string[] = [];
      for (const action of written.actions) {
        const { resource, verb } = splitAction(action);
        if (!has(`server.audit.resource.${keyOf(resource)}`)) missing.push(`resource ${resource} (${action})`);
        if (!has(`server.audit.verb.${keyOf(verb)}`)) missing.push(`verb ${verb} (${action})`);
      }
      expect([...new Set(missing)]).toEqual([]);
    });

    it(`every target type a handler writes has a ${lang} name`, () => {
      expect(written.targets.filter((x) => !has(`server.audit.target.${x}`))).toEqual([]);
    });
  }

  // The server's "What" filter: a resource key's prefixes are the key itself
  // unless auditWireResource spells the way back (`.` and `-` both fold to `_`).
  it('the resource filter reaches every resource a row can be written under', () => {
    const wire = wireResources(fs.readFileSync(LABELS, 'utf8'));
    expect(Object.keys(wire)).toContain('login_security');
    const prefixes = Object.keys(SERVER.en)
      .filter((k) => k.startsWith('server.audit.resource.'))
      .map((k) => k.slice('server.audit.resource.'.length))
      .flatMap((key) => (wire[key] ?? [key]).map((w) => `${w}.`));
    const wanted = [
      ...segments,
      ...written.actions.map((a) => splitAction(a).resource),
      ...pairs.map((p) => splitAction(p.action).resource),
    ];
    const missing = [...new Set(wanted)].filter((r) => !prefixes.includes(`${r}.`));
    expect(missing).toEqual([]);
  });
});
