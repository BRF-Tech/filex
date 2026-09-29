// The Releases page neutralizes what the export would: release bodies on
// GitHub still name the maintainers' own infrastructure in a few old releases,
// and the regenerated releases.json of v0.48.1 carried it into a commit the
// public checkout's pre-push gate refused.
//
// ⚠ The rules are read from scripts/export-public.sh — never repeated here —
// and that script is not published, so in a public checkout this suite skips.
import { existsSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '../../..');
const hasExporter = existsSync(path.join(REPO, 'scripts', 'export-public.sh'));

type Rules = { keep: string[]; replace: [string, string][]; infra: string[] };
async function load() {
  const n = (await import('../../../docs-site/scripts/neutralize.mjs')) as {
    exportRules: () => Rules | null;
    neutralize: (text: string, rules: Rules | null) => string;
    ADDRESS_STANDIN: string;
  };
  const f = (await import('../../../docs-site/scripts/fetch-releases.mjs')) as {
    normalise: (raw: Record<string, unknown>) => { prose: string; groups: unknown[] };
  };
  return { ...n, normalise: f.normalise };
}

// The same two rules the public checkout's gate applies (scripts/hooks/
// export-pre-push.sh): the private forge path, and any name under the
// project domain but the two contact addresses.
const FORGE = /gitlab\.com[:/]+brftech/i;
const DOMAIN = /[a-z0-9._%+@/-]*\bbrf\.sh\b/gi;
const leaks = (s: string) => {
  const out: string[] = [];
  if (FORGE.test(s)) out.push('forge path');
  for (const m of s.match(DOMAIN) ?? []) if (!/^(hello|security)@brf\.sh$/i.test(m)) out.push(m);
  return out;
};

describe.skipIf(!hasExporter)('the Releases page neutralizes release bodies with the export rules', () => {
  it('reads the rules from the exporter, in order', async () => {
    const { exportRules } = await load();
    const rules = exportRules()!;
    expect(rules.keep).toEqual(expect.arrayContaining(['security@brf.sh', 'hello@brf.sh']));
    expect(rules.replace.length).toBeGreaterThanOrEqual(5);
    // The registry line must come before the generic forge rule, as in convert().
    const reg = rules.replace.findIndex(([from]) => from.startsWith('registry.'));
    const generic = rules.replace.findIndex(([from]) => from === 'github.com/brf-tech/filex');
    expect(reg).toBeGreaterThanOrEqual(0);
    expect(generic).toBeGreaterThan(reg);
    expect(rules.infra.length).toBeGreaterThan(0);
  });

  it('v0.33.0, v0.31.0 and v0.14.0 bodies: red before, clean after', async () => {
    const { exportRules, neutralize, normalise, ADDRESS_STANDIN } = await load();
    const rules = exportRules()!;
    const addr = rules.infra.find((a) => !a.endsWith('.'))!;
    // Excerpts shaped like the three bodies the gate refused (2026-09-28).
    const bodies: Record<string, string> = {
      'v0.33.0':
        '**The footer and the About page linked `github.com/brf-tech/filex` — the internal development repository.** ' +
        'Issues: https://github.com/BRF-Tech/filex/issues and https://github.com/BRF-Tech/filex/pulls/12',
      'v0.31.0': '**The browser suite is a gate.** Cypress defaulted to `https://fm.example.com` — the live deployment.',
      'v0.14.0': `An embed on work.example.com reached the server at ${addr}. Write to security@brf.sh or hello@brf.sh.`,
    };
    for (const [tag, body] of Object.entries(bodies)) {
      expect(leaks(body), `${tag} is red before`).not.toEqual([]);
      const clean = neutralize(body, rules);
      expect(leaks(clean), `${tag} after`).toEqual([]);
      expect(clean).not.toContain(addr);
      // …and through the page's own path, where the prose is made.
      const rel = normalise({ tag_name: tag, body, html_url: '', published_at: '2026-01-01', assets: [] });
      expect(leaks(rel.prose), `${tag} prose`).toEqual([]);
    }
    const v33 = neutralize(bodies['v0.33.0'], rules);
    expect(v33).toContain('github.com/BRF-Tech/filex/issues');
    expect(v33).toContain('github.com/BRF-Tech/filex/pulls/12');
    const v14 = neutralize(bodies['v0.14.0'], rules);
    expect(v14).toContain('security@brf.sh');
    expect(v14).toContain('hello@brf.sh');
    expect(v14).toContain(ADDRESS_STANDIN);
  });

  it('without rules (a public checkout) a body passes unchanged', async () => {
    const { neutralize } = await load();
    expect(neutralize('any text', null)).toBe('any text');
  });
});
