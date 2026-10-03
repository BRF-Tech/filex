// The navigation panel's "Drafts" row (issue #71): beside Recent, Starred and
// Trash, with a COUNT — never a notification. It is drawn only where the
// server keeps drafts for the caller, and dropped with the other identity
// views for an app token.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import SideNav from '@brftech/filex-core/src/components/SideNav.vue';

function nav(extra: Record<string, unknown> = {}) {
  return mount(SideNav, {
    attachTo: document.body,
    // ⚠ Both said out loud: an absent Boolean prop is `false` in Vue, so a
    // panel mounted without them draws neither the identity views nor Trash.
    props: {
      expanded: true,
      activeView: '',
      storages: [{ name: 'docs' }, { name: 'arsiv' }],
      locale: 'en',
      showIdentitySurfaces: true,
      trashVisible: true,
      ...extra,
    },
  });
}

const rowOrder = (w: ReturnType<typeof nav>) =>
  w.findAll('.fe-sidenav__group [data-testid^="sidenav-view-"]').map((b) => b.attributes('data-testid'));

describe('SideNav — Drafts', () => {
  it('sits between the views and Trash, only where drafts are kept', () => {
    expect(rowOrder(nav())).not.toContain('sidenav-view-drafts');
    const w = nav({ draftsVisible: true });
    const order = rowOrder(w);
    expect(order).toContain('sidenav-view-drafts');
    expect(order.indexOf('sidenav-view-drafts')).toBe(order.indexOf('sidenav-view-trash') - 1);
    expect(order.indexOf('sidenav-view-drafts')).toBeGreaterThan(order.indexOf('sidenav-view-starred'));
  });

  it('carries the count as a badge, and none at zero', async () => {
    const w = nav({ draftsVisible: true, draftCount: 3 });
    const badge = w.get('[data-testid="sidenav-drafts-count"]');
    expect(badge.text()).toBe('3');
    // The number is read out with the row, not as a stray "3".
    expect(badge.attributes('aria-hidden')).toBe('true');
    expect(w.get('[data-testid="sidenav-view-drafts"]').attributes('aria-label')).toBe('Drafts, 3 drafts');
    await w.setProps({ draftCount: 1 });
    expect(w.get('[data-testid="sidenav-view-drafts"]').attributes('aria-label')).toBe('Drafts, 1 draft');
    await w.setProps({ draftCount: 0 });
    expect(w.find('[data-testid="sidenav-drafts-count"]').exists()).toBe(false);
    expect(w.get('[data-testid="sidenav-view-drafts"]').attributes('aria-label')).toBe('Drafts');
  });

  it('keeps the count on the collapsed rail, on the icon’s corner', () => {
    const w = nav({ draftsVisible: true, draftCount: 2, expanded: false });
    const badge = w.get('[data-testid="sidenav-drafts-count"]');
    expect(badge.classes()).toContain('fe-sidenav__count--rail');
  });

  it('is an identity view: an app token has no drafts to show', () => {
    const w = nav({ draftsVisible: true, draftCount: 2, showIdentitySurfaces: false });
    expect(rowOrder(w)).not.toContain('sidenav-view-drafts');
  });

  it('opens the view, and reads as selected there', async () => {
    const w = nav({ draftsVisible: true });
    await w.get('[data-testid="sidenav-view-drafts"]').trigger('click');
    expect(w.emitted('open-view')?.[0]).toEqual(['drafts']);
    await w.setProps({ activeView: 'drafts' });
    expect(w.get('[data-testid="sidenav-view-drafts"]').attributes('aria-current')).toBe('page');
  });

  it('in Turkish: Taslaklar', () => {
    const w = nav({ draftsVisible: true, draftCount: 4, locale: 'tr' });
    expect(w.get('[data-testid="sidenav-view-drafts"]').text()).toContain('Taslaklar');
    expect(w.get('[data-testid="sidenav-view-drafts"]').attributes('aria-label')).toBe('Taslaklar, 4 taslak');
  });
});
