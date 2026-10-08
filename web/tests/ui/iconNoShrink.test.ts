// An icon in the admin panel never gives up its width to the text beside it.
//
// ⚠ 0.53.0, tenancy/page-390.png: Multi-tenant mode's heading is an icon, a
// title and a sentence in one flex row. On a phone the sentence wraps, and the
// row took the room back from the icon - a 24 px building drawn as a speck at
// the start of the paragraph. Archives and Encryption (and every other admin
// heading) set their icon the same way, without `shrink-0`. The fix is one
// rule for the panel's icon set rather than a class on each icon, so this
// holds the rule, and that the rule reaches the icons the pages draw.
//
// A jsdom test cannot lay a row out; the measurement itself is the browser's
// (390 px, the three pages above, light and dark).
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { Archive, Building2, Lock } from 'lucide-vue-next';

const REPO = path.resolve(__dirname, '..', '..', '..');
const MAIN = readFileSync(path.join(REPO, 'web', 'src', 'styles', 'main.css'), 'utf8');

/** The body of the first `@layer base { … }` block, braces balanced. */
function baseLayer(css: string): string {
  const start = css.indexOf('@layer base {');
  expect(start, 'main.css has no @layer base block').toBeGreaterThanOrEqual(0);
  let depth = 0;
  for (let i = css.indexOf('{', start); i < css.length; i++) {
    if (css[i] === '{') depth++;
    else if (css[i] === '}' && --depth === 0) return css.slice(start, i + 1);
  }
  throw new Error('@layer base is not closed');
}

describe('admin icons keep their size in a flex row', () => {
  it('one base rule stops every lucide icon from shrinking, at zero specificity', () => {
    const rule = /:where\(svg\.lucide\)\s*\{\s*flex-shrink:\s*0;?\s*\}/.exec(baseLayer(MAIN));
    expect(rule, 'web/src/styles/main.css lost the `:where(svg.lucide) { flex-shrink: 0 }` base rule').not.toBeNull();
  });

  it('reaches the icons the headings draw: lucide renders an <svg class="lucide …">', () => {
    for (const icon of [Building2, Archive, Lock]) {
      const w = mount(icon, { props: { class: 'h-6 w-6' } });
      expect(w.element.tagName.toLowerCase()).toBe('svg');
      expect(w.classes()).toContain('lucide');
      w.unmount();
    }
  });

  it('the three headings from the report still draw their icon from that set', () => {
    for (const [file, icon] of [
      ['TenancyMode.vue', 'Building2'],
      ['Archives.vue', 'Archive'],
      ['Encryption.vue', 'Lock'],
    ] as const) {
      const src = readFileSync(path.join(REPO, 'web', 'src', 'views', file), 'utf8');
      expect(src, `${file} imports ${icon} from lucide-vue-next`).toMatch(new RegExp(`import \\{[^}]*\\b${icon}\\b[^}]*\\} from 'lucide-vue-next'`));
      expect(src, `${file} draws ${icon} beside its title`).toMatch(new RegExp(`<${icon} class="h-6 w-6`));
    }
  });
});
