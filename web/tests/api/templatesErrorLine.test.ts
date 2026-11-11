import { describe, expect, it } from 'vitest';
import { errorLineOf } from '@/api/templates';

// The template editor marks the line the engine refused (#322): read off the
// engine's own words, and only when they are about the template on screen.
describe('errorLineOf', () => {
  it('reads the line of the template named', () => {
    expect(errorLineOf('Liquid error (line 3): undefined variable "item.nmae" in mail.share: {{ item.nmae }}', 'mail.share')).toBe(3);
    expect(errorLineOf('template_invalid\nLiquid error (line 12): syntax error in mail.share.subject', 'mail.share.subject')).toBe(12);
  });

  it('marks nothing for an error in another template (the layout)', () => {
    expect(errorLineOf('Liquid error (line 7): undefined variable "x" in mail.layout', 'mail.share')).toBeNull();
  });

  it('takes a line with no template named, and nothing without a line', () => {
    expect(errorLineOf('Liquid error (line 2): unterminated tag', 'page.pin')).toBe(2);
    expect(errorLineOf('the template took longer than a render may', 'page.pin')).toBeNull();
    expect(errorLineOf('', 'page.pin')).toBeNull();
  });
});
