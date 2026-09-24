// surfaceValues — what a plugin surface's nodes hold, seeded and mapped
// without a DOM. The modal and the inspector section both post `data.values`
// from this seeding, so a wrong seed here is a wrong event in both.
import { describe, expect, it } from 'vitest';

import {
  initialValues,
  looksLikeEmail,
  pinLength,
  storageFieldOf,
} from '@brftech/filex-core/src/lib/surfaceValues';
import type { SurfaceNode } from '@brftech/filex-core/src/types/Plugins';

describe('initialValues', () => {
  it('seeds form fields FLAT by key, from values then defaults', () => {
    const nodes: SurfaceNode[] = [
      {
        id: 'f',
        type: 'form',
        props: {
          fields: [
            { key: 'quality', type: 'int', default: 80 },
            { key: 'name', type: 'string', default: 'x' },
            { key: 'none', type: 'string' },
          ],
          values: { quality: 85 },
        },
      },
    ];
    expect(initialValues(nodes)).toEqual({ quality: 85, name: 'x' });
  });

  it('seeds the value-holding nodes under their ids, walking rows', () => {
    const nodes: SurfaceNode[] = [
      {
        type: 'row',
        children: [
          { id: 'to', type: 'people-picker', props: { value: [{ email: 'a@b.c' }, { nope: true }] } },
          { id: 'src', type: 'file-chooser', props: { value: 'docs://x.pdf' } },
          { id: 'pin', type: 'pin-input', props: { length: 6 } },
          { type: 'people-picker', props: { value: [{ email: 'noid@b.c' }] } },
        ],
      },
      { type: 'text', props: { text: 'hi' } },
    ];
    expect(initialValues(nodes)).toEqual({ to: [{ email: 'a@b.c' }], src: 'docs://x.pdf', pin: '' });
  });
});

describe('storageFieldOf', () => {
  it('a `text` field is a LONG one — the contract says so, the renderer needs `multiline`', () => {
    // The signing app asks for its signers in a `text` field whose help line
    // reads "one signer per line". It arrived as a single-line input.
    const long = storageFieldOf({ key: 'identities', type: 'text', label: { en: 'Identity' } }, 'en');
    expect(long.multiline).toBe(true);
    const short = storageFieldOf({ key: 'name', type: 'string', label: { en: 'Name' } }, 'en');
    expect(short.multiline).toBe(false);
  });

  it('reads Text labels in the locale, masks secrets, keeps the catalogue out of it', () => {
    const f = storageFieldOf(
      {
        key: 'api_key',
        type: 'string',
        secret: true,
        label: { en: 'API key', tr: 'API anahtarı' },
        help: { en: 'From the console' },
        required: true,
        options: [{ value: 'a', label: { en: 'Alpha' } }],
      },
      'tr',
    );
    expect(f.type).toBe('password');
    expect(f.label).toBe('API anahtarı');
    expect(f.help).toBe('From the console');
    expect(f.i18n_key).toBe('');
    expect(f.required).toBe(true);
    expect(f.options).toEqual([{ value: 'a', label: 'Alpha' }]);
  });

  it('falls back to the key and to `string` for an unknown type', () => {
    const f = storageFieldOf({ key: 'k', type: 'weird' }, 'en');
    expect(f.label).toBe('k');
    expect(f.type).toBe('string');
  });
});

describe('pinLength', () => {
  it('clamps to the contract: 4..8, default 6', () => {
    expect(pinLength(undefined)).toBe(6);
    expect(pinLength('nope')).toBe(6);
    expect(pinLength(2)).toBe(4);
    expect(pinLength(12)).toBe(8);
    expect(pinLength(5.7)).toBe(5);
  });
});

describe('looksLikeEmail', () => {
  it('accepts an address and refuses the obvious non-addresses', () => {
    expect(looksLikeEmail('ayse@example.com')).toBe(true);
    expect(looksLikeEmail('  a@b ')).toBe(true);
    expect(looksLikeEmail('nope')).toBe(false);
    expect(looksLikeEmail('@x')).toBe(false);
    expect(looksLikeEmail('a@')).toBe(false);
    expect(looksLikeEmail('a@@b')).toBe(false);
  });
});
