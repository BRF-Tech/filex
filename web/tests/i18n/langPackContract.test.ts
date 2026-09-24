// A language pack is judged in three places that cannot import each other:
// the server (Go, backend/pkg/pluginkit/wire/langpack.go — it refuses the
// install), the translator's validator (scripts/i18n-validate.mjs — Node, no
// dependencies, copied into the template repository) and the browser. The
// limits and the right-to-left list are written twice, so this reads both
// sources and fails the moment they disagree: a validator that passes what
// the server refuses sends a translator to an install error with no warning.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { formatInstant, localeTag } from '@brftech/filex-core';
// A plain .mjs (the validator is copied verbatim into the template repo).
import { LIMITS, RTL_LANGUAGES, RTL_SCRIPTS } from '../../../scripts/i18n-validate.mjs';

const GO = fs.readFileSync(path.resolve(__dirname, '../../../backend/pkg/pluginkit/wire/langpack.go'), 'utf8');

function goConst(name: string): number {
  const m = GO.match(new RegExp(`\\b${name}\\s*=\\s*([0-9]+)(?:\\s*<<\\s*([0-9]+))?`));
  if (!m) throw new Error(`${name} not found in langpack.go`);
  return Number(m[1]) * 2 ** Number(m[2] ?? 0);
}

function goSet(varName: string): string[] {
  const m = GO.match(new RegExp(`var ${varName} = map\\[string\\]bool\\{([\\s\\S]*?)\\n\\}`));
  if (!m) throw new Error(`${varName} not found in langpack.go`);
  return [...m[1].matchAll(/"([a-z]+)":\s*true/g)].map((x) => x[1]).sort();
}

describe('the server and the validator enforce the same limits', () => {
  it.each(Object.keys(LIMITS))('%s', (name) => {
    expect((LIMITS as Record<string, number>)[name]).toBe(goConst(name));
  });

  it('and agree which languages are written right to left', () => {
    expect([...RTL_LANGUAGES].sort()).toEqual(goSet('rtlLanguages'));
    expect([...RTL_SCRIPTS].sort()).toEqual(goSet('rtlScripts'));
  });
});

describe("a pack's language formats dates and numbers as that language", () => {
  // ⚠ `localeTag` mapped everything that was not `tr` to `en-US`, so a Spanish
  // interface printed "Sep 12, 2026, 3:25 PM" between Spanish words.
  it('keeps the two built-ins pinned, and gives every other language its own tag', () => {
    expect(localeTag('en')).toBe('en-US');
    expect(localeTag('tr')).toBe('tr-TR');
    expect(localeTag('es')).toBe('es');
    expect(localeTag('pt-br')).toBe('pt-BR');
    expect(localeTag(undefined)).toBe('en-US');
  });

  it('so a Spanish date is written in Spanish', () => {
    const d = new Date(Date.UTC(2026, 8, 12, 12, 0, 0));
    expect(formatInstant(d, 'es', { month: 'long', timeZone: 'UTC' } as Intl.DateTimeFormatOptions)).toBe('septiembre');
  });
});
