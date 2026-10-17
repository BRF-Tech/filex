// How a CSV is written, between an app's interface and the server (filex
// 0.57, packages/core lib/textDialect; docs/APP-PLUGINS-API.md → CSV files:
// how they are written). The server's header is taken only when it reads as a
// dialect, a read as text decodes in the file's own encoding, and an app's
// declaration goes to the server as four query values, checked for shape.
import { describe, expect, it } from 'vitest';

import { csvDeclarationQuery, decodeIn, textDialectOf } from '@brftech/filex-core/src/lib/textDialect';

describe('textDialectOf', () => {
  it('takes the server’s header when it is a dialect', () => {
    expect(textDialectOf('{"delimiter":";","bom":false,"crlf":true,"encoding":"windows-1254"}')).toEqual({
      delimiter: ';',
      bom: false,
      crlf: true,
      encoding: 'windows-1254',
    });
    expect(textDialectOf('{"delimiter":"\\t","bom":true,"crlf":false,"encoding":"UTF-16LE"}')).toEqual({
      delimiter: '\t',
      bom: true,
      crlf: false,
      encoding: 'utf-16le',
    });
  });

  it('takes nothing that is not one', () => {
    for (const h of [
      null,
      '',
      'not json',
      '[]',
      '{"delimiter":"::","bom":false,"crlf":false,"encoding":"utf-8"}',
      '{"delimiter":",","bom":"no","crlf":false,"encoding":"utf-8"}',
      '{"delimiter":",","bom":false,"crlf":false,"encoding":"utf 8; x"}',
      '{"delimiter":",","bom":false,"crlf":false}',
    ]) {
      expect(textDialectOf(h), String(h)).toBeUndefined();
    }
  });
});

describe('decodeIn', () => {
  it('reads a code page and UTF-16 as the text they hold, the byte order mark not part of it', () => {
    expect(decodeIn(new Uint8Array([0xfe, 0x65, 0x6b, 0x65, 0x72]).buffer, 'windows-1254')).toBe('şeker');
    expect(decodeIn(new Uint8Array([0xff, 0xfe, 0x61, 0x00, 0x3b, 0x00]).buffer, 'utf-16le')).toBe('a;');
    expect(decodeIn(new Uint8Array([0x61]).buffer, 'no-such-encoding')).toBe('a');
  });
});

describe('csvDeclarationQuery', () => {
  it('is the four query values the server reads', () => {
    expect(csvDeclarationQuery({ delimiter: ';', encoding: 'utf-8', bom: false, eol: 'crlf' })).toEqual({
      csv_delimiter: ';',
      csv_encoding: 'utf-8',
      csv_bom: '0',
      csv_eol: 'crlf',
    });
    expect(csvDeclarationQuery({ bom: true })).toEqual({ csv_bom: '1' });
    expect(csvDeclarationQuery(undefined)).toBeUndefined();
    expect(csvDeclarationQuery(null)).toBeUndefined();
  });

  it('is null for what is not a declaration', () => {
    for (const bad of ['semicolon', 7, [], { delimiter: '::' }, { delimiter: ':' }, { encoding: 'utf 8' }, { bom: 'yes' }, { eol: 'cr' }]) {
      expect(csvDeclarationQuery(bad), JSON.stringify(bad)).toBeNull();
    }
  });
});
