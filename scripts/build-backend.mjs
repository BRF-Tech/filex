#!/usr/bin/env node
// `pnpm run build:backend` — the Go binary, with whatever UI `pnpm run
// sync:embed` last copied into backend/embed/.
//
//   node scripts/build-backend.mjs                 # → bin/filex (bin/filex.exe on Windows)
//   node scripts/build-backend.mjs --out <path>
//
// ⚠ This used to be an inline `cd backend && go build -ldflags='-s -w' …` in
// package.json. On Windows pnpm runs scripts through cmd.exe, where single
// quotes are not quotes, and on the maintainer's workstation `go` is not on
// that PATH at all (it lives in WSL) — so `build:all`, the documented way to
// get a binary that carries the current UI, could not finish there. The
// toolchain lookup is in scripts/lib/go-build.mjs.
//
// ⚠ It embeds backend/embed/ AS IT IS. Nothing here refreshes it: run
// `pnpm run build:web && pnpm run sync:embed` first, or use `pnpm shots`,
// which builds the whole chain and then proves the binary serves the UI that
// was just built (scripts/check-embed.mjs).

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { goBuild } from './lib/go-build.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const i = process.argv.indexOf('--out');
const out =
  i > -1 && process.argv[i + 1]
    ? path.resolve(process.argv[i + 1])
    : path.join(REPO, 'bin', process.platform === 'win32' ? 'filex.exe' : 'filex');

try {
  const { toolchain } = goBuild({
    cwd: path.join(REPO, 'backend'),
    pkg: './cmd/filex',
    out,
    log: (m) => console.log(`build:backend  ${m}`),
  });
  console.log(`build:backend  ✓ ${path.relative(REPO, out).split(path.sep).join('/')} (${toolchain})`);
} catch (err) {
  console.error(`build:backend  ✗ ${err.message}`);
  process.exit(1);
}
