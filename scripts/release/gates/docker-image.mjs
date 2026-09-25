#!/usr/bin/env node
// The image gates of `pnpm release`: an image that was built is not yet an
// image that works.
//
//   node scripts/release/gates/docker-image.mjs --version v0.45.0 <image>…
//       every image's `filex --version` names the release
//   node scripts/release/gates/docker-image.mjs --serve <image>
//       the image starts, answers /healthz, and serves the admin UI and the
//       embeddable explorer — each followed to the bundle it names and WEIGHED
//
// ⚠ Why weigh the bundles: /embed.js is a tiny ES entry that imports the real
// bundle, and /admin/ is a shell that loads its own. An image whose frontend
// stage produced nothing still answers both with 200. v0.43.0's images never
// built at all (the frontend stage did not copy two files the vite configs
// import), and nothing local had ever built one.
//
// Nothing is published on a host port: every request is made from inside the
// container, so a release run cannot collide with anything on the machine.

import { docker as dockerRun } from '../engine.mjs';

const argv = process.argv.slice(2);
const MIN_BYTES = 100_000;
const docker = (...args) => dockerRun(args);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function usage() {
  console.error('usage: docker-image.mjs --version vX.Y.Z <image>… | --serve <image>');
  process.exit(2);
}

async function versionOf(tag, images) {
  let ok = images.length > 0;
  for (const img of images) {
    const r = docker('run', '--rm', '--platform', 'linux/amd64', '--entrypoint', '/usr/local/bin/filex', img, '--version');
    const out = `${r.stdout}${r.stderr}`.trim();
    console.log(`${img}: ${out}`);
    if (r.status !== 0 || !out.includes(tag)) {
      console.log(`  ✗ does not report ${tag}`);
      ok = false;
    }
  }
  return ok;
}

async function serve(img) {
  const ctr = `filex-release-smoke-${process.pid}`;
  docker('rm', '-f', ctr);
  const start = docker('run', '-d', '--name', ctr, '--platform', 'linux/amd64', img, 'serve');
  if (start.status !== 0) {
    console.log(`could not start ${img}: ${start.stderr}`);
    return false;
  }
  const inside = (script) => docker('exec', ctr, 'sh', '-c', script);
  try {
    let healthy = false;
    for (let i = 0; i < 60 && !healthy; i++) {
      healthy = inside('wget -q -O /dev/null http://127.0.0.1:5212/healthz').status === 0;
      if (!healthy) await sleep(1000);
    }
    console.log(`healthz: ${healthy ? 'ok' : 'NEVER'}`);
    if (!healthy) return false;
    const get = (p) => inside(`wget -q -O - http://127.0.0.1:5212${p}`).stdout ?? '';
    const size = (p) => Number(inside(`wget -q -O - http://127.0.0.1:5212${p} | wc -c`).stdout.trim() || 0);
    const chunk = /\.\/([A-Za-z0-9_.-]+\.js)/.exec(get('/embed.js'))?.[1];
    const admin = /src="(\/admin\/assets\/[^"]+\.js)"/.exec(get('/admin/'))?.[1];
    const chunkBytes = chunk ? size(`/embed/${chunk}`) : 0;
    const adminBytes = admin ? size(admin) : 0;
    console.log(`embed.js imports: ${chunk ?? 'NONE'} (${chunkBytes} bytes)`);
    console.log(`admin entry:      ${admin ?? 'NONE'} (${adminBytes} bytes)`);
    return chunkBytes > MIN_BYTES && adminBytes > MIN_BYTES;
  } finally {
    const logs = docker('logs', '--tail', '20', ctr);
    console.log(`--- container log (last 20 lines)\n${logs.stdout}${logs.stderr}`);
    docker('rm', '-f', ctr);
  }
}

let ok;
if (argv[0] === '--version' && argv[1] && argv.length > 2) ok = await versionOf(argv[1], argv.slice(2));
else if (argv[0] === '--serve' && argv.length === 2) ok = await serve(argv[1]);
else usage();
process.exit(ok ? 0 : 1);
