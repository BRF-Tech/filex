// lib/accountRules — the browser's mirror of the server's account rules.
//
// Release-candidate sweep, 2026-09-21: the profile saved
// "bu-bir-eposta-degil" as an e-mail address and said "Profil kaydedildi",
// and a username with "ş" came back after Save as the server's raw English
// (`invalid username: 'ş' is not allowed …`). The forms now say the problem
// under the box while it is typed. What has to stay true:
//
//   · the mirror says what the SERVER would say, in the same order
//     (identity.Check) — a rule only here would refuse a name the server
//     accepts, a rule only there lets through what Save then refuses;
//   · the reserved names are the server's list, read from the Go source, so
//     the two cannot drift apart silently;
//   · a dotless domain is an address (`admin@local` is the first
//     administrator's);
//   · a server refusal names its field, so a form can put it under the box.
import { describe, expect, it } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  RESERVED_USERNAMES,
  USERNAME_MAX,
  USERNAME_MIN,
  emailProblem,
  refusalField,
  usernameProblem,
} from '@/lib/accountRules';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const here = path.dirname(fileURLToPath(import.meta.url));
const IDENTITY_GO = path.resolve(here, '../../../backend/internal/identity/identity.go');

describe('usernameProblem', () => {
  it.each([
    ['', 'usernameEmpty'],
    ['   ', 'usernameEmpty'],
    ['ada@example.com', 'usernameAt'],
    ['ab', 'usernameShort'],
    ['a'.repeat(33), 'usernameLong'],
    ['9lives', 'usernameDigit'],
    // ⚠ Three BYTES, as Go's len counts them: the server says "starts with a
    // digit" here, not "too short", and so must the form.
    ['9ş', 'usernameDigit'],
    ['ayşe', 'usernameChar'],
    ['ayse yilmaz', 'usernameSpace'],
    ['admin', 'usernameReserved'],
    ['Root', 'usernameReserved'],
  ])('%j → %s', (raw, key) => {
    expect(usernameProblem(raw)?.key).toBe(key);
  });

  // The first administrator holds the reserved "admin" (server
  // identity.ClaimBootstrap). Its settings form sends the name with every
  // save; the name an account HOLDS is never refused as reserved — only
  // claiming one is.
  it('does not refuse the name the account already holds', () => {
    expect(usernameProblem('admin', 'admin')).toBeNull();
    expect(usernameProblem(' Admin ', 'admin')).toBeNull();
    expect(usernameProblem('admin', 'ada')?.key).toBe('usernameReserved');
    expect(usernameProblem('root', 'admin')?.key).toBe('usernameReserved');
  });

  it('names the first character that is not allowed', () => {
    expect(usernameProblem('ayşe.yılmaz')).toEqual({ key: 'usernameChar', params: { char: 'ş' } });
  });

  it('accepts what the server accepts, folded the way it folds it', () => {
    for (const ok of ['kaya', 'Kaya', ' ada.l ', 'a-b_c.d', 'x'.repeat(USERNAME_MAX), 'abc']) {
      expect(usernameProblem(ok), ok).toBeNull();
    }
  });

  it('keeps the bounds and the reserved names the server has', () => {
    const src = fs.readFileSync(IDENTITY_GO, 'utf8');
    expect(Number(/MinLen\s*=\s*(\d+)/.exec(src)?.[1])).toBe(USERNAME_MIN);
    expect(Number(/MaxLen\s*=\s*(\d+)/.exec(src)?.[1])).toBe(USERNAME_MAX);

    const block = /var reserved = map\[string\]bool\{([\s\S]*?)\n\}/.exec(src)?.[1] ?? '';
    const fromGo = [...block.matchAll(/"([^"]+)":\s*true/g)].map((m) => m[1]).sort();
    // A parser that matches nothing would make the comparison a tautology.
    expect(fromGo.length, `no reserved names parsed out of ${IDENTITY_GO}`).toBeGreaterThan(10);
    expect([...RESERVED_USERNAMES].sort()).toEqual(fromGo);
  });
});

describe('emailProblem', () => {
  it.each([
    ['', 'emailRequired'],
    ['  ', 'emailRequired'],
    ['bu-bir-eposta-degil', 'emailInvalid'],
    ['@example.com', 'emailInvalid'],
    ['ada@', 'emailInvalid'],
    ['a@b@c', 'emailInvalid'],
    ['ada lovelace@example.com', 'emailInvalid'],
    ['Ada <ada@example.com>', 'emailInvalid'],
  ])('%j → %s', (raw, key) => {
    expect(emailProblem(raw)?.key).toBe(key);
  });

  it('accepts an address, a dotless one included', () => {
    for (const ok of ['ada@example.com', ' ada@example.com ', 'admin@local', 'a.b+tag@sub.example.co']) {
      expect(emailProblem(ok), ok).toBeNull();
    }
  });
});

describe('refusalField', () => {
  it('reads the field and the sentence the server wrote for the reader', () => {
    const err = { response: { status: 409, data: { error: 'email_taken', field: 'email', message: 'Bu adres başka bir hesaba ait.' } } };
    expect(refusalField(err)).toEqual({ field: 'email', message: 'Bu adres başka bir hesaba ait.' });
  });

  it('is null for a refusal that names no field', () => {
    expect(refusalField({ response: { data: { error: 'boom' } } })).toBeNull();
    expect(refusalField(new Error('network'))).toBeNull();
    expect(refusalField(undefined)).toBeNull();
  });
});

describe('the sentences', () => {
  it('exist in both languages, with the Turkish alphabet', () => {
    const keys = Object.keys(en.account.errors);
    expect(Object.keys(tr.account.errors).sort()).toEqual(keys.sort());
    for (const k of keys) {
      expect((tr.account.errors as Record<string, string>)[k], k).not.toBe(
        (en.account.errors as Record<string, string>)[k],
      );
    }
    // No ASCII-folded Turkish ("Kullanici adi", "degil").
    const all = Object.values(tr.account.errors).join(' ');
    expect(all).toContain('Kullanıcı adı');
    expect(all).toContain('değil');
  });
});
