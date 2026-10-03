// No page, listing or package text a person reads says an em dash or an en
// dash: a plain hyphen-minus only - nor a character that passes for a hyphen
// (U+2010, U+2011, U+2012, U+2015, a minus sign U+2212). The second half of
// noLongDashes.test.ts, which holds the translations and the interface code
// to the same rule; the rule itself is scripts/i18n-validate.mjs's
// dashProblem. In the docs the minus sign has no exception: arithmetic,
// negative numbers and ranges are all written "-", because a reader copies
// them into a field or a shell.
//
// ⚠ Why (owner's ruling, 2026-09-30): the long dashes read as a machine's hand
// ("the AI dash"), and after the interface was cleaned about 7,800 of them
// were still in the documentation, the changelog, filex.sh and the store
// texts. Every one became "-"; this keeps them from coming back. Ranges are
// "a-z", "3-60", an empty cell is "-".
//
// What is read, as a person meets it:
//   1. every markdown file git tracks, WHOLE: the docs site's pages, the
//      README, the changelog with every past release, the package READMEs
//      npm shows, the Microsoft Store listing, the working notes. Code blocks
//      included - a command's output quoted in the docs is what filex prints,
//      and filex prints "-";
//   2. the filex.sh pages and the embedding demos (comments left out);
//   3. what an app store or a package manager prints: the Umbrel, CasaOS,
//      Runtipi, Unraid, Portainer and Helm manifests, goreleaser's release
//      header and footer, electron-builder's texts and every package.json
//      (YAML comments left out, they are notes for the next maintainer);
//   4. the code that writes published text: the release notes for GitHub and
//      Umbrel, the docs site's Releases page (which also cleans the GitHub
//      bodies it renders, see plainDashes there), the winget manifests, the
//      GitHub About blurb and the docs site's own config.
//
// WHEN IT FIRES: write "-". In markdown, a sentence that wraps with the dash
// FIRST on its line would become a list item as "- ", so the dash goes to the
// end of the line before (" -"). A heading that changes spelling changes its
// anchor: `node scripts/check-doc-anchors.mjs` names every link to follow it.
// Only a verbatim quote of something that cannot change (a commit subject in
// git's history, another program's output) goes in ALLOW, with its reason; an
// entry that no longer matches anything fails the test.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  cssStrings,
  htmlStrings,
  jsonStrings,
  tsStrings,
  yamlStrings,
  type Shown,
} from '../helpers/shownStrings';
// A plain .mjs: the one definition of what is not a plain hyphen.
import { dashProblem } from '../../../scripts/i18n-validate.mjs';

const ROOT = path.resolve(__dirname, '../../..');

// Built from their code points, so no editor or tool can quietly turn the
// check itself into the character it looks for (or into a plain hyphen).
const EM_DASH = String.fromCharCode(0x2014);
const EN_DASH = String.fromCharCode(0x2013);
const NB_HYPHEN = String.fromCharCode(0x2011);
const MINUS = String.fromCharCode(0x2212);
const BS = String.fromCharCode(92);
// The same characters spelled as an escape or an HTML entity: still on
// screen (site/index.html carried four `&mdash;`).
const SPELLED = new RegExp(
  `${BS}${BS}u(201[0-5]|2212)|${BS}${BS}U0000(201[0-5]|2212)|&(mdash|ndash|minus|#82(0[89]|1[0-3])|#8722|#x(201[0-5]|2212));`,
  'i',
);
// No symbol exception in what is published: a line that is the minus sign
// alone is still one a reader could copy.
const isLong = (s: string) => SPELLED.test(s) || s.includes(MINUS) || dashProblem(s) !== null;
const FIX = 'write "-" (a plain hyphen) instead of a long dash or a character that passes for a hyphen';

