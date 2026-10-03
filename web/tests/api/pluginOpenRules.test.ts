// The administrator's open rules reach the explorer (Default apps, 0.50).
//
// GET /api/files/plugins/actions answers `open_rules` by kind, and the
// explorer's "Open with" and the handler it picks read them (lib/appViewer).
// useFileApi.pluginActions rebuilt the answer from `actions` and `views` only,
// so the rules were dropped on the way in: a handler the administrator
// switched off was still offered and still opened files (e2e 185, red in every
// engine in the 0.50 integration run).
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { openHandlersFor } from '@brftech/filex-core/src/lib/appViewer';

afterEach(() => vi.unstubAllGlobals());

const VIEW = {
  plugin: 'board',
  id: 'view',
  placement: 'viewer',
  label: { en: 'Board' },
  applies: { kind: 'file', ext: ['board'] },
  ui: { url: '/_appui/board/x/index.html', grants: [], engine: false, version: '1.0.0' },
};

function answer(body: unknown) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })),
  );
}

describe('the open rules of GET /api/files/plugins/actions', () => {
  it('are passed on with the actions and the views', async () => {
    answer({ actions: [], views: [VIEW], open_rules: { board: { order: [], off: ['builtin'] } } });
    const api = useFileApi({ apiBase: 'https://files.example.com', auth: { kind: 'bearer', token: 't' } });
    const res = await api.pluginActions();
    expect(res.views).toHaveLength(1);
    expect(res.open_rules).toEqual({ board: { order: [], off: ['builtin'] } });
  });

  it('switch the built-in viewer off for the kind, as the administrator said', async () => {
    answer({ actions: [], views: [VIEW], open_rules: { board: { order: [], off: ['builtin'] } } });
    const api = useFileApi({ apiBase: 'https://files.example.com', auth: { kind: 'bearer', token: 't' } });
    const res = await api.pluginActions();
    const file = { type: 'file', basename: 'plan.board', extension: 'board' };
    const { on, off } = openHandlersFor(res.views, file, res.open_rules);
    expect(on.map((h) => h.id)).toEqual(['app:board/view']);
    expect(off.map((h) => h.id)).toEqual(['builtin']);
  });

  it('an older server that sends no rules leaves every handler on', async () => {
    answer({ actions: [], views: [VIEW] });
    const api = useFileApi({ apiBase: 'https://files.example.com', auth: { kind: 'bearer', token: 't' } });
    const res = await api.pluginActions();
    expect(res.open_rules).toBeUndefined();
    const { on } = openHandlersFor(res.views, { type: 'file', basename: 'plan.board', extension: 'board' }, res.open_rules);
    expect(on.map((h) => h.id)).toEqual(['app:board/view', 'builtin']);
  });
});
