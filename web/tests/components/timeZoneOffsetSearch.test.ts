// Finding a time zone by its offset works in every interface language.
//
// ⚠⚠ The bug (translator finding, v0.43.0): the picker built its "+3 / UTC+3 /
// GMT+03:00" search aliases by parsing the offset the row DISPLAYS — the
// platform's `shortOffset` in the INTERFACE language — with a `GMT…` pattern.
// That text is "GMT+3" in English and German only: Arabic prints "غرينتش+3"
// (and "غرينتش+٣" with Arabic-Indic digits in ar-EG), French prints "UTC+3".
// In those languages not one zone could be found by typing its offset. The
// aliases now come from the offset as a NUMBER, read in one fixed shape.
import { describe, expect, it } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import TimeZonePicker from '@brftech/filex-core/src/components/TimeZonePicker.vue';

async function search(locale: string, query: string): Promise<string[]> {
  const w = mount(TimeZonePicker, {
    props: {
      locale: locale as never,
      modelValue: '',
      fallback: { zone: 'UTC', tier: 'device' } as never,
      testid: 'tz',
    },
    attachTo: document.body,
  });
  const input = w.get('[data-testid="tz-filter"]');
  await input.trigger('focus');
  (input.element as HTMLInputElement).value = query;
  await input.trigger('input');
  await flushPromises();
  const found = w.findAll('[role="option"]').map((o) => o.attributes('data-testid') ?? '');
  w.unmount();
  return found;
}

describe('offset search, whatever language the interface is in', () => {
  for (const locale of ['en', 'de', 'fr', 'ar', 'ar-EG']) {
    it(`${locale}: "+3" and "utc+3" find Istanbul`, async () => {
      expect(await search(locale, '+3')).toContain('tz-opt-Europe/Istanbul');
      expect(await search(locale, 'utc+3')).toContain('tz-opt-Europe/Istanbul');
    });
  }

  it('a half-hour offset is found by its minutes too', async () => {
    // The engine's list names it Asia/Calcutta or Asia/Kolkata, depending on its ICU.
    expect((await search('ar', 'utc+05:30')).some((id) => /Asia\/(Calcutta|Kolkata)$/.test(id))).toBe(true);
  });

  it('the platform really does print the offset differently — the premise of this file', () => {
    const say = (l: string) =>
      new Intl.DateTimeFormat(l, { timeZone: 'Europe/Istanbul', timeZoneName: 'shortOffset' })
        .formatToParts(new Date())
        .find((p) => p.type === 'timeZoneName')?.value;
    expect(say('ar')).not.toMatch(/^GMT/);
    expect(say('fr')).not.toMatch(/^GMT/);
  });
});
