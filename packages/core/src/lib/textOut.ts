/**
 * Handing a piece of text to the person: onto the clipboard, or as a file to
 * save. ONE home for both, so a new panel does not grow its own copy.
 *
 * ⚠ copyText falls back to a hidden textarea and `execCommand('copy')`:
 * `navigator.clipboard` needs a secure context, and an embed served over plain
 * http is a real deployment. It reports whether the text got there.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand('copy');
      document.body.removeChild(ta);
      return ok;
    } catch {
      return false;
    }
  }
}

/**
 * Save text as a file named `name`: a download of a Blob the browser holds,
 * never a round trip to the server (the text is often a credential that the
 * server keeps no copy of - an uploader's settings carry its key).
 */
export function saveTextFile(name: string, text: string, mime = 'application/json'): void {
  const url = URL.createObjectURL(new Blob([text], { type: `${mime};charset=utf-8` }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}
