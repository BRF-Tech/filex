// etiket:k2 (v0.43.0) — personal and team tags, as the screen shows them.
//
// ⚠⚠ The finding (tester, 2026-09-22): a non-admin's tag "müşteri teklifi"
// appeared in another user's and the admin's panel and on the file, and the
// other user could remove it. Tags were one label, shared with every account,
// while the code called them per-user — and the screen never said a word
// about who could see a tag. The server half is in handlers/tags.go; this file
// is the half a person SEES:
//
//   · every chip says which kind it is (glyph + words), personal first;
//   · adding asks who sees it, personal by default, "team" only where saving
//     it would succeed — and a viewer is told why it is not offered;
//   · a viewer gets no × on a team chip;
//   · the kind is chosen BEFORE the blur-to-save can fire (moving from the
//     field to the choice is not leaving the form);
//   · an older server (no `items`) gets the old request shape and no choice;
//   · the navigation panel lists both kinds, grouped, and opens the one that
//     was clicked;
//   · names keep their capitals, and "same tag" folds the Turkish i's.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount } from '@vue/test-utils';

import TagPicker from '@brftech/filex-core/src/components/TagPicker.vue';
import SideNav from '@brftech/filex-core/src/components/SideNav.vue';
import { tagKey, tagItemsOf, onTagsChanged } from '@brftech/filex-core/src/lib/tags';
import {
  makeTagSegment,
  tagOfPath,
  tagKindOfPath,
  virtualSegmentLabel,
  isVirtualViewPath,
} from '@brftech/filex-core/src/lib/listing';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';

type Answer = Record<string, unknown>;
let answer: Answer = {};
let posted: Answer[] = [];

function stubServer() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      if (!String(url).includes('/api/files/manager/tags')) throw new Error(`unexpected fetch: ${url}`);
      if (init?.method === 'POST') {
        const body = JSON.parse(String(init.body)) as Answer;
        posted.push(body);
        // Echo like the server does: the items it was sent.
        const reply = body.items ? { ok: true, items: body.items } : { ok: true, tags: body.tags };
        return { ok: true, status: 200, json: async () => reply, text: async () => '' } as unknown as Response;
      }
      return { ok: true, status: 200, json: async () => answer } as unknown as Response;
    }),
  );
}

function picker(locale: 'en' | 'tr' = 'tr') {
  return mount(TagPicker, {
    props: { nodeId: 7, locale, apiBase: '', authHeaders: () => ({}) },
    attachTo: document.body,
  });
}

async function settle(w: ReturnType<typeof picker>) {
  await vi.waitFor(() => expect(w.find('.filex-tag-picker').classes()).not.toContain('is-loading'));
}

