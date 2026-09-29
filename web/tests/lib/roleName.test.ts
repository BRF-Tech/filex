// A custom role's name for the viewer (lib/roleName): the translation for the
// panel's language, then for its primary language, then the role's own name —
// the same rule the server's model.LocalizedText follows for the refusal
// sentences, so a role is not called one thing on the page and another in
// the message under it.
import { describe, expect, it } from 'vitest';

import { localizedText, roleDescription, roleName } from '@/lib/roleName';

const accounting = {
  name: 'Accounting',
  description: 'Invoices',
  names: { tr: 'Muhasebe', pt: 'Contabilidade', 'zh-hant': '會計' },
  descriptions: { tr: 'Faturalar' },
};

describe('roleName', () => {
  it.each([
    ['tr', 'Muhasebe'],
    ['TR', 'Muhasebe'],
    ['tr_TR', 'Muhasebe'],
    ['tr-TR', 'Muhasebe'],
    ['pt-br', 'Contabilidade'],
    ['zh-hant', '會計'],
    ['zh', 'Accounting'],
    ['en', 'Accounting'],
    ['', 'Accounting'],
  ])('%s → %s', (locale, want) => {
    expect(roleName(accounting, locale)).toBe(want);
  });

  it('a blank translation is none', () => {
    expect(roleName({ name: 'Accounting', names: { tr: '   ' } }, 'tr')).toBe('Accounting');
  });

  it('a role without translations, or no role, is its own name or nothing', () => {
    expect(roleName({ name: 'Auditors' }, 'tr')).toBe('Auditors');
    expect(roleName({ name: 'Auditors', names: null }, 'tr')).toBe('Auditors');
    expect(roleName(null, 'tr')).toBe('');
    expect(roleName(undefined, 'tr')).toBe('');
  });
});

describe('roleDescription', () => {
  it('follows the same rule', () => {
    expect(roleDescription(accounting, 'tr')).toBe('Faturalar');
    expect(roleDescription(accounting, 'en')).toBe('Invoices');
    expect(roleDescription({ name: 'x' }, 'tr')).toBe('');
  });
});

describe('localizedText', () => {
  it('trims what it returns', () => {
    expect(localizedText('base', { tr: '  Muhasebe ' }, 'tr')).toBe('Muhasebe');
  });
});
