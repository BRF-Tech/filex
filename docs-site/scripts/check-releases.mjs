#!/usr/bin/env node
/**
 * check-releases.mjs — the offline gate that replaced "regenerate on every build".
 *
 * `npm run build` used to be `npm run releases && vitepress build`, so every
 * person who ran the mandatory pre-release build gate came away with two
 * modified files — docs/RELEASES.md and docs-site/data/releases.json — that had
 * nothing to do with their change. Three agents hit that in one day, each
 * reverted it by hand, and one release nearly swept the churn into an unrelated
 * commit. A gate that dirties the tree it is gating is a trap.
 *
 * So the build no longer generates anything. It runs this instead: a check that
 * the generated page is present and plausible, using no network and writing
 * nothing. Refreshing is now an explicit act — `npm run releases` — performed at
 * the release step that is meant to perform it.
 *
 * What it will NOT do: fetch, write, or fail because the page is a few days
 * behind. A build that needs GitHub to be reachable is a worse trap than the
 * one this removes.
 */

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const siteDir = path.resolve(__dirname, '..')
const PAGE = path.resolve(siteDir, '../docs/RELEASES.md')
const CACHE = path.join(siteDir, 'data', 'releases.json')

function die(lines) {
  const bar = '='.repeat(74)
  process.stderr.write(`\n${bar}\n${lines.join('\n')}\n${bar}\n\n`)
  process.exit(1)
}

const HINT = [
  '',
  '  Both files are tracked. Restore them with:',
  '      git checkout -- docs/RELEASES.md docs-site/data/releases.json',
  '  or regenerate them from the GitHub releases (needs network):',
  '      cd docs-site && npm run releases'
]

if (!fs.existsSync(PAGE)) {
  die([`RELEASES: ${path.relative(process.cwd(), PAGE)} is missing.`, ...HINT])
}

const page = fs.readFileSync(PAGE, 'utf8')
const listed = (page.match(/^## v[0-9]/gm) || []).length
if (listed === 0) {
  die([
    `RELEASES: ${path.relative(process.cwd(), PAGE)} lists no releases.`,
    '  Publishing an empty Releases page is worse than failing this build.',
    ...HINT
  ])
}

if (!fs.existsSync(CACHE)) {
  die([`RELEASES: ${path.relative(process.cwd(), CACHE)} is missing.`, ...HINT])
}
let cached
try {
  cached = JSON.parse(fs.readFileSync(CACHE, 'utf8'))
} catch (err) {
  die([`RELEASES: ${path.relative(process.cwd(), CACHE)} is not valid JSON.`, `  ${err.message}`, ...HINT])
}
if (!Array.isArray(cached.releases) || cached.releases.length === 0) {
  die([`RELEASES: ${path.relative(process.cwd(), CACHE)} holds no releases.`, ...HINT])
}

console.log(
  `[releases] page ok — ${listed} releases detailed, cache holds ${cached.releases.length}` +
    (cached.generatedAt ? `, last refreshed ${cached.generatedAt}` : '') +
    ' (run `npm run releases` to refresh)'
)
