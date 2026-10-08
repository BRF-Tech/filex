// Fail a package's build on any diagnostic its declaration build reports.
//
// ⚠⚠ vite-plugin-dts only PRINTS the TypeScript diagnostics it meets while it
// writes a package's `.d.ts`, then writes the declarations anyway - with `any`
// wherever a type could not be worked out - and `vite build` exits 0. The
// `vue-tsc --noEmit` / `tsc --noEmit` that runs first in each build script is
// not the same check: the plugin's program types `.vue` files through its own
// @vue/language-core and emits declarations, and some errors exist only there.
// Measured on the build host's nightly (nightly-20261007-230007Z): DataTable's
// `useSlots()` loop printed TS7022/TS7024 in the core and web-component
// builds, both builds passed, vue-tsc was green, and the published
// @brftech/filex-core typed the table's `$slots` as `any` for months.
//
// ⚠ ONE helper for every package that builds declarations (core,
// webcomponent, app-ui, react): `web/tests/deploy/dtsStrict.test.ts` fails on
// a `vite.config.ts` whose `dts()` call does not pass it.
//
// ⚠ `vite build --watch` (each package's `dev` script) only warns. The plugin
// collects its program's diagnostics once, at the first build, and clears them
// at the end of a declaration write that a throw never reaches - so in watch
// mode a throw would fail every later rebuild with the same stale list, fixed
// or not.

import path from 'node:path';

/** How many diagnostics the error lists; the plugin has printed all of them. */
const LISTED = 20;

/**
 * `afterDiagnostic` for vite-plugin-dts: throws when the declaration build
 * reported anything, so `vite build` fails instead of shipping `any`.
 *
 * @param {string} pkg  the package's name, for the error
 * @param {{ watch?: boolean }} [opts]  watch mode only warns (see above)
 * @returns {(diagnostics: readonly import('typescript').Diagnostic[]) => void}
 */
export function failOnDtsDiagnostics(pkg, { watch = isWatchRun(process.argv) } = {}) {
  return (diagnostics) => {
    if (!diagnostics || diagnostics.length === 0) return;
    const message = dtsDiagnosticsMessage(pkg, diagnostics);
    if (watch) {
      console.warn(`${message}\n(watch mode: not failing the rebuild; \`vite build\` fails on this)`);
      return;
    }
    throw new Error(message);
  };
}

/** Whether this process is `vite build --watch` / `-w`. */
export function isWatchRun(argv) {
  return argv.includes('--watch') || argv.includes('-w');
}

/**
 * The error's text: how many, for which package, and where (`file:line:col
 * TSnnnn: message`), relative to the package when the file is inside it.
 *
 * @param {string} pkg
 * @param {readonly import('typescript').Diagnostic[]} diagnostics
 * @param {string} [cwd]
 */
export function dtsDiagnosticsMessage(pkg, diagnostics, cwd = process.cwd()) {
  const n = diagnostics.length;
  const lines = diagnostics.slice(0, LISTED).map((d) => `  ${describeDiagnostic(d, cwd)}`);
  if (n > LISTED) lines.push(`  ... and ${n - LISTED} more (printed above)`);
  return [
    `[vite:dts] ${pkg}: ${n} TypeScript diagnostic${n === 1 ? '' : 's'} while writing the declarations.`,
    'A type the declaration build cannot work out ships as `any` in dist/*.d.ts, so the build fails;',
    'fix the cause (see the plugin output above and scripts/vite-dts-strict.mjs):',
    ...lines,
  ].join('\n');
}

/**
 * @param {import('typescript').Diagnostic} d
 * @param {string} cwd
 */
export function describeDiagnostic(d, cwd) {
  const head = `TS${d.code}: ${flatten(d.messageText)}`;
  if (!d.file) return head;
  const abs = d.file.fileName;
  const inside = path.relative(cwd, abs);
  const name = (inside && !inside.startsWith('..') && !path.isAbsolute(inside) ? inside : abs).split(path.sep).join('/');
  if (typeof d.start !== 'number' || typeof d.file.getLineAndCharacterOfPosition !== 'function') return `${name} ${head}`;
  const { line, character } = d.file.getLineAndCharacterOfPosition(d.start);
  return `${name}:${line + 1}:${character + 1} ${head}`;
}

/** A diagnostic's text: a string, or a message chain read depth first. */
function flatten(text) {
  if (typeof text === 'string') return text;
  if (!text || typeof text !== 'object') return '';
  const parts = [text.messageText];
  for (const next of text.next ?? []) parts.push(flatten(next));
  return parts.filter(Boolean).join(' ');
}
