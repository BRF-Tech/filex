/**
 * What a transfer between two places actually means.
 *
 * Three gestures land on the same wire call — drag a row onto a folder, paste
 * after Ctrl+X, paste after Ctrl+C — and they do NOT mean the same thing once
 * the two ends live in different depolar:
 *
 *   • Ctrl+C → paste is a copy, wherever it lands.
 *   • Ctrl+X → paste is a move, wherever it lands. Across depolar the server
 *     streams the bytes over and then deletes the original; before v0.27.0 the
 *     explorer quietly downgraded this to a copy and the user was left with the
 *     file in both places, believing they had moved it.
 *   • Dragging is a move inside one depo and a COPY across two — the rule
 *     Explorer and Finder have taught everyone: a drag between drives copies.
 *
 * Kept as a pure function so both the pane path and the clipboard path ask the
 * same question, and so the answer is testable without a server.
 */

/** `alpha://a/b` → `alpha`; a bare path → `''`. */
export function wireAdapterOf(p: string): string {
  const i = String(p ?? '').indexOf('://');
  return i === -1 ? '' : p.slice(0, i);
}

/** What the caller asked for. `auto` is a drag: let the depolar decide. */
export type TransferIntent = 'auto' | 'copy' | 'move';

export interface TransferPlan {
  /** What to actually ask the server for. */
  kind: 'copy' | 'move';
  /** True when at least one source lives in another depo than the target. */
  cross: boolean;
}

export function resolveTransfer(
  sources: string[],
  targetWire: string,
  intent: TransferIntent = 'auto',
): TransferPlan {
  const target = wireAdapterOf(targetWire);
  // A source with no prefix is a legacy embedder's bare path: it can only mean
  // "the same depo I am looking at", so it never counts as crossing.
  const cross = sources.some((p) => {
    const a = wireAdapterOf(p);
    return a !== '' && target !== '' && a !== target;
  });
  if (intent === 'copy') return { kind: 'copy', cross };
  if (intent === 'move') return { kind: 'move', cross };
  return { kind: cross ? 'copy' : 'move', cross };
}
