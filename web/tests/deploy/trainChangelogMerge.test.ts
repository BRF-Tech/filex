// CHANGELOG.md's [Unreleased], merged by Keep a Changelog section
// (scripts/train/changelog-merge.mjs) - what the train's merge queue does
// with the one conflict nearly every branch of a release has.
//
// ⚠ Why this exists (#179): 0.52 and 0.53 resolved these conflicts by hand,
// or with a scratch script that cut the conflict blocks apart by their
// markers. When both sides append to the END of a section, git opens the
// block in the middle of it, under no heading; the scratch moved the
// section's heading into the block and left the entries above it orphaned
// under the previous heading. "a block git opens in the middle of a section"
// below is that case, and is red against the scratch's approach. And at 0.48
// five branches merged with no conflict at all and left [Unreleased] with
// three `### Changed` (lesson #706) - "normalize" below.

import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  ORDER,
  blocksOf,
  mergeChangelog,
  normalizeChangelog,
  parseBody,
  resolveMarkers,
  sidesOf,
  splitUnreleased,
  unreleasedProblems,
} from '../../../scripts/train/changelog-merge.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const HEAD = '# Changelog\n\nAll notable changes.\n\n## [Unreleased]\n';
const TAIL = '## [0.1.0] - 2026-01-01\n\n### Added\n\n- **The first release.** It does one thing.\n';
const cl = (body: string, tail = TAIL) => `${HEAD}${body}${tail}`;
const sec = (title: string, ...entries: string[]) => `### ${title}\n\n${entries.join('\n')}\n`;
const body = (...sections: string[]) => `\n${sections.join('\n')}\n`;

type Section = { title: string; blocks: Array<{ text: string }> };
const sectionsOf = (text: string) =>
  (parseBody(splitUnreleased(text)!.body).sections as Section[]).map((s) => ({ title: s.title, entries: s.blocks.map((b) => b.text) }));

const A = '- **A.** One entry that was there before.';
const B = '- **B.** Another one,\n  wrapped onto a second line.';
const C = '- **C.** A third.';
const F = '- **F.** A fix.';

describe('reading [Unreleased]', () => {
  it('splits a changelog around the body of [Unreleased]', () => {
    const text = cl(body(sec('Added', A)));
    const p = splitUnreleased(text)!;
    expect(p.head.endsWith('## [Unreleased]\n')).toBe(true);
    expect(p.tail.startsWith('## [0.1.0]')).toBe(true);
    expect(p.head + p.body + p.tail).toBe(text);
    expect(splitUnreleased('# no unreleased here\n')).toBeNull();
  });

  it('takes an entry with its wrapped and indented lines, and a paragraph as one entry', () => {
    const blocks = blocksOf(['- **X.** first line', '  second line', '  - a nested point', '- **Y.** next', '', '**A paragraph.** that wraps', 'onto a column-0 line:', '', '- **Z.** after it']);
    expect(blocks.map((b: { text: string }) => b.text)).toEqual([
      '- **X.** first line\n  second line\n  - a nested point',
      '- **Y.** next',
      '**A paragraph.** that wraps\nonto a column-0 line:',
      '- **Z.** after it',
    ]);
    expect(blocks.map((b: { gap: boolean }) => b.gap)).toEqual([false, false, true, true]);
  });

  it('names Keep a Changelog order, with the upgrade notes first', () => {
    expect(ORDER).toEqual(['Upgrade notes', 'Added', 'Changed', 'Deprecated', 'Removed', 'Fixed', 'Security']);
  });
});

