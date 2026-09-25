/**
 * archiveFormatLabel — how an archive format is written wherever a person
 * reads it: the create dialog, the admin Archives page, its provider cards.
 *
 * Upper case (ZIP, TAR.GZ, RAR), except 7z, which its own project writes in
 * lower case. One rule in one place: the dialog wrote "7z" while the admin
 * page wrote "7Z" for the same format (measured, 0.44.0 integration of #48).
 */
export function archiveFormatLabel(format: string): string {
  const f = format.trim().toLowerCase();
  return f === '7z' ? '7z' : f.toUpperCase();
}
