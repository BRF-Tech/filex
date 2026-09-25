// "Mount as a drive" — attach a filex server as an OS drive over WebDAV, and
// detach it again, from one button per account or storage.
//
// This is the desktop counterpart of the connection guide's WebDAV tab: the
// guide PRINTS the commands, this RUNS them. The address forms they share come
// from `@brftech/filex-core` (`webdavMount.ts`), imported here by relative path
// the way src/notifications.ts imports the web notification resolver — so the
// address the button attaches and the address the guide teaches cannot drift.
//
// ⚠ This is one of the few genuinely platform-specific corners the rule allows
// on the desktop: each OS has its own WebDAV mounter, and there is no shared
// abstraction over `WNetAddConnection2`, `mount_webdav` and `gio mount`.
//
// ⚠⚠ No `electron` import, so `node --test` can drive it. Everything that
// touches the OS — running a process, reading the token — is INJECTED by
// main.ts (`DriveDeps`). The credential is an argument, never a module global.
//
// ⚠⚠ The token must not leak. It is NEVER put on a command line (a process
// list is world-readable) and NEVER written to the log. On Windows that rules
// out `net use <letter> <url> /user:<u> <token>` — measured 2026-09-25, and it
// is worse than a leak: `net use … *` reading the password from a PIPED stdin
// sends an EMPTY password (Windows reads `*` only from a real console), so the
// mount fails to authenticate. Both are why Windows goes through
// `WNetAddConnection2`, which takes the password as an API argument.

import {
  gioUri,
  isPlainHttp,
  netUseDeleteArgs,
  webdavUnc,
  webdavUrl,
} from '../../packages/core/src/lib/webdavMount.ts';

export type DrivePlatform = 'win32' | 'darwin' | 'linux';

/** What the caller asks to mount. `password` is the account's WebDAV secret —
 *  its API token (accepted in the Basic password field; the account password
 *  would be refused for a TOTP account, the token never is). */
export interface MountRequest {
  platform: DrivePlatform;
  serverUrl: string;
  /** A single storage, or every visible storage when absent. */
  storage?: string;
  user: string;
  password: string;
  /** Windows: a specific drive letter like `Z:`. Absent = pick a free one. */
  letter?: string;
  /** macOS/Linux: where to mount. Absent = a per-OS default under HOME. */
  mountDir?: string;
}

export interface UnmountRequest {
  platform: DrivePlatform;
  /** Windows: the drive letter to detach. */
  letter?: string;
  /** macOS/Linux: the directory to detach. */
  mountDir?: string;
}

/** The result of a mount, in terms the UI can act on without parsing prose. */
export interface MountResult {
  ok: boolean;
  /** Where it landed, for the "Open" button and the "Detach" that follows. */
  letter?: string;
  mountDir?: string;
  /** A machine-readable reason on failure — a key the UI turns into a
   *  sentence, never a locale-specific OS string. */
  problem?: DriveProblem;
  /** The raw OS detail, for the log and a bug report (never a credential). */
  detail?: string;
}

export type DriveProblem =
  | 'no-free-letter' // Windows: A–Z all taken
  | 'webclient-missing' // Windows: no WebClient service (Server core without the redirector)
  | 'webclient-disabled' // Windows: WebClient set to Disabled — needs an admin to enable
  | 'webclient-stopped' // Windows: could not start WebClient
  | 'auth' // the server refused the credential (1244/1790/401)
  | 'tls' // the certificate was not trusted (untrusted or self-signed HTTPS)
  | 'plain-http' // the address is http:// and the client refuses Basic there
  | 'in-use' // the chosen letter / mount point is already taken
  | 'no-tool' // the OS mounter is not installed (gio / mount_webdav)
  | 'failed'; // anything else — `detail` carries the OS message

/** What main.ts injects: the ability to run a helper and to log. Both are
 *  passed so this module holds the rules and none of the runtime. */
export interface DriveDeps {
  /**
   * Runs a helper, feeding `input` (never a credential) on stdin, and resolves
   * with its exit code and captured streams. main.ts wires it to
   * child_process; a test wires a fake.
   */
  exec: (
    file: string,
    args: string[],
    input: string,
  ) => Promise<{ code: number; stdout: string; stderr: string }>;
  /** Does this local path exist? (Windows free-letter search; mount-point check.) */
  exists: (p: string) => boolean;
  /** `tag`, `step`, and a detail object that must never carry a secret. */
  log?: (tag: string, step: string, detail?: unknown) => void;
  /** HOME, for the default mount directory on macOS/Linux. */
  home?: string;
}

