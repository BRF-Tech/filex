#!/usr/bin/env node
/**
 * fetch-releases.mjs — build-time generator for the docs.filex.sh "Releases" page.
 *
 * Reads the public GitHub Releases API for BRF-Tech/filex and writes two files:
 *
 *   docs-site/data/releases.json   the normalised release list (COMMITTED — it is
 *                                  the fallback when the API cannot be reached)
 *   docs/RELEASES.md               the generated VitePress page (COMMITTED, so a
 *                                  reviewer can see exactly what gets published)
 *
 * Why generated and not fetch-in-the-browser: the page stays static, is indexed
 * by the site's local search, survives a rate-limited GitHub, and reads fine with
 * JavaScript disabled.
 *
 * ⚠ This script is NOT part of `npm run build` any more. The build is a
 * mandatory release gate that everybody runs, and a gate that rewrites two
 * tracked files on every run hands every contributor a diff that is not theirs
 * — three of them reverted it by hand on 2026-09-06 and one release nearly
 * committed the churn. `npm run build` now runs the offline
 * scripts/check-releases.mjs instead, and refreshing is this script, run
 * deliberately at the release step that refreshes it.
 *
 * It is also idempotent, which is the second half of the same promise: when the
 * fetched release list is byte-identical to the committed cache, `generatedAt`
 * is kept as it was and NEITHER file is rewritten. Running it out of curiosity
 * therefore cannot dirty the tree either — only an actual new release does.
 *
 * Honest degradation — the two failure modes are deliberately different:
 *
 *   API unreachable / rate-limited AND a cache exists → keep the cached data,
 *     print a loud banner, exit 0. The build succeeds and publishes the last
 *     known-good list.
 *   API unreachable AND no cache at all               → exit 1. Publishing an
 *     empty "Releases" page is worse than failing the build, so this is the one
 *     case that stops it. Cannot happen once data/releases.json is committed.
 *   API answers 200 with an EMPTY list while the cache has releases → treated
 *     exactly like "unreachable": keep the cache, loud banner, exit 0. GitHub
 *     did this twice on 2026-08-17 (17:25 and 18:25) and the generator wrote a
 *     Releases page with nothing on it — only a broken build kept it off the
 *     site. A published repository does not lose all of its releases at once;
 *     an empty answer is an outage, not news.
 *
 * Nothing here is invented. Bullet text comes from the commit subjects GitHub
 * puts in the release body; the optional one-paragraph summaries come from
 * data/release-highlights.json, which is hand-written from the repository's own
 * CHANGELOG.md.
 *
 * The GitHub repository is public, so no token is used and none belongs here.
 * Unauthenticated the API allows 60 requests/hour/IP; this makes one.
 *
 * Usage:
 *   cd docs-site && npm run releases           fetch, then regenerate if changed
 *   FILEX_RELEASES_OFFLINE=1 npm run releases  skip the fetch (exercises the
 *                                              cache path — used to verify that
 *                                              a dead API still renders)
 */

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const siteDir = path.resolve(__dirname, '..')
const docsDir = path.resolve(siteDir, '../docs')

const REPO = 'BRF-Tech/filex'
// FILEX_RELEASES_API exists so the "GitHub is down" branch can be exercised on
// purpose (point it somewhere unroutable) and so an air-gapped build can read a
// mirror. It is not a secret and nothing is authenticated.
const API = process.env.FILEX_RELEASES_API || `https://api.github.com/repos/${REPO}/releases`
const CACHE = path.join(siteDir, 'data', 'releases.json')
const HIGHLIGHTS = path.join(siteDir, 'data', 'release-highlights.json')
const PAGE = path.join(docsDir, 'RELEASES.md')

/** How many releases get a full section; the rest go in the compact table. */
const DETAILED = 20
const TIMEOUT_MS = 20000
const MAX_PAGES = 3

// ---------------------------------------------------------------------------
// fetch
// ---------------------------------------------------------------------------

async function fetchReleases() {
  const all = []
  for (let page = 1; page <= MAX_PAGES; page++) {
    const res = await fetch(`${API}?per_page=100&page=${page}`, {
      headers: {
        accept: 'application/vnd.github+json',
        'user-agent': 'filex-docs-site',
        'x-github-api-version': '2022-11-28'
      },
      signal: AbortSignal.timeout(TIMEOUT_MS)
    })
    if (!res.ok) {
      const remaining = res.headers.get('x-ratelimit-remaining')
      throw new Error(
        `GitHub answered ${res.status} ${res.statusText}` +
          (remaining !== null ? ` (rate-limit remaining: ${remaining})` : '')
      )
    }
    const batch = await res.json()
    if (!Array.isArray(batch)) {
      throw new Error('GitHub answered something that is not a release array')
    }
    all.push(...batch)
    if (batch.length < 100) break
  }
  return all
}

