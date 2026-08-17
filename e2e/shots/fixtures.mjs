// The little world the screenshots are taken in.
//
// Deliberately generated rather than committed as binaries: the shots have to
// be retakeable on any machine at any release, and a fixture set that lives in
// git as 8 PNGs is one more thing to forget. Everything here is written with
// Node's own zlib — no image library, no network.

import { deflateSync } from 'node:zlib';
import { mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';

// ── minimal PNG encoder (8-bit RGB, no interlace) ─────────────────────────
const CRC_TABLE = (() => {
  const t = new Int32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c;
  }
  return t;
})();

function crc32(buf) {
  let c = -1;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ -1) >>> 0;
}

function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([len, body, crc]);
}

/** encodePNG renders `pixel(x, y) -> [r, g, b]` into a PNG buffer. */
export function encodePNG(width, height, pixel) {
  const raw = Buffer.alloc(height * (1 + width * 3));
  let o = 0;
  for (let y = 0; y < height; y++) {
    raw[o++] = 0; // filter: none
    for (let x = 0; x < width; x++) {
      const [r, g, b] = pixel(x, y);
      raw[o++] = r;
      raw[o++] = g;
      raw[o++] = b;
    }
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // colour type: truecolour
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw, { level: 6 })),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

const mix = (a, b, t) => Math.round(a + (b - a) * t);

// Each photo is a named gradient — recognisable at thumbnail size, which is
// the size they are actually seen at in the grid.
const PHOTOS = [
  { name: 'aurora.png', from: [26, 47, 74], to: [46, 189, 168] },
  { name: 'ember.png', from: [193, 68, 21], to: [246, 190, 76] },
  { name: 'forest.png', from: [24, 84, 46], to: [140, 197, 106] },
  { name: 'harbour.png', from: [17, 45, 82], to: [86, 170, 214] },
  { name: 'mosaic.png', from: [63, 105, 189], to: [126, 178, 233] },
  { name: 'nebula.png', from: [88, 27, 122], to: [230, 108, 196] },
  { name: 'ocean.png', from: [8, 60, 92], to: [40, 176, 214] },
  { name: 'sunset.png', from: [186, 44, 63], to: [244, 150, 66] },
];

const README = `# filex

A **self-hosted file manager**: one Go binary, pluggable storage, and a UI you can embed anywhere.

## What you are looking at

This folder is the demo fixture the project's screenshots are taken from — the same explorer you would run on your own server.

## Try these

- **Click a file** to preview it: images, video, audio, PDF, Office documents, source code, notebooks, 3D models and diagrams each open in their own viewer.
- **Right-click → Share / Permissions** to mint a public link, with an optional PIN, an expiry and a download limit.
- **Drag & drop** to upload, and convert between formats.
- **Search** runs across the whole tree, contents included.

## Good to know

Storage is a driver, not a directory: local disk, S3, FTP, SFTP, WebDAV and SMB/NAS all look the same from here. Nothing in the UI knows which one it is talking to.

And it works the other way too — this same tree is reachable as **S3**, **SFTP**, **FTPS**, **NFS** and **WebDAV**, so rclone, restic, WinSCP or a scanner that only ever learned FTP can point straight at it.
`;

const NOTES = `# Release notes — draft

- Share links can now be capped at a number of downloads.
- The share dialog shows a one-line curl for pulling a file onto a server.
- Profile pictures show up in the collaboration bar.
`;

/**
 * seedFixtures materialises the screenshot world under `root` and returns the
 * folder the explorer should open on.
 */
export function seedFixtures(root) {
  rmSync(root, { recursive: true, force: true });
  const photos = join(root, 'Photos');
  const docs = join(root, 'Documents');
  mkdirSync(photos, { recursive: true });
  mkdirSync(docs, { recursive: true });

  for (const p of PHOTOS) {
    const png = encodePNG(640, 640, (x, y) => {
      const t = (x + y) / (640 + 640);
      return [mix(p.from[0], p.to[0], t), mix(p.from[1], p.to[1], t), mix(p.from[2], p.to[2], t)];
    });
    writeFileSync(join(photos, p.name), png);
  }

  writeFileSync(join(root, 'README.md'), README);
  writeFileSync(join(docs, 'release-notes.md'), NOTES);
  writeFileSync(join(docs, 'budget.csv'), 'quarter,revenue,costs\nQ1,120000,84000\nQ2,138500,91200\n');
  writeFileSync(join(docs, 'deploy.sh'), '#!/usr/bin/env bash\nset -euo pipefail\nfilex serve --listen 0.0.0.0:5212\n');

  return { photos: 'Photos', readme: 'README.md' };
}
