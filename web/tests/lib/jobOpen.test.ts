// lib/jobOpen - where a finished app job sends the person who queued it
// (filex #78). The signing app's "Convert to PDF" queued a conversion and the
// wizard then stopped: nothing read the job's end, so the PDF landed and the
// person had to find it and ask for signatures again. The server now puts the
// job result's `surface.open` on the ops row as `open`, and this is the one
// rule both frames that follow a job (PluginPageView, the explorer) read.
import { describe, expect, it } from 'vitest';

import { jobOpenOf } from '@brftech/filex-core/src/lib/jobOpen';
import { normalizeOp } from '@brftech/filex-core/src/composables/usePendingOps';

/** An ops row as GET /api/files/ops answers it for a finished app job. */
function row(extra: Record<string, unknown> = {}) {
  return normalizeOp({
    id: 9,
    kind: 'plugin-action',
    status: 'ok',
    plugin: 'sign',
    action: 'convert',
    outputs: [{ path: 'docs://contract.pdf' }],
    open: { path: 'docs://contract.pdf', view: 'request' },
    ...extra,
  });
}

describe('the ops row carries where a finished job sends its person', () => {
  it('normalizeOp keeps `open`, with only the fields the contract has', () => {
    const op = row({ open: { path: 'docs://contract.pdf', view: 'request', extra: 'x' } });
    expect(op.open).toEqual({ path: 'docs://contract.pdf', view: 'request' });
  });

  it('an open with no path is no request at all', () => {
    expect(row({ open: { path: '  ', view: 'request' } }).open).toBeUndefined();
    expect(row({ open: 'docs://contract.pdf' }).open).toBeUndefined();
  });

  it('a row without one reads exactly as before', () => {
    const op = normalizeOp({ id: 3, kind: 'copy', status: 'ok' });
    expect('open' in op).toBe(false);
  });
});

describe('jobOpenOf', () => {
  it('a finished app job with an open: go there, on that app', () => {
    expect(jobOpenOf(row())).toEqual({ plugin: 'sign', open: { path: 'docs://contract.pdf', view: 'request' } });
  });

  it('not while it runs, and not after it failed or was cancelled', () => {
    expect(jobOpenOf(row({ status: 'running' }))).toBeNull();
    expect(jobOpenOf(row({ status: 'failed' }))).toBeNull();
    expect(jobOpenOf(row({ status: 'cancelled' }))).toBeNull();
  });

  it('not over a screen the person opened since', () => {
    expect(jobOpenOf(row(), { busy: true })).toBeNull();
  });

  it('nothing for a row whose app asked for nothing, or a row that is not an app job', () => {
    expect(jobOpenOf(row({ open: undefined }))).toBeNull();
    expect(jobOpenOf({ ...row(), op_type: 'copy' })).toBeNull();
    expect(jobOpenOf(row({ plugin: '' }))).toBeNull();
  });
});
