// Launch an installed desktop app, wait for its window to load a page, and
// photograph that page.
//
//   node desktop-look.mjs --exe <path> [--arg <extra arg>]... --out <shot.png>
//                         [--port 9333] [--timeout 90]
//
// Starts <exe> with --remote-debugging-port, polls /json until a page target
// has a URL, then asks that page for a screenshot over the DevTools protocol
// (Page.captureScreenshot) and writes it to --out. Prints the page's URL and
// title, and the size of the picture. Kills the app on the way out.
//
// ⚠ Why DevTools and not a screen grab: the runner's display may be a virtual
// framebuffer (xvfb on Linux) or a service session (Windows), and a grab of
// either can be a black rectangle that "passes". A picture the page itself
// renders proves the window loaded something; the PNG is looked at by a
// person before anyone calls the package good.
//
// Exit 0: a page loaded and was photographed. 1: the app exited, or no page
// within the timeout (its output is printed).
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

function parse(argv) {
  const o = { args: [], port: 9333, timeout: 90 };
  for (let i = 0; i < argv.length; i++) {
    const k = argv[i];
    const v = argv[++i];
    if (k === '--exe') o.exe = v;
    else if (k === '--arg') o.args.push(v);
    else if (k === '--out') o.out = v;
    else if (k === '--port') o.port = Number(v);
    else if (k === '--timeout') o.timeout = Number(v);
    else throw new Error(`unknown argument ${k}`);
  }
  if (!o.exe || !o.out) throw new Error('usage: desktop-look.mjs --exe <path> --out <png> [--arg x]... [--port n] [--timeout s]');
  return o;
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function pageTarget(port) {
  try {
    const r = await fetch(`http://127.0.0.1:${port}/json`);
    if (!r.ok) return null;
    const list = await r.json();
    return list.find((t) => t.type === 'page' && t.url && t.url !== 'about:blank' && t.webSocketDebuggerUrl) ?? null;
  } catch {
    return null;
  }
}

function cdp(wsUrl) {
  const ws = new WebSocket(wsUrl);
  let id = 0;
  const waiting = new Map();
  ws.addEventListener('message', (ev) => {
    const msg = JSON.parse(typeof ev.data === 'string' ? ev.data : Buffer.from(ev.data).toString('utf8'));
    const w = waiting.get(msg.id);
    if (!w) return;
    waiting.delete(msg.id);
    if (msg.error) w.reject(new Error(msg.error.message));
    else w.resolve(msg.result);
  });
  const open = new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', () => reject(new Error(`could not connect to ${wsUrl}`)), { once: true });
  });
  return {
    open,
    send: (method, params = {}) =>
      new Promise((resolve, reject) => {
        const n = ++id;
        waiting.set(n, { resolve, reject });
        ws.send(JSON.stringify({ id: n, method, params }));
      }),
    close: () => ws.close(),
  };
}

async function main() {
  const o = parse(process.argv.slice(2));
  let out = '';
  const child = spawn(o.exe, [...o.args, `--remote-debugging-port=${o.port}`], { stdio: ['ignore', 'pipe', 'pipe'] });
  child.stdout.on('data', (b) => (out += b));
  child.stderr.on('data', (b) => (out += b));
  let exited = null;
  child.on('exit', (code, sig) => (exited = `${code ?? sig}`));
  child.on('error', (e) => (exited = `could not start: ${e.message}`));
  try {
    const deadline = Date.now() + o.timeout * 1000;
    let target = null;
    while (!target && Date.now() < deadline && exited === null) {
      target = await pageTarget(o.port);
      if (!target) await sleep(1000);
    }
    if (!target) {
      console.error(exited !== null ? `the app exited (${exited}) before a page loaded` : `no page within ${o.timeout} s`);
      console.error(out.split('\n').slice(-40).join('\n'));
      return 1;
    }
    // Give the page a moment to paint past its first frame.
    await sleep(4000);
    const s = cdp(target.webSocketDebuggerUrl);
    await s.open;
    const info = await s.send('Runtime.evaluate', { expression: 'JSON.stringify({url: location.href, title: document.title, text: (document.body && document.body.innerText || "").slice(0, 200)})', returnByValue: true });
    const shot = await s.send('Page.captureScreenshot', { format: 'png' });
    s.close();
    const png = Buffer.from(shot.data, 'base64');
    fs.mkdirSync(path.dirname(path.resolve(o.out)), { recursive: true });
    fs.writeFileSync(o.out, png);
    console.log(`page: ${info.result?.value}`);
    console.log(`screenshot: ${o.out} (${png.length} bytes)`);
    return png.length > 1000 ? 0 : 1;
  } finally {
    if (exited === null) {
      child.kill();
      await sleep(2000);
    }
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then(
    (code) => process.exit(code),
    (e) => {
      console.error(String(e?.stack ?? e));
      process.exit(1);
    },
  );
}
