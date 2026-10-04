/**
 * The `echo` app fixture (backend/internal/wasmplugin/testdata/echo) for the
 * specs that install it: where it is, and whether its built module can be
 * trusted.
 *
 *   const ECHO = echoFixture();
 *   guardFixture(ECHO, test.skip);    // helpers/appPlugin
 *
 * echo.wasm is a build artefact nobody commits (scripts/build-wasm-fixture.sh).
 * A checkout keeps the module it last built: after a change to the fixture's
 * main.go or manifest.json the file is still there, and a spec that only asked
 * whether it EXISTS ran the app as it was before that change. On the v0.50.0
 * pretag spec 192 did exactly that and failed on a button the old module did
 * not have, after an hour of the release chain (lesson #960, #139). So:
 *
 *   - no module: the spec skips (FILEX_REQUIRE_WASM_FIXTURE=1 makes it a failure);
 *   - a module older than a source beside it: the spec FAILS, naming the
 *     command that rebuilds it.
 *
 * ⚠⚠ The rule lives in two languages: here for the e2e specs, and in
 * backend/internal/testutil/wasmfixture/wasmfixture.go (`Stale`) for the Go
 * tests. Change both together; web/tests/deploy/echoFixture.test.ts reads the
 * Go rule's files and holds this one to them.
 */
import { existsSync, lstatSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { AppFixture, AppManifest } from './appPlugin';

/** The fixture's directory in this checkout. */
export const ECHO_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '../../backend/internal/wasmplugin/testdata/echo');

/** What builds the module, run from the repository root. */
export const REBUILD = 'bash scripts/build-wasm-fixture.sh';

/** A file the module is built from: the .go and .json files, go.mod and go.sum (wasmfixture.Stale). */
export function isSource(name: string): boolean {
  return name.endsWith('.go') || name.endsWith('.json') || name === 'go.mod' || name === 'go.sum';
}

/**
 * The newest source in the module's directory that is newer than the module,
 * or '' when the module is at least as new as all of them.
 */
export function staleSource(wasm: string): string {
  const dir = dirname(wasm);
  let newest = '';
  let newestAt = lstatSync(wasm, { bigint: true }).mtimeNs;
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.isDirectory() || !isSource(e.name)) continue;
    const at = lstatSync(join(dir, e.name), { bigint: true }).mtimeNs;
    if (at > newestAt) {
      newest = e.name;
      newestAt = at;
    }
  }
  return newest;
}

/**
 * The fixture as an app to install (helpers/appPlugin installThroughWizard).
 * `failReason` is set when the module is older than its sources;
 * `guardFixture` fails the spec with it.
 */
export function echoFixture(dir: string = ECHO_DIR): AppFixture {
  const wasm = join(dir, 'echo.wasm');
  const manifestPath = join(dir, 'manifest.json');
  if (!existsSync(wasm)) {
    return {
      name: 'echo',
      present: false,
      wasm,
      manifestPath,
      manifest: undefined,
      languages: [],
      skipReason: `echo.wasm not built: ${REBUILD} (${wasm})`,
    };
  }
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8')) as AppManifest;
  const newer = staleSource(wasm);
  return {
    name: 'echo',
    present: true,
    wasm,
    manifestPath,
    manifest,
    languages: manifest.languages ?? [],
    skipReason: '',
    ...(newer
      ? {
          failReason:
            `echo.wasm is older than ${newer} beside it, so it was built before that change and the spec would run the old app. ` +
            `Rebuild it: ${REBUILD} (${wasm})`,
        }
      : {}),
  };
}
