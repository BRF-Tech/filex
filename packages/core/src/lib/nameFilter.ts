/**
 * "Filter in this folder…" — answered by the SERVER's name rule (task #207,
 * audit D5).
 *
 * ⚠⚠ Why. The box used to narrow the rows in hand with a rule of its own (an
 * accent-stripped substring, `lib/fileFilters` foldText) and its comment said
 * that was the search's rule. It was not: the search keeps accents ("musteri"
 * does not find "müşteri"), treats `.`, `-`, `_` and a space as one separator
 * ("invoice 2026" finds "invoice_2026.pdf") and wants every word. The same
 * folder answered the same words two ways depending on which box was used.
 * Now the box asks the server (`POST /api/files/search/match`, FileApi
 * `matchNames`) which of the names on screen answer the words, and the server
 * answers with the search's own matcher (internal/search NameMatcher).
 *
 * The one exception is the one the rule allows: a name the server does not
 * know. A row whose name was decrypted in this tab (an encrypted-names folder,
 * `e2e_name_state`) or that lives in a vault (`vault_root`) is never sent - its
 * plaintext must not leave the tab - and is matched here by `nameMatches`.
 *
 * Typing is debounced and a question the person has typed past is aborted, so
 * only the last one is answered. Until the first answer arrives the rows stay
 * as they were; a server without the route (before 0.54) leaves the box on the
 * local rule rather than dead.
 */
import { getCurrentScope, onScopeDispose, ref, watch } from 'vue';

import { nameMatches } from './fileFilters';

/** The server question: which of `names` answer `q` (indexes). */
export type MatchNamesFn = (q: string, names: string[], signal?: AbortSignal) => Promise<number[]>;

/** How long typing has to pause before the server is asked. */
export const NAME_FILTER_DEBOUNCE_MS = 200;

/** True for a row whose name only this tab knows (see the file note). */
export function nameIsLocal(row: unknown): boolean {
  if (!row || typeof row !== 'object') return false;
  const r = row as Record<string, unknown>;
  const st = r.e2e_name_state;
  if (st === 'enc' || st === 'locked' || st === 'unreadable') return true;
  return typeof r.vault_root === 'string' && r.vault_root !== '';
}

export interface ServerNameFilter<T> {
  /** The rows the box keeps, or `items` itself when it narrows nothing. */
  apply(items: T[]): T[];
  /** A question is out (typing paused, answer not back yet). */
  readonly pending: { value: boolean };
}

/**
 * One box's filter. `needle` is what was typed, `items` the rows it narrows,
 * `nameOf` a row's name, `match` the server question (FileApi `matchNames`;
 * undefined = no server, the local rule answers).
 */
export function useServerNameFilter<T>(
  needle: () => string,
  items: () => T[],
  nameOf: (item: T) => string,
  match: () => MatchNamesFn | undefined,
): ServerNameFilter<T> {
  /** The names the server said answer the needle it was last asked. */
  const answered = ref<{ q: string; names: Set<string> } | null>(null);
  /** The server could not answer (older build, offline): local rule. */
  const unanswered = ref(false);
  const pending = ref(false);
  let timer: ReturnType<typeof setTimeout> | null = null;
  let inflight: AbortController | null = null;

  function cancel() {
    if (timer) clearTimeout(timer);
    timer = null;
    inflight?.abort();
    inflight = null;
  }

  async function ask(q: string, names: string[]) {
    const fn = match();
    if (!fn) {
      unanswered.value = true;
      return;
    }
    const ctl = new AbortController();
    inflight = ctl;
    pending.value = true;
    try {
      const idx = await fn(q, names, ctl.signal);
      if (ctl.signal.aborted) return;
      answered.value = { q, names: new Set(idx.map((i) => names[i]).filter((n): n is string => typeof n === 'string')) };
      unanswered.value = false;
    } catch (e) {
      if ((e as Error)?.name === 'AbortError' || ctl.signal.aborted) return;
      unanswered.value = true;
    } finally {
      if (inflight === ctl) {
        inflight = null;
        pending.value = false;
      }
    }
  }

  watch(
    () => {
      const q = needle().trim();
      if (!q) return '';
      const names = new Set<string>();
      for (const it of items()) {
        if (!nameIsLocal(it)) names.add(nameOf(it) || '');
      }
      return JSON.stringify([q, [...names].sort()]);
    },
    (key) => {
      cancel();
      if (!key) {
        answered.value = null;
        pending.value = false;
        return;
      }
      const [q, names] = JSON.parse(key) as [string, string[]];
      if (names.length === 0) {
        answered.value = { q, names: new Set() };
        return;
      }
      pending.value = true;
      timer = setTimeout(() => {
        timer = null;
        void ask(q, names);
      }, NAME_FILTER_DEBOUNCE_MS);
    },
    { immediate: true },
  );

  // A component's setup (or any effect scope) drops the timer and the question
  // out with it; called outside one (a test), nothing to tie it to.
  if (getCurrentScope()) onScopeDispose(cancel);

  return {
    pending,
    apply(list: T[]): T[] {
      const q = needle().trim();
      if (!q) return list;
      const got = answered.value;
      return list.filter((it) => {
        const name = nameOf(it) || '';
        // No server to ask (a host that wires none), or it could not answer:
        // the local rule, at once - not rows left unnarrowed while waiting.
        if (nameIsLocal(it) || unanswered.value || !match()) return nameMatches(name, q);
        // Not answered yet for these words: keep the row rather than flash an
        // empty folder; the answer narrows it a moment later.
        if (!got || got.q !== q) return true;
        return got.names.has(name);
      });
    },
  };
}
