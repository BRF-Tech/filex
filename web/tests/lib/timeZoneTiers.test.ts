// Whose clock an instant is read on — the ORDER, pinned.
//
// The owner's ruling (2026-09-14): the embedder, then the account behind the
// key when it is a person's key, then the browser — plus a time-zone setting
// inside the embed, kept in the browser. Where that setting ranks was a
// judgement call and it went on TOP (lib/timezone, `TIME_ZONE_TIERS`, carries
// the reasoning): a host's `config.timeZone` is a default it picks for every
// visitor, a viewer's pick is a decision one person made on purpose.
//
// The bug this whole order exists to end: the admin app followed the account
// and an embed on another origin followed the browser, so a person who chose
// Tokyo saw two different times for one file. Both now resolve through ONE
// function, and this file is what fails if anybody reorders it, or if the
// explorer starts imposing an app token's owner's clock on its visitors.
//
// ⚠ The resolver and the explorer half are imported from core's SOURCE, so
// the order is pinned where it is written. The admin-app half goes through
// `@/lib/timezone`, which is how the app itself reaches core.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { defineComponent, h, nextTick, ref } from 'vue';
import { mount } from '@vue/test-utils';

import {
  TIME_ZONE_TIERS,
  activeTimeZone,
  releaseTimeZoneOwner,
  resolveTimeZone,
  resolvedTimeZone,
  setAccountTimeZone,
  setHostTimeZone,
  setViewerTimeZone,
  TIMEZONE_VIEWER_LS_KEY,
  viewerTimeZone,
} from '@brftech/filex-core/src/lib/timezone';
import { useExplorerTimeZone } from '@brftech/filex-core/src/composables/useExplorerTimeZone';
import type { ExplorerConfig } from '@brftech/filex-core/src/types/ExplorerConfig';
import type { Capabilities } from '@brftech/filex-core/src/types/FileNode';

import {
  activeTimeZone as appActiveTimeZone,
  resolvedTimeZone as appResolvedTimeZone,
  setViewerTimeZone as appSetViewerTimeZone,
} from '@brftech/filex-core';
import { applyAccountTimeZone, setStoredTimeZone } from '@/lib/timezone';

const TOKYO = 'Asia/Tokyo';
const NEW_YORK = 'America/New_York';
const KOLKATA = 'Asia/Kolkata';

describe('the order', () => {
  it('is viewer, host, account, device — in that order and no other', () => {
    expect([...TIME_ZONE_TIERS]).toEqual(['viewer', 'host', 'account', 'device']);
  });

  it('each tier wins over everything below it, and only over that', () => {
    const all = { viewer: KOLKATA, host: NEW_YORK, account: TOKYO };
    expect(resolveTimeZone(all)).toEqual({ zone: KOLKATA, tier: 'viewer' });
    expect(resolveTimeZone({ ...all, viewer: '' })).toEqual({ zone: NEW_YORK, tier: 'host' });
    expect(resolveTimeZone({ ...all, viewer: '', host: '' })).toEqual({
      zone: TOKYO,
      tier: 'account',
    });
    // The device is `undefined`, not a name: Intl resolves it live.
    expect(resolveTimeZone({})).toEqual({ zone: undefined, tier: 'device' });
  });

  it('an id the engine rejects is no opinion — it falls through, it does not win', () => {
    expect(resolveTimeZone({ viewer: 'Mars/Olympus_Mons', host: NEW_YORK })).toEqual({
      zone: NEW_YORK,
      tier: 'host',
    });
    expect(resolveTimeZone({ host: 'nope', account: null })).toEqual({
      zone: undefined,
      tier: 'device',
    });
  });
});

describe('the page state follows the same order', () => {
  const host = Symbol('host');
  const account = Symbol('account');
  afterEach(() => {
    setViewerTimeZone('');
    releaseTimeZoneOwner(host);
    releaseTimeZoneOwner(account);
    localStorage.clear();
  });

  it('adding tiers bottom-up moves the clock each time; removing top-down moves it back', () => {
    expect(resolvedTimeZone().tier).toBe('device');
    setAccountTimeZone(account, TOKYO);
    expect(resolvedTimeZone()).toEqual({ zone: TOKYO, tier: 'account' });
    setHostTimeZone(host, NEW_YORK);
    expect(resolvedTimeZone()).toEqual({ zone: NEW_YORK, tier: 'host' });
    setViewerTimeZone(KOLKATA);
    expect(resolvedTimeZone()).toEqual({ zone: KOLKATA, tier: 'viewer' });

    setViewerTimeZone('');
    expect(activeTimeZone()).toBe(NEW_YORK);
    releaseTimeZoneOwner(host);
    expect(activeTimeZone()).toBe(TOKYO);
    releaseTimeZoneOwner(account);
    expect(activeTimeZone()).toBeUndefined();
  });

  it("the viewer's pick lives in this browser, and clearing it removes it", () => {
    setViewerTimeZone(TOKYO);
    expect(localStorage.getItem(TIMEZONE_VIEWER_LS_KEY)).toBe(TOKYO);
    setViewerTimeZone('');
    expect(localStorage.getItem(TIMEZONE_VIEWER_LS_KEY)).toBeNull();
    expect(viewerTimeZone()).toBe('');
  });

  it('an account that chose nothing ("") is recorded — it overrides an older answer', () => {
    const other = Symbol('other');
    setAccountTimeZone(other, TOKYO);
    setAccountTimeZone(account, '');
    expect(activeTimeZone()).toBeUndefined();
    releaseTimeZoneOwner(other);
  });
});

