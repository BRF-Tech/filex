/**
 * encryptedRow - is this row end-to-end encrypted, in the SERVER's words?
 * wiring:e2, task #189
 *
 * The one place the explorer reads it, for every "not for an encrypted item"
 * a row can answer for itself:
 *
 *  - no app is offered it ("Open with" an interface, an app's action) and no
 *    ONLYOFFICE - only filex's own viewer opens it, decrypting in the tab;
 *    the server holds only ciphertext and refuses an app its bytes
 *    (`403 encrypted`): rowIsEncrypted, read by lib/appViewer and
 *    lib/pluginMenu;
 *  - no public link is made for an encrypted folder or anything in it
 *    (lib/e2eLinks): rowInEncryptedFolder. A single encrypted file (`.fxe`)
 *    is the exception there: it carries its own key slots, and its recipient
 *    opens it with its password.
 *
 * A folder listing knows it for every row at once (the explorer's e2eActive,
 * from the listing's `e2e_root`); a row from Recent, Starred, a tag view, a
 * search or Shared with me says it itself, in the fields the server writes:
 *
 *  - `encrypted` on a FILE row (handlers/e2e_rows.go encryptedKind):
 *    `folder` inside an encrypted folder, `vault` inside a vault (a vault's
 *    own rows too), `file` for a single encrypted file (`.fxe`) outside both;
 *  - `e2e: true`: the row IS an encrypted folder (its parent's listing);
 *  - `e2e_root`: anything inside one, a subfolder too (rows outside a folder
 *    listing name the folder they sit in).
 */

/** How a file is end-to-end encrypted, as the server stamps its row. */
export type EncryptedKind = 'folder' | 'vault' | 'file';

/** A value the server sends as a row's `encrypted` (anything else is not one). */
export function isEncryptedKind(v: unknown): v is EncryptedKind {
  return v === 'folder' || v === 'vault' || v === 'file';
}

/** What the rules read of a row. */
export interface EncryptedRowLike {
  e2e?: boolean;
  e2e_root?: string | null;
  encrypted?: string | null;
}

/** The row is an encrypted folder or inside one (or inside a vault). */
export function rowInEncryptedFolder(row: EncryptedRowLike | null | undefined): boolean {
  if (!row) return false;
  return (
    row.e2e === true ||
    row.encrypted === 'folder' ||
    row.encrypted === 'vault' ||
    (typeof row.e2e_root === 'string' && row.e2e_root !== '')
  );
}

/** The server holds this row only as ciphertext: inside an encrypted folder
 *  or a vault, or a single encrypted file (see the header). */
export function rowIsEncrypted(row: EncryptedRowLike | null | undefined): boolean {
  if (!row) return false;
  return rowInEncryptedFolder(row) || row.encrypted === 'file';
}
