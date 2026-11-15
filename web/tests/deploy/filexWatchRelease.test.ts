// filex-watch's own release line (#352, docs/FILEX-WATCH.md -> Releases).
//
// The agent is versioned and tagged apart from filex - `filex-watch-v0.1.0`,
// AgentVersion in backend/internal/watchproto - in the SAME repository. Three
// things keep the two lines from stepping on each other, and this holds them:
//   - its Release is never GitHub's "latest": every releases/latest/download
//     link filex publishes (the CLI install line, the desktop update feed)
//     names a filex asset;
//   - the update manifest (scripts/gen-update-manifest.py) and the docs'
//     Releases page (docs-site/scripts/fetch-releases.mjs) leave its tags out:
//     a server reading one as a filex version would offer to install an image
//     that does not exist;
//   - the version is written once (watchproto.AgentVersion) and the tag, the
//     image the docs and the Compose file pull, and the binary agree with it.
//
// The workflow lives in the PUBLIC checkout only; the change reaches it as
// packaging/ci/filex-watch-release.patch. Here it is read from the public
// checkout when one is at hand (FILEX_WORKFLOWS_DIR), else from the patch.
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { DIR, REPO } from '../helpers/releaseWorkflow';

const read = (rel: string) => fs.readFileSync(path.join(REPO, rel), 'utf8');

/** The workflow: the public checkout's, else the file the patch creates. */
function workflow(): string {
  if (DIR && fs.existsSync(path.join(DIR, 'filex-watch.yml'))) {
    return fs.readFileSync(path.join(DIR, 'filex-watch.yml'), 'utf8').replace(/\r\n/g, '\n');
  }
  const patch = read('packaging/ci/filex-watch-release.patch').replace(/\r\n/g, '\n');
  expect(patch).toContain('+++ b/.github/workflows/filex-watch.yml');
  const body = patch.slice(patch.indexOf('\n@@ ') + 1);
  return body
    .split('\n')
    .slice(1)
    .filter((l) => l.startsWith('+'))
    .map((l) => l.slice(1))
    .join('\n');
}

/** Without comment-only lines. */
const code = () =>
  workflow()
    .split('\n')
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

const python = process.platform === 'win32' ? 'python' : 'python3';
/** The manifest generator is Python; a machine without it skips that check
 *  (it is no workflow guard: the release tool names the others). */
const HAS_PYTHON = spawnSync(python, ['--version'], { encoding: 'utf8' }).status === 0;

const AGENT_VERSION = /^const AgentVersion = "([^"]+)"$/m.exec(read('backend/internal/watchproto/watchproto.go'))?.[1];
const TAG_PREFIX = /^const TagPrefix = "([^"]+)"$/m.exec(read('backend/internal/watchproto/watchproto.go'))?.[1];

