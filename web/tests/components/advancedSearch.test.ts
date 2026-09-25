// gorunum:v1-advsearch — the Advanced search dialog, mounted.
//
// ⚠⚠ This file exists because of a bug a pure-function test could not have
// found. `lib/fileFilters.ts` handled a custom size range correctly and its
// unit tests were green; the dialog still did not narrow by size, because the
// COMPONENT handed the predicate nothing. Vue casts the value of an
// `<input type="number">` to a NUMBER on its own (`runtime-dom`:
// `castToNumber = number || vnode.props.type === 'number'`), the helper that
// turned the field into bytes called `.trim()` on it, and the throw landed
// inside the computed the count watcher reads — so the watcher never fired and
// the dialog kept printing the count from before the change.
//
// Measured in a real browser first: a ceiling of 0 bytes over two files of 16
// and 184 bytes still said "2 matching items". Nothing said otherwise —
// vue-tsc was green (the ref was declared `string`, which is what the helper
// claimed to take), the unit suite was green, and a filter that is a no-op
// looks exactly like a filter that matched everything.
//
// So what is pinned here is the SEAM: what the form actually hands the count
// function. Not "does a size range work" — that is next door — but "does
// typing a number in this box reach the request as bytes at all".
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import AdvancedSearch from '@brftech/filex-core/src/components/AdvancedSearch.vue';
import type { AdvCountResult, AdvSearchRequest } from '@brftech/filex-core/src/lib/advSearch';

const MB = 1024 * 1024;

/** Every request the dialog asked a count for, newest last. */
let seen: AdvSearchRequest[] = [];
/** What the next count run answers. Mutable so a test can hand the dialog the
 *  per-account People members a real run would have found. */
let countAnswer: AdvCountResult = { count: 0, capped: false };

function mountDialog(props: Record<string, unknown> = {}) {
  seen = [];
  countAnswer = { count: 0, capped: false };
  return mount(AdvancedSearch, {
    props: {
      open: true,
      locale: 'en' as const,
      pathBase: 'demo://Documents',
      folderLabel: 'Documents',
      contentSearch: true,
      count: async (req: AdvSearchRequest) => {
        seen.push(JSON.parse(JSON.stringify(req)));
        return countAnswer;
      },
      ...props,
    },
    attachTo: document.body,
  });
}

/** Let the 450ms debounce elapse and the count promise settle. */
async function settle(w: VueWrapper) {
  await vi.advanceTimersByTimeAsync(600);
  await w.vm.$nextTick();
}

const last = () => seen[seen.length - 1];

