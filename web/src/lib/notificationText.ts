/**
 * The sentence a notification says — composed by the READER, not by the sender.
 *
 * ⚠⚠ Why this file exists. A notification row is written once, on the server,
 * by whichever request happened to cause it. Its `title`/`body` are therefore
 * in ONE language — and eight of the eleven file events never set a title at
 * all, so `notify.Service.Send` substitutes the event id and the person is
 * shown `share.created`. Measured on 2026-09-12: a browser notification came
 * out as `{title: "file.uploaded", body: "/Documents/measure-me.txt"}`. That is
 * not a missing translation; it is the wire format leaking onto somebody's
 * screen.
 *
 * Two people with different locales can read the same row, so the row cannot
 * carry the sentence. What it carries is the FACTS — `event`, `meta` (which
 * nests `node`, `actor`, `share` and `target`) — and each surface turns those
 * into its own reader's language, here.
 *
 * ⚠ The server's `title`/`body` are NOT removed and NOT changed. They stay the
 * fallback for every consumer that has no catalogue: the webhook payload, the
 * admin audit table, anything reading `GET /api/notifications` directly. This
 * module is what the three surfaces WITH a reader use.
 *
 * ⚠⚠ THREE SURFACES, ONE MODULE — that is the whole point, and it is why this
 * file is plain TypeScript with no framework in it:
 *
 *   • the bell list            (web/src/components/NotificationBell.vue)
 *   • the browser notification (web/src/composables/useNotificationWatcher.ts)
 *   • the native OS one        (desktop/src/notifications.ts → main.ts)
 *
 * The desktop shell's main process cannot run vue-i18n and does not load the
 * SPA's locale JSON (its own tray catalogue is an inline `[en, tr]` table for
 * exactly that reason), so the phrasing lives here as data rather than in
 * `locales/*.json`. It is imported across the package boundary the same way
 * `notificationTarget.ts` already is — the precedent that made "one resolver,
 * three surfaces" a fact rather than a promise. Split the catalogue in two and
 * the bell and the OS toast start saying different things about one event.
 *
 * ⚠ The switch labels in the settings dialog (`userSettings.notifications.
 * events.*`) are a DIFFERENT catalogue and stay in `locales/*.json`: they name
 * a class of event for someone choosing what to hear about ("A new file
 * arrives"), while these describe one thing that happened ("New file:
 * report.pdf"). `web/tests/webhooks/eventCatalog.test.ts` gates both against
 * the Go event list, so neither can go missing.
 */

/** The only two languages filex ships. */
export type NotifyLocale = 'en' | 'tr';

/** One rendered notification. */
export interface NotificationText {
  title: string;
  body: string;
}

/**
 * A phrase template pair. `{placeholders}` are filled from the row's metadata.
 *
 * `one` is the singular variant, used when `meta.count === 1`. Only English
 * needs it (Turkish does not inflect a noun after a number — "3 dosya"), so it
 * is optional.
 */
interface Phrase {
  title: string;
  body: string;
  one?: { title?: string; body?: string };
}

/**
 * What each event says, per language.
 *
 * ⚠⚠ Every placeholder below is a field the emitter ACTUALLY sets — surveyed
 * against the emit sites, not guessed. Fields that would have read better and
 * are NOT available are listed at the bottom of this file rather than invented.
 *
 * The title answers "what happened, to what"; the body carries the location or
 * the detail. A bell row shows both, an OS toast shows both, and neither may
 * depend on the other to make sense.
 */