describe('filex-watch: a release line of its own', () => {
  it('reads the version and the tag prefix from the source', () => {
    expect(AGENT_VERSION).toMatch(/^\d+\.\d+\.\d+$/);
    expect(TAG_PREFIX).toBe('filex-watch-v');
  });

  it('runs on its own tags only, and a filex tag never runs it', () => {
    const wf = code();
    expect(wf).toMatch(/\non:\n {2}push:\n {4}tags: \["filex-watch-v\*"\]\n {2}workflow_dispatch:/);
    expect(wf).not.toMatch(/tags: \["v\*"\]/);
  });

  it('publishes a Release that is never "latest", with checksums', () => {
    const wf = code();
    expect(wf).toContain('--latest=false');
    expect(wf).toContain('sha256sum -- * > checksums.txt');
    for (const target of ['linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64', 'darwin/amd64', 'darwin/arm64']) {
      expect(wf).toContain(target);
    }
    // Only a tag run publishes; a run by hand is a dry run.
    expect(wf).toContain("PUBLISH: ${{ github.event_name == 'push' && startsWith(github.ref, 'refs/tags/filex-watch-v') }}");
    expect(wf).toMatch(/- name: The GitHub Release \(never "latest"\)\n {8}if: env\.PUBLISH == 'true'/);
    expect(wf).toContain("push: ${{ env.PUBLISH == 'true' }}");
  });

  it('refuses a tag that names another version than the source', () => {
    const wf = code();
    expect(wf).toContain(`sed -n 's/^const AgentVersion = "\\(.*\\)"$/\\1/p' backend/internal/watchproto/watchproto.go`);
    expect(wf).toContain('tag="${GITHUB_REF_NAME#filex-watch-v}"');
    expect(wf).toContain('-X main.version=$VERSION');
  });

  it('builds the image from the Dockerfile in this tree, for both architectures', () => {
    const wf = code();
    expect(wf).toContain('file: docker/Dockerfile.filex-watch');
    expect(wf).toContain('platforms: linux/amd64,linux/arm64');
    expect(fs.existsSync(path.join(REPO, 'docker/Dockerfile.filex-watch'))).toBe(true);
    for (const f of ['filex-watch.service', 'sh.filex.watch.plist', 'config.example.json']) {
      expect(wf).toContain(`cmd/filex-watch/packaging/${f}`);
      expect(fs.existsSync(path.join(REPO, 'backend/cmd/filex-watch/packaging', f)), f).toBe(true);
    }
  });

  it('reads its notes from its own changelog, which has the version', () => {
    expect(code()).toContain('backend/cmd/filex-watch/CHANGELOG.md');
    expect(read('backend/cmd/filex-watch/CHANGELOG.md')).toContain(`## [${AGENT_VERSION}]`);
  });

  it('the image the Compose example and the docs pull is the version the source says', () => {
    const image = `ghcr.io/brf-tech/filex-watch:${AGENT_VERSION}`;
    expect(read('deploy/compose/docker-compose.filex-watch.yml')).toContain(`image: ${image}`);
    expect(read('docs/FILEX-WATCH.md')).toContain(image);
  });
});

describe('filex-watch releases are not filex versions', () => {
  it('the docs Releases page leaves them out', async () => {
    const { isFilexRelease, AGENT_TAG_PREFIX } = (await import('../../../docs-site/scripts/fetch-releases.mjs')) as {
      isFilexRelease: (raw: { tag_name?: string; draft?: boolean }) => boolean;
      AGENT_TAG_PREFIX: string;
    };
    expect(AGENT_TAG_PREFIX).toBe(TAG_PREFIX);
    expect(isFilexRelease({ tag_name: 'v0.61.0' })).toBe(true);
    expect(isFilexRelease({ tag_name: 'filex-watch-v0.1.0' })).toBe(false);
    expect(isFilexRelease({ tag_name: 'v0.61.0', draft: true })).toBe(false);
  });

  it.runIf(HAS_PYTHON)('the update manifest leaves them out', () => {
    const gen = path.join(REPO, 'scripts/gen-update-manifest.py');
    const probe = [
      'import importlib.util, json, sys',
      'spec = importlib.util.spec_from_file_location("gen", sys.argv[1])',
      'gen = importlib.util.module_from_spec(spec)',
      'spec.loader.exec_module(gen)',
      'rels = [{"tag_name": "v0.61.0"}, {"tag_name": "filex-watch-v0.1.0"}, {"tag_name": "v0.60.2", "prerelease": True}, {"tag_name": "v0.60.1", "draft": True}]',
      'print(json.dumps({"prefix": gen.AGENT_TAG_PREFIX, "kept": [r["tag_name"] for r in rels if gen.is_filex_release(r)]}))',
    ].join('\n');
    const out = JSON.parse(execFileSync(python, ['-c', probe, gen], { encoding: 'utf8' }));
    expect(out.prefix).toBe(TAG_PREFIX);
    expect(out.kept).toEqual(['v0.61.0']);
  });
});
