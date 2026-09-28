// Single encrypted files in the explorer's shared pieces: which menu rows a
// selection gets (composables/useE2eFiles), the padlock tile and the type
// words (lib/fileIcons), and the name a row is drawn with once the file was
// opened in the tab (useLocale nodeDisplayName). The flows themselves are
// measured in a real browser: e2e/tests/173-e2e-single-file.spec.ts.

import { describe, expect, it } from 'vitest';
import { createApp, defineComponent, h } from 'vue';

import { useE2eFiles, isFxeActionKey, isFxeRow, numberedName } from '../../../packages/core/src/composables/useE2eFiles';
import { useLocale } from '../../../packages/core/src/composables/useLocale';
import { fileIconTile, isEncryptedFile, typeLabelKey } from '../../../packages/core/src/lib/fileIcons';
import type { FileNode } from '../../../packages/core/src/types/FileNode';

function files() {
  let api!: ReturnType<typeof useE2eFiles>;
  const app = createApp(
    defineComponent({
      setup() {
        api = useE2eFiles({
          api: {} as never,
          chunked: { uploadFile: async () => ({ id: 'x' }), threshold: () => 8 << 20 },
          locale: () => 'en',
          t: (k) => k,
          toast: () => undefined,
          emitError: () => undefined,
          escrowPublicKey: () => null,
          registerOp: () => undefined,
          reload: async () => undefined,
          openPreview: () => undefined,
          showRecoveryKey: () => undefined,
        });
        return () => h('div');
      },
    }),
  );
  app.mount(document.createElement('div'));
  return { api, unmount: () => app.unmount() };
}

const file = (basename: string, extra: Partial<FileNode> = {}): FileNode => ({
  path: `s://${basename}`,
  basename,
  type: 'file',
  extension: basename.includes('.') ? basename.split('.').pop()!.toLowerCase() : '',
  size: 10,
  ...extra,
});

function shown(rows: Array<{ key: string; hidden?: boolean }>): string[] {
  return rows.filter((r) => !r.hidden).map((r) => r.key);
}

describe('single encrypted files — the menu', () => {
  it('a plain file you may write offers "Encrypt with E2EE…", nothing else of ours', () => {
    const { api, unmount } = files();
    const rows = api.menuRows([file('Rapor.pdf')], { canWrite: true, inEncrypted: false, unlockedEncryptedCopy: false });
    expect(shown(rows)).toEqual(['fxe-encrypt']);
    for (const r of rows) expect(isFxeActionKey(r.key)).toBe(true);
    unmount();
  });

  it('not inside an encrypted folder, not read-only, not a folder, not several', () => {
    const { api, unmount } = files();
    const ctx = { canWrite: true, inEncrypted: false, unlockedEncryptedCopy: false };
    expect(shown(api.menuRows([file('a.txt')], { ...ctx, inEncrypted: true }))).toEqual([]);
    expect(shown(api.menuRows([file('a.txt', { e2e_root: 's://Kasa' })], ctx))).toEqual([]);
    expect(shown(api.menuRows([file('a.txt')], { ...ctx, canWrite: false }))).toEqual([]);
    expect(shown(api.menuRows([{ ...file('dir'), type: 'dir' }], ctx))).toEqual([]);
    expect(shown(api.menuRows([file('a.txt'), file('b.txt')], ctx))).toEqual([]);
    unmount();
  });

  it('a .fxe offers its own verbs — and is never encrypted twice', () => {
    const { api, unmount } = files();
    const ctx = { canWrite: true, inEncrypted: false, unlockedEncryptedCopy: false };
    expect(shown(api.menuRows([file('Rapor.pdf.fxe')], ctx))).toEqual(['fxe-download-raw', 'fxe-password', 'fxe-remove']);
    expect(shown(api.menuRows([file('Rapor.pdf.fxe')], { ...ctx, canWrite: false }))).toEqual(['fxe-download-raw']);
    unmount();
  });

  it('inside an unlocked encrypted folder, the ciphertext copy is a second verb', () => {
    const { api, unmount } = files();
    const rows = api.menuRows([file('a.txt'), file('b.txt')], { canWrite: true, inEncrypted: true, unlockedEncryptedCopy: true });
    expect(shown(rows)).toEqual(['e2e-download-encrypted']);
    unmount();
  });

  it('isFxeRow reads the STORED name; numberedName keeps the extension', () => {
    expect(isFxeRow(file('x.fxe'))).toBe(true);
    expect(isFxeRow(file('Rapor.pdf', { e2e_stored: 'x.fxe' }))).toBe(true);
    expect(isFxeRow({ ...file('x.fxe'), type: 'dir' })).toBe(false);
    expect(numberedName('Rapor 2027.pdf', 2)).toBe('Rapor 2027 (2).pdf');
    expect(numberedName('README', 3)).toBe('README (3)');
  });
});

describe('single encrypted files — how a row looks', () => {
  it('a .fxe gets the padlock tile and says what it is', () => {
    expect(isEncryptedFile(file('a.pdf.fxe'))).toBe(true);
    expect(isEncryptedFile(file('a.pdf'))).toBe(false);
    expect(fileIconTile(file('a.pdf.fxe'))).toContain('fe-ftile--file-locked');
    expect(fileIconTile(file('a.pdf'))).not.toContain('file-locked');
    expect(typeLabelKey(file('a.pdf.fxe'))).toBe('e2e.fxe.type');
  });

  it('once opened in the tab, the row is drawn with its real name; the stored name stays basename', () => {
    const { nodeDisplayName } = useLocale(() => 'en');
    const row = file('encrypted-3fa2c1d0.fxe');
    expect(nodeDisplayName(row)).toBe('encrypted-3fa2c1d0.fxe');
    expect(nodeDisplayName({ ...row, fxe_name: 'Bütçe 2027.xlsx' })).toBe('Bütçe 2027.xlsx');
  });
});
