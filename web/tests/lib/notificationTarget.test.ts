// The one notification-click resolver.
//
// These are the rules three surfaces share — the bell row, the browser
// notification and the desktop app's native one. If this file and
// desktop/test/notifications.test.ts ever disagree, two of those three are
// landing somewhere the third does not.

import { describe, it, expect } from 'vitest';
import {
  explorerHashPath,
  isNotificationClickable,
  notificationHref,
  notificationRoute,
  resolveNotificationTarget,
  sameRowPath,
  shareHref,
  type NotificationTarget,
} from '@/lib/notificationTarget';

describe('resolveNotificationTarget', () => {
  it('sends a file to its FOLDER, with the file as the row to select', () => {
    const dest = resolveNotificationTarget({
      kind: 'file',
      storage: 'qldemo',
      path: 'Documents/report.pdf',
    });
    expect(dest).toEqual({
      kind: 'folder',
      storage: 'qldemo',
      folder: 'Documents',
      // ⚠ Exactly the string the explorer puts in `data-fe-path`. Anything
      // else and the row is never found.
      select: 'qldemo://Documents/report.pdf',
    });
  });

  it('a file at a storage root opens that root', () => {
    expect(resolveNotificationTarget({ kind: 'file', storage: 'qldemo', path: 'notes.md' })).toEqual({
      kind: 'folder',
      storage: 'qldemo',
      folder: '',
      select: 'qldemo://notes.md',
    });
  });

  it('a folder opens itself, with nothing selected', () => {
    expect(resolveNotificationTarget({ kind: 'dir', storage: 'qldemo', path: 'Inbox' })).toEqual({
      kind: 'folder',
      storage: 'qldemo',
      folder: 'Inbox',
    });
  });

  it('a share is a token, and only a token', () => {
    expect(resolveNotificationTarget({ kind: 'share', id: 'tok123' })).toEqual({
      kind: 'share',
      token: 'tok123',
    });
    expect(shareHref('tok 123')).toBe('/s/tok%20123');
  });

  it('refuses half an address rather than guessing a storage', () => {
    // ⚠ This is the case that matters. A file target with no storage would
    // otherwise be resolved against whatever storage the user happened to have
    // open — a click that lands somewhere plausible and wrong.
    expect(resolveNotificationTarget({ kind: 'file', path: 'a/b.txt' })).toEqual({ kind: 'none' });
    expect(resolveNotificationTarget({ kind: 'share', id: '  ' })).toEqual({ kind: 'none' });
  });

  it('treats absent / none / unknown as "nothing to open"', () => {
    expect(resolveNotificationTarget(undefined)).toEqual({ kind: 'none' });
    expect(resolveNotificationTarget(null)).toEqual({ kind: 'none' });
    expect(resolveNotificationTarget({ kind: 'none' })).toEqual({ kind: 'none' });
    // A kind from a newer server this build does not know.
    expect(resolveNotificationTarget({ kind: 'future' } as unknown as NotificationTarget)).toEqual({
      kind: 'none',
    });
  });

  it('normalises a path the emitter over-qualified or over-slashed', () => {
    expect(
      resolveNotificationTarget({ kind: 'file', storage: 'qldemo', path: '/Documents/report.pdf' }),
    ).toMatchObject({ folder: 'Documents', select: 'qldemo://Documents/report.pdf' });
    expect(
      resolveNotificationTarget({ kind: 'dir', storage: 'qldemo', path: 'qldemo://Inbox/' }),
    ).toMatchObject({ folder: 'Inbox' });
  });
});

describe('the address a destination becomes', () => {
  it('uses the explorer hash form, which is NOT the row form', () => {
    // Measured against a running instance: the address bar carries
    // `#qldemo/Documents` while the row carries `qldemo://Documents`.
    const dest = resolveNotificationTarget({ kind: 'dir', storage: 'qldemo', path: 'Documents' });
    expect(explorerHashPath(dest)).toBe('qldemo/Documents');
    const root = resolveNotificationTarget({ kind: 'dir', storage: 'qldemo', path: '' });
    expect(explorerHashPath(root)).toBe('qldemo');
  });

  it('routes a file to /explore with the row in ?select=', () => {
    const dest = resolveNotificationTarget({
      kind: 'file',
      storage: 'qldemo',
      path: 'Documents/report.pdf',
    });
    expect(notificationRoute(dest)).toEqual({
      name: 'explore',
      query: { select: 'qldemo://Documents/report.pdf' },
      hash: '#qldemo/Documents',
    });
  });

  it('has no route at all for "nothing to open"', () => {
    // ⚠⚠ It used to answer the notifications PAGE. That page is the one the
    // reader was most likely already on, and it is admin-gated — so for
    // everybody else the same click was a guard bounce that threw away the
    // folder they were standing in. A row with nothing to open is now drawn
    // as plain text and goes nowhere (`isNotificationClickable`).
    expect(notificationRoute({ kind: 'none' })).toBeNull();
  });

  it('says which rows are worth clicking', () => {
    expect(isNotificationClickable(null)).toBe(false);
    expect(isNotificationClickable({ kind: 'none' })).toBe(false);
    // Half an address is not an address: no storage, nowhere to go.
    expect(isNotificationClickable({ kind: 'file', path: 'a/b.txt' })).toBe(false);
    expect(isNotificationClickable({ kind: 'file', storage: 'qldemo', path: 'a/b.txt' })).toBe(true);
    expect(isNotificationClickable({ kind: 'share', id: 'tok' })).toBe(true);
  });

  it("carries an app's own screen through to the route (v2)", () => {
    const dest = resolveNotificationTarget({
      kind: 'file',
      storage: 'qldemo',
      path: 'Documents/nda.pdf',
      open: { plugin: 'sign', action: 'fill' },
    });
    expect(notificationRoute(dest)).toEqual({
      name: 'explore',
      query: { select: 'qldemo://Documents/nda.pdf', app: 'sign', appAction: 'fill' },
      hash: '#qldemo/Documents',
    });
  });

  it('drops an `open` that names an app but nothing to open', () => {
    const dest = resolveNotificationTarget({
      kind: 'file',
      storage: 'qldemo',
      path: 'a.pdf',
      open: { plugin: 'sign' },
    });
    expect(notificationRoute(dest)).toEqual({
      name: 'explore',
      query: { select: 'qldemo://a.pdf' },
      hash: '#qldemo',
    });
  });

  it('turns a destination into an address a service worker can open', () => {
    const dest = resolveNotificationTarget({
      kind: 'file',
      storage: 'qldemo',
      path: 'Documents/nda.pdf',
      open: { plugin: 'sign', view: 'wizard' },
    });
    expect(notificationHref(dest, '/admin/')).toBe(
      '/admin/explore?select=qldemo%3A%2F%2FDocuments%2Fnda.pdf&app=sign&appView=wizard#qldemo/Documents',
    );
    expect(notificationHref({ kind: 'share', token: 'tok' })).toBe('/s/tok');
    // ⚠ '' — never '/': "nowhere" must not become "the home page".
    expect(notificationHref({ kind: 'none' }, '/drive/')).toBe('');
  });

  it('has no SPA route for a share — it is a public page', () => {
    expect(notificationRoute({ kind: 'share', token: 'x' })).toBeNull();
  });
});

