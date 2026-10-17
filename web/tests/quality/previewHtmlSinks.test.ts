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

/**
 * Markdown that is not a file's preview but can carry a file's words: the AI
 * assistant's answer (#269), which a file it read can steer. Each is drawn
 * only from lib/markdownHtml's markdownToSafeHtml under the sanitizer's
 * `assistant` policy, which loads nothing from elsewhere (a remote picture
 * would carry the file's words out with its request).
 */
const MARKDOWN_SINKS: Record<string, string[]> = {
  'components/AssistantPanel.vue': ['rendered[it.text]'],
};

/**
 * 0.57: a file's markup holds back every picture from another server
 * (lib/sanitizeHtml RemoteImages) and the person decides
 * (composables/useRemoteImages). So every drawing under the `document` or
 * the `diagram` policy - `sanitizeHtml(…, 'document' | 'diagram', …)` and
 * every `markdownToSafeHtml` that is not the assistant's - passes the gate's
 * policy, and the file that draws it answers a click on a placeholder. A
 * call without it would still hold the pictures, with no way for the person
 * to see them.
 */
function callsOf(src: string, name: string): string[] {
  const out: string[] = [];
  const re = new RegExp(`\\b${name}\\(`, 'g');
  for (const m of src.matchAll(re)) {
    let depth = 0;
    let end = -1;
    for (let i = m.index! + name.length; i < src.length; i++) {
      if (src[i] === '(') depth++;
      else if (src[i] === ')' && --depth === 0) {
        end = i;
        break;
      }
    }
    if (end > 0) out.push(src.slice(m.index!, end + 1));
  }
  return out;
}

/** The ones that draw under the `document` or the `diagram` policy (no kind = `document`). */
function documentCalls(src: string): string[] {
  return [
    ...callsOf(src, 'sanitizeHtml').filter((c) => !/,\s*'(code|math|assistant)'/.test(c)),
    ...callsOf(src, 'markdownToSafeHtml').filter((c) => !/,\s*'assistant'/.test(c)),
  ];
}

/**
 * The surfaces that draw thumbnails and folder previews. They never draw a
 * file's markup: a thumbnail loads no picture from another server, whatever
 * the person's setting says (the server draws a Markdown file's thumbnail
 * from its text, backend/internal/thumb/text.go).
 */
