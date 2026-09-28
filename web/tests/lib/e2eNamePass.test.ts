// The name pass (packages/core/src/lib/e2enamepass.ts): raising a folder from
// level 1 to level 2, finishing that after a stop, and repairing names that
// were moved over WebDAV into another folder of the same encrypted folder.
// Runs against an in-memory tree, so every rename is visible and ordered.

import { beforeAll, describe, expect, it } from 'vitest';

import {
  decryptStoredName,
  effectiveDirId,
  encryptName,
  importNameKey,
  type E2eNameKey,
} from '../../../packages/core/src/lib/e2enames';
import { numberedName, runNamePass, type NamePassIo, type NamePassRow } from '../../../packages/core/src/lib/e2enamepass';

const ROOT = 'local://Kasa';

/** A folder tree by wire path: 'dir', or a file's content. */
class FakeTree {
  readonly nodes = new Map<string, 'dir' | string>();
  readonly renames: Array<{ from: string; to: string }> = [];
  constructor() {
    this.nodes.set(ROOT, 'dir');
  }
  mkdir(p: string) {
    this.nodes.set(p, 'dir');
  }
  put(p: string, content = '') {
    this.nodes.set(p, content);
  }
  children(dir: string): NamePassRow[] {
    const out: NamePassRow[] = [];
    for (const [p, v] of this.nodes) {
      if (!p.startsWith(dir + '/')) continue;
      const rest = p.slice(dir.length + 1);
      if (rest.includes('/')) continue;
      out.push({ path: p, basename: rest, type: v === 'dir' ? 'dir' : 'file' });
    }
    return out.sort((a, b) => a.basename.localeCompare(b.basename));
  }
  move(from: string, to: string) {
    if (this.nodes.has(to)) throw new Error(`exists: ${to}`);
    for (const [p, v] of [...this.nodes]) {
      if (p === from || p.startsWith(from + '/')) {
        this.nodes.delete(p);
        this.nodes.set(to + p.slice(from.length), v);
      }
    }
  }
  io(stopAfter = Infinity): NamePassIo {
    let done = 0;
    return {
      list: async (dir) => this.children(dir),
      readSidecar: async (dir, name) => {
        const v = this.nodes.get(`${dir}/${name}`);
        return typeof v === 'string' ? v : null;
      },
      writeSidecar: async (dir, name, content) => {
        this.put(`${dir}/${name}`, content);
      },
      rename: async (dir, row, to) => {
        this.move(row.path, `${dir}/${to}`);
        this.renames.push({ from: row.path, to: `${dir}/${to}` });
        done++;
      },
      stopped: () => done >= stopAfter,
    };
  }
  /** Every entry's plaintext path, read the way the explorer reads it.
   *  `mark` shows a never-encrypted name as <plain:…> instead of as itself. */
  async plainTree(nk: E2eNameKey, mark = false): Promise<string[]> {
    const out: string[] = [];
    const walk = async (dir: string, id: Uint8Array, rel: string) => {
      for (const r of this.children(dir)) {
        const got = await decryptStoredName(nk, r.basename, id, async (n) => {
          const v = this.nodes.get(`${dir}/${n}`);
          return typeof v === 'string' ? v : null;
        });
        if (got.state === 'sidecar') continue;
        const name =
          got.state === 'enc' || (got.state === 'plain' && !mark) ? got.name! : `<${got.state}:${r.basename}>`;
        out.push(rel + name + (r.type === 'dir' ? '/' : ''));
        if (r.type === 'dir') await walk(r.path, await effectiveDirId(nk, id, r.basename), rel + name + '/');
      }
    };
    await walk(ROOT, nk.rootId, '');
    return out.sort();
  }
}

const PLAIN = [
  'Bütçe.xlsx',
  'Sözleşmeler/',
  'Sözleşmeler/2024/',
  'Sözleşmeler/2024/fatura.pdf',
  'Sözleşmeler/2025/',
  'Sözleşmeler/2025/fatura.pdf',
  `${'ş'.repeat(100)}.txt`,
  `${'Klasör-'.repeat(25)}/`,
  `${'Klasör-'.repeat(25)}/iç.txt`,
].sort();

function plainTree(): FakeTree {
  const t = new FakeTree();
  for (const p of PLAIN) {
    if (p.endsWith('/')) t.mkdir(`${ROOT}/${p.slice(0, -1)}`);
    else t.put(`${ROOT}/${p}`, `content of ${p}`);
  }
  return t;
}

