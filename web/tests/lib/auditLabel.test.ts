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
import { auditActionLabel, auditTargetLabel, splitAction } from '@/lib/auditLabel';

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
