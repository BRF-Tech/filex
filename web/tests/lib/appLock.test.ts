// An app plugin's hold on a file: the badge's words, and the 423.
//
// ⚠⚠ The refusal is the part worth pinning. A `423` that falls into the
// generic status map reads "Error (423)", and a caller that folds it into the
// permission branch tells the person "you are not allowed to do this" — which
// is false: they are allowed, a named app is holding the file until a named
// date, and that sentence is the only one they can act on.
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import { anyLocked, lockOf, lockReasonText, lockUntilText, lockWords, lockedRefusal } from '@brftech/filex-core';

/** A stand-in for the explorer's locale helpers. */
const host = {
  t: (key: string, vars: Record<string, string | number> = {}) =>
    Object.entries(vars).reduce((acc, [k, v]) => acc.replaceAll(`{${k}}`, String(v)), {
      'applock.some_app': 'an app',
      'applock.held': '{app} locked this file',
      'applock.held_reason': '{app} locked this file: {reason}',
      'applock.until': '— until {date}',
    }[key] ?? key),
  formatDate: (ms: number | undefined | null) => (ms ? '3 Oct 2026' : ''),
};

describe('the lock a listing row carries', () => {
  it('needs both the flag and a named plugin', () => {
    expect(lockOf({ locked: true, lock: { plugin: 'sign' } })).toEqual({ plugin: 'sign' });
    // The flag without the row, or the row without a name, is not a lock: a
    // badge saying "locked by " is worse than no badge.
    expect(lockOf({ locked: true })).toBeNull();
    expect(lockOf({ locked: true, lock: { plugin: '' } })).toBeNull();
    expect(lockOf({ lock: { plugin: 'sign' } })).toBeNull();
    expect(lockOf(null)).toBeNull();
  });

  it('answers for a whole selection', () => {
    expect(anyLocked([{ locked: false }, { locked: true, lock: { plugin: 'sign' } }])).toBe(true);
    expect(anyLocked([{ locked: false }, {}])).toBe(false);
  });
});

describe('the words', () => {
  it('names the app, the reason and the end — and leaves out what is missing', () => {
    expect(lockWords({ plugin: 'sign' }, host)).toBe('sign locked this file');
    expect(lockWords({ plugin: 'sign', reason: 'out for signature' }, host)).toBe(
      'sign locked this file: out for signature',
    );
    expect(lockWords({ plugin: 'sign', until: '2026-10-03T09:00:00Z' }, host)).toBe(
      'sign locked this file — until 3 Oct 2026',
    );
  });

  it('never prints "undefined" for a part it does not have', () => {
    expect(lockWords({ plugin: '' }, host)).toBe('an app locked this file');
    expect(lockUntilText(null, host)).toBe('');
    expect(lockUntilText('not a date', host)).toBe('');
    expect(lockWords(null, host)).toBe('');
  });
});

describe('the 423 refusal', () => {
  it('reads the server’s own facts out of the body', () => {
    const err = Object.assign(new Error('Error (423)'), {
      status: 423,
      detail: JSON.stringify({
        error: 'locked',
        plugin: 'sign',
        path: 'Documents/nda.pdf',
        reason: 'out for signature',
        until: '2026-10-03T09:00:00Z',
      }),
    });
    expect(lockedRefusal(err)).toEqual({
      plugin: 'sign',
      path: 'Documents/nda.pdf',
      reason: 'out for signature',
      until: '2026-10-03T09:00:00Z',
    });
  });

  it('is still a lock when the body is missing or unreadable', () => {
    // ⚠ The STATUS is the test that matters. "An app is holding this file"
    // beats "Error (423)" even when the server said no more than that.
    expect(lockedRefusal(Object.assign(new Error('x'), { status: 423 }))).toEqual({ plugin: '' });
    expect(lockedRefusal(Object.assign(new Error('x'), { status: 423, detail: '<html>' }))).toEqual({ plugin: '' });
  });

  it('leaves every other failure alone', () => {
    expect(lockedRefusal(Object.assign(new Error('x'), { status: 403 }))).toBeNull();
    expect(lockedRefusal(new Error('boom'))).toBeNull();
    expect(lockedRefusal(null)).toBeNull();
  });
});

