// The link-time version stamp of the filex CLI the desktop app embeds
// (scripts/fetch-cli.mjs). Same three variables goreleaser sets for the
// released CLI (.goreleaser.yml → builds.ldflags), so `filex --version` in
// the app's resources/bin says which release it came with instead of the
// "0.1.0-dev" an unstamped build reports.

/** The `module` line of a go.mod — the export renames it, so it is read, never typed. */
export function goModulePath(goMod) {
  const m = /^module\s+(\S+)\s*$/m.exec(String(goMod));
  if (!m) throw new Error('go.mod has no module line');
  return m[1];
}

/** `-ldflags` for `go build`: stripped, and stamped with Version, Commit and Date. */
export function cliLdflags({ module, version, commit, date }) {
  if (!version) throw new Error('cliLdflags: no version to stamp');
  const pkg = `${module}/internal/version`;
  return ['-s -w', `-X ${pkg}.Version=${version}`, `-X ${pkg}.Commit=${commit || 'unknown'}`, `-X ${pkg}.Date=${date || 'unknown'}`].join(' ');
}
