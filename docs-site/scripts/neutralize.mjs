// Release bodies come from GitHub as they were written, and a few old ones
// name the maintainers' own infrastructure: the private forge path, hosts
// under the project's own domain, a server address. The Releases page must
// not carry them further (docs.filex.sh, the committed releases.json).
//
// ⚠ There is ONE list of what is private and how it is written publicly:
// scripts/export-public.sh — its `convert()` rewrite and its `infra_addrs`.
// This module reads that script rather than repeating it; a second list is the
// one that goes stale. The script is never published (private_files), so in a
// public checkout — and on the docs server, which builds from one — there is
// nothing to read and bodies pass unchanged; the export gate and the shop
// window check are what stop them there.
//
// v0.48.1 (2026-09-28): a regenerated releases.json carried the private forge
// path and two hosts of the project domain from the v0.33.0, v0.31.0 and
// v0.14.0 bodies; the public checkout's pre-push gate refused it.

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
export const EXPORT_SCRIPT = path.resolve(here, '../../scripts/export-public.sh')

/** What stands in for one of the project's own server addresses. */
export const ADDRESS_STANDIN = 'a server address'

/**
 * The rewrite rules of scripts/export-public.sh, in the order `convert()`
 * applies them, or null when the script is not there (a public checkout).
 * Throws when the script is there but no longer reads the way this parser
 * expects — a silent empty rule set would publish everything.
 */
export function exportRules(script = EXPORT_SCRIPT) {
  if (!fs.existsSync(script)) return null
  const text = fs.readFileSync(script, 'utf8')
  const fail = (what) => {
    throw new Error(`neutralize: ${path.basename(script)} ${what} — update docs-site/scripts/neutralize.mjs with it`)
  }

  const keepBlock = /KEEP\s*=\s*\{([^}]*)\}/.exec(text)
  if (!keepBlock) fail('has no KEEP = {…} of contact addresses')
  const keep = [...keepBlock[1].matchAll(/'([^']+)'\s*:/g)].map((m) => m[1])

  const conv = /def convert\(s\):\n([\s\S]*?)\n    return s\n/.exec(text)
  if (!conv) fail('has no def convert(s): … return s')
  const steps = []
  for (const m of conv[1].matchAll(/s\.replace\(\s*'([^']*)'\s*,\s*'([^']*)'\s*\)/g)) {
    steps.push({ at: m.index, pairs: [[m[1], m[2]]] })
  }
  for (const m of conv[1].matchAll(/for \w+, \w+ in \(([\s\S]*?)\):\s*\n\s*(?:s\s*=\s*)?s\.replace\(\s*'([^']*)'\s*\+\s*\w+\s*,\s*'([^']*)'\s*\+\s*\w+\s*\)/g)) {
    const pairs = [...m[1].matchAll(/\(\s*'([^']*)'\s*,\s*'([^']*)'\s*\)/g)].map((t) => [m[2] + t[1], m[3] + t[2]])
    steps.push({ at: m.index, pairs })
  }
  steps.sort((a, b) => a.at - b.at)
  const replace = steps.flatMap((s) => s.pairs)
  if (replace.length < 3) fail('convert() yields fewer than three rewrites')

  const infraBlock = /\ninfra_addrs=\(([\s\S]*?)\)/.exec(text)
  if (!infraBlock) fail('has no infra_addrs=( … )')
  const infra = infraBlock[1].split(/\s+/).filter(Boolean)
  if (infra.length === 0) fail('lists no infra_addrs')

  return { keep, replace, infra }
}

const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

/** `text` as the export would publish it; unchanged when there are no rules. */
export function neutralize(text, rules) {
  if (!rules || typeof text !== 'string' || !text) return text
  let s = text
  rules.keep.forEach((real, i) => {
    s = s.split(real).join(`\u0000K${i}\u0000`)
  })
  for (const [from, to] of rules.replace) s = s.split(from).join(to)
  for (const addr of rules.infra) {
    // An entry that ends in a dot is a whole range (`10.0.0.` = any host in it).
    const re = addr.endsWith('.') ? new RegExp(`${escapeRe(addr)}\\d{1,3}`, 'g') : new RegExp(escapeRe(addr), 'g')
    s = s.replace(re, ADDRESS_STANDIN)
  }
  rules.keep.forEach((real, i) => {
    s = s.split(`\u0000K${i}\u0000`).join(real)
  })
  return s
}
