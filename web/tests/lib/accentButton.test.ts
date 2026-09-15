import { describe, expect, it } from 'vitest';
import { accentButtonStyle, contrast, MIN_EDGE_CONTRAST } from '@/lib/accentButton';

/**
 * Issue #29: a black accent vanished on the dark sign-in card, a white one on
 * the light card — fill and label the same colour, fill the same as the card.
 */
describe('accentButtonStyle', () => {
  it('black accent in the dark theme: white label and a visible edge', () => {
    const s = accentButtonStyle('#000000', true)!;
    expect(s.color).toBe('#ffffff');
    expect(s.borderColor).not.toBe('#000000');
  });

  it('white accent in the light theme: dark label and a visible edge', () => {
    const s = accentButtonStyle('#ffffff', false)!;
    expect(s.color).toBe('#15171c');
    expect(s.borderColor).not.toBe('#ffffff');
  });

  it('black accent in the light theme stands out on its own: no extra edge', () => {
    const s = accentButtonStyle('#000', false)!;
    expect(s.borderColor).toBe('#000');
    expect(s.color).toBe('#ffffff');
  });

  it('white accent in the dark theme stands out on its own: no extra edge', () => {
    const s = accentButtonStyle('#fff', true)!;
    expect(s.borderColor).toBe('#fff');
    expect(s.color).toBe('#15171c');
  });

  it('every label is readable on its fill, in both themes', () => {
    for (const accent of ['#000000', '#ffffff', '#2f6ceb', '#facc15', '#15171c', '#808080', '#e11d48']) {
      for (const dark of [false, true]) {
        const s = accentButtonStyle(accent, dark)!;
        expect(contrast(s.backgroundColor, s.color), `${accent} dark=${dark}`).toBeGreaterThanOrEqual(3);
      }
    }
  });

  it('a fill that does not stand out from the card always gets an edge', () => {
    for (const [accent, dark] of [['#1a1c21', true], ['#f4f4f5', false]] as const) {
      const s = accentButtonStyle(accent, dark)!;
      expect(contrast(accent, dark ? '#15171c' : '#ffffff')).toBeLessThan(MIN_EDGE_CONTRAST);
      expect(s.borderColor).not.toBe(accent);
    }
  });

  it('no accent, or an invalid one, keeps the product palette', () => {
    expect(accentButtonStyle(undefined, true)).toBeUndefined();
    expect(accentButtonStyle('', false)).toBeUndefined();
    expect(accentButtonStyle('red', false)).toBeUndefined();
  });
});
