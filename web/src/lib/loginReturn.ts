/** Only the server's exact OAuth continuation may leave the SPA after sign-in. */
export function loginReturnTarget(raw: unknown, base: string): { target: string; browser: boolean } {
  if (typeof raw !== 'string') return { target: '/', browser: false };
  const prefix = `${base}/api/oauth/authorize?request=`;
  if (raw.startsWith(prefix) && /^[a-fA-F0-9]{64}$/.test(raw.slice(prefix.length))) {
    return { target: raw, browser: true };
  }
  // Validate the literal path before a browser or router can normalize it.
  // Backend paths never belong to the SPA; malformed OAuth resumes end at home.
  const path = raw.split(/[?#]/, 1)[0];
  const safe = raw.startsWith('/') && !raw.startsWith('//') &&
    !raw.includes('\\') && !Array.from(raw).some((character) => {
      const code = character.charCodeAt(0);
      return code <= 32 || code === 127;
    }) && !path.includes('%') &&
    !path.split('/').some((segment) => segment === '.' || segment === '..') &&
    !path.split('/').includes('api');
  return { target: safe ? raw : '/', browser: false };
}