export const NOTIFICATION_PHRASES: Record<string, Record<NotifyLocale, Phrase>> = {
  // writehook: meta.node.{name,path,size}, meta.origin
  'file.uploaded': {
    en: { title: 'New file: {name}', body: '{path}' },
    tr: { title: 'Yeni dosya: {name}', body: '{path}' },
  },
  'file.updated': {
    en: { title: 'File changed: {name}', body: '{path}' },
    tr: { title: 'Dosya değişti: {name}', body: '{path}' },
  },
  // meta.reason is the driver's error text — the only thing that says WHY.
  'file.upload_failed': {
    en: { title: 'Upload failed: {name}', body: '{reason}' },
    tr: { title: 'Yükleme başarısız: {name}', body: '{reason}' },
  },
  // meta.signature is the ClamAV signature name; the path is the ORIGINAL one.
  'file.infected': {
    en: { title: 'Virus found in {name}', body: '{signature} — {path}' },
    tr: { title: '{name} dosyasında virüs bulundu', body: '{signature} — {path}' },
  },
  'file.deleted': {
    en: { title: 'File deleted: {name}', body: '{path}' },
    tr: { title: 'Dosya silindi: {name}', body: '{path}' },
  },
  'file.trashed': {
    en: { title: 'Moved to trash: {name}', body: '{path}' },
    tr: { title: 'Çöp kutusuna taşındı: {name}', body: '{path}' },
  },
  // meta.from / meta.to are both set by OnFileMoved — the one event where the
  // body can show the actual change rather than repeat the title.
  'file.moved': {
    en: { title: 'Moved: {name}', body: '{from} → {to}' },
    tr: { title: 'Taşındı: {name}', body: '{from} → {to}' },
  },
  // ⚠ node is OPTIONAL here (share.go sets it only when the row resolved), so
  // the title must stand on its own with an empty body.
  'share.created': {
    en: { title: 'Share link created', body: '{path}' },
    tr: { title: 'Paylaşım bağlantısı oluşturuldu', body: '{path}' },
  },
  // meta.{folder,count,uploader}; uploader is "" when the visitor typed no name.
  'drop.received': {
    en: {
      title: '{count} files received',
      body: '{uploader} → {folder}',
      one: { title: '1 file received' },
    },
    tr: { title: '{count} dosya geldi', body: '{uploader} → {folder}' },
  },
  // meta.body is the comment, truncated to 200 chars by the emitter.
  'comment.added': {
    en: { title: 'New comment on {name}', body: '{body}' },
    tr: { title: '{name} için yeni yorum', body: '{body}' },
  },
  // ⚠⚠ The ESCROW key, never "the recovery key". They are two different keys
  // with opposite meanings for the person reading this: a recovery key is the
  // one the OWNER was handed when the folder was made, and escrow is the
  // OPERATOR's key being used on their folder. This read "recovery" for a
  // release, in both languages, while the server's own title said escrow —
  // telling someone the reverse of what happened to their data. The words are
  // the explorer's own (`e2e.recover.tab_escrow`: "Escrow key" / "Emanet
  // anahtarı"); web/tests/lib/notificationText.test.ts holds them together.
  'e2e.escrow_used': {
    en: { title: 'Encrypted folder opened with the escrow key', body: '{folder}' },
    tr: { title: 'Şifreli klasör emanet anahtarıyla açıldı', body: '{folder}' },
  },
};

/** Words the templates need that are not in the row. */
const WORDS: Record<NotifyLocale, { someone: string; unnamed: string }> = {
  en: { someone: 'Someone', unnamed: 'a file' },
  tr: { someone: 'Birisi', unnamed: 'bir dosya' },
};

/** The shape this module reads. Structural on purpose — the SPA passes a
 *  `NotificationItem`, the desktop passes its own `NotificationRow`, and
 *  neither package has to import the other's type. */
export interface NotificationLike {
  event: string;
  title?: string;
  body?: string;
  meta?: unknown;
  target?: { kind?: string; storage?: string; path?: string; id?: string } | null;
}

