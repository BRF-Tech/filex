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

/** @param {Record<string, string | undefined>} env */
export function softActivation(env) {
  return env.CI === 'true' && env.STORE_E2E_STRICT !== '1';
}

/** How much longer to wait for activation on a (slow, cold) CI runner. */
export function activationWaitFactor(env) {
  return env.CI === 'true' ? 3 : 1;
}
