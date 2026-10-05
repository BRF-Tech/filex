// Launch an installed desktop app, wait for its window to load a page, and
// photograph that page.
//
//   node desktop-look.mjs --exe <path> [--arg <extra arg>]... --out <shot.png>
//                         [--port 9333] [--timeout 90] [--expect-sandbox]
//                         [--expect-snap-confinement] [--expect-refusal]
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
// --expect-sandbox (Linux): while the page is up, every renderer of the app
// must run in a PID namespace of its own (two or more numbers on its NSpid
// line in /proc/<pid>/status) and no process of the app may carry
// --no-sandbox. Both Chromium sandboxes, the setuid helper's and the user
// namespace one, put renderers in a new PID namespace; with --no-sandbox they
// share the browser's. A window alone proves nothing: the 0.49 AppImage and
// snap opened windows with the sandbox off.
//
// --expect-snap-confinement (Linux, the snap): the snap runs with Chromium's
// sandbox off on purpose (since 0.52; the Snap Store grants `allow-sandbox`
// to trusted publishers only), and snapd's strict confinement is the wall.
// While the page is up, every process of the app must run under the snap's
// AppArmor profile in enforce mode (/proc/<pid>/attr/apparmor/current is
// `snap.filex-app.filex-app (enforce)`) with a seccomp filter (`Seccomp: 2`
// in /proc/<pid>/status). A --devmode install (complain), or a process that
// escaped the profile, fails.
//
// --expect-refusal (Linux): the opposite case. The app must NOT open: the
// launcher (desktop/build/linux/launcher.sh) finds the sandbox cannot be built,
// says what to do, and exits 78 with that message on stderr.
//
// Exit 0: a page loaded and was photographed (and, with --expect-sandbox,
// sandboxed; with --expect-snap-confinement, confined); with
// --expect-refusal, the launcher refused. 1: anything else (its output is
// printed).
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

function parse(argv) {
  const o = { args: [], port: 9333, timeout: 90, expectSandbox: false, expectConfined: false, expectRefusal: false };
  for (let i = 0; i < argv.length; i++) {
    const k = argv[i];
    if (k === '--expect-sandbox') {
      o.expectSandbox = true;
      continue;
    }
    if (k === '--expect-snap-confinement') {
      o.expectConfined = true;
      continue;
    }
    if (k === '--expect-refusal') {
      o.expectRefusal = true;
      continue;
    }
    const v = argv[++i];
    if (k === '--exe') o.exe = v;
    else if (k === '--arg') o.args.push(v);
    else if (k === '--out') o.out = v;
    else if (k === '--port') o.port = Number(v);
    else if (k === '--timeout') o.timeout = Number(v);
    else throw new Error(`unknown argument ${k}`);
  }
  if (!o.exe || (!o.out && !o.expectRefusal)) {
    throw new Error('usage: desktop-look.mjs --exe <path> --out <png> [--arg x]... [--port n] [--timeout s] [--expect-sandbox] [--expect-snap-confinement] [--expect-refusal]');
  }
  return o;
}

/** The app's processes: every /proc entry whose command starts the Electron
 *  binary behind the Linux launcher (`filex-app-bin`). */
export function appProcesses(proc = '/proc') {
  const out = [];
  for (const pid of fs.readdirSync(proc).filter((d) => /^\d+$/.test(d))) {
    let argv;
    let status;
    try {
      argv = fs.readFileSync(path.join(proc, pid, 'cmdline'), 'utf8').split('\0').filter(Boolean);
      status = fs.readFileSync(path.join(proc, pid, 'status'), 'utf8');
    } catch {
      continue;
    }
    // Chromium rewrites the title of the processes its zygote forks: their
    // command line comes back as ONE string, words joined by spaces.
    if (argv.length === 1 && argv[0].includes(' --')) argv = argv[0].split(' ').filter(Boolean);
    if (!argv.length || !/(^|\/)filex-app-bin$/.test(argv[0])) continue;
    const nspid = /^NSpid:\s*(.+)$/m.exec(status)?.[1].trim().split(/\s+/) ?? [];
    const type = argv.find((a) => a.startsWith('--type='))?.slice(7) ?? 'browser';
    // The AppArmor label: attr/apparmor/current where the kernel stacks LSMs
    // (5.8+), attr/current before that. '' when neither can be read.
    let label = '';
    for (const f of [['attr', 'apparmor', 'current'], ['attr', 'current']]) {
      try {
        label = fs.readFileSync(path.join(proc, pid, ...f), 'utf8').split(String.fromCharCode(0)).join('').trim();
      } catch {
        continue;
      }
      if (label) break;
    }
    const seccomp = /^Seccomp:[ \t]*([0-9]+)$/m.exec(status)?.[1] ?? '';
    out.push({ pid: Number(pid), type, nspid, noSandbox: argv.includes('--no-sandbox'), label, seccomp });
  }
  return out;
}

