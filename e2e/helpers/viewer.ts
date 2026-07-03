import type { Page } from '@playwright/test';
import { expect } from '@playwright/test';

/**
 * Per-extension viewer mount assertion.
 *
 * Maps an extension to the DOM selector(s) PreviewModal's lazy viewer
 * map mounts. Falls back to an alternate selector for kinds with a
 * known degradation path (pdf → native <object> when pdfjs fails;
 * office → "OnlyOffice yapılandırması yok" fallback when capability
 * gate is off). Each entry is intentionally explicit so the audit
 * report names the exact contract that broke.
 *
 * The selectors mirror PreviewModal.vue's `<template v-else-if=…>`
 * branches plus the per-viewer SFC roots in
 * `packages/core/src/viewers/*Viewer.vue`.
 */
export interface ExtMatch {
  /** Primary selector that proves the right viewer is mounted. */
  primary: string;
  /** Optional accepted fallback (capability-gated kinds). */
  fallback?: string;
  /** How long to wait for `primary` before checking fallback. */
  timeoutMs?: number;
}

export const VIEWER_MATRIX: Record<string, ExtMatch> = {
  // Images — native <img>.
  jpg: { primary: '.fe-preview__image' },
  jpeg: { primary: '.fe-preview__image' },
  png: { primary: '.fe-preview__image' },
  webp: { primary: '.fe-preview__image' },
  gif: { primary: '.fe-preview__image' },
  bmp: { primary: '.fe-preview__image' },
  avif: { primary: '.fe-preview__image' },
  svg: { primary: '.fe-preview__image' },

  // Media.
  mp4: { primary: '.fe-preview__video' },
  webm: { primary: '.fe-preview__video' },
  mov: { primary: '.fe-preview__video' },
  mp3: { primary: '.fe-preview__audio' },
  wav: { primary: '.fe-preview__audio' },
  ogg: { primary: '.fe-preview__audio' },

  // PDF — native browser <object> renderer (PdfViewer SFC removed from
  // the default map in v0.1.7). Wraps Chrome/Firefox's built-in pdf
  // engine; no custom toolbar.
  pdf: { primary: 'object[type="application/pdf"]', fallback: '.fe-preview__fallback' },

  // Markdown — split editor in edit mode (the audit always uses edit).
  md: { primary: '.fe-preview__md-split-input' },

  // Code — Monaco container OR highlight.js <pre> fallback.
  json: { primary: '.fe-preview__code-editor, .fe-preview__pre' },
  yaml: { primary: '.fe-preview__code-editor, .fe-preview__pre' },
  xml: { primary: '.fe-preview__code-editor, .fe-preview__pre' },
  html: { primary: '.fe-preview__code-editor, .fe-preview__pre' },
  js: { primary: '.fe-preview__code-editor, .fe-preview__pre' },
  py: { primary: '.fe-preview__code-editor, .fe-preview__pre' },
  go: { primary: '.fe-preview__code-editor, .fe-preview__pre' },

  // Office — OnlyOffice spawns `iframe[name^="frameEditor"]` inside the
  // `.fe-preview__office` mount via api.js. When capability is off (or
  // documentServerUrl probe fails) PreviewModal swaps in
  // `.fe-preview__fallback` with an "OnlyOffice yapılandırması yok"
  // message + İndir button — accepted as a documented degradation.
  docx: { primary: '.fe-preview__office iframe', fallback: '.fe-preview__fallback', timeoutMs: 20_000 },
  xlsx: { primary: '.fe-preview__office iframe', fallback: '.fe-preview__fallback', timeoutMs: 20_000 },
  pptx: { primary: '.fe-preview__office iframe', fallback: '.fe-preview__fallback', timeoutMs: 20_000 },
  odt:  { primary: '.fe-preview__office iframe', fallback: '.fe-preview__fallback', timeoutMs: 20_000 },
  ods:  { primary: '.fe-preview__office iframe', fallback: '.fe-preview__fallback', timeoutMs: 20_000 },
  odp:  { primary: '.fe-preview__office iframe', fallback: '.fe-preview__fallback', timeoutMs: 20_000 },

  // Drawio — diagrams.net iframe; operator-disabled fallback shows the
  // viewer chrome but no <iframe> child.
  drawio: { primary: '.filex-viewer-drawio__frame, iframe[src*="diagrams.net"]', fallback: '.filex-viewer-drawio' },
  dio:    { primary: '.filex-viewer-drawio__frame, iframe[src*="diagrams.net"]', fallback: '.filex-viewer-drawio' },

  // Mermaid — rendered SVG inside `.filex-viewer-mermaid`.
  mmd:     { primary: '.filex-viewer-mermaid svg', fallback: '.filex-viewer-mermaid' },
  mermaid: { primary: '.filex-viewer-mermaid svg', fallback: '.filex-viewer-mermaid' },

  // Epub — epubjs rendition mount.
  epub: { primary: '.filex-viewer-epub__rendition, .filex-viewer-epub iframe', fallback: '.filex-viewer-epub' },

  // Ipynb — custom JSON walker; the cells container appears as soon as
  // the JSON parses.
  ipynb: { primary: '.filex-viewer-ipynb__cell, .filex-viewer-ipynb__pane', fallback: '.filex-viewer-ipynb' },

  // 3D — model-viewer web component inside `.filex-viewer-3d`. OBJ/STL
  // get an "unsupported format" message (viewer mounts but no canvas).
  glb:  { primary: '.filex-viewer-3d model-viewer', fallback: '.filex-viewer-3d' },
  gltf: { primary: '.filex-viewer-3d model-viewer', fallback: '.filex-viewer-3d' },
  obj:  { primary: '.filex-viewer-3d', fallback: '.fe-preview__fallback' },
  stl:  { primary: '.filex-viewer-3d', fallback: '.fe-preview__fallback' },

  // Tiff — UTIF lazy renders to an <img>. Sub-element check is too
  // strict (slow image decode), so accept the wrapper too.
  tiff: { primary: '.filex-viewer-tiff__pane img, .filex-viewer-tiff canvas', fallback: '.filex-viewer-tiff' },
  tif:  { primary: '.filex-viewer-tiff__pane img, .filex-viewer-tiff canvas', fallback: '.filex-viewer-tiff' },

  // Psd — ag-psd renders to a <canvas> inside the pane.
  psd: { primary: '.filex-viewer-psd__pane canvas, .filex-viewer-psd__pane img', fallback: '.filex-viewer-psd' },

  // Csv/tsv — custom table renderer.
  csv: { primary: '.filex-viewer-csv__table', fallback: '.filex-viewer-csv' },
  tsv: { primary: '.filex-viewer-csv__table', fallback: '.filex-viewer-csv' },

  // Archives — ArchiveViewer (v0.1.7+) renders the member list via
  // /api/files/archive/list. Empty archives still mount the viewer
  // shell; corrupt archives drop into the fallback path.
  zip: { primary: '.filex-viewer-archive', fallback: '.fe-preview__fallback' },
};

