/**
 * Where a file the person asked to open goes: the host's own window, or the
 * in-page viewer.
 *
 * Host-owned open (the desktop app, `config.openInHost`): every file opens in
 * the host's window per document, so the explorer only emits `file-opened` and
 * stops. ⚠ ONE answer for every way a file gets opened — double-click, Enter,
 * the menu's Open and a document the person has just created. The last one
 * used to open the in-page viewer AND emit, so a new document came up twice on
 * the desktop: once over the explorer and once in its own window (found with
 * #56, 2026-09-26).
 */
export type OpenSurface = 'host' | 'page';

export function openSurface(config: { openInHost?: boolean }, node: { type?: string }): OpenSurface {
  return config.openInHost && node.type === 'file' ? 'host' : 'page';
}
