<script setup lang="ts">
// Access modal — one popup combining "İzinler" (per-user RBAC grants,
// owner-only) and "Bağlantı ile paylaş" (public share link, editor+). Opened
// from the explorer's unified "Paylaş / İzinler" action. The layout is a fixed
// header/tabs with a single scrollable body so the popup never grows into one
// long scroll. Styling uses the SFC's --fe-* theme variables (light/dark).
import { ref, onMounted, onBeforeUnmount, computed } from 'vue';
import type { FileApi, Grant, UserSuggestion } from '../composables/useFileApi';
import type { ShareInfo } from '../types/FileNode';

const props = defineProps<{
  api: FileApi;
  path: string; // adapter://rel of the target item
  isDir?: boolean; // folder → grants cascade; file → no `/…` inheritance hint
  size?: number; // bytes, for the share-mail body (files only)
  locale?: 'tr' | 'en';
}>();
const emit = defineEmits<{ (e: 'close'): void }>();

const tr = computed(() => (props.locale ?? 'tr') !== 'en');
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

type Tab = 'perms' | 'share' | 'drop';
const tab = ref<Tab>('perms');
const canManage = ref(false); // owner/admin → can see the permissions tab

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
const shareExpiry = ref(0); // days; 0 = never
const shareResult = ref<{ url: string; pin?: string | null } | null>(null);
const shareErr = ref('');
const copied = ref('');
// prefilled recipient when the owner chose "share link" for a no-account email
const shareMailTo = ref('');
const shareMailBusy = ref(false);
const shareMailNotice = ref('');

const expiryOptions = [
  { v: 0, l: L('Süresiz', 'Never') },
  { v: 1, l: L('1 gün', '1 day') },
  { v: 7, l: L('7 gün', '7 days') },
  { v: 30, l: L('30 gün', '30 days') },
];

// ── file-drop (public upload link) state ──
const dropPwd = ref(false);
const dropExpiry = ref(0); // days; 0 = never
const dropShowAdv = ref(false);
const dropMaxFiles = ref<string>('');
const dropMaxSizeMB = ref<string>('');
const dropAllowedExt = ref<string>('');
const dropAskName = ref(true);
const dropBusy = ref(false);
const dropErr = ref('');
const dropResult = ref<{ url: string; pin?: string | null } | null>(null);
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
    // 403 = caller is editor (not owner): no permissions tab, share only.
    const st = (e as { status?: number }).status;
    if (st === 403) {
      canManage.value = false;
      // Editor (not owner): no permissions tab → fall back to the link tab.
      tab.value = 'share';
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
onMounted(async () => {
  await reload();
  await reloadShares();
});
onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer);
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
      locale: props.locale ?? 'tr',
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
// "Sadece paylaş" → jump to the share tab with the address prefilled so the
// owner creates a link (with their chosen expiry/PIN) and mails it there.
function gotoShareWithMail() {
  shareMailTo.value = email.value.trim().toLowerCase();
  noAccount.value = false;
  notice.value = '';
  tab.value = 'share';
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
    });
    shareResult.value = { url: r.share.url, pin: r.share.password_pin ?? null };
    await reloadShares();
  } catch (e) {
    shareErr.value = e instanceof Error ? e.message : String(e);
  } finally {
    shareBusy.value = false;
  }
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
      locale: props.locale ?? 'tr',
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
    dropResult.value = { url: r.share.url, pin: r.share.password_pin ?? null };
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
      locale: props.locale ?? 'tr',
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
  try { await props.api.revokeShare(s.uuid); await reloadShares(); }
  catch (e) { shareErr.value = e instanceof Error ? e.message : String(e); }
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

// humanSize mirrors the Go humanSize() used in the email body (1.4 MB).
function humanSize(b?: number): string {
  if (!b || b <= 0) return '';
  const unit = 1024;
  if (b < unit) return `${b} B`;
  const units = ['K', 'M', 'G', 'T', 'P', 'E'];
  let div = unit, exp = 0;
  for (let n = b / unit; n >= unit; n /= unit) { div *= unit; exp++; }
  return `${(b / div).toFixed(1)} ${units[exp]}B`;
}

