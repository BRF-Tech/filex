// Issue #104 - an entry the storage could not answer for, said in words.
//
// The sync keeps and MARKS a row whose object its Stat could neither find nor
// rule out (a storage plugin that cannot look at a folder, a permission it
// lacks, a backend error). The listing carries `unavailable: true` and the
// storage's own answer in `unavailable_reason`; the server refuses every
// operation on it, or inside it, with 409 `ENTRY_UNAVAILABLE`. What the
// explorer owes the person: a "!" on the row that says why, the same sentence
// in the details panel and in the toast after a refused open, the refusal in
// words, and no action offered that the server would refuse.
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

// ⚠ The bare specifier resolves to `packages/core/dist`: the export surface
// every embedder imports (see symlink.test.ts beside this file).
import { isUnavailable, unavailableWordsFor } from '@brftech/filex-core';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import ListView from '@brftech/filex-core/src/components/ListView.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import GalleryView from '@brftech/filex-core/src/components/GalleryView.vue';
import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';
import { nodeRowToFileNode } from '@brftech/filex-core/src/lib/nodeRow';
import { requestFailure } from '@brftech/filex-core/src/lib/errorWords';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');

const host = {
  t: (key: string, vars: Record<string, string | number> = {}) =>
    Object.entries(vars).reduce((s, [k, v]) => s.replaceAll(`{${k}}`, String(v)), en[key] ?? key),
};

const REASON = 'plugin: stat is not implemented for folders (http 500)';

function row(extra: Record<string, unknown> = {}): FileNode {
  return {
    id: 7,
    type: 'dir',
    path: 'arsiv://Proje',
    basename: 'Proje',
    extension: '',
    size: 0,
    last_modified: Date.UTC(2026, 9, 1, 9),
    storage: 'arsiv',
    ...extra,
  } as unknown as FileNode;
}

describe('reading the row', () => {
  it('is unavailable only when the server says so', () => {
    expect(isUnavailable(row({ unavailable: true }))).toBe(true);
    expect(isUnavailable(row())).toBe(false);
    expect(isUnavailable(row({ unavailable: false }))).toBe(false);
    expect(isUnavailable(null)).toBe(false);
  });

  it('says what it means, then what the storage answered', () => {
    const w = unavailableWordsFor(row({ unavailable: true, unavailable_reason: REASON }), host)!;
    expect(w.badge).toBe('!');
    expect(w.why).toBe(en['unavailable.why.dir']);
    expect(w.reason).toBe(REASON);
    expect(w.full).toContain(w.why);
    expect(w.full).toContain(REASON);
    // A file has its own sentence: a folder's says its contents are locked too.
    const f = unavailableWordsFor(row({ type: 'file', unavailable: true }), host)!;
    expect(f.why).toBe(en['unavailable.why.file']);
    expect(f.full).toBe(f.why);
    expect(unavailableWordsFor(row(), host)).toBeNull();
  });
});

describe('the catalogues', () => {
  const KEYS = ['unavailable.why.file', 'unavailable.why.dir', 'unavailable.withReason', 'unavailable.inspector', 'err.entry_unavailable'];

  it('carries every key in both languages, translated', () => {
    for (const k of KEYS) {
      expect(en[k], `en.ts is missing ${k}`).toBeTruthy();
      expect(tr[k], `tr.ts is missing ${k}`).toBeTruthy();
      expect(tr[k], `${k} was never translated`).not.toBe(en[k]);
    }
  });

  it('⚠ Turkish is written in Turkish, and says "depo"', () => {
    expect(tr['unavailable.why.dir']).toContain('klasörün');
    expect(tr['unavailable.why.dir']).toContain('Depo');
    expect(tr['unavailable.inspector']).toBe('Kullanılamıyor');
    for (const k of KEYS) {
      expect(tr[k]).not.toMatch(/\b(kullanilamiyor|klasorun|degistirilemez|acilamaz)\b/i);
      expect(en[k] + tr[k]).not.toMatch(/[‐-―−]/);
    }
  });
});

