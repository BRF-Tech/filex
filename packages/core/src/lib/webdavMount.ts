/**
 * webdavMount — the forms of a filex WebDAV address, and the operating
 * systems' own commands that attach one as a drive.
 *
 * Two readers, one source:
 *  - the connection guide (`connectionGuides.ts`) PRINTS these for a person to
 *    copy;
 *  - the desktop app's "Mount as a drive" (`desktop/src/drive.ts`) RUNS them.
 * Written twice, the command the guide teaches and the one the button runs
 * would drift apart, and the person who copied the guide's version would hit
 * a failure the button never shows — or the other way round.
 *
 * ⚠ No imports, and none may be added. The desktop main process and its
 * `node --test` runner import this file by relative path (the way
 * `web/src/lib/notificationTarget.ts` is imported), and plain node does not
 * resolve an extensionless specifier.
 */

/** True when the deployment is not on TLS — several clients refuse that,
 *  and Windows refuses it silently, which is worse. */
export function isPlainHttp(origin: string): boolean {
  return /^http:\/\//i.test(origin);
}

/**
 * `https://fm.example.com` (+ `docs`) → `https://fm.example.com/dav/docs/`.
 *
 * The storage name is NOT percent-encoded: this is the form a person reads and
 * types, and the form `net use` and Explorer's "Map network drive" take — both
 * encode it on the wire themselves. Consumers that need a URI (GVfs, NetFS)
 * go through `new URL()`, which encodes it for them.
 */
export function webdavUrl(origin: string, storage?: string): string {
  const base = origin.replace(/\/+$/, '');
  return storage ? `${base}/dav/${storage}/` : `${base}/dav/`;
}

/**
 * The GVfs form (`gio mount`, GNOME Files, Dolphin): `https:` → `davs:`,
 * `http:` → `dav:`. With `user`, the account goes in the URI — which is what
 * makes `gio mount` ask for the PASSWORD only, so the one line piped to it
 * lands in the right prompt (gvfs adds the username prompt only when the URI
 * carries none).
 */
export function gioUri(url: string, user?: string): string {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return url.replace(/^https:/i, 'davs:').replace(/^http:/i, 'dav:');
  }
  const scheme = u.protocol === 'http:' ? 'dav' : 'davs';
  const auth = user ? `${encodeURIComponent(user)}@` : '';
  return `${scheme}://${auth}${u.host}${u.pathname}`;
}

/**
 * The UNC path Windows' WebDAV redirector gives the same address:
 * `https://fm.example.com:8443/dav/My files/` → `\\fm.example.com@SSL@8443\dav\My files`.
 *
 * It is what `net use` lists for a mapped drive and what WNetAddConnection2
 * takes, so it is also how a mapping is recognised as ours.
 */
export function webdavUnc(url: string): string {
  const u = new URL(url);
  const ssl = u.protocol === 'https:';
  let host = u.hostname.replace(/^\[|\]$/g, '');
  if (ssl) host += '@SSL';
  if (u.port) host += `@${u.port}`;
  const segments = u.pathname
    .split('/')
    .filter(Boolean)
    .map((s) => decodeURIComponent(s));
  return `\\\\${host}${segments.length ? '\\' + segments.join('\\') : ''}`;
}

export interface NetUseOptions {
  /** `Z:` */
  letter: string;
  url: string;
  user: string;
  /** What goes in the password slot. The guide prints a placeholder there;
   *  nothing that runs this should ever put a real secret in an argv. */
  password: string;
  persistent: boolean;
}

/** `net use` arguments (without `net`), in the order `net` wants them. */
export function netUseArgs(o: NetUseOptions): string[] {
  return ['use', o.letter, o.url, `/user:${o.user}`, o.password, `/persistent:${o.persistent ? 'yes' : 'no'}`];
}

/** The same, as a line to paste into cmd.exe — the address quoted, since a
 *  storage name may carry a space. */
export function netUseCommand(o: NetUseOptions): string {
  const [verb, letter, url, ...rest] = netUseArgs(o);
  return ['net', verb, letter, `"${url}"`, ...rest].join(' ');
}

/** Detaching a mapped drive: no credential involved, safe to run as is. */
export function netUseDeleteArgs(letter: string): string[] {
  return ['use', letter, '/delete', '/y'];
}

/**
 * Windows' WebDAV client has two built-in limits that look exactly like filex
 * bugs: transfers stop at ~47.7 MB (`FileSizeLimitInBytes`, default
 * 50,000,000) and folders of roughly a thousand entries fail to open
 * (`FileAttributesLimitInBytes`, default 1,000,000). These are the values the
 * guide recommends, and the registry lines that set them (an administrator
 * runs them; the WebClient service must restart after).
 */
export const WEBCLIENT_KEY = 'HKLM\\SYSTEM\\CurrentControlSet\\Services\\WebClient\\Parameters';
export const WEBCLIENT_FILE_SIZE_RECOMMENDED = 4294967295;
export const WEBCLIENT_ATTRIBUTES_RECOMMENDED = 20000000;
export const WEBCLIENT_LIMIT_COMMANDS: string[] = [
  `reg add "${WEBCLIENT_KEY}" /v FileSizeLimitInBytes /t REG_DWORD /d ${WEBCLIENT_FILE_SIZE_RECOMMENDED} /f`,
  `reg add "${WEBCLIENT_KEY}" /v FileAttributesLimitInBytes /t REG_DWORD /d ${WEBCLIENT_ATTRIBUTES_RECOMMENDED} /f`,
  'net stop webclient && net start webclient',
];
