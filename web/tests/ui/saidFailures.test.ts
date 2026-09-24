// No screen builds a failure message out of a status code or a raw body.
//
// ⚠⚠ QA, 2026-09-21 — every one of these was on screen: "Config fetch 503:
// {…}", "save failed: 500 {…}", "404 Not Found — {…}", "chunk 0-8388607 →
// 500", "star toggle failed: 403", and five connection panels that printed
// the server's `error` field as it came (an environment variable, to a
// regular user). The words now come from lib/errorWords (`requestFailure`,
// `sayFailure`, `serverWords`); this reads the source so a new
// `throw new Error(\`${res.status}…\`)` fails here with what to use instead.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const ROOTS = [
  path.resolve(__dirname, '../../../packages/core/src'),
  path.resolve(__dirname, '../../src'),
];

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = path.join(dir, name);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

const files = ROOTS.flatMap(walk).filter((f) => /\.(ts|vue)$/.test(f) && !f.includes(`${path.sep}locales${path.sep}`));
const USE_INSTEAD =
  'Say it with lib/errorWords: `throw requestFailure(res.status, body, locale)` for an HTTP refusal, ' +
  '`sayFailure(err, t(fallbackKey), { callerAdmin })` to show a caught one, `serverWords(err)` in a panel ' +
  'that shows the server’s own sentence. A regular user never sees a status code, a JSON body or an env var.';

describe('failures are said, not printed', () => {
  it('finds the sources at all', () => {
    expect(files.length).toBeGreaterThan(200);
  });

  it('no Error message is built from a status code or a response body', () => {
    const offenders: string[] = [];
    for (const f of files) {
      readFileSync(f, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (/^\s*(\/\/|\*|\/\*)/.test(line)) return;
          if (/new Error\(\s*`[^`]*\$\{\s*(res|xhr|resp|response)\.(status|statusText)\b/.test(line)) {
            offenders.push(`${path.relative(process.cwd(), f)}:${i + 1}`);
          }
        });
    }
    expect(offenders, USE_INSTEAD).toEqual([]);
  });

  it('a panel that shows the SERVER’s own sentence tells `serverWords` whose language it is', () => {
    /* ⚠⚠ The second half of "said, not printed": the sentence `serverWords`
       hands back may be the server's own, which never met a translator. In a
       right-to-left language its machine runs have to be isolated, and
       `serverWords(e)` with no locale cannot know (lib/direction
       `foreignText`; measured on `server.token.scope_unknown`, whose
       `root:<storage>://<folder>` lost its closing `>` to the far left of the
       line). Every caller has a locale — the connection composables hold
       `config.locale` — so every call passes it. */
    const offenders: string[] = [];
    for (const f of files) {
      if (f.endsWith(`lib${path.sep}errorWords.ts`)) continue;
      readFileSync(f, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (/^\s*(\/\/|\*|\/\*)/.test(line)) return;
          if (/\bserverWords\(([^,)]*)\)/.test(line)) {
            offenders.push(`${path.relative(process.cwd(), f)}:${i + 1} — ${line.trim()}`);
          }
        });
    }
    expect(
      offenders,
      'pass the reader’s locale: `serverWords(e, config.locale)` — a server sentence is not isolated without it',
    ).toEqual([]);
  });

  it('the explorer’s toast and the ops tray share one call for a failed row', () => {
    const explorer = readFileSync(path.resolve(ROOTS[0], 'FileExplorer.vue'), 'utf8');
    const tray = readFileSync(path.resolve(ROOTS[0], 'components/PendingOpsTray.vue'), 'utf8');
    expect(explorer).toMatch(/if \(op\.status === 'error'\) \{[\s\S]{0,400}flashToast\(opFailure\(op, t\)\.text\);/);
    expect(tray).toMatch(/return opFailure\(op, t, \{ callerAdmin: props\.callerAdmin === true \}\);/);
  });

  it('no composable prints the server’s `error` field as it came', () => {
    const offenders: string[] = [];
    for (const f of files) {
      const src = readFileSync(f, 'utf8');
      if (f.endsWith(`errorWords.ts`)) continue;
      if (/JSON\.parse\(\s*err\.detail\s*\)[\s\S]{0,120}return parsed\.error/.test(src)) {
        offenders.push(path.relative(process.cwd(), f));
      }
    }
    expect(offenders, USE_INSTEAD).toEqual([]);
  });
});
