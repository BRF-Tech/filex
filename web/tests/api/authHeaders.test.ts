// Guards the bug that made the desktop app answer 401 to itself.
//
// The explorer accepts a bearer token as either a string or a FUNCTION. The
// function form is the one that matters for a desktop app: the credential is
// fetched from the main process per call instead of sitting in the renderer
// between requests — and resolving it is therefore ASYNCHRONOUS.
//
// Every component that talks to the API is handed an `authHeaders` prop. If a
// caller invokes it without awaiting, it spreads a *Promise* into a headers
// object, which serialises to nothing: the request goes out with no
// Authorization header at all and the server answers 401. Nothing throws, and
// the only symptom is a feature that quietly does not work.
//
// Measured on 2026-08-10 against fm.brf.sh, in this order:
//   POST /api/files/onlyoffice/config   401  → "Config fetch 401" on every doc
//   GET  /api/files/manager/star/list   401  → starred files silently empty
//   POST /api/files/manager/recent      401  → recently-opened silently empty
//
// This test reads the sources rather than exercising the components, because
// the failure is a shape in the code — and reproducing it needs a real
// cross-origin embed with a function token, which a component test has not got.

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

function sources(): { file: string; text: string }[] {
  return walk(CORE_SRC)
    .filter((f) => /\.(ts|vue)$/.test(f))
    .map((file) => ({ file: path.relative(CORE_SRC, file), text: readFileSync(file, 'utf8') }));
}

describe('auth headers reach the wire', () => {
  it('every call to the authHeaders prop is awaited', () => {
    const offenders: string[] = [];
    for (const { file, text } of sources()) {
      text.split('\n').forEach((line, i) => {
        // The call forms in use: `props.authHeaders()` and `props.authHeaders?.()`.
        // A declaration (`authHeaders?: () => …`) is not a call, and neither is
        // passing the function on (`authHeaders: props.authHeaders`).
        const calls = /props\.authHeaders\s*(\?\.)?\(/.test(line);
        if (!calls) return;
        if (/await\s+props\.authHeaders/.test(line)) return;
        offenders.push(`${file}:${i + 1}  ${line.trim()}`);
      });
    }
    expect(
      offenders,
      'these calls spread a Promise into a headers object — the request goes out with no Authorization header and comes back 401',
    ).toEqual([]);
  });

  it('the explorer hands components the ASYNC header builder', () => {
    const explorer = readFileSync(path.join(CORE_SRC, 'FileExplorer.vue'), 'utf8');
    const builder = explorer.match(/function buildAuthHeaders[\s\S]{0,200}?\n\}/)?.[0] ?? '';
    expect(builder, 'buildAuthHeaders was not found — the guard below cannot be trusted').not.toBe('');
    expect(
      builder.includes('api.authHeadersSync'),
      'buildAuthHeaders must call the async api.authHeaders: the sync builder cannot resolve a function token, so it emits no Authorization header at all',
    ).toBe(false);
  });

  it('the sync builder still answers with the last token it saw', () => {
    // It stays exported for genuinely synchronous callers (XMLHttpRequest's
    // setRequestHeader loop). Returning NOTHING for a function token is what
    // made it dangerous; remembering the last resolved value makes a stale
    // token the worst case instead of an anonymous request.
    const api = readFileSync(path.join(CORE_SRC, 'composables/useFileApi.ts'), 'utf8');
    const sync = api.match(/function authHeadersSync[\s\S]*?\n  \}/)?.[0] ?? '';
    expect(sync, 'authHeadersSync was not found').not.toBe('');
    expect(
      /lastBearer/.test(sync),
      'authHeadersSync must fall back to the cached bearer for function tokens',
    ).toBe(true);
  });
});
