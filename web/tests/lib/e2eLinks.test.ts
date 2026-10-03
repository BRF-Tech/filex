// Share is never offered for an end-to-end encrypted folder or anything in
// it - the folder's OWN row in its parent's listing included (0.50, owner's
// call): the server refuses those links on every door with 409
// E2E_ENCRYPTED (backend publicLinkRefusal), and a file request on the folder
// would store a visitor's upload in it unencrypted. A single encrypted file
// (`.fxe`) outside any folder is still shared, as it is.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { publicLinksOff } from '../../../packages/core/src/lib/e2eLinks';

describe('lib/e2eLinks publicLinksOff', () => {
  it('off inside an encrypted folder, whatever the rows say', () => {
    expect(publicLinksOff([{}], true)).toBe(true);
    expect(publicLinksOff([], true)).toBe(true);
  });

  it("off for the encrypted folder's own row in its parent's listing", () => {
    expect(publicLinksOff([{ e2e: true }], false)).toBe(true);
    expect(publicLinksOff([{}, { e2e: true }], false)).toBe(true);
  });

  it('off for a row from Recent, Starred, a tag view or a search that sits in one', () => {
    expect(publicLinksOff([{ e2e_root: 'docs://kasa' }], false)).toBe(true);
  });

  it('on for ordinary rows, and for a single encrypted file outside any folder', () => {
    expect(publicLinksOff([{}], false)).toBe(false);
    expect(publicLinksOff([{ e2e: false, e2e_root: '' }], false)).toBe(false);
    // A `.fxe` row carries neither mark: it is its own root.
    expect(publicLinksOff([{ e2e_root: undefined }], false)).toBe(false);
  });
});

describe('the explorer asks it for its Share row', () => {
  const src = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

  it('the access row is hidden by publicLinksOff over the selection and the folder', () => {
    expect(src).toMatch(/key: 'access',[\s\S]{0,120}hidden: !any \|\| !w \|\| publicLinksOff\(sel, e2eActive\.value\)/);
  });

  it('the Share shortcut follows the menu (offeredOn), so it is hidden there too', () => {
    expect(src).toMatch(/offeredOn\('access', targets\)/);
  });
});
