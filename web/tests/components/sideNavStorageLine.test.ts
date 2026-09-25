// The storage line at the foot of the navigation panel — how it SAYS what
// `lib/storageLine` decided (see tests/lib/storageLine.test.ts for which
// number that is).
//
// The panel is shared: the web explorer and the desktop app both draw it, and
// Home's drive cards say a drive's size with the same two sentences — "245.3
// GB used", or "at least …" while the drive's catalogue does not cover it.
// One figure, one sentence, wherever it appears.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import SideNav from '@brftech/filex-core/src/components/SideNav.vue';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');

function line(quota: Record<string, unknown> | null, locale = 'tr') {
  const w = mount(SideNav, {
    props: {
      expanded: true,
      activeView: '',
      storages: [{ name: 'Diyetlif-Bulut-Depolama' }],
      locale,
      showIdentitySurfaces: true,
      quota,
    },
  });
  return w.find('[data-testid="sidenav-quota"]');
}

describe('SideNav — the storage line', () => {
  it('says the drives’ size the way Home’s card does', () => {
    const q = { used: 245_276_276_422, total: 0, unlimited: true, partial: false };
    expect(line(q).find('.fe-sidenav__quota-text').text()).toBe('245,3 GB kullanılıyor');
    expect(line(q, 'en').find('.fe-sidenav__quota-text').text()).toBe('245.3 GB used');
  });

  it('says a lower bound as one', () => {
    const q = { used: 245_276_276_422, total: 0, unlimited: true, partial: true };
    expect(line(q).find('.fe-sidenav__quota-text').text()).toBe('en az 245,3 GB kullanılıyor');
    expect(line(q, 'en').find('.fe-sidenav__quota-text').text()).toBe('at least 245.3 GB used');
  });

  it('keeps a quota as "X of Y", its bar filled to the share', () => {
    const q = { used: 2_500_000_000, total: 10_000_000_000, unlimited: false, partial: false };
    const el = line(q);
    expect(el.find('.fe-sidenav__quota-text').text()).toBe('10 GB alanın 2,5 GB kadarı dolu');
    expect(el.find('.fe-sidenav__quota-fill').attributes('style')).toContain('width: 25%');
  });

  it('draws nothing at all without a figure', () => {
    expect(line(null).exists()).toBe(false);
  });

  it('is computed from the panel’s drives, not from the person’s upload counter', () => {
    // FileExplorer is far too large to mount here; the wiring is a shape in
    // its source. The line must come out of storageLine() fed the person's
    // quota, the drives the panel draws (the Home cards' own list) and the
    // measured fallback — and the per-user counter must no longer be handed
    // to the panel on its own.
    const explorer = readFileSync(path.join(CORE_SRC, 'FileExplorer.vue'), 'utf8');
    expect(explorer).toMatch(/storageLine\(\s*quotaMine\.value,\s*homeStorages\.value,\s*quotaDrives\.value\s*\)/);
    expect(explorer).toMatch(/needsMeasuredDrives\(\s*q,\s*homeStorages\.value\s*\)/);
    expect(explorer).toMatch(/await api\.storageUsage\(\)/);
    expect(explorer).not.toMatch(/used:\s*q\.used_bytes/);
  });

  it('is read again by Refresh, like the listing beside it', () => {
    // The drives' sizes and the person's usage both move; a Refresh that
    // re-lists the folder and leaves the line at its mount-time figure is
    // half a refresh (the desktop app measures the drives itself).
    const explorer = readFileSync(path.join(CORE_SRC, 'FileExplorer.vue'), 'utf8');
    const refresh = explorer.match(/function refreshAll\(\) \{[\s\S]*?\n\}/)?.[0] ?? '';
    expect(refresh, 'refreshAll was not found').not.toBe('');
    expect(refresh).toMatch(/loadQuota\(\)/);
  });
});
