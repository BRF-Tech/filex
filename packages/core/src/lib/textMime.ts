/**
 * Is this media type text a person edits as text? Any `text/*`, and the
 * structured-text types filed under `application/`.
 *
 * It answers for a file whose NAME says nothing (#56): `LICENSE`, `NOTICE`,
 * `example.custom`. The viewer picks its surface from the extension, so those
 * fell through to "Download"; the server's mime for the bytes (the chosen type
 * for a New document, the sniffed one for an upload) is the fact that is left.
 *
 * ⚠ The same list is `isTextualMime` in backend/internal/api/handlers/
 * save_text.go, which decides that save-text will SAVE such a file. The two
 * answer one question from the two ends, and a file the editor opens but
 * save-text refuses is exactly the bug #56 found — change them together.
 */
const STRUCTURED_TEXT = new Set([
  'application/json',
  'application/xml',
  'application/yaml',
  'application/x-yaml',
  'application/javascript',
  'application/x-sh',
  'application/toml',
]);

export function isTextualMime(mime: string | null | undefined): boolean {
  const m = String(mime ?? '').split(';')[0].trim().toLowerCase();
  if (!m) return false;
  return m.startsWith('text/') || STRUCTURED_TEXT.has(m);
}
