/**
 * useViewerFetch — tiny wrapper around `fetch` for the rich viewers.
 *
 * The PreviewModal already builds and pushes `authHeaders` + the
 * configured `credentials` mode into every viewer. The viewers need
 * binary access (ArrayBuffer for utif/ag-psd/pdfjs, Blob for EPUB,
 * text for CSV/Mermaid/ipynb), and they all want to honour the same
 * auth strategy.
 *
 * Each helper uses the URL the modal hands down (`previewUrl(path)`)
 * and merges in the same headers + credentials so a CSRF/Bearer host
 * doesn't reject the request when the viewer fetches the file body
 * directly (instead of letting the browser do it via `<img src>` etc).
 */
export interface ViewerFetchOptions {
  url: string;
  headers?: Record<string, string>;
  credentials?: RequestCredentials;
  signal?: AbortSignal;
}

async function fetchOk(opts: ViewerFetchOptions): Promise<Response> {
  const res = await fetch(opts.url, {
    headers: opts.headers ?? {},
    credentials: opts.credentials ?? 'same-origin',
    signal: opts.signal,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new Error(
      `${res.status} ${res.statusText}${text ? ' — ' + text.slice(0, 200) : ''}`,
    );
  }
  return res;
}

export async function fetchViewerArrayBuffer(
  opts: ViewerFetchOptions,
): Promise<ArrayBuffer> {
  const res = await fetchOk(opts);
  return res.arrayBuffer();
}

export async function fetchViewerBlob(
  opts: ViewerFetchOptions,
): Promise<{ blob: Blob; objectUrl: string; mime: string }> {
  const res = await fetchOk(opts);
  const blob = await res.blob();
  const mime = blob.type || res.headers.get('content-type') || '';
  return { blob, objectUrl: URL.createObjectURL(blob), mime };
}

export async function fetchViewerText(
  opts: ViewerFetchOptions,
): Promise<string> {
  const res = await fetchOk(opts);
  return res.text();
}
