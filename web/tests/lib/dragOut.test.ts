// Dragging rows OUT of filex, and the trap that comes with it.
//
// The native OS drag (desktop app) REPLACES the HTML5 drag: `startDrag` needs
// `preventDefault()` on dragstart, so from that moment the app's own drop
// targets no longer see `application/x-brf-files` — they see an OS file drag,
// exactly like a file dragged in from Explorer. Handled naively, dragging a row
// onto a folder INSIDE filex would upload the temp copy back to the server: the
// same bytes round-tripped, a copy where the user asked for a move, and the
// original left behind.
//
// So the payload is remembered on this side and every drop target asks these
// helpers instead of reading the dataTransfer. These tests are that contract.

import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  activeNativeDrag,
  beginNativeDrag,
  canDownloadUrlDrag,
  downloadUrlPayload,
  dragKey,
  endNativeDrag,
  hasInternalDrag,
  internalDragItems,
  internalDragOrigin,
  FE_DND_MIME,
  FE_DND_SRC_MIME,
  type DragItem,
} from '../../../packages/core/src/lib/dragOut';

/** A drag event carrying nothing but OS files — what a native drag looks like. */
function osFileDrag(): DragEvent {
  return {
    dataTransfer: {
      types: ['Files'],
      getData: () => '',
    },
  } as unknown as DragEvent;
}

/** A drag event carrying filex's own HTML5 payload. */
function htmlDrag(items: DragItem[], origin = 'alpha://'): DragEvent {
  return {
    dataTransfer: {
      types: [FE_DND_MIME, FE_DND_SRC_MIME, 'text/plain'],
      getData: (t: string) =>
        t === FE_DND_MIME ? JSON.stringify(items) : t === FE_DND_SRC_MIME ? origin : '',
    },
  } as unknown as DragEvent;
}

const rows: DragItem[] = [
  { path: 'alpha://belge.pdf', basename: 'belge.pdf', type: 'file' },
  { path: 'alpha://klasor', basename: 'klasor', type: 'dir' },
];

describe('native drag bookkeeping', () => {
  beforeEach(() => {
    endNativeDrag();
    vi.useRealTimers();
  });

  it('an OS drag we started is still an INTERNAL drag when it lands back inside', () => {
    const ev = osFileDrag();
    expect(hasInternalDrag(ev)).toBe(false); // before: a genuine OS drop (upload)

    beginNativeDrag(rows, 'alpha://');
    expect(hasInternalDrag(ev)).toBe(true);
    expect(internalDragItems(ev)).toEqual(rows);
    expect(internalDragOrigin(ev)).toBe('alpha://');
  });

  it('forgets the drag once it is over, so the next one cannot inherit it', () => {
    beginNativeDrag(rows, 'alpha://');
    endNativeDrag();
    expect(activeNativeDrag()).toBeNull();
    expect(internalDragItems(osFileDrag())).toBeNull();
  });

  it('expires on its own — a record no drop ever cleared cannot haunt the session', () => {
    vi.useFakeTimers();
    beginNativeDrag(rows, 'alpha://');
    vi.advanceTimersByTime(6 * 60_000);
    expect(activeNativeDrag()).toBeNull();
    vi.useRealTimers();
  });

  it('prefers the real HTML5 payload when there is one', () => {
    beginNativeDrag([{ path: 'beta://eski.txt', basename: 'eski.txt', type: 'file' }], 'beta://');
    const ev = htmlDrag(rows, 'alpha://sub');
    expect(internalDragItems(ev)).toEqual(rows);
    expect(internalDragOrigin(ev)).toBe('alpha://sub');
  });

  it('treats a broken payload as no payload rather than throwing at the drop', () => {
    const ev = {
      dataTransfer: { types: [FE_DND_MIME], getData: () => '{not json' },
    } as unknown as DragEvent;
    expect(internalDragItems(ev)).toBeNull();
  });
});

describe('DownloadURL drag (browser path)', () => {
  it('builds mime:name:absolute-url for a file', () => {
    const payload = downloadUrlPayload(rows[0]!, 'https://fm.example.com/api/files/manager?action=download&path=x', 'application/pdf');
    expect(payload).toBe(
      'application/pdf:belge.pdf:https://fm.example.com/api/files/manager?action=download&path=x',
    );
  });

  it('falls back to a generic mime rather than emitting an empty field', () => {
    const payload = downloadUrlPayload(rows[0]!, 'https://fm.example.com/x');
    expect(payload).toBe('application/octet-stream:belge.pdf:https://fm.example.com/x');
  });

  it('refuses a folder — Chromium can only download one FILE this way', () => {
    expect(downloadUrlPayload(rows[1]!, 'https://fm.example.com/x')).toBeNull();
  });

  it('refuses a URL that is not http(s): a drop that quietly does nothing is worse than none', () => {
    expect(downloadUrlPayload(rows[0]!, 'blob:app://filex/abc')).toBeNull();
  });

  it('absolutises a relative URL against the page', () => {
    const payload = downloadUrlPayload(rows[0]!, '/api/files/manager?action=download&path=x');
    expect(payload).toContain(`:${location.origin}/api/files/manager`);
  });

  it('is only offered where the credential travels on its own', () => {
    expect(canDownloadUrlDrag(undefined)).toBe(true); // cookie session
    expect(canDownloadUrlDrag({ kind: 'none' })).toBe(true);
    expect(canDownloadUrlDrag({ kind: 'csrf', csrf: 'x' })).toBe(true);
    // A bearer token lives in the page; the browser's download stack would not
    // send it, and the "file" on the desktop would be a 401 page.
    expect(canDownloadUrlDrag({ kind: 'bearer', token: 't' })).toBe(false);
    expect(canDownloadUrlDrag({ type: 'bearer', token: 't' })).toBe(false); // 0.1.0 shape
  });
});

describe('dragKey', () => {
  it('is order-independent, so a re-selected set is still the prepared set', () => {
    expect(dragKey([{ path: 'a' }, { path: 'b' }])).toBe(dragKey([{ path: 'b' }, { path: 'a' }]));
  });
  it('changes when the selection changes', () => {
    expect(dragKey([{ path: 'a' }])).not.toBe(dragKey([{ path: 'a' }, { path: 'b' }]));
  });
});
