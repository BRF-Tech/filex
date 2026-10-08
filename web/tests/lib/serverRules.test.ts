// lib/serverRules — the server's rules, held once for every surface (#211).
//
// The explorer, the preview, the tag picker, the comment box and an app's
// kept state used to carry their own copies of a server rule; now they read
// what `/api/files/capabilities` says. What is pinned here is the holding:
// an answer from the server is taken whole, an answer that says nothing does
// not wipe it, and the lookups answer from it alone.
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  clipRunes,
  editKindsKnown,
  isOfficeExt,
  isTextEditable,
  isTextualMime,
  resetServerRules,
  runeCount,
  serverLimit,
  takeServerRules,
} from '@brftech/filex-core/src/lib/serverRules';
import { AppStateTooLarge, appStateSet } from '@brftech/filex-core/src/lib/appState';

const KINDS = {
  office: ['docx', 'docm'],
  text: ['md', 'properties', 'gitignore'],
  text_names: ['makefile', '.env'],
  text_mime_prefixes: ['text/'],
  text_mimes: ['application/json'],
};

afterEach(() => {
  vi.useRealTimers();
  resetServerRules();
});

describe('holding the server’s answer', () => {
  it('knows nothing until the server has said, and then only what it said', () => {
    resetServerRules();
    expect(editKindsKnown()).toBe(false);
    expect(isOfficeExt('docx'), 'no guess before the answer').toBe(false);
    expect(isTextEditable({ basename: 'a.md', extension: 'md' })).toBe(false);

    takeServerRules({ edit_kinds: KINDS });
    expect(editKindsKnown()).toBe(true);
    expect(isOfficeExt('DOCM')).toBe(true);
    expect(isOfficeExt('rtf'), 'not in this answer').toBe(false);
  });

  it('an answer without the rules keeps the rules it had', () => {
    takeServerRules({ edit_kinds: KINDS, limits: { tag_max_runes: 64 } });
    takeServerRules({});
    takeServerRules({ edit_kinds: null, limits: null });
    expect(isOfficeExt('docx')).toBe(true);
    expect(serverLimit('tag_max_runes')).toBe(64);
  });

  it('a number the server did not send is unknown, not zero', () => {
    takeServerRules({ limits: { tag_max_runes: 64 } });
    expect(serverLimit('comment_max_runes')).toBeUndefined();
  });
});

describe('is this text a person edits', () => {
  it('by extension, by a name that is text on its own, and by the server’s mime', () => {
    takeServerRules({ edit_kinds: KINDS });
    expect(isTextEditable({ basename: 'app.properties', extension: 'properties' })).toBe(true);
    expect(isTextEditable({ basename: 'Makefile', extension: '' })).toBe(true);
    expect(isTextEditable({ basename: '.gitignore' })).toBe(true);
    expect(isTextEditable({ basename: 'LICENSE', extension: '', mime_type: 'text/plain; charset=utf-8' })).toBe(true);
    expect(isTextEditable({ basename: 'blob', extension: '', mime_type: 'application/octet-stream' })).toBe(false);
    expect(isTextEditable({ basename: 'a.docx', extension: 'docx' })).toBe(false);
  });

  it('a mime is text by the server’s prefixes and list', () => {
    takeServerRules({ edit_kinds: KINDS });
    expect(isTextualMime('text/x-shellscript')).toBe(true);
    expect(isTextualMime('Application/JSON')).toBe(true);
    expect(isTextualMime('application/zip')).toBe(false);
    expect(isTextualMime('')).toBe(false);
  });
});

describe('characters, as the server counts them', () => {
  it('an emoji is one character, not two UTF-16 units', () => {
    expect(runeCount('a😀b')).toBe(3);
    expect(clipRunes('😀😀😀', 2)).toBe('😀😀');
    expect(clipRunes('abc', undefined)).toBe('abc');
  });
});

describe('an app’s kept state (lib/appState, audit B19)', () => {
  it('is held to the server’s number, and to none when the server sends none', () => {
    vi.useFakeTimers();
    const big = { blob: 'x'.repeat(200) };
    takeServerRules({ limits: { app_state_max_bytes: 100 } });
    expect(() => appStateSet('sketch', 'k', big)).toThrow(AppStateTooLarge);

    resetServerRules();
    expect(() => appStateSet('sketch', 'k', big), 'no number: the server judges the save').not.toThrow();
  });
});
