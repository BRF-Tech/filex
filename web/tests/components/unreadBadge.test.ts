// The unread badge — rule 3 of docs/NOTIFICATIONS.md → "The bell, and who can
// reach it", pinned at its boundary.
//
// Owner, 2026-09-20, verbatim: *"Uygulamada bildirim gelince köşe ikonda
// bildirim sayısını gösterelim; mobilde de ikon üstünde; 99 üzeri 99+."*
//
// What is pinned here:
//   1. the exact number up to 99 and `99+` above it — 99 says "99", 100 says
//      "99+", and the step between them is the whole rule;
//   2. zero draws NOTHING. A badge showing `0` is a badge that says "there is
//      news" in the exact shape of the thing that says there is none;
//   3. ONE component draws it. The bell in the admin nav, the bell in the
//      explorer's header, the full list's heading and the desktop app's dock
//      all read the same module — a counter written four times is a counter
//      that disagrees with itself, and the disagreement only shows up at
//      exactly 100 unread rows, which nobody reaches by hand;
//   4. the OS-level badge is NOT clamped: macOS draws 137 happily, and the 99
//      ceiling is a fact about a 16px circle, not about the number.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import UnreadBadge from '@/components/UnreadBadge.vue';
import { unreadBadgeCount, unreadBadgeLabel, UNREAD_BADGE_MAX } from '@/lib/unreadBadge';

const SRC = (rel: string) => readFileSync(path.resolve(__dirname, '../../src', rel), 'utf8');

describe('unreadBadgeLabel — the rule', () => {
  it('counts exactly up to 99 and says 99+ above it', () => {
    expect(UNREAD_BADGE_MAX).toBe(99);
    expect(unreadBadgeLabel(1)).toBe('1');
    expect(unreadBadgeLabel(9)).toBe('9');
    expect(unreadBadgeLabel(98)).toBe('98');
    // The boundary itself: 99 is a number, 100 is "more than we will draw".
    expect(unreadBadgeLabel(99)).toBe('99');
    expect(unreadBadgeLabel(100)).toBe('99+');
    expect(unreadBadgeLabel(133)).toBe('99+');
    expect(unreadBadgeLabel(9999)).toBe('99+');
  });

  it('draws nothing at zero, and nothing for an answer that is not a count', () => {
    expect(unreadBadgeLabel(0)).toBe('');
    expect(unreadBadgeLabel(-3)).toBe('');
    expect(unreadBadgeLabel(null)).toBe('');
    expect(unreadBadgeLabel(undefined)).toBe('');
    // The count arrives from a network response; a badge is not the place to
    // discover that a server answered oddly.
    expect(unreadBadgeLabel(Number.NaN)).toBe('');
    expect(unreadBadgeLabel(Number.POSITIVE_INFINITY)).toBe('');
  });

  it('never clamps the OS-level count — that ceiling is about a 16px circle', () => {
    expect(unreadBadgeCount(133)).toBe(133);
    expect(unreadBadgeCount(0)).toBe(0);
    expect(unreadBadgeCount(-1)).toBe(0);
    expect(unreadBadgeCount(null)).toBe(0);
  });
});

describe('UnreadBadge — the component', () => {
  it('renders the label, and renders nothing at all at zero', () => {
    const at7 = mount(UnreadBadge, { props: { count: 7 } });
    expect(at7.find('[data-testid="unread-badge"]').text()).toBe('7');

    const at0 = mount(UnreadBadge, { props: { count: 0 } });
    expect(at0.find('[data-testid="unread-badge"]').exists()).toBe(false);
    expect(at0.html()).not.toContain('fx-badge');
  });

  it('crosses the boundary on screen, not only in the helper', () => {
    const w = mount(UnreadBadge, { props: { count: 99 } });
    expect(w.find('[data-testid="unread-badge"]').text()).toBe('99');
    return w.setProps({ count: 100 }).then(() => {
      expect(w.find('[data-testid="unread-badge"]').text()).toBe('99+');
    });
  });

  it('is a picture, not a second sentence — the host control owns the words', () => {
    // The bell's accessible name already reads "Notifications — 7 unread"; a
    // stray "7" announced next to it is the same fact twice.
    const w = mount(UnreadBadge, { props: { count: 7 } });
    expect(w.find('[data-testid="unread-badge"]').attributes('aria-hidden')).toBe('true');
  });
});

describe('one badge, every surface', () => {
  // ⚠⚠ The rule that keeps rule 3 true over time. It is not enough that the
  // helper is right: what matters is that no surface has its own copy of
  // `> 99 ? '99+'`, because the copy is what drifts.
  const surfaces = [
    'components/NotificationBell.vue',
    'components/NotificationsPanel.vue',
    '../../desktop/src/main.ts',
  ];

  it('no surface hand-rolls the 99+ rule', () => {
    for (const rel of surfaces) {
      const src = SRC(rel);
      expect(src, `${rel} hand-rolls the badge label`).not.toMatch(/['"]99\+['"]/);
    }
  });

  it('every surface that draws a count reaches for the shared thing', () => {
    expect(SRC('components/NotificationBell.vue')).toMatch(/UnreadBadge/);
    expect(SRC('components/NotificationsPanel.vue')).toMatch(/UnreadBadge/);
    // The desktop has no DOM to put a component in; it takes the same rule
    // from the same module for its dock badge and tray tooltip.
    expect(SRC('../../desktop/src/main.ts')).toMatch(/unreadBadgeLabel|unreadBadgeCount/);
  });
});
