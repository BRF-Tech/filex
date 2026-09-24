// The gate that keeps audit rows readable.
//
// The dashboard's "Recent activity" card printed the wire names the server
// stores — `user.update` over `admin@… · user:12` — in English on a Turkish
// panel. src/lib/auditLabel.ts composes a label from a translated resource and
// verb instead; this file reads the Go that WRITES the rows and fails when a
// fixed action name or target type has no translation in either language, so
// a new audit case cannot ship as a raw name.
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { auditActionLabel, auditResourceOptions, auditTargetLabel, splitAction } from '@/lib/auditLabel';

const here = path.dirname(fileURLToPath(import.meta.url));
const MIDDLEWARE = path.resolve(here, '../../../backend/internal/auth/audit_middleware.go');

/** Every `return "<action>", "<target type>", …` the middleware writes. */
function backendPairs(source: string): Array<{ action: string; target: string }> {
  const re = /return\s+"([a-z_.]+)",\s*"([a-z_]*)",/g;
  const out: Array<{ action: string; target: string }> = [];
  for (let m = re.exec(source); m; m = re.exec(source)) out.push({ action: m[1], target: m[2] });
  return out;
}

function lookup(catalogue: Record<string, unknown>) {
  const get = (key: string): unknown =>
    key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], catalogue);
  const te = (key: string) => typeof get(key) === 'string';
  const t = (key: string, values: Record<string, unknown> = {}) =>
    String(get(key) ?? key).replace(/\{(\w+)\}/g, (_, k) => String(values[k] ?? ''));
  return { t, te };
}

const keyOf = (s: string) => s.replace(/[.-]/g, '_');
const pairs = backendPairs(fs.readFileSync(MIDDLEWARE, 'utf8'));

describe('audit labels', () => {
  it('reads the middleware (a parse that finds nothing proves nothing)', () => {
    expect(pairs.length).toBeGreaterThan(30);
  });

  for (const [lang, catalogue] of [['en', en], ['tr', tr]] as const) {
    const { t, te } = lookup(catalogue as Record<string, unknown>);

    it(`every fixed action has a ${lang} resource and verb`, () => {
      const missing: string[] = [];
      for (const { action } of pairs) {
        const { resource, verb } = splitAction(action);
        if (!te(`audit.resource.${keyOf(resource)}`)) missing.push(`resource ${resource} (${action})`);
        if (!te(`audit.verb.${keyOf(verb)}`)) missing.push(`verb ${verb} (${action})`);
      }
      expect([...new Set(missing)]).toEqual([]);
    });

    it(`every target type has a ${lang} name`, () => {
      const missing = [...new Set(pairs.map((p) => p.target).filter((x) => x && !te(`audit.target.${x}`)))];
      expect(missing).toEqual([]);
    });

    it(`${lang}: a label is a sentence, not the wire name`, () => {
      const label = auditActionLabel('user.password_reset', t, te);
      expect(label).not.toContain('user.password_reset');
      expect(label).not.toContain('_');
      expect(auditTargetLabel('user', '12', t, te)).toMatch(/#12$/);
    });
  }

  it('an action the catalogue has never heard of is still readable', () => {
    const { t, te } = lookup(en as Record<string, unknown>);
    expect(auditActionLabel('mystery-things.frobnicate', t, te)).toBe('mystery things: frobnicate');
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
 *  "x.y", TargetType: "z"}`, `AuditAction… = "x.y"`, and the app-plugin
 *  admin's `h.Audit(ctx, uid, "x.y", …)`. */
function handlerRows(): { actions: string[]; targets: string[] } {
  const actions = new Set<string>();
  const targets = new Set<string>();
  for (const src of goSources()) {
    if (!/AuditEntry\{|AuditAction|\.Audit\(/.test(src)) continue;
    for (const m of src.matchAll(/Action:\s*"([a-z_]+\.[a-z_.]+)"/g)) actions.add(m[1]);
    for (const m of src.matchAll(/AuditAction\w*\s*=\s*"([a-z_]+\.[a-z_.]+)"/g)) actions.add(m[1]);
    for (const m of src.matchAll(/\.Audit\([^,]+,[^,]+,\s*"([a-z_]+\.[a-z_.]+)"/g)) actions.add(m[1]);
    for (const m of src.matchAll(/TargetType:\s*"([a-z_]+)"/g)) targets.add(m[1]);
  }
  return { actions: [...actions], targets: [...targets] };
}

const segments = adminSegments(fs.readFileSync(ROUTES, 'utf8'));
const written = handlerRows();

describe('audit labels — every writer, not only the middleware', () => {
  it('reads the routes and the handlers (a parse that finds nothing proves nothing)', () => {
    expect(segments.length).toBeGreaterThan(15);
    expect(segments).toContain('app-plugins');
    expect(written.actions).toContain('share.pin_revealed');
  });

  for (const [lang, catalogue] of [['en', en], ['tr', tr]] as const) {
    const { te } = lookup(catalogue as Record<string, unknown>);

    it(`every admin route segment has a ${lang} resource name`, () => {
      const missing = segments.filter((s) => !te(`audit.resource.${keyOf(s)}`));
      expect(missing).toEqual([]);
    });

    it(`every action a handler writes has a ${lang} resource and verb`, () => {
      const missing: string[] = [];
      for (const action of written.actions) {
        const { resource, verb } = splitAction(action);
        if (!te(`audit.resource.${keyOf(resource)}`)) missing.push(`resource ${resource} (${action})`);
        if (!te(`audit.verb.${keyOf(verb)}`)) missing.push(`verb ${verb} (${action})`);
      }
      expect([...new Set(missing)]).toEqual([]);
    });

    it(`every target type a handler writes has a ${lang} name`, () => {
      expect(written.targets.filter((x) => !te(`audit.target.${x}`))).toEqual([]);
    });
  }

  it('a row names its target when the server said which one', () => {
    const { t, te } = lookup(tr as Record<string, unknown>);
    expect(auditTargetLabel('user', '12', t, te, 'ayse@example.com')).toBe('Kullanıcı “ayse@example.com”');
    expect(auditTargetLabel('user', '12', t, te)).toBe('Kullanıcı #12');
  });

  it('an action taken through the AI admin surface reads as the same action, marked (AI)', () => {
    const { t, te } = lookup(tr as Record<string, unknown>);
    const label = auditActionLabel('ai.user.create', t, te);
    expect(label).toContain('Kullanıcı');
    expect(label).toContain('(AI)');
    expect(label).not.toContain('ai user');
  });

  it('the resource filter reaches every resource a row can be written under', () => {
    const { t } = lookup(en as Record<string, unknown>);
    const prefixes = auditResourceOptions((en as { audit: { resource: Record<string, unknown> } }).audit.resource, t)
      .flatMap((o) => o.value.split(','));
    const wanted = [
      ...segments,
      ...written.actions.map((a) => splitAction(a).resource),
      ...pairs.map((p) => splitAction(p.action).resource),
    ];
    const missing = [...new Set(wanted)].filter((r) => !prefixes.includes(`${r}.`));
    expect(missing).toEqual([]);
  });
});
