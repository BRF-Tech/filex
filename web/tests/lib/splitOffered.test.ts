// Under `ui-profile="simple"` the Split button was DRAWN and did nothing.
//
// Measured 2026-09-14 in a real <filex-explorer ui-profile="simple"> at 1440:
// one pane before the click, one pane after, and the button turned PRESSED
// (`aria-pressed="true"`). The `simple` profile turns the split pane off on
// purpose (lib/uiProfile), and the pane honoured that — but the two doors to
// it, the tab strip's toggle and the command palette's row, were handed
// `!isNarrow` and nothing else. The click wrote a split onto the tab that no
// pane would ever show, and that the tab would suddenly grow the day the
// profile changed back.
//
// ⚠ It was NOT TabBar's known absent-Boolean landmine (`splitEnabled !== false`
// reads an absent prop as `false`, because Vue casts absent Booleans to
// `false`). FileExplorer always passed the prop explicitly — the value it
// passed was the wrong question. The mount test below pins the landmine's
// actual behaviour too, so nobody has to rediscover which one it was.
//
// The core package has no test runner of its own; like listing.test.ts this is
// exercised here, in the app that ships it.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import TabBar from '@brftech/filex-core/src/components/TabBar.vue';

const EXPLORER = readFileSync(
  path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'),
  'utf8',
);

describe('FileExplorer offers a split only where one can be shown', () => {
  it('has ONE rule, and the simple profile is part of it', () => {
    const rule = EXPLORER.match(/const splitOffered = computed\(\(\) => ([^;]+)\);/);
    expect(rule, 'splitOffered is gone — which rule decides whether the toggle is drawn now?').not.toBeNull();
    expect(rule![1]).toContain('!isNarrow.value');
    expect(rule![1]).toContain('!simpleUi.value');
  });

  it('every door to the split asks that rule — the strip AND the palette', () => {
    const bindings = [...EXPLORER.matchAll(/:split-enabled="([^"]+)"/g)].map((m) => m[1].trim());
    expect(bindings.length).toBeGreaterThanOrEqual(2);
    for (const b of bindings) expect(b.startsWith('splitOffered'), `:split-enabled="${b}"`).toBe(true);
  });

  it('the pane it opens is gated on the same rule', () => {
    expect(EXPLORER).toMatch(/const splitVisible = computed\(\(\) => !!activeSplit\.value && splitOffered\.value/);
  });

  it('a click (or a palette command) cannot write a split the profile forbids', () => {
    const body = EXPLORER.match(/function toggleSplit\(\) \{([\s\S]*?)\n\}/);
    expect(body).not.toBeNull();
    expect(body![1]).toMatch(/if \(!splitOffered\.value\) return;\s*\n\s*tabsApi\.setSplit\(/);
  });
});

describe('TabBar', () => {
  const base = { tabs: [{ id: 't1', label: 'demo', split: false }], activeId: 't1', locale: 'en' as const };

  it('draws the toggle when told to and not when told not to', () => {
    expect(mount(TabBar, { props: { ...base, splitEnabled: true } }).find('[data-testid="tabs-split"]').exists()).toBe(true);
    expect(mount(TabBar, { props: { ...base, splitEnabled: false } }).find('[data-testid="tabs-split"]').exists()).toBe(false);
  });

  it('the absent-Boolean landmine: NO prop means NO toggle (Vue casts it to false)', () => {
    // Documented, not fixed: every caller passes the prop. If this ever flips,
    // `splitEnabled !== false` has started meaning something else.
    expect(mount(TabBar, { props: base }).find('[data-testid="tabs-split"]').exists()).toBe(false);
  });
});
