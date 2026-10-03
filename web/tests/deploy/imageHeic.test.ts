// The full image decodes a HEIC photo, not only lists the format.
//
// ⚠ ImageMagick reads HEIC through libheif, and libheif decodes the HEVC
// picture in it through a plugin of its own. Ubuntu 24.04's libheif1 only
// SUGGESTS that plugin, and an ImageMagick installed there lists HEIC and
// fails every phone photo with "Unsupported codec" (0.50 test phase). On
// alpine:3.24 libheif 1.23.4-r0 depends on libheif-libde265 today; the image
// names it too, so a packaging split like Ubuntu's cannot drop it without
// this test saying so. (The server checks at boot as well: enginebin.HEIC
// decodes a sample, and About shows the answer.)
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');

/** The packages of the runtime stage's `apk --no-cache add`, continuations folded. */
function runtimePackages(name: string): string[] {
  const file = readFileSync(path.join(REPO, 'docker', name), 'utf8');
  const stage = file.slice(file.lastIndexOf('\nFROM '));
  const apk = stage.match(/RUN apk --no-cache add([\s\S]*?)(?:&&|\n(?!\s))/);
  expect(apk, `no \`apk --no-cache add\` in docker/${name}'s runtime stage`).not.toBeNull();
  return apk![1].replace(/\\\n/g, ' ').split(/\s+/).filter(Boolean);
}

describe.each(['Dockerfile', 'Dockerfile.local'])('docker/%s', (name) => {
  it('installs ImageMagick, its libheif and the HEVC decoder under it', () => {
    const packages = runtimePackages(name);
    expect(packages).toContain('imagemagick');
    expect(packages).toContain('imagemagick-heic');
    expect(packages).toContain('libheif-libde265');
  });
});
