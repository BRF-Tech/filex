// "Filter in this folder…" is answered by the SERVER's name rule (task #207,
// audit D5).
//
// The box used to narrow the rows in hand with an accent-stripped substring of
// its own, and its comment called that the search's rule. It was not: the
// search keeps accents and treats `.`, `-`, `_` and a space as one separator.
// Now lib/nameFilter asks `POST /api/files/search/match` (FileApi matchNames)
// which names answer, debounced, the question typed past aborted - and a name
// decrypted in this tab never leaves it.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick, ref } from 'vue';

import { NAME_FILTER_DEBOUNCE_MS, nameIsLocal, useServerNameFilter } from '@brftech/filex-core/src/lib/nameFilter';

type Row = { basename: string; e2e_name_state?: string; vault_root?: string };

function rows(...names: string[]): Row[] {
  return names.map((basename) => ({ basename }));
}

async function settle() {
  await nextTick();
  vi.advanceTimersByTime(NAME_FILTER_DEBOUNCE_MS + 1);
  for (let i = 0; i < 6; i++) await Promise.resolve();
  await nextTick();
}

describe('nameFilter — the box asks the server', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('keeps exactly the names the server matched (its rule, not a substring of ours)', async () => {
    const needle = ref('');
    const list = ref(rows('invoice_2026.pdf', 'invoice-final.pdf', 'müşteri.pdf'));
    // The server's answer for "invoice 2026": separators are one, every word.
    const match = vi.fn(async (_q: string, names: string[]) =>
      names.flatMap((n, i) => (n === 'invoice_2026.pdf' ? [i] : [])),
    );
    const f = useServerNameFilter<Row>(() => needle.value, () => list.value, (r) => r.basename, () => match);

    expect(f.apply(list.value)).toBe(list.value);
    needle.value = 'invoice 2026';
    await settle();
    expect(match).toHaveBeenCalledTimes(1);
    expect(match.mock.calls[0][0]).toBe('invoice 2026');
    expect(f.apply(list.value).map((r) => r.basename)).toEqual(['invoice_2026.pdf']);
  });

  it('asks once typing pauses, and aborts the question typed past', async () => {
    const needle = ref('');
    const list = ref(rows('a.txt', 'ab.txt'));
    const signals: AbortSignal[] = [];
    const match = vi.fn((q: string, _n: string[], signal?: AbortSignal): Promise<number[]> => {
      if (signal) signals.push(signal);
      // "abc" is still out when "abcd" is typed: it is the one to abort.
      return q === 'abc' ? new Promise<number[]>(() => {}) : Promise.resolve([0]);
    });
    useServerNameFilter<Row>(() => needle.value, () => list.value, (r) => r.basename, () => match);

    needle.value = 'a';
    await nextTick();
    needle.value = 'ab';
    await settle();
    expect(match).toHaveBeenCalledTimes(1);
    expect(match.mock.calls[0][0]).toBe('ab');

    needle.value = 'abc';
    await nextTick();
    vi.advanceTimersByTime(NAME_FILTER_DEBOUNCE_MS + 1);
    needle.value = 'abcd';
    await nextTick();
    expect(signals.at(-1)?.aborted).toBe(true);
  });

  it('never sends a name decrypted in this tab, and matches it here', async () => {
    const needle = ref('plan');
    const list = ref<Row[]>([
      { basename: 'plan.docx' },
      { basename: 'gizli plan.pdf', e2e_name_state: 'enc' },
      { basename: 'kasa plan.txt', vault_root: 'v://Kasa' },
    ]);
    const match = vi.fn(async (_q: string, names: string[]) => names.map((_, i) => i));
    const f = useServerNameFilter<Row>(() => needle.value, () => list.value, (r) => r.basename, () => match);
    await settle();
    expect(match).toHaveBeenCalledTimes(1);
    expect(match.mock.calls[0][1]).toEqual(['plan.docx']);
    expect(f.apply(list.value).map((r) => r.basename)).toEqual(['plan.docx', 'gizli plan.pdf', 'kasa plan.txt']);
    expect(nameIsLocal({ e2e_name_state: 'locked' })).toBe(true);
    expect(nameIsLocal({ e2e_name_state: 'plain' })).toBe(false);
  });

  it('a server without the route leaves the box on the local rule, not dead', async () => {
    const needle = ref('bud');
    const list = ref(rows('Q3 budget.xlsx', 'notes.txt'));
    const match = vi.fn(async () => {
      throw new Error('404');
    });
    const f = useServerNameFilter<Row>(() => needle.value, () => list.value, (r) => r.basename, () => match);
    await settle();
    expect(f.apply(list.value).map((r) => r.basename)).toEqual(['Q3 budget.xlsx']);
  });
});
