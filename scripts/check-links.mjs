#!/usr/bin/env node
// Every relative markdown link resolves to a file that is actually here.
//
// Why this exists as a script rather than a snippet in the release runbook:
// the runbook's version ran in the SOURCE tree, where every file exists by
// definition. The tree that gets published is a different one -- scripts/
// export-public.sh withholds a list of files -- and nothing has ever checked
// THAT tree. On 2026-09-05 the public README and docs/README.md both linked
// docs/MIGRATION.md, which the export strips: two dead links in the shop
// window, invisible to a check that only ever ran where the file was present.
//
//   node scripts/check-links.mjs [root]     (default: the repo it sits in)
//
// Exit 1 and a list on failure. Anchors are a separate concern and belong to
// scripts/check-doc-anchors.mjs, which needs the built site to answer.

import fs from 'node:fs'
import path from 'node:path'

const root = path.resolve(process.argv[2] ?? path.join(import.meta.dirname, '..'))

/** Markdown that a reader reaches: the shop window plus the docs tree. */
function collect() {
  const out = []
  const add = (rel) => {
    const p = path.join(root, rel)
    if (!fs.existsSync(p)) return
    if (fs.statSync(p).isDirectory()) {
      for (const f of fs.readdirSync(p)) {
        if (f.endsWith('.md')) out.push(path.join(rel, f))
      }
    } else out.push(rel)
  }
  for (const r of ['README.md', 'CHANGELOG.md', 'SECURITY.md', 'docs',
                   'desktop/README.md', 'e2e/README.md', 'deploy/README.md']) add(r)
  for (const d of ['core', 'react', 'webcomponent']) add(`packages/${d}/README.md`)
  return out
}

let bad = 0
let checked = 0
for (const rel of collect()) {
  const text = fs.readFileSync(path.join(root, rel), 'utf8')
  const base = path.dirname(path.join(root, rel))
  // ](target) or ](target#anchor) -- the anchor half is not our business.
  for (const m of text.matchAll(/\]\(([^)\s]+?)(#[^)]*)?\)/g)) {
    const target = m[1]
    if (/^(https?:|mailto:|#|\/)/.test(target)) continue
    checked++
    const abs = path.normalize(path.join(base, decodeURIComponent(target)))
    if (!fs.existsSync(abs)) {
      const line = text.slice(0, m.index).split('\n').length
      console.log(`  ${rel}:${line} -> ${target}`)
      bad++
    }
  }
}

if (bad) {
  console.log(`\ncheck-links: ${bad} of ${checked} relative links point at a file that is not here.`)
  console.log(`Root checked: ${root}`)
  console.log('If this is the public export, the target is probably withheld by')
  console.log('scripts/export-public.sh -- remove the link, do not add the file.')
  process.exit(1)
}
console.log(`check-links: ${checked} relative links across the documented surfaces, all resolve`)