describe('merging the three versions', () => {
  it('keeps every entry both sides added at the top of a section, ours first, the heading once', () => {
    const base = cl(body(sec('Added', A)));
    const ours = cl(body(sec('Added', '- **O.** Ours.', A)));
    const theirs = cl(body(sec('Added', '- **T.** Theirs.', A)));
    const r = mergeChangelog({ base, ours, theirs });
    expect(r.ok).toBe(true);
    expect(r.text).toBe(cl(body(sec('Added', '- **O.** Ours.', '- **T.** Theirs.', A))));
  });

  it('keeps the entries above a block git opens in the middle of a section under their heading', () => {
    // Both sides appended to the end of Added: git's block starts after B,
    // under no heading of its own.
    const conflicted = cl(
      `\n### Added\n\n${A}\n${B}\n<<<<<<< HEAD\n- **O.** Ours.\n=======\n- **T.** Theirs.\n>>>>>>> feat/x\n\n${sec('Fixed', F)}\n`,
    );
    const r = resolveMarkers(conflicted);
    expect(r.ok).toBe(true);
    expect(sectionsOf(r.text)).toEqual([
      { title: 'Added', entries: [A, B, '- **O.** Ours.', '- **T.** Theirs.'] },
      { title: 'Fixed', entries: [F] },
    ]);
    expect(splitUnreleased(r.text)!.body.match(/^### Added$/gm)).toHaveLength(1);
    expect(unreleasedProblems(r.text)).toEqual([]);
  });

  it('puts a section theirs added in its place in the order, whatever order theirs wrote', () => {
    const base = cl(body(sec('Added', A)));
    const ours = cl(body(sec('Added', A), sec('Changed', C)));
    const theirs = cl(body(sec('Security', '- **S.** A second layer.'), sec('Added', A), sec('Fixed', F)));
    const r = mergeChangelog({ base, ours, theirs });
    expect(r.ok).toBe(true);
    expect(sectionsOf(r.text).map((s) => s.title)).toEqual(['Added', 'Changed', 'Fixed', 'Security']);
  });

  it('puts the upgrade notes first and a heading it does not know after the ones it does', () => {
    const base = cl(body(sec('Added', A)));
    const ours = cl(body(sec('Added', A), sec('Notes', '- **N.** A note.')));
    const theirs = cl(body(sec('Fixed', F), sec('Added', A), sec('Upgrade notes', '- **U.** Back up first.')));
    const r = mergeChangelog({ base, ours, theirs });
    expect(sectionsOf(r.text).map((s) => s.title)).toEqual(['Upgrade notes', 'Added', 'Fixed', 'Notes']);
  });

  it('lands an entry theirs edited where it was, and keeps what ours added', () => {
    const base = cl(body(sec('Added', A, B, C)));
    const ours = cl(body(sec('Added', A, B, C, '- **O.** Ours.')));
    const theirs = cl(body(sec('Added', A, '- **B.** Another one, now said better.', C)));
    const r = mergeChangelog({ base, ours, theirs });
    expect(r.ok).toBe(true);
    expect(sectionsOf(r.text)[0].entries).toEqual([A, '- **B.** Another one, now said better.', C, '- **O.** Ours.']);
  });

  it('drops an entry both sides dropped, without asking', () => {
    const base = cl(body(sec('Added', A, B, C)));
    const ours = cl(body(sec('Added', A, C, '- **O.** Ours.')));
    const theirs = cl(body(sec('Added', A, C)));
    const r = mergeChangelog({ base, ours, theirs });
    expect(r.ok).toBe(true);
    expect(sectionsOf(r.text)[0].entries).toEqual([A, C, '- **O.** Ours.']);
  });

  it('refuses when both sides changed the same entry - a person decides', () => {
    const base = cl(body(sec('Added', A, B, C)));
    const ours = cl(body(sec('Added', A, '- **B.** Ours says it this way.', C)));
    const theirs = cl(body(sec('Added', A, '- **B.** Theirs says it another way.', C)));
    const r = mergeChangelog({ base, ours, theirs });
    expect(r.ok).toBe(false);
    expect(r.why).toContain('### Added');
    expect(r.why).toContain('**B.** Another one,');
  });

  it('refuses when one side deleted an entry the other edited', () => {
    const base = cl(body(sec('Added', A, B, C)));
    const ours = cl(body(sec('Added', A, C)));
    const theirs = cl(body(sec('Added', A, '- **B.** Edited.', C)));
    expect(mergeChangelog({ base, ours, theirs }).ok).toBe(false);
  });

  it('takes a change to the released history from the side that made it, and refuses two', () => {
    const fixedTail = TAIL.replace('It does one thing.', 'It does one thing well.');
    const base = cl(body(sec('Added', A)));
    const ours = cl(body(sec('Added', A, '- **O.** Ours.')));
    const theirs = cl(body(sec('Added', A)), fixedTail);
    const r = mergeChangelog({ base, ours, theirs });
    expect(r.ok).toBe(true);
    expect(r.text).toContain('It does one thing well.');
    expect(r.text).toContain('**O.** Ours.');
    const both = mergeChangelog({ base, ours: cl(body(sec('Added', A)), TAIL.replace('one thing', 'two things')), theirs });
    expect(both.ok).toBe(false);
    expect(both.why).toContain('released history');
  });

  it('keeps both sides\' lead paragraphs before the first heading', () => {
    const base = cl(body(sec('Added', A)));
    const ours = cl(`\n> ⚠ **Ours.** Read this first.\n\n${sec('Added', A)}\n`);
    const theirs = cl(`\n> ⚠ **Theirs.** And this.\n\n${sec('Added', A)}\n`);
    const r = mergeChangelog({ base, ours, theirs });
    expect(r.ok).toBe(true);
    const lead = parseBody(splitUnreleased(r.text)!.body).lead.map((b: { text: string }) => b.text);
    expect(lead).toEqual(['> ⚠ **Ours.** Read this first.', '> ⚠ **Theirs.** And this.']);
  });
});

describe('conflict markers', () => {
  it('rebuilds both sides, and the base from diff3 markers', () => {
    const text = 'x\n<<<<<<< HEAD\nours\n||||||| base\nbase\n=======\ntheirs\n>>>>>>> b\ny';
    expect(sidesOf(text)).toEqual({ ours: 'x\nours\ny', theirs: 'x\ntheirs\ny', base: 'x\nbase\ny', blocks: 1 });
    expect(sidesOf('x\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> b\n').base).toBeNull();
    expect(() => sidesOf('x\n<<<<<<< HEAD\nours\n')).toThrow(/not closed/);
  });

  it('with a diff3 base, an entry theirs removed inside the block stays removed', () => {
    const conflicted = cl(
      `\n### Added\n\n${A}\n<<<<<<< HEAD\n${B}\n- **O.** Ours.\n||||||| base\n${B}\n=======\n- **T.** Theirs.\n>>>>>>> feat/x\n\n`,
    );
    const r = resolveMarkers(conflicted);
    expect(r.ok).toBe(true);
    expect(sectionsOf(r.text)[0].entries).toEqual([A, '- **O.** Ours.', '- **T.** Theirs.']);
  });

  it('leaves a file without markers as it is', () => {
    const text = cl(body(sec('Added', A)));
    expect(resolveMarkers(text)).toEqual({ ok: true, text, blocks: 0 });
  });
});

describe('normalize: one heading per kind, even after a clean merge', () => {
  it('folds a heading that appears twice into one section, in order', () => {
    const twice = cl(body(sec('Added', A), sec('Changed', '- **C1.** One.'), sec('Fixed', F), sec('Changed', '- **C2.** Two.')));
    const n = normalizeChangelog(twice);
    expect(n.changed).toBe(true);
    expect(n.text).toBe(cl(body(sec('Added', A), sec('Changed', '- **C1.** One.', '- **C2.** Two.'), sec('Fixed', F))));
    expect(unreleasedProblems(twice)).toEqual(['[Unreleased] has "### Changed" 2 times']);
  });

  it('leaves a well-formed body byte for byte, paragraphs and gaps included', () => {
    const text = cl(
      body(
        sec('Added', A, B),
        `### Security\n\n**The store install.** What two reviews found\nbefore it shipped:\n\n- **One.** First.\n- **Two.** Second.\n`,
      ),
    );
    expect(normalizeChangelog(text)).toEqual({ text, changed: false });
    const empty = `${HEAD}\n${TAIL}`;
    expect(normalizeChangelog(empty)).toEqual({ text: empty, changed: false });
  });

  it('finds conflict markers left in the file', () => {
    expect(unreleasedProblems(cl(`\n<<<<<<< HEAD\n${A}\n=======\n${C}\n>>>>>>> x\n`))).toContain('it still holds conflict markers');
  });

  it("this repository's own [Unreleased] holds each heading once", () => {
    // The merge queue normalizes after every merge; this keeps a hand edit
    // from bringing the 0.48 mix back.
    expect(unreleasedProblems(fs.readFileSync(path.join(REPO, 'CHANGELOG.md'), 'utf8'))).toEqual([]);
  });
});
