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
      one: { title: '{count} file received' },
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
  // An installed app speaking through notify_send. The plugin phrased the
  // text itself, per language: meta.title_en/title_tr + body_en/body_tr
  // (the row's own title is the English one). `{plugin}` is the app's LABEL
  // in the reader's language (meta.plugin_label_en/_tr), not its install id.
  'plugin.notice': {
    en: { title: '{notice_title}', body: '{plugin}: {notice_body}' },
    tr: { title: '{notice_title}', body: '{plugin}: {notice_body}' },
  },

  // ── Operational alarms ──────────────────────────────────────────────────
  // Not in the subscribable catalogue, and the server writes their title in
  // English (`filex 0.42.0 available`), so a Turkish panel showed English
  // until they were phrased here too. Each placeholder is a Meta field of the
  // one emit site named beside it.
  //
  // server.go OnNewRelease: meta.{version,current}
  update_available: {
    en: { title: 'filex {version} is available', body: 'This server runs {current}.' },
    tr: { title: 'filex {version} yayınlandı', body: 'Bu sunucu {current} sürümünde çalışıyor.' },
  },
  // replica/recorder.go NotifyReplicaFail: meta.{path,op,error}
  replica_fail: {
    en: { title: 'Replica {op} failed: {name}', body: '{error}' },
    tr: { title: 'Kopyada {op} başarısız: {name}', body: '{error}' },
  },
  // replica/recorder.go NotifyPrimaryReadFail: meta.{path,primary_error}
  primary_read_fail: {
    en: { title: '{name} was served from the replica', body: 'The primary storage failed: {error}' },
    tr: { title: '{name} kopyadan sunuldu', body: 'Birincil depo hata verdi: {error}' },
  },
  // replica/reconcile.go: meta.queued
  replica_reconcile_done: {
    en: {
      title: '{count} replica retries queued',
      body: 'Progress is on the queue page.',
      one: { title: '{count} replica retry queued' },
    },
    tr: { title: '{count} kopya yeniden denemesi kuyruğa alındı', body: 'İlerleme kuyruk sayfasında.' },
  },
  // replica/reconcile.go cron report: meta.{failed_count,repaired_count}
  replica_status_report: {
    en: { title: 'Replica report: {failed} unresolved, {repaired} repaired', body: 'Last 24 hours' },
    tr: { title: 'Kopya raporu: {failed} çözülmemiş, {repaired} onarıldı', body: 'Son 24 saat' },
  },
};

/**
 * Words the templates need that are not in the row.
 *
 * `openWith` stands where a path would: a document edited through the desktop
 * app's "open with filex" lives on the person's own computer, so the row
 * (`meta.open_with`, the server's personview.go) carries its NAME and no path
 * at all — no storage path names it. This says where it is instead, rather
 * than leaving an empty line under the title.
 */
const WORDS: Record<NotifyLocale, { someone: string; unnamed: string; appNotice: string; openWith: string }> = {
  en: { someone: 'Someone', unnamed: 'a file', appNotice: 'App notification', openWith: 'Opened with the filex desktop app' },
  tr: { someone: 'Birisi', unnamed: 'bir dosya', appNotice: 'Uygulama bildirimi', openWith: 'filex masaüstü uygulamasıyla açıldı' },
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
  strings?: Record<string, string>,
  /** The reader's own language tag (`de`, `pt-br`) when it is neither of the
   *  two built-in tables — what an app's notice is looked up in first. */
  lang?: string,
): Record<string, string> {
  const meta = asRecord(row.meta);
  const node = asRecord(meta.node);
  const target = row.target ?? (asRecord(meta.target) as NotificationLike['target']);
  const words = packWords(WORDS[locale], strings);

  // meta.path is the replica alarms' own field; they carry no node.
  // ⚠ An open-with row has no path by design (see WORDS.openWith). Checked
  // FIRST so the fallbacks below never get to it: the body is the last of them,
  // and a body is whatever the emitter happened to write there.
  const path =
    meta.open_with === true
      ? words.openWith
      : str(node.path) || str(meta.path) || str(target?.path) || str(row.body);
  const name = str(node.name) || baseName(path) || words.unnamed;
  const uploader = str(meta.uploader).trim() || words.someone;
  const num = (v: unknown) => (typeof v === 'number' ? String(v) : '');
  const count = num(meta.count) || num(meta.queued);

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
    version: str(meta.version),
    current: str(meta.current),
    op: str(meta.op),
    error: str(meta.error) || str(meta.primary_error),
    failed: num(meta.failed_count),
    repaired: num(meta.repaired_count),
    // plugin.notice: the app's own wording in the reader's language, falling
    // back to the English the server stored in the row itself.
    //
    // ⚠ `plugin` is the app's LABEL in the reader's language, never
    // `meta.plugin` — that is the install id, and it was printed: "sign:
    // “sözleşme.pdf” imzanızı bekliyor…" in a bell whose side panel calls the
    // app "İmzalar" (release-candidate sweep, 2026-09-21). A row from before
    // the server sent labels has none, and then the prefix is left out
    // (fillTemplate drops the dangling ':') rather than showing the id.
    plugin: noticeText(meta, 'plugin_label_', lang, locale),
    notice_title:
      noticeText(meta, 'title_', lang, locale) ||
      (str(row.title) !== row.event ? str(row.title) : '') || words.appNotice,
    notice_body: noticeText(meta, 'body_', lang, locale) || str(row.body),
  };
}

