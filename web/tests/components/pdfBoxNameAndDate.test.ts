// A box's NAME, and a date box's LAYOUT — the owner's two findings of
// 2026-09-23, measured on the one editor both screens draw.
//
//   "kutucuğa isim verirken isimde boşluk bırakamıyorum hiç izin vermiyor"
//   "tarih biçimi ve ayraçlarını ayrı ayrı seçebilir olalım … Tarih biçimi
//    iki farklı seçimle tamamlandığında örnek halini alta gösterelim"
import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import { registerLocale, resetLocales } from '@brftech/filex-core/src/lib/uiLocales';

import PdfFieldEditor from '@brftech/filex-core/src/components/plugin/nodes/PdfFieldEditor.vue';
import {
  dateChoices,
  dateExample,
  formatFor,
  normalizeDateLabels,
  normalizeFields,
  normalizeFormats,
  splitFormat,
} from '@brftech/filex-core/src/lib/pdfFields';

/** The catalogue the plugin really sends: five arrangements × four separators. */
const ORDERS = [
  ['DDMMYYYY', 'GG AA YYYY', ['DD', 'MM', 'YYYY']],
  ['DDMMYY', 'GG AA YY', ['DD', 'MM', 'YY']],
  ['MMDDYYYY', 'AA GG YYYY', ['MM', 'DD', 'YYYY']],
  ['MMDDYY', 'AA GG YY', ['MM', 'DD', 'YY']],
  ['YYYYMMDD', 'YYYY AA GG', ['YYYY', 'MM', 'DD']],
] as const;
const SEPS = [
  ['.', 'Nokta .'],
  ['/', 'Bölü /'],
  ['-', 'Tire -'],
  [' ', 'Boşluk'],
] as const;

function catalogue() {
  const out: Array<Record<string, unknown>> = [];
  for (const [order, orderLabel, parts] of ORDERS) {
    for (const [sep, sepLabel] of SEPS) {
      out.push({
        id: parts.join(sep),
        label: { tr: parts.join(sep) },
        example: ['31', '12', '2000'].join(sep),
        order,
        order_label: { tr: orderLabel },
        separator: sep,
        separator_label: { tr: sepLabel },
      });
    }
  }
  return out;
}

const dateLabels = { order: { tr: 'Tarih sırası' }, separator: { tr: 'Ayraç' }, example: { tr: 'Örnek' } };

function editor(field: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  return mount(PdfFieldEditor, {
    props: {
      field: { page: 1, x: 0.1, y: 0.1, w: 0.2, h: 0.05, ...field } as never,
      signers: [{ id: 'a', label: 'Ann' }],
      locale: 'tr',
      ...extra,
    },
  });
}

describe("a box's name takes spaces", () => {
  // ⚠⚠ THE bug. The field was trimmed on every keystroke and the trimmed
  // name written straight back into the input, so the space typed after a
  // word was gone before the next letter arrived — "Ali Veli" could not be
  // typed at all.
  it('keeps a trailing space while a second word is being typed', async () => {
    const w = editor({ id: 'text-1', type: 'text' });
    const input = w.find('[data-testid="surface-pdf-label"]');
    let typed = '';
    for (const ch of 'Ali Veli') {
      typed += ch;
      await input.setValue(typed);
      const patched = w.emitted('patch')?.at(-1)?.[0] as { label?: string };
      expect(patched.label, `after typing ${JSON.stringify(typed)}`).toBe(typed);
      // ...and what the editor is asked to draw next is what was typed.
      await w.setProps({ field: { page: 1, x: 0.1, y: 0.1, w: 0.2, h: 0.05, ...patched } as never });
      expect((w.find('[data-testid="surface-pdf-label"]').element as HTMLInputElement).value).toBe(typed);
    }
  });

  it('emptying the field takes the name off, and any script is a name', async () => {
    const w = editor({ id: 'text-1', type: 'text', label: '顧客名' });
    expect((w.find('[data-testid="surface-pdf-label"]').element as HTMLInputElement).value).toBe('顧客名');
    await w.find('[data-testid="surface-pdf-label"]').setValue('');
    expect((w.emitted('patch')?.at(-1)?.[0] as Record<string, unknown>).label).toBeUndefined();
  });

  // The wire keeps it too: this array round-trips through the plugin on
  // every `change` while the name is being typed.
  it('normalizeFields carries the name exactly as typed', () => {
    const [f] = normalizeFields([{ id: 'x', type: 'text', page: 1, x: 0, y: 0, w: 0.1, h: 0.1, label: 'Ali ' }]);
    expect(f.label).toBe('Ali ');
    const [blank] = normalizeFields([{ id: 'y', type: 'text', page: 1, x: 0, y: 0, w: 0.1, h: 0.1, label: '   ' }]);
    expect(blank.label, 'a name of spaces is carried so the step can refuse it').toBe('   ');
    const [none] = normalizeFields([{ id: 'z', type: 'text', page: 1, x: 0, y: 0, w: 0.1, h: 0.1, label: '' }]);
    expect(none.label).toBeUndefined();
  });
});

