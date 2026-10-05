#!/usr/bin/env node
/**
 * A stand-in for GitHub's raw file host, for the specs that install an app
 * "from GitHub" (202-store-install: a store's link names a GitHub repository
 * and a release). The server under test reads repositories here through
 * FILEX_APP_GITHUB_RAW_BASE (e2e/run.mjs sets it), and a spec publishes a
 * repository by writing files into the directory:
 *
 *   <dir>/<owner>/<name>/<ref>/filex-app.json   →   GET /<owner>/<name>/<ref>/filex-app.json
 *
 *   node e2e/lib/fake-github.mjs <dir> <port>
 *
 * ⚠ Its own process, started by run.mjs: run.mjs runs Playwright with
 * spawnSync, which blocks its own event loop for the whole suite, so a server
 * inside it would never answer. Several Playwright workers share it, which is
 * why it serves a directory every worker can write into.
 */
import { createServer } from 'node:http';
import { createReadStream, statSync } from 'node:fs';
import path from 'node:path';

const [dir, portArg] = process.argv.slice(2);
if (!dir || !portArg) {
  console.error('usage: fake-github.mjs <dir> <port>');
  process.exit(2);
}
const root = path.resolve(dir);

createServer((req, res) => {
  const url = new URL(req.url ?? '/', 'http://x');
  const file = path.resolve(root, '.' + decodeURIComponent(url.pathname));
  if (req.method !== 'GET' || !file.startsWith(root + path.sep)) {
    res.writeHead(404).end('not found');
    return;
  }
  let st;
  try {
    st = statSync(file);
  } catch {
    st = null;
  }
  if (!st || !st.isFile()) {
    res.writeHead(404).end('not found');
    return;
  }
  res.writeHead(200, { 'Content-Length': st.size, 'Content-Type': 'application/octet-stream' });
  createReadStream(file).pipe(res);
}).listen(Number(portArg), '127.0.0.1', () => console.log(`fake github on 127.0.0.1:${portArg}, serving ${root}`));
