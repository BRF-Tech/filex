/**
 * Files and folders dropped INTO filex from the operating system (#94).
 *
 * A drop hands the page two views of the same things, and they disagree about
 * folders:
 *
 * - `dataTransfer.files` is a flat list of `File`s. A dropped FOLDER is in it
 *   too, as a File named after the folder, and what that File holds depends on
 *   the browser: Safari on macOS gives the folder ZIPPED (no extension, type ""
 *   or application/zip) - uploaded as is it became a zip file named after the
 *   folder; Firefox gives an empty File, which uploads as an empty file named
 *   after the folder; Chromium gives a placeholder that cannot be read (size 0,
 *   or the directory's 4096 on Linux), so the upload failed. None is the
 *   folder (measured with real folder drags from a file manager, #94).
 * - `dataTransfer.items[i].webkitGetAsEntry()` gives a `FileSystemEntry`, and a
 *   folder's entry is a `FileSystemDirectoryEntry` that can be walked. Safari
 *   (11.1+), Chromium and Firefox (50+) all have it.
 *
 * So every drop target goes through `takeDrop`, and only a browser without the
 * entry API falls back to `files`.
 *
 * ⚠ `takeDrop` MUST run synchronously inside the `drop` handler, before any
 * `await`: once the event has been dispatched the DataTransfer is emptied
 * (`items.length` reads 0, `files` is empty). The entries it returns stay
 * readable afterwards, which is what lets `readDrop` walk them at leisure.
 *
 * ⚠ For a folder item `getAsFile()` is never asked: on Safari that is the zip.
 * A plain file item keeps `getAsFile()` - the same File `files` holds - so a
 * drop of files uploads exactly what it uploaded before this module existed.
 */

/** The part of `FileSystemEntry` (and its two subclasses) this module reads. */
export interface DropEntry {
  readonly isFile: boolean;
  readonly isDirectory: boolean;
  readonly name: string;
  /** FileSystemFileEntry. */
  file?(success: (file: File) => void, failure?: (err: unknown) => void): void;
  /** FileSystemDirectoryEntry. */
  createReader?(): {
    readEntries(success: (entries: DropEntry[]) => void, failure?: (err: unknown) => void): void;
  };
}

/** One dropped thing, taken while the drop event was still being dispatched. */
export type DropSource = { kind: 'file'; file: File } | { kind: 'entry'; entry: DropEntry };

/** The drop as uploaded: folders (relative, parents before children) and files with the folder they go in. */
export interface DroppedTree {
  /** Every dropped folder and every folder below one, empty ones included: `Album`, `Album/2024`. */
  dirs: string[];
  /** `dir` is '' for a file dropped on its own, else one of `dirs`. */
  files: Array<{ file: File; dir: string }>;
  /** Relative paths of entries that could not be read (a file the OS refused, a folder that vanished). */
  unreadable: string[];
}

type ItemLike = {
  kind: string;
  getAsFile(): File | null;
  webkitGetAsEntry?: () => DropEntry | null;
  getAsEntry?: () => DropEntry | null;
};

/**
 * What was dropped, read SYNCHRONOUSLY from the event's DataTransfer (call it
 * in the `drop` handler before anything awaits).
 */
export function takeDrop(dt: DataTransfer | null | undefined): DropSource[] {
  if (!dt) return [];
  const items = dt.items as unknown as ArrayLike<ItemLike> | undefined;
  if (items && items.length > 0) {
    const out: DropSource[] = [];
    let sawFile = false;
    for (let i = 0; i < items.length; i++) {
      const it = items[i];
      if (!it || it.kind !== 'file') continue;
      sawFile = true;
      const getEntry = it.webkitGetAsEntry ?? it.getAsEntry;
      const entry = typeof getEntry === 'function' ? getEntry.call(it) : null;
      if (entry && entry.isDirectory) {
        out.push({ kind: 'entry', entry });
        continue;
      }
      const file = it.getAsFile();
      if (file) out.push({ kind: 'file', file });
      else if (entry && entry.isFile) out.push({ kind: 'entry', entry });
    }
    if (sawFile) return out;
  }
  return Array.from(dt.files ?? []).map((file) => ({ kind: 'file' as const, file }));
}

