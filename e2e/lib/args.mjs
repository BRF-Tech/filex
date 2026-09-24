// The options `e2e/run.mjs` understands, in one table, and the check that
// refuses the ones it does not.
//
// ⚠⚠ WHY THIS EXISTS. `run.mjs` read its options with `argv.includes('--x')`
// and never looked at what else was on the command line, so an option it did
// not know was not an error — it was nothing. On 2026-09-24 a release gate was
// run as
//
//     node e2e/run.mjs local --binary … --grep-invert "97-…|99-…|113-…"
//
// to leave out three specs whose app was mid-rebuild. `--grep-invert` was not
// a thing this script knew, so it was dropped on the floor: the three specs
// ran, failed, and the run came back red for exactly the reason the filter was
// there to avoid. A filter that quietly runs everything is how a red gets
// called green — the next such run might be the one that filters out a spec
// and reports the rest as a full pass.
//
// Two answers, both here: `--grep-invert` is now a real option and reaches
// Playwright, and ANY unknown `--option` stops the run with a message naming
// it. The second is the one that matters — it covers the next typo, not just
// this one.

/** Options that take the following argument as their value. */
export const VALUE_OPTIONS = ['binary', 'port', 'grep', 'grep-invert', 'spec', 'browser', 'url'];

/** Options that are on or off. */
export const BOOLEAN_OPTIONS = ['build', 's3', 'keep', 'headed'];

/**
 * The `--options` on this command line that the script does not know.
 *
 * ⚠ The value of a value-taking option is skipped, so `--grep --headed` names
 * no unknown option: `--headed` is that grep's pattern, odd but deliberate.
 * The check is about what is OFFERED, not about what is sensible.
 *
 * @param {string[]} argv the arguments after the profile name
 * @returns {string[]} the unknown options, in the order they appear
 */
export function unknownOptions(argv) {
  const out = [];
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith('--')) continue;
    const name = a.slice(2).split('=')[0];
    if (VALUE_OPTIONS.includes(name)) {
      // `--grep x` consumes x; `--grep=x` carries it already.
      if (!a.includes('=') && i + 1 < argv.length && !argv[i + 1].startsWith('--')) i++;
      continue;
    }
    if (BOOLEAN_OPTIONS.includes(name)) continue;
    out.push(a);
  }
  return out;
}

/** The sentence printed for an unknown option, naming the nearest known one. */
export function unknownOptionMessage(unknown) {
  const known = [...VALUE_OPTIONS, ...BOOLEAN_OPTIONS].sort();
  const lines = unknown.map((u) => {
    const bare = u.slice(2).split('=')[0];
    const near = known.filter((k) => k.startsWith(bare.slice(0, 3)) || bare.startsWith(k.slice(0, 3)));
    return near.length ? `  ${u} is not an option — did you mean ${near.map((k) => `--${k}`).join(' or ')}?` : `  ${u} is not an option`;
  });
  return [
    `[e2e] refusing to run: ${unknown.length} option(s) this script does not know.`,
    ...lines,
    `  known: ${known.map((k) => `--${k}`).join(' ')}`,
    '  An option that is silently ignored turns a filtered run into a full one,',
    '  and a full one into a result nobody asked for. Fix the command line.',
  ].join('\n');
}
