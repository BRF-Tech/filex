// Issue #34 — a symlink the server will not follow, said in words.
//
// The reporter's complaint was not "filex does not follow my link". It was
// that a link to a directory inside a storage root showed as a 0-byte file
// that would not open, with nothing on screen explaining either half. The
// backend answered the first half (a link whose target is INSIDE the root is
// now followed and arrives as the directory it is) and flagged the second
// (`symlink: true`, sometimes with a `link_state`). Until this module nothing
// rendered the flag, so an out-of-root link was still an ordinary-looking file
// that mysteriously fails — the original complaint, half answered.
//
// ⚠⚠ THE FACT THE WHOLE DESIGN TURNS ON, measured in `handlers/manager.go`:
// the listing has TWO projectors, and only one of them carries a reason.
//   · `projectFileNodes` (:1412) — the DB cache, i.e. the NORMAL listing —
//     emits `symlink: true` and nothing else. `model.Node` has no column for
//     the state, because the state describes the link as it is now and the
//     catalogue records what was seen at scan time.
//   · `projectDriverObjects` (:826) — the cold-cache / pre-sync fallback —
//     reads the driver and does carry `link_state`.
// So "flagged, with no reason" is the case a warm installation hits EVERY
// time, and a design that only understood the three named states would have
// left the reporter's own screen exactly as broken as before. That is what
// `'unknown'` is, and why it has wording of its own rather than a fallback to
// nothing.
//
// `followed` never reaches the browser at all: by the time manager.go looks,
// a followed link has already become KindDirectory/KindFile. `symlink: true`
// on the wire therefore always means "this one will not open".
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

// ⚠⚠ The bare specifier resolves to `packages/core/dist`, NOT `src` — so this
// file only sees a change after `pnpm -r --filter './packages/*' build`. It is
// deliberate: it is the export surface every embedder actually imports, and
// `appLock.test.ts` beside it pins its module the same way.
import { isUnopenableLink, linkStateOf, linkWords, linkWordsFor } from '@brftech/filex-core';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import ListView from '@brftech/filex-core/src/components/ListView.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import GalleryView from '@brftech/filex-core/src/components/GalleryView.vue';
import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';
import { nodeRowToFileNode } from '@brftech/filex-core/src/lib/nodeRow';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');

/** A stand-in for the explorer's locale helper — the real English catalogue. */
const host = { t: (key: string) => en[key] ?? key };

/** A listing row as the wire delivers it. */
function row(extra: Record<string, unknown> = {}): FileNode {
  return {
    id: 7,
    type: 'file',
    path: 'e2e-local://shared',
    basename: 'shared',
    extension: '',
    size: 0,
    last_modified: Date.UTC(2026, 8, 21, 9),
    storage: 'e2e-local',
    ...extra,
  } as unknown as FileNode;
}

describe('reading the row', () => {
  it('names the three states the driver can send', () => {
    expect(linkStateOf(row({ symlink: true, link_state: 'outside_root' }))).toBe('outside_root');
    expect(linkStateOf(row({ symlink: true, link_state: 'broken' }))).toBe('broken');
    expect(linkStateOf(row({ symlink: true, link_state: 'unresolved' }))).toBe('unresolved');
  });

  it('⚠⚠ still answers for a flag with NO reason — the warm-cache listing', () => {
    // `projectFileNodes` sends exactly this, on every ordinary listing. If this
    // returned null the badge would be absent on the one screen the issue was
    // filed about, and the whole fix would be dead in production while every
    // other assertion here stayed green.
    expect(linkStateOf(row({ symlink: true }))).toBe('unknown');
    // And for a state invented by a server newer than this client: an
    // unrecognised word is still "a link that will not open", never silence.
    expect(linkStateOf(row({ symlink: true, link_state: 'something_new' }))).toBe('unknown');
  });

  it('is null for every ordinary row, including a link that WAS followed', () => {
    // A followed link arrives as its target — a directory, with the target's
    // size and mtime and no flag at all. Nothing to explain, nothing to badge.
    expect(linkStateOf(row({ type: 'dir', size: 4096 }))).toBeNull();
    expect(linkStateOf(row())).toBeNull();
    expect(linkStateOf(row({ symlink: false, link_state: 'outside_root' }))).toBeNull();
    expect(linkStateOf(null)).toBeNull();
    expect(linkStateOf(undefined)).toBeNull();
  });

  it('is the same question `isUnopenableLink` asks', () => {
    expect(isUnopenableLink(row({ symlink: true }))).toBe(true);
    expect(isUnopenableLink(row({ symlink: true, link_state: 'broken' }))).toBe(true);
    expect(isUnopenableLink(row({ type: 'dir' }))).toBe(false);
  });
});

