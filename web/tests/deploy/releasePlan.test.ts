// The gates of THIS repository's release (scripts/release/plan.mjs) cannot
// quietly disappear.
//
// ⚠ Every gate in the plan is there because a release shipped without it. A
// gate removed "for now" to get a release out is a gate the next release does
// not have either — this list is how deleting one becomes a visible decision
// in a diff instead of a silent one. Deleting a line here is allowed; it has
// to be done on purpose, with the incident that justified the gate in mind.

import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import plan from '../../../scripts/release/plan.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const p = plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9' });
const names = (list: Array<{ name: string }>) => list.map((g) => g.name);

/** gate-name fragment → why it is in the plan */
const REQUIRED: Record<string, Record<string, string>> = {
  docs: {
    'every relative link resolves': 'CONTRIBUTING step 3',
    'the site builds': 'lesson #34: a broken code span kills the build',
    'every in-page anchor lands': '98 of 366 anchors were dead on a green build',
    'Helm chart lints and renders': 'lessons #430/#432',
  },
  pretag: {
    'build: packages, admin (vue-tsc': 'v0.38.1: vue-tsc was the only gate that saw the WIP files',
    'serves the UI just built': '2026-09-14: a binary with a 16-hour-old UI',
    'go: vet + test': 'the backend suite',
    'migrations on sqlite, postgres AND mysql': 'the parity tests skip without a DSN',
    'web unit (TZ=UTC': 'lesson #448: v0.43.0 CI failed in UTC',
    'desktop: typecheck + unit': '0.43.x shipped a desktop window that never parsed (#52)',
    'docker: full image': 'v0.43.0: npm published, images never built',
    'docker: slim image': 'v0.43.0',
    'both images report the release': 'the version is baked in',
    'shop window: a throwaway instance': 'CONTRIBUTING step 12, before the tag',
    'e2e: Cypress': 'the suite release.yml waits for',
    'e2e: Playwright': 'the journeys Cypress does not walk',
  },
  exportGates: {
    'go build + vet + test (public module path)': 'lesson #55 checklist: test IN the export',
    'goreleaser check, with the GoReleaser CI uses': 'lesson #510',
    'workflow guards ran against the workflows that will run': 'lessons #455, #461, #510',
  },
  published: {
    'GitHub Release': '0.44.0/0.44.1 shipped without the desktop packages',
    'ghcr:': 'v0.43.0 had no images',
    'npm:': 'the three packages',
  },
  deployed: {
    'update': 'lesson #69: stable.json three releases behind',
    'desktop feeds offer': 'lesson #69: the desktop feed four releases behind',
    "pages, not a snapshot": 'lesson #511: docs.filex.sh served an old snapshot',
    'shop window: the published surfaces': 'CONTRIBUTING step 12, after the docs',
  },
};

describe("this repository's release plan", () => {
  for (const [stage, gates] of Object.entries(REQUIRED)) {
    it(`${stage}: keeps every gate a past release needed`, () => {
      const have = names(p[stage]);
      for (const [fragment, why] of Object.entries(gates)) {
        expect(have.some((n) => n.includes(fragment)), `${stage} lost "${fragment}" — it exists because: ${why}`).toBe(true);
      }
    });
  }

  it('gate names are unique (each writes its own log)', () => {
    for (const stage of ['audit', 'docs', 'pretag', 'exportGates', 'published', 'deployed']) {
      const n = names(p[stage]);
      expect(new Set(n).size, stage).toBe(n.length);
    }
  });

  it('the pretag chain runs Cypress and Playwright LAST, after the builds they test', () => {
    const n = names(p.pretag);
    const build = n.findIndex((x) => x.startsWith('build: server binary'));
    const e2e = n.findIndex((x) => x.startsWith('e2e:'));
    expect(build).toBeGreaterThanOrEqual(0);
    expect(e2e).toBeGreaterThan(build);
  });

  it('names the workflow guards by titles that exist, so a rename cannot make the gate vacuous', () => {
    const guards = p.exportGates.find((g: { vitest?: unknown }) => g.vitest).vitest.mustPass as string[];
    const sources = ['releaseGatesImages.test.ts', 'goreleaserTemplates.test.ts'].map((f) =>
      fs.readFileSync(path.join(REPO, 'web', 'tests', 'deploy', f), 'utf8'),
    );
    expect(guards.length).toBeGreaterThanOrEqual(5);
    for (const title of guards) expect(sources.some((s) => s.includes(title)), title).toBe(true);
  });

  it('signs with the maintainer key and keeps only the contact addresses public', () => {
    expect(p.signingKeys).toEqual(['EFA3B1262FD992800DBBB5E3A8FEBA97FF786513']);
    expect(p.privateHosts.forbid.length).toBeGreaterThanOrEqual(2);
    for (const a of p.privateHosts.allow) expect(a).toMatch(/^(security|hello)@/);
  });
});
