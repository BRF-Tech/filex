// Reads and writes PNG with Node's zlib alone, and tells two pictures apart by
// their pixels - what `pnpm shots` uses to decide whether a screenshot it has
// just taken is a new picture or the published one again.
//
// ⚠ Why not compare the files. Two takes of an unchanged screen are often not
// byte-identical: the encoder, a font hinted a hair differently, a caret that
// blinked. Comparing bytes would put every such picture in front of a person
// and on the site again, which is exactly the review load this exists to cut
// (task #176: 150 pictures reviewed per release, ~25 of them changed).
//
// ⚠ Why not a package. The repository's tooling has no image dependency on
// purpose (e2e/shots/fixtures.mjs encodes its PNGs the same way), and the
// pictures this reads are all Chromium's: 8-bit RGB or RGBA, not interlaced.
// Everything else a PNG may be is read too where it is cheap (grey, palette,
// 16-bit), and refused by name where it is not (interlaced), so a picture this
// cannot read is reported as "could not compare" - never as "unchanged".

import zlib from 'node:zlib';

const SIGNATURE = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
const CHANNELS = { 0: 1, 2: 3, 3: 1, 4: 2, 6: 4 };

/**
 * Decodes a PNG into `{ width, height, data }`, `data` being RGBA, 4 bytes a
 * pixel, row after row. Throws with the reason on anything it cannot read.
 */
export function decodePng(input) {
  const buf = Buffer.isBuffer(input) ? input : Buffer.from(input);
  if (buf.length < 8 || !buf.subarray(0, 8).equals(SIGNATURE)) throw new Error('not a PNG file');
  let off = 8;
  let ihdr = null;
  let palette = null;
  let trns = null;
  const idat = [];
  while (off + 8 <= buf.length) {
    const len = buf.readUInt32BE(off);
    const type = buf.toString('latin1', off + 4, off + 8);
    if (off + 12 + len > buf.length) throw new Error(`PNG chunk ${type} runs past the end of the file`);
    const data = buf.subarray(off + 8, off + 8 + len);
    off += 12 + len;
    if (type === 'IHDR') {
      ihdr = {
        width: data.readUInt32BE(0),
        height: data.readUInt32BE(4),
        depth: data[8],
        type: data[9],
        interlace: data[12],
      };
    } else if (type === 'PLTE') palette = data;
    else if (type === 'tRNS') trns = data;
    else if (type === 'IDAT') idat.push(data);
    else if (type === 'IEND') break;
  }
  if (!ihdr) throw new Error('PNG has no IHDR');
  const { width, height, depth, type, interlace } = ihdr;
  if (!width || !height) throw new Error('PNG has no pixels');
  if (interlace !== 0) throw new Error('interlaced PNG is not supported');
  const channels = CHANNELS[type];
  if (!channels) throw new Error(`PNG colour type ${type} is not supported`);
  const depthOk = type === 3 ? [1, 2, 4, 8].includes(depth) : depth === 8 || depth === 16;
  if (!depthOk) throw new Error(`PNG bit depth ${depth} with colour type ${type} is not supported`);
  if (type === 3 && !palette) throw new Error('palette PNG without a PLTE chunk');

  const bitsPerPixel = channels * depth;
  const stride = Math.ceil((width * bitsPerPixel) / 8);
  const bpp = Math.max(1, bitsPerPixel >> 3);
  const raw = zlib.inflateSync(Buffer.concat(idat));
  if (raw.length < height * (stride + 1)) throw new Error('PNG image data is shorter than its size says');

  const rows = Buffer.alloc(stride * height);
  let prev = Buffer.alloc(stride);
  let p = 0;
  for (let y = 0; y < height; y++) {
    const filter = raw[p++];
    const cur = rows.subarray(y * stride, (y + 1) * stride);
    for (let i = 0; i < stride; i++) {
      const x = raw[p + i];
      const a = i >= bpp ? cur[i - bpp] : 0;
      const b = prev[i];
      const c = i >= bpp ? prev[i - bpp] : 0;
      let v;
      switch (filter) {
        case 0:
          v = x;
          break;
        case 1:
          v = x + a;
          break;
        case 2:
          v = x + b;
          break;
        case 3:
          v = x + ((a + b) >> 1);
          break;
        case 4: {
          const pa = Math.abs(b - c);
          const pb = Math.abs(a - c);
          const pc = Math.abs(a + b - 2 * c);
          v = x + (pa <= pb && pa <= pc ? a : pb <= pc ? b : c);
          break;
        }
        default:
          throw new Error(`PNG row ${y} has an unknown filter ${filter}`);
      }
      cur[i] = v & 0xff;
    }
    p += stride;
    prev = cur;
  }

  const data = new Uint8Array(width * height * 4);
  const sample = (row, index) => (depth === 16 ? row[index * 2] : row[index]);
  for (let y = 0; y < height; y++) {
    const row = rows.subarray(y * stride, (y + 1) * stride);
    for (let x = 0; x < width; x++) {
      const o = (y * width + x) * 4;
      if (type === 3) {
        const perByte = 8 / depth;
        const byte = row[Math.floor(x / perByte)];
        const shift = 8 - depth * ((x % perByte) + 1);
        const idx = (byte >> shift) & ((1 << depth) - 1);
        data[o] = palette[idx * 3] ?? 0;
        data[o + 1] = palette[idx * 3 + 1] ?? 0;
        data[o + 2] = palette[idx * 3 + 2] ?? 0;
        data[o + 3] = trns && idx < trns.length ? trns[idx] : 255;
      } else if (type === 0) {
        const g = sample(row, x);
        data[o] = data[o + 1] = data[o + 2] = g;
        data[o + 3] = 255;
      } else if (type === 4) {
        const g = sample(row, x * 2);
        data[o] = data[o + 1] = data[o + 2] = g;
        data[o + 3] = sample(row, x * 2 + 1);
      } else if (type === 2) {
        data[o] = sample(row, x * 3);
        data[o + 1] = sample(row, x * 3 + 1);
        data[o + 2] = sample(row, x * 3 + 2);
        data[o + 3] = 255;
      } else {
        data[o] = sample(row, x * 4);
        data[o + 1] = sample(row, x * 4 + 1);
        data[o + 2] = sample(row, x * 4 + 2);
        data[o + 3] = sample(row, x * 4 + 3);
      }
    }
  }
  return { width, height, data };
}