describe('AdvancedSearch — what the form hands the query', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('a custom size range reaches the request as BYTES', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await w.find('[data-testid="advsearch-size"]').setValue('range');
    // ⚠ The regression itself. Vue hands this back as a number, not a string.
    await w.find('[data-testid="advsearch-size-from"]').setValue('2');
    await settle(w);

    expect(last().filters.size).toBe('range');
    expect(last().filters.sizeMin).toBe(2 * MB);
    expect(last().filters.sizeMax).toBeNull();
    w.unmount();
  });

  it('an empty end of the range stays OPEN — never zero bytes', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await w.find('[data-testid="advsearch-size"]').setValue('range');
    await w.find('[data-testid="advsearch-size-to"]').setValue('5');
    await settle(w);
    expect(last().filters.sizeMin).toBeNull();
    expect(last().filters.sizeMax).toBe(5 * MB);
    w.unmount();
  });

  it('the unit multiplies the number the reader typed', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await w.find('[data-testid="advsearch-size"]').setValue('range');
    await w.find('[data-testid="advsearch-size-from"]').setValue('4');
    await w.find('.fe-advsearch__unit').setValue('kb');
    await settle(w);
    expect(last().filters.sizeMin).toBe(4 * 1024);
    w.unmount();
  });

  it('tags become tag: tokens on the wire, and the dialog shows the wire text', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await w.find('[data-testid="advsearch-tags"]').setValue('invoice, quarterly report');
    await w.find('[data-testid="advsearch-extags"]').setValue('archive');
    await settle(w);
    expect(last().tags).toEqual(['invoice', 'quarterly report']);
    expect(last().excludeTags).toEqual(['archive']);
    // A value with a space has to be quoted or the parser splits it in two.
    expect(w.find('.fe-advsearch__wire').text()).toContain(
      'report tag:invoice tag:"quarterly report" -tag:archive',
    );
    w.unmount();
  });

  it('the folder choice carries the base it was opened with, not where the explorer ends up', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    // The folder choice is a segmented trio now (the reference shell's shape),
    // so it is CLICKED rather than set. What is pinned here is unchanged: which
    // base the choice measures against.
    await w.find('[data-testid="advsearch-where-here"]').trigger('click');
    await settle(w);
    expect(last().filters.pathMode).toBe('here');
    expect(last().filters.pathBase).toBe('demo://Documents');
    w.unmount();
  });

  it('counts nothing until there is something to ask, and a bare tag IS something', async () => {
    const w = mountDialog();
    await settle(w);
    expect(seen.length).toBe(0);
    // A bare `tag:` with no free text is a listing, which the backend answers.
    await w.find('[data-testid="advsearch-tags"]').setValue('invoice');
    await settle(w);
    expect(seen.length).toBe(1);
    expect(last().text).toBe('');
    expect(last().tags).toEqual(['invoice']);
    w.unmount();
  });

  // ⚠ This test used to read "draws no Owner control and no phrase option".
  // Half of it expired: migration 00038 put ownership on the row and the
  // listing projection carries it, so the Owner control now answers from data.
  // The phrase half did not expire — the query language still has no phrase
  // operator — so it stays exactly as it was.
  it('draws no phrase option — the query language has no phrase operator', () => {
    const w = mountDialog();
    expect(w.text()).not.toMatch(/phrase/i);
    w.unmount();
  });

  it('offers exactly the scopes the backend accepts, and only names when content is off', async () => {
    const w = mountDialog();
    expect(w.findAll('.fe-advsearch__seg-btn[role="tab"]').length).toBe(3);
    expect(w.find('[data-testid="advsearch-scope-content"]').exists()).toBe(true);
    await w.setProps({ contentSearch: false });
    expect(w.findAll('.fe-advsearch__seg-btn[role="tab"]').length).toBe(1);
    expect(w.find('[data-testid="advsearch-scope-content"]').exists()).toBe(false);
    w.unmount();
  });

  it('says out loud that the count costs a search', () => {
    const w = mountDialog();
    expect(w.text()).toMatch(/count runs this search/i);
    // ⚠ And that the sentence still NAMES every client-side narrowing. Owner
    // joined the list; a control applied to the rows that came back while the
    // sentence claims only four are is the dialog quietly starting to lie.
    expect(w.text()).toMatch(/type, owner, date, size and folder/i);
    w.unmount();
  });

  /* ── the Owner filter ─────────────────────────────────────── */

  it('the Owner choice reaches the request as filters.people', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await settle(w);
    expect(last().filters.people).toBe('any');
    await w.find('[data-testid="advsearch-people"]').setValue('me');
    await settle(w);
    expect(last().filters.people).toBe('me');
    await w.find('[data-testid="advsearch-people"]').setValue('system');
    await settle(w);
    expect(last().filters.people).toBe('system');
    w.unmount();
  });

  it('offers Anyone / You / System with no directory behind them', () => {
    const w = mountDialog();
    const opts = w.find('[data-testid="advsearch-people"]').findAll('option');
    expect(opts.map((o) => o.attributes('value'))).toEqual(['any', 'me', 'system']);
    expect(opts.map((o) => o.text())).toEqual(['Anyone', 'You', 'System']);
    w.unmount();
  });

  it('names an account only when the counted rows actually contained one', async () => {
    const w = mountDialog();
    countAnswer = { count: 4, capped: false, people: [{ value: 'u:7', name: 'Grace Hopper' }] };
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await settle(w);
    const opts = w.find('[data-testid="advsearch-people"]').findAll('option');
    expect(opts.map((o) => o.attributes('value'))).toEqual(['any', 'me', 'system', 'u:7']);
    expect(opts[3].text()).toBe('Grace Hopper');

    await w.find('[data-testid="advsearch-people"]').setValue('u:7');
    await settle(w);
    expect(last().filters.people).toBe('u:7');

    // ⚠ The member leaves when the next run's rows no longer contain it. A
    // <select> holding a value that is no longer an option renders BLANK and
    // goes on filtering — so the choice has to fall back, visibly, to Anyone.
    countAnswer = { count: 2, capped: false, people: [] };
    await w.find('[data-testid="advsearch-query"]').setValue('reports');
    await settle(w);
    await settle(w);
    expect((w.find('[data-testid="advsearch-people"]').element as HTMLSelectElement).value).toBe('any');
    expect(last().filters.people).toBe('any');
    w.unmount();
  });

  it('turns Owner OFF under the content scopes, and stops filtering by it', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await w.find('[data-testid="advsearch-people"]').setValue('me');
    await settle(w);
    expect(last().filters.people).toBe('me');
    expect(w.find('[data-testid="advsearch-people"]').attributes('disabled')).toBeUndefined();

    // ⚠ A content hit reaches the client through lib/searchHit.hitToNode,
    // which carries no owner — every row would read as System. Disabling the
    // control is only half of it: the value it was left holding must stop
    // reaching the request too, or a filter survives its own control.
    await w.find('[data-testid="advsearch-scope-content"]').trigger('click');
    await settle(w);
    expect(w.find('[data-testid="advsearch-people"]').attributes('disabled')).toBeDefined();
    expect(last().filters.people).toBe('any');
    expect(w.text()).toMatch(/only a name search returns rows that carry one/i);
    w.unmount();
  });

  /* ── reopening continues the search, it does not restart it ───────────── */

  it('reopening over the applied query shows the filter that is IN FORCE', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await w.find('[data-testid="advsearch-type"]').setValue('folder');
    await w.find('[data-testid="advsearch-people"]').setValue('me');
    await w.find('[data-testid="advsearch-tags"]').setValue('invoice');
    await settle(w);
    await w.find('[data-testid="advsearch-submit"]').trigger('click');

    const req = w.emitted('submit')?.[0]?.[0] as AdvSearchRequest;
    expect(req.filters.type).toBe('folder');
    // What the explorer does with a submit: close, and put the wire query in
    // the toolbar field — which is what it seeds the dialog from next time.
    await w.setProps({ open: false });
    await w.setProps({ open: true, initialQuery: 'report tag:invoice' });
    await settle(w);

    // ⚠⚠ The bug: this used to be `any` / `any` / `''`, and pressing Search
    // from there submitted the reset form and silently dropped the filter.
    expect((w.find('[data-testid="advsearch-type"]').element as HTMLSelectElement).value).toBe('folder');
    expect((w.find('[data-testid="advsearch-people"]').element as HTMLSelectElement).value).toBe('me');
    expect((w.find('[data-testid="advsearch-tags"]').element as HTMLInputElement).value).toBe('invoice');
    // …and the count now describes THAT filter, not an empty one.
    expect(last().filters.type).toBe('folder');
    expect(last().filters.people).toBe('me');
    expect(last().tags).toEqual(['invoice']);
    w.unmount();
  });

  it('reopening after the toolbar query changed starts CLEAN', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await w.find('[data-testid="advsearch-type"]').setValue('folder');
    await settle(w);
    await w.find('[data-testid="advsearch-submit"]').trigger('click');

    // Typing in the toolbar field clears the advanced filters in the explorer
    // (`onToolbarSearch`), so the dialog must not offer to continue a filter
    // that is no longer applied to anything.
    await w.setProps({ open: false });
    await w.setProps({ open: true, initialQuery: 'budget' });
    await settle(w);
    expect((w.find('[data-testid="advsearch-type"]').element as HTMLSelectElement).value).toBe('any');
    expect((w.find('[data-testid="advsearch-query"]').element as HTMLInputElement).value).toBe('budget');
    expect(last().filters.type).toBe('any');
    w.unmount();
  });

  it('abandoning the dialog changes nothing — the NEXT opening still shows what is applied', async () => {
    const w = mountDialog();
    await w.find('[data-testid="advsearch-query"]').setValue('report');
    await w.find('[data-testid="advsearch-size"]').setValue('lt1');
    await settle(w);
    await w.find('[data-testid="advsearch-submit"]').trigger('click');

    // Reopen, fiddle, press Cancel. Nothing was applied, so nothing changed.
    await w.setProps({ open: false });
    await w.setProps({ open: true, initialQuery: 'report' });
    await settle(w);
    await w.find('[data-testid="advsearch-size"]').setValue('gt100');
    await w.find('[data-testid="advsearch-type"]').setValue('image');
    await settle(w);
    await w.setProps({ open: false });
    await w.setProps({ open: true, initialQuery: 'report' });
    await settle(w);
    expect((w.find('[data-testid="advsearch-size"]').element as HTMLSelectElement).value).toBe('lt1');
    expect((w.find('[data-testid="advsearch-type"]').element as HTMLSelectElement).value).toBe('any');
    w.unmount();
  });
});
