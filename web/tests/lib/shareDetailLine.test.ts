import { describe, expect, it } from 'vitest';
import { shareDetailLine, validUntilLine } from '@brftech/filex-core/src/lib/shareTtl';

describe('shareDetailLine', () => {
  it('does not leave a full stop in the middle of the line', () => {
    const when = validUntilLine('2026-09-14T07:00:00Z', 'en');
    const line = shareDetailLine([when, '3 downloads']);
    expect(line).not.toMatch(/\.\s·/);
    expect(line.endsWith(' · 3 downloads')).toBe(true);
    expect(line.startsWith('This link is valid until ')).toBe(true);
  });

  it('keeps the closing full stop of a sentence that ends the line', () => {
    expect(shareDetailLine(['This link does not expire.'])).toBe('This link does not expire.');
  });

  it('skips empty facts', () => {
    expect(shareDetailLine(['Bu bağlantının süresi yoktur.', '', null, '(sunucu sınırı uygulandı)'])).toBe(
      'Bu bağlantının süresi yoktur · (sunucu sınırı uygulandı)',
    );
  });
});
