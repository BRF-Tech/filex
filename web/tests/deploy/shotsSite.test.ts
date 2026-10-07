// The published screenshots (task #176): taken only when what they show
// changed, compared by their pixels, published on filex.sh under names that
// carry their content hash, and linked from every page by exactly those URLs.
//
// ⚠⚠ Why these are tests. Every rule below fails SILENTLY when it breaks:
//
//   · a scene skipped although something it shows changed keeps a stale
//     picture in the README - wrong information, not missing information;
//   · a picture counted "changed" on byte noise puts the same 150 pictures in
//     front of a person every release, which is what this exists to stop;
//   · a README link to a file nobody published is a broken image in the
//     public repository, exported from this tree as it stands;
//   · a site deploy that does not spare /shots/ deletes every published
//     picture, and every older README a tag or a fork still shows.
//
// They need no browser, no build and no network: the rules live in
// scripts/lib/shots-site.mjs and scripts/lib/png.mjs, and the network is a
// function they are handed.

import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import zlib from 'node:zlib';
import { afterAll, describe, expect, it } from 'vitest';

import { decodePng, diffImages, diffOverlay, encodePng, pngSize } from '../../../scripts/lib/png.mjs';
import { findShotScripts } from '../../../scripts/lib/shot-scripts.mjs';
import {
  DIFF_DEFAULTS,
  MANIFEST_REL,
  PUBLISH_ENVIRONMENT,
  PUBLISH_PLATFORM,
  SCENE_DATA,
  SHARED_INPUTS,
  SHOTS_SITE_BASE,
  acceptRefusal,
  baseTokens,
  decideScenes,
  diffThresholds,
  environmentNote,
  fileHasher,
  findReferences,
  isChanged,
  localImports,
  manifestProblems,
  nextManifest,
  parsePublishedUrl,
  productInputs,
  publishedName,
  publishedUrl,
  readManifest,
  referenceFiles,
  relinkRepo,
  relinkText,
  sceneDigest,
  sceneOfName,
  scriptInputs,
  serializeManifest,
  sha256,
  shotSets,
  urlOf,
  verifyPublished,
} from '../../../scripts/lib/shots-site.mjs';
import { bashArray } from '../helpers/exporterArrays';

const REPO = path.resolve(__dirname, '..', '..', '..');
const SHOTS_DIR = path.join(REPO, 'e2e', 'shots');
const SHA_A = 'a1'.repeat(32);
const SHA_B = 'b2'.repeat(32);
const SHA_C = 'c3'.repeat(32);

// ─── PNG: what the comparison reads ─────────────────────────────────────────

type Image = { width: number; height: number; data: Uint8Array };

function image(width: number, height: number, at: (x: number, y: number) => [number, number, number, number]): Image {
  const data = new Uint8Array(width * height * 4);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) data.set(at(x, y), (y * width + x) * 4);
  }
  return { width, height, data };
}

/** A PNG chunk with a zero CRC: the decoder does not check it, and the test
 *  should not need a second CRC implementation to build its input. */
function chunk(type: string, data: Buffer): Buffer {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length, 0);
  return Buffer.concat([len, Buffer.from(type, 'latin1'), data, Buffer.alloc(4)]);
}

const paeth = (a: number, b: number, c: number) => {
  const pa = Math.abs(b - c);
  const pb = Math.abs(a - c);
  const pc = Math.abs(a + b - 2 * c);
  return pa <= pb && pa <= pc ? a : pb <= pc ? b : c;
};

/** Filters one scanline the way an encoder would (the inverse of the decoder). */
function filterRow(type: number, cur: Buffer, prev: Buffer, bpp: number): Buffer {
  const out = Buffer.alloc(cur.length + 1);
  out[0] = type;
  for (let i = 0; i < cur.length; i++) {
    const a = i >= bpp ? cur[i - bpp] : 0;
    const b = prev[i];
    const c = i >= bpp ? prev[i - bpp] : 0;
    const pred = [0, a, b, (a + b) >> 1, paeth(a, b, c)][type];
    out[i + 1] = (cur[i] - pred) & 0xff;
  }
  return out;
}

