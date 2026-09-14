<script setup lang="ts">
/**
 * Access modal — "Share / Permissions", the popup behind the explorer's
 * `access` action.
 *
 * gorunum:v2-share — the LOOK changed, the capabilities did not.
 *
 * It used to open as three peer tabs (People · Link · Request files), each a
 * grid of controls competing for the first glance. Nine times in ten the
 * person who opened it wants one thing: turn the link on and copy it. So the
 * dialog now leads with exactly that — one switch, one plain sentence saying
 * who can open the item right now, and the link with its Copy button — and
 * everything else sits underneath in a **named** second tier:
 *
 *   · "Bağlantı seçenekleri" — PIN, expiry, download cap, the one-line curl,
 *     e-mail delivery, the OS share sheet, and every existing download link.
 *   · "Erişimi olanlar"      — the per-user/per-group grants (owner only).
 *   · "Dosya İste"           — the inbound drop link (folders only).
 *
 * ⚠ Second tier, not "Advanced": each section says what is in it and carries a
 * one-glance summary of its own state ("PIN · 7 gün · 3 indirme"), because a
 * control nobody can find is a control that does not exist. `initialTab` still
 * decides which section opens with the dialog, so "Request files" still lands
 * on the drop link.
 *
 * ⚠ No `<style>` block: the CSS lives in `styles/base.css`
 * (`gorunum:v2-share`). A scoped style block here is silently dropped in the
 * web-component build — see web/tests/api/scopedStyles.test.ts.
 */
import { ref, onMounted, onBeforeUnmount, computed, inject, watch } from 'vue';
import type { FileApi, Grant, UserSuggestion } from '../composables/useFileApi';
import type { ShareInfo } from '../types/FileNode';
import { shareCliCommand } from '../lib/shareCli';
import {
  STOCK_EXPIRY_DAYS,
  clampExpiryOptions,
  defaultExpiryDays,
  shareDetailLine,
  ttlCeilingHint,
  validUntilLine,
} from '../lib/shareTtl';
import { EXPLORER_CLOCK } from '../lib/timezone';
import { resolveLocale } from '../locales/resolve';
import { formatByteSize, useLocale } from '../composables/useLocale';
import { actionIconSvg } from '../lib/actionIcons';
import { fileIconTile } from '../lib/fileIcons';

const props = defineProps<{
  api: FileApi;
  path: string; // adapter://rel of the target item
  isDir?: boolean; // folder → grants cascade; file → no `/…` inheritance hint
  size?: number; // bytes, for the share-mail body (files only)
  locale?: 'tr' | 'en';
  /** Server ceiling on a new link's life in days (capabilities.share_max_ttl_days).
   *  undefined/0 = no ceiling. The expiry choices are derived from it. */
  shareMaxTtlDays?: number;
  /**
   * surucu:d1 — which part of the dialog the caller wants opened. Absent =
   * 'perms', the behaviour every existing caller has.
   *
   * The drive shell's "Request files" needs 'drop': the file-drop link lives
   * in this dialog, and a menu entry that opened it anywhere else would be a
   * menu entry that lied about what it does.
   *
   * ⚠ A hint, not a lock: `load()` still falls back to the link tier when the
   * caller turns out not to be an owner, because the grants section is not
   * theirs to see.
   */
  initialTab?: 'perms' | 'share' | 'drop';
}>();
const emit = defineEmits<{ (e: 'close'): void }>();

const localeCode = computed(() => resolveLocale(props.locale));
const { t } = useLocale(() => localeCode.value);
const tr = computed(() => localeCode.value !== 'en');
function L(t: string, e: string): string {
  return tr.value ? t : e;
}

// Split adapter://rel for a friendlier path chip.
const pathParts = computed(() => {
  const m = /^([^:]+):\/\/(.*)$/.exec(props.path);
  const adapter = m ? m[1] : '';
  const rel = m ? m[2] : props.path;
  const segs = rel.split('/').filter(Boolean);
  return { adapter, name: segs.length ? segs[segs.length - 1] : adapter, rel };
});
// The type tile beside the title — the same badge the listing draws, so the
// dialog is visibly about the row the user right-clicked.
const titleTile = computed(() => {
  const name = pathParts.value.name;
  const dot = name.lastIndexOf('.');
  return fileIconTile({
    type: props.isDir ? 'dir' : 'file',
    extension: !props.isDir && dot > 0 ? name.slice(dot + 1) : '',
  });
});

type Section = 'link' | 'people' | 'drop';
const open = ref<Record<Section, boolean>>({ link: false, people: false, drop: false });
function toggleSection(s: Section) {
  open.value = { ...open.value, [s]: !open.value[s] };
}
const canManage = ref(false); // owner/admin → can see the grants section

// ── permissions state ──
const loading = ref(true);
const err = ref('');
const direct = ref<Grant[]>([]);
const inherited = ref<Grant[]>([]);
const storageRbac = ref(true);
const email = ref('');
const level = ref<'viewer' | 'editor' | 'owner'>('viewer');
const busy = ref(false);
const notice = ref('');
const noAccount = ref(false);
const createRole = ref<'user' | 'viewer'>('user');
const inviteResult = ref<{ tempPassword?: string } | null>(null);
const suggestions = ref<UserSuggestion[]>([]);
const showSuggest = ref(false);
let searchTimer: ReturnType<typeof setTimeout> | undefined;

const levels: Array<{ v: 'viewer' | 'editor' | 'owner'; l: string; d: string }> = [
  { v: 'viewer', l: L('Görüntüleyen', 'Viewer'), d: L('görüntüle + indir', 'view + download') },
  { v: 'editor', l: L('Düzenleyen', 'Editor'), d: L('oku + yaz + sil', 'read + write + delete') },
  { v: 'owner', l: L('Sahip', 'Owner'), d: L('düzenle + izin yönet', 'edit + manage access') },
];
function levelLabel(v: string): string {
  return levels.find((o) => o.v === v)?.l ?? v;
}

// ── share state ──
const shares = ref<ShareInfo[]>([]);
const shareBusy = ref(false);
const sharePwd = ref(false);
const shareExpiry = ref(defaultExpiryDays(props.shareMaxTtlDays)); // days; 0 = never
const shareResult = ref<{ url: string; pin?: string | null; expiresAt?: string | null; clamped?: boolean } | null>(null);
const shareErr = ref('');
const copied = ref('');
// prefilled recipient when the owner chose "share link" for a no-account email
const shareMailTo = ref('');
const shareMailBusy = ref(false);
const shareMailNotice = ref('');

