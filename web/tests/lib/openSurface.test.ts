// A document the person has just created opens ONCE (found with #56).
//
// On the desktop (`config.openInHost`) every open goes to the host's own
// window: the explorer emits `file-opened` and stops. onDocumentCreated used to
// open the in-page viewer and then emit as well, so a new document came up
// twice — over the explorer and in its own window. The decision lives in one
// pure function (lib/openSurface.ts); FileExplorer is too large to mount here,
// so the second half reads its source and pins that the created-document path
// asks that function before it opens anything in the page.

import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { openSurface } from '@brftech/filex-core/src/lib/openSurface';

describe('openSurface', () => {
  it('a file goes to the host window when the host owns opening', () => {
    expect(openSurface({ openInHost: true }, { type: 'file' })).toBe('host');
  });
  it('a folder never does — it navigates in the page', () => {
    expect(openSurface({ openInHost: true }, { type: 'dir' })).toBe('page');
  });
  it('without a host every file opens in the page', () => {
    expect(openSurface({}, { type: 'file' })).toBe('page');
    expect(openSurface({ openInHost: false }, { type: 'file' })).toBe('page');
  });
});

describe('a created document on the desktop', () => {
  const src = fs.readFileSync(
    path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'),
    'utf8',
  );
  const start = src.indexOf('async function onDocumentCreated(');
  const body = src.slice(start, src.indexOf('\n}\n', start));

  it('asks openSurface and returns before the in-page viewer opens', () => {
    expect(start).toBeGreaterThan(-1);
    const ask = body.indexOf("openSurface(props.config, node) === 'host'");
    const page = body.indexOf('showPreview.value = true');
    expect(ask, 'onDocumentCreated consults openSurface').toBeGreaterThan(-1);
    expect(page).toBeGreaterThan(ask);
  });

  it('both branches still announce the open (file-opened is a public event)', () => {
    const ask = body.indexOf("openSurface(props.config, node) === 'host'");
    const page = body.indexOf('showPreview.value = true');
    expect(body.slice(ask, page).includes("emit('file-opened'"), 'the host branch emits').toBe(true);
    expect(body.slice(page).includes("emit('file-opened'"), 'the in-page branch emits').toBe(true);
  });
});
