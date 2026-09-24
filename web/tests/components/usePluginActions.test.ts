// usePluginActions — one fetch per endpoint shared by every explorer on the
// page, nothing at all while the feature is off, and the run body the
// contract names (adapter-qualified paths).
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick, ref } from 'vue';

import {
  usePluginActions,
  invalidatePluginActions,
} from '@brftech/filex-core/src/composables/usePluginActions';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const listing = {
  actions: [
    { plugin: 'sign', id: 'sign', key: 'plugin:sign/sign', label: { en: 'Sign…' }, applies: { kind: 'file', ext: ['pdf'] } },
  ],
  views: [],
};

function fakeApi(url = 'https://x/api/files/plugins/actions') {
  const pluginActions = vi.fn(async () => listing);
  const pluginActionRun = vi.fn(async () => ({ op: { id: 7, kind: 'plugin-action', status: 'queued' } }));
  const api = { endpoints: { pluginActions: url }, pluginActions, pluginActionRun } as unknown as FileApi;
  return { api, pluginActions, pluginActionRun };
}

const pdf = { type: 'file', path: 'docs://nda.pdf', basename: 'nda.pdf', extension: 'pdf' } as FileNode;

describe('usePluginActions', () => {
  beforeEach(() => invalidatePluginActions());

  it('makes no request while disabled, and asks once enabled', async () => {
    const { api, pluginActions } = fakeApi();
    const on = ref(false);
    const store = usePluginActions(api, () => on.value);
    await nextTick();
    expect(pluginActions).not.toHaveBeenCalled();
    expect(store.actionsFor([pdf])).toEqual([]);

    on.value = true;
    await nextTick();
    await store.refresh();
    expect(pluginActions).toHaveBeenCalledTimes(1);
    expect(store.actionsFor([pdf]).map((a) => a.id)).toEqual(['sign']);
    expect(store.byKey('plugin:sign/sign')?.plugin).toBe('sign');
  });

  it('shares one answer between instances on the same endpoint', async () => {
    const { api, pluginActions } = fakeApi();
    const a = usePluginActions(api, () => true);
    const b = usePluginActions(api, () => true);
    await a.refresh();
    await b.refresh();
    expect(pluginActions).toHaveBeenCalledTimes(1);
    expect(b.actions.value).toHaveLength(1);
  });

  it('asks again after the cache is invalidated', async () => {
    const { api, pluginActions } = fakeApi();
    const store = usePluginActions(api, () => true);
    await store.refresh();
    invalidatePluginActions();
    const fresh = usePluginActions(api, () => true);
    await fresh.refresh();
    expect(pluginActions).toHaveBeenCalledTimes(2);
  });

  it('runs with the adapter-qualified paths of the targets', async () => {
    const { api, pluginActionRun } = fakeApi();
    const store = usePluginActions(api, () => true);
    await store.refresh();
    const action = store.byKey('plugin:sign/sign')!;
    const res = await store.run(action, [pdf], { quality: 85 });
    expect(pluginActionRun).toHaveBeenCalledWith('sign', 'sign', { paths: ['docs://nda.pdf'], params: { quality: 85 } });
    expect(res.op).toBeTruthy();
  });

  it('keeps a failed fetch quiet and empty', async () => {
    const { api, pluginActions } = fakeApi();
    pluginActions.mockRejectedValueOnce(new Error('boom'));
    const store = usePluginActions(api, () => true);
    await store.refresh();
    expect(store.actions.value).toEqual([]);
    expect(store.error.value).toBe('boom');
  });
});