/**
 * Asserts the right viewer rendered for the given extension. Times
 * out per `match.timeoutMs ?? 10s` — generous enough to let lazy
 * chunks (epubjs / mermaid / model-viewer) finish their dynamic
 * import on a cold cache.
 */
export async function expectViewerForExt(page: Page, ext: string): Promise<void> {
  const match = VIEWER_MATRIX[ext];
  if (!match) {
    throw new Error(`No viewer matrix entry for ext=${ext}`);
  }
  const timeout = match.timeoutMs ?? 10_000;

  // Primary first.
  try {
    await expect(page.locator(match.primary).first()).toBeVisible({ timeout });
    return;
  } catch (primaryErr) {
    if (!match.fallback) throw primaryErr;
  }
  // Fallback path — capability gate off / lazy peer missing / etc.
  await expect(page.locator(match.fallback!).first()).toBeVisible({
    timeout: 2_000,
  });
}

/**
 * Captures console errors + viewer-asset 404s for the lifetime of the
 * caller's scope. Returns a teardown that returns the collected lists.
 *
 * Filters:
 *   - Console: severity=error only. Skips known-noisy WebGL/CSS
 *     warnings that don't indicate a viewer regression.
 *   - Requests: only `/admin/assets/*.js` chunk failures (the lazy
 *     viewer chunks live here) AND the OnlyOffice `api.js` script.
 */
export interface ViewerErrorSink {
  pageErrors: string[];
  consoleErrors: string[];
  failedAssets: string[];
}

export function instrumentPage(page: Page): { collect: () => ViewerErrorSink; dispose: () => void } {
  const sink: ViewerErrorSink = { pageErrors: [], consoleErrors: [], failedAssets: [] };

  const onPageError = (err: Error) => {
    sink.pageErrors.push(err.message);
  };
  const onConsole = (msg: import('@playwright/test').ConsoleMessage) => {
    if (msg.type() !== 'error') return;
    const text = msg.text();
    // Drop benign noise.
    if (/Failed to load resource:.+favicon/i.test(text)) return;
    if (/WebGL|WEBGL_/i.test(text)) return;
    sink.consoleErrors.push(text);
  };
  const onRequestFailed = (req: import('@playwright/test').Request) => {
    const url = req.url();
    if (!/\/admin\/assets\/.*\.js|api\.js$/.test(url)) return;
    sink.failedAssets.push(`${req.failure()?.errorText ?? 'failed'} ${url}`);
  };

  page.on('pageerror', onPageError);
  page.on('console', onConsole);
  page.on('requestfailed', onRequestFailed);

  return {
    collect: () => sink,
    dispose: () => {
      page.off('pageerror', onPageError);
      page.off('console', onConsole);
      page.off('requestfailed', onRequestFailed);
    },
  };
}
