// A staged upload whose bytes are made as they are sent (lib/uploadSource):
// read forward, the last range again on a retry, from inside it when the
// server's offset moved — and never backwards, because a stream cannot.

import { describe, expect, it } from 'vitest';

import { streamUploadSource, streamToFile } from '../../../packages/core/src/lib/uploadSource';
import { bytesStream } from '../../../packages/core/src/lib/e2estream';

function pattern(n: number, seed: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 31 + seed) & 0xff;
  return out;
}

async function bytesOf(b: Blob): Promise<Uint8Array> {
  return new Uint8Array(await b.arrayBuffer());
}

describe('streamUploadSource', () => {
  it('hands out the stream in the ranges asked for', async () => {
    const all = pattern(10_000, 1);
    const src = streamUploadSource(bytesStream(all, 333), all.length);
    expect(src.size).toBe(10_000);
    expect(await bytesOf(await src.read(0, 4000))).toEqual(all.slice(0, 4000));
    expect(await bytesOf(await src.read(4000, 8000))).toEqual(all.slice(4000, 8000));
    expect(await bytesOf(await src.read(8000, 10_000))).toEqual(all.slice(8000));
  });

  it('a retried range comes back the same; a start inside the last range works too', async () => {
    const all = pattern(9000, 2);
    const src = streamUploadSource(bytesStream(all, 1000), all.length);
    await src.read(0, 3000);
    expect(await bytesOf(await src.read(0, 3000))).toEqual(all.slice(0, 3000));
    expect(await bytesOf(await src.read(1200, 6000))).toEqual(all.slice(1200, 6000));
    expect(await bytesOf(await src.read(6000, 9000))).toEqual(all.slice(6000));
  });

  it('refuses to rewind, to skip ahead, or to run past the end', async () => {
    const all = pattern(5000, 3);
    const src = streamUploadSource(bytesStream(all), all.length);
    await src.read(0, 2000);
    await src.read(2000, 4000);
    await expect(src.read(1000, 2000)).rejects.toThrow(/cannot read/);
    await expect(src.read(4500, 5000)).rejects.toThrow(/cannot read/);
    await expect(src.read(4000, 6000)).rejects.toThrow(/cannot read/);
  });

  it('a stream shorter than it promised is an error, not a short upload', async () => {
    const src = streamUploadSource(bytesStream(pattern(100, 4)), 200);
    await expect(src.read(0, 200)).rejects.toThrow(/ended early/);
  });

  it('streamToFile gathers a small stream into a File', async () => {
    const all = pattern(1234, 5);
    const f = await streamToFile(bytesStream(all, 100), 'x.bin');
    expect(f.name).toBe('x.bin');
    expect(await bytesOf(f)).toEqual(all);
  });
});