describe('TagPicker — the kind is on every chip and in every add', () => {
  beforeEach(() => {
    posted = [];
    stubServer();
  });
  afterEach(() => vi.unstubAllGlobals());

  it('says which kind each chip is, personal first, in the viewer’s language', async () => {
    answer = {
      tags: ['Rapor', 'Müşteri Teklifi'],
      items: [
        { name: 'Rapor', kind: 'team' },
        { name: 'Müşteri Teklifi', kind: 'personal' },
      ],
      can_edit_team: true,
    };
    const w = picker('tr');
    await vi.waitFor(() => expect(w.findAll('.filex-tag').length).toBe(2));
    const chips = w.findAll('.filex-tag');
    expect(chips.map((c) => c.attributes('data-tag-kind'))).toEqual(['personal', 'team']);
    expect(chips[0].find('.fe-tagkind').attributes('data-tag-kind')).toBe('personal');
    const title = chips[0].find('.filex-tag-open').attributes('title') ?? '';
    expect(title).toContain('Kişisel etiket');
    expect(title).toContain('yalnızca siz görürsünüz');
    expect(chips[1].find('.filex-tag-open').attributes('title')).toContain('Ekip etiketi');
    // The name is exactly as typed — v0.42 lower-cased it.
    expect(chips[0].find('.filex-tag-open').text()).toBe('Müşteri Teklifi');
    w.unmount();
  });

  it('adds PERSONAL by default and TEAM when chosen — the choice is not lost to the blur', async () => {
    answer = { tags: [], items: [], can_edit_team: true };
    const w = picker('en');
    await settle(w);

    await w.find('.filex-tag-add-btn').trigger('click');
    await w.find('.filex-tag-add input').setValue('Draft');
    await w.find('.filex-tag-add').trigger('submit');
    await vi.waitFor(() => expect(posted.length).toBe(1));
    expect(posted[0]).toEqual({ node_id: 7, items: [{ name: 'Draft', kind: 'personal' }] });

    await w.find('.filex-tag-add-btn').trigger('click');
    const field = w.find('.filex-tag-add input');
    await field.setValue('Customer Offer');
    const team = w.find('[data-testid="tag-kind-team"]');
    // Leaving the field — to the choice, or anywhere — must NOT save it: that
    // is how a tag became personal on the pointer's way to "Team". A blur
    // with no relatedTarget is exactly what Safari produces.
    field.element.dispatchEvent(new FocusEvent('blur'));
    await w.vm.$nextTick();
    expect(posted.length).toBe(1);
    expect(w.find('.filex-tag-add').exists()).toBe(true);
    await team.trigger('click');
    await w.find('.filex-tag-add').trigger('submit');
    await vi.waitFor(() => expect(posted.length).toBe(2));
    expect(posted[1].items).toEqual([
      { name: 'Draft', kind: 'personal' },
      { name: 'Customer Offer', kind: 'team' },
    ]);
    w.unmount();
  });

  it('a viewer: team is offered disabled WITH the reason, and a team chip has no ×', async () => {
    answer = {
      items: [
        { name: 'Sözleşme', kind: 'team' },
        { name: 'Okunacak', kind: 'personal' },
      ],
      can_edit_team: false,
    };
    const w = picker('tr');
    await vi.waitFor(() => expect(w.findAll('.filex-tag').length).toBe(2));
    const byKind = (k: string) => w.find(`.filex-tag[data-tag-kind="${k}"]`);
    expect(byKind('team').find('.filex-tag-x').exists()).toBe(false);
    expect(byKind('personal').find('.filex-tag-x').exists()).toBe(true);

    await w.find('.filex-tag-add-btn').trigger('click');
    const team = w.find('[data-testid="tag-kind-team"]');
    expect(team.attributes('disabled')).toBeDefined();
    expect(team.text()).toContain('düzenleme yetkiniz olmalı');
    w.unmount();
  });

  it('an OLDER server (no items) gets the old shape and no kind choice', async () => {
    answer = { tags: ['alpha'] };
    const w = picker('en');
    await vi.waitFor(() => expect(w.findAll('.filex-tag').length).toBe(1));
    // Everything an old server had was shared with everybody — a team chip.
    expect(w.find('.filex-tag').attributes('data-tag-kind')).toBe('team');
    await w.find('.filex-tag-add-btn').trigger('click');
    expect(w.find('[data-testid="tag-kind-team"]').exists()).toBe(false);
    await w.find('.filex-tag-add input').setValue('beta');
    await w.find('.filex-tag-add').trigger('submit');
    await vi.waitFor(() => expect(posted.length).toBe(1));
    expect(posted[0]).toEqual({ node_id: 7, tags: ['alpha', 'beta'] });
    w.unmount();
  });

  it('a save that lands AFTER the dialog closed still reaches the panel', async () => {
    // ⚠ Measured in the full Playwright run: the tag saved, the panel kept
    // "No tags yet" — Vue drops an emit from an unmounted component, and the
    // dialog had been closed while the POST was in flight.
    answer = { items: [], can_edit_team: true };
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        if (init?.method === 'POST') {
          await gate;
          const body = JSON.parse(String(init.body)) as Answer;
          return { ok: true, status: 200, json: async () => ({ ok: true, items: body.items }) } as unknown as Response;
        }
        return { ok: true, status: 200, json: async () => answer } as unknown as Response;
      }),
    );
    const heard = vi.fn();
    const off = onTagsChanged(heard);
    const w = picker('en');
    await settle(w);
    await w.find('.filex-tag-add-btn').trigger('click');
    await w.find('.filex-tag-add input').setValue('Late');
    await w.find('.filex-tag-add').trigger('submit');
    w.unmount(); // the dialog is closed before the server answers
    release();
    await vi.waitFor(() => expect(heard).toHaveBeenCalledTimes(1));
    off();
  });

  it('opens the view of the chip’s own kind', async () => {
    answer = { items: [{ name: 'Rapor', kind: 'personal' }], can_edit_team: true };
    const w = picker('en');
    await vi.waitFor(() => expect(w.findAll('.filex-tag').length).toBe(1));
    await w.find('.filex-tag-open').trigger('click');
    expect(w.emitted('open')?.[0]).toEqual(['Rapor', 'personal']);
    w.unmount();
  });
});

