/**
 * RTL — the text filex did NOT write is isolated too.
 *
 * ⚠⚠ Why this file exists. `useLocale().t` isolates a machine run inside a
 * right-to-left sentence, and the admin panel's post-translation hook does
 * the same for vue-i18n. Neither sees the other half of what a person reads:
 * a sentence the SERVER composed (`server.*`, arriving as the `message` of a
 * refusal), an installed app's own words, a notification built out of a row.
 * That half is exactly the half full of paths, URLs, commands and token
 * syntax — and in Arabic those lose their neutral characters to the
 * paragraph's direction.
 *
 * Measured in the Arabic admin panel (v0.43.0 pack round): the API/MCP page
 * drew `server.token.scope_unknown` — which names `root:<storage>://<folder>`
 * — with the closing `>` at the FAR LEFT of the run, mirrored into `<`, so
 * the line read `…<root:<storage>://<folder`. The SAME sentence out of the
 * catalogue was correct, which is what made it read as a broken pack.
 *
 * A pack cannot fix it: bidi controls are forbidden in a translation and the
 * pack's own validator refuses them (`BIDI`). So every one of these paths
 * goes through ONE function — `lib/direction foreignText` — and this file
 * holds each of them to it. The layout itself is measured in a real browser:
 * `e2e/tests/126-rtl-server-text.spec.ts`.
 */
import { describe, expect, it, vi } from 'vitest';

vi.mock('@brftech/filex-core/src/lib/uiLocales', async (orig) => {
  const real = (await orig()) as Record<string, unknown>;
  return {
    ...real,
    availableLocales: () => [
      { code: 'en', label: 'English', source: 'builtin' },
      { code: 'tr', label: 'Türkçe', source: 'builtin' },
      { code: 'ar', label: 'العربية', source: 'plugin', plugin: 'lang-ar', rtl: true },
    ],
  };
});

import { foreignText, isolateLtrRuns } from '@brftech/filex-core/src/lib/direction';
import {
  jobFailure,
  requestFailure,
  sayFailure,
  serverWords,
  wordsIn,
} from '@brftech/filex-core/src/lib/errorWords';
import { useLocale } from '@brftech/filex-core/src/composables/useLocale';

const LRI = String.fromCharCode(0x2066);
const PDI = String.fromCharCode(0x2069);

/** The sentence the defect was found on, as the Arabic pack writes it. */
const AR_SCOPE_UNKNOWN =
  '«sudo» ليس إذنًا يصدره هذا الخادم (أو root:<storage>://<folder>، مع وضع اسم وحدة التخزين).';

/** Is the machine run wrapped in an isolate, exactly once? */
function isolatedOnce(s: string, run: string): boolean {
  return s.includes(LRI + run + PDI) && (s.match(/\u2066/g) ?? []).length === (s.match(/\u2069/g) ?? []).length;
}

describe('foreignText — the one entry point', () => {
  it('isolates a machine run for a right-to-left language', () => {
    const said = foreignText('ar', AR_SCOPE_UNKNOWN);
    expect(isolatedOnce(said, 'root:<storage>://<folder>')).toBe(true);
  });

  it('leaves a left-to-right language byte for byte as it was', () => {
    for (const code of ['en', 'tr', 'es', '', undefined, null]) {
      expect(foreignText(code, AR_SCOPE_UNKNOWN)).toBe(AR_SCOPE_UNKNOWN);
    }
  });

  it('isolates an absolute PATH too — its leading slash is a neutral as well', () => {
    /* Measured in Chromium at dir="rtl": `/var/lib/filex/report.pdf` inside an
       Arabic sentence is drawn in TWO rectangles, the second of them the six
       pixels of the leading slash, at the far RIGHT of the run — the line
       reads `var/lib/filex/report.pdf/`. Isolated, one rectangle. */
    const said = foreignText('ar', 'تعذّر الحفظ في /var/lib/filex/report.pdf فاحذر.');
    expect(isolatedOnce(said, '/var/lib/filex/report.pdf'), said).toBe(true);
    // ⚠ Not a lone slash between two Arabic words, and not a `3 / 10` (which
    // the number-pair rule already answers).
    expect(foreignText('ar', 'نعم و/أو لا')).toBe('نعم و/أو لا');
  });

  it('isolates a path of ONE segment — a file at the root of a storage', () => {
    /* ⚠⚠ The rule used to want two segments, and this file pinned `مجلد /a`
       as left alone. That pin WAS the defect, not a guard: a root file's path
       is `/informe.pdf`, a notification body names it that way, and the admin
       Notifications page drew it `informe.pdf/` in Arabic (v0.43.0 pack
       agent) — its leading slash is a neutral like any other. */
    const said = foreignText('ar', 'تم رفع /informe.pdf بنجاح.');
    expect(isolatedOnce(said, '/informe.pdf'), said).toBe(true);
    expect(isolatedOnce(foreignText('ar', 'مجلد /a'), '/a')).toBe(true);
    expect(isolatedOnce(foreignText('ar', 'المجلد /Documents'), '/Documents')).toBe(true);
    // A full stop at the end of the sentence is the sentence's, not the path's.
    expect(foreignText('ar', 'تم رفع /informe.pdf.')).toContain(`${LRI}/informe.pdf${PDI}.`);
    // …and idempotent, like every other run.
    expect(foreignText('ar', said)).toBe(said);
  });

  it('…but a slash INSIDE a word or between numbers starts no path', () => {
    /* The single-segment rule is the one that could over-reach, so its limits
       are spelled out: a slash after a Latin letter or digit joins two halves
       of one thing (`and/or`, `km/h`, a date, `TCP/IP`), and a slash followed
       by Arabic or by a space starts nothing Latin. */
    for (const s of [
      'نعم و/أو لا',
      'السرعة 90 km/h تقريبًا',
      'بروتوكول TCP/IP فقط',
      'خيار and/or آخر',
      'كلمة / كلمة',
      'مسار / فارغ',
    ]) {
      expect(foreignText('ar', s).includes(`${LRI}/`), s).toBe(false);
    }
    // A spaced number pair is still the number-pair rule's, as ONE run.
    expect(isolateLtrRuns('الصفحة 3 / 10')).toBe(`الصفحة ${LRI}3 / 10${PDI}`);
  });

  it('is idempotent — text now reaches a screen through more than one gate', () => {
    const once = foreignText('ar', AR_SCOPE_UNKNOWN);
    expect(foreignText('ar', once)).toBe(once);
    expect(isolateLtrRuns(isolateLtrRuns('الصفحة 3 / 10'))).toBe(isolateLtrRuns('الصفحة 3 / 10'));
    expect(isolateLtrRuns(`نص ${LRI}tag:وسم${PDI}`)).toBe(`نص ${LRI}tag:وسم${PDI}`);
  });
});

