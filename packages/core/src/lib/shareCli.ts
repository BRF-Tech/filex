// The one-line download command shown next to a fresh share link.
//
// It exists because a link is often made for a SERVER, not a person: the fast
// way to get the file onto a box is to paste one curl, not to open a browser.
// The command lived in the old standalone share dialog and was left behind
// when link creation moved into the "Share / Permissions" panel — so it lives
// here now, in one place, and both surfaces render the same string.

/** Wrap a value in single quotes for safe paste into a POSIX shell — an
 *  embedded single quote becomes the standard '\'' dance. */
export function shQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

export interface ShareCliTarget {
  /** The public /s/<token> URL. */
  url: string;
  /** The PIN, when the link has one (it rides in the querystring — the
   *  download endpoint accepts ?pin=). */
  pin?: string | null;
  /** File name, for an explicit -o. Unknown → curl takes the server's
   *  Content-Disposition name with -OJ. */
  filename?: string | null;
  /** Folder links serve a browse PAGE at the bare URL; the archive is behind
   *  ?zip=wait, which blocks until the ZIP is built and then streams it. */
  isDir?: boolean;
}

/**
 * shareCliCommand renders the copy-paste curl for a share link.
 *
 * -L is not optional: an S3-backed instance answers with a 302 to a presigned
 * URL, and without it curl saves the redirect page instead of the file.
 */
export function shareCliCommand(share: ShareCliTarget | null | undefined): string {
  if (!share?.url) return '';
  let url = share.url;
  const params: string[] = [];
  if (share.isDir) params.push('zip=wait');
  if (share.pin) params.push('pin=' + encodeURIComponent(share.pin));
  if (params.length) url += (url.includes('?') ? '&' : '?') + params.join('&');

  let target = '-OJ ';
  if (share.filename) {
    target = `-o ${shQuote(share.isDir ? `${share.filename}.zip` : share.filename)} `;
  }
  return `curl -fSL ${target}${shQuote(url)}`;
}