/**
 * ⚠ `listShares` returns BOTH kinds of link — a download link (`/s/…`) and a
 * file-drop link (`/d/…`) are the same row with a different `kind`. They used
 * to be listed together under "Existing links", which meant an upload link
 * appeared in the download tab and the "link sharing" state could not be read
 * off the list at all. Split at the source: each section owns its own kind.
 *
 * `kind` is absent from the `ShareInfo` type but present on the wire (see
 * backend/internal/api/handlers/share.go), so it is read through a narrow cast
 * rather than by widening a shared type this dialog does not own.
 */
function shareKind(s: ShareInfo): string {
  return (s as ShareInfo & { kind?: string }).kind ?? 'download';
}
const downloadShares = computed(() => shares.value.filter((s) => shareKind(s) !== 'drop'));
const dropShares = computed(() => shares.value.filter((s) => shareKind(s) === 'drop'));

// ⚠ Derived from the server's ceiling, never a fixed list: offering "30 days"
// on a server that keeps links for 7 would show a choice that is not one.
// The same helper drives every surface (see lib/shareTtl.ts).
function expiryLabel(days: number): string {
  if (days === 0) return L('Süresiz', 'Never');
  return tr.value ? `${days} gün` : `${days} day${days === 1 ? '' : 's'}`;
}
const expiryOptions = computed(() => clampExpiryOptions(STOCK_EXPIRY_DAYS, props.shareMaxTtlDays, expiryLabel));
const ttlHint = computed(() => ttlCeilingHint(props.shareMaxTtlDays, tr.value ? 'tr' : 'en'));
watch(
  () => props.shareMaxTtlDays,
  () => {
    // The ceiling can arrive after mount (capabilities load async): snap a
    // selection the server would not honour back to what it will.
    const allowed = expiryOptions.value.map((o) => o.v);
    if (!allowed.includes(shareExpiry.value)) shareExpiry.value = defaultExpiryDays(props.shareMaxTtlDays);
    if (!allowed.includes(dropExpiry.value)) dropExpiry.value = defaultExpiryDays(props.shareMaxTtlDays);
  },
);

// ⚠ The download cap lived in the old standalone ShareModal and was left
// behind when this panel took over link creation — the server has honoured
// `max_downloads` the whole time, there was simply no way to set it. A select
// rather than a number box: "let this be opened three times" is the whole use
// case, and typing a number to say it is friction.
const shareMaxDl = ref(0); // 0 = unlimited
const maxDlOptions = [
  { v: 0, l: L('Sınırsız', 'Unlimited') },
  { v: 1, l: L('1 indirme', '1 download') },
  { v: 3, l: L('3 indirme', '3 downloads') },
  { v: 5, l: L('5 indirme', '5 downloads') },
  { v: 10, l: L('10 indirme', '10 downloads') },
  { v: 25, l: L('25 indirme', '25 downloads') },
];

// ⚠ The one-line curl went missing the same way the download cap did: it was
// part of the old standalone share dialog, and link creation moved here
// without it. A share link is regularly made FOR a server ("pull this onto the
// box"), and that reader has no browser — so the command comes back, built by
// the shared helper both surfaces use.
const shareCli = computed(() =>
  shareCliCommand(
    shareResult.value
      ? {
          url: shareResult.value.url,
          pin: shareResult.value.pin,
          filename: pathParts.value.name,
          isDir: !!props.isDir,
        }
      : null,
  ),
);

// ── file-drop (public upload link) state ──
const dropPwd = ref(false);
const dropExpiry = ref(defaultExpiryDays(props.shareMaxTtlDays)); // days; 0 = never
const dropShowAdv = ref(false);
const dropMaxFiles = ref<string>('');
const dropMaxSizeMB = ref<string>('');
const dropAllowedExt = ref<string>('');
const dropAskName = ref(true);
const dropBusy = ref(false);
const dropErr = ref('');
const dropResult = ref<{ url: string; pin?: string | null; expiresAt?: string | null; clamped?: boolean } | null>(null);
const dropMailTo = ref('');
const dropMailBusy = ref(false);
const dropMailNotice = ref('');

// splitEmails turns a free-text recipient field into a deduped address list —
// comma / semicolon / whitespace separated, so one input handles many people.
function splitEmails(raw: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(/[,;\s]+/)) {
    const e = part.trim().toLowerCase();
    if (e && e.includes('@') && !seen.has(e)) {
      seen.add(e);
      out.push(e);
    }
  }
  return out;
}

// mailResultNotice renders a sent/failed summary from the share-mail response.
function mailResultNotice(res: { sent?: string[]; failed?: string[] }): string {
  const sent = res.sent?.length ?? 0;
  const failed = res.failed?.length ?? 0;
  if (failed === 0) return L(`E-posta gönderildi ✓ (${sent})`, `Email sent ✓ (${sent})`);
  return L(`${sent} gönderildi, ${failed} başarısız`, `${sent} sent, ${failed} failed`);
}

async function reload() {
  loading.value = true;
  err.value = '';
  try {
    const r = await props.api.listPermissions(props.path);
    direct.value = r.direct ?? [];
    inherited.value = r.inherited ?? [];
    storageRbac.value = r.storage_rbac;
    canManage.value = true;
  } catch (e) {
    // 403 = caller is editor (not owner): no grants section, link only.
    const st = (e as { status?: number }).status;
    if (st === 403) {
      canManage.value = false;
      // Editor (not owner): the grants section is not theirs — close it and
      // leave the link tier, which is the whole dialog for them.
      // ⚠ Unless the caller asked for the drop link, which an editor may
      // perfectly well mint.
      open.value = { ...open.value, people: false };
    } else {
      err.value = e instanceof Error ? e.message : String(e);
    }
  } finally {
    loading.value = false;
  }
}
async function reloadShares() {
  try {
    const r = await props.api.listShares(props.path);
    shares.value = r.shares ?? [];
  } catch {
    shares.value = [];
  }
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close');
}
onMounted(async () => {
  // surucu:d1 — the caller's entry point decides which section opens with the
  // dialog.
  //
  // ⚠ The DEFAULT (no `initialTab`, i.e. the plain "Share / Permissions"
  // action) now opens NOTHING. It used to land on the permissions tab, which
  // meant the common case — turn the link on, copy it — arrived buried under a
  // grant editor. The link tier is the answer to the default question; the
  // grants are one click away with their own state ("2 people") readable from
  // the closed heading. A caller that explicitly wants the grants still says
  // so, and "Request files" still lands on the drop link.
  if (props.initialTab === 'drop') open.value = { ...open.value, drop: true };
  else if (props.initialTab === 'perms') open.value = { ...open.value, people: true };
  document.addEventListener('keydown', onKey);
  await reload();
  await reloadShares();
});
onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer);
  document.removeEventListener('keydown', onKey);
});

