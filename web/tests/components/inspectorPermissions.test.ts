import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';
import { en } from '@brftech/filex-core/src/locales/en';

const node = { id: 53280, path: 'Files://Supply/Certificates', basename: 'Certificates', type: 'dir' as const, perm: 'owner' };
function panel() {
  const api = {
    listPermissions: vi.fn(async () => ({ storage_rbac: true, direct: [
      { id: 1, kind: 'group', group_id: 8, group_name: 'Design', level: 'editor' },
      { id: 2, kind: 'group', group_id: 9, level: 'viewer' },
      { id: 1, user_id: 3, user_name: 'Ada', user_email: 'ada@test', level: 'viewer' },
    ], inherited: [
      { id: 3, kind: 'group', group_id: 10, group_name: 'Support', level: 'editor', inherited: true },
      { id: 4, user_id: 1, user_name: 'lex', level: 'owner', inherited: true },
    ] })),
    listShares: vi.fn(async () => ({ shares: [] })),
    listVersions: vi.fn(async () => []),
    listComments: vi.fn(async () => []),
  };
  const w = mount(InspectorPanel, { attachTo: document.body, props: { api: api as never, nodes: [node], dirLabel: 'Another folder', dirCount: 1, dirPerm: 'owner', heldIn: 'Supply', locale: 'en' } });
  return { w, api };
}
describe('inspector permission recipients', () => {
  it('names groups and users in their own identity spaces, including unnamed groups', async () => {
    const { w, api } = panel(); await flushPromises();
    expect(api.listPermissions).toHaveBeenCalledWith(node.path);
    const rows = w.findAll('.fe-inspector__person');
    expect(rows).toHaveLength(5);
    expect(rows.map(r => r.get('.fe-inspector__person-name').text())).toEqual(['Design', '#9', 'Ada', 'Support', 'lex']);
    expect(rows.map(r => r.get('.fe-inspector__avatar').text())).toEqual(['D', '#', 'A', 'S', 'L']);
    expect(rows[0].get('.fe-inspector__person-sub').text()).toContain(en['access.ui.group']);
    expect(rows[0].get('.fe-inspector__person-sub').text()).not.toContain(en['inspector.people.inherited']);
    expect(rows[3].get('.fe-inspector__person-sub').text()).toContain(en['inspector.people.inherited']);
    expect(rows[4].get('.fe-inspector__person-sub').text()).toContain(en['inspector.people.inherited']);
    expect(w.text()).not.toContain('#undefined');
    w.unmount();
  });
});
