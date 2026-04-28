#!/usr/bin/env node
/**
 * sync-embed.mjs
 *
 * Copies built front-end assets into backend/embed/* so the Go binary's
 * `//go:embed` directive picks them up.
 *
 *   web/dist/                       -> backend/embed/admin/
 *   packages/webcomponent/dist/     -> backend/embed/web/
 *
 * Run after building the frontend:
 *   pnpm run build:packages
 *   pnpm run build:web
 *   pnpm run sync:embed       # this script
 *   pnpm run build:backend    # `go build` will embed the synced dirs
 */

import { rm, cp, mkdir, stat } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const root = path.resolve(path.dirname(__filename), '..');

const targets = [
  {
    src: path.join(root, 'web', 'dist'),
    dest: path.join(root, 'backend', 'embed', 'admin'),
    label: 'admin UI',
  },
  {
    src: path.join(root, 'packages', 'webcomponent', 'dist'),
    dest: path.join(root, 'backend', 'embed', 'web'),
    label: 'web component bundle',
  },
];

async function syncDir({ src, dest, label }) {
  if (!existsSync(src)) {
    console.error(`\u2717 [${label}] Source not found: ${src}`);
    console.error(`  Did you run 'pnpm run build:packages && pnpm run build:web' first?`);
    process.exit(1);
  }

  const stats = await stat(src);
  if (!stats.isDirectory()) {
    console.error(`\u2717 [${label}] Source is not a directory: ${src}`);
    process.exit(1);
  }

  await rm(dest, { recursive: true, force: true });
  await mkdir(dest, { recursive: true });
  await cp(src, dest, { recursive: true });

  console.log(`\u2713 [${label}] ${rel(src)} -> ${rel(dest)}`);
}

function rel(p) {
  return path.relative(root, p).replace(/\\/g, '/');
}

console.log('Syncing embed assets...');
await Promise.all(targets.map(syncDir));
console.log('Done. Now run: pnpm run build:backend');
