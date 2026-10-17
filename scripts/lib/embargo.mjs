// An embargoed branch and its chain (task #182).
//
// A fix for a hole in a released version lives on `sec/<name>` in the
// maintainers' private repository and passes the whole test chain there, on
// the build host's runner, before it reaches main (docs/CONTRIBUTING.md ->
// "The private pipeline"). A push to such a branch starts that pipeline - unless
// the push says `-o ci.skip`, and then the forge records a pipeline as
// "skipped" and runs nothing, without a word to anybody. That happened on
// 2026-10-10: an embargoed branch was pushed with the option, its pipeline was
// "skipped", and it reached main that afternoon without the chain having run
// on it once.
//
// Two places now ask about it, and both ask through this file, so they cannot
// disagree about what "the chain ran" means:
//   - the merge queue (scripts/train/merge-queue.mjs) refuses an embargoed
//     branch whose tip has no green chain, unless it is told why not
//     (--embargo-unchecked, which goes into the merge commit as a trailer);
//   - the build host's watcher (the private pipeline's runner directory)
//     starts the chain on an embargoed branch whose tip has none.
//
// ⚠ This file is published and names no forge, host or project: where the
// pipelines are is a setting (EMBARGO_API, the project's API root, and
// EMBARGO_TOKEN_FILE, a file holding a token that may read it - and, for the
// watcher, start one).

import fs from 'node:fs';

/** A branch that carries an embargoed security fix. */
export const EMBARGO_BRANCH = /^sec\/./;

export const isEmbargoed = (branch) => EMBARGO_BRANCH.test(String(branch ?? ''));

/** A pipeline the forge made and then did not run: `-o ci.skip`, `[ci skip]`. */
const SKIPPED = new Set(['skipped']);
/** A pipeline still on its way. */
const UNDER_WAY = new Set(['created', 'waiting_for_resource', 'preparing', 'pending', 'running', 'scheduled', 'manual']);

/**
 * Did the chain pass on this commit? `pipelines` is the forge's list for the
 * commit (newest first, as it answers). { ok, why } - `why` is a sentence for
 * the person who has to act on it.
 */
export function chainVerdict(pipelines) {
  const all = Array.isArray(pipelines) ? pipelines : [];
  const ran = all.filter((p) => !SKIPPED.has(p?.status));
  if (ran.some((p) => p.status === 'success')) return { ok: true, why: 'its chain passed' };
  if (all.length === 0) return { ok: false, why: 'no pipeline was made for its tip' };
  if (ran.length === 0) return { ok: false, why: 'its pipeline was skipped (a push with -o ci.skip): the chain never ran' };
  const going = ran.find((p) => UNDER_WAY.has(p.status));
  if (going) return { ok: false, why: `its chain is still ${going.status}` };
  return { ok: false, why: `its chain ended ${ran[0].status}` };
}

/**
 * Must the chain be started on this commit? Yes when nothing ever ran on it:
 * no pipeline at all, or only skipped ones. A chain that ran and is red is
 * not started again - red is not silent, it is waiting for a fix.
 */
export function needsAChain(pipelines) {
  const all = Array.isArray(pipelines) ? pipelines : [];
  return !all.some((p) => !SKIPPED.has(p?.status));
}

/** { api, tokenFile } from the settings, or null when either is missing. */
export function embargoSettings(env = {}) {
  const api = String(env.EMBARGO_API ?? '').trim().replace(/\/+$/, '');
  const tokenFile = String(env.EMBARGO_TOKEN_FILE ?? '').trim();
  return api && tokenFile ? { api, tokenFile } : null;
}

/** The token, from its file (one line). Never logged, never in an argument. */
export function readToken(file) {
  return fs.readFileSync(file, 'utf8').trim();
}

/** GET/POST against the project's API with the token in a header. */
async function call(api, token, method, rel, fetchImpl, body) {
  const res = await fetchImpl(`${api}${rel}`, {
    method,
    headers: { 'PRIVATE-TOKEN': token, ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) throw new Error(`${method} ${rel}: ${res.status}`);
  return res.json();
}

/** The pipelines the forge made for `sha` (on `ref`, when given), newest first. */
export function pipelinesFor({ api, token, sha, ref = '', fetchImpl = globalThis.fetch }) {
  const q = new URLSearchParams({ sha, per_page: '50', order_by: 'id', sort: 'desc' });
  if (ref) q.set('ref', ref);
  return call(api, token, 'GET', `/pipelines?${q}`, fetchImpl);
}

/** The project's embargoed branches: [{ name, sha, at }] - `at` is the tip's commit time (ms, NaN when not said). */
export async function embargoedBranches({ api, token, fetchImpl = globalThis.fetch }) {
  const list = await call(api, token, 'GET', `/repository/branches?${new URLSearchParams({ search: '^sec/', per_page: '100' })}`, fetchImpl);
  return (Array.isArray(list) ? list : [])
    .filter((b) => isEmbargoed(b?.name) && b?.commit?.id)
    .map((b) => ({ name: b.name, sha: b.commit.id, at: Date.parse(b.commit.committed_date ?? '') }));
}

/** Starts the chain on `ref`, the whole of it. The forge's answer. */
export function startChain({ api, token, ref, fetchImpl = globalThis.fetch }) {
  return call(api, token, 'POST', `/pipeline?${new URLSearchParams({ ref })}`, fetchImpl, {
    variables: [{ key: 'CHAIN_PROFILE', value: 'full' }],
  });
}
