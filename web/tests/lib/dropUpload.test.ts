// A folder dropped into filex from the operating system (#94, GitHub issue 94).
//
// Safari on macOS hands a page that reads `dataTransfer.files` a dropped
// folder ZIPPED: one File named after the folder, no extension. The explorer
// read `files` and uploaded it, so a folder became an extensionless zip.
// Chromium and Firefox put a placeholder for the folder in `files` (measured
// with a real folder dragged in from a file manager under Xvfb, and through
// CDP): Firefox an empty File, which uploads as an empty FILE named after the
// folder; Chromium one that cannot be read (size 0, or 4096 on Linux), so the
// XHR fails.
// Every browser also offers the folder as an entry
// (`items[i].webkitGetAsEntry()`), which is what is read now: the folder is
// walked and made again, empty folders included.
//
// The DataTransfer fakes below are shaped like each browser's at drop time.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it, vi } from 'vitest';

import {
  emptyDirs,
  readDrop,
  takeDrop,
  uploadDroppedTree,
  withRelativePath,
  type DropEntry,
  type DroppedTree,
} from '@brftech/filex-core/src/lib/dropUpload';

function fileEntry(name: string, body: string, opts: { fail?: boolean } = {}): DropEntry {
  return {
    isFile: true,
    isDirectory: false,
    name,
    file(ok, no) {
      if (opts.fail) no?.(new Error('NotReadableError'));
      else ok(new File([body], name, { type: 'text/plain' }));
    },
  };
}

/** A folder entry whose reader answers in `batch`-sized pieces, then [] (Chromium answers 100 at a time). */
function dirEntry(name: string, children: DropEntry[], batch = 100): DropEntry {
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader() {
      let at = 0;
      return {
        readEntries(ok) {
          const next = children.slice(at, at + batch);
          at += next.length;
          ok(next);
        },
      };
    },
  };
}

function album(): DropEntry {
  return dirEntry('Album', [fileEntry('a.txt', 'alpha'), dirEntry('sub', [fileEntry('b.txt', 'bravo')]), dirEntry('empty', [])]);
}

type Item = { kind: string; getAsFile: () => File | null; webkitGetAsEntry?: () => DropEntry | null };

/** A DataTransfer at drop time: `items` and `files` side by side, as the browser fills them. */
function dataTransfer(items: Item[], files: File[]): DataTransfer {
  return { items: Object.assign([...items], { length: items.length }), files } as unknown as DataTransfer;
}

/** Records what an upload would do, in order. */
function recorder(opts: { refuse?: string[] } = {}) {
  const log: string[] = [];
  const uploaded: Array<{ dir: string; names: string[]; files: File[] }> = [];
  const failed: string[] = [];
  return {
    log,
    uploaded,
    failed,
    steps: {
      mkdir: vi.fn(async (parent: string, name: string) => {
        const wire = `${parent.endsWith('://') ? parent : `${parent}/`}${name}`;
        if (opts.refuse?.includes(wire)) throw new Error('refused');
        log.push(`mkdir ${wire}`);
        return wire;
      }),
      upload: vi.fn(async (files: File[], dir: string) => {
        log.push(`upload ${dir}: ${files.map((f) => f.name).join(', ')}`);
        uploaded.push({ dir, names: files.map((f) => f.name), files });
      }),
      failed: vi.fn((rel: string) => {
        failed.push(rel);
      }),
    },
  };
}

async function dropInto(dt: DataTransfer, target = 'docs://') {
  const sources = takeDrop(dt);
  const tree = await readDrop(sources);
  const r = recorder();
  await uploadDroppedTree(tree, target, r.steps);
  return { sources, tree, ...r };
}

describe('a folder dropped as Safari hands it over (#94)', () => {
  // `files` holds the folder as a zip; the entry API holds the folder.
  const zip = new File([new Uint8Array([0x50, 0x4b, 0x03, 0x04, 1, 2, 3])], 'Album', { type: '' });

  it('is made again as a folder tree - no zip file is uploaded', async () => {
    const getAsFile = vi.fn(() => zip);
    const dt = dataTransfer([{ kind: 'file', getAsFile, webkitGetAsEntry: () => album() }], [zip]);
    const { tree, log, uploaded } = await dropInto(dt);
    expect(tree.dirs).toEqual(['Album', 'Album/sub', 'Album/empty']);
    expect(log).toEqual([
      'mkdir docs://Album',
      'mkdir docs://Album/sub',
      'mkdir docs://Album/empty',
      'upload docs://Album: a.txt',
      'upload docs://Album/sub: b.txt',
    ]);
    const sent = uploaded.flatMap((u) => u.files);
    expect(sent).not.toContain(zip);
    expect(sent.map((f) => f.name)).not.toContain('Album');
    expect(await sent[0]!.text()).toBe('alpha');
    // The folder's File is never even asked for: on Safari that IS the zip.
    expect(getAsFile).not.toHaveBeenCalled();
  });

  it('is also right when the zip says application/zip', async () => {
    const typed = new File([zip], 'Album', { type: 'application/zip' });
    const dt = dataTransfer([{ kind: 'file', getAsFile: () => typed, webkitGetAsEntry: () => album() }], [typed]);
    const { uploaded } = await dropInto(dt);
    expect(uploaded.flatMap((u) => u.files)).not.toContain(typed);
    expect(uploaded.map((u) => u.dir)).toEqual(['docs://Album', 'docs://Album/sub']);
  });
});

