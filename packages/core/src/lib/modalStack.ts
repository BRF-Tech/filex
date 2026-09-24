/**
 * modalStack — which explorer dialog is on top.
 *
 * Every open `modals/Modal.vue` registers here, and only the LAST one
 * answers the keyboard (Escape, the Tab trap). Two dialogs open at once is
 * ordinary — a confirmation over a plugin's screen, the destination picker
 * over a move — and Escape must close the one in front, not both.
 *
 * ⚠ Module state on purpose: `<script setup>` top-level declarations are
 * per INSTANCE, so a stack declared inside the component would hold one
 * entry per dialog and every dialog would think it was on top.
 */
const stack: symbol[] = [];

export function pushModal(id: symbol): void {
  stack.push(id);
}

export function popModal(id: symbol): void {
  const i = stack.lastIndexOf(id);
  if (i >= 0) stack.splice(i, 1);
}

export function isTopModal(id: symbol): boolean {
  return stack[stack.length - 1] === id;
}
