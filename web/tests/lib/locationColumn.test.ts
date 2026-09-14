// Where a row lives, in Starred / Recent / a tag view / search results.
//
// Reported off a screenshot and measured on the current build, 2026-09-14: the
// Location column of a starred file in a storage called `My files` read
// `My files/My files://Photos` — the storage twice and the scheme on show.
//
// The list, the grid and the gallery each had a private `parentDir` that
// stripped a URL-SCHEME pattern (`[a-z][a-z0-9+.-]*` then `://`) from the
// qualified path. A storage name is not a scheme: `My files` has a space, so
// the pattern did not match, nothing was stripped, and the "folder" came out
// as `My files://Photos`. The list then put the storage in front of that. An
// underscore or a leading digit broke it the same way; `qldemo` did not, which
// is why nobody saw it on the development instance.
//
// The three copies are gone. One helper, `parentDirOf` in lib/listing, splits
// on the first `://`, and all three views call it.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import ListView from '@brftech/filex-core/src/components/ListView.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import { parentDirOf } from '@brftech/filex-core/src/lib/listing';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const CORE = path.resolve(__dirname, '../../../packages/core/src');

function node(storage: string, rel: string): FileNode {
  const basename = rel.split('/').pop() ?? rel;
  return {
    id: 4,
    type: 'file',
    path: `${storage}://${rel}`,
    basename,
    extension: basename.split('.').pop() ?? '',
    size: 10,
    last_modified: Date.UTC(2026, 8, 13, 12),
    storage,
  } as unknown as FileNode;
}

describe('parentDirOf', () => {
  it('takes the storage off whatever the storage is called', () => {
    expect(parentDirOf('My files://Photos/cover.jpg')).toBe('Photos');
    expect(parentDirOf('my_drive://a/b/c.txt')).toBe('a/b');
    expect(parentDirOf('2024 archive://x/y.pdf')).toBe('x');
    expect(parentDirOf('qldemo://Documents/notes.txt')).toBe('Documents');
  });

  it('is empty for a row at a storage root', () => {
    expect(parentDirOf('My files://cover.jpg')).toBe('');
    expect(parentDirOf('qldemo://a.txt')).toBe('');
  });

  it('never leaves a scheme in what it returns', () => {
    for (const p of ['My files://Photos/a.jpg', 'Ünlü Depo://Belgeler/Şubat/fatura.pdf', 'a.b-c+d://e/f']) {
      expect(parentDirOf(p)).not.toContain('://');
    }
  });
});

describe('the views print it', () => {
  it('the list: "My files/Photos", not "My files/My files://Photos"', () => {
    const w = mount(ListView, {
      props: {
        selected: new Set<string>(),
        locale: 'en' as const,
        showParentPath: true,
        files: [node('My files', 'Photos/cover.jpg')],
      },
    });
    const cells = w.findAll('.fe-list__row .fe-list__col--location').map((c) => c.text());
    expect(cells).toEqual(['My files/Photos']);
  });

  it('the grid card names the folder alone', () => {
    const w = mount(GridView, {
      props: {
        selected: new Set<string>(),
        locale: 'en' as const,
        showParentPath: true,
        files: [node('My files', 'Photos/cover.jpg')],
      },
    });
    expect(w.find('.fe-grid__parent').text()).toBe('Photos');
  });

  it('no view carries its own copy of the rule again', () => {
    for (const f of ['components/ListView.vue', 'components/GridView.vue', 'components/GalleryView.vue']) {
      const src = readFileSync(path.join(CORE, f), 'utf8');
      expect(src, `${f} defines its own parentDir`).not.toMatch(/function\s+parentDir\s*\(/);
      expect(src, `${f} strips a URL scheme to find a storage`).not.toContain('a-z0-9+.-]*:');
    }
  });
});