describe('a date box: two questions, one layout', () => {
  it('reads the two halves out of one catalogue', () => {
    const formats = normalizeFormats(catalogue());
    const parts = dateChoices(formats)!;
    expect(parts.orders.map((o) => o.id)).toEqual(['DDMMYYYY', 'DDMMYY', 'MMDDYYYY', 'MMDDYY', 'YYYYMMDD']);
    expect(parts.separators.map((s) => s.id)).toEqual(['.', '/', '-', ' ']);
    expect(formatFor(formats, 'MMDDYY', '-')).toBe('MM-DD-YY');
    expect(splitFormat(formats, 'YYYY MM DD')).toEqual({ order: 'YYYYMMDD', separator: ' ' });
    // A catalogue that does NOT carry the halves asks the old single question.
    expect(dateChoices(normalizeFormats([{ id: 'DD.MM.YYYY', label: 'DD.MM.YYYY' }]))).toBeNull();
  });

  it('writes a real date the chosen way, in plain digits', () => {
    const day = new Date(2026, 8, 23);
    expect(dateExample('DDMMYYYY', '.', day)).toBe('23.09.2026');
    expect(dateExample('MMDDYY', '/', day)).toBe('09/23/26');
    expect(dateExample('YYYYMMDD', '-', day)).toBe('2026-09-23');
    expect(dateExample('DDMMYYYY', ' ', day)).toBe('23 09 2026');
  });

  it('normalizeDateLabels keeps the plugin’s own captions', () => {
    expect(normalizeDateLabels(dateLabels).order).toEqual({ tr: 'Tarih sırası' });
    expect(normalizeDateLabels('nonsense')).toEqual({});
  });

  // The control: two rows of BUTTONS (v3 §2 — no dropdown anywhere) and the
  // date they make underneath, which is the whole point of asking twice.
  it('draws two button groups and the date they make', async () => {
    const w = editor({ id: 'date-1', type: 'date', format: 'DD.MM.YYYY' }, { formats: catalogue(), dateLabels });
    expect(w.find('[data-testid="surface-pdf-editor"]').find('select').exists()).toBe(false);
    expect(w.findAll('[data-testid^="surface-pdf-order-"]')).toHaveLength(5);
    expect(w.findAll('[data-testid^="surface-pdf-sep-"]')).toHaveLength(4);
    // The single whole-layout row is gone: twenty buttons is a wall.
    expect(w.find('[data-testid^="surface-pdf-format-"]').exists()).toBe(false);

    const today = new Date();
    const dd = String(today.getDate()).padStart(2, '0');
    const mm = String(today.getMonth() + 1).padStart(2, '0');
    const yyyy = String(today.getFullYear());
    const shown = () => w.find('[data-testid="surface-pdf-date-example"]').text();
    expect(shown()).toContain('Örnek');
    expect(shown()).toContain(`${dd}.${mm}.${yyyy}`);

    // Change ONE answer at a time and read the example back.
    await w.find('[data-testid="surface-pdf-sep-/"]').trigger('click');
    let patched = w.emitted('patch')?.at(-1)?.[0] as { format?: string };
    expect(patched.format).toBe('DD/MM/YYYY');
    await w.setProps({ field: { page: 1, x: 0.1, y: 0.1, w: 0.2, h: 0.05, id: 'date-1', type: 'date', ...patched } as never });
    expect(shown()).toContain(`${dd}/${mm}/${yyyy}`);

    await w.find('[data-testid="surface-pdf-order-MMDDYY"]').trigger('click');
    patched = w.emitted('patch')?.at(-1)?.[0] as { format?: string };
    expect(patched.format, 'the separator already chosen is kept').toBe('MM/DD/YY');
    await w.setProps({ field: { page: 1, x: 0.1, y: 0.1, w: 0.2, h: 0.05, id: 'date-1', type: 'date', ...patched } as never });
    expect(shown()).toContain(`${mm}/${dd}/${yyyy.slice(2)}`);
  });

  it('shows the layout a box already carries as the one chosen', () => {
    const w = editor({ id: 'date-1', type: 'date', format: 'YYYY-MM-DD' }, { formats: catalogue(), dateLabels });
    expect(w.find('[data-testid="surface-pdf-order-YYYYMMDD"]').attributes('aria-checked')).toBe('true');
    expect(w.find('[data-testid="surface-pdf-sep--"]').attributes('aria-checked')).toBe('true');
  });

  it('only a DATE box is asked', () => {
    for (const type of ['text', 'signature', 'checkbox']) {
      const w = editor({ id: 'x', type }, { formats: catalogue(), dateLabels });
      expect(w.find('[data-testid^="surface-pdf-order-"]').exists()).toBe(false);
      expect(w.find('[data-testid="surface-pdf-date-example"]').exists()).toBe(false);
    }
  });
});

