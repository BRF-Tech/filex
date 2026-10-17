// When may store-e2e's activation checks warn instead of fail?
//
// Three of its checks need Windows to ACTIVATE the installed package: a
// filex-e2e:// link handed to it by the shell, the app started inside the
// package and answering over DevTools, and the startup task Windows records on
// first activation. On a developer machine all three pass (15/15, 2026-09-24).
// (Since 0.47.0 a toast shown from inside the package counts as one too: a
// runner's session may have no notification service to take it.)
// On the GitHub-hosted Windows runner of the v0.44.2 release (run 36109704658)
// exactly those three failed while every static and registration check passed
// — and the run had no Store upload to protect (MSSTORE_* unset), so the
// release lost its desktop job for a package nobody was going to submit.
//
// So on CI an activation failure is a warning with diagnostics; on a
// developer machine it stays a failure. A package that is submitted has to
// have been seen working, and since 0.47.0 every release submits one — so the
// local release run checks it strictly first (scripts/release/plan.mjs,
// "desktop: the Store package works as a Store copy"). release.yml used to set
// STORE_E2E_STRICT=1 once the Store secrets existed; that would have failed
// the Store job on every release (the runner never activated a package from
// v0.44.2 to v0.46.1), so it no longer does. The switch remains for a runner
// that can activate one.

import path from 'node:path';

/** @param {Record<string, string | undefined>} env */
export function softActivation(env) {
  return env.CI === 'true' && env.STORE_E2E_STRICT !== '1';
}

/** How much longer to wait for activation on a (slow, cold) CI runner. */
export function activationWaitFactor(env) {
  return env.CI === 'true' ? 3 : 1;
}

// ── the window watch (store-e2e step 7) ─────────────────────────────────
//
// "No window of the Store copy came on screen" guards a PERSON's screen: the
// desktop of whoever cuts the release, where the run used to leave the Store
// copy's sign-in window for its whole length (2026-09-28). A CI runner has
// nobody in front of it, may have no interactive desktop at all, and the watch
// behind the check (a PowerShell that compiles a user32 helper and enumerates
// windows) may not run there. So on CI both window checks only warn, whatever
// the reason — the watch could not run, the session is not interactive, or a
// window did show where no one can see it. On a developer machine, which is
// where the release's pretag runs this, both stay failures.
// STORE_E2E_STRICT does not change this: it is about the package working, and
// a runner's screen says nothing about that.

/** @param {Record<string, string | undefined>} env */
export function softWindowChecks(env) {
  return env.CI === 'true' || env.GITHUB_ACTIONS === 'true';
}

/** One window the watch wrote down, for a check's detail line. */
export function describeWindow(r) {
  return `${r.at} pid ${r.pid} ${r.kind} ${r.cls} ${r.size}${r.cloaked !== '0' ? ' cloaked' : ''} "${r.title}"`;
}

/**
 * The two checks the window watch answers, each with whether it may only warn.
 *
 * @param {{ ran: boolean, error?: string, onScreen: object[], hidden: object[] }} seen
 * @param {Record<string, string | undefined>} env
 */
export function windowVerdicts(seen, env) {
  const soft = softWindowChecks(env);
  const notRun = `the window watch did not run to the end: ${seen.error || 'no word from it'}`;
  // The app's own window, there and hidden. Without it a watch looking at the
  // wrong processes passes the first check for nothing.
  const held = seen.hidden.filter((r) => /filex/i.test(r.title));
  return [
    {
      name: 'no window of the Store copy came on screen',
      ok: seen.ran && seen.onScreen.length === 0,
      soft,
      detail: seen.ran ? seen.onScreen.map(describeWindow).join('; ') : notRun,
    },
    {
      name: '…while its window was there, kept off screen',
      ok: seen.ran && held.length > 0,
      soft,
      detail: seen.ran ? held.map(describeWindow).join('; ') || 'no window of the app was seen at all' : notRun,
    },
  ];
}

