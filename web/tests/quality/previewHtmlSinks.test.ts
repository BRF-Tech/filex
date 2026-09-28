/**
 * Every place the explorer turns a string into markup (`v-html`, an
 * `innerHTML =` assignment) is either a static icon from filex's own
 * libraries or a preview that went through the one preview sanitizer
 * (packages/core/src/lib/sanitizeHtml.ts, DOMPurify).
 *
 * A preview draws somebody's FILE inside filex's page; a sink that skips the
 * sanitizer draws it with the person's session in reach. This check fails on
 * any new sink until it is added below — as a static icon, or as a preview
 * that calls sanitizeHtml in the same file — so the question is asked once,
 * when the sink is written, and never forgotten.
 */
import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';

const CORE = resolve(__dirname, '../../../packages/core/src');

/** Values built from filex's own icon/tile libraries (keyed by name). */
const STATIC_ICON = /^(actionIconSvg\(|fileIconTile\(|encryptedFolderTile\(|iconFor\(|iconHtml\(|rowIcon\(|entryTile\(|tileFor\(|typeTile$|tileHtml$|tile$|titleTile$|storageTile$|headIcon$|checkIcon$|a\.svg$)/;

/**
 * Preview output — each must be produced by sanitizeHtml in its file.
 * file (relative to packages/core/src) → the v-html expressions it draws.
 */
const PREVIEW_SINKS: Record<string, string[]> = {
  'modals/PreviewModal.vue': ['markdownHtml', 'codeHtml'],
  'viewers/IpynbViewer.vue': ['renderedSources.get(idx)', 'renderedMarkdown.get(idx)', 'outputHtml(out)'],
};

/**
 * Server-made markup that is not a file's content. The TOTP enrolment QR is
 * an SVG the server renders from the person's own secret.
 */
const SERVER_MARKUP: Record<string, string[]> = {
  'components/UserSettingsDialog.vue': ['totpQr'],
};

function files(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...files(p));
    else if (/\.(vue|ts)$/.test(name) && !/\.(test|spec)\.ts$/.test(name)) out.push(p);
  }
  return out;
}

describe('markup sinks in @brftech/filex-core', () => {
  const all = files(CORE);

  it('every v-html is a static icon, server markup, or a sanitized preview', () => {
    const unknown: string[] = [];
    for (const f of all) {
      const rel = relative(CORE, f).replace(/\\/g, '/');
      const src = readFileSync(f, 'utf8');
      for (const m of src.matchAll(/v-html="([^"]*)"/g)) {
        const expr = m[1].trim();
        if (STATIC_ICON.test(expr)) continue;
        if (SERVER_MARKUP[rel]?.includes(expr)) continue;
        if (PREVIEW_SINKS[rel]?.includes(expr)) continue;
        unknown.push(`${rel}: v-html="${expr}"`);
      }
    }
    expect(unknown, 'a new markup sink: draw it through lib/sanitizeHtml.ts and list it here').toEqual([]);
  });

  it('every preview sink sits in a file that sanitizes', () => {
    for (const rel of Object.keys(PREVIEW_SINKS)) {
      expect(readFileSync(join(CORE, rel), 'utf8'), rel).toMatch(/sanitizeHtml\(/);
    }
  });

  it('every innerHTML write is empty or sanitized', () => {
    const bad: string[] = [];
    for (const f of all) {
      const rel = relative(CORE, f).replace(/\\/g, '/');
      readFileSync(f, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          const m = line.match(/\.innerHTML\s*=\s*(.+?);?\s*$/);
          if (!m) return;
          const rhs = m[1].trim();
          if (rhs === "''" || rhs === '""' || rhs.startsWith('sanitizeHtml(')) return;
          bad.push(`${rel}:${i + 1}: ${line.trim()}`);
        });
    }
    expect(bad).toEqual([]);
  });
});