// v0.43.0 wave 2: a lock's reason was one string in the language of whoever
// took the lock — a German administrator read "imzalar toplanıyor". A reason
// the app named as a manifest message arrives in every language it wrote.
describe("the reason in the reader's language", () => {
  const lock = {
    plugin: 'sign',
    reason: 'signatures are being collected',
    reason_text: { en: 'signatures are being collected', tr: 'imzalar toplanıyor', de: 'Unterschriften werden gesammelt' },
  };

  it('shows the reader its own words, the plain reason otherwise', () => {
    expect(lockWords(lock, { ...host, locale: 'de' })).toBe('sign locked this file: Unterschriften werden gesammelt');
    expect(lockWords(lock, { ...host, locale: 'tr' })).toBe('sign locked this file: imzalar toplanıyor');
    // A language the app did not write, and no locale at all: English.
    expect(lockWords(lock, { ...host, locale: 'fr' })).toBe('sign locked this file: signatures are being collected');
    expect(lockReasonText({ plugin: 'x', reason: 'plain' }, 'de')).toBe('plain');
  });

  it('reads it out of a 423 body too', () => {
    const body = JSON.stringify({ error: 'locked', plugin: 'sign', reason: 'signatures are being collected', reason_text: lock.reason_text });
    const held = lockedRefusal({ status: 423, detail: body });
    expect(lockWords(held, { ...host, locale: 'de' })).toBe('sign locked this file: Unterschriften werden gesammelt');
  });
});

// v0.43.0: the banner in the details panel said "sign locked this file" —
// the app's manifest NAME, which addresses it and which no other screen ever
// shows. Every list, the install review and the app's own page say
// "e-Signature", so the server now sends the label beside the name.
//
// ⚠⚠ The fixture is the SERVER's own bytes
// (backend/internal/api/handlers/testdata/wire/app-plugin-lock.json, written
// by TestAppPluginWireFixtures): a hand-written lock would only ever agree
// with what this file believes the wire looks like.
describe('the app behind the lock, as a person reads it', () => {
  const wire = JSON.parse(
    readFileSync(
      path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire/app-plugin-lock.json'),
      'utf8',
    ),
  ) as { labelled: Record<string, unknown>; unlabelled: Record<string, unknown> };

  it("says the app's label, in the reader's language", () => {
    expect(lockWords(wire.labelled, host)).toBe(
      'e-Signature locked this file: signatures are being collected — until 3 Oct 2026',
    );
    expect(lockWords(wire.labelled, { ...host, locale: 'tr' })).toBe(
      'e-İmza locked this file: signatures are being collected — until 3 Oct 2026',
    );
    // A language the manifest did not write falls back to English, never to
    // the object and never to the address.
    expect(lockWords(wire.labelled, { ...host, locale: 'fr' })).toContain('e-Signature locked this file');
  });

  it('still makes a sentence for an app it cannot resolve', () => {
    // No label on the wire (the app was removed, or no app runtime): the
    // name, which is what every surface showed before the label existed.
    expect(lockWords(wire.unlabelled, host)).toBe(
      'ghost locked this file: signatures are being collected — until 3 Oct 2026',
    );
    // An empty label is not a name: it must not blank the sentence out.
    expect(lockWords({ plugin: 'sign', plugin_label: {} }, host)).toBe('sign locked this file');
    expect(lockWords({ plugin: '', plugin_label: { en: '' } }, host)).toBe('an app locked this file');
  });

  it('reads the label out of a 423 body too', () => {
    const body = JSON.stringify({ error: 'locked', plugin: 'sign', plugin_label: { en: 'e-Signature' }, path: 'Contracts/a.pdf' });
    expect(lockWords(lockedRefusal({ status: 423, detail: body }), host)).toBe('e-Signature locked this file');
  });
});
