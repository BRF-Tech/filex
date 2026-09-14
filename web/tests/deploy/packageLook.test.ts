/**
 * The three published wrappers have to come out looking like one product.
 *
 * filex ships the same explorer three ways — `@brftech/filex-core` (the Vue
 * SFC the admin app mounts), `@brftech/filex` (the `<filex-explorer>` web
 * component) and `@brftech/filex-react` (the React adapter around it) — and
 * the look is ONE global stylesheet plus the `--fe-*` tokens on it. Nothing
 * in the build says so, and nothing said a word when it stopped being true.
 *
 * ⚠⚠ Measured 2026-09-13, with the three packages built and each mounted in
 * its own page against the same backend: the React one rendered a BLANK
 * PAGE. Two independent breaks, neither of which any test or type could see:
 *
 *   1. `@brftech/filex` declared `sideEffects: ["./dist/filex.js", …]`, and
 *      `dist/filex.js` is only the entry re-export — `customElements.define`
 *      and the `<style data-filex>` injection live in the hashed chunk beside
 *      it (`dist/index-<hash>.js`), which the list did not name. So every
 *      bundler consumer (React, and any Vite/webpack app that imports the web
 *      component) was free to drop the chunk as side-effect-free: the element
 *      was never registered and the stylesheet never injected.
 *
 *   2. The React adapter handed `createComponent` the class from
 *      `customElements.get('filex-explorer')`, and `createComponent` decides
 *      prop-by-prop with `k in elementClass.prototype`. Vue's
 *      `defineCustomElement` puts its props on each INSTANCE, so that
 *      prototype carries nothing but `constructor` and every prop took the
 *      attribute path: `config` — an object — reached the element as the
 *      string `[object Object]`.
 *
 * The suite could not see either one, because both live in what the packages
 * SHIP rather than in what they compute. So this file checks the shipped
 * contract: who owns the stylesheet, who is allowed to tree-shake what, and
 * whether the React bridge still knows the props the element has.
 *
 * Artefact checks (dist/) run when a build is present — locally and in any
 * job that follows `build:packages`. They announce themselves when they are
 * skipped, because a gate that quietly does nothing is not a gate; the
 * source-level checks above them run everywhere and are the ones that catch
 * both 2026-09-13 breaks.
 */
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');
const pkgDir = (name: string) => path.join(REPO, 'packages', name);
const read = (...p: string[]) => readFileSync(path.join(...p), 'utf8');
const manifest = (name: string) =>
  JSON.parse(read(pkgDir(name), 'package.json')) as {
    name: string;
    exports?: Record<string, unknown>;
    sideEffects?: string[] | boolean;
    dependencies?: Record<string, string>;
  };

const CORE = manifest('core');
const WC = manifest('webcomponent');
const REACT = manifest('react');

const wcSource = read(pkgDir('webcomponent'), 'src', 'index.ts');
const reactSource = read(pkgDir('react'), 'src', 'index.ts');

/** `dist` exists only after a build; the parity checks that read it say so. */
function dist(name: string): string | null {
  const dir = path.join(pkgDir(name), 'dist');
  return existsSync(dir) ? dir : null;
}
const built = ['core', 'webcomponent', 'react'].every((n) => dist(n) !== null);

describe('who owns the stylesheet', () => {
  it('core and the web component both publish one; the source of truth is core', () => {
    expect(CORE.exports?.['./style.css'], 'core stops publishing its stylesheet').toBe(
      './dist/style.css',
    );
    expect(
      WC.exports?.['./style.css'],
      'the web component stops publishing a stylesheet — link-tag consumers lose the look',
    ).toBe('./dist/style.css');
  });

  it('the web component takes its CSS from core rather than growing its own', () => {
    expect(
      wcSource,
      'the web component must import core\'s built stylesheet — a second sheet of its ' +
        'own is how the two distributions start looking different',
    ).toContain("from '@brftech/filex-core/style.css?inline'");
    expect(
      wcSource,
      'and inject it, which is what makes a bundler consumer styled without importing anything',
    ).toContain("setAttribute('data-filex'");
  });

  it('React ships no stylesheet, and therefore must pull in the one that injects itself', () => {
    // Deliberate: a copy in `packages/react/dist` would be a second artefact
    // to keep in step with core's. The adapter wraps the web component, whose
    // bundle carries the CSS inline and appends it on first mount — so a React
    // consumer is styled by importing nothing. If that ever changes, this test
    // is where the decision gets rewritten, and the README below with it.
    expect(REACT.exports?.['./style.css']).toBeUndefined();
    expect(REACT.dependencies?.['@brftech/filex']).toBeTruthy();
    expect(
      reactSource,
      'the side-effect import IS the stylesheet for React consumers',
    ).toContain("import '@brftech/filex'");
  });
});

