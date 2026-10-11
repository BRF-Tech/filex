// The compatibility window for plugins and apps (docs/CONTRIBUTING.md →
// "Compatibility window for plugins and apps"): an old form a storage plugin
// or an app sends filex stays accepted for at least 6 minor releases or 12
// months after the release that replaced it, whichever is longer, and is
// refused only after a published CHANGELOG section announced it.
//
// ⚠⚠ Why this test exists. 0.55 promised, on five pages, in the CHANGELOG,
// in the plugin's log and in the server's sentence on the plugin's row, that
// a storage plugin signed in the old form would be "refused from 0.56" - one
// release later. Nothing held that promise to any rule, so nothing noticed it
// was far shorter than what the people building plugins can live with. Now
// backend/internal/compat/windows.json is the one place a window is written:
// the server reads the end from it (compat.Get(id).Until), and this test
// holds the prose to the same numbers - check-links, the docs build and the
// anchor checker all pass on a page that promises the wrong release.
//
// ⚠ NOT SCANNED for promises: CHANGELOG.md, docs/RELEASES.md and
// docs-site/data/releases.json - the historical record (0.55's entry said
// "refused from 0.56", and that was what 0.55 said) - and docs/handovers.
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';
// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore -- plain ESM helper shared with the VitePress config
import { githubSlug } from '../../../docs-site/.vitepress/github-slug.mjs';

const REPO = path.resolve(__dirname, '../../..');
const read = (rel: string): string => readFileSync(path.join(REPO, rel), 'utf8');

interface Window {
  id: string;
  form: string;
  since: string;
  since_date: string;
  docs: string;
  announced?: string;
  removed?: string;
}

interface Registry {
  rule: { minors: number; months: number };
  windows: Window[];
}

const registry: Registry = JSON.parse(read('backend/internal/compat/windows.json'));

type Minor = [major: number, minor: number];

function minorOf(v: string): Minor {
  const m = /^v?(\d+)\.(\d+)(?:\.\d+)?$/.exec(v.trim());
  if (!m) throw new Error(`not MAJOR.MINOR: ${v}`);
  return [Number(m[1]), Number(m[2])];
}

const cmp = (a: Minor, b: Minor): number => a[0] - b[0] || a[1] - b[1];

/** The end of a window, as internal/compat Window.Until computes it. */
function until(w: Window, rule = registry.rule): { version: string; date: string } {
  const [major, minor] = minorOf(w.since);
  const d = new Date(`${w.since_date}T00:00:00Z`);
  // Go's time.AddDate(0, months, 0): the month moves, the day overflows the
  // same way Date.UTC normalises it.
  const end = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + rule.months, d.getUTCDate()));
  return { version: `${major}.${minor + rule.minors}`, date: end.toISOString().slice(0, 10) };
}

const open = registry.windows.filter((w) => !w.removed);

