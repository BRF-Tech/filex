// Every word the interface shows must be reachable by a language pack.
//
// A pack translates the CATALOGUES (packages/core/src/locales, web/src/locales)
// — nothing else. The Spanish pack, the first complete one, found the three
// ways a string escapes them (2026-09-21):
//
//   1. hard-coded in a template or an attribute — the admin footer
//      "filex · self-hosted file manager", "Missing in DB", aria-label="Close";
//   2. built ONCE at setup — `const columns = [{ label: t('…') }]` is
//      evaluated when the component mounts and never again, so the labels
//      keep the language the page was opened in (the Appearance table, the
//      role and state filters, the theme menu);
//   3. an inline `en ? '…' : '…'` pair — ~100 of them in the share dialog
//      and the explorer, which also meant a THIRD language got the Turkish.
//
// This scans for all three. It is a ratchet, not a style rule: an entry in an
// allow-list below has to say why the words are not interface text.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const ROOT = path.resolve(__dirname, '../../..');
const DIRS = [path.join(ROOT, 'web', 'src'), path.join(ROOT, 'packages', 'core', 'src')];

function walk(dir: string, ext: RegExp, out: string[] = []): string[] {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (!['node_modules', 'dist', 'locales'].includes(e.name)) walk(p, ext, out);
    } else if (ext.test(e.name)) out.push(p);
  }
  return out;
}

const vueFiles = DIRS.flatMap((d) => walk(d, /\.vue$/));
const rel = (f: string) => path.relative(ROOT, f).split(path.sep).join('/');
const lineOf = (src: string, idx: number) => src.slice(0, idx).split('\n').length;

/** Attribute values that are names or samples, not sentences. */
const ALLOWED_ATTRS = new Set([
  // an iframe's accessible name, naming the third-party editor it frames
  'title="diagrams.net editor"',
]);

