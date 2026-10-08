// The public pages' sentences, as the SERVER answers them.
//
// ⚠ The pages a stranger opens (`/s/<token>`, `/d/<token>`) have no words of
// their own since 0.54: they read `GET /api/public/strings?lang=` - the server
// catalogue's `server.public.*` (backend/internal/srvtext/locales/<lang>.json,
// prefix taken off), the same table its no-JavaScript pages render. A test
// that mounts one of them answers that request from the catalogue FILE, so it
// asserts the server's real words and goes red the day a sentence the page
// shows leaves the catalogue.
import { readFileSync } from 'node:fs';
import path from 'node:path';

const LOCALES = path.resolve(__dirname, '../../../backend/internal/srvtext/locales');
const PREFIX = 'server.public.';

/** `server.public.*` of the built-in `lang` catalogue, prefix taken off. */
export function publicTable(lang = 'en'): Record<string, string> {
  const all = JSON.parse(readFileSync(path.join(LOCALES, `${lang}.json`), 'utf8')) as Record<string, string>;
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(all)) {
    if (k.startsWith(PREFIX)) out[k.slice(PREFIX.length)] = v;
  }
  return out;
}

/** The body of `GET /api/public/strings` for `lang`. */
export function publicStringsAnswer(lang = 'en') {
  return { lang, dir: 'ltr', strings: publicTable(lang) };
}

/** Is this the public-strings request? */
export function isPublicStrings(url: string): boolean {
  return url.split('?')[0].endsWith('/api/public/strings');
}

/** `{name}` placeholders filled, as the page fills them. */
export function fillPublic(tpl: string, vars: Record<string, string | number>): string {
  return Object.entries(vars).reduce((acc, [k, v]) => acc.replaceAll(`{${k}}`, String(v)), tpl);
}