describe('a folder dropped in Chromium or Firefox', () => {
  it('uploads the folder, not the 0-byte placeholder `files` holds for it', async () => {
    const placeholder = new File([], 'Album', { type: '' });
    const dt = dataTransfer([{ kind: 'file', getAsFile: () => placeholder, webkitGetAsEntry: () => album() }], [placeholder]);
    const { log, uploaded } = await dropInto(dt);
    expect(log[0]).toBe('mkdir docs://Album');
    expect(uploaded.flatMap((u) => u.files)).not.toContain(placeholder);
    expect(uploaded.flatMap((u) => u.names)).toEqual(['a.txt', 'b.txt']);
  });

  it('reads a big folder to the end - the reader answers in batches', async () => {
    const many = Array.from({ length: 250 }, (_, i) => fileEntry(`f${i}.txt`, String(i)));
    const dt = dataTransfer([{ kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => dirEntry('Big', many, 100) }], []);
    const { uploaded } = await dropInto(dt);
    expect(uploaded).toHaveLength(1);
    expect(uploaded[0]!.names).toHaveLength(250);
  });
});

describe('files dropped on their own', () => {
  it('upload the very File objects `files` holds, into the drop folder (Chromium/Firefox unchanged)', async () => {
    const a = new File(['1'], 'a.txt');
    const b = new File(['2'], 'b.pdf');
    const dt = dataTransfer(
      [
        { kind: 'file', getAsFile: () => a, webkitGetAsEntry: () => fileEntry('a.txt', 'other bytes') },
        { kind: 'file', getAsFile: () => b, webkitGetAsEntry: () => fileEntry('b.pdf', 'other bytes') },
      ],
      [a, b],
    );
    const { tree, log, uploaded } = await dropInto(dt);
    expect(tree.dirs).toEqual([]);
    expect(tree.files.map((f) => f.file)).toEqual([a, b]);
    expect(tree.files[0]!.file).toBe(a);
    expect(log).toEqual(['upload docs://: a.txt, b.pdf']);
    expect(uploaded[0]!.files[1]).toBe(b);
  });

  it('a string item beside them (a link dragged along) is not a file', () => {
    const a = new File(['1'], 'a.txt');
    const dt = dataTransfer(
      [
        { kind: 'string', getAsFile: () => null },
        { kind: 'file', getAsFile: () => a, webkitGetAsEntry: () => fileEntry('a.txt', '1') },
      ],
      [a],
    );
    expect(takeDrop(dt)).toEqual([{ kind: 'file', file: a }]);
  });

  it('a browser without the entry API falls back to the item, then to `files`', () => {
    const a = new File(['1'], 'a.txt');
    expect(takeDrop(dataTransfer([{ kind: 'file', getAsFile: () => a }], [a]))).toEqual([{ kind: 'file', file: a }]);
    expect(takeDrop({ files: [a] } as unknown as DataTransfer)).toEqual([{ kind: 'file', file: a }]);
    expect(takeDrop(dataTransfer([], [a]))).toEqual([{ kind: 'file', file: a }]);
    expect(takeDrop(null)).toEqual([]);
  });
});

