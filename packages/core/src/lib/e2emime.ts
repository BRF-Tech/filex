/**
 * e2emime — the MIME type a decrypted blob is handed to the viewers with.
 *
 * The server knows an encrypted file as `application/octet-stream` (and a
 * `.fxe` by a name that says nothing of its type), so the type of the blob a
 * viewer mounts in `<img>`/`<video>`/`<object>` comes from the PLAINTEXT name,
 * here. One table for the folder files and the single encrypted files.
 */
const E2E_MIME: Record<string, string> = {
  txt: 'text/plain', md: 'text/markdown', log: 'text/plain', csv: 'text/csv',
  json: 'application/json', xml: 'application/xml', html: 'text/html',
  jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif',
  webp: 'image/webp', bmp: 'image/bmp', avif: 'image/avif', svg: 'image/svg+xml',
  pdf: 'application/pdf',
  mp4: 'video/mp4', webm: 'video/webm', mov: 'video/quicktime', m4v: 'video/mp4',
  mp3: 'audio/mpeg', wav: 'audio/wav', ogg: 'audio/ogg', flac: 'audio/flac',
  m4a: 'audio/mp4', aac: 'audio/aac', opus: 'audio/opus',
};

/** MIME for a lower-case extension (no dot); octet-stream when unknown. */
export function e2eMimeForExt(ext: string | null | undefined): string {
  return E2E_MIME[(ext || '').toLowerCase()] || 'application/octet-stream';
}