describe('every view draws it', () => {
  const props = (files: FileNode[]) => ({
    props: { selected: new Set<string>(), locale: 'en' as const, files },
  });

  for (const [name, View] of [
    ['list', ListView],
    ['grid', GridView],
    ['gallery', GalleryView],
  ] as const) {
    it(`${name}: a "!" with the sentence and the storage's answer`, () => {
      const w = mount(View as never, props([row({ unavailable: true, unavailable_reason: REASON })]));
      const badge = w.find('[data-testid="unavailable-badge"]');
      expect(badge.exists(), `${name} view draws no badge`).toBe(true);
      expect(badge.text()).toBe('!');
      expect(badge.attributes('title')).toContain(en['unavailable.why.dir']);
      expect(badge.attributes('title')).toContain(REASON);
      expect(badge.attributes('aria-label')).toBe(badge.attributes('title'));
    });

    it(`${name}: an ordinary row carries nothing`, () => {
      const w = mount(View as never, props([row()]));
      expect(w.find('[data-testid="unavailable-badge"]').exists()).toBe(false);
    });
  }

  it('⚠ the list badge is OUTSIDE `.fe-list__name` (lesson #29)', () => {
    const w = mount(ListView, props([row({ unavailable: true })]));
    expect(w.find('.fe-list__name').text()).toBe('Proje');
    expect(w.find('.fe-list__name [data-testid="unavailable-badge"]').exists()).toBe(false);
  });
});

describe('the details panel', () => {
  const api = {
    listShares: async () => [],
    listVersions: async () => [],
    listPermissions: async () => [],
    listComments: async () => [],
  };

  it('says why, with the storage’s answer on its own line', () => {
    const w = mount(InspectorPanel, {
      props: {
        api: api as never,
        nodes: [row({ unavailable: true, unavailable_reason: REASON })] as never,
        dirLabel: 'arsiv',
        dirCount: 1,
        locale: 'en',
      },
    });
    const note = w.find('[data-testid="inspector-unavailable"]');
    expect(note.exists()).toBe(true);
    expect(note.text()).toContain(en['unavailable.inspector']);
    expect(note.text()).toContain(en['unavailable.why.dir']);
    expect(note.text()).toContain(REASON);
  });
});

describe('rows from outside a folder listing', () => {
  it('Recent, Starred and Home carry the flag from the raw node row', () => {
    const n = nodeRowToFileNode(
      { id: 9, path: 'Proje', name: 'Proje', type: 'dir', storage: 'arsiv', unavailable: true, unavailable_reason: REASON },
      { storages: [{ name: 'arsiv' }], multiStorageRoot: false },
    )!;
    expect(n.unavailable).toBe(true);
    expect(n.unavailable_reason).toBe(REASON);
    const plain = nodeRowToFileNode({ id: 10, path: 'a.txt', name: 'a.txt', type: 'file', storage: 'arsiv' }, {
      storages: [{ name: 'arsiv' }],
      multiStorageRoot: false,
    })!;
    expect(plain.unavailable).toBeUndefined();
  });
});

describe('the server’s refusal, in words', () => {
  it('409 ENTRY_UNAVAILABLE is not "already exists"', () => {
    const body = JSON.stringify({ error: 'main://Proje is unavailable: …', code: 'ENTRY_UNAVAILABLE', path: 'main://Proje', reason: REASON });
    const err = requestFailure(409, body, 'en');
    expect(err.message).toBe(en['err.entry_unavailable']);
    expect(requestFailure(409, body, 'tr').message).toBe(tr['err.entry_unavailable']);
  });
});

describe('the explorer refuses it before the server has to', () => {
  // ⚠ A source scan, as symlink.test.ts does: FileExplorer.vue is far too large
  // to mount here, and what is pinned is an ORDER and a filter.
  const src = readFileSync(path.join(CORE_SRC, 'FileExplorer.vue'), 'utf8');

  it('openNode refuses it, out loud, before it can navigate or open anything', () => {
    const openNode = src.slice(src.indexOf('function openNode(n: FileNode)'));
    const body = openNode.slice(0, openNode.indexOf('\nconst VIEW_DEFAULT_EXTS'));
    const refusal = body.search(/const\s+gone\s*=\s*unavailableWordsFor\(\s*n\s*,\s*\{\s*t\s*\}\s*\)\s*;/);
    expect(refusal, 'openNode does not ask lib/unavailable').toBeGreaterThan(-1);
    expect(body).toMatch(/if\s*\(\s*gone\s*\)\s*\{\s*showToast\(\s*\{\s*message:\s*gone\.full\s*\}/);
    for (const later of ["n.type === 'dir'", "emit('file-opened'"]) {
      expect(body.indexOf(later), `openNode reaches ${later} before refusing`).toBeGreaterThan(refusal);
    }
  });

  it('the menu, the toolbar and the keyboard offer nothing on it but its details', () => {
    const fn = src.slice(src.indexOf('function selectionActionList(sel: FileNode[])'));
    const body = fn.slice(0, fn.indexOf('\nfunction selectionActionListAll'));
    expect(body).toMatch(/sel\.some\(\s*isUnavailable\s*\)/);
    expect(body).toMatch(/a\.divider\s*\|\|\s*a\.key\s*===\s*'details'\s*\?\s*a\s*:\s*\{\s*\.\.\.a,\s*disabled:\s*true\s*\}/);
  });
});
