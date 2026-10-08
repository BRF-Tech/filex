// The vault (encryption level 3, docs/E2E-VAULT-FORMAT.md) is loaded when a
// vault is opened or made, never with the explorer.
//
// ⚠ #94 (2026-10-06): with lib/e2evault, useE2eVault and the vault strip in
// the explorer's static imports, the web app's main chunk went from 2,049 kB
// to 2,128 kB, over workbox's 2 MiB precache limit, and the build failed
// (chain run targeted-20261006-202234Z-5faeb820); the limit was raised to
// 3 MiB to let it through. The vault's code now sits behind `import()`:
// composables/useE2eVault.ts is the light half the explorer holds, the engine
// (composables/e2eVaultEngine.ts) and the rest of lib/e2evault come with the
// first vault, the strip with it. Only e2evault/consts.ts - the key file's
// numbers lib/e2ecrypto checks on every key file - is loaded with the
// explorer.
//
// A module the explorer imports statically is in the main chunk with every
// export anything uses, the lazy code's too, so the rule is about the static
// import graph: from the library's entry, through every `import` and
// `export ... from` that is not `import type` / `export type`, no module of
// the vault but consts.ts. Red on the old code: FileExplorer imported
// useE2eVault, E2eVaultStrip and e2evault/vindex, and e2ecrypto imported
// e2evault/keys, writer, vindex and layout.

import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { PWA_WORKBOX } from '../../pwa.config';
import { CORE, loadedWith, scriptOf, staticImports } from '../helpers/coreImports';

const loaded = loadedWith(path.join(CORE, 'index.ts'));

describe('the vault is loaded with the first vault, not with the explorer', () => {
  it('the walk reaches the explorer and the vault light half (it is not vacuous)', () => {
    expect(loaded.has('FileExplorer.vue')).toBe(true);
    expect(loaded.has('composables/useE2eVault.ts')).toBe(true);
    expect(loaded.has('lib/e2ecrypto.ts')).toBe(true);
    expect(loaded.has('lib/e2evault/consts.ts')).toBe(true);
  });

  it('no module of lib/e2evault but consts.ts is loaded with the explorer', () => {
    const vault = [...loaded].filter((f) => f.startsWith('lib/e2evault/') && f !== 'lib/e2evault/consts.ts');
    expect(vault).toEqual([]);
  });

  it('neither the engine nor the strip is loaded with the explorer', () => {
    expect(loaded.has('composables/e2eVaultEngine.ts')).toBe(false);
    expect(loaded.has('components/E2eVaultStrip.vue')).toBe(false);
  });

  it('they are still loaded - by import(), where a vault is opened', () => {
    expect(scriptOf(path.join(CORE, 'composables/useE2eVault.ts'))).toContain("import('./e2eVaultEngine')");
    expect(scriptOf(path.join(CORE, 'FileExplorer.vue'))).toContain("import('./components/E2eVaultStrip.vue')");
  });

  it('consts.ts imports nothing, so it brings nothing with it', () => {
    expect(staticImports(path.join(CORE, 'lib/e2evault/consts.ts'))).toEqual([]);
  });

  it('the walker reads the import forms the rule depends on', () => {
    const probe = path.join(CORE, 'composables/useE2eVault.ts');
    const specs = staticImports(probe);
    expect(specs).toContain('vue');
    expect(specs).not.toContain('./e2eVaultEngine');
    expect(specs).not.toContain('../lib/e2evault/vindex');
  });

  it("the precache limit is workbox's 2 MiB again: the main chunk fits it", () => {
    expect(PWA_WORKBOX?.maximumFileSizeToCacheInBytes ?? 2 * 1024 * 1024).toBeLessThanOrEqual(2 * 1024 * 1024);
  });
});