// ⚠⚠ A soft delete used to target the item's KEY inside the bin — `{kind:
// 'file', path: '.filex-trash/1789-6c7d18__report.txt'}` — and a click opened
// the bin's raw folder: breadcrumb `docs › .filex-trash`, nothing listed,
// nothing to restore (owner's report, 2026-09-21, measured in a browser). The
// server now addresses the Trash VIEW by the path the item came from.
describe('a trashed file opens the Trash view, never the bin', () => {
  it('selects the item there by its ORIGINAL path', () => {
    const dest = resolveNotificationTarget({ kind: 'trash', storage: 'docs', path: 'Documents/report.txt' });
    expect(dest).toEqual({ kind: 'trash', select: 'docs://Documents/report.txt' });
    expect(isNotificationClickable({ kind: 'trash', storage: 'docs', path: 'Documents/report.txt' })).toBe(true);
    expect(explorerHashPath(dest)).toBe('.trash');
    expect(notificationRoute(dest)).toEqual({
      name: 'explore',
      query: { select: 'docs://Documents/report.txt' },
      hash: '#.trash',
    });
    expect(notificationHref(dest, '/admin/')).toBe(
      '/admin/explore?select=docs%3A%2F%2FDocuments%2Freport.txt#.trash',
    );
  });

  it('still opens the Trash view when the event could not say which item', () => {
    // The view spans every storage, so it is a destination on its own.
    const dest = resolveNotificationTarget({ kind: 'trash' });
    expect(dest).toEqual({ kind: 'trash' });
    expect(notificationRoute(dest)).toEqual({ name: 'explore', query: {}, hash: '#.trash' });
  });

  it('no destination anywhere contains a filex-internal directory', () => {
    for (const t of [
      { kind: 'trash', storage: 'docs', path: 'Documents/report.txt' },
      { kind: 'trash', storage: 'docs', path: '/Documents/report.txt' },
    ] as NotificationTarget[]) {
      const href = notificationHref(resolveNotificationTarget(t), '/admin/');
      expect(decodeURIComponent(href)).not.toMatch(/\.filex-|\.versions|\.thumbs/);
    }
  });
});

describe('sameRowPath', () => {
  it("matches the Trash view's slash-prefixed row path to the target's", () => {
    // The Trash view builds `<storage>://<original path>` and the original path
    // is stored with its leading slash; an exact compare selected nothing.
    expect(sameRowPath('docs:///Documents/report.txt', 'docs://Documents/report.txt')).toBe(true);
    expect(sameRowPath('docs://Documents/report.txt', 'docs://Documents/report.txt')).toBe(true);
    expect(sameRowPath('docs://Documents/report.txt', 'docs://Documents/other.txt')).toBe(false);
    expect(sameRowPath('docs://a.txt', 'other://a.txt')).toBe(false);
  });
});

// ⚠ An app's HOME page, at a section (2026-09-21: a notice about the
// requests you sent lands on the Signatures page's "I asked for these").
describe('an app page target', () => {
  const target: NotificationTarget = {
    kind: 'app',
    open: { plugin: 'sign', view: 'envelopes', section: 'requested' },
  };

  it('resolves to the page and its section', () => {
    expect(resolveNotificationTarget(target)).toEqual({
      kind: 'app',
      plugin: 'sign',
      view: 'envelopes',
      section: 'requested',
    });
    expect(isNotificationClickable(target)).toBe(true);
  });

  it('is the SPA’s own page, in the same tab, with the section in its address', () => {
    expect(notificationRoute(resolveNotificationTarget(target))).toEqual({
      name: 'app-home',
      params: { plugin: 'sign', view: 'envelopes' },
      query: { section: 'requested' },
      hash: '',
    });
    expect(notificationHref(resolveNotificationTarget(target), '/drive/')).toBe('/drive/app/sign/envelopes?section=requested');
  });

  it('half an address is no address', () => {
    expect(resolveNotificationTarget({ kind: 'app', open: { plugin: 'sign' } })).toEqual({ kind: 'none' });
    expect(resolveNotificationTarget({ kind: 'app' })).toEqual({ kind: 'none' });
  });
});
