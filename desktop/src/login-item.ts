// "Start when I sign in" — who has the last word, and what gets written.
//
// The user has the last word, in either of two places: the Settings switch, or
// the OS's own list (Task Manager → Startup apps, Windows Settings → Apps →
// Startup, System Settings → Login Items, the XDG autostart entry). The app
// used to re-register itself on every start whenever its own preference was
// on — and Windows' `setLoginItemSettings` defaults `enabled` to true, which
// also clears the Task Manager "disabled" flag — so a client someone had
// switched off came back at the next sign-in, sync and all.
//
// Now:
//   • only the Settings switch WRITES the login item;
//   • at startup the PREFERENCE follows the OS, never the other way round;
//   • "is it on?" is read from what the OS will actually do.
//
// ⚠ No `electron` import, so node:test can drive it (same boundary as
// src/notifications.ts). main.ts gathers the report and does the writing.

/** What the OS says about the login item — whichever of these the platform
 *  answers. The Windows and macOS fields are Electron's
 *  `app.getLoginItemSettings()`; Linux has no API and is the autostart file. */
export interface LoginItemReport {
  platform: string;
  openAtLogin?: boolean;
  /** Windows: a Run entry for this executable exists AND is not disabled in
   *  Task Manager (StartupApproved). */
  executableWillLaunchAtLogin?: boolean;
  /** Windows: the Run entries for this executable, with that same flag. */
  launchItems?: ReadonlyArray<{ enabled?: boolean }>;
  /** macOS 13+: 'enabled' | 'requires-approval' | 'not-registered' | 'not-found'. */
  status?: string;
  /** Linux: `~/.config/autostart/filex.desktop` exists. */
  autostartFile?: boolean;
}

/**
 * Will the OS start this app at the next sign-in?
 *
 * ⚠ On Windows `openAtLogin` is NOT that answer: it only says a Run value
 * exists with our command. Task Manager's "Disabled" leaves the value and
 * flips a separate flag, so `openAtLogin` stayed true — and Settings reported
 * a login item the OS was never going to honour.
 */
export function osWillLaunch(r: LoginItemReport): boolean {
  if (r.platform === 'linux') return r.autostartFile === true;
  if (r.platform === 'win32') {
    if (typeof r.executableWillLaunchAtLogin === 'boolean') return r.executableWillLaunchAtLogin;
    if (Array.isArray(r.launchItems)) return r.launchItems.some((i) => i.enabled !== false);
  }
  return r.openAtLogin === true;
}

/**
 * The preference to keep after looking at the OS at startup.
 *
 * On, but the OS will not launch us — the entry was disabled or removed out
 * there — means the user switched it off where they were: the preference goes
 * off too, and nothing is written. Off stays off. A run that cannot have a
 * login item at all (`supported` false: from source, not Linux) has nothing
 * to compare against and leaves the preference alone.
 *
 * macOS "requires-approval" is the one "not launching" that is not a refusal:
 * the item is registered and the OS is waiting for the user to allow it.
 */
export function preferenceAfterStartup(pref: boolean, supported: boolean, r: LoginItemReport): boolean {
  if (!pref || !supported) return pref;
  if (r.platform === 'darwin' && r.status === 'requires-approval') return true;
  return osWillLaunch(r);
}

/**
 * The executable the OS is asked to start at sign-in.
 *
 * ⚠ Not always `process.execPath`: under an AppImage that is the extracted
 * temp mount, and under the Windows PORTABLE build it is a fresh extraction
 * folder on every launch — a path that is gone by the next sign-in. With the
 * preference following the OS at startup (preferenceAfterStartup), a portable
 * copy's switch then turned itself off at every start. The image or the
 * portable .exe itself is what has to be run.
 */
export function loginItemExecutable(env: Record<string, string | undefined>, execPath: string): string {
  return env.APPIMAGE || env.PORTABLE_EXECUTABLE_FILE || execPath;
}

/** The exact command the OS runs at sign-in (see loginItemSpec in main.ts). */
export interface LoginItemSpec {
  path: string;
  args: string[];
}

/**
 * What the Settings switch hands `app.setLoginItemSettings`.
 *
 * ⚠ On Windows `enabled` is passed explicitly. It defaults to true, and it
 * rewrites the Task Manager flag: flipping the switch ON is the user asking
 * for exactly that, so it is written on purpose rather than as a side effect.
 * `path` and `args` are Windows-only options and are not sent elsewhere.
 */
export function loginItemWrite(
  on: boolean,
  platform: string,
  spec: LoginItemSpec,
): { openAtLogin: boolean; enabled?: boolean; path?: string; args?: string[] } {
  if (platform === 'win32') return { openAtLogin: on, enabled: on, path: spec.path, args: spec.args };
  return { openAtLogin: on };
}
