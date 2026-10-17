// The files an app's upload needs beside its manifest (lib/appUploadParts):
// the interface bundle for a `ui` block, the office engine for an
// `engines.office` block (filex 0.57). Only the blocks' presence is read;
// everything else about the manifest is the server's to judge.
import { describe, expect, it } from 'vitest';

import { uploadPartsMissing } from '@/lib/appUploadParts';

const none = { ui: false, engine: false };

describe('uploadPartsMissing', () => {
  it('names the interface and the engine the manifest has blocks for, in field order', () => {
    const m = JSON.stringify({ name: 'office-editor', ui: { bundle: {} }, engines: { office: { sha256: 'ab'.repeat(32) } } });
    expect(uploadPartsMissing(m, none)).toEqual(['ui', 'engine']);
    expect(uploadPartsMissing(m, { ui: true, engine: false })).toEqual(['engine']);
    expect(uploadPartsMissing(m, { ui: false, engine: true })).toEqual(['ui']);
    expect(uploadPartsMissing(m, { ui: true, engine: true })).toEqual([]);
  });

  it('asks for nothing a manifest does not name', () => {
    expect(uploadPartsMissing(JSON.stringify({ name: 'sign', permissions: ['files:read'] }), none)).toEqual([]);
    // `engines` without `office` is the server's refusal to say, not a file to ask for.
    expect(uploadPartsMissing(JSON.stringify({ name: 'x', engines: {} }), none)).toEqual([]);
    expect(uploadPartsMissing(JSON.stringify({ name: 'x', ui: null, engines: { office: null } }), none)).toEqual([]);
  });

  it('leaves a manifest that does not parse, or is not an object, to the server', () => {
    expect(uploadPartsMissing('{ not json', none)).toEqual([]);
    expect(uploadPartsMissing('[]', none)).toEqual([]);
    expect(uploadPartsMissing('"ui"', none)).toEqual([]);
  });
});
