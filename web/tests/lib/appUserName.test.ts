import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { appUserName } from '@/lib/explorerConfig';

/**
 * An app's own interface is told who is looking (`session.user.name`,
 * packages/app-ui protocol.ts `Session.user`): the display name, else the
 * username, never the e-mail address.
 *
 * ⚠ Measured 2026-10-10 in a filex built from main with the office editor
 * 0.3.0: no page of the SPA handed the name to an app (`ExplorerConfig.userName`
 * was never set, PluginPageView and the inspector section had no way to pass
 * it), so the editor named everyone "filex" - its own avatar, the author of
 * its comments and tracked changes - while editing together showed the right
 * names for the others, which the relay knows.
 */
describe('appUserName', () => {
  it('is the display name, else the username, never the e-mail address', () => {
    expect(appUserName({ display_name: 'Ayşe Yılmaz', username: 'ayse' })).toBe('Ayşe Yılmaz');
    expect(appUserName({ display_name: '  ', username: 'ayse' })).toBe('ayse');
    expect(appUserName({ display_name: '', username: '', email: 'ayse@example.test' } as never)).toBe('');
    expect(appUserName(null)).toBe('');
  });
});

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => fs.readFileSync(path.resolve(here, rel), 'utf8');

describe('every page that mounts an app interface tells it the name', () => {
  it('core: every AppFrame is given user-name, and the components between pass it on', () => {
    const core = '../../../packages/core/src/';
    for (const rel of [
      'modals/PreviewModal.vue',
      'components/plugin/AppFrameModal.vue',
      'components/plugin/PluginPageView.vue',
      'components/plugin/PluginInspectorSection.vue',
    ]) {
      const src = read(core + rel);
      const frames = src.split('<AppFrame').slice(1);
      expect(frames.length, rel).toBeGreaterThan(0);
      for (const f of frames) expect(f.slice(0, f.indexOf('/>')), rel).toContain(':user-name=');
    }
    const inspector = read(core + 'components/InspectorPanel.vue');
    expect(inspector.slice(inspector.indexOf('<PluginInspectorSection'))).toMatch(/^[^>]*:user-name="userName"/);
    const explorer = read(core + 'FileExplorer.vue');
    for (const tag of ['<PreviewModal', '<AppFrameModal', '<InspectorPanel']) {
      const at = explorer.indexOf(tag);
      expect(at, tag).toBeGreaterThan(0);
      const block = explorer.slice(at, explorer.indexOf('\n    />', at));
      expect(block, tag).toContain(':user-name="props.config.userName');
    }
  });

  it('web: the explorer config, the standalone editor and the app pages', () => {
    expect(read('../../src/views/Explore.vue')).toContain('userName: appUserName(auth.user)');
    expect(read('../../src/views/Editor.vue')).toContain(':user-name="userName"');
    for (const v of ['AppPage.vue', 'AppHome.vue', 'AppScreen.vue']) {
      const src = read(`../../src/views/${v}`);
      const at = src.indexOf('<PluginPageView');
      expect(src.slice(at, src.indexOf('/>', at)), v).toContain(':user-name="appUserName(auth.user)"');
    }
  });
});
