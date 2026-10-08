// The escrow notification must say ESCROW — in every string that describes it.
//
// ⚠⚠ The bug: `e2e.escrow_used` rendered as "opened with the RECOVERY key"
// (and "kurtarma anahtarıyla") in the bell, the browser toast, the desktop's
// OS notification, the settings switch and the webhook event list, while the
// server's own title said "escrow". Those are two different keys with opposite
// meanings: a recovery key is what the OWNER was handed when the folder was
// made; escrow means the OPERATOR's key was used on their folder. A person told
// the first when the second happened is told the reverse of what happened to
// their data. Found by the v0.41.0 screenshot pass, 2026-09-14.
//
// What holds it, and against what:
//   1. The words come from the explorer's own E2E vocabulary — the recovery
//      dialog's two tab labels (`e2e.recover.tab_escrow` / `tab_recovery`) —
//      so there is one name for each key across the product, not one per file.
//   2. There is no second wording to disagree with: since 2026-10-08 no
//      emitter writes a sentence of its own (backend notify say_test.go
//      TestSay_NoEmitterWritesItsOwnSentence) - an English `Title:` in the
//      escrow handler was the second wording this test used to compare.
//
// The phrases are the SERVER catalogue's (`server.notify.<event>.title` in
// backend/internal/srvtext/locales): since 2026-10-08 the server says every
// notification (backend notify say.go) and no client composes one.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';
import { tr as coreTr } from '@brftech/filex-core/src/locales/tr';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { userEventKey, webhookEventKey } from '@brftech/filex-core/src/lib/webhookEvents';

const here = path.dirname(fileURLToPath(import.meta.url));
const BACKEND = path.resolve(here, '../../../backend/internal');

/** The server catalogue, the one place a notification's words live. */
const SERVER: Record<'en' | 'tr', Record<string, string>> = {
  en: JSON.parse(fs.readFileSync(path.join(BACKEND, 'srvtext/locales/en.json'), 'utf8')),
  tr: JSON.parse(fs.readFileSync(path.join(BACKEND, 'srvtext/locales/tr.json'), 'utf8')),
};

function lookup(catalogue: unknown, key: string): string {
  const v = key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], catalogue);
  return typeof v === 'string' ? v : '';
}

/** The stem of a key's name as it appears inside a sentence ("Emanet anahtarı"
 *  → "emanet anahtar", which also matches "anahtarıyla"). */
function stem(label: string, lang: 'en' | 'tr'): string {
  const lower = label.toLocaleLowerCase(lang === 'tr' ? 'tr-TR' : 'en-US');
  return lang === 'tr' ? lower.replace(/ı$/, '') : lower;
}

const TERMS = {
  en: { escrow: stem(coreEn['e2e.recover.tab_escrow'], 'en'), recovery: stem(coreEn['e2e.recover.tab_recovery'], 'en') },
  tr: { escrow: stem(coreTr['e2e.recover.tab_escrow'], 'tr'), recovery: stem(coreTr['e2e.recover.tab_recovery'], 'tr') },
};

describe('e2e.escrow_used names the escrow key', () => {
  it('reads the vocabulary it checks against (a missing label would make every check vacuous)', () => {
    expect(TERMS.en).toEqual({ escrow: 'escrow key', recovery: 'recovery key' });
    expect(TERMS.tr).toEqual({ escrow: 'emanet anahtar', recovery: 'kurtarma anahtar' });
  });

  for (const lang of ['en', 'tr'] as const) {
    const catalogue = lang === 'en' ? en : tr;
    const texts: Array<[string, string]> = [
      ['server phrase', SERVER[lang]['server.notify.e2e.escrow_used.title'] ?? ''],
      ['settings switch label', lookup(catalogue, userEventKey('e2e.escrow_used'))],
      ['webhook event label', lookup(catalogue, webhookEventKey('e2e.escrow_used'))],
    ];
    for (const [where, text] of texts) {
      it(`${lang}: the ${where} says escrow, not recovery`, () => {
        const lower = text.toLocaleLowerCase(lang === 'tr' ? 'tr-TR' : 'en-US');
        expect(text, `${where} is empty`).not.toBe('');
        expect(lower).toContain(TERMS[lang].escrow);
        expect(lower).not.toContain(TERMS[lang].recovery);
      });
    }
  }
});
