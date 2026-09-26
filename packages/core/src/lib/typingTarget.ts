/**
 * Is this key event aimed at a place a person is TYPING? Then it is text, not
 * a shortcut: `I` is a letter, not "toggle the info panel".
 *
 * ⚠ Not only form controls. Monaco 0.55 types through the browser's
 * EditContext API in Chromium, so the focused element of the code editor is a
 * `<div class="native-edit-context" role="textbox">` — no <textarea>, not
 * contenteditable. A guard that knew only INPUT/TEXTAREA/SELECT/contenteditable
 * let the explorer's single-letter shortcuts eat keystrokes out of the
 * document: "MIT License" typed into a new LICENSE arrived as "MLcene"
 * (I = info panel, S = star, Space = quick look; measured while building #56).
 * `role="textbox"` is the ARIA contract every custom text surface carries, and
 * anything inside a Monaco editor is the editor's.
 *
 * One helper for every keyboard guard (the explorer's shortcuts, quick look's
 * arrows), because the two copies it replaces had the same hole.
 */
export function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el || typeof el.tagName !== 'string') return false;
  if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable) {
    return true;
  }
  if (el.getAttribute?.('role') === 'textbox') return true;
  return typeof el.closest === 'function' && !!el.closest('.monaco-editor');
}