/**
 * One of an app notice's per-language texts (`<prefix><lang>`), in the
 * reader's language: their own tag, its base language (`pt` for `pt-br`), the
 * built-in table's language, then English.
 *
 * ⚠⚠ The reader's OWN language first, not `locale`. `locale` is which of the
 * two built-in tables this renderer falls back on, so a German reader has
 * `locale: 'en'` — and read the e-Signature app's English under "e-Signature:"
 * while the app had written its notice in German too (2026-09-22). The server
 * keeps a text per language the app wrote (wasmplugin `noticeMeta`).
 */
function noticeText(meta: Record<string, unknown>, prefix: string, lang: string | undefined, locale: NotifyLocale): string {
  const tags: string[] = [];
  const own = (lang ?? '').trim().toLowerCase();
  if (own) {
    tags.push(own);
    const dash = own.indexOf('-');
    if (dash > 0) tags.push(own.slice(0, dash));
  }
  tags.push(locale, 'en');
  for (const tag of tags) {
    const v = str(meta[prefix + tag]);
    if (v) return v;
  }
  return '';
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
  /**
   * A language pack's strings for the reader's language — the `server.
   * notify.*` keys are read (`<event>.title` / `.body`, a plural form as
   * `.title_<category>`, `word.<name>`); anything else is ignored.
   *
   * ⚠⚠ Why this exists. `locale` above is the BUILT-IN table to fall back
   * on, and there are two; a Spanish reader got the English one, so the bell
   * of a Spanish screen spoke English (measured 2026-09-22). The phrases here
   * are the English and Turkish of the server catalogue's `server.notify.*`
   * keys (scripts/lib/i18n-catalogue.mjs reads them out of this file), a
   * pack translates them like any key, and a key the pack lacks falls back
   * to this table — per key.
   */
  strings?: Record<string, string>;
  /** The reader's language tag, for the pack's plural forms (Intl.PluralRules). */
  lang?: string;
  /**
   * ⚠⚠ Machine runs inside the finished sentence, isolated for the reader's
   * direction — core `foreignText` / `useLocale().t.foreign`.
   *
   * A notification is composed HERE, out of a phrase table and a row's own
   * fields: it never passes through `useLocale().t` or the admin panel's
   * post-translation hook, so nothing isolated it. Nearly every body IS
   * machine text — a path, a reason, an error, an app's own words — and in an
   * Arabic bell a path's trailing characters take the paragraph's direction
   * and jump to the far side of the line.
   *
   * A hook rather than an import: this module is plain TypeScript with no
   * dependencies because the DESKTOP shell's main process renders from it too
   * (it has no catalogue and no packages/core), and its toasts are laid out
   * by the OS. A caller that passes nothing gets exactly what it got before.
   */
  foreign?: (text: string) => string;
}

const NOTIFY_KEY = 'server.notify.';

/** A pack's value for key, or undefined when it has none. */
function packValue(strings: Record<string, string> | undefined, key: string): string | undefined {
  const v = strings?.[key];
  return typeof v === 'string' && v.trim() ? v : undefined;
}

/** WORDS with the pack's `server.notify.word.*` laid over, per word. */
function packWords<T extends Record<string, string>>(base: T, strings?: Record<string, string>): T {
  if (!strings) return base;
  const out = { ...base };
  for (const k of Object.keys(base) as Array<keyof T & string>) {
    const v = packValue(strings, `${NOTIFY_KEY}word.${k}`);
    if (v) out[k] = v as T[typeof k];
  }
  return out;
}

/**
 * The CLDR plural category of count in lang — the rule the explorer, the admin
 * panel and the server all pick forms by. ⚠ Guarded: an invalid tag, or a
 * runtime without Intl.PluralRules, must not take the bell down.
 */
function pluralCategory(lang: string | undefined, count: string): string {
  if (count === '') return 'other';
  try {
    return new Intl.PluralRules(lang || 'en').select(Number(count));
  } catch {
    return count === '1' ? 'one' : 'other';
  }
}

/** The pack's form of one phrase field: `<field>_<category>`, then `<field>`. */
function packField(opts: RenderOptions, event: string, field: 'title' | 'body', count: string): string | undefined {
  if (!opts.strings) return undefined;
  const base = `${NOTIFY_KEY}${event}.${field}`;
  const cat = pluralCategory(opts.lang, count);
  return (cat !== 'other' && packValue(opts.strings, `${base}_${cat}`)) || packValue(opts.strings, base);
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
  const vars = notificationVars(row, locale, opts.strings, opts.lang);
  const phrase = NOTIFICATION_PHRASES[row.event]?.[locale];
  const packTitle = packField(opts, row.event, 'title', vars.count);
  const packBody = packField(opts, row.event, 'body', vars.count);
  /** The reader's direction, applied once to whatever this ends up saying. */
  const say = (text: string): NotificationText['title'] =>
    typeof opts.foreign === 'function' ? opts.foreign(text) : text;
  const said = (title: string, body: string): NotificationText => ({ title: say(title), body: say(body) });

  if (phrase || packTitle) {
    const singular = vars.count === '1' ? phrase?.one : undefined;
    const title = fillTemplate(packTitle ?? singular?.title ?? phrase?.title ?? '', vars);
    const body = fillTemplate(packBody ?? singular?.body ?? phrase?.body ?? '', vars);
    if (title) return said(title, body);
  }

  const serverTitle = str(row.title).trim();
  const fallbackBody = vars.path || str(row.body);

  if (opts.fallbackLabel && opts.fallbackLabel.trim()) {
    return said(opts.fallbackLabel.trim(), fallbackBody);
  }
  if (serverTitle && serverTitle !== row.event) {
    return said(serverTitle, str(row.body) || fallbackBody);
  }
  return said(row.event, fallbackBody);
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
