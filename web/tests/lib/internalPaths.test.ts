// filex's own directories, on the client: the one list (packages/core/src/lib/
// internalPaths.ts) and the two things only the client can do with it.
//
// ⚠⚠ Why the first describe parses Go. The backend's list lives in
// backend/internal/syspath/syspath.go and the whole server judges paths by it.
// This client copy exists for navigation guards and older servers — and a
// second copy of a list is how one surface comes to know a name another does
// not: before syspath, seventeen hand-written backend copies disagreed and `.filex-open`
// was known to exactly one of them (owner's report, 2026-09-21). So the two
// lists are compared here, and so is the desktop app's own name for its
// working area, which the server decodes working copies by.
import { describe, it, expect, vi, afterEach } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  INTERNAL_DIR_NAMES,
  KEEP_MARKER_NAME,
  isInternalName,
  isInternalPath,
  listingAddress,
} from '@brftech/filex-core/src/lib/internalPaths';
import { filterInternalEntries } from '@brftech/filex-core/src/lib/listing';
import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const here = path.dirname(fileURLToPath(import.meta.url));
const SYSPATH_GO = path.resolve(here, '../../../backend/internal/syspath/syspath.go');
const OPENWITH_TS = path.resolve(here, '../../../desktop/src/openwith.ts');

/** `Name = "value"` constants in syspath.go (in a `const (…)` block or as a
 *  one-line `const Name = "…"`), by identifier. */
function goConsts(src: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const m of src.matchAll(/^\s*(?:const\s+)?([A-Z]\w*)\s*=\s*"([^"]+)"/gm)) out[m[1]] = m[2];
  return out;
}

describe('the client list is the server list', () => {
  const src = fs.readFileSync(SYSPATH_GO, 'utf8');
  const consts = goConsts(src);

  it('names the same directories, in the same order', () => {
    const decl = src.match(/^var dirs = \[\]string\{([^}]*)\}/m);
    expect(decl, 'syspath.go no longer declares `var dirs = []string{…}` on one line').toBeTruthy();
    const fromGo = decl![1].split(',').map((id) => consts[id.trim()]);
    expect(fromGo.every(Boolean), `unresolved identifier in ${decl![1]}`).toBe(true);
    expect([...INTERNAL_DIR_NAMES]).toEqual(fromGo);
  });

  it('names the same keep marker', () => {
    expect(KEEP_MARKER_NAME).toBe(consts.KeepMarker);
  });

  it("is the desktop app's name for its working area", () => {
    const ts = fs.readFileSync(OPENWITH_TS, 'utf8');
    const m = ts.match(/export const SCRATCH_DIR_NAME = '([^']+)'/);
    expect(m, 'desktop/src/openwith.ts no longer exports SCRATCH_DIR_NAME').toBeTruthy();
    expect(m![1]).toBe(consts.OpenWith);
    expect(INTERNAL_DIR_NAMES).toContain(m![1]);
  });
});

describe('isInternalPath', () => {
  it.each([
    ['docs://.filex-open', true],
    ['docs://.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx', true],
    ['docs/.filex-trash', true], // the address-bar hash form
    ['docs://Projects/.versions/7/1', true],
    ['docs://Photos/.keepdir', true],
    ['/.thumbs/a.jpg', true],
    // A person's own names that merely CONTAIN one. The old filter was
    // `path.includes('.thumbs')` / `path.includes('.versions')` and hid the
    // first two from their owner.
    ['docs://my.thumbs.txt', false],
    ['docs://notes.versions/a.txt', false],
    ['docs://.filex-openers.md', false],
    ['docs://Documents/report.pdf', false],
    ['docs://', false],
    ['', false],
  ])('%s → %s', (p, want) => {
    expect(isInternalPath(p)).toBe(want);
  });

  it('isInternalName covers every directory and the marker, nothing else', () => {
    for (const n of INTERNAL_DIR_NAMES) expect(isInternalName(n)).toBe(true);
    expect(isInternalName(KEEP_MARKER_NAME)).toBe(true);
    expect(isInternalName('.config')).toBe(false);
    expect(isInternalName('.trash')).toBe(false); // the VIRTUAL view, not a directory
  });
});

describe('filterInternalEntries', () => {
  const row = (p: string): FileNode =>
    ({ path: p, basename: p.split('/').pop() || p, type: 'file', size: 1 }) as FileNode;

  it('drops filex directories by name and by path, and keeps a person\'s look-alikes', () => {
    const kept = filterInternalEntries([
      row('docs://.filex-open'),
      row('docs://.filex-open/0123456789ab-Plan.docx'), // a search hit from an older server
      row('docs://.versions'),
      row('docs://Photos/.keepdir'),
      row('docs://my.thumbs.txt'),
      row('docs://.versions-notes.txt'),
      row('docs://Documents'),
    ]).map((f) => f.path);
    expect(kept).toEqual(['docs://my.thumbs.txt', 'docs://.versions-notes.txt', 'docs://Documents']);
  });
});

describe('listingAddress — a person never lands inside one', () => {
  it.each([
    ['docs://.filex-open', 'docs://'],
    ['docs://.filex-open/sub', 'docs://'],
    ['My files://Projects/.versions/7', 'My files://'],
    ['docs://Documents', 'docs://Documents'],
    ['docs://', 'docs://'],
  ])('%s → %s', (wire, want) => {
    expect(listingAddress(wire)).toBe(want);
  });

  afterEach(() => vi.unstubAllGlobals());

  it('the explorer\'s own listing call asks for the storage, not the machinery', async () => {
    const asked: string[] = [];
    vi.stubGlobal('fetch', async (url: string) => {
      asked.push(new URL(url, 'http://x').searchParams.get('path') ?? '');
      return new Response(JSON.stringify({ adapter: 'docs', dirname: 'docs://', files: [] }), { status: 200 });
    });
    const api = useFileApi({ endpoint: '/api/files/manager' } as never);
    await api.index('docs://.filex-open');
    await api.search('docs://.filex-open', 'Plan');
    await api.index('docs://Documents');
    expect(asked).toEqual(['docs://', 'docs://', 'docs://Documents']);
  });
});