// ---------------------------------------------------------------------------
// normalise
// ---------------------------------------------------------------------------

const MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December'
]

/** Deterministic, locale-independent — the output is committed, so it must not
 *  change shape because a machine has different ICU data. */
function humanDate(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`
}

/** feat/fix/… → the heading a reader understands. */
const GROUP_OF_TYPE = {
  feat: 'New',
  fix: 'Fixed',
  perf: 'New'
}
const GROUP_ORDER = ['New', 'Fixed', 'Other changes']

/** Scopes whose readable form is not just "capitalise the first letter". */
const SCOPE_LABEL = {
  ai: 'AI',
  api: 'API',
  av: 'Antivirus',
  cli: 'CLI',
  ci: 'CI',
  dav: 'WebDAV',
  db: 'Database',
  e2e: 'End-to-end encryption',
  i18n: 'Translations',
  ldap: 'LDAP',
  mcp: 'MCP',
  oidc: 'OIDC',
  onlyoffice: 'OnlyOffice',
  pwa: 'PWA',
  rbac: 'RBAC',
  s3: 'S3',
  sftp: 'SFTP',
  sso: 'SSO',
  ui: 'UI',
  webdav: 'WebDAV'
}

function scopeLabel(scope) {
  if (!scope) return null
  const key = scope.toLowerCase()
  if (SCOPE_LABEL[key]) return SCOPE_LABEL[key]
  return scope.charAt(0).toUpperCase() + scope.slice(1)
}

/** Sentence-case a commit subject without mangling identifiers: `v0.4.2 …`,
 *  `` `filex sync` … `` and `/dav …` are left exactly as the author wrote them. */
function sentence(text) {
  let s = text.trim()
  if (!s) return s
  if (!/^(`|\/|\$|v\d|[A-Z0-9])/.test(s)) {
    s = s.charAt(0).toUpperCase() + s.slice(1)
  }
  if (!/[.!?)\]`]$/.test(s)) s += '.'
  return s
}

const CONVENTIONAL =
  /^(feat|fix|perf|refactor|docs|test|build|ci|chore|style|revert)(?:\(([^)]+)\))?(!?):\s*(.+)$/

/**
 * The goreleaser body carries a `## Changelog` section of
 * `* <40-char sha> <conventional commit subject>` lines under `### <group>`
 * headings. Turn that into readable, grouped bullets — the SHA is noise to a
 * reader and the conventional prefix is a label, not prose.
 */
