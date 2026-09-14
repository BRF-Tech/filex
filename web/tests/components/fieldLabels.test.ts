// A field says what it is for after something has been typed into it.
//
// ⚠ Found by the v0.41.0 screenshot pass (2026-09-14): the encrypted-folder
// dialog's three fields — name, password, repeat — had placeholders and no
// labels. A placeholder disappears as soon as the field holds a value, and on a
// password field the value is dots, so a half-filled form showed two identical
// boxes of dots with nothing saying which was which. Measured in the browser:
// `input.labels` empty, no `aria-label`, no `aria-labelledby` on any of them.
// The plain New folder dialog that opens it had the same shape.
//
// Pinned two ways: the dialogs themselves, mounted, and every password field
// in packages/core, read from source — a password box is the one field that can
// never lean on its own contents to explain itself.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import EncryptedFolderModal from '@brftech/filex-core/src/components/EncryptedFolderModal.vue';
import NewFolderModal from '@brftech/filex-core/src/modals/NewFolderModal.vue';

function fieldNames(root: Element): Array<{ type: string; name: string }> {
  return Array.from(root.querySelectorAll<HTMLInputElement>('input:not([type="checkbox"])')).map((i) => ({
    type: i.type,
    name: (i.labels?.[0]?.textContent ?? i.getAttribute('aria-label') ?? '').trim(),
  }));
}

describe('the folder dialogs name their fields', () => {
  for (const locale of ['en', 'tr'] as const) {
    it(`encrypted folder (${locale}): name, password and repeat each have a visible label`, () => {
      const w = mount(EncryptedFolderModal, { props: { open: true, locale }, attachTo: document.body });
      const names = fieldNames(document.body);
      expect(names.map((n) => n.type)).toEqual(['text', 'password', 'password']);
      for (const n of names) expect(n.name, `a ${n.type} field has no label`).not.toBe('');
      // Two password boxes, two different names — the whole point.
      expect(names[1].name).not.toBe(names[2].name);
      if (locale === 'en') expect(names.map((n) => n.name)).toEqual(['Folder name', 'Folder password', 'Repeat the password']);
      else expect(names.map((n) => n.name)).toEqual(['Klasör adı', 'Klasör parolası', 'Parolayı tekrar girin']);
      w.unmount();
      document.body.innerHTML = '';
    });
  }

  it('new folder: the name field has a visible label', () => {
    const w = mount(NewFolderModal, { props: { open: true, locale: 'en' }, attachTo: document.body });
    expect(fieldNames(document.body)).toEqual([{ type: 'text', name: 'Folder name' }]);
    w.unmount();
    document.body.innerHTML = '';
  });
});

describe('every password field in packages/core has a name', () => {
  const CORE = resolve(__dirname, '../../../packages/core/src');
  const files: string[] = [];
  (function walk(dir: string) {
    for (const n of readdirSync(dir)) {
      const p = join(dir, n);
      if (statSync(p).isDirectory()) walk(p);
      else if (n.endsWith('.vue')) files.push(p);
    }
  })(CORE);

  const sites: Array<{ file: string; tag: string; before: string }> = [];
  for (const f of files) {
    const src = readFileSync(f, 'utf8');
    for (const m of src.matchAll(/<input\b[^>]*?type="password"[^>]*?\/?>/gs)) {
      sites.push({ file: f.slice(CORE.length + 1), tag: m[0], before: src.slice(Math.max(0, m.index! - 400), m.index) });
    }
  }

  it('finds the password fields (a scanner that found none would pass everything)', () => {
    expect(sites.length).toBeGreaterThanOrEqual(4);
  });

  for (const s of sites) {
    it(`${s.file}: ${s.tag.replace(/\s+/g, ' ').slice(0, 70)}…`, () => {
      const labelled =
        /aria-label(ledby)?=/.test(s.tag) ||
        // wrapped: the nearest <label …> before it is not closed before the input
        s.before.lastIndexOf('<label') > s.before.lastIndexOf('</label>');
      expect(labelled, 'wrap it in <label class="fe-field"> or give it an aria-label').toBe(true);
    });
  }
});
