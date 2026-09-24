// One name per external service, the name its own project uses: "draw.io"
// and "ONLYOFFICE".
//
// ⚠ QA, 2026-09-21: draw.io was "Drawio" on the External services screen
// (the id, capitalised), "drawio" in the New menu's reason, "diagrams.net" in
// the editor tab, and "DRAWIO" in the Type column. A person reading four
// words cannot tell they are one thing. Every string a person reads — both
// apps, both languages — is checked here, so a fifth spelling fails.
import { describe, expect, it } from 'vitest';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';
import { tr as coreTr } from '@brftech/filex-core/src/locales/tr';
import { typeLabelFor } from '@brftech/filex-core/src/lib/fileIcons';

import webEn from '@/locales/en.json';
import webTr from '@/locales/tr.json';

function values(obj: unknown, path = ''): Array<[string, string]> {
  if (typeof obj === 'string') return [[path, obj]];
  if (obj && typeof obj === 'object') {
    return Object.entries(obj as Record<string, unknown>).flatMap(([k, v]) => values(v, path ? `${path}.${k}` : k));
  }
  return [];
}

/* The spellings that are NOT the name. `drawio` inside a longer word, a URL
   or an env var name (FILEX_DRAWIO_URL) is not a name a person reads, but no
   user string should carry those either — an operator's variable is not a
   sentence for a person (the same QA pass). */
const WRONG = /\bdrawio\b|diagrams\.net|\bOnlyOffice\b|\bOnlyoffice\b|FILEX_DRAWIO_URL/;

describe('one name per service', () => {
  for (const [label, dict] of [
    ['core en', coreEn],
    ['core tr', coreTr],
    ['web en', webEn],
    ['web tr', webTr],
  ] as const) {
    it(`${label}: no other spelling of draw.io or ONLYOFFICE`, () => {
      const offenders = values(dict)
        .filter(([, v]) => WRONG.test(v))
        .map(([k, v]) => `${k}: ${v}`);
      expect(offenders).toEqual([]);
    });
  }

  it('the Type column names a .drawio file, rather than printing DRAWIO', () => {
    const t = (k: string) => (coreEn as Record<string, string>)[k] ?? k;
    expect(typeLabelFor({ type: 'file', extension: 'drawio' }, t)).toBe('draw.io diagram');
    expect(typeLabelFor({ type: 'file', extension: 'dio' }, t)).toBe('draw.io diagram');
    const tt = (k: string) => (coreTr as Record<string, string>)[k] ?? k;
    expect(typeLabelFor({ type: 'file', extension: 'drawio' }, tt)).toBe('draw.io diyagramı');
  });
});