describe('a mixed drop, and what can go wrong', () => {
  it('files beside a folder go into the drop folder, the folder below it', async () => {
    const loose = new File(['x'], 'loose.txt');
    const dt = dataTransfer(
      [
        { kind: 'file', getAsFile: () => loose, webkitGetAsEntry: () => fileEntry('loose.txt', 'x') },
        { kind: 'file', getAsFile: () => new File([], 'Album'), webkitGetAsEntry: () => album() },
      ],
      [loose],
    );
    const { log } = await dropInto(dt);
    expect(log).toContain('upload docs://: loose.txt');
    expect(log).toContain('upload docs://Album: a.txt');
  });

  it('a folder that cannot be made takes its subtree out and is said once', async () => {
    const tree = await readDrop(takeDrop(dataTransfer([{ kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => album() }], [])));
    const r = recorder({ refuse: ['docs://Album/sub'] });
    await uploadDroppedTree(tree, 'docs://', r.steps);
    expect(r.failed).toEqual(['Album/sub']);
    expect(r.log).toEqual(['mkdir docs://Album', 'mkdir docs://Album/empty', 'upload docs://Album: a.txt']);
  });

  it('a folder whose files could not go is reported, and the next folder still goes', async () => {
    const tree = await readDrop([{ kind: 'entry', entry: album() }]);
    const r = recorder();
    const upload = r.steps.upload;
    r.steps.upload = vi.fn(async (files: File[], dir: string) => {
      if (dir === 'docs://Album') throw new Error('left the folder');
      await upload(files, dir);
    });
    await uploadDroppedTree(tree, 'docs://', r.steps);
    expect(r.failed).toEqual(['Album']);
    expect(r.uploaded.map((u) => u.dir)).toEqual(['docs://Album/sub']);
  });

  it('a file the OS will not hand over is reported, the rest goes', async () => {
    const dir = dirEntry('Album', [fileEntry('locked.txt', '', { fail: true }), fileEntry('ok.txt', 'ok')]);
    const tree = await readDrop([{ kind: 'entry', entry: dir }]);
    expect(tree.unreadable).toEqual(['Album/locked.txt']);
    expect(tree.files.map((f) => f.file.name)).toEqual(['ok.txt']);
  });

  it('entries taken at drop time stay readable after the DataTransfer is emptied', async () => {
    const items: Item[] = [{ kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => album() }];
    const dt = dataTransfer(items, []);
    const sources = takeDrop(dt);
    // What the browser does once the drop event is over.
    (dt as unknown as { items: Item[] }).items = Object.assign([], { length: 0 });
    const tree = await readDrop(sources);
    expect(tree.files.map((f) => `${f.dir}/${f.file.name}`)).toEqual(['Album/a.txt', 'Album/sub/b.txt']);
  });
});

describe('the vault side', () => {
  it('knows which folders no file will make', () => {
    const tree: DroppedTree = {
      dirs: ['A', 'A/full', 'A/empty', 'A/empty/deeper', 'B'],
      files: [{ file: new File(['1'], 'x'), dir: 'A/full' }],
      unreadable: [],
    };
    expect(emptyDirs(tree)).toEqual(['A/empty', 'A/empty/deeper', 'B']);
  });

  it('gives a File the relative path the vault upload reads', () => {
    const f = withRelativePath(new File(['1'], 'b.txt'), 'Album/sub/b.txt');
    expect((f as File & { webkitRelativePath: string }).webkitRelativePath).toBe('Album/sub/b.txt');
    expect(f.name).toBe('b.txt');
  });
});

describe('the explorer takes every OS drop through it', () => {
  const src = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const fn = (name: string) => src.match(new RegExp(`(?:async )?function ${name}\\([\\s\\S]*?\\n\\}`))?.[0] ?? '';

  it('reads the drop synchronously, in the drop handler, through takeDrop', () => {
    const drop = fn('onDropUpload');
    expect(drop).toMatch(/const sources = takeDrop\(ev\.dataTransfer\);/);
    expect(drop).toMatch(/void uploadDrop\(sources, qualify\(currentPath\.value\)\);/);
    expect(drop, 'the handler must not await before it has taken the drop').not.toMatch(/\bawait\b/);
    // `files` is read nowhere in the explorer any more: it is where Safari's zip is.
    expect(src).not.toMatch(/dataTransfer\??\.files/);
  });

  it('makes a dropped folder the way New folder makes one, and uploads into each', () => {
    const drop = fn('uploadDrop');
    expect(drop).toMatch(/await uploadDroppedTree\(tree, target, \{\s*mkdir: \(at, name\) => stay\(\(\) => createFolderAt\(at, name\)\),/);
    expect(drop).toMatch(/upload: \(files, at\) => stay\(\(\) => uploadFiles\(files, at, \{ reload: false \}\)\)/);
    expect(drop).toMatch(/await vault\.upload\(target, files\)/);
    expect(drop.indexOf('e2eLocked.value'), 'a locked encrypted folder is refused before a folder is made').toBeLessThan(
      drop.indexOf('uploadDroppedTree('),
    );
    expect(fn('submitNewFolder')).toMatch(/await createFolderAt\(dirWire, name\);/);
  });

  it('stops the rest of a tree when the screen leaves the encrypted folder it was dropped in', () => {
    // uploadFiles encrypts by the folder ON SCREEN; a tree is many calls of it.
    const drop = fn('uploadDrop');
    const at = drop.indexOf('const e2eAtDrop = e2eDropState();');
    expect(at, 'read in the drop handler, before the walk awaits').toBeGreaterThan(-1);
    expect(at).toBeLessThan(drop.indexOf('await readDrop(sources)'));
    expect(drop).toMatch(/e2eDropState\(\) === e2eAtDrop \? go\(\) : Promise\.reject\(/);
    expect(fn('e2eDropState')).toMatch(/e2eActive\.value \? `\$\{e2eRoot\.value\}\|\$\{e2eUnlocked\.value \? 'open' : 'locked'\}` : ''/);
  });
});
