// The hostile file names (task #238) for everything that is not Go: the
// Playwright specs, the web unit tests and the desktop scripts.
//
// ⚠⚠ ONE LIST. The names live in backend/internal/testutil/hostilenames/
// names.json; the Go tests embed that file (package hostilenames) and this
// module reads the same file. Add a name THERE, never here and never as a
// literal in a spec: a second list is how one surface comes to be tested with
// names the others never see.
//
// ⚠ Plain JavaScript on purpose (like app-locations.mjs): the desktop
// scripts run under bare `node`, which cannot import a TypeScript helper.
//
// A name is its `parts` joined: a string is itself, a number ONE code point,
// {repeat, times} that many copies. Invisible, combining and emoji characters
// are code points in the file because an editor shows nothing for them.

import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), '../..');

/** Where the one list lives, from the repository root. */
export const HOSTILE_NAMES_FILE = 'backend/internal/testutil/hostilenames/names.json';

function part(p, where) {
  if (typeof p === 'string') return p;
  if (typeof p === 'number') {
    if (!Number.isInteger(p) || p < 0 || p > 0x10ffff || (p >= 0xd800 && p <= 0xdfff)) {
      throw new Error(`${where}: ${p} is not a Unicode scalar value`);
    }
    return String.fromCodePoint(p);
  }
  if (p && typeof p === 'object' && 'repeat' in p) {
    if (!Number.isInteger(p.times) || p.times < 1) throw new Error(`${where}: repeat times ${p.times}`);
    return part(p.repeat, where).repeat(p.times);
  }
  throw new Error(`${where}: unknown part ${JSON.stringify(p)}`);
}

function join(parts, where) {
  return parts.map((p) => part(p, where)).join('');
}

const file = JSON.parse(readFileSync(resolve(REPO, HOSTILE_NAMES_FILE), 'utf8'));

/** Every hostile name, in the file's order. */
export const HOSTILE_NAMES = Object.freeze(
  file.names.map((n) =>
    Object.freeze({
      id: n.id,
      name: join(n.parts, n.id),
      why: n.why,
      tags: Object.freeze([...(n.tags ?? [])]),
      refused: n.refused === true,
      windowsInvalid: n.windows === 'invalid',
      twin: n.twin ?? '',
      twinKind: n.twin_kind ?? '',
    }),
  ),
);

/** Every hostile path (a deep one), slash-separated, no leading slash. */
export const HOSTILE_PATHS = Object.freeze(
  file.paths.map((p) => {
    const seg = join(p.segment, p.id);
    const leaf = join(p.leaf, p.id);
    return Object.freeze({
      id: p.id,
      path: [...Array(p.depth).fill(seg), leaf].join('/'),
      depth: p.depth,
      leaf,
      why: p.why,
    });
  }),
);

/** The name with `id`; throws on an unknown id (a typo in a spec). */
export function hostileName(id) {
  const n = HOSTILE_NAMES.find((x) => x.id === id);
  if (!n) throw new Error(`hostile-names: no name ${id}`);
  return n;
}

/** The names carrying none of `tags` (the ones a surface can carry at all). */
export function hostileNamesWithout(names, ...tags) {
  return names.filter((n) => !tags.some((t) => n.tags.includes(t)));
}

/** Every name a filex surface must accept. */
export const CREATABLE_NAMES = Object.freeze(HOSTILE_NAMES.filter((n) => !n.refused));

/** Every name no filex surface may create. */
export const REFUSED_NAMES = Object.freeze(HOSTILE_NAMES.filter((n) => n.refused));

/** UTF-8 length of a name, the unit a local disk's 255 limit counts. */
export function utf8Bytes(s) {
  return Buffer.byteLength(s, 'utf8');
}
