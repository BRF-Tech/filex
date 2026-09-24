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
import { WEBHOOK_EVENTS, userEventKey, webhookEventKey } from '@/lib/webhookEvents';
import { NOTIFICATION_PHRASES, renderNotification } from '@/lib/notificationText';

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

/** Walks a dotted i18n path into a loaded bundle. */
function lookup(bundle: Record<string, unknown>, key: string): string | undefined {
  let cur: unknown = bundle;
  for (const seg of key.split('.')) {
    if (!cur || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return typeof cur === 'string' ? cur : undefined;
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

    // ── the second audience ───────────────────────────────────
    //
    // The per-event switches in the user-settings dialog are read by the
    // person receiving the notifications, not by the operator wiring the
    // delivery. They borrowed the operator sentences for a release, and the
    // result was a list explaining write semantics ("a write created a file
    // that did not exist") to somebody who had opened "What to tell me about"
    // to stop being pinged about comments.
    //
    // ⚠ This is the half of the gate that can rot silently: an event added to
    // event.go and to WEBHOOK_EVENTS with only an operator label renders as a
    // raw i18n key in the dialog — visible to every user, invisible to the
    // build. Both catalogues are therefore required, for both languages.
    it(`gives every event a short end-user label in ${name}`, () => {
      const problems: string[] = [];
      for (const ev of WEBHOOK_EVENTS) {
        const key = userEventKey(ev);
        const label = lookup(bundle, key);
        if (!label || !label.trim()) {
          problems.push(`${ev}: no end-user label (${name} ${key})`);
          continue;
        }
        if (label.trim() === ev) {
          problems.push(`${ev}: the end-user label is just the event id`);
        }
      }
      expect(problems, problems.join('\n')).toEqual([]);
    });

    // ⚠ Not a style rule — a drift alarm. The cheapest way to satisfy the test
    // above is to paste the operator sentence into the new block, which is
    // exactly the state this work removed. If the two are identical, the
    // dialog is back to explaining write semantics and nobody would notice.
    it(`keeps the two audiences apart in ${name}`, () => {
      const operator = (bundle.webhooks as Record<string, unknown> | undefined)?.events as
        | Record<string, string>
        | undefined;
      const same: string[] = [];
      for (const ev of WEBHOOK_EVENTS) {
        const slug = webhookEventKey(ev).split('.').pop() as string;
        const op = operator?.[slug]?.trim();
        const user = lookup(bundle, userEventKey(ev))?.trim();
        if (op && user && op === user) same.push(ev);
      }
      expect(
        same,
        `the end-user label is a copy of the operator sentence for: ${same.join(', ')} — ` +
          'the switches in the settings dialog are read by the person being notified, ' +
          'not by the person wiring the webhook',
      ).toEqual([]);
    });
  }

  // ── the third catalogue: what a notification SAYS ──────────────────────
  //
  // ⚠⚠ This is the one that was reaching real people as wire format. A row is
  // written once, on the server, in one language, and eight of the eleven file
  // events set no title at all — `notify.Service.Send` substitutes the event
  // id, so the bell showed `share.created` and a browser notification came out
  // as `{title: "file.uploaded", body: "/Documents/measure-me.txt"}` (measured
  // 2026-09-12). The sentence is composed by the reader now; these tests make
  // it impossible for a NEW event to arrive without one.
  for (const lang of ["en", "tr"] as const) {
    it(`phrases every backend event in ${lang}`, () => {
      const missing = fromGo.filter((e) => !NOTIFICATION_PHRASES[e]?.[lang]);
      expect(
        missing,
        `emitted by the backend with no ${lang} phrasing: ${missing.join(", ")} — ` +
          "add it to web/src/lib/notificationText.ts, or the bell, the browser " +
          "toast and the desktop app will all show the raw event id",
      ).toEqual([]);
    });

    it(`never renders a raw event id in ${lang}`, () => {
      // A realistic row: what the server actually stores for a file event —
      // no usable title, a bare path for a body, the facts in meta.
      const problems: string[] = [];
      for (const ev of fromGo) {
        const { title, body } = renderNotification(
          {
            event: ev,
            title: ev,
            body: "Belgeler/rapor.pdf",
            meta: {
              origin: "manager",
              node: { path: "Belgeler/rapor.pdf", name: "rapor.pdf", size: 12 },
              reason: "driver refused the write",
              signature: "Eicar-Test-Signature",
              from: "Belgeler/eski.pdf",
              to: "Belgeler/rapor.pdf",
              folder: "Gelen",
              count: 3,
              uploader: "",
              body: "looks good to me",
              storage: "team",
            },
            target: { kind: "file", storage: "team", path: "Belgeler/rapor.pdf" },
          },
          lang,
        );
        if (!title.trim()) problems.push(`${ev}: empty title`);
        if (title.includes(ev)) problems.push(`${ev}: the title is the event id (${title})`);
        if (/\{\w+\}/.test(title) || /\{\w+\}/.test(body)) {
          problems.push(`${ev}: an unresolved placeholder survived (${title} / ${body})`);
        }
        // A dangling separator is what a missing field leaves behind, and it
        // reads as a bug to the person looking at it.
        if (/(^\s*[-:]|[-:]\s*$)/.test(title)) problems.push(`${ev}: dangling punctuation (${title})`);
      }
      expect(problems, problems.join('\n')).toEqual([]);
    });
  }

  // ── the fourth: what KIND of event a row is, on the admin list ─────────
  //
  // ⚠ The admin notifications page printed the raw id in its Event column —
  // `share.created`, `update_available` — beside a Turkish sentence
  // (release-candidate sweep, 2026-09-21). Every id the backend declares,
  // dotted or not, plus the two test ids the admin screens fire, needs a short
  // name in both languages, or the column is back to wire format for that one.
  const allFromGo = [...source.matchAll(/^\s*Event\w*\s+EventType\s*=\s*"([^"]+)"/gm)].map((m) => m[1]);
  for (const [name, bundle] of locales) {
    it(`names every event kind for the admin list in ${name}`, () => {
      const missing: string[] = [];
      for (const ev of [...allFromGo, 'admin_test', 'webhook_test']) {
        const key = `notifications.kinds.${ev.replace(/\./g, '_')}`;
        const label = lookup(bundle, key);
        if (!label || !label.trim() || label.trim() === ev) missing.push(`${ev} (${key})`);
      }
      expect(allFromGo.length, 'parsed no EventType constants at all').toBeGreaterThan(15);
      expect(missing, missing.join('\n')).toEqual([]);
    });
  }
});
