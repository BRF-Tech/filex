// lib/accountRules - what an account's e-mail address and username may be,
// checked while the person types, BY THE SERVER.
//
// Release-candidate sweep, 2026-09-21: the profile saved
// "bu-bir-eposta-degil" as an e-mail address and said "Profil kaydedildi",
// and a username with "ş" came back after Save as the server's raw English
// (`invalid username: 'ş' is not allowed …`). The forms say the problem under
// the box while it is typed.
//
// ⚠⚠ 0.54 (#209, audit B15 + A12): the browser's MIRROR of the rules is gone,
// and so are the two client copies of their sentences (core
// `account.errors.*` / `account.problem.*`, web `account.errors.*`). The mirror
// had drifted already: it let `a,b@x` and `ada.@x` through while the save
// refused them. A form now asks POST /api/auth/account/check and shows the
// server's sentence; the rules, their order and the reserved names are tested
// where they live - backend identity (internal/identity tests) and
// backend/internal/api/handlers/error_envelope_test.go
// TestAccountCheck_TheSavesRulesInTheReadersLanguage. What has to stay true
// here:
//
//   · the forms ask the server and keep no copy of a rule or a sentence;
//   · a server refusal names its field, so a form can put it under the box.
import { describe, expect, it } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { normalizeUsername, refusalField, refusalFor } from '@brftech/filex-core/src/lib/accountRules';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';
import { tr as coreTr } from '@brftech/filex-core/src/locales/tr';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const here = path.dirname(fileURLToPath(import.meta.url));
const FORMS = [
  path.resolve(here, '../../../packages/core/src/components/UserSettingsDialog.vue'),
  path.resolve(here, '../../src/views/Users.vue'),
];

describe('normalizeUsername', () => {
  it('folds the way the server stores it (identity.Normalize)', () => {
    expect(normalizeUsername('  Ada.L ')).toBe('ada.l');
  });
});

describe('the forms ask the server', () => {
  it('both account forms go through the server check and keep no rule of their own', () => {
    for (const file of FORMS) {
      const src = fs.readFileSync(file, 'utf8');
      expect(src, path.basename(file)).toMatch(/accountChecker\(/);
      // RED before 0.54: both judged with the mirror and their own words.
      expect(src, path.basename(file)).not.toMatch(/\b(usernameProblem|emailProblem|accountProblemKey)\b/);
      expect(src, path.basename(file)).not.toMatch(/account\.(errors|problem)\./);
    }
  });

  // Task #311: both forms say an answer only while the box holds the value it
  // was asked about - never the last answer whatever the box now holds.
  it('both account forms read an answer through refusalFor, with what it was asked about', () => {
    for (const file of FORMS) {
      const src = fs.readFileSync(file, 'utf8');
      expect(src, path.basename(file)).toMatch(/refusalFor\(/);
      expect(src, path.basename(file)).not.toMatch(/\.value\.(email|username)\?\.message/);
    }
  });

  it('no catalogue keeps a copy of the server’s account sentences', () => {
    for (const table of [coreEn, coreTr]) {
      expect(Object.keys(table).filter((k) => /^account\.(errors|problem)\./.test(k))).toEqual([]);
    }
    expect((en as Record<string, unknown>).account).toBeUndefined();
    expect((tr as Record<string, unknown>).account).toBeUndefined();
  });
});

describe('refusalField', () => {
  it('reads the field and the sentence the server wrote for the reader', () => {
    const err = { response: { status: 409, data: { error: 'email_taken', field: 'email', message: 'Bu adres başka bir hesaba ait.' } } };
    expect(refusalField(err)).toEqual({ field: 'email', message: 'Bu adres başka bir hesaba ait.' });
  });

  it('reads it from the explorer’s own refusal too (the raw body in `detail`)', () => {
    const err = { detail: JSON.stringify({ error: 'username_invalid', field: 'username', message: 'Kullanıcı adı rakamla başlayamaz.' }) };
    expect(refusalField(err)).toEqual({ field: 'username', message: 'Kullanıcı adı rakamla başlayamaz.' });
  });

  it('is null for a refusal that names no field', () => {
    expect(refusalField({ response: { data: { error: 'boom' } } })).toBeNull();
    expect(refusalField(new Error('network'))).toBeNull();
    expect(refusalField(undefined)).toBeNull();
  });
});

// Task #311 (Cypress spec 42, red on and off from 0.59.1): the Add user form
// asked about the address with the username still empty, the address's
// suggestion then filled the username in, and the old answer - "Enter a
// username." - stood under the suggested name until the next one came back.
// The form sends nothing while a box says what is wrong, so the Enter pressed
// in that window was lost.
describe('refusalFor', () => {
  const emptyRefused = {
    asked: { email: 'new@corp.example', username: '', for: 'new' as const },
    answer: { username: { error: 'username_invalid', message: 'Enter a username.' } },
  };

  it('says the refusal while the box holds the value the server was asked about', () => {
    expect(refusalFor(emptyRefused, 'username', '')).toBe('Enter a username.');
  });

  it('says nothing about a value the server has not been asked about yet', () => {
    // RED before #311: the form read `answer.username.message` whatever the box held.
    expect(refusalFor(emptyRefused, 'username', 'new')).toBe('');
  });

  it('compares the way the server stores the value (trimmed, lower-case)', () => {
    const taken = {
      asked: { email: 'Ada@Corp.example' },
      answer: { email: { error: 'email_taken', message: 'That address belongs to another account.' } },
    };
    expect(refusalFor(taken, 'email', ' ada@corp.example ')).toBe('That address belongs to another account.');
    expect(refusalFor(taken, 'email', 'ada@corp.example.org')).toBe('');
  });

  it('a field the check was not asked about, a field it accepted, and no answer yet all say nothing', () => {
    expect(refusalFor(emptyRefused, 'email', 'new@corp.example')).toBe('');
    expect(refusalFor({ asked: { username: 'ada' }, answer: {} }, 'email', '')).toBe('');
    expect(refusalFor(null, 'username', '')).toBe('');
  });
});
