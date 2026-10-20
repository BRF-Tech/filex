/**
 * Where the built-in viewer reads a PDF (GitHub #124).
 *
 * ⚠⚠ filex's own pages frame filex itself only BY PATH (security review
 * UI-11), and Chrome checks a PDF in an `<object>` against the page's
 * frame-src: the manager's preview URL was refused there, and the preview
 * showed its "could not show" fallback instead of the document (measured
 * 2026-10-20 with the policy filex sends). The server answers a PDF's preview
 * on a path of its own, `GET /api/files/pdf/{name}?path=<storage>://<file>`,
 * the one API path its pages may frame (backend handlers/manager_pdf.go).
 *
 * `{name}` is the file's name, for the browser's viewer only (its title and
 * the name it saves under); the file is `?path=`, as on the manager.
 */

/** The PDF path under a server root (`apiBase`). */
export const PDF_PREVIEW_ROUTE = '/api/files/pdf';

/** Whether a path names a PDF, by its extension, as the server decides. */
export function isPdfPath(path: string): boolean {
  return /\.pdf$/i.test(path);
}

/**
 * The preview address of the PDF at `path` (`<storage>://<file>`) on the PDF
 * path `base` (`…/api/files/pdf`).
 */
export function pdfPreviewAddress(base: string, path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1);
  return `${base.replace(/\/+$/, '')}/${encodeURIComponent(name)}?${new URLSearchParams({ path }).toString()}`;
}
