// A person is named ONE way on every surface.
//
// ⚠ QA, 2026-09-21: one administrator was "admin2" in the Owner column,
// "admin@local" in the share dialog, the admin tables and notifications, and a
// full name on signatures — each screen had its own chain (display → username
// → e-mail on the server's Owner lookup, display → e-mail in the share dialog
// and the details panel, display → the address's local part in the presence
// strip). The rule is core's personName(); the server's twin is
// model.PersonLabel (backend/internal/model/user_label_test.go holds it to the
// same cases). docs/CONTRIBUTING.md → "A person is named one way".
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { personInitial, personName } from '@brftech/filex-core/src/lib/personName';

describe('personName — the one rule', () => {
  it('display name, else username, else e-mail — the same cases as model.PersonLabel', () => {
    const cases: Array<[string, string, string, string]> = [
      ['Ayşe Yılmaz', 'ayse', 'ayse@example.com', 'Ayşe Yılmaz'],
      ['', 'ayse', 'ayse@example.com', 'ayse'],
      ['  ', '  ayse ', 'ayse@example.com', 'ayse'],
      ['', '', 'ayse@example.com', 'ayse@example.com'],
      ['', '', '', ''],
    ];
    for (const [display_name, username, email, want] of cases) {
      expect(personName({ display_name, username, email })).toBe(want);
    }
    expect(personName(null)).toBe('');
    expect(personName({ display_name: '', username: 'admin2', email: 'admin@local' })).toBe('admin2');
  });

  it('a name the server already worked out comes first (owner_name, user_name, creator_name)', () => {
    expect(personName({ name: 'admin2', email: 'admin@local' })).toBe('admin2');
    expect(personName({ name: '', email: 'admin@local' })).toBe('admin@local');
  });

  it('the avatar letter is a whole character, upper-cased in the viewer\'s language', () => {
    expect(personInitial({ display_name: 'ismail' }, 'tr-TR')).toBe('İ');
    expect(personInitial({ display_name: 'ismail' }, 'en-US')).toBe('I');
    expect(personInitial({ display_name: '😀 smile' })).toBe('😀');
    expect(personInitial({})).toBe('');
  });
});

/* The ratchet: a component that re-derives a person's name with its own
   `display_name || email` chain is how the three spellings came about. */
describe('nobody re-derives a person\'s name', () => {
  const ROOT = path.resolve(__dirname, '../../..');
  const DIRS = [path.join(ROOT, 'web', 'src'), path.join(ROOT, 'packages', 'core', 'src')];
  const walk = (dir: string, out: string[] = []): string[] => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) {
        if (!['node_modules', 'dist', 'locales'].includes(e.name)) walk(p, out);
      } else if (/\.(vue|ts)$/.test(e.name)) out.push(p);
    }
    return out;
  };

  it('finds sources to scan (a scan of nothing passes everything)', () => {
    expect(DIRS.flatMap((d) => walk(d)).length).toBeGreaterThan(200);
  });

  it('no `display_name ||`, `user_email ||` or `creator_email ||` chain outside lib/personName', () => {
    const chain = /\b(user_)?display_name\s*\|\||\b(user_|creator_)?email\s*\|\|\s*(['`#]|\w+\.display|\w*username)/;
    const hits: string[] = [];
    for (const f of DIRS.flatMap((d) => walk(d))) {
      if (f.endsWith(`${path.sep}personName.ts`)) continue;
      fs.readFileSync(f, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (chain.test(line)) hits.push(`${path.relative(ROOT, f)}:${i + 1}  ${line.trim().slice(0, 90)}`);
        });
    }
    expect(hits).toEqual([]);
  });
});
