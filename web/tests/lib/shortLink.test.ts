// lib/shortLink: a public link's token from its address, and an address as a
// person reads it (filex 0.60, docs/SHARING.md → Short links).
import { describe, expect, it } from 'vitest';

import { displayLink, linkTokenOf } from '@brftech/filex-core/src/lib/shortLink';

describe('linkTokenOf', () => {
  it('reads the token of a share and of a file request, with or without a base path', () => {
    expect(linkTokenOf('https://files.example/s/AbC_12-x')).toBe('AbC_12-x');
    expect(linkTokenOf('https://files.example/d/tok9')).toBe('tok9');
    expect(linkTokenOf('https://example.com/filex/s/tok9/')).toBe('tok9');
    expect(linkTokenOf('/s/rel')).toBe('rel');
  });
  it('is empty for anything that is not a public link', () => {
    expect(linkTokenOf('')).toBe('');
    expect(linkTokenOf(undefined)).toBe('');
    expect(linkTokenOf('https://files.example/drive/explore')).toBe('');
    expect(linkTokenOf('https://files.example/s/tok/f/a.txt')).toBe('');
  });
});

describe('displayLink', () => {
  it('shows emoji and letters decoded, keeps what decodeURI keeps escaped', () => {
    expect(displayLink('https://l.example.com/%F0%9F%98%80%C3%A7')).toBe('https://l.example.com/\u{1F600}ç');
    expect(displayLink('https://l.example.com/a%2Fb')).toBe('https://l.example.com/a%2Fb');
  });
  it('gives back an address it cannot decode as it came', () => {
    expect(displayLink('https://l.example.com/%E0%A4%A')).toBe('https://l.example.com/%E0%A4%A');
    expect(displayLink('')).toBe('');
  });
});