/** Every file git tracks: the same list the public export is built from. */
const TRACKED = execFileSync('git', ['-C', ROOT, 'ls-files', '-z'], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
  .split('\0')
  .filter((f) => f && fs.existsSync(path.join(ROOT, f)));
const tracked = new Set(TRACKED);

/** A long dash that has to stay. `file` is repo-relative, `text` a piece of the line. */
interface Allow {
  file: string;
  text: string;
  why: string;
}
const GIT_LOG = "git log's output: a commit subject in the history, which cannot be rewritten";
const ALLOW: Allow[] = [
  { file: 'HANDOVER-2026-05-08-parity-round-2-3.md', text: '127e38b fix(web): FileVersions.vue', why: GIT_LOG },
  { file: 'HANDOVER-2026-05-08-parity-round-2-3.md', text: 'bc08815 feat: parity round 2', why: GIT_LOG },
  { file: 'HANDOVER-2026-05-08-round-7.md', text: '3bf6201 test(e2e): rounds 4-6 regression suite', why: GIT_LOG },
  {
    file: 'HANDOVER-2026-05-08-round-8.md',
    text: 'ok  Round 8 ',
    why: "Playwright's report of that day, quoting the test titles it ran",
  },
];

/** Every line of a file, as it is: markdown and plain text are read whole. */
const lines = (src: string): Shown[] => src.split('\n').map((text, i) => ({ line: i + 1, text }));

function reader(name: string): (src: string) => Shown[] {
  if (/\.(md|txt)$/.test(name)) return lines;
  if (/\.(html|xml)$/.test(name)) return htmlStrings;
  if (name.endsWith('.json')) return jsonStrings;
  if (/\.ya?ml$/.test(name)) return yamlStrings;
  if (name.endsWith('.css')) return (src) => cssStrings(src);
  return (src) => tsStrings(src);
}

const used = new Set<Allow>();

/** "file:line: text" for every shown line of `src` that carries a long dash. */
function longDashes(name: string, src: string): string[] {
  const out: string[] = [];
  for (const s of reader(name)(src)) {
    s.text.split('\n').forEach((l, k) => {
      if (!isLong(l)) return;
      const allowed = ALLOW.find((a) => a.file === name && l.includes(a.text));
      if (allowed) used.add(allowed);
      else out.push(`${name}:${s.line + k}: ${l.trim().slice(0, 140)}`);
    });
  }
  return out;
}

const HELM_TEMPLATES = 'deploy/helm/filex/templates/';

/** What is read, and how many files each place has at least (a scan of nothing passes everything). */
const AREAS: Array<{ area: string; files: () => string[]; atLeast: number }> = [
  {
    area: 'markdown: every tracked *.md (docs, README, CHANGELOG, package READMEs, store listing, notes)',
    files: () => TRACKED.filter((f) => f.endsWith('.md')),
    atLeast: 60,
  },
  {
    area: 'pages: filex.sh and the embedding demos',
    files: () => TRACKED.filter((f) => /^(site|demo)\/.*\.html$/.test(f)),
    atLeast: 3,
  },
  {
    // Helm's templates are Go templates around YAML, with `{{/* */}}` notes a
    // YAML reader cannot see; the one a person reads is NOTES.txt, printed by
    // `helm install`.
    area: 'store manifests and packages (Umbrel, CasaOS, Runtipi, Unraid, Portainer, Helm, goreleaser, electron-builder, package.json)',
    files: () =>
      TRACKED.filter(
        (f) =>
          (f.startsWith('deploy/') &&
            /\.(ya?ml|json|xml|txt)$/.test(f) &&
            (!f.startsWith(HELM_TEMPLATES) || f === `${HELM_TEMPLATES}NOTES.txt`)) ||
          f === '.goreleaser.yml' ||
          f === 'desktop/electron-builder.yml' ||
          f === 'package.json' ||
          f.endsWith('/package.json'),
      ),
    atLeast: 20,
  },
  {
    area: 'code that writes published text, and the docs site config',
    files: () =>
      [
        'docs-site/scripts/fetch-releases.mjs',
        'scripts/release-notes.mjs',
        'scripts/shop-window-data.mjs',
        'desktop/scripts/pkg-manifests.mjs',
        'docs-site/.vitepress/config.mts',
        'docs-site/.vitepress/theme/custom.css',
      ].filter((f) => tracked.has(f)),
    atLeast: 6,
  },
];

describe('what the repository publishes uses a plain hyphen, never a long dash', () => {
  it('reads what is shown and skips what is a comment', () => {
    const d = EM_DASH;
    const samples: Record<string, string> = {
      'x.yml': [
        `# HIDDEN ${d}`,
        `key: SHOWN1 ${d}  # HIDDEN ${d}`,
        'desc: >-',
        `  SHOWN2 ${d} # a hash inside a block scalar is text`,
        `  SHOWN3 ${EN_DASH}`,
        'list:',
        `  - "SHOWN4 ${d} # not a comment"`,
        `  - it's SHOWN5 ${d}   # HIDDEN ${d}`,
        `back: SHOWN6 ${d}`,
      ].join('\n'),
      'x.md': ['# Title', '', '```sh', `echo SHOWN1 ${d}`, '```', `SHOWN2 &mdash; text`].join('\n'),
      'x.xml': [`<!-- HIDDEN ${d} -->`, `<Overview>SHOWN1 ${d}</Overview>`, `<Config Description="SHOWN2 ${d}"/>`].join('\n'),
    };
    const shown: Record<string, number> = { 'x.yml': 6, 'x.md': 2, 'x.xml': 2 };
    for (const [name, src] of Object.entries(samples)) {
      const hits = longDashes(name, src).join('\n');
      expect(hits, name).not.toContain('HIDDEN');
      for (let i = 1; i <= shown[name]!; i++) expect(hits, `${name} SHOWN${i}`).toContain(`SHOWN${i}`);
    }
  });

  it('a character that passes for a hyphen is caught too, the minus sign included', () => {
    const md = [
      `SHOWN1 read${NB_HYPHEN}only`,
      `SHOWN2 \`max_downloads ${MINUS} download_count\``,
      `SHOWN3 priority ${MINUS}1000`,
      MINUS,
      'SHOWN5 &minus;1',
      `SHOWN6 ${String.fromCharCode(0x2012)} ${String.fromCharCode(0x2015)} ${String.fromCharCode(0x2010)}`,
      'read-only, 3-60, -1, `a - b`',
    ].join('\n');
    const hits = longDashes('y.md', md);
    expect(hits).toHaveLength(6);
    expect(hits.join('\n')).toContain('y.md:4:');
    for (const i of [1, 2, 3, 5, 6]) expect(hits.join('\n'), `SHOWN${i}`).toContain(`SHOWN${i}`);
  });

  // Each area is read once: its own case and the allowance check share it.
  const scans = new Map<string, { files: number; found: string[] }>();
  const scan = ({ area, files }: (typeof AREAS)[number]) => {
    let s = scans.get(area);
    if (!s) {
      const list = files();
      s = {
        files: list.length,
        found: list.flatMap((f) => longDashes(f, fs.readFileSync(path.join(ROOT, f), 'utf8'))),
      };
      scans.set(area, s);
    }
    return s;
  };

  it.each(AREAS)('$area', (a) => {
    const { files, found } = scan(a);
    expect(files).toBeGreaterThanOrEqual(a.atLeast);
    expect(found, FIX).toEqual([]);
  });

  it('the store blurb and the GitHub release body the release writes carry none either', async () => {
    const { releaseNotes, githubReleaseBody } = (await import('../../../scripts/release-notes.mjs')) as {
      releaseNotes: (changelog: string, version: string) => string | null;
      githubReleaseBody: (changelog: string, version: string) => Promise<string | null>;
    };
    const changelog = fs.readFileSync(path.join(ROOT, 'CHANGELOG.md'), 'utf8');
    const version = changelog.match(/^## \[(\d+\.\d+\.\d+)\]/m)?.[1];
    expect(version, 'CHANGELOG.md has no released version').toBeTruthy();
    const notes = releaseNotes(changelog, version!) ?? '';
    const body = (await githubReleaseBody(changelog, version!)) ?? '';
    expect(notes).toContain(`filex v${version}`);
    expect(notes.split('\n').filter(isLong)).toEqual([]);
    expect(body.split('\n').filter(isLong)).toEqual([]);
  });

  // An allowance for a file this tree does not have (the working notes are
  // not in the public export) has nothing to match here, and is not stale.
  it('every allowance still matches something', () => {
    AREAS.forEach(scan);
    const stale = ALLOW.filter((a) => tracked.has(a.file) && !used.has(a)).map((a) => `${a.file}: ${a.text}`);
    expect(stale).toEqual([]);
  });
});