// ── drive letters (Windows) ────────────────────────────────────────────────

/** Free drive letters, highest first — Z: is the WebDAV convention, and a high
 *  letter is least likely to collide with a USB stick that appears later. Stops
 *  at D: so C: (and the rare A:/B:) are never handed out. */
export function freeDriveLetters(exists: (p: string) => boolean): string[] {
  const out: string[] = [];
  for (let c = 90 /* Z */; c >= 68 /* D */; c--) {
    const letter = `${String.fromCharCode(c)}:`;
    if (!exists(`${letter}\\`)) out.push(letter);
  }
  return out;
}

/** The letter to mount on: the caller's choice if free, else the first free
 *  one. Null when the caller asked for a taken letter or none is free. */
export function pickDriveLetter(exists: (p: string) => boolean, wanted?: string): string | null {
  if (wanted) return exists(`${wanted}\\`) ? null : wanted;
  return freeDriveLetters(exists)[0] ?? null;
}

// ── Windows: WNetAddConnection2 through PowerShell ──────────────────────────
//
// ⚠ Why PowerShell and not `net use`: see the file header. The script takes the
// address, user and password as a JSON object on STDIN, so the token is never
// an argument. It also starts the WebClient service if it is stopped — the
// service is Manual and usually not running, which without this looks exactly
// like "filex is unreachable" — by firing the service's own ETW start trigger,
// which needs no administrator. It prints one JSON line: {ok, started?, problem?, code?}.
export const WINDOWS_MOUNT_PS = String.raw`
$ErrorActionPreference = 'Stop'
$in = [Console]::In.ReadToEnd() | ConvertFrom-Json
function Reply($o) { [Console]::Out.WriteLine(($o | ConvertTo-Json -Compress)) }
$svc = Get-Service -Name WebClient -ErrorAction SilentlyContinue
if (-not $svc) { Reply @{ ok = $false; problem = 'webclient-missing' }; exit 0 }
if ($svc.Status -ne 'Running') {
  if ($svc.StartType -eq 'Disabled') { Reply @{ ok = $false; problem = 'webclient-disabled' }; exit 0 }
  try {
    $p = New-Object System.Diagnostics.Eventing.EventProvider ([guid]'22b6d684-fa63-4578-87c9-effcbe6643c7')
    $d = New-Object System.Diagnostics.Eventing.EventDescriptor (1, 0, 0, 4, 0, 0, 0)
    [void]$p.WriteEvent([ref]$d, [object[]]@())
    $p.Dispose()
  } catch {}
  $deadline = (Get-Date).AddSeconds(20)
  do { Start-Sleep -Milliseconds 250; $svc.Refresh() } while ($svc.Status -ne 'Running' -and (Get-Date) -lt $deadline)
  if ($svc.Status -ne 'Running') { Reply @{ ok = $false; problem = 'webclient-stopped' }; exit 0 }
}
try {
  (New-Object -ComObject WScript.Network).MapNetworkDrive($in.letter, $in.unc, $in.persistent, $in.user, $in.password)
  Reply @{ ok = $true; started = $true }
} catch {
  $e = $_.Exception; while ($e.InnerException) { $e = $e.InnerException }
  Reply @{ ok = $false; code = ($e.HResult -band 0xFFFF) }
}
`;

/** Windows error numbers this maps to a problem. Everything else is 'failed'
 *  with the number in `detail`. */
export function windowsProblem(code: number): DriveProblem {
  switch (code) {
    case 1244: // ERROR_NOT_AUTHENTICATED
    case 1326: // ERROR_LOGON_FAILURE
    case 5: // ERROR_ACCESS_DENIED
      return 'auth';
    case 1790: // ERROR_NO_NETWORK / trust — WebClient reports it for an untrusted cert
      return 'tls';
    case 85: // ERROR_ALREADY_ASSIGNED
    case 1219: // ERROR_SESSION_CREDENTIAL_CONFLICT
      return 'in-use';
    case 67: // ERROR_BAD_NET_NAME — usually the server is unreachable
      return 'failed';
    default:
      return 'failed';
  }
}