describe('lib/errorWords — every failure a person reads', () => {
  it("a catalogue sentence said by `wordsIn` is isolated, which `render()` never did for it", () => {
    // ⚠ This path does NOT go through useLocale().t: `wordsIn` reads the
    // table directly, so until now an engine's name or a size pair inside a
    // failure was drawn raw in an Arabic panel while the same words drawn
    // from a component were right.
    const ar = wordsIn('ar');
    const said = ar('plugin.failed', { label: 'libreoffice' });
    expect(said).toContain('libreoffice');
    expect(wordsIn('en')('plugin.failed', { label: 'libreoffice' })).not.toContain(LRI);
    expect(typeof ar.foreign, 'the translator carries its direction').toBe('function');
    expect(ar.foreign!('root:<storage>://<folder>')).toBe(`${LRI}root:<storage>://<folder>${PDI}`);
    expect(wordsIn('en').foreign!('root:x://y')).toBe('root:x://y');
  });

  it("the SERVER's own sentence, shown by a connection panel, is isolated", () => {
    const err = requestFailure(400, JSON.stringify({ error: 'bad_key', message: AR_SCOPE_UNKNOWN }), 'ar');
    const said = serverWords(err, 'ar');
    expect(isolatedOnce(said, 'root:<storage>://<folder>'), said).toBe(true);
    // The same refusal for an English reader is untouched.
    expect(serverWords(requestFailure(400, JSON.stringify({ error: 'bad_key', message: 'use root:<a>://<b>' }), 'en'), 'en')).not.toContain(LRI);
  });

  it("the SERVER's sentence for a failed queue row (`error_text`) is isolated, with its raw second line", () => {
    // ⚠ 0.54 (#209): a failed row is said by the server (backend
    // ops/errcode.go); the client no longer has opc.err.* of its own, so the
    // sentence that reaches an Arabic screen is the server's - and it carries
    // a program's name, a path, a token syntax like any other.
    const { t } = useLocale(() => 'ar');
    // The raw second line carries a machine run (the engine's path): plain
    // English words alone have nothing to isolate.
    const said = jobFailure(
      {
        error: 'engine libreoffice missing at /usr/bin/soffice',
        error_code: 'engine_missing',
        error_engine: 'libreoffice',
        error_text: AR_SCOPE_UNKNOWN,
      },
      t('toast.failed'),
      t,
      { callerAdmin: true },
    );
    expect(isolatedOnce(said.text, 'root:<storage>://<folder>'), said.text).toBe(true);
    expect(said.detail).toContain(LRI);
    const { t: en } = useLocale(() => 'en');
    expect(jobFailure({ error: 'x', error_code: 'name_taken', error_text: 'use root:<a>://<b>' }, en('toast.failed'), en).text).not.toContain(LRI);
  });

  it("a caught failure's sentence AND the administrator's raw second line are isolated", () => {
    const { t } = useLocale(() => 'ar');
    const err = requestFailure(500, 'engine libreoffice missing at /usr/bin/soffice', 'ar');
    const said = sayFailure(err, t('toast.failed'), { t, callerAdmin: true });
    expect(said.detail, 'the raw words are there for an administrator').toContain('libreoffice');
    expect(said.detail).toContain(LRI);
    // A viewer who may not administer the instance gets no second line at all.
    expect(sayFailure(err, t('toast.failed'), { t }).detail).toBeUndefined();
  });

  it("an APP's own words are isolated — they are the least ours of all", () => {
    const { t } = useLocale(() => 'ar');
    const said = jobFailure({ error: `تعذّر التحويل — ${'root:<storage>://<folder>'} غير صالح`, error_code: 'app' }, t('toast.failed'), t);
    expect(isolatedOnce(said.text, 'root:<storage>://<folder>'), said.text).toBe(true);
    // English is byte-for-byte what it was.
    const { t: en } = useLocale(() => 'en');
    expect(jobFailure({ error: 'could not convert root:<a>://<b>', error_code: 'app' }, en('toast.failed'), en).text).not.toContain(LRI);
  });

  it('a translator that does not know its direction simply passes the text through', () => {
    // A test stub, or any `T` written as a plain function: nothing throws,
    // nothing is isolated, and the caller still gets a sentence.
    const plain = (key: string) => key;
    expect(jobFailure({ error: 'boom root:<a>://<b>', error_code: 'app' }, 'fallback', plain).text).toBe('boom root:<a>://<b>');
  });
});

// The notification bell is not here any more: since 2026-10-08 the SERVER
// composes every notification (backend notify say.go) and isolates each value
// it places in a right-to-left sentence itself - the Go test
// TestSay_ALanguagePacksWordsFormsAndDirection holds it.
