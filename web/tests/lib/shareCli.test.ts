// The one-line curl shown next to a fresh share link.
//
// ⚠ The helper lives in @brftech/filex-core (both the standalone share dialog
// and the Share / Permissions panel render it, and the point of extracting it
// was that they cannot drift). The core package has no test runner of its own,
// and this is a pure function — so it is exercised here, in the app that ships
// it, rather than left unchecked or paid for with a new toolchain.
//
// A wrong command is worse than no command: it is copied into a terminal on a
// server and fails there, where nobody can see why.
import { describe, it, expect } from 'vitest';
import { shareCliCommand, shQuote } from '@brftech/filex-core/src/lib/shareCli';

describe('shareCliCommand', () => {
  it('is empty without a link', () => {
    expect(shareCliCommand(null)).toBe('');
    expect(shareCliCommand({ url: '' })).toBe('');
  });

  it('names the output file when we know it', () => {
    expect(shareCliCommand({ url: 'https://files.example.com/s/abc', filename: 'q3.pdf' })).toBe(
      "curl -fSL -o 'q3.pdf' 'https://files.example.com/s/abc'",
    );
  });

  it('falls back to the server-supplied name', () => {
    // -OJ takes the name from Content-Disposition — the only honest choice
    // when the caller does not know it.
    expect(shareCliCommand({ url: 'https://files.example.com/s/abc' })).toBe(
      "curl -fSL -OJ 'https://files.example.com/s/abc'",
    );
  });

  it('carries the PIN in the querystring', () => {
    const cmd = shareCliCommand({ url: 'https://files.example.com/s/abc', pin: '12345678', filename: 'q3.pdf' });
    expect(cmd).toContain('?pin=12345678');
  });

  it('appends the PIN with & when the URL already has a query', () => {
    const cmd = shareCliCommand({ url: 'https://files.example.com/s/abc?x=1', pin: '99' });
    expect(cmd).toContain('?x=1&pin=99');
  });

  it('targets the ZIP for a folder link, and names it .zip', () => {
    // A folder's bare URL serves a browse PAGE; ?zip=wait blocks until the
    // archive is built and then streams it. Without this the command would
    // save an HTML page called "reports".
    const cmd = shareCliCommand({ url: 'https://files.example.com/s/abc', filename: 'reports', isDir: true });
    expect(cmd).toContain('?zip=wait');
    expect(cmd).toContain("-o 'reports.zip'");
  });

  it('keeps -L, because an S3-backed instance answers with a redirect', () => {
    expect(shareCliCommand({ url: 'https://files.example.com/s/abc' })).toMatch(/^curl -fSL /);
  });

  it('quotes for a POSIX shell, including embedded quotes', () => {
    expect(shQuote("it's")).toBe(`'it'\\''s'`);
    const cmd = shareCliCommand({ url: 'https://files.example.com/s/abc', filename: "burak's file.pdf" });
    expect(cmd).toContain(`'burak'\\''s file.pdf'`);
  });
});
