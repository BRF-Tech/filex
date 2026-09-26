/**
 * A random id for the explorer's own bookkeeping: an upload's progress row,
 * a tab. Never a secret, never sent as one.
 *
 * ⚠ Not `crypto.randomUUID()` directly: browsers define it only in a secure
 * context (https, or http on localhost). Opened over plain http at any other
 * address — a LAN IP, a VM, a test box — it is undefined, and every upload
 * threw "crypto.randomUUID is not a function" before a byte was sent: picking
 * a file did nothing at all (found by the sub-path e2e, 2026-09-26).
 * `getRandomValues` exists in every context, so the fallback is still a
 * random v4 UUID.
 */
export function newId(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (typeof c?.randomUUID === 'function') return c.randomUUID();
  const b = new Uint8Array(16);
  if (typeof c?.getRandomValues === 'function') c.getRandomValues(b);
  else for (let i = 0; i < b.length; i++) b[i] = Math.floor(Math.random() * 256);
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
