// No image carries LibreOffice (0.50).
//
// Office documents - their thumbnails and the apps' office engine
// (`engines:office`, formerly `engines:libreoffice`) - go to the ONLYOFFICE
// Document Server filex is connected to, and filex runs no soffice, not even
// one installed in the image. LibreOffice and the headless JRE it needed were
// the bulk of the full image (docker/Dockerfile's header has the measurement),
// so a package that comes back "for office thumbnails" is dead weight that
// nothing runs. The fonts stay: Ghostscript and poppler (a PDF's unembedded
// fonts), rsvg (an SVG's text) and ffmpeg (burned-in subtitles) draw with
// them.
//
// Red before 0.50: docker/Dockerfile and docker/Dockerfile.local installed
// `libreoffice` and `openjdk17-jre-headless`.
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

describe.each(['Dockerfile', 'Dockerfile.local', 'Dockerfile.slim'])('docker/%s', (name) => {
  it('installs no LibreOffice and no Java runtime', () => {
    const office = runtimePackages(name).filter((p) => /libreoffice|openjdk|jre/i.test(p));
    expect(office).toEqual([]);
  });
});

describe.each(['Dockerfile', 'Dockerfile.local'])('docker/%s', (name) => {
  it('keeps the programs the other engines are, and the fonts they draw with', () => {
    expect(runtimePackages(name)).toEqual(
      expect.arrayContaining([
        'ffmpeg',
        'imagemagick',
        'ghostscript',
        'poppler-utils',
        'rsvg-convert',
        'ttf-liberation',
        'ttf-dejavu',
        'font-noto',
        'font-noto-cjk',
      ]),
    );
  });
});