/**
 * v0.43.0: three English words in the middle of an Arabic screen.
 *
 * The signing app captions these three itself, in en/tr/es/de/fr. filex has
 * its OWN words for all three, and the Arabic pack translates them. The call
 * was `labelOf(appCaption, locale) || t(hostKey)` — and `labelOf` answers
 * ENGLISH when the app does not speak the reader's language, so the host
 * string was unreachable: everything filex drew was Arabic and the three
 * captions were English.
 *
 * ⚠ The pack is registered here the way the server delivers one
 * (`registerLocale`), so this measures the real lookup and not a stub.
 */
describe('an app caption, where filex has its own words', () => {
  const AR = {
    'plugin.pdf.date_order': 'ترتيب التاريخ',
    'plugin.pdf.date_separator': 'الفاصل',
  };
  /** English captions only — exactly what filex-sign ships for a reader in Arabic. */
  const englishOnly = {
    order: { en: 'Date order' },
    separator: { en: 'Separator' },
    example: { en: 'Example' },
  };

  afterEach(() => resetLocales());

  function arabicEditor(labels: Record<string, unknown>) {
    registerLocale({ code: 'ar', label: 'العربية', source: 'plugin', plugin: 'lang-ar', rtl: true, strings: AR });
    return editor({ id: 'date-1', type: 'date', format: 'DD.MM.YYYY' }, {
      formats: catalogue(),
      dateLabels: labels,
      locale: 'ar',
    });
  }

  it("shows FILEX's Arabic, not the app's English", () => {
    const w = arabicEditor(englishOnly);
    const text = w.text();
    expect(text).toContain(AR['plugin.pdf.date_order']);
    expect(text).toContain(AR['plugin.pdf.date_separator']);
    expect(text, 'the app’s English beat the pack again').not.toContain('Date order');
    expect(text).not.toContain('Separator');
  });

  it("still gives the app its say when the app speaks Arabic", () => {
    const w = arabicEditor({ ...englishOnly, order: { en: 'Date order', ar: 'ترتيب المورد' } });
    expect(w.text()).toContain('ترتيب المورد');
    expect(w.text()).not.toContain(AR['plugin.pdf.date_order']);
  });

  it("falls back to the app's English where filex has no words — the last resort", () => {
    // `plugin.pdf.date_example` is deliberately NOT in this pack: an app may
    // caption something filex has never had a key for, and an English word
    // beats an empty one.
    const w = arabicEditor(englishOnly);
    expect(w.text()).toContain('Example');
  });
});
