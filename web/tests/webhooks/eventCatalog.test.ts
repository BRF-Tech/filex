// The gate that keeps the admin UI's webhook event list honest.
//
// Why this test exists: `web/src/views/Webhooks.vue` carried a hand-written
// copy of the backend's event names, and it drifted. `file.upload_failed`,
// `file.infected` and `comment.added` were emitted by the server for releases
// while no operator could tick them — `file.infected` most of all: filex scans
// uploads for viruses and there was no way to ask it to say so when it found
// one. The copy even carried a comment admitting the drift, which is the shape
// of a problem everybody knows about and nobody is told about again.
//
// A hand-maintained list is the right answer here (see the note in
// `src/lib/webhookEvents.ts` — the UI is compiled into the server binary, so a
// runtime endpoint could never disagree and would only add a failure mode).
// What a hand list needs is a build-time gate, which is this file: it parses
// the Go constants and fails when the two sets differ, so the next drift is
// caught by CI instead of discovered by a user.
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { WEBHOOK_EVENTS, webhookEventKey } from '@/lib/webhookEvents';

const here = path.dirname(fileURLToPath(import.meta.url));
const EVENT_GO = path.resolve(here, '../../../backend/internal/notify/event.go');

/**
 * The dotted `EventType` constants declared in event.go — the backend's
 * subscribable webhook-v2 catalogue.
 *
 * The `Name EventType = "value"` form only matches a declaration: a
 * `notify.EventType("x.y")` conversion (the shape the Go test refuses) and a
 * mention inside a `//` comment both fail the anchor.
 */
function backendEvents(source: string): string[] {
  const re = /^\s*Event\w*\s+EventType\s*=\s*"([^"]+)"/gm;
  const out: string[] = [];
  for (const m of source.matchAll(re)) {
    if (m[1].includes('.')) out.push(m[1]);
  }
  return out;
}

describe('webhook event catalogue', () => {
  const source = fs.readFileSync(EVENT_GO, 'utf8');
  const fromGo = backendEvents(source);

  it('finds the backend catalogue at all', () => {
    // A parser that silently matches nothing would turn every assertion below
    // into a tautology — exactly the "test that passes for the wrong reason"
    // this file is meant to prevent.
    expect(
      fromGo.length,
      `parsed no dotted EventType constants out of ${EVENT_GO}; the declaration ` +
        'shape changed and this test is no longer measuring anything',
    ).toBeGreaterThan(5);
  });

  it('offers every event the backend can emit', () => {
    const missing = fromGo.filter((e) => !(WEBHOOK_EVENTS as readonly string[]).includes(e));
    expect(
      missing,
      `emitted by the backend but not offered in the admin UI: ${missing.join(', ')} — ` +
        'add them to web/src/lib/webhookEvents.ts with a label in en.json and tr.json',
    ).toEqual([]);
  });

  it('offers nothing the backend does not emit', () => {
    const extra = (WEBHOOK_EVENTS as readonly string[]).filter((e) => !fromGo.includes(e));
    expect(
      extra,
      `offered in the admin UI but never emitted: ${extra.join(', ')} — ` +
        'a checkbox that can never fire is a promise the product does not keep',
    ).toEqual([]);
  });

  it('has no duplicates', () => {
    expect(new Set(WEBHOOK_EVENTS).size).toBe(WEBHOOK_EVENTS.length);
  });

  const locales: Array<[string, Record<string, unknown>]> = [
    ['en.json', en as Record<string, unknown>],
    ['tr.json', tr as Record<string, unknown>],
  ];

  for (const [name, bundle] of locales) {
    it(`gives every event an operator-readable label in ${name}`, () => {
      const events = (bundle.webhooks as Record<string, unknown> | undefined)?.events as
        | Record<string, string>
        | undefined;
      expect(events, `${name} has no webhooks.events block`).toBeTruthy();

      const problems: string[] = [];
      for (const ev of WEBHOOK_EVENTS) {
        const slug = webhookEventKey(ev).split('.').pop() as string;
        const label = events?.[slug];
        if (!label || !label.trim()) {
          problems.push(`${ev}: no label (${name} webhooks.events.${slug})`);
          continue;
        }
        // Pasting the event id in as its own label is not a translation; the
        // raw name is already shown underneath the checkbox.
        if (label.trim() === ev) {
          problems.push(`${ev}: the label is just the event id`);
        }
      }
      expect(problems, problems.join('\n')).toEqual([]);
    });
  }
});