function onEmailInput() {
  noAccount.value = false;
  inviteResult.value = null;
  notice.value = '';
  const q = email.value.trim();
  if (searchTimer) clearTimeout(searchTimer);
  if (q.length < 1) {
    suggestions.value = [];
    showSuggest.value = false;
    return;
  }
  searchTimer = setTimeout(async () => {
    try {
      const r = await props.api.searchUsers(q);
      suggestions.value = r.users ?? [];
      showSuggest.value = suggestions.value.length > 0;
    } catch {
      showSuggest.value = false;
    }
  }, 180);
}
async function pickUser(u: UserSuggestion) {
  showSuggest.value = false;
  email.value = u.email;
  let lvl = level.value;
  if (u.role === 'viewer' && lvl !== 'viewer') lvl = 'viewer';
  busy.value = true;
  notice.value = '';
  try {
    await props.api.addPermission({ path: props.path, user_id: u.id, level: lvl, is_dir: !!props.isDir });
    email.value = '';
    suggestions.value = [];
    await reload();
    notice.value = L('Yetki verildi.', 'Access granted.');
  } catch (e) {
    notice.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
async function submitEmail() {
  const addr = email.value.trim().toLowerCase();
  if (!addr || !addr.includes('@')) {
    notice.value = L('Geçerli bir e-posta girin.', 'Enter a valid email.');
    return;
  }
  showSuggest.value = false;
  busy.value = true;
  notice.value = '';
  inviteResult.value = null;
  try {
    const res = await props.api.resolveEmail(addr);
    if (res.found && res.user) {
      let lvl = level.value;
      if (res.user.role === 'viewer' && lvl !== 'viewer') lvl = 'viewer';
      await props.api.addPermission({ path: props.path, user_id: res.user.id, level: lvl, is_dir: !!props.isDir });
      email.value = '';
      await reload();
      notice.value = L('Yetki verildi.', 'Access granted.');
    } else {
      noAccount.value = true;
    }
  } catch (e) {
    notice.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
async function inviteCreateUser() {
  busy.value = true;
  notice.value = '';
  try {
    const r = await props.api.invitePermission({
      path: props.path, email: email.value.trim().toLowerCase(),
      level: level.value, create_user: true, role: createRole.value, is_dir: !!props.isDir,
      locale: resolveLocale(props.locale),
    });
    inviteResult.value = { tempPassword: r.temp_password };
    notice.value = r.emailed
      ? L('Kullanıcı açıldı, davet e-postası gönderildi.', 'User created, invite emailed.')
      : L('Kullanıcı açıldı. Geçici parolayı iletin.', 'User created. Share the temp password.');
    noAccount.value = false; email.value = '';
    await reload();
  } catch (e) {
    notice.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
// "Just send a share link" → open the link options with the address prefilled
// so the owner creates a link (with their chosen expiry/PIN) and mails it
// from there.
function gotoShareWithMail() {
  shareMailTo.value = email.value.trim().toLowerCase();
  noAccount.value = false;
  notice.value = '';
  open.value = { ...open.value, link: true };
}
async function changeLevel(g: Grant, newLevel: string) {
  if (newLevel === g.level) return;
  busy.value = true;
  try { await props.api.updatePermission(g.id, newLevel); await reload(); }
  catch (e) { notice.value = e instanceof Error ? e.message : String(e); }
  finally { busy.value = false; }
}
async function removeGrant(g: Grant) {
  busy.value = true;
  try { await props.api.deletePermission(g.id); await reload(); }
  catch (e) { notice.value = e instanceof Error ? e.message : String(e); }
  finally { busy.value = false; }
}
function glabel(g: Grant): string {
  return g.user_display_name || g.user_email || `#${g.user_id}`;
}
function ginitial(g: Grant): string {
  return (g.user_display_name || g.user_email || '?').charAt(0).toUpperCase();
}

// ── share actions ──
function expiresAtISO(): string | null {
  if (!shareExpiry.value) return null;
  return new Date(Date.now() + shareExpiry.value * 86400000).toISOString();
}
async function createLink() {
  shareBusy.value = true;
  shareErr.value = '';
  shareResult.value = null;
  shareMailNotice.value = '';
  try {
    const r = await props.api.createShare({
      path: props.path,
      password: sharePwd.value,
      expires_at: expiresAtISO(),
      // null, not 0 — the server reads 0 as "no cap given" either way, but a
      // null says it explicitly and keeps the payload honest.
      max_downloads: shareMaxDl.value || null,
    });
    shareResult.value = {
      url: r.share.url,
      pin: r.share.password_pin ?? null,
      expiresAt: r.share.expires_at ?? null,
      clamped: !!r.share.expiry_clamped,
    };
    await reloadShares();
  } catch (e) {
    shareErr.value = e instanceof Error ? e.message : String(e);
  } finally {
    shareBusy.value = false;
  }
}

/**
 * gorunum:v2-share — the switch.
 *
 * ON with nothing minted mints one with whatever the options tier currently
 * says (that is the default: the server's ceiling as the expiry, no PIN, no
 * cap). OFF revokes every DOWNLOAD link on this item, because that is what
 * "link sharing is off" has to mean — a switch that left a live link behind
 * would be the most dangerous control in the dialog.
 *
 * ⚠ Drop links are deliberately untouched: they are an inbound door managed by
 * its own section, and revoking someone's upload invitation because the owner
 * turned off downloads is not what either control says.
 */
async function toggleLink() {
  if (shareBusy.value) return;
  if (linkOn.value) {
    shareBusy.value = true;
    shareErr.value = '';
    try {
      for (const s of downloadShares.value) await props.api.revokeShare(s.uuid);
      shareResult.value = null;
      await reloadShares();
    } catch (e) {
      shareErr.value = e instanceof Error ? e.message : String(e);
    } finally {
      shareBusy.value = false;
    }
    return;
  }
  await createLink();
}

async function sendShareMail() {
  const list = splitEmails(shareMailTo.value);
  if (!list.length) {
    shareMailNotice.value = L('Geçerli bir e-posta girin.', 'Enter a valid email.');
    return;
  }
  if (!shareResult.value?.url) return;
  shareMailBusy.value = true;
  shareMailNotice.value = '';
  try {
    const res = await props.api.shareMail({
      path: props.path, emails: list, url: shareResult.value.url,
      pin: shareResult.value.pin ?? undefined,
      expires_days: shareExpiry.value || undefined,
      locale: resolveLocale(props.locale),
      is_dir: !!props.isDir,
      size: props.size,
    });
    shareMailNotice.value = mailResultNotice(res);
  } catch (e) {
    const detail = (e as { detail?: string }).detail ?? '';
    if (detail.includes('not_configured')) {
      shareMailNotice.value = L('SMTP ayarlı/doğrulanmış değil — linki elle iletin.', 'SMTP not set up/verified — share the link manually.');
    } else if (detail.includes('send_failed')) {
      shareMailNotice.value = L('Gönderilemedi (geçici hata) — tekrar deneyin.', 'Send failed (temporary) — please retry.');
    } else {
      shareMailNotice.value = e instanceof Error ? e.message : String(e);
    }
  } finally {
    shareMailBusy.value = false;
  }
}

// ── file-drop (upload link) actions ──
function dropExpiresAtISO(): string | null {
  if (!dropExpiry.value) return null;
  return new Date(Date.now() + dropExpiry.value * 86400000).toISOString();
}
async function createDropLink() {
  dropBusy.value = true;
  dropErr.value = '';
  dropResult.value = null;
  dropMailNotice.value = '';
  try {
    const drop_settings: Record<string, unknown> = { ask_name: dropAskName.value };
    if (dropMaxFiles.value) drop_settings.max_files = Number(dropMaxFiles.value);
    if (dropMaxSizeMB.value) drop_settings.max_file_size_mb = Number(dropMaxSizeMB.value);
    const exts = dropAllowedExt.value.split(/[,\s]+/).map((s) => s.trim().replace(/^\./, '')).filter(Boolean);
    if (exts.length) drop_settings.allowed_ext = exts;
    const r = await props.api.createShare({
      path: props.path,
      kind: 'drop',
      password: dropPwd.value,
      expires_at: dropExpiresAtISO(),
      drop_settings,
    });
    dropResult.value = {
      url: r.share.url,
      pin: r.share.password_pin ?? null,
      expiresAt: r.share.expires_at ?? null,
      clamped: !!r.share.expiry_clamped,
    };
    await reloadShares();
  } catch (e) {
    dropErr.value = e instanceof Error ? e.message : String(e);
  } finally {
    dropBusy.value = false;
  }
}
async function sendDropMail() {
  const list = splitEmails(dropMailTo.value);
  if (!list.length) {
    dropMailNotice.value = L('Geçerli bir e-posta girin.', 'Enter a valid email.');
    return;
  }
  if (!dropResult.value?.url) return;
  dropMailBusy.value = true;
  dropMailNotice.value = '';
  try {
    const res = await props.api.shareMail({
      path: props.path, emails: list, url: dropResult.value.url,
      pin: dropResult.value.pin ?? undefined,
      expires_days: dropExpiry.value || undefined,
      locale: resolveLocale(props.locale),
      is_dir: true,
      mode: 'drop',
    });
    dropMailNotice.value = mailResultNotice(res);
  } catch (e) {
    const detail = (e as { detail?: string }).detail ?? '';
    if (detail.includes('not_configured')) {
      dropMailNotice.value = L('SMTP ayarlı/doğrulanmış değil — linki elle iletin.', 'SMTP not set up/verified — share the link manually.');
    } else {
      dropMailNotice.value = e instanceof Error ? e.message : String(e);
    }
  } finally {
    dropMailBusy.value = false;
  }
}
async function revoke(s: ShareInfo) {
  shareBusy.value = true;
  try {
    await props.api.revokeShare(s.uuid);
    // ⚠ The lead tier reads `shareResult` first. Revoking the link that is
    // showing up there has to clear it too, or the dialog goes on offering a
    // Copy button — and a switch reading ON — for a link the server has just
    // thrown away.
    if (shareResult.value && shareResult.value.url === s.url) shareResult.value = null;
    await reloadShares();
  } catch (e) { shareErr.value = e instanceof Error ? e.message : String(e); }
  finally { shareBusy.value = false; }
}
function copy(text: string, tag = 'url') {
  navigator.clipboard?.writeText(text);
  copied.value = tag;
  setTimeout(() => { if (copied.value === tag) copied.value = ''; }, 1400);
}

// ── native share (Web Share API) ──
// Same OS share sheet the fishapp uses (Windows share / Android share). Only
// shown when the browser supports it (secure context + navigator.share, e.g.
// Chrome/Edge on Windows, Android). The shared text mirrors the invite email
// body (see backend mail_templates.go) so a WhatsApp/mail forward reads the
// same as an emailed link.
const canShare = computed(() => typeof navigator !== 'undefined' && typeof navigator.share === 'function');

/**
 * The size line in the share message, spelled the way the SERVER spells it.
 *
 * ⚠ This one really does have to mirror `humanSize()` in
 * backend/internal/api/handlers/mail_templates.go — the same file forwarded
 * by e-mail and by the OS share sheet must not carry two different sizes. So
 * the mirroring is stated as arguments to the one formatter (1024, always one
 * decimal, a dot and English letters, because that is what Go's `%.1f %cB`
 * prints) instead of being a fifth private copy of the arithmetic.
 *
 * ⚠ It is therefore the one place in the UI that is NOT decimal, so a share
 * message reads "1.4 MB" where the listing row reads "1.5 MB". The register
 * entry for this was right about the real fix: the e-mail should render from
 * one source rather than the UI copying its rounding.
 */
function humanSize(b?: number): string {
  if (!b || b <= 0) return '';
  return formatByteSize(b, { base: 1024, digits: 'fixed1', numberLocale: null });
}

function expiryLine(days: number): string {
  if (days > 0) return L(`Bu bağlantı ${days} gün geçerlidir.`, `This link is valid for ${days} day(s).`);
  return L('Bu bağlantının süresi yoktur.', 'This link does not expire.');
}
// What the server actually stored — shown under a fresh link so the real
// expiry is visible even when the server shortened the request.
// The explorer this dialog belongs to reads deadlines on its own clock.
const clock = inject(EXPLORER_CLOCK, undefined);

function validUntil(r: { expiresAt?: string | null } | null): string {
  return validUntilLine(r?.expiresAt ?? null, tr.value ? 'tr' : 'en', clock);
}

/* ── the top tier: one switch, one sentence, one link ──────────────────── */

/** The link the header row shows: the one just minted, else the oldest live one. */
const primaryLink = computed(() => shareResult.value?.url ?? downloadShares.value[0]?.url ?? '');
const linkOn = computed(() => !!primaryLink.value);

/**
 * The plain sentence. It says who can open the item RIGHT NOW, which is the
 * question the dialog exists to answer.
 *
 * ⚠ The PIN claim is only made about a link this dialog minted: `listShares`
 * does not return `password_pin` (by design — the server will not re-serve a
 * PIN), so for a link that was already there we say how many exist rather than
 * guessing at its protection. A confident wrong sentence about who can read a
 * file is worse than a vaguer true one.
 */
const whoLine = computed(() => {
  if (!linkOn.value) return t('access.who.private');
  if (shareResult.value) return shareResult.value.pin ? t('access.who.pin') : t('access.who.link');
  return t('access.who.existing', { n: downloadShares.value.length });
});

/** The muted second line under it: what the server actually stored. */
const linkDetail = computed(() => {
  const bits: string[] = [];
  if (shareResult.value) {
    bits.push(validUntil(shareResult.value));
    if (shareResult.value.clamped) bits.push(L('(sunucu sınırı uygulandı)', '(server limit applied)'));
    if (shareMaxDl.value) bits.push(maxDlOptions.find((o) => o.v === shareMaxDl.value)?.l ?? '');
  } else if (downloadShares.value[0]) {
    const s = downloadShares.value[0];
    bits.push(validUntil({ expiresAt: s.expires_at ?? null }));
    if (s.max_downloads) bits.push(maxDlOptions.find((o) => o.v === s.max_downloads)?.l ?? String(s.max_downloads));
  }
  return shareDetailLine(bits);
});

/* Section summaries — a named section still has to say what is inside it, or
   the reader has to open all three to find the control they came for. */
const linkSummary = computed(() => {
  const bits: string[] = [];
  bits.push(sharePwd.value ? t('access.sum.pin_on') : t('access.sum.pin_off'));
  bits.push(expiryLabel(shareExpiry.value));
  if (shareMaxDl.value) bits.push(maxDlOptions.find((o) => o.v === shareMaxDl.value)?.l ?? '');
  return bits.filter(Boolean).join(' · ');
});
const peopleSummary = computed(() => {
  const n = direct.value.length + inherited.value.length;
  return n ? t('access.sum.people', { n }) : t('access.sum.people_none');
});
const dropSummary = computed(() =>
  dropShares.value.length ? t('access.sum.drop', { n: dropShares.value.length }) : t('access.sum.drop_none'),
);

// Text + title for a download-share link, mirroring shareMailText().
function shareBody(): { title: string; text: string } {
  const name = pathParts.value.name;
  const url = shareResult.value?.url ?? '';
  const pin = shareResult.value?.pin ?? '';
  const kind = props.isDir ? L('klasör', 'folder') : L('dosya', 'file');
  const title = tr.value
    ? `${name} ${props.isDir ? 'klasörü' : 'dosyası'} sizinle paylaşıldı`
    : `${name} has been shared with you`;
  const lines: string[] = [];
  lines.push(L('Merhaba,', 'Hello,'), '');
  lines.push(L(`Sizinle bir ${kind} paylaşıldı:`, `A ${kind} has been shared with you:`), '');
  if (props.isDir) {
    lines.push(L(`Klasör: ${name}`, `Folder: ${name}`));
  } else {
    lines.push(L(`Dosya: ${name}`, `File: ${name}`));
    const sz = humanSize(props.size);
    if (sz) lines.push(L(`Boyut: ${sz}`, `Size: ${sz}`));
  }
  lines.push('', L('İndirmek için:', 'Download it here:'), url);
  if (pin) lines.push('', L(`PIN (erişim kodu): ${pin}`, `PIN (access code): ${pin}`));
  lines.push('', expiryLine(shareExpiry.value));
  return { title, text: lines.join('\n') };
}

// Text + title for a file-drop (upload) link, mirroring dropInviteMailText().
function dropBody(): { title: string; text: string } {
  const folder = pathParts.value.name;
  const url = dropResult.value?.url ?? '';
  const pin = dropResult.value?.pin ?? '';
  const maxFiles = Number(dropMaxFiles.value) || 20;
  const maxMB = Number(dropMaxSizeMB.value) || 500;
  const exts = dropAllowedExt.value.split(/[,\s]+/).map((s) => s.trim().replace(/^\./, '')).filter(Boolean);
  const title = L(`${folder} adlı klasöre dosya eklemeniz istendi`, `You've been asked to add files to ${folder}`);
  const lines: string[] = [];
  lines.push(L('Merhaba,', 'Hello,'), '');
  lines.push(L('Dosya yüklemeniz istendi.', "You've been invited to upload files."), '');
  lines.push(L(`Klasör: ${folder}`, `Folder: ${folder}`));
  lines.push(L(`Sınır: en fazla ${maxFiles} dosya, dosya başına ${maxMB} MB.`, `Limit: up to ${maxFiles} file(s), ${maxMB} MB per file.`));
  lines.push(exts.length
    ? L(`İzinli türler: ${exts.join(', ')}`, `Allowed types: ${exts.join(', ')}`)
    : L('İzinli türler: tüm türler', 'Allowed types: all'));
  lines.push('', L('Dosyalarınızı buradan yükleyebilirsiniz:', 'Upload your files here:'), url);
  if (pin) lines.push('', L(`PIN (erişim kodu): ${pin}`, `PIN (access code): ${pin}`));
  lines.push('', expiryLine(dropExpiry.value));
  return { title, text: lines.join('\n') };
}

async function nativeShare(body: { title: string; text: string }) {
  if (!canShare.value) return;
  try {
    await navigator.share(body);
  } catch {
    // user cancelled (AbortError) or the target rejected — nothing to do.
  }
}
</script>

<template>
  <div class="fx-perm-overlay fe-share__scrim" @click.self="emit('close')">
    <!-- ⚠ `fx-perm-modal` is kept alongside the new class: e2e/shots/capture.mjs
         and the embedders' own harnesses address the dialog by it. -->
    <div class="fx-perm-modal fe-share" role="dialog" aria-modal="true" :aria-label="t('access.title', { name: pathParts.name })">
      <header class="fe-share__head">
        <span class="fe-share__tile" aria-hidden="true" v-html="titleTile"></span>
        <div class="fe-share__headtext">
          <h3 class="fe-share__title">{{ t('access.title', { name: pathParts.name }) }}</h3>
          <span class="fe-share__path" :title="path">{{ pathParts.adapter }}</span>
        </div>
        <button type="button" class="fe-share__close" :aria-label="t('access.close')" :title="t('access.close')" @click="emit('close')">
          <span aria-hidden="true" v-html="actionIconSvg('close')"></span>
        </button>
      </header>

      <div class="fe-share__body">
        <!-- ───────── tier 1: the link, the whole reason people open this ───── -->
        <div class="fe-share__lead">
          <div class="fe-share__switchrow">
            <span class="fe-share__leadicon" aria-hidden="true" v-html="actionIconSvg('access')"></span>
            <span class="fe-share__leadlabel">{{ t('access.link.switch') }}</span>
            <button
              type="button"
              class="fe-share__switch"
              role="switch"
              :aria-checked="linkOn ? 'true' : 'false'"
              :aria-label="t('access.link.switch')"
              data-testid="share-switch"
              :disabled="shareBusy"
              @click="toggleLink"
            ><span class="fe-share__knob"></span></button>
          </div>
          <p class="fe-share__who" data-testid="share-who">{{ whoLine }}</p>

          <template v-if="linkOn">
            <div class="fe-share__linkrow">
              <a :href="primaryLink" target="_blank" rel="noopener" class="fe-share__url" :title="primaryLink">{{ primaryLink }}</a>
              <button type="button" class="fe-share__copy" data-testid="share-copy" @click="copy(primaryLink, 'new')">
                <span aria-hidden="true" v-html="actionIconSvg('copy')"></span>
                <span>{{ copied === 'new' ? t('access.copied') : t('access.copy') }}</span>
              </button>
            </div>
            <p v-if="linkDetail" class="fe-share__detail" data-testid="share-valid-until">{{ linkDetail }}</p>
            <div v-if="shareResult?.pin" class="fe-share__pinrow">
              <span class="fe-share__pinlabel">PIN</span>
              <code class="fe-share__pin">{{ shareResult?.pin }}</code>
              <button type="button" class="fe-share__mini" @click="copy(shareResult?.pin ?? '', 'sharepin')">
                {{ copied === 'sharepin' ? t('access.copied') : t('access.copy') }}
              </button>
            </div>
          </template>
          <div v-if="shareErr" class="fe-share__warn">{{ shareErr }}</div>
        </div>

        <!-- ───────── tier 2: named sections, one click each ───────────────── -->
        <div class="fe-share__sections">
          <!-- Link options: PIN · expiry · download cap · curl · e-mail · links -->
          <section class="fe-share__section">
            <button
              type="button"
              class="fe-share__sechead"
              :aria-expanded="open.link ? 'true' : 'false'"
              data-testid="share-options-toggle"
              @click="toggleSection('link')"
            >
              <svg class="fe-share__caret" :class="{ 'is-open': open.link }" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"
                aria-hidden="true" focusable="false"><path d="M9 6l6 6-6 6" /></svg>
              <span class="fe-share__seclabel">{{ t('access.section.link') }}</span>
              <span class="fe-share__secmeta">{{ linkSummary }}</span>
            </button>
            <div v-if="open.link" class="fe-share__panel">
              <div class="fe-share__opts">
                <label class="fe-share__check">
                  <input type="checkbox" v-model="sharePwd" />
                  <span>{{ L('PIN ile koru', 'Protect with a PIN') }}</span>
                </label>
                <label class="fe-share__field">
                  <span class="fe-share__fieldlabel">{{ L('Süre', 'Expiry') }}</span>
                  <select v-model.number="shareExpiry" class="fe-share__select" data-testid="share-expiry">
                    <option v-for="o in expiryOptions" :key="o.v" :value="o.v">{{ o.l }}</option>
                  </select>
                </label>
                <label class="fe-share__field">
                  <span class="fe-share__fieldlabel">{{ L('İndirme limiti', 'Download limit') }}</span>
                  <select v-model.number="shareMaxDl" class="fe-share__select">
                    <option v-for="o in maxDlOptions" :key="o.v" :value="o.v">{{ o.l }}</option>
                  </select>
                </label>
              </div>
              <p v-if="ttlHint" class="fe-share__hint" data-testid="share-ttl-hint">{{ ttlHint }}</p>
              <button type="button" class="fx-perm-create fe-share__btn fe-share__btn--primary fe-share__btn--wide" :disabled="shareBusy" @click="createLink">
                {{ L('Bağlantı oluştur', 'Create link') }}
              </button>

              <p v-if="!shareResult && shareMailTo" class="fe-share__hint">
                {{ L('Bir bağlantı oluşturun, ardından', 'Create a link, then it will be sent to') }}
                <strong>{{ shareMailTo }}</strong> {{ L('adresine gönderin.', '.') }}
              </p>

              <template v-if="shareResult">
                <!-- one-line download command, for pulling the file onto a server -->
                <div class="fx-perm-cli fe-share__cli">
                  <span class="fe-share__fieldlabel">{{ L('Komut satırı', 'Command line') }}</span>
                  <div class="fe-share__clirow">
                    <code class="fe-share__clicmd" :title="shareCli">{{ shareCli }}</code>
                    <button type="button" class="fe-share__mini" @click="copy(shareCli, 'sharecli')">
                      {{ copied === 'sharecli' ? t('access.copied') : t('access.copy') }}
                    </button>
                  </div>
                </div>

                <!-- send by email (one or more, comma/space separated) -->
                <div class="fe-share__mailrow">
                  <input v-model="shareMailTo" type="text" class="fe-share__input" autocomplete="off"
                    :placeholder="L('e-posta(lar) — virgülle ayırın', 'email(s) — comma separated')" @keyup.enter="sendShareMail" />
                  <button type="button" class="fe-share__btn" :disabled="shareMailBusy" @click="sendShareMail">
                    {{ L('Gönder', 'Send') }}
                  </button>
                </div>
                <div v-if="shareMailNotice" class="fe-share__notice">{{ shareMailNotice }}</div>

                <!-- native share (OS share sheet) — same as the fishapp Share button -->
                <button v-if="canShare" type="button" class="fe-share__btn fe-share__btn--wide" @click="nativeShare(shareBody())">
                  <span aria-hidden="true" v-html="actionIconSvg('access')"></span>
                  <span>{{ L('Paylaş', 'Share') }}</span>
                </button>
              </template>

              <div class="fe-share__list">
                <h4 class="fe-share__listhead">{{ L('Mevcut bağlantılar', 'Existing links') }}</h4>
                <p v-if="!downloadShares.length" class="fe-share__empty">{{ L('Yok', 'None') }}</p>
                <div v-for="s in downloadShares" :key="s.uuid" class="fe-share__row">
                  <span class="fe-share__url" :title="s.url">{{ s.url }}</span>
                  <button type="button" class="fe-share__mini" @click="copy(s.url, s.uuid)">
                    {{ copied === s.uuid ? t('access.copied') : t('access.copy') }}
                  </button>
                  <button type="button" class="fe-share__del" :disabled="shareBusy" :title="L('İptal', 'Revoke')"
                    :aria-label="L('İptal', 'Revoke')" @click="revoke(s)">
                    <span aria-hidden="true" v-html="actionIconSvg('close')"></span>
                  </button>
                </div>
              </div>
            </div>
          </section>

          <!-- People with access: the per-user / inherited grants -->
          <section v-if="canManage" class="fe-share__section">
            <button
              type="button"
              class="fe-share__sechead"
              :aria-expanded="open.people ? 'true' : 'false'"
              data-testid="share-people-toggle"
              @click="toggleSection('people')"
            >
              <svg class="fe-share__caret" :class="{ 'is-open': open.people }" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"
                aria-hidden="true" focusable="false"><path d="M9 6l6 6-6 6" /></svg>
              <span class="fe-share__seclabel">{{ t('access.section.people') }}</span>
              <span class="fe-share__secmeta">{{ peopleSummary }}</span>
            </button>
            <div v-if="open.people" class="fe-share__panel">
              <div v-if="!storageRbac" class="fe-share__warn">
                {{ L('Bu diskte RBAC kapalı — izinler yalnızca RBAC açık disklerde geçerli.', 'RBAC is off on this storage — grants only apply when RBAC is enabled.') }}
              </div>
              <div v-if="err" class="fe-share__warn">{{ err }}</div>
              <p v-if="loading" class="fe-share__hint">{{ L('Yükleniyor…', 'Loading…') }}</p>
              <template v-else>
                <div class="fe-share__add">
                  <div class="fe-share__emailwrap">
                    <input v-model="email" type="email" class="fe-share__input" autocomplete="off"
                      :placeholder="L('İsim veya e-posta', 'Name or email')"
                      @input="onEmailInput" @keyup.enter="submitEmail" @focus="onEmailInput" />
                    <ul v-if="showSuggest" class="fe-share__suggest">
                      <li v-for="u in suggestions" :key="u.id" @mousedown.prevent="pickUser(u)">
                        <span class="fe-share__av fe-share__av--sm">{{ (u.display_name || u.email).charAt(0).toUpperCase() }}</span>
                        <span class="fe-share__suggesttxt">
                          <span class="fe-share__suggestname">{{ u.display_name || u.email }}</span>
                          <span class="fe-share__suggestmeta">{{ u.email }} · {{ u.role }}</span>
                        </span>
                      </li>
                    </ul>
                  </div>
                  <select v-model="level" class="fe-share__select" :title="levels.find(o => o.v === level)?.d">
                    <option v-for="o in levels" :key="o.v" :value="o.v">{{ o.l }}</option>
                  </select>
                  <button type="button" class="fe-share__btn fe-share__btn--primary" :disabled="busy" @click="submitEmail">
                    {{ L('Ekle', 'Add') }}
                  </button>
                </div>

                <div v-if="noAccount" class="fe-share__invite">
                  <p class="fe-share__hint">{{ L('Bu e-postada hesap yok. Ne yapmak istersiniz?', 'No account for this email — what next?') }}</p>
                  <div class="fe-share__inviterow">
                    <select v-model="createRole" class="fe-share__select">
                      <option value="user">{{ L('Kullanıcı', 'User') }}</option>
                      <option value="viewer">{{ L('Görüntüleyen', 'Viewer') }}</option>
                    </select>
                    <button type="button" class="fe-share__btn fe-share__btn--primary" :disabled="busy" @click="inviteCreateUser">
                      {{ L('Kullanıcı oluştur + yetki ver', 'Create user + grant') }}
                    </button>
                  </div>
                  <button type="button" class="fe-share__linkbtn" :disabled="busy" @click="gotoShareWithMail">
                    {{ L('Sadece paylaşım linki gönder →', 'Just send a share link →') }}
                  </button>
                </div>
                <div v-if="notice" class="fe-share__notice">{{ notice }}</div>
                <div v-if="inviteResult?.tempPassword" class="fe-share__notice">
                  {{ L('Geçici parola:', 'Temp password:') }} <code class="fe-share__pin">{{ inviteResult.tempPassword }}</code>
                </div>

                <div class="fe-share__list">
                  <h4 class="fe-share__listhead">{{ L('Erişimi olanlar', 'People with access') }}</h4>
                  <p v-if="!direct.length && !inherited.length" class="fe-share__empty">
                    {{ L('Henüz kimseyle paylaşılmadı.', 'Not shared with anyone yet.') }}
                  </p>
                  <div v-for="g in direct" :key="'d' + g.id" class="fe-share__row">
                    <span class="fe-share__av">{{ ginitial(g) }}</span>
                    <span class="fe-share__person" :title="g.user_email">{{ glabel(g) }}</span>
                    <select class="fe-share__select fe-share__select--sm" :value="g.level"
                      @change="changeLevel(g, ($event.target as HTMLSelectElement).value)">
                      <option v-for="o in levels" :key="o.v" :value="o.v">{{ o.l }}</option>
                    </select>
                    <button type="button" class="fe-share__del" :disabled="busy" :title="L('Kaldır', 'Remove')"
                      :aria-label="L('Kaldır', 'Remove')" @click="removeGrant(g)">
                      <span aria-hidden="true" v-html="actionIconSvg('close')"></span>
                    </button>
                  </div>
                  <div v-for="g in inherited" :key="'i' + g.id" class="fe-share__row fe-share__row--dim">
                    <span class="fe-share__av fe-share__av--dim">{{ ginitial(g) }}</span>
                    <span class="fe-share__person" :title="g.user_email">{{ glabel(g) }}</span>
                    <span class="fe-share__badge">{{ levelLabel(g.level) }}</span>
                    <span class="fe-share__from" :title="L('Üst klasörden gelir', 'Inherited from') + ': ' + (g.path_prefix || '/')">
                      {{ g.path_prefix || '/' }}
                    </span>
                  </div>
                </div>
              </template>
            </div>
          </section>

          <!-- Request files: the inbound drop link (folders only) -->
          <section v-if="isDir" class="fe-share__section">
            <button
              type="button"
              class="fe-share__sechead"
              :aria-expanded="open.drop ? 'true' : 'false'"
              data-testid="share-drop-toggle"
              @click="toggleSection('drop')"
            >
              <svg class="fe-share__caret" :class="{ 'is-open': open.drop }" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"
                aria-hidden="true" focusable="false"><path d="M9 6l6 6-6 6" /></svg>
              <span class="fe-share__seclabel">{{ t('access.section.drop') }}</span>
              <span class="fe-share__secmeta">{{ dropSummary }}</span>
            </button>
            <div v-if="open.drop" class="fe-share__panel">
              <p class="fe-share__hint">
                {{ L('Bu klasöre herkesin dosya YÜKLEYEBİLECEĞİ herkese açık bir bağlantı. Yükleyenler klasördeki mevcut dosyaları göremez.', 'A public link that lets anyone UPLOAD files into this folder. Uploaders never see the folder\'s existing files.') }}
              </p>
              <div class="fe-share__opts">
                <label class="fe-share__check">
                  <input type="checkbox" v-model="dropPwd" />
                  <span>{{ L('PIN ile koru', 'Protect with a PIN') }}</span>
                </label>
                <label class="fe-share__field">
                  <span class="fe-share__fieldlabel">{{ L('Süre', 'Expiry') }}</span>
                  <select v-model.number="dropExpiry" class="fe-share__select" data-testid="drop-expiry">
                    <option v-for="o in expiryOptions" :key="o.v" :value="o.v">{{ o.l }}</option>
                  </select>
                </label>
              </div>
              <p v-if="ttlHint" class="fe-share__hint">{{ ttlHint }}</p>
              <button type="button" class="fe-share__btn fe-share__btn--primary fe-share__btn--wide" data-testid="drop-create" :disabled="dropBusy" @click="createDropLink">
                {{ L('Bağlantı oluştur', 'Create link') }}
              </button>

              <button type="button" class="fe-share__linkbtn" :aria-expanded="dropShowAdv ? 'true' : 'false'" @click="dropShowAdv = !dropShowAdv">
                {{ dropShowAdv ? L('Yükleme sınırlarını gizle', 'Hide upload limits') : L('Yükleme sınırları', 'Upload limits') }}
              </button>
              <div v-if="dropShowAdv" class="fe-share__adv">
                <label class="fe-share__advrow">
                  <span class="fe-share__fieldlabel">{{ L('En fazla dosya', 'Max files') }}</span>
                  <input v-model="dropMaxFiles" type="number" min="1" class="fe-share__input fe-share__input--sm" placeholder="20" />
                </label>
                <label class="fe-share__advrow">
                  <span class="fe-share__fieldlabel">{{ L('Dosya başı MB', 'MB / file') }}</span>
                  <input v-model="dropMaxSizeMB" type="number" min="1" class="fe-share__input fe-share__input--sm" placeholder="500" />
                </label>
                <label class="fe-share__advrow">
                  <span class="fe-share__fieldlabel">{{ L('İzinli türler', 'Allowed types') }}</span>
                  <input v-model="dropAllowedExt" type="text" class="fe-share__input fe-share__input--sm" :placeholder="L('hepsi (örn. pdf, jpg)', 'all (e.g. pdf, jpg)')" />
                </label>
                <label class="fe-share__check">
                  <input type="checkbox" v-model="dropAskName" />
                  <span>{{ L('Yükleyenin adını sor', 'Ask uploader name') }}</span>
                </label>
              </div>

              <div v-if="dropErr" class="fe-share__warn">{{ dropErr }}</div>

              <template v-if="dropResult">
                <div class="fe-share__linkrow">
                  <a :href="dropResult.url" target="_blank" rel="noopener" class="fe-share__url" :title="dropResult.url">{{ dropResult.url }}</a>
                  <button type="button" class="fe-share__copy" data-testid="drop-copy" @click="copy(dropResult.url, 'drop')">
                    <span aria-hidden="true" v-html="actionIconSvg('copy')"></span>
                    <span>{{ copied === 'drop' ? t('access.copied') : t('access.copy') }}</span>
                  </button>
                </div>
                <p class="fe-share__detail">
                  {{ validUntil(dropResult) }}
                  <span v-if="dropResult.clamped">{{ L('(sunucu sınırı uygulandı)', '(server limit applied)') }}</span>
                </p>
                <div v-if="dropResult.pin" class="fe-share__pinrow">
                  <span class="fe-share__pinlabel">PIN</span>
                  <code class="fe-share__pin">{{ dropResult.pin }}</code>
                  <button type="button" class="fe-share__mini" @click="copy(dropResult.pin, 'droppin')">
                    {{ copied === 'droppin' ? t('access.copied') : t('access.copy') }}
                  </button>
                </div>

                <!-- email the upload link to one or more people -->
                <div class="fe-share__mailrow">
                  <input v-model="dropMailTo" type="text" class="fe-share__input" autocomplete="off"
                    :placeholder="L('e-posta(lar) — virgülle ayırın', 'email(s) — comma separated')" @keyup.enter="sendDropMail" />
                  <button type="button" class="fe-share__btn" :disabled="dropMailBusy" @click="sendDropMail">
                    {{ L('Gönder', 'Send') }}
                  </button>
                </div>
                <div v-if="dropMailNotice" class="fe-share__notice">{{ dropMailNotice }}</div>

                <!-- native share (OS share sheet) — same as the fishapp Share button -->
                <button v-if="canShare" type="button" class="fe-share__btn fe-share__btn--wide" @click="nativeShare(dropBody())">
                  <span aria-hidden="true" v-html="actionIconSvg('access')"></span>
                  <span>{{ L('Paylaş', 'Share') }}</span>
                </button>
              </template>

              <div class="fe-share__list">
                <h4 class="fe-share__listhead">{{ L('Mevcut bağlantılar', 'Existing links') }}</h4>
                <p v-if="!dropShares.length" class="fe-share__empty">{{ L('Yok', 'None') }}</p>
                <div v-for="s in dropShares" :key="s.uuid" class="fe-share__row">
                  <span class="fe-share__url" :title="s.url">{{ s.url }}</span>
                  <button type="button" class="fe-share__mini" @click="copy(s.url, s.uuid)">
                    {{ copied === s.uuid ? t('access.copied') : t('access.copy') }}
                  </button>
                  <button type="button" class="fe-share__del" :disabled="shareBusy" :title="L('İptal', 'Revoke')"
                    :aria-label="L('İptal', 'Revoke')" @click="revoke(s)">
                    <span aria-hidden="true" v-html="actionIconSvg('close')"></span>
                  </button>
                </div>
              </div>
            </div>
          </section>
        </div>
      </div>

      <footer class="fe-share__foot">
        <button type="button" class="fe-share__btn fe-share__btn--primary" data-testid="share-done" @click="emit('close')">
          {{ t('access.done') }}
        </button>
      </footer>
    </div>
  </div>
</template>