// ── the plan (pure) ─────────────────────────────────────────────────────────

/** The non-secret shape of what a mount will do — the letter or directory it
 *  targets and the warnings the UI should show first. Testable without running
 *  anything. */
export interface MountPlan {
  platform: DrivePlatform;
  url: string;
  unc?: string;
  gio?: string;
  letter?: string;
  mountDir?: string;
  /** Advisory keys, most important first (the UI turns them into sentences). */
  warnings: DriveWarning[];
}

export type DriveWarning = 'plain-http' | 'windows-large-files';

function defaultMountDir(req: MountRequest, home: string): string {
  const leaf = req.storage ? `filex-${req.storage}` : 'filex';
  return req.platform === 'darwin' ? `/Volumes/${leaf}` : `${home}/${leaf}`;
}

export function planMount(req: MountRequest, exists: (p: string) => boolean, home = ''): MountPlan {
  const url = webdavUrl(req.serverUrl, req.storage);
  const warnings: DriveWarning[] = [];
  if (isPlainHttp(url)) warnings.push('plain-http');

  if (req.platform === 'win32') {
    const letter = pickDriveLetter(exists, req.letter) ?? undefined;
    warnings.push('windows-large-files');
    return { platform: 'win32', url, unc: webdavUnc(url), letter, warnings };
  }
  const mountDir = req.mountDir || defaultMountDir(req, home);
  if (req.platform === 'linux') {
    return { platform: 'linux', url, gio: gioUri(url, req.user), mountDir, warnings };
  }
  return { platform: 'darwin', url, mountDir, warnings };
}

// ── mount / unmount (run) ────────────────────────────────────────────────────

export async function mount(req: MountRequest, deps: DriveDeps): Promise<MountResult> {
  const plan = planMount(req, deps.exists, deps.home ?? '');
  const log = deps.log ?? (() => {});
  // The address and target are safe to log; the password is not passed here.
  log('drive', 'mount', { platform: req.platform, url: plan.url, letter: plan.letter, mountDir: plan.mountDir });

  if (req.platform === 'win32') return mountWindows(req, plan, deps, log);
  if (req.platform === 'linux') return mountLinux(req, plan, deps, log);
  return mountMac(req, plan, deps, log);
}

async function mountWindows(
  req: MountRequest,
  plan: MountPlan,
  deps: DriveDeps,
  log: NonNullable<DriveDeps['log']>,
): Promise<MountResult> {
  if (!plan.letter) return fail('no-free-letter', log);
  const payload = JSON.stringify({
    letter: plan.letter,
    unc: plan.unc,
    user: req.user,
    password: req.password,
    persistent: false,
  });
  // ⚠ The SCRIPT goes in as `-EncodedCommand` (base64 UTF-16LE — not a secret),
  // and the JSON payload with the token goes on STDIN, which the script reads.
  // `-Command -` would make PowerShell read the script from stdin and leave the
  // script's own stdin read empty, so it must be the encoded form.
  const encoded = Buffer.from(WINDOWS_MOUNT_PS, 'utf16le').toString('base64');
  const { code, stdout, stderr } = await deps.exec(
    'powershell.exe',
    ['-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', encoded],
    `${payload}\n`,
  );
  // The helper prints one JSON line. A non-zero exit with no JSON is a launch
  // failure (PowerShell missing, policy) — reported as 'failed' with the text.
  const parsed = lastJson(stdout);
  if (!parsed) {
    log('drive', 'mount win: no json', { code, stderr: stderr.slice(0, 200) });
    return fail('failed', log, `powershell exited ${code}`);
  }
  if (parsed.ok) {
    log('drive', 'mounted', { letter: plan.letter });
    return { ok: true, letter: plan.letter };
  }
  if (parsed.problem) return fail(parsed.problem as DriveProblem, log);
  const problem = typeof parsed.code === 'number' ? windowsProblem(parsed.code) : 'failed';
  log('drive', 'mount win failed', { problem, code: parsed.code });
  return { ok: false, problem, detail: parsed.code ? `windows error ${parsed.code}` : undefined };
}