/** Width and height from the IHDR alone - no decoding. Null when not a PNG. */
export function pngSize(buf) {
  if (!buf || buf.length < 24 || !buf.subarray(0, 8).equals(SIGNATURE)) return null;
  if (buf.toString('latin1', 12, 16) !== 'IHDR') return null;
  return { width: buf.readUInt32BE(16), height: buf.readUInt32BE(20) };
}

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(buf) {
  let c = 0xffffffff;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length, 0);
  const body = Buffer.concat([Buffer.from(type, 'latin1'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body), 0);
  return Buffer.concat([len, body, crc]);
}

/** Encodes RGBA pixels (4 bytes a pixel, row after row) as an 8-bit PNG. */
export function encodePng(width, height, rgba) {
  if (rgba.length !== width * height * 4) throw new Error(`encodePng: ${rgba.length} bytes for ${width}x${height} RGBA`);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 6;
  const stride = width * 4;
  const raw = Buffer.alloc((stride + 1) * height);
  for (let y = 0; y < height; y++) {
    raw[y * (stride + 1)] = 0;
    Buffer.from(rgba.buffer, rgba.byteOffset + y * stride, stride).copy(raw, y * (stride + 1) + 1);
  }
  return Buffer.concat([SIGNATURE, chunk('IHDR', ihdr), chunk('IDAT', zlib.deflateSync(raw)), chunk('IEND', Buffer.alloc(0))]);
}

/**
 * Where two pictures differ.
 *
 *   tolerance  a pixel counts as different when any channel moved by MORE
 *              than this (0-255). Anti-aliasing jitter between two takes of
 *              the same screen stays well under the default.
 *   cell       the side of the squares the difference is gathered into; the
 *              squares that touch are merged into one box, so the report
 *              says "the toolbar and the third row" rather than listing
 *              every pixel.
 *
 * Returns `{ sameSize, pixels, total, boxes, mask }`: `pixels` that differ out
 * of `total`, the `boxes` ({ x, y, w, h, pixels }, largest first) and the
 * per-pixel `mask` (null when the sizes differ - then everything differs).
 */
