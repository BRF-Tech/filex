// Bundles the main process (and both preloads) into dist/.
//
// ⚠⚠ Why bundle instead of just running tsc: this is a pnpm workspace, so
// `desktop/node_modules/<dep>` is a SYMLINK into the repo root's store —
// outside the app directory. electron-builder does not follow those, so a
// runtime dependency (electron-updater) was silently absent from the asar: the
// main process threw on its import before creating a window and the app
// started to nothing. Bundling means the package carries our code and nothing
// else, and packaging stops depending on how the install is linked.
//
// `electron` stays external — it is provided by the runtime, not from npm.
import { rmSync } from 'node:fs';
import { build } from 'esbuild';

// ⚠ Wipe first: this used to be `tsc -p .`, which left a file per module in
// dist/. Those are dead now (main.js is a bundle) but would still be packaged,
// and a stale one would be loaded if an entry point were ever renamed.
rmSync('dist', { recursive: true, force: true });

const common = {
  bundle: true,
  platform: 'node',
  target: 'node20',
  external: ['electron'],
  logLevel: 'info',
};

await build({
  ...common,
  entryPoints: ['src/main.ts'],
  outfile: 'dist/main.js',
  format: 'esm',
  // ESM output that still needs CJS interop for bundled deps.
  banner: { js: "import { createRequire as __cr } from 'node:module'; const require = __cr(import.meta.url);" },
});

// Preloads must be CommonJS: they run in a sandboxed context that has no ESM
// loader, which is why they are .cts in the first place.
for (const name of ['preload-app', 'preload-shell', 'preload-editor']) {
  await build({
    ...common,
    entryPoints: [`src/${name}.cts`],
    outfile: `dist/${name}.cjs`,
    format: 'cjs',
  });
}
