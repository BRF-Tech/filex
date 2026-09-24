/**
 * Putting a string on the clipboard, once, for the whole panel.
 *
 * `navigator.clipboard` is unavailable on a page served over plain http from
 * anything but localhost — which is how a good number of self-hosted installs
 * are reached — so the old `document.execCommand` route stays as the fallback.
 * Callers get a boolean instead of an exception: every one of them has a
 * better thing to do with a failure (a toast in the person's language) than
 * rethrow it.
 */
export async function copyText(value: string): Promise<boolean> {
  if (typeof navigator !== 'undefined' && navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(value);
      return true;
    } catch {
      /* fall through to the legacy route */
    }
  }
  return legacyCopy(value);
}

/** The pre-Clipboard-API route: a hidden field, selected, copied, removed. */
function legacyCopy(value: string): boolean {
  if (typeof document === 'undefined') return false;
  const field = document.createElement('textarea');
  field.value = value;
  field.setAttribute('readonly', '');
  field.style.position = 'fixed';
  field.style.opacity = '0';
  document.body.appendChild(field);
  field.select();
  let ok = false;
  try {
    ok = document.execCommand('copy');
  } catch {
    ok = false;
  }
  field.remove();
  return ok;
}
