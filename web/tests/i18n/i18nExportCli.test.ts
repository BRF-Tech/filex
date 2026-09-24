// `i18n-export --help` answers and writes nothing.
//
// ⚠ Translator report, 2026-09-22: `node scripts/i18n-export.mjs --help`
// ignored the flag and wrote ./filex-catalogue/ into whatever directory the
// person was standing in — the one flag somebody runs to find out what a tool
// does BEFORE letting it do anything.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const SCRIPT = path.resolve(__dirname, '../../../scripts/i18n-export.mjs');

describe('i18n-export --help', () => {
  for (const flag of ['--help', '-h']) {
    it(`${flag} prints usage, exits 0 and writes nothing`, () => {
      const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-i18n-help-'));
      try {
        const out = execFileSync(process.execPath, [SCRIPT, flag], { cwd: dir, encoding: 'utf8' });
        expect(out).toMatch(/^usage: /);
        expect(out).toContain('--out <dir>');
        expect(fs.readdirSync(dir)).toEqual([]);
      } finally {
        fs.rmSync(dir, { recursive: true, force: true });
      }
    });
  }
});
