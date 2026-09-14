// Two explorers on ONE page read their dates on their OWN clocks.
//
// The zone tiers live in module state, one copy per page, and a tier fed by
// several explorers answered with the NEWEST write. So a page embedding a
// Tokyo explorer beside a London one printed both in whichever mounted last:
// the host tier (`config.timeZone`) is by definition one explorer's setting,
// yet the other explorer obeyed it. Each explorer now provides its owner key
// and every date its components print resolves against that explorer's own
// host and account tiers; the viewer tier (this browser's pick) stays shared,
// because it is the person's, not the embed's.
import { afterEach, describe, expect, it } from 'vitest';
import { defineComponent, h, nextTick, provide, ref, type Ref } from 'vue';
import { mount } from '@vue/test-utils';

import { EXPLORER_CLOCK, releaseTimeZoneOwner, setHostTimeZone, setViewerTimeZone } from '@brftech/filex-core/src/lib/timezone';
import { useLocale } from '@brftech/filex-core/src/composables/useLocale';

const INSTANT = Date.UTC(2026, 8, 14, 22, 30); // 22:30 UTC

const owners: symbol[] = [];

/** A stand-in explorer: registers its host zone, provides its owner, renders one date. */
function explorer(zone: string, out: Ref<string>) {
  const Child = defineComponent({
    setup() {
      const { formatDate } = useLocale(() => 'en');
      return () => {
        out.value = formatDate(INSTANT, { time: true });
        return h('span', out.value);
      };
    },
  });
  return defineComponent({
    setup() {
      const owner = Symbol('explorer');
      owners.push(owner);
      setHostTimeZone(owner, zone);
      provide(EXPLORER_CLOCK, owner);
      return () => h(Child);
    },
  });
}

afterEach(() => {
  owners.splice(0).forEach(releaseTimeZoneOwner);
  setViewerTimeZone('');
});

describe('two explorers on one page', () => {
  it('each prints the time on its own host zone', async () => {
    const tokyo = ref('');
    const london = ref('');
    mount(explorer('Asia/Tokyo', tokyo));
    mount(explorer('Europe/London', london));
    await nextTick();

    expect(tokyo.value).toContain('7:30'); // 07:30 on the 15th in Tokyo
    expect(tokyo.value).toContain('Sep 15');
    expect(london.value).toContain('11:30'); // 23:30 BST on the 14th
    expect(london.value).toContain('Sep 14');
  });

  it("the viewer's own pick still wins in both", async () => {
    setViewerTimeZone('America/New_York');
    const a = ref('');
    const b = ref('');
    mount(explorer('Asia/Tokyo', a));
    mount(explorer('Europe/London', b));
    await nextTick();
    expect(a.value).toContain('6:30');
    expect(b.value).toContain('6:30');
  });
});