describe('SideNav — both kinds, grouped', () => {
  function nav(extra: Record<string, unknown> = {}) {
    return mount(SideNav, {
      props: {
        expanded: true,
        activeView: '',
        storages: [{ name: 'main' }],
        locale: 'tr',
        showIdentitySurfaces: true,
        tagsLoaded: true,
        tags: [
          { name: 'Rapor', kind: 'personal' },
          { name: 'Müşteri Teklifi', kind: 'team' },
          { name: 'Rapor', kind: 'team' },
        ],
        ...extra,
      },
    });
  }

  it('draws a Personal group and a Team group, each row with its kind', () => {
    const w = nav();
    expect(w.find('[data-testid="sidenav-tags-personal"]').text()).toContain('Kişisel');
    expect(w.find('[data-testid="sidenav-tags-team"]').text()).toContain('Ekip');
    const rows = w.findAll('.fe-sidenav__item--tag');
    expect(rows.map((r) => `${r.attributes('data-tag-kind')}:${r.text()}`)).toEqual([
      'personal:Rapor',
      'team:Müşteri Teklifi',
      'team:Rapor',
    ]);
    expect(rows[1].attributes('title')).toContain('Ekip etiketi');
  });

  it('opens the one that was clicked, and lights only that one', async () => {
    const w = nav();
    const teamRapor = w.findAll('.fe-sidenav__item--tag')[2];
    await teamRapor.trigger('click');
    expect(w.emitted('open-tag')?.[0]).toEqual(['Rapor', 'team']);

    const active = nav({ activeView: 'tag', activeTag: 'Rapor', activeTagKind: 'team' });
    const lit = active.findAll('.fe-sidenav__item--tag.is-active');
    expect(lit.length).toBe(1);
    expect(lit[0].attributes('data-tag-kind')).toBe('team');
  });

  it('a group with only one kind shows only that caption', () => {
    const w = nav({ tags: [{ name: 'Rapor', kind: 'team' }] });
    expect(w.find('[data-testid="sidenav-tags-personal"]').exists()).toBe(false);
    expect(w.find('[data-testid="sidenav-tags-team"]').exists()).toBe(true);
  });
});

describe('lib — what "the same tag" is, and the kind in the address', () => {
  it('folds case and the four Turkish i’s, keeps accents', () => {
    expect(tagKey('MÜŞTERİ TEKLİFİ')).toBe(tagKey('müşteri teklifi'));
    expect(tagKey('IŞIK')).toBe(tagKey('ışık'));
    expect(tagKey('INVOICE')).toBe(tagKey('invoice'));
    expect(tagKey('  a   b ')).toBe(tagKey('a b'));
    expect(tagKey('müşteri')).not.toBe(tagKey('musteri'));
    // The premise: JS on its own gets the Turkish pair wrong.
    expect('IŞIK'.toLowerCase()).not.toBe('ışık');
  });

  it('reads both server shapes', () => {
    expect(tagItemsOf({ items: [{ name: 'a', kind: 'personal' }, { name: 'b', kind: 'x' }] })).toEqual([
      { name: 'a', kind: 'personal' },
    ]);
    expect(tagItemsOf({ tags: ['a'] })).toEqual([{ name: 'a', kind: 'team' }]);
    expect(tagItemsOf(null)).toEqual([]);
  });

  it('a tag view’s address carries its kind; the old one means both', () => {
    expect(makeTagSegment('rapor', 'personal')).toBe('.mytag~rapor');
    expect(makeTagSegment('rapor', 'team')).toBe('.teamtag~rapor');
    expect(makeTagSegment('rapor')).toBe('.tag~rapor');
    for (const p of ['.mytag~rapor', '.teamtag~rapor', '.tag~rapor']) {
      expect(isVirtualViewPath(p)).toBe(true);
      expect(tagOfPath(p)).toBe('rapor');
    }
    expect(tagKindOfPath('.mytag~rapor')).toBe('personal');
    expect(tagKindOfPath('.teamtag~rapor')).toBe('team');
    expect(tagKindOfPath('.tag~rapor')).toBe('');
    expect(isVirtualViewPath('.mytag~')).toBe(false);
  });

  it('the crumb says whose tag it is, in each language', () => {
    const trT = (k: string) => tr[k] ?? k;
    const enT = (k: string) => en[k] ?? k;
    expect(virtualSegmentLabel('.mytag~rapor', trT)).toBe('#rapor · Kişisel');
    expect(virtualSegmentLabel('.teamtag~rapor', enT)).toBe('#rapor · Team');
    expect(virtualSegmentLabel('.tag~rapor', enT)).toBe('#rapor');
  });
});