describe('the words', () => {
  it('says what it is, why it will not open, and who can allow it', () => {
    const w = linkWords('outside_root', host)!;
    expect(w.badge).toBe('Outside storage');
    expect(w.why).toContain('outside this storage');
    expect(w.why).toContain('cannot be opened');
    // The remedy, named the way the storage form names it — an operator has to
    // be able to find the switch from this sentence alone.
    expect(w.why).toContain('Follow symlinks that leave this folder');
    expect(w.why).toContain('administrator');
  });

  it('⚠⚠ a BROKEN link reads differently and offers no setting', () => {
    const broken = linkWords('broken', host)!;
    const outside = linkWords('outside_root', host)!;
    expect(broken.why).not.toBe(outside.why);
    expect(broken.badge).not.toBe(outside.badge);
    // Sending somebody to a storage setting for a target that has been deleted
    // is sending them somewhere that cannot help, and it buries the only thing
    // that can: fix or remove the link on the server.
    expect(broken.why).not.toContain('Follow symlinks that leave this folder');
    expect(broken.why).toMatch(/no longer exists/i);
    expect(broken.why).toMatch(/repaired or removed/i);
  });

  it('a remote link says it is remote, and the reasonless one stays honest', () => {
    expect(linkWords('unresolved', host)!.why).toMatch(/remote server/i);
    const unknown = linkWords('unknown', host)!;
    // It must not claim a reason it does not have, and must not be silent:
    // it names all three possibilities.
    expect(unknown.why).toMatch(/outside this storage/i);
    expect(unknown.why).toMatch(/missing/i);
    expect(unknown.why).toMatch(/remote server/i);
  });

  it('every state has a badge short enough for a row', () => {
    for (const s of ['outside_root', 'broken', 'unresolved', 'unknown'] as const) {
      const w = linkWords(s, host)!;
      expect(w.badge.split(/\s+/).length, `${s} badge is a sentence`).toBeLessThanOrEqual(2);
      expect(w.why.length, `${s} has no sentence behind the badge`).toBeGreaterThan(40);
    }
    expect(linkWords(null, host)).toBeNull();
  });

  it('`linkWordsFor` is the one call a view makes', () => {
    expect(linkWordsFor(row({ symlink: true, link_state: 'broken' }), host)!.state).toBe('broken');
    expect(linkWordsFor(row(), host)).toBeNull();
  });
});

