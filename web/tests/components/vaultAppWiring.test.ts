// filex 0.56 (task #189) - the explorer's wiring for an app editing a file of
// a VAULT, held by its shape (the behaviour itself: lib/appPlaintext.test.ts,
// components/appFrameVault.test.ts, composables/useE2eVault.test.ts, and in
// a browser e2e 236).
//
//  - a vault's file is handed to an app by the one rule every encrypted file
//    goes through (e2ePlainRootOf → lib/appViewer `e2ePlaintext`), while the
//    vault is open in this tab;
//  - opening it (double-click, Enter, a look) tries the app first, and only
//    then decrypts a copy for filex's own viewer;
//  - the door the app's frame is given carries the vault's half, over the
//    vault engine (useE2eVault readForApp / saveForApp) - never a server door;
//  - every lock - Lock, the vault's own after 15 idle minutes - closes the
//    app and says which file lost what it had not saved (the app says it
//    holds unsaved changes through PreviewModal's `app-dirty`).
//
// Red before 0.56: e2ePlainRootOf refused every vault row, the vault's open
// branch decrypted for filex's viewer only, the door had no vault half, and a
// lock closed the app without a word.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const core = (p: string) => readFileSync(resolve(__dirname, '../../../packages/core/src', p), 'utf8');
const src = core('FileExplorer.vue');
const between = (from: string, to: string) => src.slice(src.indexOf(from), src.indexOf(to, src.indexOf(from)));

describe('the explorer hands a vault’s file to an app (filex 0.56)', () => {
  it('by the one rule, while the vault is open here and the server’s switch is on', () => {
    const rule = between('function e2ePlainRootOf', 'function vaultAppRootOf');
    expect(rule).toMatch(/apps_e2e_plaintext !== true\) return null;\s*if \(vault\.isVaultRow\(n\)\) return vaultAppRootOf\(n\);/);
    expect(rule).not.toMatch(/vault\.isVaultRow\(n\) \|\| isFxeRow\(n\)\) return null/);
    const root = between('function vaultAppRootOf', 'function openEncryptedInApp');
    expect(root).toMatch(/vault\.isOpen\(root\)/);
  });

  it('opening it tries the app before decrypting a copy for filex’s own viewer', () => {
    const open = between('function openNode', 'const VIEW_DEFAULT_EXTS');
    expect(open).toMatch(/if \(vault\.isVaultRow\(n\) && n\.type === 'file'\) \{\s*if \(openEncryptedInApp\(n, null, 'edit'\)\) return;\s*void e2eOpenPreview\(n\);/);
    const look = between('function previewNode', 'function openNodeInNewTab');
    expect(look).toMatch(/if \(vault\.isVaultRow\(n\) && n\.type === 'file'\) \{\s*if \(openEncryptedInApp\(n, null, 'view'\)\) return;/);
    const chosen = between('function openWithChosen', 'const stopFollowingOpenWith');
    expect(chosen).toMatch(/\(e2eUnlocked\.value \|\| vault\.isVaultRow\(n\)\) && e2ePlainRootOf\(n\)/);
  });

  it('the app’s door carries the vault’s half, over the vault engine', () => {
    expect(src).toMatch(/const appE2eDoor = createAppPlaintextDoor\([\s\S]*?createAppVaultDoor\(vault\),\s*\);/);
    const facade = core('composables/useE2eVault.ts');
    expect(facade).toMatch(/readForApp: async/);
    expect(facade).toMatch(/saveForApp: async/);
  });

  it('every lock closes the app and says what it had not saved', () => {
    expect(between('function e2eLock', '// Extension → preview MIME')).toMatch(/const unsaved = closePreviewOnLock\(root\);/);
    expect(between('function vaultDropKeys', 'function vaultLocked')).toMatch(/vaultLockUnsaved = closePreviewOnLock\(root\);/);
    expect(between('function vaultLocked', 'function vaultChanged')).toMatch(/const unsaved = vaultLockUnsaved;/);
    const close = between('function closePreviewOnLock', '/** The vault locked (or was locked)');
    expect(close).toMatch(/const lost = previewAppDirty\.value;/);
    expect(close).toMatch(/t\('e2e\.app_closed_unsaved', \{ name: n\.basename \}\)/);
    expect(src).toMatch(/@app-dirty="onPreviewAppDirty/);
    expect(core('modals/PreviewModal.vue')).toMatch(/function onAppDirty\(v: boolean\): void \{\s*appDirty\.value = v;\s*emit\('app-dirty', v\);/);
  });

  it('a minute before the vault locks itself, an app’s unsaved changes are warned about in the server’s words, and the vault can be kept open', () => {
    expect(src).toMatch(/onLockSoon: \(root, words\) => vaultLockSoon\(root, words\)/);
    const soon = between('function vaultLockSoon', 'function dropLockSoon');
    expect(soon).toMatch(/!previewAppDirty\.value\) return;/);
    expect(soon).toMatch(/message: words\.app_lock_soon,/);
    expect(soon).toMatch(/actionLabel: words\.app_lock_soon_keep,/);
    expect(soon).toMatch(/action: \(\) => vault\.touch\(root\),/);
    // No words of its own: a server that says nothing gets nothing said.
    expect(soon).not.toMatch(/\bt\('/);
    // Saved, or closed: the warning goes.
    expect(between('function onPreviewAppDirty', 'let lockSoonToast')).toMatch(/if \(!dirty\) dropLockSoon\(\);/);
  });

  it('an app saying it holds unsaved changes in a vault is activity there', () => {
    const dirty = between('function onPreviewAppDirty', 'watch(showPreview');
    expect(dirty).toMatch(/if \(dirty && n && vault\.isVaultRow\(n\)\) vault\.touch\(n\.path\);/);
  });
});
