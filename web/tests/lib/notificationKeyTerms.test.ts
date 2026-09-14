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
//   2. Every English phrase whose title has no placeholder must say exactly
//      what the emitting Go code's literal `Title:` says, for every event that
//      sets one. The server title is the fallback every catalogue-less reader
//      gets (webhook payloads, the audit table); the two may not disagree.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';
import { tr as coreTr } from '@brftech/filex-core/src/locales/tr';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { NOTIFICATION_PHRASES } from '@/lib/notificationText';
import { userEventKey, webhookEventKey } from '@/lib/webhookEvents';

const here = path.dirname(fileURLToPath(import.meta.url));
const BACKEND = path.resolve(here, '../../../backend/internal');

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
      ['notificationText phrase', NOTIFICATION_PHRASES['e2e.escrow_used'][lang].title],
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

describe('a fixed English phrase says what the server title says', () => {
  function goFiles(dir: string, out: string[] = []): string[] {
    for (const name of fs.readdirSync(dir)) {
      const p = path.join(dir, name);
      if (fs.statSync(p).isDirectory()) goFiles(p, out);
      else if (name.endsWith('.go') && !name.endsWith('_test.go')) out.push(p);
    }
    return out;
  }

  const consts = new Map<string, string>();
  const eventGo = fs.readFileSync(path.join(BACKEND, 'notify/event.go'), 'utf8');
  for (const m of eventGo.matchAll(/(Event\w+)\s+EventType\s*=\s*"([^"]+)"/g)) consts.set(m[1], m[2]);

  /** event id → the literal titles its emitters set. */
  const serverTitles = new Map<string, string[]>();
  for (const f of goFiles(BACKEND)) {
    const src = fs.readFileSync(f, 'utf8');
    for (const m of src.matchAll(/Event:\s*notify\.(Event\w+),[^}]*?Title:\s*"([^"]+)"\s*,/g)) {
      const id = consts.get(m[1]);
      if (!id) continue;
      serverTitles.set(id, [...(serverTitles.get(id) ?? []), m[2]]);
    }
  }

  it('finds the emitters it compares (a parser that matched nothing would pass everything)', () => {
    expect(serverTitles.get('e2e.escrow_used')).toEqual(['Encrypted folder opened with the escrow key']);
  });

  const fixed = Object.entries(NOTIFICATION_PHRASES).filter(
    ([id, p]) => serverTitles.has(id) && !/\{\w+\}/.test(p.en.title),
  );
  for (const [id, phrase] of fixed) {
    it(`${id}: "${phrase.en.title}"`, () => {
      expect(serverTitles.get(id)).toContain(phrase.en.title);
    });
  }
});
