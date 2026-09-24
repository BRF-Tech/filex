// Every label a Go storage descriptor names by i18n key — a driver's fields,
// their select options, and the scan settings every storage has (issue #44) —
// must exist in the admin panel's catalogue in English AND Turkish. The field
// component falls back to the backend's English when a key is missing, so a
// forgotten key does not show up as a raw key anywhere: it shows up as English
// on a Turkish screen, which nobody notices until a Turkish admin does.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const ROOT = path.resolve(__dirname, '../../..');
const STORAGE = path.join(ROOT, 'backend/internal/storage');

function descriptorFiles(dir: string): string[] {
  const out: string[] = [];
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) out.push(...descriptorFiles(p));
    else if (/^descriptor\.go$/.test(e.name)) out.push(p);
  }
  return out;
}

function lookup(cat: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((o, k) => (o && typeof o === 'object' ? (o as Record<string, unknown>)[k] : undefined), cat);
}

describe('storage descriptor labels are in the admin catalogue', () => {
  const files = descriptorFiles(STORAGE);
  const keys: Array<{ file: string; key: string }> = [];
  for (const f of files) {
    const src = fs.readFileSync(f, 'utf8');
    for (const m of src.matchAll(/(?:I18nKey|HelpI18nKey):\s*"([^"]+)"/g)) {
      keys.push({ file: path.relative(ROOT, f), key: m[1] });
    }
  }

  it('finds the descriptors (a scan of nothing would pass everything)', () => {
    expect(files.length).toBeGreaterThanOrEqual(7); // the shared one + six drivers
    expect(keys.map((k) => k.key)).toContain('storages.fields.scanExclude');
  });

  it('every key has an English and a Turkish string', () => {
    const missing = keys.filter(
      ({ key }) => typeof lookup(en, key) !== 'string' || typeof lookup(tr, key) !== 'string',
    );
    expect(missing.map((m) => `${m.file}: ${m.key}`)).toEqual([]);
  });
});
