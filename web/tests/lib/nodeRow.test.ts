// nodeRowToFileNode — the row a virtual view renders carries what the context
// menu gates on.
//
// Recent, Starred, a tag view, the Home cards and the Recently-opened tray
// all list raw node rows from the per-user endpoints, and the explorer's ONE
// menu (`selectionActionList`) decides Rename / Delete / Move to / Share from
// the row's `perm` and its storage's `read_only`. Until 2026-09-19 the
// converter dropped both, so those views had no level of their own and the
// menu borrowed the last folder's — nothing on the landing page, so every
// write verb was missing ("son kullanılanlar, ana sayfa gibi sayfalarda
// context menu eksik kalıyor. CONTEXT MENÜ HER YERDE AYNI OLMALI").
import { describe, expect, it } from 'vitest';
import { nodeRowToFileNode } from '../../../packages/core/src/lib/nodeRow';

const single = { storages: [{ name: 'drive' }], multiStorageRoot: false };
const multi = {
  storages: [{ name: 'drive' }, { name: 'frozen', readOnly: true }],
  multiStorageRoot: true,
};

describe('nodeRowToFileNode', () => {
  it('carries the row\'s own perm and read_only onto the FileNode', () => {
    const n = nodeRowToFileNode(
      { id: 7, path: 'docs/a.txt', name: 'a.txt', storage: 'drive', perm: 'editor', read_only: false },
      multi,
    );
    expect(n).not.toBeNull();
    expect(n?.path).toBe('drive://docs/a.txt');
    expect(n?.perm).toBe('editor');
    expect(n?.read_only).toBe(false);
  });

  it('keeps read_only true from the server even on a storage the host thinks is writable', () => {
    const n = nodeRowToFileNode(
      { id: 8, path: 'b.txt', name: 'b.txt', storage: 'drive', perm: 'owner', read_only: true },
      multi,
    );
    expect(n?.read_only).toBe(true);
    expect(n?.perm).toBe('owner');
  });

  it('falls back to the host\'s storage list when an older server sends no read_only', () => {
    const frozen = nodeRowToFileNode({ id: 9, path: 'c.txt', name: 'c.txt', storage: 'frozen' }, multi);
    expect(frozen?.read_only).toBe(true);
    const drive = nodeRowToFileNode({ id: 10, path: 'd.txt', name: 'd.txt', storage: 'drive' }, multi);
    expect(drive?.read_only).toBe(false);
  });

  it('leaves perm undefined — ungated — when the server sends none or nonsense', () => {
    const none = nodeRowToFileNode({ id: 11, path: 'e.txt', storage: 'drive' }, multi);
    expect(none?.perm).toBeUndefined();
    expect('perm' in (none ?? {})).toBe(false);
    const junk = nodeRowToFileNode({ id: 12, path: 'f.txt', storage: 'drive', perm: 'root' }, multi);
    expect(junk?.perm).toBeUndefined();
  });

  it('names the sole storage for a row that carries none, and drops a nameless row at a multi-storage root', () => {
    const named = nodeRowToFileNode({ id: 1, path: '/x/y.txt' }, single);
    expect(named?.path).toBe('drive://x/y.txt');
    expect(named?.storage).toBe('drive');
    expect(nodeRowToFileNode({ id: 2, path: 'z.txt' }, multi)).toBeNull();
    expect(nodeRowToFileNode({ id: 3, path: '' }, single)).toBeNull();
  });

  it('maps the RFC3339 mtimes to unix ms, storage first', () => {
    const n = nodeRowToFileNode(
      { id: 4, path: 'g.txt', storage: 'drive', backend_mtime: '2026-09-19T10:00:00Z', db_mtime: '2026-09-19T11:00:00Z' },
      multi,
    );
    expect(n?.last_modified).toBe(Date.parse('2026-09-19T10:00:00Z'));
    const dbOnly = nodeRowToFileNode({ id: 5, path: 'h.txt', storage: 'drive', db_mtime: 'not a date' }, multi);
    expect(dbOnly?.last_modified).toBeUndefined();
  });

  it('shapes a dir row as a folder with no extension and no thumb', () => {
    const n = nodeRowToFileNode({ id: 6, path: 'sub', name: 'sub', type: 'dir', storage: 'drive' }, multi);
    expect(n?.type).toBe('dir');
    expect(n?.extension).toBe('');
    expect(n?.thumb_url).toBeUndefined();
  });

  it('asks for a thumbnail only where the server said there is one', () => {
    // ⚠ 2026-09-21: every file on Recent / Starred / a tag view got a made-up
    // `/api/files/thumb/<id>`, and every docx, note and diagram answered
    // `404 "not ready"` — ten requests, ten 404s for one Recent view.
    const none = nodeRowToFileNode(
      { id: 82, path: 'letter.docx', name: 'letter.docx', type: 'file', storage: 'drive' },
      multi,
    );
    expect(none?.thumb_url).toBeUndefined();
    const url = '/api/files/thumb/56?exp=1&sig=abc';
    const ready = nodeRowToFileNode(
      { id: 56, path: 'square.jpg', name: 'square.jpg', type: 'file', storage: 'drive', thumb_url: url },
      multi,
    );
    // The server's URL, signature and all — never one rebuilt from the id.
    expect(ready?.thumb_url).toBe(url);
  });
});
