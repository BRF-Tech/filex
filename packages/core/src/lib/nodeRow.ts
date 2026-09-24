/**
 * nodeRow — the starred / recently-opened / tag endpoints answer with raw node
 * rows (relative `path`, numeric `storage_id`, RFC3339 stamps), not the
 * listing shape. This turns one row into the `FileNode` every view renders.
 *
 * Pure on purpose: the explorer wraps it with its own config, and the unit
 * test pins the two facts the context menu depends on — `perm` and
 * `read_only` — without mounting the component.
 */
import type { FileNode } from '../types';

/** What the explorer knows about the install, as far as a row needs it. */
export interface NodeRowContext {
  /** `config.storages` — names, and the host's read-only flag per storage. */
  storages: ReadonlyArray<{ name: string; readOnly?: boolean }>;
  /** Multi-storage root mode: a row that cannot be addressed by storage name
   *  is dropped rather than guessed into somebody else's drive. */
  multiStorageRoot: boolean;
}

const PERM_LEVELS = new Set(['none', 'viewer', 'editor', 'owner']);

/** tablo:t1 — an RFC3339 stamp from a node row as unix ms, or undefined. A
 *  string we cannot parse is left undefined rather than turned into `NaN`,
 *  which would print as "Invalid Date" and sort unpredictably. */
export function rowMillis(v: unknown): number | undefined {
  if (typeof v !== 'string' || !v) return undefined;
  const ms = Date.parse(v);
  return Number.isFinite(ms) ? ms : undefined;
}

/**
 * The storage NAME is what a qualified path needs, and a node row does not
 * carry the id-to-name mapping. The backend fills `storage` for exactly this
 * (handlers/meta.go, `(*Meta).rows`); against an older server the only safe
 * fallback is the single-storage case — guessing in a multi-storage install
 * sends the user to a path in somebody else's drive.
 */
export function nodeRowToFileNode(
  row: Record<string, unknown>,
  ctx: NodeRowContext,
): FileNode | null {
  const rel = String(row?.path ?? '').replace(/^\/+/, '');
  if (!rel) return null;
  const configured = ctx.storages ?? [];
  const storageName =
    typeof row.storage === 'string' && row.storage
      ? row.storage
      : configured.length === 1
        ? configured[0].name
        : '';
  if (ctx.multiStorageRoot && !storageName) return null;
  const name = String(row.name ?? rel.split('/').pop() ?? '');
  const isDir = row.type === 'dir';
  /* issue #34 — these endpoints answer with the raw node row, so `type` here
     is `model.NodeType`: 'file', 'dir' OR 'symlink'. The listing projector
     collapses that third value to 'file' and sets a `symlink` flag beside it;
     `handlers/meta.go` does not, because it ships the node struct as it is.
     Without this line a link the server will not follow appears on Recent,
     Starred, a tag view or the Home tray as a plain file with no badge and no
     explanation — the same fix reaching the folder listing and stopping at
     the view beside it, which is the split lesson #174 was written about. */
  const isSymlink = row.type === 'symlink';
  const size = typeof row.size === 'number' ? row.size : 0;
  const id = typeof row.id === 'number' ? row.id : undefined;
  /* ⚠⚠ THE CONTEXT MENU MUST BE THE SAME EVERYWHERE (owner, 2026-09-19). The
     menu gates Rename / Delete / Move to / Share on the row's own `perm` and
     on the storage's `read_only` — a folder listing carries both, and until
     these two lines a Recent / Starred / tag / Home row carried neither, so
     the menu fell back to the folder opened LAST: nothing on the landing page
     (every write verb gone), or a stale writable folder (every write verb
     offered on a read-only mount). The row says for itself what may be done
     to it, because in these views there is no folder to ask.
     `read_only` prefers the server's word and falls back to the host's own
     storage list for a server that predates the field. */
  const perm =
    typeof row.perm === 'string' && PERM_LEVELS.has(row.perm)
      ? (row.perm as FileNode['perm'])
      : undefined;
  const hostReadOnly = configured.find((s) => s.name === storageName)?.readOnly === true;
  const readOnly = typeof row.read_only === 'boolean' ? row.read_only : hostReadOnly;
  return {
    type: isDir ? 'dir' : 'file',
    id,
    /* tablo:t1 — ⚠⚠ THE DATE. A node row carries `backend_mtime` (what the
       storage says) and `db_mtime` (what our last scan recorded) as RFC3339
       strings; `FileNode.last_modified` is unix MILLISECONDS. Nothing mapped
       between the two, so every row from Recent, Starred and a tag view
       arrived with no date at all — measured on Recent: eleven rows, eleven em
       dashes in the Modified column, and a Modified column header you could
       click that then sorted nothing. It also made the date grouping this view
       is supposed to show impossible, because every row fell in the "No date"
       bucket. Storage first: `backend_mtime` is the file's own truth and
       `db_mtime` only says when we last looked at it. */
    last_modified: rowMillis(row.backend_mtime) ?? rowMillis(row.db_mtime),
    path: storageName ? `${storageName}://${rel}` : rel,
    basename: name,
    extension: isDir
      ? ''
      : name.includes('.')
        ? (name.split('.').pop() || '').toLowerCase()
        : '',
    storage: storageName,
    visibility: 'private',
    size,
    file_size: size,
    mime_type: typeof row.mime === 'string' ? row.mime : '',
    ...(perm ? { perm } : {}),
    read_only: readOnly,
    ...(isSymlink ? { symlink: true } : {}),
    // ⚠⚠ The SERVER's `thumb_url`, never one built here. It used to be
    // `/api/files/thumb/<id>` for every file, and the endpoint answers
    // `404 "not ready"` for everything it has not rendered — a docx, a
    // markdown note, a diagram: opening Recent with ten such files fired ten
    // requests and all ten 404'd (2026-09-21). The rows now carry the URL
    // exactly when there is an image behind it (handlers/meta.go, the folder
    // listing's rule), so a file with none is never asked about.
    thumb_url: !isDir && typeof row.thumb_url === 'string' && row.thumb_url ? row.thumb_url : undefined,
    extra_metadata: {},
  } as unknown as FileNode;
}