function rawPng({ width, height, depth, colour, rows, plte, trns, interlace = 0 }: {
  width: number;
  height: number;
  depth: number;
  colour: number;
  rows: Buffer[];
  plte?: Buffer;
  trns?: Buffer;
  interlace?: number;
}): Buffer {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = depth;
  ihdr[9] = colour;
  ihdr[12] = interlace;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    ...(plte ? [chunk('PLTE', plte)] : []),
    ...(trns ? [chunk('tRNS', trns)] : []),
    chunk('IDAT', zlib.deflateSync(Buffer.concat(rows))),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

describe('reading a PNG', () => {
  it('reads back what it wrote, and the size from the header alone', () => {
    const img = image(7, 5, (x, y) => [x * 30, y * 50, (x + y) * 10, 200 + x]);
    const png = encodePng(img.width, img.height, img.data);
    const back = decodePng(png);
    expect(back.width).toBe(7);
    expect(back.height).toBe(5);
    expect(Buffer.from(back.data).equals(Buffer.from(img.data))).toBe(true);
    expect(pngSize(png)).toEqual({ width: 7, height: 5 });
    expect(pngSize(Buffer.from('not a png at all, really not'))).toBeNull();
  });

  it('undoes all five row filters of an 8-bit RGB picture (what Chromium writes)', () => {
    const width = 4;
    const pixel = (x: number, y: number) => [(x * 61 + y * 17) & 255, (x * 13 + y * 97) & 255, (x * 7 + y * 29 + 3) & 255];
    const rows: Buffer[] = [];
    let prev = Buffer.alloc(width * 3);
    for (let y = 0; y < 5; y++) {
      const cur = Buffer.from(Array.from({ length: width }, (_, x) => pixel(x, y)).flat());
      rows.push(filterRow(y, cur, prev, 3));
      prev = cur;
    }
    const back = decodePng(rawPng({ width, height: 5, depth: 8, colour: 2, rows }));
    for (let y = 0; y < 5; y++) {
      for (let x = 0; x < width; x++) {
        const o = (y * width + x) * 4;
        expect([...back.data.slice(o, o + 4)], `pixel ${x},${y} after filter ${y}`).toEqual([...pixel(x, y), 255]);
      }
    }
  });

  it('reads a 2-bit palette with transparency, and 16-bit grey', () => {
    const plte = Buffer.from([10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120]);
    const trns = Buffer.from([0, 128]);
    // four pixels in one byte: indexes 0, 1, 2, 3
    const pal = decodePng(rawPng({ width: 4, height: 1, depth: 2, colour: 3, rows: [Buffer.from([0, 0b00011011])], plte, trns }));
    expect([...pal.data]).toEqual([10, 20, 30, 0, 40, 50, 60, 128, 70, 80, 90, 255, 100, 110, 120, 255]);
    const grey = decodePng(rawPng({ width: 2, height: 1, depth: 16, colour: 0, rows: [Buffer.from([0, 0x12, 0x34, 0xfe, 0xdc])] }));
    expect([...grey.data]).toEqual([0x12, 0x12, 0x12, 255, 0xfe, 0xfe, 0xfe, 255]);
  });

  it('refuses what it cannot read, by name, instead of guessing', () => {
    expect(() => decodePng(Buffer.from('GIF89a and so on'))).toThrow(/not a PNG/);
    const rows = [Buffer.from([0, 1, 2, 3])];
    expect(() => decodePng(rawPng({ width: 1, height: 1, depth: 8, colour: 2, rows, interlace: 1 }))).toThrow(/interlaced/);
  });
});

describe('telling two pictures apart', () => {
  const grey = image(64, 64, () => [200, 200, 200, 255]);
  const touched = (points: Array<[number, number]>, delta: number) => {
    const img = image(64, 64, () => [200, 200, 200, 255]);
    for (const [x, y] of points) img.data[(y * 64 + x) * 4] = 200 - delta;
    return img;
  };

  it('finds nothing between a picture and itself', () => {
    const d = diffImages(grey, grey);
    expect(d.pixels).toBe(0);
    expect(d.boxes).toEqual([]);
  });

  it('counts a pixel that moved past the tolerance, and not one that jittered under it', () => {
    expect(diffImages(grey, touched([[3, 3]], 100)).pixels).toBe(1);
    expect(diffImages(grey, touched([[3, 3]], DIFF_DEFAULTS.tolerance)).pixels).toBe(0);
    expect(diffImages(grey, touched([[3, 3]], DIFF_DEFAULTS.tolerance + 1)).pixels).toBe(1);
  });

  it('gathers what moved into regions, largest first', () => {
    const d = diffImages(grey, touched([[1, 1], [2, 1], [60, 60], [61, 60], [60, 61]], 100), { cell: 16 });
    expect(d.pixels).toBe(5);
    expect(d.boxes).toHaveLength(2);
    expect(d.boxes[0]).toMatchObject({ x: 48, y: 48, pixels: 3 });
    expect(d.boxes[1]).toMatchObject({ x: 0, y: 0, w: 16, h: 16, pixels: 2 });
  });

  it('calls a picture of another size different everywhere', () => {
    const d = diffImages(grey, image(32, 64, () => [200, 200, 200, 255]));
    expect(d.sameSize).toBe(false);
    expect(d.pixels).toBe(d.total);
    expect(isChanged(d)).toBe(true);
  });

  it('changed means more than the threshold of pixels, overridable per run', () => {
    expect(isChanged({ sameSize: true, pixels: DIFF_DEFAULTS.pixels })).toBe(false);
    expect(isChanged({ sameSize: true, pixels: DIFF_DEFAULTS.pixels + 1 })).toBe(true);
    expect(diffThresholds({ SHOTS_DIFF_TOLERANCE: '8', SHOTS_DIFF_PIXELS: '0' })).toEqual({ tolerance: 8, pixels: 0 });
    expect(diffThresholds({ SHOTS_DIFF_TOLERANCE: 'lots', SHOTS_DIFF_PIXELS: '-3' })).toEqual(DIFF_DEFAULTS);
  });

  it('draws where it moved, over the new picture', () => {
    const after = touched([[10, 10]], 100);
    const overlay = decodePng(diffOverlay(after, diffImages(grey, after)));
    expect([overlay.width, overlay.height]).toEqual([64, 64]);
    const o = (10 * 64 + 10) * 4;
    expect([...overlay.data.slice(o, o + 3)]).toEqual([230, 20, 40]);
  });
});

// ─── names and links ────────────────────────────────────────────────────────

describe('a published name carries its content hash', () => {
  it('puts the first 12 hex digits of the sha256 before .png', () => {
    expect(publishedName('sidenav/sidenav-rail-1440.png', SHA_A)).toBe(`sidenav/sidenav-rail-1440.${SHA_A.slice(0, 12)}.png`);
    expect(publishedUrl(SHOTS_SITE_BASE, 'share-modal.png', SHA_B)).toBe(`https://filex.sh/shots/share-modal.${SHA_B.slice(0, 12)}.png`);
  });

  it('refuses a name that could leave the folder, or a hash that is not one', () => {
    expect(() => publishedName('../x.png', SHA_A)).toThrow();
    expect(() => publishedName('set/x.jpg', SHA_A)).toThrow();
    expect(() => publishedName('x.png', 'abc')).toThrow();
  });

  it('reads a URL back to the picture it shows', () => {
    const url = publishedUrl(SHOTS_SITE_BASE, 'store/store-review-1440.png', SHA_C);
    expect(parsePublishedUrl(url)).toEqual({ name: 'store/store-review-1440.png', short: SHA_C.slice(0, 12) });
    expect(parsePublishedUrl('https://filex.sh/shots/a.png')).toEqual({ name: 'a.png', short: null });
    expect(parsePublishedUrl('https://example.com/shots/a.png')).toBeNull();
  });
});

const FIXTURE = {
  about: '',
  base: SHOTS_SITE_BASE,
  platform: 'win32',
  pictures: {
    'a.png': { sha256: SHA_A, width: 10, height: 10, bytes: 100, scene: 'capture.mjs', taken: '2026-10-05T10:00:00Z' },
    'set/b.png': { sha256: SHA_B, width: 10, height: 10, bytes: 100, scene: 'set.mjs', taken: '2026-10-05T10:00:00Z' },
  },
  scenes: {},
};
const URL_A = publishedUrl(SHOTS_SITE_BASE, 'a.png', SHA_A);
const URL_B = publishedUrl(SHOTS_SITE_BASE, 'set/b.png', SHA_B);

describe('every way a page used to show a picture becomes the current URL', () => {
  const text = [
    '![a](docs/screenshots/v0.52.0/a.png)',
    '<img src="./docs/screenshots/v0.51.0/set/b.png" width="10">',
    '| ![b](screenshots/v0.52.0/set/b.png) |',
    '  - https://raw.githubusercontent.com/BRF-Tech/filex/main/docs/screenshots/v0.52.0/a.png',
    `![old](https://filex.sh/shots/a.${'0'.repeat(12)}.png) and ![current](${URL_B})`,
    '![up](../screenshots/v0.1.0/a.png)',
    '![gone](docs/screenshots/v0.52.0/gone.png)',
  ].join('\n');

  it('finds each reference once - the raw.githubusercontent link is one, not two', () => {
    const refs = findReferences(text);
    expect(refs.map((r: { kind: string; name: string }) => `${r.kind}:${r.name}`)).toEqual([
      'legacy:a.png',
      'legacy:set/b.png',
      'legacy:set/b.png',
      'legacy:a.png',
      'published:a.png',
      'published:set/b.png',
      'legacy:a.png',
      'legacy:gone.png',
    ]);
  });

  it('relinks to the manifest URL, and names what it cannot point anywhere', () => {
    const r = relinkText(text, FIXTURE);
    expect(r.text.replace('docs/screenshots/v0.52.0/gone.png', '')).not.toMatch(/screenshots\/v\d/);
    expect(r.text.split(URL_A).length - 1).toBe(4);
    expect(r.text.split(URL_B).length - 1).toBe(3);
    expect(r.changes).toHaveLength(6);
    expect(r.unresolved).toEqual([{ line: 7, ref: 'docs/screenshots/v0.52.0/gone.png', why: `gone.png is not in ${MANIFEST_REL}` }]);
    expect(r.text).toContain('docs/screenshots/v0.52.0/gone.png');
    expect(relinkText(r.text, FIXTURE).changes).toEqual([]);
  });

  it("on the site page, only a screenshot's own file is relinked, never the page's own pictures", () => {
    const page = '<img src="assets/a.png"><meta content="https://filex.sh/assets/social-preview.png"><img src="assets/b.png">';
    expect(relinkText(page, FIXTURE, { siteAssets: true }).text).toBe(
      `<img src="${URL_A}"><meta content="https://filex.sh/assets/social-preview.png"><img src="assets/b.png">`,
    );
    expect(relinkText(page, FIXTURE).text).toBe(page);
  });
});

// ─── which scenes a run takes ───────────────────────────────────────────────

describe('a scene is taken again only when what it reads changed', () => {
  const scripts = ['a.mjs', 'b.mjs', 'c.mjs', 'd.mjs', 'e.mjs'];
  const digests = new Map([['a.mjs', 'D1'], ['b.mjs', 'D2'], ['c.mjs', 'D3'], ['d.mjs', 'D4'], ['e.mjs', 'D5']]);
  const manifest = { ...FIXTURE, scenes: { 'a.mjs': { digest: 'D1' }, 'b.mjs': { digest: 'OLD' }, 'c.mjs': { digest: null } } };
  const previous = { scenes: { 'd.mjs': { action: 'shot', status: 'passed', digest: 'D4', pictures: [] } }, pictures: {} };
  const excluded = new Map([['e.mjs', 'needs the sign app build']]);
  const actions = (opts: Record<string, unknown>) =>
    Object.fromEntries(
      [...decideScenes({ scripts, digests, manifest, previous, excluded, keptOk: () => true, ...opts })].map(([f, p]: [string, { action: string }]) => [f, p.action]),
    );

  it('incremental: published and kept scenes are skipped, the rest are taken', () => {
    expect(actions({})).toEqual({ 'a.mjs': 'published', 'b.mjs': 'shoot', 'c.mjs': 'shoot', 'd.mjs': 'kept', 'e.mjs': 'left-out' });
  });

  it('--all takes every scene that can be taken', () => {
    expect(actions({ mode: 'all' })).toEqual({ 'a.mjs': 'shoot', 'b.mjs': 'shoot', 'c.mjs': 'shoot', 'd.mjs': 'shoot', 'e.mjs': 'left-out' });
  });

  it('--only takes the named ones; the rest stand as published or kept, or are not asked', () => {
    expect(actions({ mode: 'only', only: ['b'] })).toEqual({ 'a.mjs': 'published', 'b.mjs': 'shoot', 'c.mjs': 'not-asked', 'd.mjs': 'kept', 'e.mjs': 'left-out' });
  });

  it("the last run's pictures stand only when they are still there, and only after a pass", () => {
    expect(actions({ keptOk: () => false })['d.mjs']).toBe('shoot');
    const failed = { scenes: { 'd.mjs': { ...previous.scenes['d.mjs'], status: 'failed' } }, pictures: {} };
    expect(actions({ previous: failed })['d.mjs']).toBe('shoot');
    const moved = new Map(digests).set('d.mjs', 'D4b');
    expect(actions({ digests: moved })['d.mjs']).toBe('shoot');
  });
});

describe("a scene's digest is what has to change for its pictures to change", () => {
  const root = mkdtempSync(path.join(tmpdir(), 'filex-shots-digest-'));
  afterAll(() => rmSync(root, { recursive: true, force: true }));
  const put = (rel: string, text: string) => {
    mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
    writeFileSync(path.join(root, rel), text);
  };
  put('e2e/shots/x.mjs', "import { chromium } from '@playwright/test';\nimport { shot } from './scene.mjs';\nimport { SHOTS_RELEASE } from './release.mjs';\n");
  put('e2e/shots/scene.mjs', "import { help } from '../helpers/h.mjs';\nexport const shot = 1;\n");
  put('e2e/helpers/h.mjs', 'export const help = 1;\n');
  put('e2e/shots/release.mjs', "export const SHOTS_RELEASE = 'v1.0.0';\n");
  put('e2e/fixtures/f.txt', 'fixture\n');
  put('web/src/a.vue', '<template>a</template>\n');
  put('web/src/b.vue', '<template>b</template>\n');
  put('web/src/a.test.ts', 'test\n');
  put('backend/x.go', 'package x\n');

  const walk = (rel: string): string[] => {
    const abs = path.join(root, rel);
    if (!existsSync(abs)) return [];
    if (statSync(abs).isFile()) return [rel];
    return readdirSync(abs).flatMap((n) => walk(`${rel}/${n}`));
  };
  const filesUnder = (prefixes: string[]) => prefixes.flatMap(walk).sort();
  const digest = (opts: Record<string, unknown> = {}) =>
    sceneDigest({ repo: root, script: 'x.mjs', hash: fileHasher(root), filesUnder, ...opts });

  it('follows the imports, and leaves release.mjs to the version token', () => {
    expect(localImports(root, 'e2e/shots/x.mjs')).toEqual(['e2e/helpers/h.mjs', 'e2e/shots/release.mjs', 'e2e/shots/scene.mjs', 'e2e/shots/x.mjs']);
    const before = digest();
    put('e2e/shots/release.mjs', "export const SHOTS_RELEASE = 'v1.0.0'; // a comment\n");
    expect(digest()).toBe(before);
  });

  it('moves with the scene, its modules, its fixtures and the product; not with a test file', () => {
    const before = digest();
    put('web/src/a.test.ts', 'another test\n');
    expect(digest()).toBe(before);
    for (const rel of ['e2e/helpers/h.mjs', 'e2e/fixtures/f.txt', 'web/src/a.vue', 'backend/x.go', 'e2e/shots/x.mjs']) {
      const was = digest();
      put(rel, `${readFileSync(path.join(root, rel), 'utf8')}// changed\n`);
      expect(digest(), `${rel} changed and the digest did not`).not.toBe(was);
    }
  });

  it('with declared INPUTS, moves with those and not with the rest of the product', () => {
    const declared = ['web/src/b.vue'];
    const before = digest({ declared });
    put('web/src/a.vue', '<template>a, again</template>\n');
    expect(digest({ declared })).toBe(before);
    put('web/src/b.vue', '<template>b, again</template>\n');
    expect(digest({ declared })).not.toBe(before);
  });

  it('carries the locale, the platform, the version and what the caller adds', () => {
    expect(baseTokens({ platform: 'linux', release: 'v9.9.9' })).toEqual(['locale:en-US', 'platform:linux', 'version:v9.9.9']);
    expect(baseTokens({ platform: 'linux', release: 'v9.9.9', declared: ['web/src'] })).toEqual(['locale:en-US', 'platform:linux']);
    expect(baseTokens({ platform: 'win32', release: 'v9.9.9', declared: ['@version'] })).toContain('version:v9.9.9');
    expect(digest({ tokens: ['app:sign:1'] })).not.toBe(digest({ tokens: ['app:sign:2'] }));
  });
});

// ─── what a reviewed run becomes ────────────────────────────────────────────

describe('a reviewed run becomes the published set', () => {
  const T = '2026-10-07T03:00:00.000Z';
  const manifest = {
    ...FIXTURE,
    pictures: {
      ...FIXTURE.pictures,
      'gone.png': { sha256: SHA_C, width: 1, height: 1, bytes: 1, scene: 'capture.mjs', taken: '2026-01-01T00:00:00Z' },
    },
    scenes: { 'capture.mjs': { digest: 'OLD' }, 'set.mjs': { digest: 'S' }, 'old.mjs': { digest: 'X' } },
  };
  const review = {
    when: T,
    platform: 'win32',
    mode: 'incremental',
    complete: false,
    failure: null,
    pictures: {
      'a.png': { status: 'same', sha256: SHA_C, scene: 'capture.mjs' },
      'set/b.png': { status: 'changed', sha256: SHA_C, width: 20, height: 20, bytes: 200, scene: 'set.mjs' },
      'new.png': { status: 'new', sha256: SHA_A, width: 5, height: 5, bytes: 50, scene: 'capture.mjs' },
    },
    removed: ['gone.png'],
    scenes: {
      'capture.mjs': { action: 'shot', status: 'passed', digest: 'C2' },
      'set.mjs': { action: 'shot', status: 'failed', digest: 'S2' },
      'kept.mjs': { action: 'kept', status: null, digest: 'K' },
      'pub.mjs': { action: 'published', status: null, digest: 'P' },
    },
  };

  it('new and changed take their file, removed ones go, unchanged ones keep the published file', () => {
    const next = nextManifest(manifest, review);
    expect(next.pictures['a.png']).toEqual({ ...FIXTURE.pictures['a.png'], checked: T });
    expect(next.pictures['set/b.png']).toEqual({ sha256: SHA_C, width: 20, height: 20, bytes: 200, scene: 'set.mjs', taken: T, checked: T });
    expect(next.pictures['new.png'].sha256).toBe(SHA_A);
    expect(next.pictures['gone.png']).toBeUndefined();
  });

  it('records the digest of every scene taken with a pass, or kept; a failed one keeps the old', () => {
    const next = nextManifest(manifest, review);
    expect(next.scenes).toEqual({ 'capture.mjs': { digest: 'C2' }, 'set.mjs': { digest: 'S' }, 'old.mjs': { digest: 'X' }, 'kept.mjs': { digest: 'K' } });
    expect(nextManifest(manifest, { ...review, complete: true }).scenes['old.mjs']).toBeUndefined();
  });

  it('refuses a failed run, and a partial run on another platform (two typefaces in one README)', () => {
    expect(acceptRefusal(manifest, null)).toMatch(/no run to accept/);
    expect(acceptRefusal(manifest, { ...review, failure: 'x.mjs failed' })).toMatch(/the run failed/);
    // The manifest's set came from Windows: Linux replaces it only whole.
    expect(acceptRefusal(manifest, { ...review, platform: 'linux' })).toMatch(/two typefaces/);
    expect(acceptRefusal(manifest, { ...review, platform: 'linux', mode: 'all', complete: true })).toBe('');
    expect(acceptRefusal({ ...manifest, platform: null }, { ...review, platform: 'linux' })).toBe('');
    expect(acceptRefusal({ ...manifest, platform: 'linux' }, { ...review, platform: 'linux' })).toBe('');
  });

  it('takes the published set on Linux, the build host, and refuses a run from anywhere else, partial or whole (#176)', () => {
    // ⚠ The owner's decision of 2026-10-06. Until then a manifest with no
    // platform took a partial run from any system, and the first accept chose
    // the typeface of every picture after it: a Windows workstation's partial
    // set beside the build host's pictures is a README in two typefaces.
    expect(PUBLISH_PLATFORM).toBe('linux');
    const fresh = { ...manifest, platform: null };
    expect(acceptRefusal(fresh, review)).toMatch(/taken on win32, and the published set is taken on linux/);
    expect(acceptRefusal(manifest, review)).toMatch(/taken on win32, and the published set is taken on linux/);
    expect(acceptRefusal({ ...manifest, platform: 'linux' }, { ...review, mode: 'all', complete: true })).toMatch(/published set is taken on linux/);
    expect(acceptRefusal({ ...manifest, platform: 'linux' }, { ...review, platform: 'darwin' })).toMatch(/taken on darwin/);
    // Where the set is taken is one setting, not a rule spread over the code.
    expect(acceptRefusal(fresh, review, { publishPlatform: 'win32' })).toBe('');
  });

  it("refuses a run taken off the scenes' clock (SHOTS_REAL_CLOCK=1): its pictures carry the day it ran", () => {
    const linux = { ...manifest, platform: 'linux' };
    expect(acceptRefusal(linux, { ...review, platform: 'linux', clock: 'real' })).toMatch(/real clock/);
    expect(acceptRefusal(linux, { ...review, platform: 'linux', clock: 'scene' })).toBe('');
    expect(readFileSync(path.join(REPO, 'scripts', 'shots.mjs'), 'utf8')).toContain("clock: process.env.SHOTS_REAL_CLOCK === '1' ? 'real' : 'scene',");
  });

  it('stages nothing for the site from a run on another system, and says why', () => {
    const shots = readFileSync(path.join(REPO, 'scripts', 'shots.mjs'), 'utf8');
    expect(shots).toContain('const publishable = process.platform === PUBLISH_PLATFORM;');
    expect(shots).toMatch(/if \(!publishable\) break;/);
    expect(shots).toMatch(/staged for the site: nothing - this run is on/);
  });

  it('the nightly run takes every scene, on Linux (scripts/chain/job/shots.sh)', () => {
    // --all: a scene whose INPUTS missed something it shows keeps its digest,
    // and only taking it again finds its pixels moved (lesson #1150).
    const job = readFileSync(path.join(REPO, 'scripts', 'chain', 'job', 'shots.sh'), 'utf8');
    expect(job).toMatch(/^SHOTS_ENVIRONMENT=chain node scripts\/shots\.mjs --all /m);
    expect(job).toContain('--without-apps');
  });

  it("takes the published set in the build host's test chain; elsewhere on Linux it warns, it does not refuse (2026-10-06)", () => {
    // ⚠ The 0.52.0 set reads in DejaVu Sans, the build host's own face; the
    // chain's Playwright container sets the same pages in Liberation Sans
    // (scripts/chain/run.mjs FONTS_CONF). Both are "linux": the platform rule
    // cannot tell them apart, the environment does. A warning, not a refusal:
    // the app and Document Server scenes are still taken on the host.
    expect(PUBLISH_ENVIRONMENT).toBe('chain');
    const linux = { ...manifest, platform: 'linux', environment: 'chain' };
    expect(environmentNote(linux, { ...review, platform: 'linux', environment: 'chain' })).toBe('');
    expect(environmentNote(linux, { ...review, platform: 'linux', environment: 'local' })).toMatch(/taken in "local", and the published set in "chain".*typeface/);
    expect(environmentNote(linux, { ...review, platform: 'linux' })).toMatch(/taken in "local"/);
    expect(acceptRefusal(linux, { ...review, platform: 'linux', environment: 'local' })).toBe('');
    expect(nextManifest(linux, { ...review, platform: 'linux', environment: 'chain' }).environment).toBe('chain');
    expect(readFileSync(path.join(REPO, 'scripts', 'shots.mjs'), 'utf8')).toContain("environment: process.env.SHOTS_ENVIRONMENT || 'local',");
    expect(readFileSync(path.join(REPO, 'scripts', 'shots-site.mjs'), 'utf8')).toContain('const note = environmentNote(m, review);');
    const runMjs = readFileSync(path.join(REPO, 'scripts', 'chain', 'run.mjs'), 'utf8');
    expect(runMjs).toMatch(/<family>sans-serif<\/family><prefer><family>Liberation Sans<\/family>/);
    expect(runMjs).toMatch(/shots: \{ image: 'pw', script: 'shots\.sh'[^}]*browser: true/);
  });

  it('serializes in one stable form', () => {
    const a = serializeManifest(nextManifest(manifest, review));
    const shuffled = nextManifest({ ...manifest, pictures: Object.fromEntries(Object.entries(manifest.pictures).reverse()) }, review);
    expect(serializeManifest(shuffled)).toBe(a);
    expect(a.endsWith('}\n')).toBe(true);
  });
});

describe('reading a published picture back', () => {
  const bytes = Buffer.from('the published bytes');
  const answer = (status: number, body: Buffer) => async () =>
    ({ ok: status === 200, status, arrayBuffer: async () => body.buffer.slice(body.byteOffset, body.byteOffset + body.length) }) as unknown as Response;

  it('passes only a 200 with exactly the bytes the manifest names', async () => {
    const want = sha256(bytes);
    expect(await verifyPublished([{ url: 'https://filex.sh/shots/a.png', sha256: want }], { fetchImpl: answer(200, bytes) })).toEqual([
      { url: 'https://filex.sh/shots/a.png', ok: true, why: '' },
    ]);
    const [other] = await verifyPublished([{ url: 'u', sha256: want }], { fetchImpl: answer(200, Buffer.from('other')) });
    expect(other.ok).toBe(false);
    expect(other.why).toMatch(/serves other bytes/);
    const [missing] = await verifyPublished([{ url: 'u', sha256: want }], { fetchImpl: answer(404, Buffer.alloc(0)) });
    expect(missing).toEqual({ url: 'u', ok: false, why: 'HTTP 404' });
  });
});

// ─── this repository ────────────────────────────────────────────────────────

describe('this repository shows exactly the published pictures', () => {
  const manifest = readManifest(path.join(REPO, MANIFEST_REL));
  const scripts = findShotScripts(SHOTS_DIR).scripts as string[];
  const names = Object.keys(manifest.pictures);

  it('the manifest is well-formed, and written by the tool (its one stable form)', () => {
    expect(manifest.base).toBe(SHOTS_SITE_BASE);
    // The published set was taken on Linux (the 0.52.0 set: DejaVu Sans, the
    // build host's face), and is taken there from now on.
    expect(manifest.platform).toBe(PUBLISH_PLATFORM);
    // ...in the chain's Playwright container and fontconfig (scripts/chain/job/shots.sh).
    expect(manifest.environment).toBe(PUBLISH_ENVIRONMENT);
    expect(names.length, 'the manifest holds next to no pictures - the checks below would compare nothing').toBeGreaterThan(100);
    expect(manifestProblems(manifest, { scripts })).toEqual([]);
    expect(readFileSync(path.join(REPO, MANIFEST_REL), 'utf8').replace(/\r\n/g, '\n')).toBe(serializeManifest(manifest));
  });

  it("every picture sits in the folder of the scene that takes it", () => {
    const sets = shotSets(SHOTS_DIR, scripts);
    const wrong = names.filter((n) => sceneOfName(sets, n) !== manifest.pictures[n].scene);
    expect(wrong, 'a picture attributed to a scene that does not write its folder').toEqual([]);
  });

  it('every page links the current published file - no repository path, no older hash, no unknown picture', () => {
    const files = relinkRepo(REPO, manifest);
    expect(files.length).toBeGreaterThan(20);
    const stale = files.flatMap((f: { file: string; changes: Array<{ line: number; from: string }> }) => f.changes.map((c) => `${f.file}:${c.line} ${c.from}`));
    const unresolved = files.flatMap((f: { file: string; unresolved: Array<{ line: number; ref: string }> }) => f.unresolved.map((u) => `${f.file}:${u.line} ${u.ref}`));
    expect(stale, 'run: node scripts/shots-site.mjs relink --write (after the pictures are published)').toEqual([]);
    expect(unresolved, `a page shows a picture ${MANIFEST_REL} does not hold`).toEqual([]);
  });

  it('the README shows its pictures from the site, each one the manifest names', () => {
    const refs = findReferences(readFileSync(path.join(REPO, 'README.md'), 'utf8'), { base: manifest.base });
    expect(refs.length).toBeGreaterThan(20);
    for (const r of refs) {
      expect(r.kind, `${r.raw} is not a published URL`).toBe('published');
      expect(r.raw).toBe(urlOf(manifest, r.name));
    }
  });

  it('the reference scan covers the README, its translations, the docs, the site and the store manifests', () => {
    const files = referenceFiles(REPO);
    for (const f of ['README.md', 'README.tr.md', 'docs/APP-PLUGINS.md', 'deploy/casaos/docker-compose.yml']) expect(files).toContain(f);
    expect(files.some((f: string) => f.startsWith('docs/handovers/'))).toBe(false);
  });

  it('no screenshot is kept in the repository any more', () => {
    const tracked = execFileSync('git', ['-C', REPO, 'ls-files', '--', 'docs/screenshots', 'e2e/.artifacts'], { encoding: 'utf8' }).trim();
    expect(tracked, 'the pictures live on filex.sh (e2e/shots/README.md); a folder of them in git is ~40 MB a release').toBe('');
    expect(readFileSync(path.join(REPO, '.gitignore'), 'utf8')).toMatch(/^e2e\/\.artifacts\/$/m);
  });

  it('everything a digest reads from exists - a renamed folder would make every scene look unchanged', () => {
    for (const p of [...SCENE_DATA, ...SHARED_INPUTS, ...productInputs(REPO)]) expect(existsSync(path.join(REPO, p)), p).toBe(true);
    for (const f of scripts) {
      for (const p of (scriptInputs(SHOTS_DIR, f) ?? []).filter((x: string) => !x.startsWith('@'))) {
        expect(existsSync(path.join(REPO, p)), `e2e/shots/${f} declares INPUTS ${p}, which is not there`).toBe(true);
      }
    }
  });

  it('the release audit reads every linked picture back before anything is exported', () => {
    const plan = readFileSync(path.join(REPO, 'scripts', 'release', 'plan.mjs'), 'utf8');
    expect(plan).toContain("cmd: ['node', 'scripts/shots-site.mjs', 'verify', '--live']");
  });
});

// The publishing side lives in the maintainers' checkout only; the public tree
// has neither script, and says so (siteAssets.test.ts tells the trees apart
// the same way).
const SYNC_SITE = path.join(REPO, 'scripts', 'sync-site.sh');
const UPLOAD = path.join(REPO, 'scripts', 'shots-upload.sh');
const EXPORTER = path.join(REPO, 'scripts', 'export-public.sh');
const inSource = existsSync(EXPORTER);

it('the publishing scripts are in this checkout exactly when it is the source tree', () => {
  expect(existsSync(SYNC_SITE)).toBe(inSource);
  expect(existsSync(UPLOAD)).toBe(inSource);
});

describe.skipIf(!inSource)('publishing never takes a published picture away', () => {
  it('the site deploy spares /shots/ like /desktop/ and /updates/, and stops on a planned deletion there', () => {
    const text = readFileSync(SYNC_SITE, 'utf8');
    expect(text).toContain('--exclude=/shots/');
    expect(text).toContain('updates\\|shots\\)');
    expect(text).toMatch(/if \[ -d '\$TARGET'\/shots \]/);
  });

  it('the upload adds files and never overwrites or deletes one', () => {
    const text = readFileSync(UPLOAD, 'utf8');
    expect(text).toContain('--skip-old-files');
    expect(text).not.toMatch(/\brm\s+-|--delete|rsync/);
    expect(text).toMatch(/\[0-9a-f\]\{12\}\\\.png\$/);
  });

  it('the upload script is withheld from the public export, like sync-site.sh', () => {
    const files = bashArray(readFileSync(EXPORTER, 'utf8'), 'private_files');
    expect(files).toContain('scripts/sync-site.sh');
    expect(files).toContain('scripts/shots-upload.sh');
  });
});