function fileOf(entry: DropEntry): Promise<File> {
  return new Promise((resolve, reject) => {
    if (typeof entry.file !== 'function') {
      reject(new Error('not a file entry'));
      return;
    }
    entry.file(resolve, reject);
  });
}

/** Every child of a folder entry: `readEntries` answers in batches (Chromium: 100) until it answers none. */
async function childrenOf(entry: DropEntry): Promise<DropEntry[]> {
  if (typeof entry.createReader !== 'function') throw new Error('not a folder entry');
  const reader = entry.createReader();
  const all: DropEntry[] = [];
  for (;;) {
    const batch = await new Promise<DropEntry[]>((resolve, reject) => reader.readEntries(resolve, reject));
    if (batch.length === 0) return all;
    all.push(...batch);
  }
}

function join(dir: string, name: string): string {
  return dir ? `${dir}/${name}` : name;
}

/** Walk what `takeDrop` took into folders and files. */
export async function readDrop(sources: DropSource[]): Promise<DroppedTree> {
  const tree: DroppedTree = { dirs: [], files: [], unreadable: [] };
  const walk = async (entry: DropEntry, dir: string): Promise<void> => {
    const here = join(dir, entry.name);
    if (entry.isDirectory) {
      let children: DropEntry[];
      try {
        children = await childrenOf(entry);
      } catch {
        tree.unreadable.push(here);
        return;
      }
      tree.dirs.push(here);
      for (const child of children) await walk(child, here);
      return;
    }
    if (!entry.isFile) return;
    try {
      tree.files.push({ file: await fileOf(entry), dir });
    } catch {
      tree.unreadable.push(here);
    }
  };
  for (const s of sources) {
    if (s.kind === 'file') tree.files.push({ file: s.file, dir: '' });
    else await walk(s.entry, '');
  }
  return tree;
}

/** Folders of the tree that end up with no file anywhere below them. */
export function emptyDirs(tree: DroppedTree): string[] {
  const filled = new Set<string>();
  for (const { dir } of tree.files) {
    for (let d = dir; d; d = d.includes('/') ? d.slice(0, d.lastIndexOf('/')) : '') filled.add(d);
  }
  return tree.dirs.filter((d) => !filled.has(d));
}

export interface DropUploadSteps {
  /** Make folder `name` in `parentWire`; resolves to the new folder's wire path (an existing folder of that name is reused). */
  mkdir(parentWire: string, name: string): Promise<string>;
  /** Upload `files` into `dirWire`; a rejection is reported through `failed` and the next folder goes on. */
  upload(files: File[], dirWire: string): Promise<void>;
  /** A folder could not be made (nothing below it is sent), or its files could not be. */
  failed(relPath: string, err: unknown): void;
}

/**
 * Upload a dropped tree into `targetWire`: every folder first, parents before
 * children (empty ones too), then each folder's files into it. A folder that
 * could not be made takes its whole subtree out, and says so once.
 */
export async function uploadDroppedTree(tree: DroppedTree, targetWire: string, steps: DropUploadSteps): Promise<void> {
  const wireOf = new Map<string, string>([['', targetWire]]);
  for (const dir of tree.dirs) {
    const cut = dir.lastIndexOf('/');
    const parent = wireOf.get(cut === -1 ? '' : dir.slice(0, cut));
    if (parent === undefined) continue;
    try {
      wireOf.set(dir, await steps.mkdir(parent, dir.slice(cut + 1)));
    } catch (err) {
      steps.failed(dir, err);
    }
  }
  const byDir = new Map<string, File[]>();
  for (const { file, dir } of tree.files) {
    const list = byDir.get(dir);
    if (list) list.push(file);
    else byDir.set(dir, [file]);
  }
  for (const [dir, files] of byDir) {
    const at = wireOf.get(dir);
    if (at === undefined) continue;
    try {
      await steps.upload(files, at);
    } catch (err) {
      steps.failed(dir || files[0]!.name, err);
    }
  }
}

/**
 * The file as a folder upload's File: `webkitRelativePath` names where it goes
 * below the drop target (the vault's upload reads it to make the folders). A
 * File from an entry carries an empty one and the property is a read-only
 * getter on the prototype, so the File is given its own.
 */
export function withRelativePath(file: File, relPath: string): File {
  Object.defineProperty(file, 'webkitRelativePath', { value: relPath, configurable: true });
  return file;
}