function parseChangelog(body) {
  const groups = new Map()
  let headline = null
  const lines = body.split(/\r?\n/)
  let inChangelog = false
  let found = false

  for (const raw of lines) {
    const line = raw.trim()
    if (/^##\s+Changelog\s*$/i.test(line)) {
      inChangelog = true
      found = true
      continue
    }
    if (!inChangelog) continue
    if (/^---\s*$/.test(line)) break
    if (/^##\s/.test(line) && !/^###/.test(line)) break

    const bullet = line.match(/^[*-]\s+(?:([0-9a-f]{7,40})\s+)?(.*)$/)
    if (!bullet) continue
    const subject = bullet[2].trim()
    if (!subject) continue

    const m = subject.match(CONVENTIONAL)
    const type = m ? m[1] : null
    const scope = m ? m[2] : null
    const text = m ? m[4] : subject

    // `chore(release): v0.9.0 — olivov multi-tenant isolation, strict-S3 …`
    // is the release's own one-line summary. Promote it to the headline and
    // drop the bullet: the version is already the heading.
    // The release's OWN commit, in either shape this project has used:
    // `chore(release): v0.9.0 - ...` in the source repo, and a bare
    // `release: v0.31.0` in the public export. Neither is a change somebody
    // made in the release; the version is already the heading it would sit
    // under. The bare form is not a conventional type, so it fell through to
    // "Other changes" and every release since v0.27.6 published a section
    // whose single item was its own version number.
    const bare = subject.match(/^release:\s*(v?\d+\.\d+\.\d+.*)$/i)
    if (bare || (type === 'chore' && scope === 'release')) {
      const raw2 = bare ? bare[1] : text
      const tail = raw2.replace(/^v?\d+\.\d+\.\d+\s*[—–-]?\s*/, '').trim()
      if (tail && !/^v?\d+\.\d+\.\d+$/.test(tail)) headline = sentence(tail)
      continue
    }

    const group = GROUP_OF_TYPE[type] || 'Other changes'
    if (!groups.has(group)) groups.set(group, [])
    groups.get(group).push({ scope: scopeLabel(scope), text: sentence(text) })
  }

  const ordered = []
  for (const name of GROUP_ORDER) {
    if (groups.has(name)) ordered.push({ title: name, items: groups.get(name) })
  }
  return { groups: ordered, headline, found }
}

/**
 * Releases published by hand carry ordinary prose instead of a `## Changelog`
 * block. Keep the prose, drop the boilerplate around it (the title, the
 * "download a binary below" pitch, the docker-pull block, the verify footer).
 */
function parseProse(body) {
  const out = []
  let skippingFence = false
  for (const raw of body.split(/\r?\n/)) {
    const line = raw.replace(/\s+$/, '')
    if (/^```/.test(line)) {
      skippingFence = !skippingFence
      continue
    }
    if (skippingFence) continue
    if (/^#{1,6}\s*filex\s+v?\d/i.test(line)) continue
    if (/^Self-hosted file manager\b/i.test(line)) continue
    if (/^Download a binary below/i.test(line)) continue
    if (/^\*\*Verify:\*\*/i.test(line)) continue
    if (/^(Issues|Docs):\s/i.test(line)) continue
    if (/^---\s*$/.test(line)) continue
    out.push(line)
  }
  return out.join('\n').replace(/\n{3,}/g, '\n\n').trim()
}

function parseImages(body) {
  const images = []
  for (const m of body.matchAll(/docker pull\s+(\S+)/g)) {
    if (!images.includes(m[1])) images.push(m[1])
  }
  return images
}

function normalise(raw) {
  const body = typeof raw.body === 'string' ? raw.body : ''
  const { groups, headline, found } = parseChangelog(body)
  // Prose only for releases published by hand, which carry no `## Changelog`
  // section at all. A generated body whose every commit was filtered out (a
  // lone `chore(release)`) must NOT fall through here — it would republish the
  // raw changelog headings the parser just consumed.
  const prose = found ? '' : parseProse(body)
  const assets = (raw.assets || []).map((a) => a.name)

  return {
    tag: raw.tag_name,
    date: raw.published_at || raw.created_at || '',
    url: raw.html_url,
    prerelease: !!raw.prerelease,
    headline,
    groups,
    prose,
    images: parseImages(body),
    hasDesktop: assets.some((n) => /^filex-desktop-|^filex-\d+\.\d+\.\d+-(x64|amd64|x86_64)/.test(n)),
    assetCount: assets.length
  }
}

// ---------------------------------------------------------------------------
// render
// ---------------------------------------------------------------------------

/**
 * Markdown is rendered by Vue: a bare `<` starts a tag and `{{` interpolates.
 * Inside a code span neither is true and entities are NOT decoded, so escaping
 * there would publish a literal `&lt;` — hence the split.
 */
function esc(text) {
  return String(text)
    .split(/(`[^`]*`)/)
    .map((part, i) =>
      i % 2 === 1 ? part : part.replace(/</g, '&lt;').replace(/\{\{/g, '&#123;&#123;')
    )
    .join('')
}

/**
 * Trim a hand-written summary down to a table cell: whole sentences until it
 * carries something (a lone "Cleanup release." says nothing), then stop.
 */
function condense(text, min = 90, max = 230) {
  // Split only on whitespace that follows sentence punctuation, so `versions.keep_n`
  // is not mistaken for a sentence end — and so nothing is dropped on the floor.
  const parts = text.split(/(?<=[.!?])\s+/)
  const taken = []
  for (const part of parts) {
    taken.push(part)
    if (taken.join(' ').length >= min) break
  }
  let out = taken.join(' ').trim()
  if (out.length > max) {
    out = out.slice(0, max).replace(/\s+\S*$/, '')
    // Never end inside a code span: a stray backtick would swallow the rest of the row.
    if ((out.match(/`/g) || []).length % 2 === 1) {
      out = out.slice(0, out.lastIndexOf('`')).replace(/\s+\S*$/, '')
    }
    out += '…'
  }
  return out
}

