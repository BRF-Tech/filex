// An embargoed branch: a fix for a hole in a released version, on
// `sec/<name>` in the maintainers' private repository until the patch release
// that ships it (docs/CONTRIBUTING.md -> "An embargoed fix").
//
// ⚠ No CI sees one before that release (#361, the owner's decision of
// 2026-11-18). The public repository gets it only with the export of that
// patch, and GitHub's temporary private forks of a security advisory run no
// CI. So the merge queue (scripts/train/merge-queue.mjs) tests it where the
// queue runs - the build and vet every merge gets, and the Go tests of the
// packages it changed and of every package that imports one - and GitHub's
// full matrix runs on it at the patch release's gate, before anything is
// tagged. Until 2026-11-18 the private forge's runner ran the whole chain on
// every push of one (task #182), and this file asked the forge about it.

/** A branch that carries an embargoed security fix. */
export const EMBARGO_BRANCH = /^sec\/./;

export const isEmbargoed = (branch) => EMBARGO_BRANCH.test(String(branch ?? ''));