describe('the name pass', () => {
  let nk: E2eNameKey;
  beforeAll(async () => {
    nk = await importNameKey(new Uint8Array(64).fill(5), 220, new Uint8Array(16).fill(1));
  });

  it('raises a plaintext tree to encrypted names, contents before their folder', async () => {
    const t = plainTree();
    const prog = { renamed: 0, failed: 0, seen: 0 };
    await runNamePass(nk, ROOT, t.io(), prog);
    expect(prog).toMatchObject({ renamed: PLAIN.length, failed: 0, seen: PLAIN.length });
    expect(await t.plainTree(nk)).toEqual(PLAIN);
    // No plaintext name is left on the "server".
    for (const p of t.nodes.keys()) expect(p).not.toMatch(/Sözleşme|fatura|Bütçe|Klasör|ş{10}/);
    // Every folder was renamed after everything inside it.
    t.renames.forEach((r, i) => {
      const later = t.renames.slice(i + 1);
      expect(later.some((l) => l.from.startsWith(r.to + '/'))).toBe(false);
    });
    // The same name in two folders came out two different stored names.
    const faturas = [...t.nodes.keys()].filter((p) => p.split('/').length === 6);
    expect(faturas).toHaveLength(2);
    expect(new Set(faturas.map((p) => p.slice(p.lastIndexOf('/') + 1))).size).toBe(2);
  });

  it('continues after a stop, and ends where one uninterrupted pass ends', async () => {
    const t = plainTree();
    const prog = { renamed: 0, failed: 0, seen: 0 };
    await runNamePass(nk, ROOT, t.io(3), prog);
    expect(prog.renamed).toBe(3);
    // Half-way: what was renamed reads, what was not is still plaintext.
    const mid = await t.plainTree(nk);
    expect(mid).toEqual(PLAIN);
    // Counted by the entry's OWN name (a parent still plaintext shows in the path).
    const own = (p: string) => /<plain:[^/]*>\/?$/.test(p);
    expect((await t.plainTree(nk, true)).filter(own).length).toBe(PLAIN.length - 3);
    const again = { renamed: 0, failed: 0, seen: 0 };
    await runNamePass(nk, ROOT, t.io(), again);
    expect(again.renamed).toBe(PLAIN.length - 3);
    expect(await t.plainTree(nk)).toEqual(PLAIN);
    // A third run finds nothing to do.
    const idle = { renamed: 0, failed: 0, seen: 0 };
    await runNamePass(nk, ROOT, t.io(), idle);
    expect(idle).toEqual({ renamed: 0, failed: 0, seen: 0 });
  });

  it('repairs a file and a folder moved over WebDAV into another folder', async () => {
    const t = plainTree();
    await runNamePass(nk, ROOT, t.io(), { renamed: 0, failed: 0, seen: 0 });
    const stored = async (plainPath: string) => {
      // Find the stored path of a plaintext path by walking.
      let dir = ROOT;
      let id = nk.rootId;
      for (const seg of plainPath.split('/')) {
        for (const r of t.children(dir)) {
          const got = await decryptStoredName(nk, r.basename, id, async (n) => {
            const v = t.nodes.get(`${dir}/${n}`);
            return typeof v === 'string' ? v : null;
          });
          if (got.name === seg) {
            id = await effectiveDirId(nk, id, r.basename);
            dir = r.path;
            break;
          }
        }
      }
      return dir;
    };
    const fatura = await stored('Sözleşmeler/2024/fatura.pdf');
    const y2025 = await stored('Sözleşmeler/2025');
    const soz = await stored('Sözleşmeler');
    // A WebDAV client moves 2024/fatura.pdf up into Sözleşmeler, and the
    // folder 2025 up to the root — names untouched, as WebDAV would.
    t.move(fatura, `${soz}/${fatura.slice(fatura.lastIndexOf('/') + 1)}`);
    t.move(y2025, `${ROOT}/${y2025.slice(y2025.lastIndexOf('/') + 1)}`);
    const broken = await t.plainTree(nk, true);
    expect(broken.some((p) => p.startsWith('Sözleşmeler/<plain:'))).toBe(true);
    expect(broken.some((p) => p.startsWith('<unreadable:'))).toBe(true);

    const prog = { renamed: 0, failed: 0, seen: 0, repaired: 0 };
    await runNamePass(nk, ROOT, t.io(), prog);
    expect(prog).toMatchObject({ renamed: 2, repaired: 2, failed: 0 });
    const fixed = await t.plainTree(nk, true);
    expect(fixed).toContain('Sözleşmeler/fatura.pdf');
    expect(fixed).toContain('2025/');
    // The moved folder kept its id: what is inside it reads untouched.
    expect(fixed).toContain('2025/fatura.pdf');
    expect(fixed.some((p) => p.includes('<'))).toBe(false);
  });

  it('keeps both when a plaintext name meets the same name already encrypted', async () => {
    const t = new FakeTree();
    const enc = await encryptName(nk, 'not.txt', nk.rootId);
    t.put(`${ROOT}/${enc.stored}`, 'encrypted one');
    t.put(`${ROOT}/not.txt`, 'plaintext one');
    const prog = { renamed: 0, failed: 0, seen: 0 };
    await runNamePass(nk, ROOT, t.io(), prog);
    expect(prog).toMatchObject({ renamed: 1, failed: 0 });
    expect(await t.plainTree(nk)).toEqual(['not (2).txt', 'not.txt']);
  });

  it('counts, and leaves alone, a folder name it cannot read', async () => {
    const t = new FakeTree();
    // Folder-shaped (S.D), so ours by its spelling — and it opens under no id.
    t.mkdir(`${ROOT}/${'A'.repeat(30)}.${'A'.repeat(22)}`);
    const prog = { renamed: 0, failed: 0, seen: 0 };
    await runNamePass(nk, ROOT, t.io(), prog);
    expect(prog).toMatchObject({ renamed: 0, failed: 1, seen: 1 });
  });

  it('numbers a name the way an upload conflict is numbered', () => {
    expect(numberedName('rapor.pdf', 2)).toBe('rapor (2).pdf');
    expect(numberedName('.gizli', 3)).toBe('.gizli (3)');
    expect(numberedName('Klasör', 2)).toBe('Klasör (2)');
  });
});