/** Every published release in a CHANGELOG, with its date and its section. */
function releases(changelog: string): Array<{ version: string; date: string; text: string }> {
  const heads = [...changelog.matchAll(/^## \[(\d+\.\d+\.\d+)\] - (\d{4}-\d{2}-\d{2})/gm)];
  return heads.map((m) => {
    const start = m.index ?? 0;
    const next = changelog.indexOf('\n## [', start + 1);
    return { version: m[1], date: m[2], text: changelog.slice(start, next < 0 ? undefined : next) };
  });
}

function lastPublished(changelog: string): Minor {
  const all = releases(changelog).map((r) => minorOf(r.version));
  return all.sort(cmp)[all.length - 1];
}

/** Text with its line breaks (and the comment or quote marks they bring) folded to one space. */
function fold(text: string): string {
  return text.replace(/[ \t]*\r?\n[ \t]*(?:\/\/+|>|\*|#)?[ \t]*/g, ' ');
}

/**
 * A refusal promised for a release: "refused from 0.56", "until filex 0.56",
 * "0.56 refuses", and their Turkish forms. "filex before 0.56 refuses the
 * field" is the other direction (an older filex refusing a newer field) and
 * is not one.
 */
const PROMISES: RegExp[] = [
  /\b(?:refused|rejected|removed|dropped|retired)\s+(?:from|in|with|as of|starting with)\s+(?:filex\s+)?v?(\d+)\.(\d+)/gi,
  /\buntil\s+(?:filex\s+)?v?(\d+)\.(\d+)/gi,
  /(?<!(?:before|than|below)\s)(?<![\w.])v?(\d+)\.(\d+)(?:\.\d+)?\s+(?:refuses|rejects|removes|drops)\b/gi,
  /(?<![\w.])(\d+)\.(\d+)(?:\.\d+)?['’](?:ya|ye|a|e) kadar/gi,
  /(?<![\w.])(\d+)\.(\d+)(?:\.\d+)?['’](?:dan|den|tan|ten) itibaren (?:redded|kaldır)/gi,
];

/**
 * The promises in text that name a release after the last published one and
 * are not the end of an open window.
 */
function earlyPromises(text: string, published: Minor, windows: Window[]): string[] {
  const ends = new Set(windows.map((w) => until(w).version));
  const flat = fold(text);
  const out: string[] = [];
  for (const re of PROMISES) {
    for (const m of flat.matchAll(re)) {
      const v: Minor = [Number(m[1]), Number(m[2])];
      if (cmp(v, published) <= 0) continue; // history: a release that is out
      if (ends.has(`${v[0]}.${v[1]}`)) continue;
      const at = m.index ?? 0;
      out.push(flat.slice(Math.max(0, at - 60), at + m[0].length + 20).trim());
    }
  }
  return out;
}

/** The "until filex X and D" pairs in text that are not an open window's end. */
function wrongEnds(text: string, windows: Window[]): string[] {
  const ends = new Set(windows.map((w) => `${until(w).version} ${until(w).date}`));
  const out: string[] = [];
  for (const m of fold(text).matchAll(/until filex v?(\d+\.\d+) and (\d{4}-\d{2}-\d{2})/g)) {
    if (!ends.has(`${m[1]} ${m[2]}`)) out.push(m[0]);
  }
  return out;
}

/** What is wrong with a removed window, against a CHANGELOG and today. */
function removalProblems(w: Window, changelog: string, today: string, rule = registry.rule): string[] {
  if (!w.removed) return [];
  const out: string[] = [];
  const end = until(w, rule);
  if (!w.announced) out.push(`${w.id}: removed without announced`);
  if (cmp(minorOf(w.removed), minorOf(end.version)) < 0) {
    out.push(`${w.id}: removed in ${w.removed}, before the window's end ${end.version}`);
  }
  const all = releases(changelog);
  const shipped = all.find((r) => cmp(minorOf(r.version), minorOf(w.removed as string)) === 0);
  const removedOn = shipped ? shipped.date : today;
  if (removedOn < end.date) out.push(`${w.id}: removed on ${removedOn}, before the window's end ${end.date}`);
  if (w.announced) {
    if (cmp(minorOf(w.announced), minorOf(w.removed)) >= 0) {
      out.push(`${w.id}: announced in ${w.announced}, not before the removal in ${w.removed}`);
    }
    const notice = all.find((r) => cmp(minorOf(r.version), minorOf(w.announced as string)) === 0);
    if (!notice) out.push(`${w.id}: the announcing release ${w.announced} is not a published CHANGELOG section`);
    else if (!notice.text.includes(w.id)) out.push(`${w.id}: the ${w.announced} CHANGELOG section does not name it`);
  }
  return out;
}

/** Files that describe the product AS IT IS (customCssDocs.test.ts's list). */
function livingDocs(): string[] {
  const out: string[] = [];
  for (const name of readdirSync(path.join(REPO, 'docs'))) {
    const p = path.join(REPO, 'docs', name);
    if (statSync(p).isDirectory() || !name.endsWith('.md') || name === 'RELEASES.md') continue;
    out.push(p);
  }
  for (const name of readdirSync(REPO)) {
    if (!name.endsWith('.md') || name === 'CHANGELOG.md' || name.startsWith('HANDOVER-')) continue;
    out.push(path.join(REPO, name));
  }
  for (const pkg of readdirSync(path.join(REPO, 'packages'))) {
    out.push(path.join(REPO, 'packages', pkg, 'README.md'));
  }
  out.push(path.join(REPO, 'desktop', 'README.md'));
  return out.filter((p) => existsSync(p) && statSync(p).isFile());
}

/** The sentences a person reads: the server's catalogue and the interface's. */
const CATALOGUES = [
  'backend/internal/srvtext/locales/en.json',
  'backend/internal/srvtext/locales/tr.json',
  'web/src/locales/en.json',
  'web/src/locales/tr.json',
];

/** The code that takes what a plugin or an app sends, comments included. */
function pluginSources(): string[] {
  const out: string[] = [];
  const walk = (dir: string) => {
    for (const name of readdirSync(dir)) {
      const p = path.join(dir, name);
      if (statSync(p).isDirectory()) {
        if (name !== 'testdata') walk(p);
      } else if (name.endsWith('.go')) out.push(p);
    }
  };
  for (const dir of ['backend/internal/plugin', 'backend/internal/wasmplugin', 'backend/internal/appstore', 'backend/internal/compat', 'backend/pkg/pluginkit']) {
    walk(path.join(REPO, dir));
  }
  const handlers = path.join(REPO, 'backend/internal/api/handlers');
  for (const name of readdirSync(handlers)) {
    if (/^(plugins|app_plugins|app_store)/.test(name) && name.endsWith('.go')) out.push(path.join(handlers, name));
  }
  return out;
}

const rel = (p: string): string => path.relative(REPO, p).split(path.sep).join('/');

describe('the compatibility window', () => {
  const changelog = read('CHANGELOG.md');
  const published = lastPublished(changelog);

  it('computes the end the way internal/compat does', () => {
    const w: Window = { id: 'x', form: 'f', since: '0.55', since_date: '2026-10-09', docs: 'd' };
    expect(until(w, { minors: 6, months: 12 })).toEqual({ version: '0.61', date: '2027-10-09' });
    // A month end overflows as Go's AddDate does: 2026-08-31 + 1 month = 2026-10-01.
    expect(until({ ...w, since_date: '2026-08-31' }, { minors: 6, months: 1 }).date).toBe('2026-10-01');
  });

  it("states the rule's numbers on this page and on the authors' pages", () => {
    const { minors, months } = registry.rule;
    const said = `at least ${minors} minor releases or ${months} months`;
    for (const page of ['docs/CONTRIBUTING.md', 'docs/PLUGINS.md', 'docs/PLUGIN-KIT.md']) {
      expect(fold(read(page)), page).toContain(said);
    }
  });

  it('describes every window on a page and heading that exist', () => {
    for (const w of registry.windows) {
      const [file, anchor] = w.docs.split('#');
      expect(existsSync(path.join(REPO, file)), `${w.id}: ${file}`).toBe(true);
      const slugs = [...read(file).matchAll(/^#{1,6} (.+)$/gm)].map((m) => githubSlug(m[1]));
      expect(slugs, `${w.id}: #${anchor} in ${file}`).toContain(anchor);
    }
  });

  it('lists every open window in the CONTRIBUTING table with its end', () => {
    const page = read('docs/CONTRIBUTING.md');
    const section = page.slice(page.indexOf('## Compatibility window for plugins and apps'));
    const table = section.slice(0, section.indexOf('\n## '));
    const rows = table.split('\n').filter((l) => l.startsWith('| `'));
    for (const w of open) {
      const row = rows.find((r) => r.startsWith(`| \`${w.id}\` |`));
      expect(row, `${w.id} has no row`).toBeDefined();
      const end = until(w);
      expect(row).toContain(`${w.since} (${w.since_date})`);
      expect(row).toContain(`${end.version} and ${end.date}`);
      expect(row).toContain(`(${w.docs.replace(/^docs\//, '')})`);
    }
    const ids = new Set(open.map((w) => w.id));
    for (const r of rows) {
      const id = /^\| `([^`]+)`/.exec(r)?.[1] ?? '';
      expect(ids.has(id), `a row for ${id}, which is not an open window in windows.json`).toBe(true);
    }
  });

  it("lists every open window in its author's page's own table", () => {
    for (const w of open) {
      const [file, anchor] = w.docs.split('#');
      const page = read(file);
      const at = page.indexOf('## Compatibility window');
      expect(at, `${file} has no Compatibility window section`).toBeGreaterThan(-1);
      const section = page.slice(at, page.indexOf('\n## ', at + 1));
      const row = section.split('\n').find((l) => l.startsWith('|') && l.includes(`(#${anchor})`));
      expect(row, `${file}: no row for ${w.id}`).toBeDefined();
      const end = until(w);
      expect(row).toContain(`${w.since} (${w.since_date})`);
      expect(row).toContain(`filex ${end.version} and ${end.date}`);
    }
  });

  it('names a window end only as the end of an open window', () => {
    const bad: string[] = [];
    for (const p of [...livingDocs(), ...CATALOGUES.map((c) => path.join(REPO, c))]) {
      for (const hit of wrongEnds(readFileSync(p, 'utf8'), open)) bad.push(`${rel(p)}: ${hit}`);
    }
    expect(bad).toEqual([]);
  });

  it('promises no refusal earlier than a window allows, in a page, a sentence or a plugin source', () => {
    const bad: string[] = [];
    for (const p of [...livingDocs(), ...CATALOGUES.map((c) => path.join(REPO, c)), ...pluginSources()]) {
      for (const hit of earlyPromises(readFileSync(p, 'utf8'), published, open)) bad.push(`${rel(p)}: ${hit}`);
    }
    expect(bad).toEqual([]);
  });

  it("keeps a window's end out of the catalogues: the server fills {version} and {date}", () => {
    for (const c of CATALOGUES) {
      const text = read(c);
      for (const w of open) {
        const end = until(w);
        expect(text.includes(end.date), `${c} writes ${end.date} as a literal`).toBe(false);
      }
    }
  });

  it('lets a window go only after its end, and after a published notice', () => {
    for (const w of registry.windows) {
      expect(removalProblems(w, changelog, new Date().toISOString().slice(0, 10))).toEqual([]);
    }
  });

  describe('the checks themselves', () => {
    const w: Window = { id: 'demo-form', form: 'f', since: '0.55', since_date: '2026-10-09', docs: 'd' };
    const log = [
      '## [Unreleased]',
      '## [0.62.0] - 2027-10-20',
      '### Removed',
      '- `demo-form` is refused.',
      '## [0.61.0] - 2027-09-01',
      '### Changed',
      '- `demo-form` will be refused from 0.62.',
      '## [0.55.0] - 2026-10-09',
    ].join('\n');

    it('catches the promise this rule replaced', () => {
      const pub: Minor = [0, 55];
      expect(earlyPromises('is accepted in 0.55\n> only, with a warning, and refused from 0.56.', pub, [w])).toHaveLength(1);
      expect(earlyPromises('accepted until filex 0.56 - install a build', pub, [w])).toHaveLength(1);
      expect(earlyPromises("filex 0.56'ya kadar kabul edilir", pub, [w])).toHaveLength(1);
      expect(earlyPromises("0.56'dan itibaren reddedilir", pub, [w])).toHaveLength(1);
      expect(earlyPromises('// accepted during filex 0.55, with this warning; 0.56 refuses it.', pub, [w])).toHaveLength(1);
    });

    it('lets history, the other direction and a window end through', () => {
      const pub: Minor = [0, 55];
      expect(earlyPromises('which was removed in 0.54.0', pub, [w])).toEqual([]);
      expect(earlyPromises('filex before 0.56 refuses the field', pub, [w])).toEqual([]);
      expect(earlyPromises('filex before 0.49.0 refuses both fields', pub, [w])).toEqual([]);
      expect(earlyPromises('at least until filex 0.61 and 2027-10-09', pub, [w])).toEqual([]);
      expect(wrongEnds('at least until filex 0.61 and 2027-10-09', [w])).toEqual([]);
      expect(wrongEnds('at least until filex 0.60 and 2027-10-09', [w])).toHaveLength(1);
    });

    it('takes a removal after the end and a published notice', () => {
      expect(removalProblems({ ...w, announced: '0.61', removed: '0.62' }, log, '2027-10-20')).toEqual([]);
    });

    it('refuses a removal that cuts the window short or comes without notice', () => {
      expect(removalProblems({ ...w, announced: '0.55', removed: '0.56' }, log, '2026-10-12').join('\n')).toMatch(/before the window's end 0\.61/);
      expect(removalProblems({ ...w, announced: '0.61', removed: '0.63' }, log, '2027-09-30').join('\n')).toMatch(/before the window's end 2027-10-09/);
      expect(removalProblems({ ...w, announced: '0.63', removed: '0.63' }, log, '2027-11-01').join('\n')).toMatch(/not before the removal/);
      expect(removalProblems({ ...w, announced: '0.60', removed: '0.62' }, log, '2027-11-01').join('\n')).toMatch(/not a published CHANGELOG section/);
      expect(removalProblems({ ...w, announced: '0.55', removed: '0.62' }, log, '2027-11-01').join('\n')).toMatch(/does not name it/);
    });
  });
});
