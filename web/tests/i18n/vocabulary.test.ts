// One term per concept — the machine-checkable half of the glossary in
// docs/CONTRIBUTING.md → "Words: one term per concept".
//
// ⚠ QA, 2026-09-21 (the v0.43.0 release-candidate sweep): the key a person
// creates for a device was "API anahtarı", "API jetonu" and "API token" on
// neighbouring screens; a share's PIN was "PIN" in the dialog that made it and
// "Kod" on the page that asks for it; "Giriş" meant both Home and sign in;
// the owner filter said "Kişiler" over a column that said "Sahibi"; a storage
// was a "depo", a "disk" and "Tüm disk" on one form; "Şifre" and "Parola" sat
// in one panel. Every one of those is a sentence a translator copied, so a
// rule that lives only in a document is broken by the next string.
//
// Every catalogue is read — explorer (packages/core), admin panel (web) and
// the server's `server.*` text — through the loader the language-pack tools
// use, so a new table cannot hide from it.
//
// ⚠ Partial by nature. A banned WORD can be caught; a Turkish sentence in the
// "sen" form mostly cannot (a possessive -n is a genitive -n to a regex), so
// the register rule catches the pronoun, the second-person verb endings and a
// sentence ending in a bare command — the shapes the sweep found — and the
// rest is the review the glossary asks for.
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { loadCatalogue } from '../../../scripts/lib/i18n-catalogue.mjs';

const ROOT = path.resolve(__dirname, '../../..');
const cat = loadCatalogue(ROOT) as Record<string, Record<string, string>>;

const english: Record<string, string> = { ...cat.explorer, ...cat.admin, ...cat.server };
const turkish: Record<string, string> = { ...cat.explorerTr, ...cat.adminTr, ...cat.serverTr };

/**
 * Text that is not a word a person reads as ours: an HTTP header, an
 * environment variable, a `code span`, a URL, a `{placeholder}`. Stripped before matching.
 */
