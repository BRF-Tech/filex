// KEY=VALUE settings files: what the maintainers' own tools read beside the
// environment - the build host's test chain (scripts/chain) and the release
// train (scripts/train: the merge queue, the ship, the wake-up wrapper).
//
// ⚠ Why a file and not the repository: these settings name machines, paths on
// them and where a key lives. None of that belongs in a repository that is
// published (the export refuses a private host or name, scripts/export-public.sh),
// and none of it is the same on two maintainers' machines. The repository
// carries an example with placeholders (scripts/chain/chain.env.example,
// scripts/train/train.env.example); the real file lives in ~/.config.
//
// ⚠ A secret is never a value here: a setting names the FILE a key is in (or,
// for a token the session already holds, the variable it is in), and the tool
// reads it when it needs it. A value in a settings file ends up in a backup,
// a paste or a screenshot.
//
// ⚠ The parser is scripts/chain/env.mjs's, re-exported: that file is copied
// on its own to the build host by scripts/chain/install-nightly.sh, so it
// cannot import from here - this imports from it instead, and there is one
// parser.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { mergeEnv, parseEnvFile } from '../chain/env.mjs';

export { mergeEnv, parseEnvFile };

/**
 * The settings a tool runs with: a file under the process environment, which
 * wins. The file is `file` when given, else the one `$<envVar>` names, else
 * ~/.config/<fallback> when it exists, else none.
 *
 * Returns { env, file }: `file` is '' when no file was read.
 */
export function loadSettings({ file = '', envVar = '', fallback = '', env = process.env, home = os.homedir() } = {}) {
  const fb = fallback ? path.join(home, '.config', fallback) : '';
  const chosen = file || (envVar && env[envVar]) || (fb && fs.existsSync(fb) ? fb : '');
  if (chosen && !fs.existsSync(chosen)) throw new Error(`settings file ${chosen} does not exist`);
  return { env: mergeEnv(chosen ? fs.readFileSync(chosen, 'utf8') : '', env), file: chosen };
}

/** A setting split on commas and whitespace, empty parts dropped. */
export function listSetting(value) {
  return String(value ?? '')
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}
