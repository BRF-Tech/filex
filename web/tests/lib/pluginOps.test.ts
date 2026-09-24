// An app-plugin job in the ops tray: `plugin-action` on the wire becomes the
// `plugin` kind with its label, message and outputs — and a kind this
// package has never heard of is carried through, not read as a delete.
import { describe, expect, it } from 'vitest';

import { normalizeOp } from '@brftech/filex-core/src/composables/usePendingOps';

describe('normalizeOp — plugin-action rows', () => {
  const raw = {
    id: 42,
    kind: 'plugin-action',
    status: 'running',
    total: 1,
    done: 0,
    plugin: 'sign',
    action: 'sign',
    label: 'Sign…',
    message: 'Stamping page 2 of 5',
    outputs: [{ path: 'docs://reports/nda-signed.pdf' }, { path: '' }, 'junk'],
  };

  it('maps the wire kind to `plugin` and keeps the plugin fields', () => {
    const op = normalizeOp(raw);
    expect(op.op_type).toBe('plugin');
    expect(op.status).toBe('running');
    expect(op.plugin).toBe('sign');
    expect(op.action).toBe('sign');
    expect(op.label).toBe('Sign…');
    expect(op.message).toBe('Stamping page 2 of 5');
    expect(op.progress_total).toBe(1);
    expect(op.progress_done).toBe(0);
  });

  it('keeps only the outputs that name a path', () => {
    expect(normalizeOp(raw).outputs).toEqual([{ path: 'docs://reports/nda-signed.pdf' }]);
  });

  it('accepts the already-normalized spelling too (idempotent)', () => {
    const once = normalizeOp(raw);
    const twice = normalizeOp(once as unknown as Record<string, unknown>);
    expect(twice.op_type).toBe('plugin');
    expect(twice.outputs).toEqual(once.outputs);
  });

  it('a row with no outputs has none, not an empty list pretending to be one', () => {
    expect(normalizeOp({ id: 1, kind: 'plugin-action', status: 'queued' }).outputs).toBeUndefined();
  });
});

describe('normalizeOp — unknown kinds', () => {
  it('copy and move stay themselves; a kind it does not know passes through', () => {
    expect(normalizeOp({ id: 1, kind: 'copy' }).op_type).toBe('copy');
    expect(normalizeOp({ id: 1, kind: 'move' }).op_type).toBe('move');
    expect(normalizeOp({ id: 1, kind: 'thumbnail-rebuild' }).op_type).toBe('thumbnail-rebuild');
  });
  it('only a row with no kind at all is read as delete (the legacy delete endpoint)', () => {
    expect(normalizeOp({ id: 1, status: 'ok' }).op_type).toBe('delete');
    expect(normalizeOp({ id: 1, kind: 'delete' }).op_type).toBe('delete');
  });
});
