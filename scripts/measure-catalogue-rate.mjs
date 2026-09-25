#!/usr/bin/env node
// How fast a local storage is catalogued, in files per second — the rate half
// of docs/LAZY-CATALOGUE.md "Measured", against a real binary.
//
//   node scripts/measure-catalogue-rate.mjs --bin <filex binary> --port <p> \
//        [--tree <dir>] [--what A|P|AP] [--out <report.json>] [--keep]
//
//   A  a `lazy` storage in behaviour A (background fill): the storage's
//      figures are polled every second, as an admin page watching it would,
//      until the filler converges. Rate = files / time from creation.
//   P  a `poll` storage during its first full scan: file count growth over a
//      60 s window with nothing polling, then on to the end of the scan.
//
// The tree (default: <os tmp>/filex-catalogue-100k) is generated when missing:
// 40 × 25 folders of 100 files plus 20 at the root — 100,020 files in 1,041
// folders, the layout every figure in the doc was taken on. The server gets a
// fresh data directory under the OS temp directory each run, is stopped at the
// end, and the directory is removed unless --keep; nothing else is touched.
//
// ⚠ Compare only numbers taken on the same machine, one run after another.
// The binary's SQLite fsyncs on every commit, so the disk and whatever else is
// using it are half of any single figure.

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { spawn } from 'node:child_process'

const arg = (name, def) => {
  const i = process.argv.indexOf(`--${name}`)
  return i > -1 && process.argv[i + 1] ? process.argv[i + 1] : def
}
const BIN = arg('bin')
const PORT = Number(arg('port'))
const TREE = path.resolve(arg('tree', path.join(os.tmpdir(), 'filex-catalogue-100k')))
const WHAT = arg('what', 'AP')
const OUT = arg('out')
const KEEP = process.argv.includes('--keep')
if (!BIN || !PORT) {
  console.error('usage: node scripts/measure-catalogue-rate.mjs --bin <filex binary> --port <port> [--tree <dir>] [--what A|P|AP] [--out <file>]')
  process.exit(2)
}

const TOP = 40
const SUB = 25
const FILES = 100
const TOTAL = TOP * SUB * FILES + 20

function ensureTree() {
  // Beside the tree, not in it: the storage would count it.
  const marker = `${TREE}.complete`
  if (fs.existsSync(marker)) return
  const t0 = Date.now()
  fs.mkdirSync(TREE, { recursive: true })
  for (let i = 0; i < 20; i++) fs.writeFileSync(path.join(TREE, `kok-${String(i).padStart(2, '0')}.txt`), 'x'.repeat(100 + i))
  for (let a = 0; a < TOP; a++) {
    for (let b = 0; b < SUB; b++) {
      const d = path.join(TREE, `arsiv-${String(a).padStart(2, '0')}`, `klasor-${String(b).padStart(2, '0')}`)
      fs.mkdirSync(d, { recursive: true })
      for (let c = 0; c < FILES; c++) fs.writeFileSync(path.join(d, `belge-${String(c).padStart(3, '0')}.txt`), 'y'.repeat(10 + c))
    }
  }
  fs.writeFileSync(marker, '')
  console.error(`tree: ${TOTAL} files in ${((Date.now() - t0) / 1000).toFixed(0)} s`)
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const BASE = `http://127.0.0.1:${PORT}`
let cookie = ''
async function api(method, url, body) {
  const res = await fetch(BASE + url, {
    method,
    headers: { 'content-type': 'application/json', ...(cookie ? { cookie } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  })
  const set = res.headers.getSetCookie?.() ?? []
  if (set.length) cookie = set.map((c) => c.split(';')[0]).join('; ')
  if (!res.ok) throw new Error(`${method} ${url}: ${res.status} ${await res.text()}`)
  return res.json()
}

ensureTree()
const data = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-rate-'))
const log = fs.openSync(path.join(data, 'server.log'), 'a')
const server = spawn(path.resolve(BIN), ['serve'], {
  env: {
    ...process.env,
    FILEX_DATA_DIR: data,
    FILEX_LISTEN: `127.0.0.1:${PORT}`,
    FILEX_PUBLIC_URL: BASE,
    FILEX_ADMIN_EMAIL: 'admin@local',
    FILEX_ADMIN_PASSWORD: 'admin',
    FILEX_SECRET_KEY: 'catalogue-rate-lab-not-for-production',
  },
  stdio: ['ignore', log, log],
})
let exited = null
server.on('exit', (c) => (exited = c))

const storage = (name, mode, extra) =>
  api('POST', '/api/admin/storages', {
    name, driver: 'local', sync_mode: mode, sync_interval_s: 0, enabled: true, read_only: false,
    config: { path: TREE, ...extra },
  })

const report = { binary: path.resolve(BIN), tree: TREE, files: TOTAL, data, started: new Date().toISOString() }
try {
  for (let i = 0; ; i++) {
    if (exited !== null) throw new Error(`server exited ${exited}`)
    try {
      if ((await fetch(`${BASE}/healthz`)).ok) break
    } catch {
      /* not listening yet */
    }
    if (i > 240) throw new Error('server never became healthy')
    await sleep(250)
  }
  await api('POST', '/api/auth/login', { email: 'admin@local', password: 'admin' })

  if (WHAT.includes('A')) {
    const t0 = performance.now()
    const { id } = await storage('rate-a', 'lazy', { lazy_fill: 'background' })
    for (;;) {
      const st = await api('GET', `/api/admin/storages/${id}`)
      if (st.catalogue?.complete) {
        const s = (performance.now() - t0) / 1000
        report.A = { seconds: Math.round(s), files: st.stats?.file_count, files_per_s: Math.round(TOTAL / s) }
        break
      }
      if (performance.now() - t0 > 3 * 3600_000) throw new Error('behaviour A never converged')
      await sleep(1000)
    }
    console.error('A', JSON.stringify(report.A))
  }

  if (WHAT.includes('P')) {
    const t0 = performance.now()
    const { id } = await storage('rate-p', 'poll', {})
    await sleep(1600)
    const count = async () => (await api('GET', `/api/admin/storages/${id}`)).stats?.file_count ?? 0
    const c0 = await count()
    const w0 = performance.now()
    await sleep(60_000)
    const c1 = await count()
    report.P = { files_per_s_60s: Math.round(((c1 - c0) * 1000) / (performance.now() - w0)) }
    for (;;) {
      const st = await api('GET', `/api/admin/storages/${id}`)
      if (st.last_sync_at && (st.stats?.file_count ?? 0) >= TOTAL) {
        const s = (performance.now() - t0) / 1000
        Object.assign(report.P, { seconds: Math.round(s), files: st.stats?.file_count, files_per_s: Math.round(TOTAL / s) })
        break
      }
      if (performance.now() - t0 > 3 * 3600_000) throw new Error('the full scan never finished')
      await sleep(5000)
    }
    console.error('P', JSON.stringify(report.P))
  }
} catch (err) {
  report.error = String(err?.stack ?? err)
  process.exitCode = 1
} finally {
  server.kill()
  await sleep(1000)
  if (!KEEP) fs.rmSync(data, { recursive: true, force: true })
  report.finished = new Date().toISOString()
  if (OUT) fs.writeFileSync(OUT, JSON.stringify(report, null, 2))
  console.log(JSON.stringify(report, null, 2))
}
