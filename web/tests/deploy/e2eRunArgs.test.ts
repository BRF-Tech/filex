// An option `e2e/run.mjs` does not know must STOP the run, not be ignored.
//
// ⚠⚠ Measured 2026-09-24, on the v0.43.0 release branch. Three sign specs
// could not pass while their app was being rebuilt, so the gate was run as
//
//     node e2e/run.mjs local --binary … --grep-invert "97-…|99-…|113-…"
//
// `run.mjs` read its options with `argv.includes('--x')` and never looked at
// the rest of the command line, so `--grep-invert` — an option it did not have
// — was simply dropped. The three specs ran, failed, and the run came back red
// for precisely the reason the filter was there to avoid.
//
// The damage that time was a wasted run and a confusing report. The damage
// next time is the other direction: a filter that is silently ignored can just
// as easily leave a spec IN that somebody meant to take out, or be typed
// `--grep` when `--grep-invert` was meant, and the suite comes back green
// having measured something nobody asked for. So the rule is the general one
// — any unknown option stops the run — and this is the test for it.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  BOOLEAN_OPTIONS,
  VALUE_OPTIONS,
  unknownOptions,
  unknownOptionMessage,
} from '../../../e2e/lib/args.mjs';

const RUN_MJS = resolve(__dirname, '../../../e2e/run.mjs');

describe('e2e/run.mjs option checking', () => {
  it('accepts every option it documents', () => {
    const argv = [
      '--binary',
      'bin/filex.exe',
      '--port',
      '5999',
      '--grep',
      'a|b',
      '--grep-invert',
      'c|d',
      '--build',
      '--s3',
      '--keep',
      '--headed',
      '--spec',
      'x.cy.ts',
      '--browser',
      'chrome',
      '--url',
      'https://fm.example.com',
    ];
    expect(unknownOptions(argv)).toEqual([]);
  });

  it('names the option it does not know, and does not swallow it', () => {
    expect(unknownOptions(['--nosuch'])).toEqual(['--nosuch']);
    expect(unknownOptions(['--binary', 'x', '--nosuch', '--keep'])).toEqual(['--nosuch']);
    expect(unknownOptions(['--grepp', 'a|b'])).toEqual(['--grepp']);
  });

  // The defect itself: before `--grep-invert` existed, this returned [] —
  // the option looked accepted and filtered nothing.
  it('would have caught the release run that filtered nothing', () => {
    const argv = ['--binary', 'bin/filex-merge-e2e.exe', '--grep-invert', '97|99|113'];
    expect(unknownOptions(argv)).toEqual([]);
    expect(VALUE_OPTIONS).toContain('grep-invert');
  });

  it("skips a value-taking option's value, and reads --opt=value", () => {
    // `--grep --headed` means a grep FOR "--headed": odd, but it is a value.
    expect(unknownOptions(['--grep', '--headed'])).toEqual([]);
    expect(unknownOptions(['--grep=a|b', '--nosuch=1'])).toEqual(['--nosuch=1']);
    // A value-taking option with nothing after it is still known, not unknown.
    expect(unknownOptions(['--binary'])).toEqual([]);
  });

  it('a positional argument is not an option', () => {
    expect(unknownOptions(['bin/filex.exe', 'tests/95.spec.ts'])).toEqual([]);
  });

  it('the refusal names the stray, suggests the near miss and lists what is known', () => {
    const msg = unknownOptionMessage(['--grepinvert']);
    expect(msg).toContain('--grepinvert');
    expect(msg).toContain('--grep-invert');
    expect(msg).toContain('--binary');
    expect(msg).toMatch(/refusing to run/);
  });

  // ⚠ The table is the script's own: a new option added to run.mjs without a
  // line here is an option this check would refuse at the first use of it.
  it('run.mjs reads no option that the table does not carry', () => {
    const src = readFileSync(RUN_MJS, 'utf8');
    const read = new Set<string>();
    for (const m of src.matchAll(/\b(?:flag|value)\('([a-z0-9-]+)'/g)) read.add(m[1]);
    const known = new Set([...VALUE_OPTIONS, ...BOOLEAN_OPTIONS]);
    expect([...read].filter((o) => !known.has(o)), 'read by run.mjs, missing from lib/args.mjs').toEqual([]);
  });

  it('run.mjs actually calls the check and exits on a stray', () => {
    const src = readFileSync(RUN_MJS, 'utf8');
    expect(src).toContain('unknownOptions(');
    expect(src).toMatch(/if \(strays\.length\)[\s\S]{0,120}process\.exit\(2\)/);
    // …and passes the new option on to Playwright rather than holding it.
    expect(src).toContain("args.push('--grep-invert', grepInvert)");
  });
});
