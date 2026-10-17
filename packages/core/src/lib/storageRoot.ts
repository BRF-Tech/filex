/**
 * #117 — a whole storage, given to people and groups.
 *
 * A storage is handed to a person or a group by a grant on its ROOT (the
 * path prefix "", backend model.FileGrant): the same row a folder grant is,
 * so the same permission routes, the same resolver and the same answer on
 * every door (the web interface, the desktop app, WebDAV, SFTP, FTP, NFS, the
 * S3 API, an AI agent). Two screens make one: the admin panel's storage page
 * (its Access section) and the explorer's storage row ("Access…", which
 * opens the access dialog on `<storage>://`).
 *
 * What the dialog offers there is decided here, once, from the PATH, so a
 * host that opens the dialog on a storage root by any other way gets the same
 * answer: people and groups only. No public link and no file request - a
 * link to a whole storage would hand everything in it to whoever holds the
 * address, and the server refuses both (backend public_link_rule.go,
 * errRootLink), so the dialog does not offer what would be refused.
 */
import type { SharingHeld } from './sharingHeld';
import type { FileNode } from '../types/FileNode';

/** Is `path` (`adapter://rel`) a storage's root? A path with no adapter is not. */
export function isStorageRootPath(path: string): boolean {
  const at = path.indexOf('://');
  return at > 0 && path.slice(at + 3).replace(/\/+/g, '') === '';
}

/** The sharing the access dialog may offer on `path`: at a storage root,
 *  giving people and groups access and nothing else. */
export function sharingAtTarget(path: string, held: SharingHeld): SharingHeld {
  return isStorageRootPath(path) ? { ...held, links: false, uploadLinks: false } : held;
}

/** A storage row as the explorer draws it: its virtual path is the
 *  storage's name, and `storage` names it too. */
export interface StorageRowLike {
  path: string;
  basename: string;
  storage?: string;
}

/**
 * The access dialog's target for a storage row: the storage's root on the
 * wire (`alpha` → `alpha://`), a folder, named as the row is.
 *
 * `perm` is `owner` because the row offers "Access…" only to whoever owns
 * the storage's root - an administrator, or a person given the whole storage
 * as owner (storageRowMenu, managesStorageRoot) - so the dialog reads the
 * root's grants without asking again.
 */
export function storageRootTarget(row: StorageRowLike): FileNode {
  const name = String(row.storage || row.path).replace(/:\/\/.*$/, '').replace(/^\/+|\/+$/g, '');
  return {
    path: `${name}://`,
    basename: row.basename || name,
    type: 'dir',
    mime_type: 'inode/storage',
    perm: 'owner',
  };
}

/** The `ContextAction` fields a storage row fills (the menu's own type owns the rest). */
export interface StorageRowMenuRow {
  key: string;
  label: string;
  icon?: string;
  hidden?: boolean;
}

/**
 * What a storage row's menu offers: open it, open it in a new tab and - for
 * whoever manages the root (managesStorageRoot) and may give people access
 * (`share.users`) - "Access…", the whole storage given to people and groups.
 * A storage row is a mount point, not a file: nothing that would rename,
 * move, delete or link it.
 *
 * ⚠ One list for both places a storage row is drawn - the main listing's
 * virtual root and the split pane - so the two cannot drift (the pane
 * offered "Delete" on a whole storage once, pane:p1).
 */
export function storageRowMenu(
  t: (key: string) => string,
  opts: { manages: boolean; shareUsers: boolean },
): StorageRowMenuRow[] {
  return [
    { key: 'open', label: t('ctx.open') },
    { key: 'open-tab', label: t('ctx.open_new_tab') },
    {
      key: 'storage-access',
      label: t('ctx.storage_access'),
      icon: 'access',
      hidden: !(opts.manages && opts.shareUsers),
    },
  ];
}

/** The caller's level at a storage's root, as the server says it. */
export type RootLevel = 'none' | 'viewer' | 'editor' | 'owner';
const ROOT_LEVELS: ReadonlySet<string> = new Set(['none', 'viewer', 'editor', 'owner']);

/** A level the server could have said, or undefined. */
export function rootLevel(v: unknown): RootLevel | undefined {
  return typeof v === 'string' && ROOT_LEVELS.has(v) ? (v as RootLevel) : undefined;
}

/**
 * `storage_info` (every listing's, backend manager.go) → storage name → the
 * caller's level at its root (`perm`). An older server says none: the map is
 * then empty and only an administrator is offered "Access…", as before.
 */
export function rootLevelsOf(infos: unknown): Record<string, RootLevel> {
  const out: Record<string, RootLevel> = {};
  if (!Array.isArray(infos)) return out;
  for (const i of infos) {
    const row = i as { name?: unknown; perm?: unknown };
    const level = rootLevel(row?.perm);
    if (typeof row?.name === 'string' && level) out[row.name] = level;
  }
  return out;
}

/**
 * The caller's level at the root of storage `name`: what the host said with
 * its storages (`config.storages[].perm`), else what the last listing said.
 */
export function rootLevelFor(
  name: string,
  host: ReadonlyArray<{ name: string; perm?: string | null }> | undefined,
  learned: Readonly<Record<string, RootLevel>>,
): RootLevel | undefined {
  return rootLevel(host?.find((s) => s.name === name)?.perm) ?? learned[name];
}

/**
 * May the caller manage who has the whole storage? Whoever OWNS its root:
 * an administrator (who owns every path), or a person given the storage as
 * owner. It is the server's rule for the panel routes (handlers/grants.go
 * requireOwner); the explorer offering it to administrators alone hid from
 * such an owner what the API let them do.
 */
export function managesStorageRoot(opts: { admin: boolean; level?: string | null }): boolean {
  return opts.admin || opts.level === 'owner';
}
