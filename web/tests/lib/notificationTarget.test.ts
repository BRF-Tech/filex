// The one notification-click resolver.
//
// These are the rules three surfaces share — the bell row, the browser
// notification and the desktop app's native one. If this file and
// desktop/test/notifications.test.ts ever disagree, two of those three are
// landing somewhere the third does not.

import { describe, it, expect } from 'vitest';
import {
  explorerHashPath,
  notificationRoute,
  resolveNotificationTarget,
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

  it('routes "nothing to open" to the notifications page', () => {
    expect(notificationRoute({ kind: 'none' })).toEqual({
      name: 'notifications',
      query: {},
      hash: '',
    });
  });

  it('has no SPA route for a share — it is a public page', () => {
    expect(notificationRoute({ kind: 'share', token: 'x' })).toBeNull();
  });
});
