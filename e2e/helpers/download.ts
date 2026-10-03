/**
 * The next file the browser is HANDED, whichever way it arrives.
 *
 * One file is a `window.open` of its own body (core `downloadFile`), a
 * selection or a folder is a hidden frame on this page (lib/downloadSelection),
 * a public link's button navigates the page itself. Playwright reports a
 * download on the page that started it, on the new tab, or on this page.
 *
 * ⚠⚠ Playwright's WebKit does not download a `text/plain` attachment that a
 * page or a new tab NAVIGATES to: it SHOWS it, and no `download` event fires
 * anywhere. Measured 2026-10-01 in the Playwright image: the same response
 * downloads from an `<a download>` and from an iframe, and an
 * `application/octet-stream` body downloads from the same popup; only a
 * displayable type, navigated to, is shown. What a spec measures is the
 * server's answer (where the address points, the name it gives, the bytes),
 * so on WebKit a page or tab that finishes loading is asked again, with this
 * context's own session, and an attachment answer is read.
 *
 * ⚠ Reading the navigation RESPONSE instead (context `response`) was tried
 * first: it caught the answer on a bare test page and did not resolve for the
 * explorer's tab in the 0.50 targeted run (the cause was not isolated).
 * Asking the address again does not depend on how the load is reported.
 * It does mean a single-use address (a /z/ ticket) cannot be read this way;
 * those go through a hidden frame, which downloads in every engine.
 */
import fs from 'node:fs';
import type { Download, Page } from '@playwright/test';

export interface HandedFile {
  /** The address the browser fetched the file from. */
  url: string;
  /** The name it would be saved under. */
  filename: string;
  /** The bytes. */
  body(): Promise<Buffer>;
}

function fromDownload(d: Download): HandedFile {
  return {
    url: d.url(),
    filename: d.suggestedFilename(),
    body: async () => fs.readFileSync((await d.path())!),
  };
}

/** `filename*=UTF-8''…` first (RFC 6266), then the plain `filename=`. */
export function filenameFromDisposition(cd: string): string {
  const star = /filename\*\s*=\s*utf-8''([^;]+)/i.exec(cd);
  if (star) return decodeURIComponent(star[1].trim());
  const plain = /filename\s*=\s*"([^"]*)"|filename\s*=\s*([^;]+)/i.exec(cd);
  return plain ? (plain[1] ?? plain[2] ?? '').trim() : '';
}

/** Start waiting BEFORE the click that hands the file over. */
export function nextHandedFile(page: Page): Promise<HandedFile> {
  const context = page.context();
  const webkit = context.browser()?.browserType().name() === 'webkit';
  return new Promise((resolve) => {
    let settled = false;
    const undo: Array<() => void> = [];
    const done = (f: HandedFile) => {
      if (settled) return;
      settled = true;
      for (const u of undo) u();
      resolve(f);
    };
    const onDownload = (d: Download) => done(fromDownload(d));
    /** WebKit: a page that SHOWED an attachment is asked for it again. */
    const shownOn = (p: Page, already: boolean) => {
      const onLoad = async () => {
        const url = p.url();
        if (settled || !/^https?:/.test(url)) return;
        const res = await page.request.get(url).catch(() => null);
        const cd = res?.headers()['content-disposition'] ?? '';
        if (!res?.ok() || !/^\s*attachment/i.test(cd)) return;
        const bytes = await res.body();
        done({ url, filename: filenameFromDisposition(cd), body: async () => bytes });
      };
      p.on('load', onLoad);
      undo.push(() => p.off('load', onLoad));
      // A new tab may have loaded before Playwright handed it over.
      if (already) void p.waitForLoadState('load').then(onLoad, () => undefined);
    };
    const onPage = (p: Page) => {
      p.once('download', onDownload);
      if (webkit) shownOn(p, true);
    };
    page.on('download', onDownload);
    context.on('page', onPage);
    undo.push(() => page.off('download', onDownload), () => context.off('page', onPage));
    if (webkit) shownOn(page, false);
  });
}
