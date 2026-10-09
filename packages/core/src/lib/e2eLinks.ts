/**
 * e2eLinks - may the explorer offer Share (a public link or a file request)
 * for these rows? wiring:e2
 *
 * Never for an end-to-end encrypted folder or anything inside one: a link
 * hands a visitor ciphertext they cannot open (and the folder's key file with
 * it), and a file request stores their uploads in the folder unencrypted. The
 * server refuses both with 409 E2E_ENCRYPTED on every door
 * (backend publicLinkRefusal); this is the same rule on the client, so the
 * menu does not offer what the server will refuse.
 *
 * "Inside one" is told three ways, all of which the server already sends: the
 * listing the rows came from is an encrypted folder (`insideEncrypted`, the
 * explorer's e2eActive), a row IS one (`e2e: true`, the folder badge in its
 * parent's listing), or a row from Recent, Starred, a tag view or a search
 * names the folder it sits in (`e2e_root`).
 *
 * A single encrypted file (`.fxe`) outside any folder is NOT refused: it
 * carries its own key slots, and its recipient opens it with its password
 * (docs/E2E-ENCRYPTION.md, "Single encrypted files").
 */
import { rowInEncryptedFolder, type EncryptedRowLike } from './encryptedRow';

/** What the rule reads of a row (lib/encryptedRow: `e2e`, `e2e_root`, and a
 *  file row's `encrypted` - `file`, a `.fxe`, is not refused). */
export type E2eLinkRow = EncryptedRowLike;

export function publicLinksOff(rows: readonly E2eLinkRow[], insideEncrypted: boolean): boolean {
  if (insideEncrypted) return true;
  return rows.some(rowInEncryptedFolder);
}