function expiryLine(days: number): string {
  if (days > 0) return L(`Bu bağlantı ${days} gün geçerlidir.`, `This link is valid for ${days} day(s).`);
  return L('Bu bağlantının süresi yoktur.', 'This link does not expire.');
}

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
  <div class="fx-perm-overlay" @click.self="emit('close')">
    <div class="fx-perm-modal">
      <header class="fx-perm-head">
        <div class="fx-perm-title">
          <span class="fx-perm-ico" aria-hidden="true">{{ isDir ? '📁' : '📄' }}</span>
          <div class="fx-perm-titletext">
            <h3>{{ L('Paylaş / İzinler', 'Share / Permissions') }}</h3>
            <span class="fx-perm-sub" :title="path">{{ pathParts.name }}<span class="fx-perm-subdim"> · {{ pathParts.adapter }}</span></span>
          </div>
        </div>
        <button class="fx-perm-x" @click="emit('close')" aria-label="close">✕</button>
      </header>

      <div class="fx-perm-tabs">
        <button
          v-if="canManage"
          class="fx-perm-tab"
          :class="{ 'is-active': tab === 'perms' }"
          @click="tab = 'perms'"
        >{{ L('Kişiler', 'People') }}</button>
        <button
          class="fx-perm-tab"
          :class="{ 'is-active': tab === 'share' }"
          @click="tab = 'share'"
        >{{ L('Bağlantı', 'Link') }}</button>
        <button
          v-if="isDir"
          class="fx-perm-tab"
          :class="{ 'is-active': tab === 'drop' }"
          @click="tab = 'drop'"
        >{{ L('Dosya İste', 'Request files') }}</button>
      </div>

      <div class="fx-perm-body">
        <!-- ───────── Permissions tab ───────── -->
        <template v-if="tab === 'perms' && canManage">
          <div v-if="!storageRbac" class="fx-perm-warn">
            {{ L('Bu diskte RBAC kapalı — izinler yalnızca RBAC açık disklerde geçerli.', 'RBAC is off on this storage — grants only apply when RBAC is enabled.') }}
          </div>
          <div v-if="err" class="fx-perm-warn">{{ err }}</div>
          <div v-if="loading" class="fx-perm-muted">{{ L('Yükleniyor…', 'Loading…') }}</div>
          <template v-else>
            <!-- Add person (primary action, on top) -->
            <div class="fx-perm-addcard">
              <div class="fx-perm-add">
                <div class="fx-perm-emailwrap">
                  <input v-model="email" type="email" class="fx-perm-input" autocomplete="off"
                    :placeholder="L('İsim veya e-posta', 'Name or email')"
                    @input="onEmailInput" @keyup.enter="submitEmail" @focus="onEmailInput" />
                  <ul v-if="showSuggest" class="fx-perm-suggest">
                    <li v-for="u in suggestions" :key="u.id" @mousedown.prevent="pickUser(u)">
                      <span class="fx-perm-suggest-av">{{ (u.display_name || u.email).charAt(0).toUpperCase() }}</span>
                      <span class="fx-perm-suggest-txt">
                        <span class="fx-perm-suggest-name">{{ u.display_name || u.email }}</span>
                        <span class="fx-perm-suggest-meta">{{ u.email }} · {{ u.role }}</span>
                      </span>
                    </li>
                  </ul>
                </div>
                <select v-model="level" class="fx-perm-sel" :title="levels.find(o => o.v === level)?.d">
                  <option v-for="o in levels" :key="o.v" :value="o.v">{{ o.l }}</option>
                </select>
                <button class="fx-perm-btn fx-perm-btn--primary" :disabled="busy" @click="submitEmail">
                  {{ L('Ekle', 'Add') }}
                </button>
              </div>

              <div v-if="noAccount" class="fx-perm-invite">
                <p class="fx-perm-muted">{{ L('Bu e-postada hesap yok. Ne yapmak istersiniz?', 'No account for this email — what next?') }}</p>
                <div class="fx-perm-invite-actions">
                  <div class="fx-perm-invite-row">
                    <select v-model="createRole" class="fx-perm-sel">
                      <option value="user">{{ L('Kullanıcı', 'User') }}</option>
                      <option value="viewer">{{ L('Görüntüleyen', 'Viewer') }}</option>
                    </select>
                    <button class="fx-perm-btn fx-perm-btn--primary" :disabled="busy" @click="inviteCreateUser">
                      {{ L('Kullanıcı oluştur + yetki ver', 'Create user + grant') }}
                    </button>
                  </div>
                  <button class="fx-perm-btn fx-perm-btn--ghost" :disabled="busy" @click="gotoShareWithMail">
                    {{ L('Sadece paylaşım linki gönder →', 'Just send a share link →') }}
                  </button>
                </div>
              </div>
              <div v-if="notice" class="fx-perm-notice">{{ notice }}</div>
              <div v-if="inviteResult?.tempPassword" class="fx-perm-reveal">
                {{ L('Geçici parola:', 'Temp password:') }} <code>{{ inviteResult.tempPassword }}</code>
              </div>
            </div>

            <!-- People with access -->
            <div class="fx-perm-section">
              <h4>{{ L('Erişimi olanlar', 'People with access') }}</h4>
              <div v-if="!direct.length && !inherited.length" class="fx-perm-empty">
                {{ L('Henüz kimseyle paylaşılmadı.', 'Not shared with anyone yet.') }}
              </div>
              <div v-for="g in direct" :key="'d' + g.id" class="fx-perm-person">
                <span class="fx-perm-av">{{ ginitial(g) }}</span>
                <span class="fx-perm-user" :title="g.user_email">{{ glabel(g) }}</span>
                <select class="fx-perm-sel fx-perm-sel--sm" :value="g.level"
                  @change="changeLevel(g, ($event.target as HTMLSelectElement).value)">
                  <option v-for="o in levels" :key="o.v" :value="o.v">{{ o.l }}</option>
                </select>
                <button class="fx-perm-del" :disabled="busy" :title="L('Kaldır', 'Remove')" @click="removeGrant(g)">✕</button>
              </div>
              <div v-for="g in inherited" :key="'i' + g.id" class="fx-perm-person fx-perm-inh">
                <span class="fx-perm-av fx-perm-av--dim">{{ ginitial(g) }}</span>
                <span class="fx-perm-user" :title="g.user_email">{{ glabel(g) }}</span>
                <span class="fx-perm-badge">{{ levelLabel(g.level) }}</span>
                <span class="fx-perm-from" :title="L('Üst klasörden gelir', 'Inherited from') + ': ' + (g.path_prefix || '/')">
                  ↳ {{ g.path_prefix || '/' }}
                </span>
              </div>
            </div>
          </template>
        </template>

        <!-- ───────── Share tab ───────── -->
        <template v-if="tab === 'share'">
          <div class="fx-perm-addcard">
            <div class="fx-perm-share-opts">
              <label class="fx-perm-check">
                <input type="checkbox" v-model="sharePwd" />
                {{ L('PIN ile koru', 'Protect with a PIN') }}
              </label>
              <label class="fx-perm-expiry">
                <span class="fx-perm-muted">{{ L('Süre', 'Expiry') }}</span>
                <select v-model.number="shareExpiry" class="fx-perm-sel fx-perm-sel--sm">
                  <option v-for="o in expiryOptions" :key="o.v" :value="o.v">{{ o.l }}</option>
                </select>
              </label>
              <button class="fx-perm-btn fx-perm-btn--primary" :disabled="shareBusy" @click="createLink">
                {{ L('Bağlantı oluştur', 'Create link') }}
              </button>
            </div>

            <div v-if="shareErr" class="fx-perm-warn">{{ shareErr }}</div>

            <div v-if="shareResult" class="fx-perm-result">
              <div class="fx-perm-linkrow">
                <a :href="shareResult.url" target="_blank" rel="noopener" class="fx-perm-link">{{ shareResult.url }}</a>
                <button class="fx-perm-btn fx-perm-btn--sm" @click="copy(shareResult.url, 'new')">
                  {{ copied === 'new' ? L('Kopyalandı ✓', 'Copied ✓') : L('Kopyala', 'Copy') }}
                </button>
              </div>
              <div v-if="shareResult.pin" class="fx-perm-pin">
                <span>PIN: <code>{{ shareResult.pin }}</code></span>
                <button class="fx-perm-btn fx-perm-btn--sm" @click="copy(shareResult.pin, 'sharepin')">
                  {{ copied === 'sharepin' ? L('Kopyalandı ✓', 'Copied ✓') : L('Kopyala', 'Copy') }}
                </button>
              </div>

              <!-- send by email (one or more, comma/space separated) -->
              <div class="fx-perm-mailrow">
                <input v-model="shareMailTo" type="text" class="fx-perm-input" autocomplete="off"
                  :placeholder="L('e-posta(lar) — virgülle ayırın', 'email(s) — comma separated')" @keyup.enter="sendShareMail" />
                <button class="fx-perm-btn" :disabled="shareMailBusy" @click="sendShareMail">
                  {{ L('Gönder', 'Send') }}
                </button>
              </div>
              <div v-if="shareMailNotice" class="fx-perm-notice">{{ shareMailNotice }}</div>

              <!-- native share (OS share sheet) — same as the fishapp Paylaş button -->
              <button v-if="canShare" type="button" class="fx-perm-btn fx-perm-btn--ghost fx-perm-sharebtn" @click="nativeShare(shareBody())">
                <span aria-hidden="true">📤</span> {{ L('Paylaş', 'Share') }}
              </button>
            </div>
            <p v-else-if="shareMailTo" class="fx-perm-hint">
              {{ L('Aşağıdan bir bağlantı oluşturun, ardından', 'Create a link below, then it will be sent to') }}
              <strong>{{ shareMailTo }}</strong> {{ L('adresine gönderin.', '.') }}
            </p>
          </div>

          <div class="fx-perm-section">
            <h4>{{ L('Mevcut bağlantılar', 'Existing links') }}</h4>
            <div v-if="!shares.length" class="fx-perm-empty">{{ L('Yok', 'None') }}</div>
            <div v-for="s in shares" :key="s.uuid" class="fx-perm-person">
              <span class="fx-perm-user fx-perm-link" :title="s.url">{{ s.url }}</span>
              <button class="fx-perm-btn fx-perm-btn--sm" @click="copy(s.url, s.uuid)">
                {{ copied === s.uuid ? L('✓', '✓') : L('Kopyala', 'Copy') }}
              </button>
              <button class="fx-perm-del" :disabled="shareBusy" :title="L('İptal', 'Revoke')" @click="revoke(s)">✕</button>
            </div>
          </div>
        </template>

        <!-- ───────── File-drop (upload link) tab ───────── -->
        <template v-if="tab === 'drop'">
          <div class="fx-perm-addcard">
            <p class="fx-perm-muted fx-perm-dropintro">
              {{ L('Bu klasöre herkesin dosya YÜKLEYEBİLECEĞİ herkese açık bir bağlantı. Yükleyenler klasördeki mevcut dosyaları göremez.', 'A public link that lets anyone UPLOAD files into this folder. Uploaders never see the folder\'s existing files.') }}
            </p>
            <div class="fx-perm-share-opts">
              <label class="fx-perm-check">
                <input type="checkbox" v-model="dropPwd" />
                {{ L('PIN ile koru', 'Protect with a PIN') }}
              </label>
              <label class="fx-perm-expiry">
                <span class="fx-perm-muted">{{ L('Süre', 'Expiry') }}</span>
                <select v-model.number="dropExpiry" class="fx-perm-sel fx-perm-sel--sm">
                  <option v-for="o in expiryOptions" :key="o.v" :value="o.v">{{ o.l }}</option>
                </select>
              </label>
              <button class="fx-perm-btn fx-perm-btn--primary" :disabled="dropBusy" @click="createDropLink">
                {{ L('Bağlantı oluştur', 'Create link') }}
              </button>
            </div>

            <button type="button" class="fx-perm-btn fx-perm-btn--ghost fx-perm-advtoggle" @click="dropShowAdv = !dropShowAdv">
              {{ dropShowAdv ? L('Gelişmiş ▲', 'Advanced ▲') : L('Gelişmiş ▼', 'Advanced ▼') }}
            </button>
            <div v-if="dropShowAdv" class="fx-perm-adv">
              <label class="fx-perm-adv-row">
                <span class="fx-perm-muted">{{ L('En fazla dosya', 'Max files') }}</span>
                <input v-model="dropMaxFiles" type="number" min="1" class="fx-perm-input fx-perm-input--sm" placeholder="20" />
              </label>
              <label class="fx-perm-adv-row">
                <span class="fx-perm-muted">{{ L('Dosya başı MB', 'MB / file') }}</span>
                <input v-model="dropMaxSizeMB" type="number" min="1" class="fx-perm-input fx-perm-input--sm" placeholder="500" />
              </label>
              <label class="fx-perm-adv-row">
                <span class="fx-perm-muted">{{ L('İzinli türler', 'Allowed types') }}</span>
                <input v-model="dropAllowedExt" type="text" class="fx-perm-input fx-perm-input--sm" :placeholder="L('hepsi (örn. pdf, jpg)', 'all (e.g. pdf, jpg)')" />
              </label>
              <label class="fx-perm-check">
                <input type="checkbox" v-model="dropAskName" />
                {{ L('Yükleyenin adını sor', 'Ask uploader name') }}
              </label>
            </div>

            <div v-if="dropErr" class="fx-perm-warn">{{ dropErr }}</div>

            <div v-if="dropResult" class="fx-perm-result">
              <div class="fx-perm-linkrow">
                <a :href="dropResult.url" target="_blank" rel="noopener" class="fx-perm-link">{{ dropResult.url }}</a>
                <button class="fx-perm-btn fx-perm-btn--sm" @click="copy(dropResult.url, 'drop')">
                  {{ copied === 'drop' ? L('Kopyalandı ✓', 'Copied ✓') : L('Kopyala', 'Copy') }}
                </button>
              </div>
              <div v-if="dropResult.pin" class="fx-perm-pin">
                <span>PIN: <code>{{ dropResult.pin }}</code></span>
                <button class="fx-perm-btn fx-perm-btn--sm" @click="copy(dropResult.pin, 'droppin')">
                  {{ copied === 'droppin' ? L('Kopyalandı ✓', 'Copied ✓') : L('Kopyala', 'Copy') }}
                </button>
              </div>

              <!-- email the upload link to one or more people -->
              <div class="fx-perm-mailrow">
                <input v-model="dropMailTo" type="text" class="fx-perm-input" autocomplete="off"
                  :placeholder="L('e-posta(lar) — virgülle ayırın', 'email(s) — comma separated')" @keyup.enter="sendDropMail" />
                <button class="fx-perm-btn" :disabled="dropMailBusy" @click="sendDropMail">
                  {{ L('Gönder', 'Send') }}
                </button>
              </div>
              <div v-if="dropMailNotice" class="fx-perm-notice">{{ dropMailNotice }}</div>

              <!-- native share (OS share sheet) — same as the fishapp Paylaş button -->
              <button v-if="canShare" type="button" class="fx-perm-btn fx-perm-btn--ghost fx-perm-sharebtn" @click="nativeShare(dropBody())">
                <span aria-hidden="true">📤</span> {{ L('Paylaş', 'Share') }}
              </button>
            </div>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.fx-perm-overlay {
  position: fixed; inset: 0; background: rgba(0, 0, 0, 0.5);
  display: flex; align-items: center; justify-content: center; z-index: 10000;
  font-family: var(--fe-font);
}
.fx-perm-modal {
  display: flex; flex-direction: column;
  background: var(--fe-bg); color: var(--fe-text);
  width: min(520px, 94vw); max-height: 86vh;
  border: 1px solid var(--fe-border); border-radius: var(--fe-radius-lg, 14px);
  box-shadow: var(--fe-shadow); font-size: 14px; overflow: hidden;
}
/* header + tabs are fixed; only the body scrolls */
.fx-perm-head {
  display: flex; align-items: center; justify-content: space-between; gap: 10px;
  padding: 16px 18px 12px; flex: none;
}
.fx-perm-title { display: flex; align-items: center; gap: 10px; min-width: 0; }
.fx-perm-ico { font-size: 22px; line-height: 1; }
.fx-perm-titletext { display: flex; flex-direction: column; min-width: 0; }
.fx-perm-title h3 { margin: 0; font-size: 15px; font-weight: 600; color: var(--fe-text); }
.fx-perm-sub {
  font-size: 12px; color: var(--fe-text); max-width: 340px;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.fx-perm-subdim { color: var(--fe-text-muted); }
.fx-perm-x {
  background: none; border: none; font-size: 17px; cursor: pointer;
  color: var(--fe-text-muted); flex: none; line-height: 1; padding: 2px 4px; border-radius: 6px;
}
.fx-perm-x:hover { color: var(--fe-text); background: var(--fe-bg-hover); }
.fx-perm-tabs { display: flex; gap: 2px; padding: 0 18px; border-bottom: 1px solid var(--fe-border); flex: none; }
.fx-perm-tab {
  background: none; border: none; border-bottom: 2px solid transparent;
  padding: 8px 12px; cursor: pointer; color: var(--fe-text-muted); font-size: 13px;
  font-family: inherit; font-weight: 500;
}
.fx-perm-tab:hover { color: var(--fe-text); }
.fx-perm-tab.is-active { color: var(--fe-primary); border-bottom-color: var(--fe-primary); }
.fx-perm-body { flex: 1 1 auto; overflow-y: auto; padding: 14px 18px 18px; }

.fx-perm-addcard {
  padding: 12px; border-radius: var(--fe-radius-md, 10px);
  background: var(--fe-bg-elev); border: 1px solid var(--fe-border);
}
.fx-perm-section { margin-top: 16px; }
.fx-perm-section h4 {
  margin: 0 0 8px; font-size: 11px; text-transform: uppercase; letter-spacing: 0.05em; color: var(--fe-text-muted);
}
.fx-perm-empty { color: var(--fe-text-muted); font-size: 13px; padding: 8px 2px; }

/* person rows */
.fx-perm-person { display: flex; align-items: center; gap: 10px; padding: 7px 2px; }
.fx-perm-person + .fx-perm-person { border-top: 1px solid var(--fe-border); }
.fx-perm-inh { color: var(--fe-text-muted); }
.fx-perm-av {
  width: 28px; height: 28px; flex: none; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  background: var(--fe-primary); color: var(--fe-text-on-primary, #fff);
  font-size: 12px; font-weight: 600;
}
.fx-perm-av--dim { background: var(--fe-bg-hover); color: var(--fe-text-muted); }
.fx-perm-user { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--fe-text); min-width: 0; }
.fx-perm-from { font-size: 11px; color: var(--fe-text-muted); white-space: nowrap; }
.fx-perm-badge {
  font-size: 11px; padding: 2px 9px; border-radius: 999px;
  background: var(--fe-bg-hover); color: var(--fe-text);
}

/* inputs / selects / buttons */
.fx-perm-sel, .fx-perm-input {
  padding: 7px 9px; border-radius: var(--fe-radius-sm, 7px); border: 1px solid var(--fe-border);
  background: var(--fe-bg); color: var(--fe-text); font-size: 13px; font-family: inherit;
}
.fx-perm-sel--sm { padding: 5px 7px; font-size: 12px; }
.fx-perm-sel:focus, .fx-perm-input:focus { outline: none; border-color: var(--fe-primary); }
.fx-perm-add { display: flex; gap: 8px; align-items: stretch; }
.fx-perm-emailwrap { position: relative; flex: 1; min-width: 120px; }
.fx-perm-input { width: 100%; box-sizing: border-box; }
.fx-perm-suggest {
  position: absolute; top: calc(100% + 3px); left: 0; right: 0; z-index: 5; margin: 0; padding: 4px;
  list-style: none; background: var(--fe-bg); border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-sm, 7px); box-shadow: var(--fe-shadow); max-height: 210px; overflow: auto;
}
.fx-perm-suggest li { display: flex; align-items: center; gap: 8px; padding: 6px 8px; border-radius: 6px; cursor: pointer; }
.fx-perm-suggest li:hover { background: var(--fe-bg-hover); }
.fx-perm-suggest-av {
  width: 24px; height: 24px; flex: none; border-radius: 50%; display: flex; align-items: center; justify-content: center;
  background: var(--fe-bg-hover); color: var(--fe-text); font-size: 11px; font-weight: 600;
}
.fx-perm-suggest-txt { display: flex; flex-direction: column; min-width: 0; }
.fx-perm-suggest-name { color: var(--fe-text); font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.fx-perm-suggest-meta { color: var(--fe-text-muted); font-size: 11px; }
.fx-perm-check { display: inline-flex; gap: 6px; align-items: center; font-size: 13px; color: var(--fe-text); cursor: pointer; }
.fx-perm-btn {
  padding: 7px 13px; border-radius: var(--fe-radius-sm, 7px); border: 1px solid var(--fe-border);
  background: var(--fe-bg); color: var(--fe-text); font-size: 13px; font-family: inherit; cursor: pointer; white-space: nowrap;
}
.fx-perm-btn:hover:not(:disabled) { background: var(--fe-bg-hover); }
.fx-perm-btn:disabled { opacity: 0.55; cursor: default; }
.fx-perm-btn--sm { padding: 5px 10px; font-size: 12px; }
.fx-perm-btn--primary { background: var(--fe-primary); border-color: var(--fe-primary); color: var(--fe-text-on-primary, #fff); }
.fx-perm-btn--primary:hover:not(:disabled) { background: var(--fe-primary-hover, var(--fe-primary)); filter: brightness(1.05); }
.fx-perm-btn--ghost { background: none; border-color: transparent; color: var(--fe-primary); padding-left: 2px; }
.fx-perm-btn--ghost:hover:not(:disabled) { background: none; text-decoration: underline; }
.fx-perm-del { background: none; border: none; color: var(--fe-danger); cursor: pointer; font-size: 14px; flex: none; padding: 2px 4px; }
.fx-perm-del:hover:not(:disabled) { filter: brightness(1.15); }

/* invite sub-flow */
.fx-perm-invite {
  margin-top: 10px; padding-top: 10px; border-top: 1px dashed var(--fe-border);
}
.fx-perm-invite-actions { display: flex; flex-direction: column; gap: 8px; margin-top: 6px; }
.fx-perm-invite-row { display: flex; gap: 8px; align-items: stretch; }
.fx-perm-invite-row .fx-perm-btn--primary { flex: 1; }

/* share result */
.fx-perm-share-opts { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.fx-perm-expiry { display: inline-flex; gap: 6px; align-items: center; font-size: 13px; }
.fx-perm-share-opts .fx-perm-btn--primary { margin-left: auto; }
.fx-perm-result { margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--fe-border); }
.fx-perm-linkrow { display: flex; gap: 8px; align-items: center; }
.fx-perm-link { color: var(--fe-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; min-width: 0; text-decoration: none; }
.fx-perm-link:hover { text-decoration: underline; }
.fx-perm-pin { margin: 8px 0 0; font-size: 13px; color: var(--fe-text); display: flex; align-items: center; gap: 8px; }
.fx-perm-pin code, .fx-perm-reveal code { font-family: var(--fe-font-mono, monospace); background: var(--fe-bg-hover); padding: 1px 6px; border-radius: 5px; }
.fx-perm-mailrow { display: flex; gap: 8px; margin-top: 10px; }
.fx-perm-mailrow .fx-perm-input { flex: 1; }
.fx-perm-hint { font-size: 13px; color: var(--fe-text-muted); margin: 10px 0 0; }
/* native "Paylaş" button — sits under the mail row, full width */
.fx-perm-sharebtn { display: flex; width: 100%; align-items: center; justify-content: center; gap: 6px; margin-top: 8px; }

/* file-drop tab */
.fx-perm-dropintro { margin: 0 0 12px; line-height: 1.4; }
.fx-perm-advtoggle { margin-top: 10px; padding-left: 2px; }
.fx-perm-adv {
  margin-top: 8px; padding-top: 10px; border-top: 1px dashed var(--fe-border);
  display: flex; flex-direction: column; gap: 8px;
}
.fx-perm-adv-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; font-size: 13px; }
.fx-perm-input--sm { width: 130px; padding: 5px 8px; font-size: 12px; }

.fx-perm-warn {
  background: rgba(245, 158, 11, 0.14); border: 1px solid rgba(245, 158, 11, 0.4);
  border-radius: var(--fe-radius-sm, 7px); padding: 8px 10px; font-size: 13px; margin-bottom: 10px; color: var(--fe-text);
}
.fx-perm-notice { margin-top: 8px; font-size: 13px; color: var(--fe-text-muted); }
.fx-perm-reveal { margin-top: 8px; font-size: 13px; word-break: break-all; color: var(--fe-text); }
.fx-perm-muted { color: var(--fe-text-muted); font-size: 13px; }
</style>
