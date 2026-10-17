// What an app's `file.save` asks beyond its bytes (filex 0.57, packages/core
// lib/appSaveAside; docs/APP-PLUGINS-API.md → Saving beside the file,
// conflict copies): only the shape is checked in the page, the rule is the
// server's; and the version a read hands over, as a save's `expect`.
import { describe, expect, it } from 'vitest';

import { saveAsideQuery, versionOf } from '@brftech/filex-core/src/lib/appSaveAside';
import { decodeIn } from '@brftech/filex-core/src/lib/textDialect';

describe('saveAsideQuery', () => {
  it('takes an extension and copy, nothing else', () => {
    expect(saveAsideQuery({})).toEqual({});
    expect(saveAsideQuery(undefined)).toEqual({});
    expect(saveAsideQuery({ beside: 'docx' })).toEqual({ beside: 'docx' });
    expect(saveAsideQuery({ beside: '.XLSX', conflict: 'copy' })).toEqual({ beside: 'xlsx', conflict: 'copy' });
    expect(saveAsideQuery({ conflict: 'copy' })).toEqual({ conflict: 'copy' });
  });

  it('refuses what is not one (the bridge answers invalid)', () => {
    for (const bad of [{ beside: 7 }, { beside: 'do cx' }, { beside: '../x' }, { conflict: true }, { conflict: 'yes' }]) {
      expect(saveAsideQuery(bad)).toBeNull();
    }
  });
});

describe('versionOf', () => {
  it('reads "<size>:<ms>" and nothing else', () => {
    expect(versionOf('2048:1760000000123')).toEqual({ size: 2048, modified: 1760000000123 });
    expect(versionOf(' 0:5 ')).toEqual({ size: 0, modified: 5 });
    for (const bad of [null, undefined, '', 'x:1', '1', '-1:2', '1:2:3']) expect(versionOf(bad)).toBeUndefined();
  });
});

describe('the Greek and Baltic code pages a .csv may be in (filex 0.57)', () => {
  it('are read by the browser under the names the server gives them', () => {
    expect(decodeIn(new Uint8Array([0xd0, 0xdc, 0xf4, 0xf1, 0xe1]).buffer, 'windows-1253')).toBe('Πάτρα');
    expect(decodeIn(new Uint8Array([0xd0, 0x69, 0x61, 0x75, 0x6c, 0x69, 0xf8]).buffer, 'windows-1257')).toBe('Šiaulių');
  });
});
