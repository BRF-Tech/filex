/**
 * THE ONE LIST SPLITTER (packages/core/src/lib/listInput.ts).
 *
 * ⚠⚠ The Arabic translator's finding, 2026-09-22: every list field split on
 * the ASCII comma only, so `a@x.com، b@y.com` — what an Arabic or Persian
 * keyboard types — was ONE malformed address and the share mail answered
 * "enter a valid email". Five fields carried five `split(/[,…]/)`s; they all
 * read through `splitList` now, and this file pins both the new separators and
 * the old behaviour for the old ones.
 */
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import { isListSeparatorKey, splitList } from '@brftech/filex-core/src/lib/listInput';
import { parseTagList } from '@brftech/filex-core/src/lib/advSearch';

const AR_COMMA = String.fromCharCode(0x060c); // ،
const AR_SEMI = String.fromCharCode(0x061b); // ؛
const IDEO_COMMA = String.fromCharCode(0x3001); // 、
const FW_COMMA = String.fromCharCode(0xff0c); // ，

describe('splitList', () => {
  it('splits on the ASCII comma exactly as before: trimmed, empties dropped', () => {
    expect(splitList('a, b ,c,,')).toEqual(['a', 'b', 'c']);
    expect(splitList('')).toEqual([]);
    expect(splitList(null)).toEqual([]);
    // a tag may contain a space — commas only, by default
    expect(splitList('quarterly report, tax')).toEqual(['quarterly report', 'tax']);
  });

  it('splits on the Arabic, ideographic and fullwidth commas too', () => {
    expect(splitList(`a@x.com${AR_COMMA} b@y.com`)).toEqual(['a@x.com', 'b@y.com']);
    expect(splitList(`一${IDEO_COMMA}二${FW_COMMA}三`)).toEqual(['一', '二', '三']);
  });

  it('semicolons (ASCII and Arabic) only when asked', () => {
    expect(splitList(`a;b${AR_SEMI}c`)).toEqual([`a;b${AR_SEMI}c`]);
    expect(splitList(`a;b${AR_SEMI}c`, { semicolons: true })).toEqual(['a', 'b', 'c']);
  });

  it('whitespace and new lines only when asked', () => {
    expect(splitList('a b\nc')).toEqual(['a b\nc']);
    expect(splitList('a b\nc', { spaces: true })).toEqual(['a', 'b', 'c']);
    expect(splitList('a b\nc', { newlines: true })).toEqual(['a b', 'c']);
  });
});

/**
 * ONE implementation, not five. A comma split written anywhere else is a
 * field that will not accept "،" — unless it splits something the SERVER wrote
 * (a stored scope list), which no keyboard ever typed.
 */
const SERVER_WRITTEN: { file: string; line: string; why: string }[] = [
  { file: 'web/src/views/ApiMcp.vue', line: "return v ? v.split(',') : [];", why: 'a token\'s scope string, as the server stores it' },
  { file: 'web/src/views/ApiMcp.vue', line: "(tok.usernames || '')", why: 'the usernames the server stored, comma-joined by the server' },
  // (arrives with the release branch) the field keys a server-side check names
  { file: 'web/src/views/AuthProviders.vue', line: "params.fields = params.fields.split(',')", why: 'the field keys a provider check reports, as the server joined them' },
  { file: 'packages/core/src/lib/productVersion.ts', line: "for (const part of m[2].split(',')", why: 'capabilities.version — commit and build time as backend internal/version.String joins them' },
];

describe('no list field splits on its own', () => {
  it('every comma split outside lib/listInput is server-written data', () => {
    const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
    const hits: string[] = [];
    const walk = (dir: string) => {
      for (const n of readdirSync(dir)) {
        const p = path.join(dir, n);
        if (statSync(p).isDirectory()) {
          if (!/node_modules|dist|locales/.test(n)) walk(p);
          continue;
        }
        if (!/\.(ts|vue)$/.test(n) || n === 'listInput.ts') continue;
        const rel = path.relative(REPO, p).split(path.sep).join('/');
        const lines = readFileSync(p, 'utf8').split('\n');
        lines.forEach((l, i) => {
          if (!/\.split\((\/[^/]*,[^/]*\/|'\s*,\s*'|"\s*,\s*")/.test(l)) return;
          const around = lines.slice(Math.max(0, i - 3), i + 1).join('\n');
          if (SERVER_WRITTEN.some((s) => s.file === rel && around.includes(s.line))) return;
          hits.push(`${rel}:${i + 1}  ${l.trim()}`);
        });
      }
    };
    walk(path.join(REPO, 'packages/core/src'));
    walk(path.join(REPO, 'web/src'));
    expect(hits, 'Split a typed list with splitList (packages/core/src/lib/listInput.ts)').toEqual([]);
  });
});

describe('the fields that use it', () => {
  it('parseTagList: commas of every script and new lines, never spaces', () => {
    expect(parseTagList(`فاتورة${AR_COMMA} quarterly report\ntax`)).toEqual(['فاتورة', 'quarterly report', 'tax']);
  });

  it('a chip commits on any of the commas, and on nothing else of that kind', () => {
    for (const k of [',', AR_COMMA, IDEO_COMMA, FW_COMMA]) expect(isListSeparatorKey(k), k).toBe(true);
    for (const k of [';', ' ', 'a', 'Enter', '']) expect(isListSeparatorKey(k), k).toBe(false);
  });
});