/**
 * A minimal npm `sideEffects` matcher: `*` matches within one path segment,
 * `**` across segments. Same shape webpack and @rollup/plugin-node-resolve
 * use to decide whether a file may be dropped.
 */
function matchesGlob(glob: string, file: string): boolean {
  const rx = glob
    .replace(/^\.\//, '')
    .split(/(\*\*|\*)/)
    .map((part) => (part === '**' ? '.*' : part === '*' ? '[^/]*' : part.replace(/[.+?^${}()|[\]\\]/g, '\\$&')))
    .join('');
  return new RegExp(`^${rx}$`).test(file.replace(/^\.\//, ''));
}

describe('what a bundler is allowed to drop', () => {
  const globs = Array.isArray(WC.sideEffects) ? WC.sideEffects : null;

  it('the web component lists its side effects as globs, not as named files', () => {
    expect(
      globs,
      'sideEffects: false would let a bundler drop the element registration entirely',
    ).toBeTruthy();
  });

  // The names the build emits: the entry, the UMD twin, and the hashed chunks
  // rollup splits out — one of which holds `customElements.define`. A list
  // that names files instead of matching shapes cannot cover a hash.
  const emitted = [
    'dist/filex.js',
    'dist/filex.umd.cjs',
    'dist/index-BfKVQyBf.js',
    'dist/common-xdTjul80.js',
    'dist/style.css',
  ];

  it.each(emitted)('%s is covered, so nothing tree-shakes the registration away', (file) => {
    expect(
      globs!.some((g) => matchesGlob(g, file)),
      `no sideEffects entry in @brftech/filex matches ${file} — a bundler may drop it, ` +
        'and if it is the chunk with customElements.define the element never registers ' +
        'and the page is blank',
    ).toBe(true);
  });

  it.runIf(built)('covers every file the current build actually emitted', () => {
    const files = readdirSync(dist('webcomponent')!).filter((f) => /\.(js|cjs)$/.test(f));
    expect(files.length, 'the web component build emitted no JS').toBeGreaterThan(1);
    const uncovered = files.filter((f) => !globs!.some((g) => matchesGlob(g, `dist/${f}`)));
    expect(uncovered, 'emitted chunks no sideEffects glob names').toEqual([]);
  });
});

describe('the React prop bridge', () => {
  /** Prop names declared on a `defineCustomElement({ props: { … } })` block. */
  function declaredProps(source: string, after: string): string[] {
    const start = source.indexOf(after);
    expect(start, `${after} is no longer in the web component source`).toBeGreaterThan(-1);
    const block = source.slice(start);
    const props = block.slice(block.indexOf('props: {'));
    const end = props.indexOf('\n  },');
    return [...props.slice(0, end).matchAll(/^\s{4}(\w+):/gm)].map((m) => m[1]);
  }

  it('knows exactly the props the explorer element declares', () => {
    const wcProps = declaredProps(wcSource, 'const FilexExplorerWrapper = defineCustomElement(');
    const listed = /const FALLBACK_PROPS = \[([^\]]*)\]/.exec(reactSource);
    expect(listed, 'FALLBACK_PROPS is no longer a literal array in the React adapter').not.toBeNull();
    const reactProps = [...listed![1].matchAll(/'([^']+)'/g)].map((m) => m[1]);

    expect(wcProps.length, 'no props parsed out of the web component wrapper').toBeGreaterThan(4);
    expect(
      reactProps.sort(),
      'a prop added to <filex-explorer> that the React adapter does not know is not ' +
        'passed as a property: React stringifies it into an attribute instead, which for ' +
        '`config` means the element receives the text "[object Object]"',
    ).toEqual([...wcProps].sort());
  });

  it('still builds its own prototype-carrying class rather than the registered one', () => {
    expect(
      reactSource,
      'Vue puts custom-element props on the instance, so the registered class prototype ' +
        'is empty and @lit/react sends every prop down the attribute path',
    ).toContain('FilexExplorerProps');
    expect(reactSource).not.toMatch(/elementClass:\s*customElements\.get/);
  });
});

describe('what the built packages ship', () => {
  it.runIf(built)("the web component's stylesheet is core's, byte for byte", () => {
    const core = readFileSync(path.join(dist('core')!, 'style.css'));
    const wc = readFileSync(path.join(dist('webcomponent')!, 'style.css'));
    expect(
      wc.equals(core),
      'the two shipped stylesheets have drifted — one distribution is now painted from ' +
        'a different sheet than the other',
    ).toBe(true);
  });

  it.runIf(built)('every token the stock palette declares survives into the shipped sheet', () => {
    const declared = [
      ...new Set(
        [...read(pkgDir('core'), 'src', 'styles', 'variables.css').matchAll(/(--fe-[a-z0-9-]+):/g)].map(
          (m) => m[1],
        ),
      ),
    ];
    expect(declared.length, 'no tokens parsed out of variables.css').toBeGreaterThan(40);
    const sheet = read(dist('webcomponent')!, 'style.css');
    expect(declared.filter((t) => !sheet.includes(`${t}:`))).toEqual([]);
  });

  it.runIf(built)('the web component bundle carries that stylesheet inside its JS', () => {
    // This is the whole reason a React consumer needs no CSS import. If the
    // build ever emits the sheet only as a file, React goes unstyled and
    // nothing else changes.
    const js = readdirSync(dist('webcomponent')!)
      .filter((f) => f.endsWith('.js'))
      .map((f) => read(dist('webcomponent')!, f));
    expect(
      js.some((s) => s.includes('--fe-sidenav-w') && s.includes('data-filex')),
      'no emitted chunk carries both the inlined tokens and the style injection',
    ).toBe(true);
  });

  it.skipIf(built)('reports that the artefact checks had nothing to read', () => {
    console.warn(
      '[packageLook] packages/*/dist is absent — stylesheet parity was NOT checked. ' +
        'Run `pnpm -r --filter="./packages/*" build` first.',
    );
    expect(built).toBe(false);
  });
});

describe('the docs a reader copies', () => {
  const reactReadme = read(pkgDir('react'), 'README.md');
  const wcReadme = read(pkgDir('webcomponent'), 'README.md');
  const integration = read(REPO, 'docs', 'INTEGRATION.md');

  it('tells a Vue consumer to import the stylesheet, because that one must', () => {
    expect(integration).toContain("import '@brftech/filex-core/style.css'");
  });

  /** The body of a `## Heading` section, up to the next heading of any level. */
  function section(markdown: string, heading: string): string | null {
    const at = markdown.indexOf(`\n## ${heading}\n`);
    if (at < 0) return null;
    const body = markdown.slice(at + heading.length + 5);
    const next = body.search(/\n#{1,3} /);
    return next < 0 ? body : body.slice(0, next);
  }

  it('tells a React consumer that the look needs no import, because that one must not', () => {
    // ⚠ The question this section answers is not decoration — it is the reader
    // who pastes the snippet, gets an unstyled (before 2026-09-13: an empty)
    // page, and has nothing anywhere telling him whether he forgot a CSS
    // import. It has to be findable, so a heading, and it has to answer, so
    // the sentence.
    const styles = section(reactReadme, 'Styles');
    expect(styles, 'packages/react/README.md has no "## Styles" section').toBeTruthy();
    expect(
      /no (?:separate )?(?:CSS import|stylesheet)/i.test(styles!),
      'the Styles section does not say that a React consumer imports no stylesheet',
    ).toBe(true);
    expect(styles, 'the Styles section never names the sheet it is talking about').toContain(
      'style.css',
    );
    expect(styles, 'nor the tokens that are the actual styling API').toContain('--fe-');
  });

  it('says the same in the wrapper table, which is where a chooser looks first', () => {
    const row = integration
      .split('\n')
      .find((l) => l.includes('`@brftech/filex-react`') && l.startsWith('|'));
    expect(row, 'docs/INTEGRATION.md has no wrapper-table row for the React package').toBeTruthy();
    // A markdown row is `| a | b |`, so splitting on the pipe leaves an empty
    // cell at each end — take the last one with anything in it.
    const cells = row!.split('|').map((c) => c.trim()).filter(Boolean);
    expect(
      /none/i.test(cells[cells.length - 1] ?? ''),
      'the React row does not say its stylesheet column is "none" — a reader comparing ' +
        'the three wrappers has to be told which one imports a sheet and which do not',
    ).toBe(true);
    expect(integration).toContain("import '@brftech/filex-core/style.css'");
  });

  it('tells a web-component consumer the same, since that bundle also self-injects', () => {
    expect(wcReadme).toContain('style.css');
  });
});