// ── who is running the check (task #248) ─────────────────────────────────
//
// An MSIX package's Chromium does not start under the BUILT-IN Administrator
// account (RID 500): with FilterAdministratorToken off - Windows' default for
// that account - it runs with a full administrator token and no UAC at all,
// and packaged apps refuse that (actions/runner-images#9537). GitHub's
// Windows runner is exactly that account (`runneradmin`, renamed), which is
// why no activation check ever passed there from 0.44.2 on, and why the
// failures said nothing useful: the diagnosis printed neither the account nor
// its token. A run under any other account (a second local administrator,
// with UAC, in an interactive session: docs/CONTRIBUTING.md, "The Store
// check without a Windows desktop") is the fix this points to.

/**
 * What `whoami /user /groups /fo csv /nh` and the UAC policy say about the
 * account, in words. `whoami` is the two CSV blocks as printed (the first line
 * the user, then one line per group); `policy` the values of
 * HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System (EnableLUA,
 * FilterAdministratorToken, ConsentPromptBehaviorAdmin; absent = undefined).
 *
 * Returns { sid, user, builtinAdmin, integrity, elevated, lines }: `lines` is
 * what the diagnosis prints, the last one the verdict.
 */
export function accountVerdict(whoami, policy = {}) {
  const rows = String(whoami ?? '')
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean)
    .map((l) => [...l.matchAll(/"([^"]*)"/g)].map((m) => m[1]));
  const userRow = rows.find((r) => r.length === 2 && /^S-1-5-21-/.test(r[1])) ?? rows.find((r) => r.length === 2) ?? [];
  const [user = '?', sid = '?'] = userRow;
  const label = rows.find((r) => /^Mandatory Label\\/i.test(r[0] ?? ''))?.[0] ?? '';
  const integrity = /High/i.test(label) ? 'high' : /System/i.test(label) ? 'system' : /Medium/i.test(label) ? 'medium' : label ? 'low' : 'unknown';
  const builtinAdmin = /^S-1-5-21-.*-500$/.test(sid);
  const lua = policy.EnableLUA;
  const filter = policy.FilterAdministratorToken;
  const elevated = integrity === 'high' || integrity === 'system';
  const lines = [
    `account ${user} ${sid}${builtinAdmin ? ' (RID 500: the built-in Administrator)' : ''}`,
    `token integrity ${integrity}${elevated ? ' (elevated)' : ''}`,
    `UAC: EnableLUA=${lua ?? 'unset'} FilterAdministratorToken=${filter ?? 'unset (0)'} ConsentPromptBehaviorAdmin=${policy.ConsentPromptBehaviorAdmin ?? 'unset'}`,
  ];
  if (builtinAdmin && String(filter ?? '0') !== '1') {
    lines.push('verdict: this is the built-in Administrator with a full token and no UAC - an MSIX app does not start under it (actions/runner-images#9537). Run the check as another account in an interactive session.');
  } else if (String(lua ?? '1') === '0') {
    lines.push('verdict: UAC is off (EnableLUA=0), and packaged apps do not activate without it. Turn it on and sign in again.');
  } else {
    lines.push('verdict: the account and its token allow a packaged app to start; look at the app log and the package status above.');
  }
  return { sid, user, builtinAdmin, integrity, elevated, lines };
}

/**
 * Where the app under test logs, in the order to read them: the Store copy's
 * own folder (`~/.filex/desktop/logs`, src/log.ts: Explorer cannot see the
 * package's redirected AppData, so the Store build logs beside the user's
 * files), then the package's redirected userData, where a build from before
 * that move, or one the channel was not detected for, writes.
 */
export function storeLogDirs({ home, localAppData, pfn, userDataName }) {
  return [
    path.join(home, '.filex', 'desktop', 'logs'),
    path.join(localAppData, 'Packages', pfn, 'LocalCache', 'Roaming', userDataName, 'logs'),
  ];
}
