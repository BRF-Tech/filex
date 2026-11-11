// The public link page's class names are a contract (lib/publicPageClasses):
// an administrator's page.shell template restyles the card through them
// (internal/templates, docs/TEMPLATES.md "The public link page"). A class
// renamed in a component, or left out of the documentation, is red here
// before it silently undoes somebody's design.
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { PUBLIC_PAGE_ATTRIBUTES, PUBLIC_PAGE_CLASSES } from '../../../packages/core/src/lib/publicPageClasses';

const here = path.dirname(fileURLToPath(import.meta.url));
const publicDir = path.resolve(here, '../../../packages/core/src/components/public');
const components = ['PublicShell.vue', 'PublicPinGate.vue', 'PublicShareBody.vue', 'PublicRequestBody.vue'].map((f) =>
  fs.readFileSync(path.join(publicDir, f), 'utf8'),
);
const docs = fs.readFileSync(path.resolve(here, '../../../docs/TEMPLATES.md'), 'utf8');

/** Every class a component's markup names (`class="a b"`, `:class` strings). */
const used = new Set(components.join('\n').match(/fe-ppage[a-zA-Z0-9_-]*/g) ?? []);

describe('the public link page classes', () => {
  it('reads the components (a match that finds nothing proves nothing)', () => {
    expect(used.size).toBeGreaterThan(20);
  });

  for (const cls of PUBLIC_PAGE_CLASSES) {
    it(`${cls} is still on the page`, () => {
      expect(used.has(cls), `${cls} is part of the template contract and no component draws it any more`).toBe(true);
    });
    it(`${cls} is documented`, () => {
      expect(docs.includes('`.' + cls + '`'), `${cls} is not in docs/TEMPLATES.md`).toBe(true);
    });
  }

  for (const attr of PUBLIC_PAGE_ATTRIBUTES) {
    it(`${attr} is on the page and documented`, () => {
      expect(components.join('\n').includes(attr)).toBe(true);
      expect(docs.includes('`' + attr + '`')).toBe(true);
    });
  }
});
