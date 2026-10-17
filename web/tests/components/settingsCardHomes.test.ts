// Each admin setting has ONE home (task #258, the owner's grouping of
// 2026-10-15). Two cards for one setting on two pages is how they drift: the
// thumbnail limits were drawn on Settings and again on Tools → Thumbnail
// repair, and the default folder view sat on Settings, away from the default
// theme it belongs beside. Read from the sources, so a second copy fails here
// before anybody has to notice two pages saying different things.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = path.resolve(__dirname, '../../src');
const read = (rel: string) => fs.readFileSync(path.join(SRC, rel), 'utf8');

/** The files of the admin app that draw a component by its tag. */
function drawnIn(tag: string): string[] {
  const out: string[] = [];
  const walk = (dir: string) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p);
      else if (e.name.endsWith('.vue') && new RegExp(`<${tag}\\b`).test(fs.readFileSync(p, 'utf8'))) {
        out.push(path.relative(SRC, p).split(path.sep).join('/'));
      }
    }
  };
  walk(SRC);
  return out.sort();
}

describe('one home per setting', () => {
  it('the thumbnail settings are drawn on General alone, as the card the search scrolls to', () => {
    expect(drawnIn('ThumbLimitsCard')).toEqual(['views/Settings.vue']);
    expect(read('views/Settings.vue')).toMatch(/<ThumbLimitsCard[^>]*id="thumb-limits"/);
    // Tools → Thumbnail repair links there instead.
    expect(read('components/tools/ThumbnailRepairTab.vue')).toMatch(/name: 'settings', hash: '#thumb-limits'/);
  });

  it('the search settings are drawn on General alone; the Search index page links there', () => {
    expect(drawnIn('SearchSettingsCard')).toEqual(['views/Settings.vue']);
    expect(read('views/Settings.vue')).toMatch(/<SearchSettingsCard[^>]*id="search-settings"/);
    expect(read('views/SearchTest.vue')).toMatch(/name: 'settings', hash: '#search-settings'/);
  });

  it('the settings that left the environment have one card, one per page, one group each', () => {
    const where: Record<string, string> = {};
    for (const f of drawnIn('InstanceSettingsCard')) {
      for (const m of read(f).matchAll(/<InstanceSettingsCard[^>]*group="([a-z]+)"/g)) {
        expect(where[m[1]], `group ${m[1]} drawn twice`).toBeUndefined();
        where[m[1]] = f;
      }
    }
    expect(where).toEqual({
      general: 'views/Settings.vue',
      protection: 'views/Protection.vue',
      apps: 'views/Plugins.vue',
      notifications: 'views/Notifications.vue',
      webhooks: 'views/Webhooks.vue',
    });
  });

  it('the default folder view is drawn on Appearance alone', () => {
    expect(drawnIn('FolderViewCard')).toEqual(['views/Appearance.vue']);
    expect(read('views/Settings.vue')).not.toContain('ui.default_folder_view');
    expect(read('components/settings/FolderViewCard.vue')).toContain('id="folder-view"');
  });

  it('Appearance still takes the operator’s stylesheet out before anything else', () => {
    // The card moved INTO the page that must survive a ruinous stylesheet;
    // the guard is still the first thing the page does (views/Appearance.vue).
    // ⚠ The hook's body starts AFTER its opening line: sliced from the line
    // itself, the first line read was `onMounted(async () => {` and the check
    // could never pass (int/057-wave 60f73d64), whatever the page did first.
    const src = read('views/Appearance.vue');
    const open = 'onMounted(async () => {';
    const at = src.indexOf(open);
    expect(at, 'Appearance mounts with an async hook').toBeGreaterThan(-1);
    const body = src.slice(at + open.length, src.indexOf('});', at));
    const firstCall = body.split('\n').map((l) => l.trim()).find((l) => l && !l.startsWith('//'));
    expect(firstCall).toBe('suspendCustomCss();');
    expect(src).toContain('fe-css-immune');
  });
});