const THUMBNAIL_SURFACES = [
  'components/ThumbTile.vue',
  'components/FolderMosaic.vue',
  'components/FolderPeek.vue',
  'components/GridView.vue',
  'components/GalleryView.vue',
  'components/ListView.vue',
  'composables/useThumbs.ts',
  'lib/filePreview.ts',
];

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
        if (MARKDOWN_SINKS[rel]?.includes(expr)) continue;
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

  it("every Markdown sink sits in a file that draws markdownToSafeHtml's answer under the assistant policy", () => {
    for (const rel of Object.keys(MARKDOWN_SINKS)) {
      const src = readFileSync(join(CORE, rel), 'utf8');
      const calls = src.match(/markdownToSafeHtml\([^)]*\)/g) ?? [];
      expect(calls.length, rel).toBeGreaterThan(0);
      for (const c of calls) expect(c, rel).toMatch(/,\s*'assistant'\s*\)$/);
    }
  });

  it('every drawing under the document or diagram policy passes the remote-picture gate, and its file answers a placeholder', () => {
    const missing: string[] = [];
    for (const f of all) {
      const rel = relative(CORE, f).replace(/\\/g, '/');
      if (rel === 'lib/sanitizeHtml.ts' || rel === 'lib/markdownHtml.ts') continue;
      const src = readFileSync(f, 'utf8');
      const calls = documentCalls(src);
      for (const c of calls) if (!/remoteImages:\s*\w+\.policy\(\)/.test(c)) missing.push(`${rel}: ${c}`);
      if (calls.length && !/remoteImages\.onClick\b/.test(src)) missing.push(`${rel}: no remoteImages.onClick`);
    }
    expect(missing).toEqual([]);
    // The two that do it today, so a rename that silences the scan is seen.
    expect(Object.keys(PREVIEW_SINKS).every((rel) => documentCalls(readFileSync(join(CORE, rel), 'utf8')).length > 0)).toBe(true);
  });

  it('every Mermaid diagram is drawn by lib/mermaidRender, in its sealed frame', () => {
    const own: string[] = [];
    for (const f of all) {
      const rel = relative(CORE, f).replace(/\\/g, '/');
      if (rel === 'lib/mermaidRender.ts') continue;
      const src = readFileSync(f, 'utf8');
      if (/\bmermaid\??\.render\(|securityLevel\s*:/.test(src)) own.push(rel);
    }
    expect(own, 'draw a diagram with renderMermaid (lib/mermaidRender)').toEqual([]);
    for (const rel of ['modals/PreviewModal.vue', 'viewers/MermaidViewer.vue']) {
      expect(readFileSync(join(CORE, rel), 'utf8'), rel).toMatch(/renderMermaid\(/);
    }
  });

  it('every EPUB chapter is sealed before epub.js draws it (lib/epubRemote)', () => {
    const src = readFileSync(join(CORE, 'viewers/EpubViewer.vue'), 'utf8');
    expect(src).toMatch(/hooks\??\.content\??\.register\??\.?\(onChapter\)/);
    expect(src).toMatch(/sealChapter\(/);
  });

  it("no thumbnail or folder preview draws a file's markup", () => {
    for (const rel of THUMBNAIL_SURFACES) {
      const src = readFileSync(join(CORE, rel), 'utf8');
      expect(src, rel).not.toMatch(/sanitizeHtml|markdownToSafeHtml|markdown-it/);
      expect(PREVIEW_SINKS[rel], rel).toBeUndefined();
      expect(MARKDOWN_SINKS[rel], rel).toBeUndefined();
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

/**
 * The admin app (web/src) has its own few sinks. Since #122 one of them draws
 * markup that came from outside - a release's notes, Markdown a source
 * published - so the admin app is held to the same question: every v-html is
 * listed here with why it is safe, and none writes innerHTML.
 */
const WEB = resolve(__dirname, '../../src');
const WEB_SINKS: Record<string, string[]> = {
  // Static markup from lib/actionIcons. (The menu's app rows used to be the
  // third, in components/Sidebar.vue; since 0.51 core's MegaMenu draws them
  // from an icon NAME, under the package's own rule above.)
  'views/AppHome.vue': ['icon'],
  'views/AppScreen.vue': ['icon'],
  // A release's notes: only markdownToSafeHtml's answer (markdown-it, then
  // sanitizeHtml 'document').
  'components/plugins/ReleaseNotes.vue': ['html'],
};

describe('markup sinks in the admin app', () => {
  const all = files(WEB);

  it('every v-html is listed, and the release notes draw only the sanitized pipeline', () => {
    const unknown: string[] = [];
    for (const f of all) {
      const rel = relative(WEB, f).replace(/\\/g, '/');
      const src = readFileSync(f, 'utf8');
      for (const m of src.matchAll(/v-html="([^"]*)"/g)) {
        if (!WEB_SINKS[rel]?.includes(m[1].trim())) unknown.push(`${rel}: v-html="${m[1].trim()}"`);
      }
    }
    expect(unknown, 'a new markup sink in web/src: sanitize it and list it here').toEqual([]);
    expect(readFileSync(join(WEB, 'components/plugins/ReleaseNotes.vue'), 'utf8')).toMatch(/markdownToSafeHtml\(/);
  });

  it("the release notes' pictures from other servers wait for the gate, as a preview's do", () => {
    const src = readFileSync(join(WEB, 'components/plugins/ReleaseNotes.vue'), 'utf8');
    const calls = documentCalls(src);
    expect(calls.length).toBeGreaterThan(0);
    for (const c of calls) expect(c).toMatch(/remoteImages:\s*\w+\.policy\(\)/);
    expect(src).toMatch(/@click="remoteImages\.onClick"/);
    expect(src).toMatch(/<RemoteImageDialog\b/);
  });

  it('no innerHTML is written', () => {
    const bad: string[] = [];
    for (const f of all) {
      const rel = relative(WEB, f).replace(/\\/g, '/');
      readFileSync(f, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (/\.innerHTML\s*=(?!=)/.test(line)) bad.push(`${rel}:${i + 1}: ${line.trim()}`);
        });
    }
    expect(bad).toEqual([]);
  });
});