describe('the catalogues', () => {
  const KEYS = [
    'symlink.badge.outside_root',
    'symlink.badge.broken',
    'symlink.badge.unresolved',
    'symlink.badge.unknown',
    'symlink.why.outside_root',
    'symlink.why.broken',
    'symlink.why.unresolved',
    'symlink.why.unknown',
    'symlink.inspector',
  ];

  it('carries every key in both languages', () => {
    for (const k of KEYS) {
      expect(en[k], `en.ts is missing ${k}`).toBeTruthy();
      expect(tr[k], `tr.ts is missing ${k}`).toBeTruthy();
      expect(tr[k], `${k} was never translated`).not.toBe(en[k]);
    }
  });

  it('⚠ Turkish is written in Turkish, not ASCII-folded', () => {
    // House rule: ASCII-folded Turkish ("Depo disinda", "Kirik bag") is a
    // defect here, not a typo. The words below are the ones that lose their
    // letters first.
    expect(tr['symlink.badge.outside_root']).toContain('dışında');
    expect(tr['symlink.badge.broken']).toContain('Kırık');
    expect(tr['symlink.why.outside_root']).toContain('deponun');
    expect(tr['symlink.why.outside_root']).toContain('yönetici');
    expect(tr['symlink.why.broken']).toContain('artık');
    expect(tr['symlink.why.broken']).toContain('kaldırılması');
    for (const k of KEYS) {
      expect(tr[k], `${k} reads as ASCII-folded Turkish`).not.toMatch(
        /\b(disinda|Kirik|yonetici|acilamiyor|baglanti|cozumleyemedigi)\b/i,
      );
    }
  });

  it('the Turkish sentence names the switch by its Turkish label', () => {
    // web/src/locales/tr.json → storages.fields.followSymlinks. An operator
    // reading the toast has to recognise the row on the storage form.
    const label = JSON.parse(
      readFileSync(path.resolve(__dirname, '../../src/locales/tr.json'), 'utf8'),
    ).storages.fields.followSymlinks as string;
    expect(tr['symlink.why.outside_root']).toContain(label);
    const enLabel = JSON.parse(
      readFileSync(path.resolve(__dirname, '../../src/locales/en.json'), 'utf8'),
    ).storages.fields.followSymlinks as string;
    expect(en['symlink.why.outside_root']).toContain(enLabel);
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
    it(`${name}: badged, with the sentence on hover and for a screen reader`, () => {
      const w = mount(View as never, props([row({ symlink: true, link_state: 'outside_root' })]));
      const badge = w.find('[data-testid="symlink-badge"]');
      expect(badge.exists(), `${name} view draws no badge`).toBe(true);
      // The glyph rides inside the badge (aria-hidden), so the visible text is
      // the glyph plus the words — `toContain`, not `toBe`.
      expect(badge.text()).toContain('Outside storage');
      expect(badge.attributes('data-link-state')).toBe('outside_root');
      // ⚠ The badge's two words are a riddle read aloud; the label is the
      // sentence, exactly as `lib/symlink` wrote it. One source of words.
      expect(badge.attributes('aria-label')).toBe(en['symlink.why.outside_root']);
      expect(badge.attributes('title')).toBe(en['symlink.why.outside_root']);
    });

    it(`${name}: an ordinary row carries nothing`, () => {
      const w = mount(View as never, props([row()]));
      expect(w.find('[data-testid="symlink-badge"]').exists()).toBe(false);
    });

    it(`${name}: a reasonless flag is still badged`, () => {
      const w = mount(View as never, props([row({ symlink: true })]));
      const badge = w.find('[data-testid="symlink-badge"]');
      expect(badge.exists(), `${name} view goes silent on the common case`).toBe(true);
      expect(badge.attributes('data-link-state')).toBe('unknown');
    });
  }

  it('⚠ the list badge is OUTSIDE `.fe-list__name` (lesson #29)', () => {
    // The shot scripts and `desktop/scripts/lib/harness.mjs` find a row by that
    // element's textContent. A name reading "shared Outside storage" breaks
    // every one of them while the product looks perfect.
    const w = mount(ListView, props([row({ symlink: true, link_state: 'broken' })]));
    expect(w.find('.fe-list__name').text()).toBe('shared');
    expect(w.find('.fe-list__name [data-testid="symlink-badge"]').exists()).toBe(false);
    expect(w.find('[data-testid="symlink-badge"]').exists()).toBe(true);
  });

  it('broken wears its own class, so it is not the same mark as the rest', () => {
    const w = mount(ListView, props([row({ symlink: true, link_state: 'broken' })]));
    expect(w.find('[data-testid="symlink-badge"]').classes()).toContain('fe-symlink--broken');
  });
});

describe('the details panel', () => {
  const api = {
    listShares: async () => [],
    listVersions: async () => [],
    listPermissions: async () => [],
    listComments: async () => [],
  };

  it('explains the row above the facts that mislead on their own', () => {
    const w = mount(InspectorPanel, {
      props: {
        api: api as never,
        nodes: [row({ symlink: true, link_state: 'outside_root' })] as never,
        dirLabel: 'e2e-local',
        dirCount: 1,
        locale: 'en',
      },
    });
    const note = w.find('[data-testid="inspector-symlink"]');
    // "Size: 0 bytes" and "Type: file" are both true here and both misleading;
    // the reason is not readable off any of them.
    expect(note.exists()).toBe(true);
    expect(note.attributes('data-link-state')).toBe('outside_root');
    expect(note.text()).toContain(en['symlink.inspector']);
    expect(note.text()).toContain('Follow symlinks that leave this folder');
  });

  it('says nothing about an ordinary file', () => {
    const w = mount(InspectorPanel, {
      props: {
        api: api as never,
        nodes: [row()] as never,
        dirLabel: 'e2e-local',
        dirCount: 1,
        locale: 'en',
      },
    });
    expect(w.find('[data-testid="inspector-symlink"]').exists()).toBe(false);
  });
});

describe('the rows that come from outside a folder listing', () => {
  // ⚠ `handlers/meta.go` ships `model.Node` as it is, so `type` there is the
  // raw NodeType — 'symlink' included, where the folder listing would have
  // collapsed it to 'file' and set the flag. Without the mapping, Recent,
  // Starred, a tag view and the Home tray would draw the row with no badge and
  // no explanation: the same fix reaching one screen and stopping at the next
  // (lesson #174).
  const ctx = { storages: [{ name: 'e2e-local' }], multiStorageRoot: false };

  it('turns a raw `symlink` node into a flagged file row', () => {
    const n = nodeRowToFileNode(
      { id: 9, path: 'shared', name: 'shared', type: 'symlink', size: 0, storage: 'e2e-local' },
      ctx,
    )!;
    expect(n.type).toBe('file');
    expect(n.symlink).toBe(true);
    expect(linkStateOf(n)).toBe('unknown');
  });

  it('leaves ordinary rows exactly as they were', () => {
    const f = nodeRowToFileNode(
      { id: 9, path: 'a.txt', name: 'a.txt', type: 'file', size: 4, storage: 'e2e-local' },
      ctx,
    )!;
    expect(f.symlink).toBeUndefined();
    const d = nodeRowToFileNode(
      { id: 10, path: 'docs', name: 'docs', type: 'dir', size: 0, storage: 'e2e-local' },
      ctx,
    )!;
    expect(d.type).toBe('dir');
    expect(d.symlink).toBeUndefined();
  });
});

describe('opening one', () => {
  // ⚠ A source scan, not a mount: `FileExplorer.vue` is far too large to mount
  // here — `sideNavMyShares.test.ts` says so for the same reason — and the
  // thing being pinned is an ORDER (the refusal comes before anything opens),
  // which is exactly what a scan can see and a partial mount cannot.
  const src = readFileSync(path.join(CORE_SRC, 'FileExplorer.vue'), 'utf8');
  const openNode = src.slice(src.indexOf('function openNode(n: FileNode)'));
  const body = openNode.slice(0, openNode.indexOf('\nconst VIEW_DEFAULT_EXTS'));

  it('is refused in the ONE funnel every way of opening a row goes through', () => {
    // ⚠ The GUARD, not merely the identifier. An earlier version of this
    // assertion asked only `toContain('linkWordsFor')`, and a mutation that
    // deleted the whole guard while leaving the name in a type annotation
    // (`ReturnType<typeof linkWordsFor>`) kept it GREEN — measured, 2026-09-21.
    // A test that survives the removal of the thing it guards protects nothing.
    expect(body).toMatch(/const\s+link\s*=\s*linkWordsFor\(\s*n\s*,\s*\{\s*t\s*\}\s*\)\s*;/);
    expect(body).toMatch(/if\s*\(\s*link\s*\)\s*\{/);
  });

  it('⚠⚠ refuses BEFORE it can navigate, preview or hand the row to the host', () => {
    const refusal = body.search(/const\s+link\s*=\s*linkWordsFor\(/);
    expect(refusal).toBeGreaterThan(-1);
    for (const later of ['loadTrash', "n.type === 'dir'", 'showPreview.value = true', "emit('file-opened'"]) {
      const at = body.indexOf(later);
      if (later === 'loadTrash') {
        // The virtual `.trash` row is not a file at all and is answered first.
        expect(at).toBeLessThan(refusal);
        continue;
      }
      expect(at, `openNode reaches ${later} before refusing a link`).toBeGreaterThan(refusal);
    }
  });

  it('⚠ says why, out loud — silence is the bug being fixed', () => {
    // Doing nothing is issue #34 itself. Letting the request go is barely
    // better: the driver's containment refusal surfaces as a generic failure
    // and reads as "filex is broken" rather than "this is a setting".
    expect(body).toMatch(/showToast\(\s*\{\s*message:\s*link\.why/);
    expect(body).not.toMatch(/if \(link\) \{\s*return/);
  });
});
