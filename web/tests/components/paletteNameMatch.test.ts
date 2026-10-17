// The ⌘K palette's "Files" group matches the folder's files with the
// explorer's own folding (lib/fileFilters foldText), not `toLocaleLowerCase`
// (task #238): a name a Mac uploaded decomposed never answered the composed
// word a keyboard types, and a browser running in Turkish lower-cased
// `FILE.txt` to `fıle.txt`, so "file" missed it. And a found name is shown
// through `visibleName`: a RIGHT-TO-LEFT OVERRIDE cannot hide its extension.

import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import CommandPalette from '@brftech/filex-core/src/components/CommandPalette.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { hostileName } from '../../../e2e/helpers/hostile-names.mjs';

let wrapper: VueWrapper | null = null;
afterEach(() => {
  wrapper?.unmount();
  wrapper = null;
});

const node = (basename: string): FileNode =>
  ({ path: `docs://${basename}`, basename, type: 'file', extension: basename.split('.').pop() ?? '' }) as FileNode;

async function labelsFor(files: FileNode[], q: string): Promise<string[]> {
  wrapper = mount(CommandPalette, {
    props: { open: false, locale: 'en' as const, files, viewMode: 'list' as const },
    attachTo: document.body,
  });
  await wrapper.setProps({ open: true });
  await wrapper.vm.$nextTick();
  await wrapper.find('.fe-cmdp__input').setValue(q);
  await wrapper.vm.$nextTick();
  return wrapper.findAll('.fe-cmdp__item .fe-cmdp__label').map((l) => l.text());
}

describe('the palette finds the folder’s files the way the explorer does', () => {
  it('the composed word finds the decomposed name', async () => {
    const nfd = hostileName('nfd-butce').name;
    const labels = await labelsFor([node(nfd), node('other.txt')], hostileName('nfc-butce').name.replace(/\.txt$/, ''));
    expect(labels).toContain(nfd);
  });

  it('the override is drawn as a marker, the extension last', async () => {
    const rlo = hostileName('rlo-extension').name;
    const labels = await labelsFor([node(rlo)], 'invoice');
    expect(labels).toContain(`invoice${String.fromCodePoint(0xfffd)}fdp.exe`);
  });
});