/** One line for the compact table: the hand-written summary if there is one. */
function firstLine(rel, highlight) {
  if (highlight) return condense(highlight)
  if (rel.headline) return rel.headline
  for (const g of rel.groups) {
    if (g.items.length) {
      const it = g.items[0]
      return it.scope ? `${it.scope} — ${it.text}` : it.text
    }
  }
  if (rel.prose) {
    const p = rel.prose.split('\n').find((l) => l.trim() && !/^[#>*\-|]/.test(l.trim()))
    if (p) return p.trim()
  }
  return '—'
}

function renderRelease(rel, highlight) {
  const out = []
  out.push(`## ${rel.tag}`)
  out.push('')
  out.push(
    `<span class="filex-release-date">${humanDate(rel.date)}</span>` +
      (rel.prerelease ? ' · **pre-release**' : '')
  )
  out.push('')

  if (highlight) {
    out.push(esc(highlight))
    out.push('')
  } else if (rel.headline) {
    out.push(`**${esc(rel.headline)}**`)
    out.push('')
  }

  if (rel.prose) {
    out.push(esc(rel.prose))
    out.push('')
  }

  for (const group of rel.groups) {
    out.push(`**${group.title}**`)
    out.push('')
    for (const item of group.items) {
      out.push(item.scope ? `- **${esc(item.scope)}** — ${esc(item.text)}` : `- ${esc(item.text)}`)
    }
    out.push('')
  }

  const links = [`[Downloads and checksums](${rel.url})`]
  if (rel.hasDesktop) links.push('desktop packages included')
  if (rel.images.length) links.push('`' + rel.images[0] + '`')
  out.push(links.join(' · '))
  out.push('')

  return out.join('\n')
}

function renderPage(releases, highlights, meta) {
  const latest = releases[0]
  const out = []

  out.push('---')
  out.push('title: Releases')
  out.push(
    'description: Every filex release with a plain-English summary of what changed — generated from the GitHub releases at release time.'
  )
  out.push('---')
  out.push('')
  out.push('<!-- GENERATED FILE — do not edit by hand. Your edits will be overwritten.')
  out.push(`     Source:      GitHub Releases for ${REPO}`)
  out.push('     Generator:   docs-site/scripts/fetch-releases.mjs')
  out.push('     Summaries:   docs-site/data/release-highlights.json (hand-written)')
  out.push('     Regenerate:  cd docs-site && npm run releases -->')
  out.push('')
  out.push('# Releases')
  out.push('')
  out.push(
    'Every published filex release, newest first. This page is generated from the'
  )
  out.push(
    `[GitHub releases](https://github.com/${REPO}/releases) by \`npm run releases\`, which is`
  )
  out.push(
    'run once when a release is cut — not by the site build, which would rewrite this'
  )
  out.push('file on every contributor who ran it.')
  out.push('')
  out.push(
    'Whether filex installs a release by itself depends on which part of the version moved —'
  )
  out.push('see [Updates](./UPDATES.md).')
  out.push('')

  if (latest) {
    out.push(`::: tip Latest — ${latest.tag}, ${humanDate(latest.date)}`)
    const lead = highlights[latest.tag] || latest.headline || firstLine(latest, null)
    out.push(esc(lead))
    out.push(':::')
    out.push('')
    if (latest.images.length) {
      out.push('```bash')
      for (const img of latest.images) out.push(`docker pull ${img}`)
      out.push('```')
      out.push('')
    }
  }

  const detailed = releases.slice(0, DETAILED)
  const rest = releases.slice(DETAILED)

  for (const rel of detailed) {
    out.push(renderRelease(rel, highlights[rel.tag]))
  }

  if (rest.length) {
    out.push('## Earlier releases')
    out.push('')
    out.push(
      `The ${rest.length} releases before ${detailed[detailed.length - 1].tag}, in brief. Full notes are on GitHub.`
    )
    out.push('')
    out.push('| Version | Date | What changed |')
    out.push('|---|---|---|')
    for (const rel of rest) {
      out.push(
        `| [${rel.tag}](${rel.url}) | ${humanDate(rel.date)} | ` +
          `${esc(firstLine(rel, highlights[rel.tag])).replace(/\|/g, '\\|')} |`
      )
    }
    out.push('')
  }

  out.push('---')
  out.push('')
  // No "GitHub was unreachable" marker here, deliberately. The unreachable path
  // renders from the committed cache, which by definition produces the page
  // that is already published — adding a banner would make an identical refresh
  // rewrite the file, which is exactly the churn this script stopped causing.
  // The date below already tells the reader how fresh the list is, and the
  // operator gets a loud banner on stderr.
  out.push(
    `<small>Last refreshed ${meta.generatedAt} from ${releases.length} published releases.</small>`
  )
  out.push('')

  return out.join('\n')
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

function loud(lines) {
  const bar = '='.repeat(74)
  process.stderr.write(`\n${bar}\n${lines.join('\n')}\n${bar}\n\n`)
}

/**
 * Write only when the bytes differ.
 *
 * Both outputs are tracked files, and rewriting one with identical content
 * still moves its mtime — enough to make a watcher rebuild and enough to look
 * like a change to anyone eyeballing the tree. Returns whether it wrote.
 */
function writeIfChanged(file, content) {
  try {
    if (fs.readFileSync(file, 'utf8') === content) return false
  } catch {
    // missing or unreadable — fall through and write it
  }
  fs.writeFileSync(file, content, 'utf8')
  return true
}

function readJson(file, fallback) {
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'))
  } catch {
    return fallback
  }
}

async function main() {
  const cached = fs.existsSync(CACHE) ? readJson(CACHE, null) : null
  let releases = null
  let stale = false

  if (process.env.FILEX_RELEASES_OFFLINE === '1') {
    loud([
      'FILEX_RELEASES_OFFLINE=1 — the GitHub fetch was skipped on purpose.',
      'The Releases page is being rendered from data/releases.json.'
    ])
    stale = true
  } else {
    try {
      const raw = await fetchReleases()
      const fetched = raw
        .filter((r) => !r.draft && r.tag_name)
        .map(normalise)
        .sort((a, b) => String(b.date).localeCompare(String(a.date)))
      const cachedCount = cached && Array.isArray(cached.releases) ? cached.releases.length : 0
      if (fetched.length === 0 && cachedCount > 0) {
        throw new Error(
          `GitHub answered 200 with an empty release list while the cache holds ${cachedCount}` +
            ' — an outage, not a repository that lost every release'
        )
      }
      releases = fetched
      console.log(`[releases] fetched ${releases.length} releases from ${REPO}`)
    } catch (err) {
      stale = true
      loud([
        'RELEASES: GitHub could not be reached — the page was NOT refreshed.',
        `  reason: ${err.message}`,
        cached && cached.releases && cached.releases.length
          ? `  falling back to the committed cache (${cached.releases.length} releases,` +
            ` last refreshed ${cached.generatedAt}).`
          : '  and there is NO cache to fall back to.'
      ])
    }
  }

  if (!releases) {
    if (!cached || !Array.isArray(cached.releases) || cached.releases.length === 0) {
      loud([
        'RELEASES: refusing to publish an empty Releases page.',
        `  Restore ${path.relative(process.cwd(), CACHE)} from git, or run this`,
        '  script again with working network access.'
      ])
      process.exit(1)
    }
    releases = cached.releases
  }

  // Idempotence: `generatedAt` only moves when the release list actually moved.
  // Stamping today's date on an unchanged list is what made a re-run dirty the
  // tree — the data was identical and only the date differed, so the diff said
  // "something happened" when nothing had.
  const unchanged =
    cached &&
    Array.isArray(cached.releases) &&
    JSON.stringify(cached.releases) === JSON.stringify(releases)
  const generatedAt =
    unchanged || (stale && cached)
      ? cached.generatedAt
      : new Date().toISOString().slice(0, 10)

  const highlights = readJson(HIGHLIGHTS, {})

  fs.mkdirSync(path.dirname(CACHE), { recursive: true })
  const wroteCache = writeIfChanged(
    CACHE,
    JSON.stringify({ repo: REPO, generatedAt, releases }, null, 2) + '\n'
  )
  const wrotePage = writeIfChanged(PAGE, renderPage(releases, highlights, { generatedAt }))

  if (!wroteCache && !wrotePage) {
    console.log(
      `[releases] nothing to do — ${releases.length} releases, unchanged since ` +
        `${generatedAt}. Both files left exactly as they were.`
    )
    return
  }

  const summarised = releases.filter((r) => highlights[r.tag]).length
  const written = [wrotePage && PAGE, wroteCache && CACHE]
    .filter(Boolean)
    .map((f) => path.relative(process.cwd(), f))
    .join(' + ')
  console.log(
    `[releases] wrote ${written} — ` +
      `${releases.length} releases, ${summarised} with a hand-written summary`
  )
}

main().catch((err) => {
  loud(['RELEASES: generator crashed.', `  ${err.stack || err.message}`])
  process.exit(1)
})
