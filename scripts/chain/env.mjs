// The settings files of scripts/chain: KEY=VALUE lines, read the same way by
// the chain (run.mjs), the nightly run (nightly.mjs) and its morning report
// (report.mjs), so one file can serve all three. Pure.

/** KEY=VALUE lines; `#` comments, an optional `export `, one pair of quotes. No expansion. */
export function parseEnvFile(text) {
  const out = {};
  for (const raw of text.split('\n')) {
    const line = raw.replace(/\r$/, '').trim();
    if (!line || line.startsWith('#')) continue;
    const m = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$/.exec(line);
    if (!m) throw new Error(`not KEY=VALUE: ${line}`);
    let v = m[2];
    if (v.length >= 2 && (v[0] === '"' || v[0] === "'") && v.at(-1) === v[0]) v = v.slice(1, -1);
    out[m[1]] = v;
  }
  return out;
}

/** The settings a run sees: the file's, with the process environment winning. */
export function mergeEnv(fileText, processEnv) {
  return { ...(fileText ? parseEnvFile(fileText) : {}), ...processEnv };
}