export function diffImages(a, b, { tolerance = 24, cell = 32 } = {}) {
  const total = b.width * b.height;
  if (a.width !== b.width || a.height !== b.height) {
    return { sameSize: false, pixels: total, total, boxes: [{ x: 0, y: 0, w: b.width, h: b.height, pixels: total }], mask: null };
  }
  const { width, height } = b;
  const mask = new Uint8Array(total);
  const cols = Math.ceil(width / cell);
  const rows = Math.ceil(height / cell);
  const cellPixels = new Uint32Array(cols * rows);
  let pixels = 0;
  for (let i = 0; i < total; i++) {
    const o = i * 4;
    const d = Math.max(
      Math.abs(a.data[o] - b.data[o]),
      Math.abs(a.data[o + 1] - b.data[o + 1]),
      Math.abs(a.data[o + 2] - b.data[o + 2]),
      Math.abs(a.data[o + 3] - b.data[o + 3]),
    );
    if (d > tolerance) {
      mask[i] = 1;
      pixels++;
      const x = i % width;
      const y = (i - x) / width;
      cellPixels[Math.floor(y / cell) * cols + Math.floor(x / cell)]++;
    }
  }

  // Squares with a difference, merged with the squares they touch (8 ways).
  const seen = new Uint8Array(cols * rows);
  const boxes = [];
  for (let start = 0; start < cellPixels.length; start++) {
    if (!cellPixels[start] || seen[start]) continue;
    let minC = cols;
    let minR = rows;
    let maxC = -1;
    let maxR = -1;
    let count = 0;
    const stack = [start];
    seen[start] = 1;
    while (stack.length) {
      const k = stack.pop();
      const c = k % cols;
      const r = (k - c) / cols;
      count += cellPixels[k];
      minC = Math.min(minC, c);
      maxC = Math.max(maxC, c);
      minR = Math.min(minR, r);
      maxR = Math.max(maxR, r);
      for (let dr = -1; dr <= 1; dr++) {
        for (let dc = -1; dc <= 1; dc++) {
          const nr = r + dr;
          const nc = c + dc;
          if (nr < 0 || nc < 0 || nr >= rows || nc >= cols) continue;
          const n = nr * cols + nc;
          if (cellPixels[n] && !seen[n]) {
            seen[n] = 1;
            stack.push(n);
          }
        }
      }
    }
    const x = minC * cell;
    const y = minR * cell;
    boxes.push({ x, y, w: Math.min(width, (maxC + 1) * cell) - x, h: Math.min(height, (maxR + 1) * cell) - y, pixels: count });
  }
  boxes.sort((p, q) => q.pixels - p.pixels);
  return { sameSize: true, pixels, total, boxes, mask };
}

/**
 * The new picture faded, its changed pixels in red and every box outlined -
 * what the contact sheet shows beside the before and the after, so a person
 * sees at once WHERE a picture moved.
 */
export function diffOverlay(after, diff) {
  const { width, height } = after;
  const out = new Uint8Array(width * height * 4);
  for (let i = 0; i < width * height; i++) {
    const o = i * 4;
    if (diff.mask && diff.mask[i]) {
      out[o] = 230;
      out[o + 1] = 20;
      out[o + 2] = 40;
      out[o + 3] = 255;
    } else {
      const grey = 0.299 * after.data[o] + 0.587 * after.data[o + 1] + 0.114 * after.data[o + 2];
      const v = Math.round(255 - (255 - grey) * 0.3);
      out[o] = out[o + 1] = out[o + 2] = v;
      out[o + 3] = 255;
    }
  }
  const paint = (x, y) => {
    if (x < 0 || y < 0 || x >= width || y >= height) return;
    const o = (y * width + x) * 4;
    out[o] = 200;
    out[o + 1] = 0;
    out[o + 2] = 200;
    out[o + 3] = 255;
  };
  for (const box of diff.sameSize ? diff.boxes : []) {
    for (let t = 0; t < 2; t++) {
      for (let x = box.x - 2; x < box.x + box.w + 2; x++) {
        paint(x, box.y - 2 + t);
        paint(x, box.y + box.h + 1 - t);
      }
      for (let y = box.y - 2; y < box.y + box.h + 2; y++) {
        paint(box.x - 2 + t, y);
        paint(box.x + box.w + 1 - t, y);
      }
    }
  }
  return encodePng(width, height, out);
}
