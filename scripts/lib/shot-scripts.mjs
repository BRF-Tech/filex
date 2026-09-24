// Which files in e2e/shots/ are shot scripts — found, never listed.
//
// ⚠ A hard-coded list is exactly what the next new script is missing from.
// The rule instead: a file that imports @playwright/test (it drives a browser)
// and that no sibling imports is a script; a file some sibling imports is a
// module (fixtures.mjs, release.mjs). A file that is neither is returned as
// `stray` so the caller can refuse it — silently not running it would be the
// same rot this exists to stop.

import fs from 'node:fs';
import path from 'node:path';

export function findShotScripts(dir) {
  const files = fs.readdirSync(dir).filter((f) => f.endsWith('.mjs')).sort();
  const src = new Map(files.map((f) => [f, fs.readFileSync(path.join(dir, f), 'utf8')]));
  const imported = new Set();
  for (const text of src.values()) {
    for (const m of text.matchAll(/(?:from\s+|import\s*\(\s*)['"]\.\/([^'"]+\.mjs)['"]/g)) imported.add(m[1]);
  }
  const scripts = [];
  const modules = [];
  const stray = [];
  for (const f of files) {
    if (imported.has(f)) modules.push(f);
    else if (/from\s+['"](?:@playwright\/test|playwright)['"]/.test(src.get(f))) scripts.push(f);
    else stray.push(f);
  }
  return { scripts, modules, stray };
}

/**
 * What a shot script needs that this tree does not hold, read off its source:
 * the apps it asks for (`findApp('sign')` — e2e/shots/scene.mjs) and the set
 * folder it writes (`const SET = 'apps'`).
 *
 * ⚠ Read, not declared, for the reason this module exists: a list of "the
 * scripts that need the apps" is exactly what the next such script is missing
 * from. The call that needs an app is the declaration.
 */
export function scriptNeeds(dir, file) {
  const src = fs.readFileSync(path.join(dir, file), 'utf8');
  const apps = [...new Set([...src.matchAll(/\bfindApp\(\s*['"]([\w-]+)['"]\s*\)/g)].map((m) => m[1]))];
  const set = /\bconst\s+SET\s*=\s*['"]([\w-]+)['"]/.exec(src)?.[1] ?? null;
  return { apps, set };
}

/**
 * Why the app scenes are left out of this run, or '' when they are not:
 * `--without-apps` anywhere, or `CI` set (GitHub Actions and GitLab both set
 * it) unless `--with-apps` asks for them back.
 */
export function appScenesLeftOutBy({ withApps = false, withoutApps = false, env = process.env } = {}) {
  if (withoutApps) return '--without-apps';
  const ci = !!env.CI && env.CI !== 'false';
  if (ci && !withApps) return 'CI is set (pass --with-apps to include them)';
  return '';
}

/**
 * Decides, before anything is built, what happens to the scenes that need an
 * app build (see the note at the top of scripts/shots.mjs):
 *
 *   withoutApps  → every such scene is `excluded` (file → why); one without a
 *                  `const SET` is `refused` instead, because its pictures
 *                  could not be told apart from leftovers;
 *   otherwise    → every app build that is missing is `refused`, with where it
 *                  was looked for.
 *
 * `needs` maps a script to scriptNeeds(); `locate` is app-locations.mjs's
 * locateApp. Pure, so the rule is testable without a build.
 */
export function planAppScenes({ needs, withoutApps, locate }) {
  const excluded = new Map();
  const refused = [];
  for (const [file, n] of needs) {
    if (!n.apps.length) continue;
    if (withoutApps) {
      if (!n.set) {
        refused.push(`e2e/shots/${file} needs ${n.apps.join(' + ')} and would be left out, but names no \`const SET\` — its pictures could not be told apart from leftovers. Give it one.`);
        continue;
      }
      excluded.set(file, `needs the ${n.apps.join(' + ')} app build${n.apps.length > 1 ? 's' : ''}`);
      continue;
    }
    for (const app of n.apps) {
      const at = locate(app);
      if (!at.present) refused.push(`e2e/shots/${file} needs the ${app} app: ${at.how}.`);
    }
  }
  return { excluded, refused };
}