// ⚠ macOS is left as a best-effort that has NOT been measured on a Mac (this
// was built and tested on Windows). `mount_webdav` needs the mount point to
// exist and empty, and takes the password over a pipe rather than an argument.
async function mountMac(
  req: MountRequest,
  plan: MountPlan,
  deps: DriveDeps,
  log: NonNullable<DriveDeps['log']>,
): Promise<MountResult> {
  const dir = plan.mountDir!;
  await deps.exec('/bin/mkdir', ['-p', dir], '');
  // -i makes mount_webdav read the credentials interactively; with no password
  // in the URL it prompts, and we feed username then password on stdin.
  const { code, stdout, stderr } = await deps.exec(
    '/sbin/mount_webdav',
    ['-i', plan.url, dir],
    `${req.user}\n${req.password}\n`,
  );
  if (code === 0) {
    log('drive', 'mounted', { mountDir: dir });
    return { ok: true, mountDir: dir };
  }
  log('drive', 'mount mac failed', { code, stderr: stderr.slice(0, 200), stdout: stdout.slice(0, 200) });
  return { ok: false, problem: 'failed', detail: `mount_webdav exited ${code}` };
}

// ⚠ Linux is likewise UNMEASURED here. `gio mount` reads the password from
// stdin when there is no askpass agent, and the URI carries the username so it
// asks for the password only.
async function mountLinux(
  req: MountRequest,
  plan: MountPlan,
  deps: DriveDeps,
  log: NonNullable<DriveDeps['log']>,
): Promise<MountResult> {
  const { code, stdout, stderr } = await deps.exec('gio', ['mount', plan.gio!], `${req.password}\n`);
  const text = `${stdout}\n${stderr}`;
  if (code === 0) {
    log('drive', 'mounted', { gvfs: true });
    // gio mounts under the GVfs run dir, not a path we chose; the UI opens it
    // through `gio open` rather than a file path.
    return { ok: true, mountDir: plan.mountDir };
  }
  if (/not found|No such file|command not found/i.test(text)) return fail('no-tool', log);
  if (/Permission denied|Authentication|not authorized/i.test(text)) return fail('auth', log);
  log('drive', 'mount linux failed', { code, stderr: stderr.slice(0, 200) });
  return { ok: false, problem: 'failed', detail: `gio exited ${code}` };
}

export async function unmount(req: UnmountRequest, deps: DriveDeps): Promise<MountResult> {
  const log = deps.log ?? (() => {});
  if (req.platform === 'win32') {
    if (!req.letter) return fail('failed', log, 'no letter');
    // ⚠ `net use /delete` carries NO credential, so it is safe as a command
    // line — unlike the mount, which is why the two do not share a mechanism.
    const { code, stderr } = await deps.exec('net.exe', netUseDeleteArgs(req.letter), '');
    log('drive', 'unmount', { letter: req.letter, code });
    return code === 0 ? { ok: true, letter: req.letter } : { ok: false, problem: 'failed', detail: stderr.slice(0, 200) };
  }
  if (req.platform === 'linux') {
    const { code } = await deps.exec('gio', ['mount', '-u', gioUriFromDir()], '');
    return code === 0 ? { ok: true } : { ok: false, problem: 'failed' };
  }
  const { code } = await deps.exec('/sbin/umount', [req.mountDir ?? ''], '');
  return code === 0 ? { ok: true, mountDir: req.mountDir } : { ok: false, problem: 'failed' };
}

// gio unmount is keyed by the mount's own location, which is not a path we
// hold; the desktop UI on Linux is unmeasured, so this is a placeholder the
// caller overrides with the real GVfs location when Linux support is finished.
function gioUriFromDir(): string {
  return '';
}

// ── helpers ─────────────────────────────────────────────────────────────────

function fail(problem: DriveProblem, log: NonNullable<DriveDeps['log']>, detail?: string): MountResult {
  log('drive', 'mount refused', { problem, detail });
  return { ok: false, problem, detail };
}

/** The last JSON object printed on stdout — PowerShell may prepend progress
 *  noise (CLIXML on stderr, banners on stdout), so the reply is the last line
 *  that parses as an object. */
export function lastJson(stdout: string): { ok?: boolean; started?: boolean; problem?: string; code?: number } | null {
  const lines = stdout.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
  for (let i = lines.length - 1; i >= 0; i--) {
    if (!lines[i].startsWith('{')) continue;
    try {
      return JSON.parse(lines[i]);
    } catch {
      /* keep looking */
    }
  }
  return null;
}