function asRecord(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

/** Last path segment. `Docs/a.pdf` → `a.pdf`; `` → ``. */
function baseName(p: string): string {
  const clean = p.replace(/[\\/]+$/, '');
  const i = Math.max(clean.lastIndexOf('/'), clean.lastIndexOf('\\'));
  return i >= 0 ? clean.slice(i + 1) : clean;
}

/**
 * The values a template may interpolate, resolved from the row.
 *
 * ⚠ Every one of these has a chain rather than a single source, because the
 * emitters are not uniform: `node.size` is absent on five events, `node` itself
 * is absent on `share.created` when the row did not resolve, and
 * `meta.trash_path` can be the empty string. A template must never render
 * `undefined`, a literal `{name}`, or an empty sentence fragment.
 */
export function notificationVars(
  row: NotificationLike,
  locale: NotifyLocale,
): Record<string, string> {
  const meta = asRecord(row.meta);
  const node = asRecord(meta.node);
  const target = row.target ?? (asRecord(meta.target) as NotificationLike['target']);
  const words = WORDS[locale];

  const path = str(node.path) || str(target?.path) || str(row.body);
  const name = str(node.name) || baseName(path) || words.unnamed;
  const uploader = str(meta.uploader).trim() || words.someone;
  const count = typeof meta.count === 'number' ? String(meta.count) : '';

  return {
    name,
    path,
    count,
    uploader,
    folder: str(meta.folder) || baseName(path) || path,
    storage: str(meta.storage) || str(target?.storage),
    reason: str(meta.reason) || str(row.body),
    signature: str(meta.signature),
    from: str(meta.from),
    to: str(meta.to) || path,
    // The comment excerpt. Collapsed to one line: a bell row and an OS toast
    // both clip, and a newline inside a toast body renders as a gap.
    body: str(meta.body).replace(/\s+/g, ' ').trim(),
    actor: str(asRecord(meta.actor).email),
  };
}

/**
 * Fill `{placeholders}`, then repair what the metadata could not supply.
 *
 * ⚠ An unresolved placeholder is deleted along with the punctuation that was
 * holding it: `"{signature} — {path}"` with no signature must read `"/a/b.txt"`,
 * not `"— /a/b.txt"`. This is the difference between a sentence with a missing
 * word and a sentence with a dangling dash, and only one of them looks like a
 * bug to the person reading it.
 */
export function fillTemplate(tpl: string, vars: Record<string, string>): string {
  const filled = tpl.replace(/\{(\w+)\}/g, (_m, k: string) => vars[k] ?? '');
  return filled
    .replace(/\s*(—|→|:)\s*$/g, '')
    .replace(/^\s*(—|→|:)\s*/g, '')
    .replace(/\s{2,}/g, ' ')
    .trim();
}

export interface RenderOptions {
  /**
   * The friendly per-event label from the settings dialog
   * (`userSettings.notifications.events.*`), supplied by the SPA as the
   * fallback for an event with no phrase yet.
   *
   * ⚠ The desktop shell passes nothing — it has no locale catalogue — and does
   * not need to: `eventCatalog.test.ts` fails the build when an event in the
   * Go list has no phrase, so this path is a seatbelt, not a route.
   */
  fallbackLabel?: string;
}

/**
 * Render one notification in the reader's language.
 *
 * The chain, in order, and every step exists because the one below it is worse:
 *
 *   1. the phrase for this event    — the answer
 *   2. the friendly switch label    — an event we know but have not phrased
 *   3. the server's own title       — a legacy operational alarm (`disk_full`,
 *      `update_available`, …) which is not in the subscribable catalogue and
 *      does carry a real, if English, sentence
 *   4. the raw event id             — last resort, and a bug if it is ever seen
 *
 * ⚠ Step 3 is skipped when the server title IS the event id, which is what
 * `notify.Service.Send` substitutes for the eight file events that set no
 * title. Taking it would put `share.created` back on somebody's screen.
 */
export function renderNotification(
  row: NotificationLike,
  locale: NotifyLocale,
  opts: RenderOptions = {},
): NotificationText {
  const vars = notificationVars(row, locale);
  const phrase = NOTIFICATION_PHRASES[row.event]?.[locale];

  if (phrase) {
    const singular = vars.count === '1' ? phrase.one : undefined;
    const title = fillTemplate(singular?.title ?? phrase.title, vars);
    const body = fillTemplate(singular?.body ?? phrase.body, vars);
    if (title) return { title, body };
  }

  const serverTitle = str(row.title).trim();
  const fallbackBody = vars.path || str(row.body);

  if (opts.fallbackLabel && opts.fallbackLabel.trim()) {
    return { title: opts.fallbackLabel.trim(), body: fallbackBody };
  }
  if (serverTitle && serverTitle !== row.event) {
    return { title: serverTitle, body: str(row.body) || fallbackBody };
  }
  return { title: row.event, body: fallbackBody };
}

/* ─────────────────────────────────────────────────────────────────────────
 * Fields the phrasing WANTED and the server does not send. Recorded rather
 * than invented — a sentence built on a field that is not there renders as a
 * hole, and the next person cannot tell a design choice from a bug.
 *
 *  • A person's NAME. Every actor-bearing event carries `meta.actor.email`
 *    and nothing else, so "Ayşe shared a folder" is not available. An e-mail
 *    address is not a substitute: it is an identity, and a toast that can be
 *    read over somebody's shoulder is the wrong place to print one. Titles
 *    therefore say what happened, never who did it.
 *  • `share.created` carries no expiry, no download cap and no URL — only
 *    `kind` ("download"/"drop") and `has_pin`. "Share link created" is as far
 *    as the data goes.
 *  • `file.deleted` / `file.trashed` / `file.moved` / `file.upload_failed`
 *    carry no `size` (the emitter fills `NodeRef.Size` only on write events),
 *    so none of these can say how much was removed.
 *  • `comment.added` carries no author name and no node id — only
 *    `comment_id` and the excerpt.
 *  • `drop.received` carries `submission` (the timestamped subfolder) but no
 *    file names; the count is all that can be said about what arrived.
 *  • `file.moved` carries `rename: true` ONLY when the rename came from the
 *    browser (manager_mutate.go); the same rename through AI, WebDAV or the
 *    ops worker is indistinguishable from a move, so the phrasing does not
 *    split the two.
 * ───────────────────────────────────────────────────────────────────────── */
