/**
 * textDialect - how a CSV is written, between an app's interface and the
 * server (filex 0.57; docs/APP-PLUGINS-API.md → CSV files: how they are
 * written).
 *
 *  - Reading: the server reads a `.csv`'s delimiter, byte order mark, line
 *    ends and encoding from its bytes and says them on the read's answer
 *    (`X-Filex-Text-Dialect`, JSON). AppFrame hands them to the interface as
 *    `ReadResult.text_dialect`, and a read `as: 'text'` decodes the file in
 *    its own encoding (a windows-1254 file is not read as UTF-8).
 *  - Saving: the interface may say how the CSV it saves is written
 *    (`SaveParams.csv`). It goes to the server beside the save
 *    (`csv_delimiter`, `csv_encoding`, `csv_bom`, `csv_eol`), which checks it
 *    against the bytes and refuses a save they contradict.
 */
import type { CsvDeclaration, TextDialect } from '@brftech/filex-app-ui/protocol';

/** The read's answer header the dialect comes in. */
export const TEXT_DIALECT_HEADER = 'X-Filex-Text-Dialect';

const DELIMITERS = [',', ';', '\t', '|'];
/** An encoding's name as TextDecoder takes it (`utf-8`, `windows-1254`). */
const ENCODING_NAME = /^[a-z0-9_-]{1,40}$/i;

/** The server's dialect header as a TextDialect; undefined when there is none or it does not read as one. */
export function textDialectOf(header: string | null | undefined): TextDialect | undefined {
  if (!header) return undefined;
  let v: unknown;
  try {
    v = JSON.parse(header);
  } catch {
    return undefined;
  }
  const d = v as Partial<TextDialect> | null;
  if (
    !d ||
    typeof d !== 'object' ||
    typeof d.delimiter !== 'string' ||
    !DELIMITERS.includes(d.delimiter) ||
    typeof d.bom !== 'boolean' ||
    typeof d.crlf !== 'boolean' ||
    typeof d.encoding !== 'string' ||
    !ENCODING_NAME.test(d.encoding)
  ) {
    return undefined;
  }
  return { delimiter: d.delimiter, bom: d.bom, crlf: d.crlf, encoding: d.encoding.toLowerCase() };
}

/**
 * Bytes as text in the encoding named (its byte order mark not part of the
 * text). UTF-8 when the name is one this browser does not know.
 */
export function decodeIn(bytes: ArrayBuffer, encoding: string): string {
  try {
    return new TextDecoder(encoding).decode(bytes);
  } catch {
    return new TextDecoder().decode(bytes);
  }
}

/**
 * An interface's `csv` save option as the save's query values, checked for
 * its shape; undefined when it gave none, null when it is not one (the
 * bridge answers `invalid`).
 */
export function csvDeclarationQuery(decl: unknown): Record<string, string> | undefined | null {
  if (decl === undefined || decl === null) return undefined;
  if (typeof decl !== 'object' || Array.isArray(decl)) return null;
  const d = decl as CsvDeclaration;
  const q: Record<string, string> = {};
  if (d.delimiter !== undefined) {
    if (typeof d.delimiter !== 'string' || !DELIMITERS.includes(d.delimiter)) return null;
    q.csv_delimiter = d.delimiter;
  }
  if (d.encoding !== undefined) {
    if (typeof d.encoding !== 'string' || !ENCODING_NAME.test(d.encoding)) return null;
    q.csv_encoding = d.encoding;
  }
  if (d.bom !== undefined) {
    if (typeof d.bom !== 'boolean') return null;
    q.csv_bom = d.bom ? '1' : '0';
  }
  if (d.eol !== undefined) {
    if (d.eol !== 'crlf' && d.eol !== 'lf') return null;
    q.csv_eol = d.eol;
  }
  return q;
}
