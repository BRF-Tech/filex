/**
 * tema:v1 — the operator's own stylesheet, and the two ways it is kept away
 * from the controls that turn it off.
 *
 * The sheet arrives from `GET /api/me/custom-css` — authenticated, so an
 * anonymous visitor and the login page never receive it at all — already
 * sanitised and already wrapped in its `@scope (:root) to (.fe-css-immune)`
 * rule by the server. This module only puts it in the document.
 *
 * ⚠ It is CSS, not markup. The value is assigned to `textContent`, which an
 * HTML parser never re-reads, so nothing in it can become an element. It never
 * goes near `v-html` or `innerHTML`.
 *
 * ⚠ The element must be the LAST stylesheet in `<head>`. A token override is
 * the same specificity as the declaration it overrides — `.fe { --fe-primary }`
 * against `.fe--theme-dark { --fe-primary }` on the same element — so ties are
 * broken by source order and "last" is the whole mechanism. Appending once at
 * boot is not enough: both Vite dev and the production build inject a lazily
 * loaded route's CSS into `<head>` when that route is first opened, long after
 * boot (the explorer's own `style.css` is exactly such a chunk). So we watch
 * `<head>` and move back to the end whenever a stylesheet is added after us.
 *
 * ⚠⚠ SUSPENSION — THE GUARANTEE THAT NOBODY CAN LOCK THEMSELVES OUT.
 * `suspendCustomCss()` takes the element OUT of the document; the admin
 * Appearance route calls it on enter and `resumeCustomCss()` on leave. The
 * `@scope` wrapper already stops an operator selector MATCHING inside the
 * immune panel, but scoping cannot un-apply an inherited property or an
 * ancestor-level one: a sheet that writes `:root { display: none }` or
 * `:root { opacity: 0 }` blanks everything below it, immune subtree included,
 * because those are not questions about which elements a rule matches. Nothing
 * a stylesheet can express survives the stylesheet not being in the document —
 * so the screen that edits and removes the sheet is the screen where the sheet
 * is not loaded. The two guards cover different failures and both are needed.
 */
import { AppearanceApi } from '@/api/appearance';

const MARKER = 'data-filex-custom';

let el: HTMLStyleElement | null = null;
let observer: MutationObserver | null = null;
/** The text we would be serving if we were not suspended. */
let current = '';
let suspended = false;

/** Put (or replace) the operator stylesheet. An empty value removes it. */
export function applyCustomCss(css: string | null | undefined): void {
  current = (css ?? '').trim();
  render();
}

/** Drop the operator stylesheet (the setting was cleared, or switched off). */
export function removeCustomCss(): void {
  current = '';
  render();
}

/**
 * Take the sheet out of the document until `resumeCustomCss` is called.
 *
 * Idempotent, and it does NOT forget the sheet: leaving the Appearance screen
 * has to put back exactly what was there, without another round trip.
 */
export function suspendCustomCss(): void {
  suspended = true;
  render();
}

/** Put the sheet back after a suspension. */
export function resumeCustomCss(): void {
  suspended = false;
  render();
}

/** Whether the sheet is currently held out of the document. */
export function isCustomCssSuspended(): boolean {
  return suspended;
}

/**
 * Fetch the sheet and wear it. Best-effort, and only worth calling once a
 * session exists — the endpoint is authenticated.
 */
export async function loadCustomCss(): Promise<void> {
  try {
    const payload = await AppearanceApi.customCss();
    applyCustomCss(payload?.css);
  } catch {
    /* The endpoint needs a session and the sheet is optional — an unstyled
       panel is the right failure, and it is the same one an installation with
       no custom CSS already sees. */
  }
}

/* ------------------------------------------------------------------ */

function render(): void {
  const wanted = suspended ? '' : current;
  if (!wanted) {
    observer?.disconnect();
    observer = null;
    el?.remove();
    el = null;
    return;
  }
  if (!el) {
    el = document.createElement('style');
    el.setAttribute(MARKER, '');
  }
  el.textContent = wanted;
  moveToEnd();
  watchHead();
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