/** Problems with the sandbox, from appProcesses(); empty when it is on. */
export function sandboxProblems(procs) {
  const problems = [];
  if (!procs.some((p) => p.type === 'browser')) problems.push('no browser process of filex-app-bin found');
  const renderers = procs.filter((p) => p.type === 'renderer');
  if (!renderers.length) problems.push('no renderer process found');
  for (const p of procs) if (p.noSandbox) problems.push(`pid ${p.pid} (${p.type}) runs with --no-sandbox`);
  for (const r of renderers) {
    if (r.nspid.length < 2) problems.push(`renderer ${r.pid} shares the browser's PID namespace (NSpid ${r.nspid.join(' ')}): not sandboxed`);
  }
  return problems;
}

/** Problems with the snap's confinement, from appProcesses(); empty when every
 *  process of the app runs under the snap's own AppArmor profile in enforce
 *  mode, with a seccomp filter. */
export function confinementProblems(procs, snap = 'filex-app') {
  const problems = [];
  if (!procs.some((p) => p.type === 'browser')) problems.push('no browser process of filex-app-bin found');
  if (!procs.some((p) => p.type === 'renderer')) problems.push('no renderer process found');
  const want = `snap.${snap}.${snap} (enforce)`;
  for (const p of procs) {
    if (p.label !== want) problems.push(`pid ${p.pid} (${p.type}) runs under AppArmor label "${p.label || 'none'}", not "${want}"`);
    if (p.seccomp !== '2') problems.push(`pid ${p.pid} (${p.type}) has no seccomp filter (Seccomp: ${p.seccomp || '?'})`);
  }
  return problems;
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
  if (o.expectRefusal) return refusal(o);
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
    if (o.expectSandbox) {
      const procs = appProcesses();
      const problems = sandboxProblems(procs);
      console.log(`sandbox: ${procs.map((p) => `${p.type}:${p.pid}[NSpid ${p.nspid.join(' ')}]`).join(', ')}`);
      if (problems.length) {
        console.error(`the app is NOT sandboxed:\n  ${problems.join('\n  ')}`);
        return 1;
      }
      console.log('sandbox: every renderer is in its own PID namespace, no --no-sandbox');
    }
    if (o.expectConfined) {
      const procs = appProcesses();
      const problems = confinementProblems(procs);
      console.log(`confinement: ${procs.map((p) => `${p.type}:${p.pid}[${p.label || 'no label'}, Seccomp ${p.seccomp || '?'}]`).join(', ')}`);
      if (problems.length) {
        console.error('the app is NOT confined by its snap:');
        for (const p of problems) console.error(`  ${p}`);
        return 1;
      }
      console.log("confinement: every process of the app is under the snap's AppArmor profile (enforce), with a seccomp filter");
    }
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

/** --expect-refusal: the launcher must say what to do and exit 78, with no
 *  window and no app process left behind. */
async function refusal(o) {
  const r = await new Promise((resolve) => {
    let out = '';
    let err = '';
    const child = spawn(o.exe, [...o.args, `--remote-debugging-port=${o.port}`], { stdio: ['ignore', 'pipe', 'pipe'] });
    const timer = setTimeout(() => child.kill(), o.timeout * 1000);
    child.stdout.on('data', (b) => (out += b));
    child.stderr.on('data', (b) => (err += b));
    child.on('exit', (code, sig) => {
      clearTimeout(timer);
      resolve({ code, sig, out, err });
    });
    child.on('error', (e) => {
      clearTimeout(timer);
      resolve({ code: null, sig: null, out, err: `could not start: ${e.message}` });
    });
  });
  console.log(r.err.split('\n').slice(0, 30).join('\n'));
  const page = await pageTarget(o.port);
  const left = appProcesses();
  if (r.code === 78 && /does not start without Chromium's sandbox/.test(r.err) && !page && !left.length) {
    console.log('refused: exit 78, the message above, no window');
    return 0;
  }
  console.error(`expected the launcher to refuse (exit 78 + message, no window); got exit ${r.code ?? r.sig}, page ${page ? page.url : 'none'}, ${left.length} app process(es)`);
  return 1;
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
