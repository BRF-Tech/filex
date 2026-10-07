// The release's packaging off GitHub Actions (scripts/release/package-local.mjs,
// task #174).
//
// ⚠ Why (0.52.0, 2026-10-05): Actions could not run the tag, and the release
// went out from a PC by hand, in an order and with commands nobody had
// written down. The script is that path, and what it must keep each one
// careless edit away:
//   - it never builds or publishes macOS (a Mac does: only=macos, later) nor
//     the arm64 snap (only=snap-arm64), and says how to add them;
//   - without --publish nothing leaves the machine;
//   - goreleaser creates the Release before anything is attached to it;
//   - the images get the tags and labels release.yml gives them;
//   - between them, the steps attach every Release file a release must carry
//     but the two above (scripts/release/plan.mjs, releaseAssets).
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { globMatch } from '../../../scripts/release/checks.mjs';
import { DESKTOP_FILES, PARTS, imageTags, localPlan } from '../../../scripts/release/package-local.mjs';
import { releaseAssets } from '../../../scripts/release/plan.mjs';

const REPO = path.resolve(__dirname, '../../..');
const DIR = [process.env.FILEX_WORKFLOWS_DIR, path.join(REPO, '.github', 'workflows')].find((d) => d && fs.existsSync(path.join(d, 'release.yml')));

const plan = (over: Record<string, unknown> = {}) =>
  localPlan({
    tag: 'v1.2.3',
    version: '1.2.3',
    exportDir: '/work/public',
    workDir: '/work/public/.git/filex-pc-package/v1.2.3',
    linuxDir: '"$HOME/wt/filex-pc-package/v1.2.3"',
    linuxExport: '/work/public',
    linuxOut: '/work/public/.git/filex-pc-package/v1.2.3/out/linux',
    ...over,
  });

describe('packaging off GitHub Actions', () => {
  it('builds no macOS package and no arm64 snap, and says how to add both once GitHub can', () => {
    for (const publish of [false, true]) {
      const { steps, notes } = plan({ publish });
      const all = steps.map((s: { cmd: string }) => s.cmd).join('\n');
      expect(all).not.toMatch(/dist:mac|--mac\b|\.dmg|only=macos|--linux snap --arm64|arm64\.snap/);
      const text = notes.join('\n');
      expect(text).toContain('gh workflow run release.yml -R BRF-Tech/filex -f tag=v1.2.3 -f only=macos -f publish=true');
      expect(text).toContain('gh workflow run release.yml -R BRF-Tech/filex -f tag=v1.2.3 -f only=snap-arm64 -f publish=true');
      expect(text, 'the release notes say macOS follows').toMatch(/goes out without the macOS packages: say so in its notes/);
      expect(text).toContain('pnpm release 1.2.3 --resume --only deploy');
    }
  });

  it('publishes nothing without --publish', () => {
    const { steps } = plan({ publish: false });
    expect(steps.filter((s: { publishes: boolean }) => s.publishes)).toEqual([]);
    const all = steps.map((s: { cmd: string }) => s.cmd).join('\n');
    expect(all).not.toMatch(/gh release (upload|create)|--push\b|docker push|release --clean --release-notes/);
    expect(all).toContain('release --clean --skip=publish');
    expect(all).toContain('--output type=cacheonly');
  });

  it('with --publish: goreleaser creates the Release first, and the desktop files are attached to it after', () => {
    const { steps } = plan({ publish: true });
    const ids = steps.map((s: { id: string }) => s.id);
    expect(ids).toEqual(['tree-windows', 'tree-linux', 'binaries', 'images', 'desktop-windows', 'desktop-linux', 'upload']);
    const by = Object.fromEntries(steps.map((s: { id: string }) => [s.id, s])) as Record<string, { cmd: string; publishes: boolean; where: string }>;
    expect(by.binaries.publishes).toBe(true);
    expect(by.binaries.cmd).toContain('GORELEASER_CURRENT_TAG=\'v1.2.3\' go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --clean --release-notes="$NOTES"');
    expect(by.binaries.cmd).toContain("node scripts/release-notes.mjs --github 'v1.2.3'");
    expect(by.images.cmd).toContain('--push');
    expect(by.upload.cmd).toContain("gh release upload 'v1.2.3'");
    expect(by.upload.cmd.indexOf('gh release view')).toBeLessThan(by.upload.cmd.indexOf('gh release upload'));
    // Linux runs where Linux is; nothing builds on /mnt (WSL's own disk).
    for (const id of ['tree-linux', 'binaries', 'images', 'desktop-linux']) {
      expect(by[id].where, id).toBe('linux');
      expect(by[id].cmd, id).not.toMatch(/\ncd ['"]?\/mnt\//);
    }
    expect(by['desktop-windows'].where).toBe('windows');
  });

  it('a part alone, and a part that does not exist is refused', () => {
    const { steps } = plan({ only: ['images'], publish: true });
    expect(steps.map((s: { id: string }) => s.id)).toEqual(['tree-linux', 'images']);
    expect(() => plan({ only: ['macos'] })).toThrow('--only macos: the parts are');
    expect(() => plan({ tag: 'v1.2', version: '1.2' })).toThrow('a release is vX.Y.Z');
    expect(PARTS).toEqual(['binaries', 'images', 'desktop-windows', 'desktop-linux']);
  });

  it('labels and tags both images as release.yml does, amd64 and arm64', () => {
    const { steps } = plan({ publish: true });
    const images = steps.find((s: { id: string }) => s.id === 'images')!.cmd as string;
    expect(images).toContain('--platform linux/amd64,linux/arm64');
    expect(images).toContain('--label "org.opencontainers.image.revision=$sha"');
    expect(images).toContain('--label org.opencontainers.image.version=v1.2.3');
    expect(images).toContain('--build-arg "COMMIT=$sha"');
    for (const t of imageTags('v1.2.3').full) expect(images).toContain(`-t ghcr.io/brf-tech/filex:${t}`);
    for (const t of imageTags('v1.2.3').slim) expect(images).toContain(`-t ghcr.io/brf-tech/filex:${t}`);
    expect(images).toContain('smoke-cli.mjs --image "$img" --expect-arch amd64 --expect-version 1.2.3');
  });

  it.runIf(!!DIR)("tags the images release.yml's docker-manifest tags", () => {
    const release = fs.readFileSync(path.join(DIR!, 'release.yml'), 'utf8');
    const joins = [...release.matchAll(/^\s+join (full|slim) (.+)$/gm)].map((m) => [m[1], m[2].split(' ').map((t) => t.replace(/"/g, '').replace('${ver}', 'v1.2.3'))]);
    expect(Object.fromEntries(joins)).toEqual(imageTags('v1.2.3'));
  });

  it('attaches every file a release must carry but the macOS packages and the arm64 snap', () => {
    const shipped = [...DESKTOP_FILES['desktop-windows'], ...DESKTOP_FILES['desktop-linux']];
    const goreleaser = (f: string) => /^filex[_-]/.test(f) && !f.startsWith('filex-desktop') || f === 'checksums.txt';
    const offGitHub = (f: string) => /arm64\.dmg$|^latest-mac\.yml$|arm64\.snap$/.test(f);
    for (const f of releaseAssets('1.2.3')) {
      if (goreleaser(f) || offGitHub(f)) continue;
      expect(shipped.some((g) => globMatch(g, f)), `${f} is attached by no step`).toBe(true);
    }
  });
});