describe('an explorer instance: host config, and the account only for a PERSON', () => {
  function mountExplorer(opts: {
    config: ExplorerConfig;
    caps?: Capabilities | null;
    me?: () => Promise<{ user?: { timezone?: unknown } }>;
  }) {
    const calls = { me: 0 };
    const capabilities = ref<Capabilities | null>(opts.caps ?? null);
    const Probe = defineComponent({
      setup() {
        useExplorerTimeZone({
          config: () => opts.config,
          capabilities,
          fetchMe: () => {
            calls.me += 1;
            return (opts.me ?? (() => Promise.resolve({ user: { timezone: TOKYO } })))();
          },
        });
        return () => h('div');
      },
    });
    const w = mount(Probe);
    return { w, calls, capabilities };
  }
  const flush = async () => {
    for (let i = 0; i < 4; i++) await nextTick();
    await new Promise((r) => setTimeout(r, 0));
  };

  beforeEach(() => {
    setViewerTimeZone('');
    localStorage.clear();
  });

  it("an APP token never asks whose account it is — the visitors get their browser's clock", async () => {
    const { w, calls } = mountExplorer({ config: { apiBase: '' }, caps: { caller_kind: 'app' } });
    await flush();
    expect(calls.me).toBe(0);
    expect(resolvedTimeZone().tier).toBe('device');
    w.unmount();
  });

  it("a PERSON's token brings the account's zone", async () => {
    const { w, calls } = mountExplorer({ config: { apiBase: '' }, caps: { caller_kind: 'user' } });
    await flush();
    expect(calls.me).toBe(1);
    expect(resolvedTimeZone()).toEqual({ zone: TOKYO, tier: 'account' });
    // …and takes it away again on unmount, rather than leaving it to the next explorer.
    w.unmount();
    expect(resolvedTimeZone().tier).toBe('device');
  });

  it('the kind is unknown until capabilities land, and unknown does not guess "person"', async () => {
    const { w, calls, capabilities } = mountExplorer({ config: { apiBase: '' }, caps: null });
    await flush();
    expect(calls.me).toBe(0);
    capabilities.value = { caller_kind: 'user' };
    await flush();
    expect(calls.me).toBe(1);
    expect(activeTimeZone()).toBe(TOKYO);
    w.unmount();
  });

  it("the host's `callerKind: 'app'` wins over the server's answer", async () => {
    const { w, calls } = mountExplorer({
      config: { apiBase: '', callerKind: 'app' },
      caps: { caller_kind: 'user' },
    });
    await flush();
    expect(calls.me).toBe(0);
    w.unmount();
  });

  it('a failed lookup falls through to the browser silently', async () => {
    const { w } = mountExplorer({
      config: { apiBase: '' },
      caps: { caller_kind: 'user' },
      me: () => Promise.reject(Object.assign(new Error('401'), { status: 401 })),
    });
    await flush();
    expect(resolvedTimeZone().tier).toBe('device');
    w.unmount();
  });

  it('host config beats the account; the viewer beats the host', async () => {
    const { w } = mountExplorer({
      config: { apiBase: '', timeZone: NEW_YORK },
      caps: { caller_kind: 'user' },
    });
    await flush();
    expect(resolvedTimeZone()).toEqual({ zone: NEW_YORK, tier: 'host' });
    setViewerTimeZone(KOLKATA);
    expect(resolvedTimeZone()).toEqual({ zone: KOLKATA, tier: 'viewer' });
    setViewerTimeZone('');
    expect(resolvedTimeZone()).toEqual({ zone: NEW_YORK, tier: 'host' });
    w.unmount();
  });
});

describe('the admin app resolves through the same function', () => {
  beforeEach(() => {
    setStoredTimeZone('');
    localStorage.clear();
  });

  it("follows the account, and falls back to the browser when the account chose nothing", () => {
    applyAccountTimeZone(TOKYO);
    expect(appResolvedTimeZone()).toEqual({ zone: TOKYO, tier: 'account' });
    applyAccountTimeZone('');
    expect(appResolvedTimeZone()).toEqual({ zone: undefined, tier: 'device' });
  });

  it("a viewer pick outranks the account here too — and the settings modal's pick clears it", () => {
    applyAccountTimeZone(TOKYO);
    appSetViewerTimeZone(KOLKATA);
    expect(appActiveTimeZone()).toBe(KOLKATA);
    // The admin app's only door to the zone is the settings modal, so a pick
    // there must be able to undo a viewer pick left behind on this origin.
    setStoredTimeZone(NEW_YORK);
    expect(appResolvedTimeZone()).toEqual({ zone: NEW_YORK, tier: 'account' });
  });
});
