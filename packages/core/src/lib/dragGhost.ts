/**
 * dragGhost — wiring:c4 custom HTML5 drag image.
 *
 * The browser default drag image (a translucent snapshot of the row/card)
 * reads poorly for multi-selection drags. This builds a small pill with the
 * dragged item's name and, for multi-drags, a count badge, parks it
 * off-screen (setDragImage requires a rendered element), and removes it on
 * the next tick. Purely visual — the dataTransfer payload is untouched.
 *
 * Styled by `.fe-dragghost` in base.css via the `--fe-*` tokens published
 * at :root (and re-published at `.dark`/html level for dark hosts), so it
 * matches the active theme even though it's appended to <body>.
 */
export function applyDragGhost(ev: DragEvent, name: string, count: number): void {
  const dt = ev.dataTransfer;
  if (!dt || typeof document === 'undefined' || typeof dt.setDragImage !== 'function') return;
  try {
    const el = document.createElement('div');
    el.className = 'fe-dragghost';
    const label = document.createElement('span');
    label.className = 'fe-dragghost__name';
    label.textContent = name;
    el.appendChild(label);
    if (count > 1) {
      const badge = document.createElement('span');
      badge.className = 'fe-dragghost__badge';
      badge.textContent = String(count);
      el.appendChild(badge);
    }
    el.style.position = 'fixed';
    el.style.top = '-1000px';
    el.style.left = '-1000px';
    document.body.appendChild(el);
    dt.setDragImage(el, 14, 14);
    // The engine snapshots the element synchronously during dragstart —
    // safe to drop it right after.
    setTimeout(() => el.remove(), 0);
  } catch {
    /* non-critical visual nicety — native ghost remains */
  }
}
