// The details panel's Node ID row is an ADMINISTRATOR's.
//
// ⚠ Burak, 2026-09-23: the id is a support handle — what an administrator
// quotes into Admin → File history or reads off an audit row — and it says
// nothing to anybody else, so a shared surface was putting a number in front
// of every reader. It is gated in the SHARED panel (core InspectorPanel), so
// the explorer, the Drive shell and every embed answer the same way; the
// explorer passes `capabilities.caller_admin`, and a host that asks the
// server nothing gets the safe answer (no row).
import { describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';

import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';
import { en } from '@brftech/filex-core/src/locales/en';

const api = {
  listShares: vi.fn(async () => {
    throw new Error('no shares here');
  }),
  listVersions: vi.fn(async () => {
    throw new Error('no versions here');
  }),
  listPermissions: vi.fn(async () => {
    throw new Error('no grants here');
  }),
  listComments: vi.fn(async () => {
    throw new Error('no comments here');
  }),
  createShare: vi.fn(),
  restoreVersion: vi.fn(),
  snapshotVersion: vi.fn(),
  addComment: vi.fn(),
  deleteComment: vi.fn(),
};

const NODE = {
  id: 6,
  path: 'demo://app.ts',
  basename: 'app.ts',
  type: 'file' as const,
  extension: 'ts',
  size: 20,
  last_modified: 1_757_000_000_000,
};

function panel(extra: Record<string, unknown> = {}) {
  return mount(InspectorPanel, {
    props: {
      api: api as never,
      nodes: [NODE],
      dirLabel: 'demo',
      dirCount: 3,
      locale: 'en' as const,
      ...extra,
    },
    attachTo: document.body,
  });
}

/** The row, by the label the panel prints for it. */
function nodeIdRow(w: ReturnType<typeof panel>) {
  return w.findAll('.fe-inspector__row').find((r) => r.text().startsWith(en['inspector.nodeId']));
}

describe('the details panel’s Node ID row', () => {
  it('is not drawn for a reader who is not an administrator', () => {
    const w = panel();
    expect(nodeIdRow(w)).toBeUndefined();
    expect(w.text()).not.toContain(en['inspector.nodeId']);
    w.unmount();
  });

  it('is not drawn when the host says caller_admin is false', () => {
    const w = panel({ callerAdmin: false });
    expect(nodeIdRow(w)).toBeUndefined();
    w.unmount();
  });

  it('is drawn, with the id, for an administrator', () => {
    const w = panel({ callerAdmin: true });
    const row = nodeIdRow(w);
    expect(row).toBeTruthy();
    expect(row!.text()).toContain('6');
    w.unmount();
  });

  it('is not drawn for a row with no backend id, administrator or not', () => {
    const w = panel({ callerAdmin: true, nodes: [{ ...NODE, id: undefined }] });
    expect(nodeIdRow(w)).toBeUndefined();
    w.unmount();
  });
});
