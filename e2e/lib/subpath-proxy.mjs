// A reverse proxy that serves filex under a sub-path, for the e2e suite.
//
//   node e2e/run.mjs local --base-path /filex
//
// runs the WHOLE Playwright suite against filex served under `/filex` behind
// this proxy (FILEX_BASE_PATH=/filex on the server). It does what a real proxy
// is told to do in docs/DEPLOYMENT.md: everything under the base is passed on
// with the FULL path (Caddy `handle /filex/*`, nginx `location /filex/`), the
// Host header unchanged, WebSocket upgrades included.
//
// ⚠⚠ The one thing it does that a production proxy does not: the specs were
// written for a filex at the root, and they address it with root-relative
// paths (`page.goto('/admin/login')`, `request.post('/api/files/manager')`).
// A request the TEST made to a path outside the base is answered with a 307
// into the base, so the suite reaches the app at all — the browser then LIVES
// under the base (a page has to, or it is not the deployment being measured),
// and Playwright's API client follows it with the cookies the base carries.
// ⚠ A spec that refuses redirects (`maxRedirects: 0`) addresses the base
// itself (E2E_BASE_PATH), because it is measuring the answer, not reaching it.
//
// A request the APP made outside the base is the bug this run exists to find:
// it is answered 404, exactly as a real proxy would, and recorded as a
// VIOLATION — the run fails on any.
//
// Which is which is read from the browser's own Fetch Metadata. Chromium sends
// `Sec-Fetch-Site` on every request a page makes; a navigation the test starts
// (`page.goto`) is `none` (like a typed address), anything the app starts —
// fetch, XHR, <img>, <script>, a link, `location.replace` — is `same-origin`.
// Playwright's API client (the `request` fixture) runs outside the browser and
// sends no Fetch Metadata at all.
//
// ⚠ A PROCESS of its own (run.mjs spawns this file), never a server inside
// run.mjs: the harness runs Playwright with spawnSync, which blocks its event
// loop for the whole suite, and a proxy living on that loop answers nothing —
// measured, every spec timed out on its first request. The verdict is read
// back over HTTP afterwards: GET /__subpath-proxy/report.

import http from 'node:http';
import net from 'node:net';
import { fileURLToPath } from 'node:url';

/** Where run.mjs reads the verdict. Outside any base, answered first. */
export const REPORT_PATH = '/__subpath-proxy/report';

/**
 * @param {{ base: string, upstream: string, port?: number }} opts
 *   base: `/filex`; upstream: `http://127.0.0.1:<filex port>` (no path)
 */
export function startSubpathProxy({ base, upstream, port = 0 }) {
  const up = new URL(upstream);
  const report = { forwarded: 0, rescued: 0, rescuedSamples: [], violations: [] };

  const underBase = (p) => p === base || p.startsWith(`${base}/`);
  const testInitiated = (req) => {
    const site = req.headers['sec-fetch-site'];
    return site === undefined || site === 'none';
  };

  /** Pass req upstream, path unchanged. */
  const forward = (req, res, path) => {
    const fwd = http.request(
      {
        host: up.hostname,
        port: up.port,
        method: req.method,
        path,
        headers: { ...req.headers, 'x-forwarded-proto': 'http', 'x-forwarded-host': req.headers.host },
      },
      (upRes) => {
        res.writeHead(upRes.statusCode || 502, upRes.headers);
        upRes.on('error', () => res.destroy());
        upRes.pipe(res);
      },
    );
    fwd.on('error', (err) => {
      if (res.writableEnded || res.destroyed) return;
      if (!res.headersSent) res.writeHead(502, { 'content-type': 'text/plain' });
      res.end(`proxy: ${err.message}`);
    });
    // ⚠ A browser aborts requests all the time (a navigation away, a
    // cancelled download). An 'error' nobody listens to kills the process,
    // and every later spec then fails on a proxy that is not there.
    req.on('error', () => fwd.destroy());
    res.on('error', () => fwd.destroy());
    res.on('close', () => {
      if (!res.writableFinished) fwd.destroy();
    });
    req.pipe(fwd);
  };

  const server = http.createServer((req, res) => {
    const path = (req.url || '/').split('?')[0];
    if (path === REPORT_PATH) {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify(report));
      return;
    }
    if (underBase(path)) {
      report.forwarded++;
      forward(req, res, req.url);
      return;
    }
    if (testInitiated(req)) {
      report.rescued++;
      if (report.rescuedSamples.length < 20) report.rescuedSamples.push(`${req.method} ${req.url}`);
      res.writeHead(307, { location: base + req.url });
      res.end();
      return;
    }
    report.violations.push({
      method: req.method,
      url: req.url,
      site: req.headers['sec-fetch-site'] ?? '',
      dest: req.headers['sec-fetch-dest'] ?? '',
      referer: req.headers.referer ?? '',
    });
    res.writeHead(404, { 'content-type': 'text/plain' });
    res.end('not under the base path (e2e sub-path proxy)');
  });

  // WebSocket upgrades (the realtime socket, /filex/api/ws): a raw pipe.
  server.on('upgrade', (req, socket, head) => {
    const path = (req.url || '/').split('?')[0];
    if (!underBase(path)) {
      report.violations.push({ method: 'UPGRADE', url: req.url, site: req.headers['sec-fetch-site'] ?? '', dest: 'websocket', referer: '' });
      socket.end('HTTP/1.1 404 Not Found\r\n\r\n');
      return;
    }
    report.forwarded++;
    const upSock = net.connect(Number(up.port), up.hostname, () => {
      const lines = [`${req.method} ${req.url} HTTP/${req.httpVersion}`];
      for (let i = 0; i < req.rawHeaders.length; i += 2) lines.push(`${req.rawHeaders[i]}: ${req.rawHeaders[i + 1]}`);
      upSock.write(`${lines.join('\r\n')}\r\n\r\n`);
      if (head?.length) upSock.write(head);
      upSock.pipe(socket);
      socket.pipe(upSock);
    });
    const end = () => {
      socket.destroy();
      upSock.destroy();
    };
    upSock.on('error', end);
    socket.on('error', end);
  });

  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', () => {
      const { port: bound } = server.address();
      resolve({
        url: `http://127.0.0.1:${bound}`,
        port: bound,
        report,
        close: () =>
          new Promise((r) => {
            server.closeAllConnections?.();
            server.close(() => r());
          }),
      });
    });
  });
}

// As a process: node e2e/lib/subpath-proxy.mjs --base /filex --upstream http://127.0.0.1:N --port P
if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const arg = (name) => {
    const i = process.argv.indexOf(`--${name}`);
    return i > 0 ? process.argv[i + 1] : undefined;
  };
  // A dead proxy fails every remaining spec for a reason that is not filex's;
  // say what happened and keep serving.
  process.on('uncaughtException', (err) => console.error(`subpath-proxy: ${err.stack || err}`));
  const p = await startSubpathProxy({ base: arg('base'), upstream: arg('upstream'), port: Number(arg('port')) || 0 });
  console.log(`subpath-proxy listening ${p.url} base=${arg('base')} upstream=${arg('upstream')}`);
}
