// The ShareX template the docs hand out (docs/uploaders/filex.sxcu, for a
// ShareX set up by hand on any filex server) is the file the server makes for
// an uploader, with placeholders where the server writes its address and the
// key.
//
// The server's own bytes are written and checked by the Go test
// backend/internal/api/handlers/uploaders_template_test.go, from
// buildUploaderSetup, into the module's testdata. This test holds the docs
// copy to those bytes, and those bytes to what the web app writes when it
// saves the same settings (packages/core UploadersPanel.vue:
// JSON.stringify(setup.sharex, null, 2) + '\n'), so the template cannot drift
// from what a filex server hands out.
//
// ⚠ Why the bond is in two halves: a Go test reads nothing outside the backend
// module (web/tests/deploy/goWslMirror.test.ts). The Go gates run on a copy of
// backend/ alone, and #334's first version, a Go test reading docs/, skipped
// in every such copy and turned that test red on main (2026-11-12).
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '../../..');
const DOCS_COPY = 'docs/uploaders/filex.sxcu';
const SERVERS = 'backend/internal/api/handlers/testdata/uploaders/filex.sxcu';
// A checkout with autocrlf hands back CRLF; git stores both files LF.
const read = (rel: string) => readFileSync(path.join(REPO, rel), 'utf8').split('\r\n').join('\n');

describe('the ShareX template the docs hand out', () => {
  it('is the file the server makes, byte for byte', () => {
    expect(read(DOCS_COPY), `${DOCS_COPY} is not ${SERVERS}: copy the server's file over it`).toBe(read(SERVERS));
  });

  it('is what the web app saves for those settings', () => {
    const server = read(SERVERS);
    expect(JSON.stringify(JSON.parse(server), null, 2) + '\n').toBe(server);
  });

  it('carries the placeholders, not an address or a key', () => {
    const doc = JSON.parse(read(DOCS_COPY));
    expect(doc.RequestURL).toBe('https://YOUR-FILEX-HOST/api/uploads');
    expect(doc.Headers.Authorization).toBe('Bearer fxu_YOUR_KEY');
  });
});
