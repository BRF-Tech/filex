// Guards a bug that made four features fail SILENTLY in every embed served from
// a different origin to the API — the desktop app, and any reverse-proxy split.
//
// The explorer authenticates with a bearer token, so its requests must go out
// with `same-origin` credentials. filex answers `Access-Control-Allow-Origin: *`
// by default, and the Fetch spec forbids answering a CREDENTIALED cross-origin
// request with a wildcard origin — so a call that hardcodes `credentials:
// 'include'` is rejected by the browser before the response is ever read.
//
// Measured on 2026-08-07 in the desktop app: everything worked except starred
// files, recently-opened, starring and tags. Those four, and only those four,
// hardcoded 'include'. Nothing surfaced in the UI — the lists were simply empty.
//
// This test reads the actual sources rather than exercising the components,
// because the failure is a literal in the code, and a component test would need
// a real cross-origin browser to reproduce it.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = path.join(dir, name);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

describe('cross-origin credentials', () => {
  it('no source file hardcodes credentials: include', () => {
    const offenders: string[] = [];
    for (const file of walk(CORE_SRC)) {
      if (!/\.(ts|vue)$/.test(file)) continue;
      const src = readFileSync(file, 'utf8');
      src.split('\n').forEach((line, i) => {
        // The sanctioned forms: api.credentialsMode(), a passed-down prop, or
        // an explicit 'same-origin'. A bare 'include' is the bug.
        if (/credentials:\s*['"]include['"]/.test(line)) {
          offenders.push(`${path.relative(CORE_SRC, file)}:${i + 1}`);
        }
      });
    }
    expect(offenders, `hardcoded credentials: 'include' — these calls are blocked by CORS in every cross-origin embed`).toEqual([]);
  });

  it("no fallback defaults to 'include' either", () => {
    const offenders: string[] = [];
    for (const file of walk(CORE_SRC)) {
      if (!/\.(ts|vue)$/.test(file)) continue;
      const src = readFileSync(file, 'utf8');
      src.split('\n').forEach((line, i) => {
        if (/authCredentials\s*(\|\||\?\?)\s*['"]include['"]/.test(line)) {
          offenders.push(`${path.relative(CORE_SRC, file)}:${i + 1}`);
        }
      });
    }
    expect(offenders, "a prop that falls back to 'include' has the same effect when the host does not pass one").toEqual([]);
  });

  it('credentialsMode only asks for credentials with cookie/CSRF auth', () => {
    const src = readFileSync(path.join(CORE_SRC, 'composables/useFileApi.ts'), 'utf8');
    const fn = src.slice(src.indexOf('function credentialsMode'));
    const body = fn.slice(0, fn.indexOf('}'));
    expect(body).toContain("'csrf'");
    expect(body).toContain("'include'");
    expect(body).toContain("'same-origin'");
  });
});