function readable(s: string): string {
  return s
    .replace(/`[^`]*`/g, ' ')
    .replace(/\bX-[A-Za-z-]+/g, ' ')
    .replace(/\b[A-Z][A-Z0-9]+_[A-Z0-9_]+\b/g, ' ')
    .replace(/https?:\/\/\S+/g, ' ')
    .replace(/\{[^{}]*\}/g, ' ');
}

interface Rule {
  /** What the concept is called instead. */
  use: string;
  wrong: RegExp;
  /** Keys where the word is somebody else's name, each with its reason. */
  except?: Record<string, string>;
}

const TOKEN_IS_THEIRS: Record<string, string> = {
  'authProviders.checks.endpoints.ok': 'the OIDC discovery document names its token endpoint; that is the protocol\'s word',
  'notifications.webhookToken': 'a webhook target\'s Bearer token is the receiving service\'s secret, pasted from there',
  'notifications.tokenHint': 'the global webhook\'s Bearer token (see notifications.webhookToken)',
  'notifications.tokenPlaceholder': 'the global webhook\'s Bearer token (see notifications.webhookToken)',
  'notifications.tokenSet': 'the global webhook\'s Bearer token (see notifications.webhookToken)',
  'notifications.tokenUnset': 'the global webhook\'s Bearer token (see notifications.webhookToken)',
  'webhooks.global.hint': 'the global webhook\'s Bearer token (see notifications.webhookToken)',
  'plugins.fields.token': 'a remote storage plugin\'s token is the plugin service\'s own secret',
  'plugins.remoteHint': 'a remote storage plugin\'s token is the plugin service\'s own secret',
};

/* The mapped drive a connection guide talks a person through â a letter in
   Windows Explorer, a volume under macOS Locations. That is a drive; a place
   filex keeps files is a storage (docs/CONTRIBUTING.md -> Words). */
const DRIVE_IS_A_MOUNT: Record<string, string> = {
  'conn.guide.webdav.summary': 'WebDAV mounts filex AS a drive on the person’s machine',
  'conn.guide.webdav.win.s1': 'the Windows menu item is called “Map network drive…”',
  'conn.guide.webdav.win.s2': 'a Windows drive letter',
  'conn.guide.webdav.win.regCaption': 'the mapped drive, reconnected',
  'conn.guide.webdav.win.persist': 'the mapped drive and Credential Manager',
  'conn.guide.webdav.mac.note': 'the mounted volume as macOS shows it',
  'conn.guide.nfs.summary': 'NFS mounts filex AS a drive on another machine',
  'conn.guide.nfs.win.s3': 'a Windows drive letter',
  'conn.guide.mount.win.mountCaption': 'filex mount attaches a Windows drive',
  'conn.guide.mount.win.freeLetter': 'a Windows drive letter',
  'conn.guide.mount.win.stop': 'the mounted drive disappears',
  'conn.guide.mount.mac.alternatives': 'clients that mount drives on macOS',
  'conn.nfs.lead': 'an export mounts filex AS a drive on a machine in the network',
  'conn.tokens.revokeHint': 'a mounted drive stops working (the Turkish says sürücü for “mount”)',
  'demo.features.desktopBody': '“filex mount” attaches a Windows drive',
  'demo.subtitle': '“AI agents can drive it natively” — the verb, not a disk',
};

/* Somebody else's word for what filex calls a permission: an OIDC provider's
   `scope` parameter is part of the protocol and is typed into a config box. */
const SCOPE_IS_THEIRS: Record<string, string> = {
  'authProviders.fields.scopes': 'the extra OIDC scopes sent to the identity provider — the protocol’s word',
  'authProviders.fields.scopesHint': 'the extra OIDC scopes sent to the identity provider',
};

const ENGLISH: Rule[] = [
  { use: 'API key', wrong: /\bAPI tokens?\b/i },
  { use: 'API key', wrong: /\btokens?\b/i, except: TOKEN_IS_THEIRS },
  { use: 'sign in / sign-in', wrong: /\blog ?in\b|\blogins?\b|\blogs in\b|\blogged in\b/i },
  { use: 'username', wrong: /\blogin name\b/i },
  { use: 'storage', wrong: /\bfull disk\b/i },
  { use: 'sign out', wrong: /\blog ?out\b|\blogged out\b/i },
  { use: 'Customize (American English)', wrong: /\bcustomis(e|ed|es|ing|ation)\b/i },
  /* ⚠ American English everywhere, and the British spellings survived the
     v0.43.0 sweep in five places: "Colour palette", "your own colours", "the
     colour palette", "which colours" and macFUSE's "licence". A product that
     spells one word two ways is a product a translator has to guess about. */
  {
    use: 'American English: color, license, favorite, center, gray, behavior, organize, analyze, catalog, defense',
    wrong:
      /\b(colour|colours|coloured|colourful|licence|licences|licenced|favourite|favourites|centre|centres|centred|grey|greyed|behaviour|behaviours|organis(e|ed|es|ing|ation|ations)|analys(e|ed|es|ing)|catalogue|catalogues|defence|programme|programmes|whilst|amongst|fulfil|travelled|traveller|labelled|modelling)\b/i,
  },
  { use: 'storage (a drive is a letter a connection guide maps)', wrong: /\bdrives?\b/i, except: DRIVE_IS_A_MOUNT },
  { use: 'permission (what an API key may do)', wrong: /\bscopes?\b/i, except: SCOPE_IS_THEIRS },
  { use: 'what the preview can show — not the library behind it', wrong: /model-viewer/i },
  { use: 'email', wrong: /(?<![\w-])e-mails?\b/i },
  { use: 'Synchronous / Asynchronous (a write mode — "sync" is the scan)', wrong: /^(Sync|Async) \(|\bAsync\b/ },
];

/* ⚠ `\b` is an ASCII word boundary in JavaScript, even under `u`: before "ş"
   or "Ç" it never matches, and a rule written with it passes "Şifre" forever.
   `W` (not a letter before) and `E` (not a letter after) are the boundaries a
   Turkish word needs. */
const W = '(?<!\\p{L})';
const E = '(?!\\p{L})';
const tr = (src: string, flags = 'iu') => new RegExp(src.replaceAll('<', W).replaceAll('>', E), flags);

const TURKISH: Rule[] = [
  { use: 'API anahtarı', wrong: tr('<API (token|jeton)') },
  { use: 'API anahtarı', wrong: tr('<jeton') },
  { use: 'API anahtarı', wrong: tr('<token'), except: TOKEN_IS_THEIRS },
  { use: 'parola', wrong: tr('<şifre(yi|si|sini|siyle|niz|nizi|nizle|n|)>') },
  { use: 'ad', wrong: tr('<[iİ](sim|smi|sme|simler|simleri|simli)>') },
  { use: 'repo (a code repository is not a storage)', wrong: tr('GitHub depo') },
  { use: 'oturumu kapat', wrong: tr('<çıkış yap') },
  { use: 'Çöp kutusu', wrong: tr('Çöp Kutusu|<çöpün(e|de|den)?>|<çöpü>', 'u') },
  { use: 'Ana sayfa (Home)', wrong: /^Giriş$/u },
  { use: 'oturum aç', wrong: tr('<giriş yap|<(son|başarısız|sso|yönetici|kurtarma) giriş|<girişte>|<girişine>') },
  { use: 'kullanıcı adı', wrong: tr('<giriş ad') },
  { use: 'bağlantı', wrong: tr('<link') },
  { use: 'filtre', wrong: tr('<süzgeç|<süzer>|<süzebilir') },
  { use: 'depo', wrong: tr('<tüm disk|<bu diskte|<disklerde>') },
  { use: 'kova', wrong: tr('<bucket') },
  { use: 'önizlemenin gösterebildiği — arkasındaki kütüphanenin adı değil', wrong: /model-viewer/i },
  { use: 'e-posta', wrong: tr('<(e-mail|email|mail)>') },
  { use: 'izin (API anahtarının yapabildikleri)', wrong: tr('<scope'), except: SCOPE_IS_THEIRS },
  /* ⚠ "sürücü" is NOT banned in Turkish the way "drive" is in English: it
     is also the word for a storage DRIVER (`storages.driverLabel`,
     `plugins.fields.driver`, "6 depolama sürücüsü") and for a mapped
     Windows drive, and a regex cannot tell the three apart. The Turkish
     side of this concept is held by the `one concept, one label` group
     below (`destpicker.drives` = `sidenav.storages` = `storages.title`). */
  /* Two concepts, two words (docs/CONTRIBUTING.md → Words): the SERVER's scan
     of a storage is "senkron"; the desktop keeping folders on a computer is
     "eşitleme"; a replica's write mode is "eşzamanlı / eşzamansız". */
  {
    use: 'klasör eşitleme (the desktop keeps a copy) — senkron is the server\'s scan',
    wrong: tr('<klasör senkron|<çift yönlü senkron|<senkron araçları|<senkronizasyon'),
  },
  { use: 'eşzamanlı / eşzamansız (a write mode)', wrong: tr('<asenkron|^senkron \\(') },
];

/**
 * The "sen" form in a sentence addressed to the reader — the pronoun, a
 * second-person ending on a verb, the "sen" question particle and conditional,
 * and a sentence that ends in a bare command ("Tekrar dene."). A command LABEL
 * ("Kaydet", "Yeni sekmede aç") is the bare verb in every Turkish interface
 * and is not matched: it is not a sentence.
 */
const SEN: Rule[] = [
  { use: 'siz (the pronoun)', wrong: tr('<(sen|seni|sana|senin|seninle|sende|senden)>') },
  {
    use: 'siz (a second-person verb: -ebilirsiniz, -ırsınız…)',
    wrong: tr('\\p{L}(abilir|ebilir|ır|ir|ur|ür|ar|er|yor|malı|meli)(sın|sin|sun|sün)>'),
  },
  { use: 'siz (the question: "Emin misiniz?")', wrong: tr('<(mısın|misin|musun|müsün)>') },
  {
    use: 'siz (the conditional: -rsanız, -mezseniz…)',
    wrong: tr('\\p{L}(ır|ir|ur|ür|ar|er|maz|mez|acak|ecek|dı|di|du|dü|tı|ti|tu|tü)(san|sen)>'),
  },
  {
    use: 'siz (a possessive or participle the sweep found: "aramanızla", "açtığınızda", "yetkiniz")',
    wrong: tr('<(aramanla|uygulamandaki|uygulamanla|cihazına|bulunduğun|açtığın|yazdığında|istediğini|hesabınla)>|<yetkin (yok|olmalı)|^Lütfen .* et$'),
  },
  {
    use: 'siz (a sentence that ends in a bare command: "Tekrar deneyin.")',
    wrong: tr('\\p{L}\\s+(dene|seç|gir|tıkla|kullan|ekle|sor|geç|yap|bırak|oluştur|kopyala|sakla|tara|bak|dön|al)\\s*[.!]'),
  },
];

function offenders(table: Record<string, string>, rules: Rule[]): string[] {
  const out: string[] = [];
  for (const [key, value] of Object.entries(table)) {
    const text = readable(value);
    for (const r of rules) {
      if (r.except && key in r.except) continue;
      if (r.wrong.test(text)) out.push(`${key}: "${value}" — use "${r.use}"`);
    }
  }
  return out;
}

describe('one term per concept (docs/CONTRIBUTING.md → Words)', () => {
  it('reads every table — a scan of nothing would pass everything', () => {
    expect(Object.keys(english).length).toBeGreaterThan(3000);
    expect(Object.keys(turkish).length).toBeGreaterThan(3000);
    expect(Object.keys(english).some((k) => k.startsWith('server.'))).toBe(true);
  });

  it('English uses the glossary\'s words', () => {
    expect(offenders(english, ENGLISH)).toEqual([]);
  });

  it('Turkish uses the glossary\'s words', () => {
    expect(offenders(turkish, TURKISH)).toEqual([]);
  });

  it('Turkish addresses the reader as "siz"', () => {
    expect(offenders(turkish, SEN)).toEqual([]);
  });

  it('the register rules still see the "sen" form (a detector that finds nothing proves nothing)', () => {
    const t = { a: 'Bir şeyler ters gitti. Tekrar dene.', b: 'Yalnız sen görürsün', c: 'Emin misin?', d: 'Boş bırakırsan korunur' };
    expect(offenders(t, SEN).length).toBeGreaterThanOrEqual(4);
    expect(offenders({ ok: 'Kaydet', ok2: 'Tekrar deneyin.', ok3: 'Emin misiniz?' }, SEN)).toEqual([]);
  });

  it('an exception names a key that exists — a stale exemption hides the next copy', () => {
    const stale = Object.keys(TOKEN_IS_THEIRS).filter((k) => !(k in english));
    expect(stale).toEqual([]);
  });
});

/**
 * One concept, one label, on every surface that names it. The column, the
 * filter over it, the sort by it and the details panel's row are ONE thing.
 */
const SAME: Array<[string, string[]]> = [
  ['modified', ['col.modified', 'filter.modified', 'inspector.modified', 'explore.cols.modified', 'settings.folderView.col_modified', 'settings.folderView.sort_modified', 'userSettings.prefs.fvSort_modified']],
  ['owner', ['col.owner', 'filter.people', 'settings.folderView.col_owner']],
  ['trash', ['node.trash', 'sidenav.trash', 'nav.trash', 'trash.title']],
  ['home', ['node.home', 'sidenav.home', 'home.title', 'userSettings.prefs.startPage_home']],
  ['API keys', ['sidenav.apikeys', 'conn.tokens.title', 'explore.apiKeys']],
  ['PIN', ['plugin.pin.label', 'myShares.fields.pin', 'server.public.pin_aria']],
  /* What an API key may do: the fieldset the boxes sit in, the column that
     lists them afterwards, and the same pair in the admin panel. Said four
     ways before v0.43.0 — "Scopes", "Permissions", "Can do", "Yetkisi". */
  ['API key permissions', ['conn.tokens.scopes', 'conn.tokens.col.scopes', 'apiMcp.fields.scopes', 'apiMcp.cols.scopes']],
  /* The top of the file tree: every storage the person can open.
     ⚠ `conn.tab.storages` was the fifth member and is gone with the tab it
     named (v0.43.0 — the connections surface has no storage half any more).
     The four survivors are the four places the word still appears. */
  ['storages', ['sidenav.storages', 'storages.title', 'nav.storages', 'destpicker.drives']],
];

describe('one concept, one label', () => {
  for (const [concept, keys] of SAME) {
    it(`${concept}: ${keys.join(' = ')}`, () => {
      for (const [lang, table] of [['en', english], ['tr', turkish]] as const) {
        const seen = keys.map((k) => table[k]);
        expect(seen.every((v) => typeof v === 'string'), `${lang}: a key is missing — ${keys.join(', ')}`).toBe(true);
        expect(new Set(seen).size, `${lang}: ${keys.map((k, i) => `${k}="${seen[i]}"`).join(', ')}`).toBe(1);
      }
    });
  }
});
