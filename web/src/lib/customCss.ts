/**
 * gorunum:v1 — the operator's own stylesheet.
 *
 * Besides the shipped theme gallery an operator can paste CSS on the admin
 * Settings page (`ui.custom_css`). It arrives on the `/api/branding` boot
 * payload and lands here as the text of ONE `<style data-filex-custom>`
 * element in `<head>`: replaced when the setting changes, removed when it is
 * cleared, never a second element.
 *
 * ⚠ It is CSS, not markup. The value is assigned to `textContent`, which an
 * HTML parser never re-reads, so nothing in it can become an element. It never
 * goes near `v-html` or `innerHTML`, and the server refuses the one string
 * (`</style`) that would matter if it ever reached a server-rendered page.
 *
 * ⚠ The element must be the LAST stylesheet in `<head>`. A theme override is
 * the same specificity as the declaration it overrides — `.fe { --fe-primary }`
 * against `.fe--theme-dark { --fe-primary }` on the same element — so ties are
 * broken by source order and "last" is the whole mechanism. Appending once at
 * boot is not enough: both Vite dev and the production build inject a lazily
 * loaded route's CSS into `<head>` when that route is first opened, which is
 * long after boot (the explorer's own `style.css` is exactly such a chunk). So
 * we watch `<head>` and move back to the end whenever a stylesheet is added
 * after us.
 */
import { BrandingApi } from '@/api/branding';

const MARKER = 'data-filex-custom';

let el: HTMLStyleElement | null = null;
let observer: MutationObserver | null = null;

/** Put (or replace) the operator stylesheet. An empty value removes it. */
export function applyCustomCss(css: string | null | undefined): void {
  const text = (css ?? '').trim();
  if (!text) {
    removeCustomCss();
    return;
  }
  if (!el) {
    el = document.createElement('style');
    el.setAttribute(MARKER, '');
  }
  el.textContent = text;
  moveToEnd();
  watchHead();
}

/** Drop the operator stylesheet (the setting was cleared). */
export function removeCustomCss(): void {
  observer?.disconnect();
  observer = null;
  el?.remove();
  el = null;
}

/** Fetch the boot payload and wear whatever it carries. Best-effort. */
export async function loadCustomCss(): Promise<void> {
  try {
    const branding = await BrandingApi.boot();
    applyCustomCss(branding?.custom_css);
  } catch {
    /* The endpoint is public but optional — an unstyled panel is the right
       failure, and it is the same one an installation with no custom CSS
       already sees. */
  }
}

function moveToEnd(): void {
  if (el && document.head.lastElementChild !== el) document.head.appendChild(el);
}

function watchHead(): void {
  if (observer || typeof MutationObserver === 'undefined') return;
  observer = new MutationObserver((records) => {
    for (const record of records) {
      for (const node of Array.from(record.addedNodes)) {
        // Our own re-append shows up here too; skipping it is what keeps this
        // from looping forever.
        if (node === el || !(node instanceof Element)) continue;
        const tag = node.tagName;
        if (tag === 'STYLE' || (tag === 'LINK' && node.getAttribute('rel') === 'stylesheet')) {
          moveToEnd();
          return;
        }
      }
    }
  });
  observer.observe(document.head, { childList: true });
}