describe('no interface text outside the catalogues', () => {
  it('finds templates to scan (a scan of nothing passes everything)', () => {
    expect(vueFiles.length).toBeGreaterThan(100);
  });

  it('no sentence written straight into a template', () => {
    const hits: string[] = [];
    for (const f of vueFiles) {
      const src = fs.readFileSync(f, 'utf8');
      const tm = src.match(/<template>([\s\S]*)<\/template>/);
      if (!tm) continue;
      const tpl = tm[1]
        .replace(/<!--[\s\S]*?-->/g, (c) => ' '.repeat(c.length))
        .replace(/<(code|pre|kbd|style|script)\b[\s\S]*?<\/\1>/g, (c) => ' '.repeat(c.length));
      const base = src.indexOf(tm[1]);
      const re = />([^<>]+)</g;
      let m: RegExpExecArray | null;
      while ((m = re.exec(tpl))) {
        const text = m[1].replace(/\{\{[\s\S]*?\}\}/g, ' ').trim();
        if (/[A-Za-z]{3,}\s+[A-Za-z]{2,}/.test(text)) hits.push(`${rel(f)}:${lineOf(src, base + m.index)}  ${text.slice(0, 70)}`);
      }
      const attr = /\s(title|placeholder|aria-label|label|alt)="([^"{]*)"/g;
      while ((m = attr.exec(tpl))) {
        if (!/[A-Za-z]{2,}\s+[A-Za-z]{2,}/.test(m[2]) || ALLOWED_ATTRS.has(`${m[1]}="${m[2]}"`)) continue;
        hits.push(`${rel(f)}:${lineOf(src, base + m.index)}  ${m[1]}="${m[2]}"`);
      }
    }
    expect(hits).toEqual([]);
  });

  /* ⚠ The rule above wants two words, so a single word glued to a number —
     `{{ formatNumber(total) }} results` in the admin search page — passed it,
     and every language saw "results" (wave-2 wording sweep, 2026-09-22). A
     word beside an interpolation is a sentence with a hole in it: it belongs
     in the catalogue, with the value as a placeholder. */
  const GLUE_OK = new Set(['filex', 'plugin', 'dav', 'MIT']); // a brand, a path, a licence

  it('no word glued to an interpolation', () => {
    const hits: string[] = [];
    for (const f of vueFiles) {
      const src = fs.readFileSync(f, 'utf8');
      const tm = src.match(/<template>([\s\S]*)<\/template>/);
      if (!tm) continue;
      const tpl = tm[1]
        .replace(/<!--[\s\S]*?-->/g, (c) => ' '.repeat(c.length))
        .replace(/<(code|pre|kbd|style|script)\b[\s\S]*?<\/\1>/g, (c) => ' '.repeat(c.length))
        // an interpolation may itself hold `<` (`n < 10 ? … : …`): blank it first
        .replace(/\{\{[\s\S]*?\}\}/g, (c) => `{{${' '.repeat(c.length - 4)}}}`);
      const base = src.indexOf(tm[1]);
      const re = />([^<>]+)</g;
      let m: RegExpExecArray | null;
      while ((m = re.exec(tpl))) {
        if (!m[1].includes('{{')) continue;
        const words = (m[1].replace(/\{\{[\s\S]*?\}\}/g, ' ').match(/[A-Za-z]{3,}/g) ?? []).filter((w) => !GLUE_OK.has(w));
        if (words.length) hits.push(`${rel(f)}:${lineOf(src, base + m.index)}  ${words.join(' ')}`);
      }
    }
    expect(hits).toEqual([]);
  });

  it('no label built once at setup', () => {
    const hits: string[] = [];
    for (const f of vueFiles) {
      const src = fs.readFileSync(f, 'utf8');
      const sm = src.match(/<script setup[^>]*>([\s\S]*?)<\/script>/);
      if (!sm) continue;
      const lines = sm[1].split('\n');
      for (let i = 0; i < lines.length; i++) {
        const d = lines[i].match(/^(?:export )?const (\w+)(\s*:[^=]+)?\s*=\s*(.*)$/);
        if (!d) continue;
        if (/^(computed|ref|reactive|shallowRef|watch|function|async|\(|use[A-Z]|defineProps|withDefaults|inject)/.test(d[3].trim())) continue;
        let text = d[3];
        let depth = 0;
        const count = (s: string) => {
          for (const c of s) {
            if ('[{('.includes(c)) depth += 1;
            else if (']})'.includes(c)) depth -= 1;
          }
        };
        count(d[3]);
        let j = i;
        while (depth > 0 && j + 1 < lines.length) {
          j += 1;
          text += `\n${lines[j]}`;
          count(lines[j]);
        }
        // A `t(…)` behind an arrow is evaluated when called — that is fine.
        const eager = text.replace(/=>\s*t\(/g, '=> _(');
        if (/(^|[^\w.$])t\(\s*['`]/.test(eager)) hits.push(`${rel(f)}: const ${d[1]}`);
      }
    }
    expect(hits).toEqual([]);
  });

  it('no inline pair of sentences chosen by language', () => {
    const files = DIRS.flatMap((d) => walk(d, /\.(vue|ts)$/));
    const hits: string[] = [];
    const pair = /(===|!==)\s*'(en|tr)'\s*\?\s*(['`])[^'`\n]*[A-Za-zçğıöşüÇĞİÖŞÜ]{2,}\s[^'`\n]*\3/g;
    for (const f of files) {
      const src = fs.readFileSync(f, 'utf8');
      let m: RegExpExecArray | null;
      while ((m = pair.exec(src))) hits.push(`${rel(f)}:${lineOf(src, m.index)}  ${m[0].slice(0, 70)}`);
    }
    expect(hits).toEqual([]);
  });

  it('the share dialog has no inline pairs left', () => {
    // The ratchet reached 0 at the v0.43.0 merge. PermissionsModal had ~90
    // `L('tr', 'en')` calls; the last mail-row lines (rewritten on the tables
    // branch in the same release, which added a sixth: "email is not set
    // up") now read access.ui.emails_comma_separated / access.ui.send /
    // access.ui.mail_not_set_up, and the `L` helper is gone.
    const src = fs.readFileSync(path.join(ROOT, 'packages/core/src/modals/PermissionsModal.vue'), 'utf8');
    const calls = (src.match(/\bL\(\s*['"`]/g) ?? []).length;
    expect(calls).toBe(0);
  });
});
