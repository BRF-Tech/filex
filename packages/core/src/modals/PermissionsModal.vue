<script setup lang="ts">
// Access modal — a single popup combining "İzinler" (per-user RBAC grants,
// owner-only) and "Paylaş" (public share link, editor+). Opened from the
// explorer's unified "Paylaş / İzinler" action. Styling uses the SFC's --fe-*
// theme variables so it matches light/dark.
import { ref, onMounted, onBeforeUnmount, computed } from 'vue';
import type { FileApi, Grant, UserSuggestion } from '../composables/useFileApi';
import type { ShareInfo } from '../types/FileNode';

const props = defineProps<{
  api: FileApi;
  path: string; // adapter://rel of the target item
  locale?: 'tr' | 'en';
}>();
const emit = defineEmits<{ (e: 'close'): void }>();

const tr = computed(() => (props.locale ?? 'tr') !== 'en');
function L(t: string, e: string): string {
  return tr.value ? t : e;
}

type Tab = 'perms' | 'share';
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
const inviteResult = ref<{ link?: string; tempPassword?: string } | null>(null);
const suggestions = ref<UserSuggestion[]>([]);
const showSuggest = ref(false);
let searchTimer: ReturnType<typeof setTimeout> | undefined;

const levels: Array<{ v: 'viewer' | 'editor' | 'owner'; l: string }> = [
  { v: 'viewer', l: L('Görüntüleyen', 'Viewer') },
  { v: 'editor', l: L('Düzenleyen', 'Editor') },
  { v: 'owner', l: L('Sahip', 'Owner') },
];

// ── share state ──
const shares = ref<ShareInfo[]>([]);
const shareBusy = ref(false);
const sharePwd = ref(false);
const shareResult = ref<{ url: string; pin?: string | null } | null>(null);
const shareErr = ref('');

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
    await props.api.addPermission({ path: props.path, user_id: u.id, level: lvl });
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
      await props.api.addPermission({ path: props.path, user_id: res.user.id, level: lvl });
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
      level: level.value, create_user: true, role: createRole.value,
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
async function inviteShare() {
  busy.value = true;
  notice.value = '';
  try {
    const r = await props.api.invitePermission({
      path: props.path, email: email.value.trim().toLowerCase(), level: level.value,
    });
    inviteResult.value = { link: r.url };
    notice.value = r.emailed
      ? L('Paylaşım linki e-postayla gönderildi.', 'Share link emailed.')
      : L('Paylaşım linki oluşturuldu.', 'Share link created.');
    noAccount.value = false;
  } catch (e) {
    notice.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
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

// ── share actions ──
async function createLink() {
  shareBusy.value = true;
  shareErr.value = '';
  shareResult.value = null;
  try {
    const r = await props.api.createShare({ path: props.path, password: sharePwd.value });
    shareResult.value = { url: r.share.url, pin: r.share.password_pin ?? null };
    await reloadShares();
  } catch (e) {
    shareErr.value = e instanceof Error ? e.message : String(e);
  } finally {
    shareBusy.value = false;
  }
}
async function revoke(s: ShareInfo) {
  shareBusy.value = true;
  try { await props.api.revokeShare(s.uuid); await reloadShares(); }
  catch (e) { shareErr.value = e instanceof Error ? e.message : String(e); }
  finally { shareBusy.value = false; }
}
function copy(text: string) {
  navigator.clipboard?.writeText(text);
}
</script>

<template>
  <div class="fx-perm-overlay" @click.self="emit('close')">
    <div class="fx-perm-modal">
      <header class="fx-perm-head">
        <h3>{{ L('Paylaş / İzinler', 'Share / Permissions') }}</h3>
        <button class="fx-perm-x" @click="emit('close')" aria-label="close">✕</button>
      </header>
      <p class="fx-perm-path">{{ path }}</p>

      <div class="fx-perm-tabs">
        <button
          v-if="canManage"
          class="fx-perm-tab"
          :class="{ 'is-active': tab === 'perms' }"
          @click="tab = 'perms'"
        >{{ L('İzinler', 'Permissions') }}</button>
        <button
          class="fx-perm-tab"
          :class="{ 'is-active': tab === 'share' }"
          @click="tab = 'share'"
        >{{ L('Bağlantı ile paylaş', 'Share link') }}</button>
      </div>

      <!-- ───────── Permissions tab ───────── -->
      <template v-if="tab === 'perms' && canManage">
        <div v-if="!storageRbac" class="fx-perm-warn">
          {{ L('Bu diskte RBAC kapalı — izinler yalnızca RBAC açık disklerde geçerli.', 'RBAC is off on this storage.') }}
        </div>
        <div v-if="err" class="fx-perm-warn">{{ err }}</div>
        <div v-if="loading" class="fx-perm-muted">{{ L('Yükleniyor…', 'Loading…') }}</div>
        <template v-else>
          <div class="fx-perm-section">
            <h4>{{ L('Doğrudan verilenler', 'Direct') }}</h4>
            <div v-if="!direct.length" class="fx-perm-muted">{{ L('Yok', 'None') }}</div>
            <div v-for="g in direct" :key="g.id" class="fx-perm-row">
              <span class="fx-perm-user">{{ glabel(g) }}</span>
              <select class="fx-perm-sel" :value="g.level"
                @change="changeLevel(g, ($event.target as HTMLSelectElement).value)">
                <option v-for="o in levels" :key="o.v" :value="o.v">{{ o.l }}</option>
              </select>
              <button class="fx-perm-del" :disabled="busy" @click="removeGrant(g)">✕</button>
            </div>
          </div>
          <div v-if="inherited.length" class="fx-perm-section">
            <h4>{{ L('Üst klasörden gelen', 'Inherited') }}</h4>
            <div v-for="g in inherited" :key="g.id" class="fx-perm-row fx-perm-inh">
              <span class="fx-perm-user">{{ glabel(g) }}</span>
              <span class="fx-perm-badge">{{ g.level }}</span>
              <span class="fx-perm-from" :title="g.path_prefix">↳ {{ g.path_prefix || '/' }}</span>
            </div>
          </div>
          <div class="fx-perm-section">
            <h4>{{ L('Kişi ekle', 'Add person') }}</h4>
            <div class="fx-perm-add">
              <div class="fx-perm-emailwrap">
                <input v-model="email" type="email" class="fx-perm-input" autocomplete="off"
                  :placeholder="L('e-posta adresi', 'email address')"
                  @input="onEmailInput" @keyup.enter="submitEmail" @focus="onEmailInput" />
                <ul v-if="showSuggest" class="fx-perm-suggest">
                  <li v-for="u in suggestions" :key="u.id" @mousedown.prevent="pickUser(u)">
                    <span class="fx-perm-suggest-name">{{ u.display_name || u.email }}</span>
                    <span class="fx-perm-suggest-meta">{{ u.email }} · {{ u.role }}</span>
                  </li>
                </ul>
              </div>
              <select v-model="level" class="fx-perm-sel">
                <option v-for="o in levels" :key="o.v" :value="o.v">{{ o.l }}</option>
              </select>
              <button class="fx-perm-btn fx-perm-btn--primary" :disabled="busy" @click="submitEmail">
                {{ L('Ekle', 'Add') }}
              </button>
            </div>
            <div v-if="noAccount" class="fx-perm-invite">
              <p class="fx-perm-muted">{{ L('Bu e-postada hesap yok. Ne yapmak istersiniz?', 'No account for this email.') }}</p>
              <div class="fx-perm-add">
                <select v-model="createRole" class="fx-perm-sel">
                  <option value="user">{{ L('Kullanıcı', 'User') }}</option>
                  <option value="viewer">{{ L('Görüntüleyen', 'Viewer') }}</option>
                </select>
                <button class="fx-perm-btn" :disabled="busy" @click="inviteCreateUser">{{ L('Yeni kullanıcı aç + yetki ver', 'Create user + grant') }}</button>
                <button class="fx-perm-btn" :disabled="busy" @click="inviteShare">{{ L('Paylaşım linki gönder', 'Send share link') }}</button>
              </div>
            </div>
            <div v-if="notice" class="fx-perm-notice">{{ notice }}</div>
            <div v-if="inviteResult?.tempPassword" class="fx-perm-reveal">
              {{ L('Geçici parola:', 'Temp password:') }} <code>{{ inviteResult.tempPassword }}</code>
            </div>
            <div v-if="inviteResult?.link" class="fx-perm-reveal">
              <a :href="inviteResult.link" target="_blank" rel="noopener">{{ inviteResult.link }}</a>
            </div>
          </div>
        </template>
      </template>

      <!-- ───────── Share tab ───────── -->
      <template v-if="tab === 'share'">
        <div class="fx-perm-section">
          <label class="fx-perm-check">
            <input type="checkbox" v-model="sharePwd" />
            {{ L('PIN ile koru', 'Protect with a PIN') }}
          </label>
          <div class="fx-perm-add" style="margin-top:8px">
            <button class="fx-perm-btn fx-perm-btn--primary" :disabled="shareBusy" @click="createLink">
              {{ L('Bağlantı oluştur', 'Create link') }}
            </button>
          </div>
          <div v-if="shareErr" class="fx-perm-warn">{{ shareErr }}</div>
          <div v-if="shareResult" class="fx-perm-reveal">
            <div class="fx-perm-add">
              <a :href="shareResult.url" target="_blank" rel="noopener" class="fx-perm-user">{{ shareResult.url }}</a>
              <button class="fx-perm-btn" @click="copy(shareResult.url)">{{ L('Kopyala', 'Copy') }}</button>
            </div>
            <p v-if="shareResult.pin" class="fx-perm-notice">PIN: <code>{{ shareResult.pin }}</code></p>
          </div>
        </div>
        <div class="fx-perm-section">
          <h4>{{ L('Mevcut bağlantılar', 'Existing links') }}</h4>
          <div v-if="!shares.length" class="fx-perm-muted">{{ L('Yok', 'None') }}</div>
          <div v-for="s in shares" :key="s.uuid" class="fx-perm-row">
            <span class="fx-perm-user">{{ s.url }}</span>
            <button class="fx-perm-btn" @click="copy(s.url)">{{ L('Kopyala', 'Copy') }}</button>
            <button class="fx-perm-del" :disabled="shareBusy" @click="revoke(s)">✕</button>
          </div>
        </div>
      </template>
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
  background: var(--fe-bg); color: var(--fe-text);
  width: min(560px, 94vw); max-height: 88vh; overflow: auto;
  border: 1px solid var(--fe-border); border-radius: var(--fe-radius-lg, 12px);
  padding: 18px 20px; box-shadow: var(--fe-shadow); font-size: 14px;
}
.fx-perm-head { display: flex; align-items: center; justify-content: space-between; }
.fx-perm-head h3 { margin: 0; font-size: 16px; font-weight: 600; color: var(--fe-text); }
.fx-perm-x { background: none; border: none; font-size: 18px; cursor: pointer; color: var(--fe-text-muted); }
.fx-perm-x:hover { color: var(--fe-text); }
.fx-perm-path { font-size: 12px; color: var(--fe-text-muted); margin: 2px 0 10px; word-break: break-all; }
.fx-perm-tabs { display: flex; gap: 4px; border-bottom: 1px solid var(--fe-border); margin-bottom: 6px; }
.fx-perm-tab {
  background: none; border: none; border-bottom: 2px solid transparent;
  padding: 6px 10px; cursor: pointer; color: var(--fe-text-muted); font-size: 13px; font-family: inherit;
}
.fx-perm-tab.is-active { color: var(--fe-text); border-bottom-color: var(--fe-primary); }
.fx-perm-section { margin-top: 14px; }
.fx-perm-section h4 {
  margin: 0 0 6px; font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; color: var(--fe-text-muted);
}
.fx-perm-row { display: flex; align-items: center; gap: 8px; padding: 5px 0; }
.fx-perm-inh { color: var(--fe-text-muted); font-size: 13px; }
.fx-perm-user { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--fe-text); }
.fx-perm-from { font-size: 11px; color: var(--fe-text-muted); }
.fx-perm-badge { font-size: 11px; padding: 1px 8px; border-radius: 999px; background: var(--fe-bg-hover); color: var(--fe-text); }
.fx-perm-sel, .fx-perm-input {
  padding: 6px 8px; border-radius: var(--fe-radius-sm, 6px); border: 1px solid var(--fe-border);
  background: var(--fe-bg-elev); color: var(--fe-text); font-size: 13px; font-family: inherit;
}
.fx-perm-sel:focus, .fx-perm-input:focus { outline: none; border-color: var(--fe-primary); }
.fx-perm-emailwrap { position: relative; flex: 1; min-width: 160px; }
.fx-perm-input { width: 100%; box-sizing: border-box; }
.fx-perm-suggest {
  position: absolute; top: calc(100% + 2px); left: 0; right: 0; z-index: 5; margin: 0; padding: 4px;
  list-style: none; background: var(--fe-bg-elev); border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-sm, 6px); box-shadow: var(--fe-shadow-sm); max-height: 200px; overflow: auto;
}
.fx-perm-suggest li { display: flex; flex-direction: column; padding: 5px 8px; border-radius: 5px; cursor: pointer; }
.fx-perm-suggest li:hover { background: var(--fe-bg-hover); }
.fx-perm-suggest-name { color: var(--fe-text); font-size: 13px; }
.fx-perm-suggest-meta { color: var(--fe-text-muted); font-size: 11px; }
.fx-perm-add { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.fx-perm-check { display: inline-flex; gap: 6px; align-items: center; font-size: 13px; color: var(--fe-text); }
.fx-perm-btn {
  padding: 6px 12px; border-radius: var(--fe-radius-sm, 6px); border: 1px solid var(--fe-border);
  background: var(--fe-bg-elev); color: var(--fe-text); font-size: 13px; font-family: inherit; cursor: pointer;
}
.fx-perm-btn:hover:not(:disabled) { background: var(--fe-bg-hover); }
.fx-perm-btn:disabled { opacity: 0.55; cursor: default; }
.fx-perm-btn--primary { background: var(--fe-primary); border-color: var(--fe-primary); color: var(--fe-text-on-primary); }
.fx-perm-btn--primary:hover:not(:disabled) { background: var(--fe-primary-hover); }
.fx-perm-del { background: none; border: none; color: var(--fe-danger); cursor: pointer; font-size: 14px; }
.fx-perm-warn {
  background: rgba(245, 158, 11, 0.14); border: 1px solid rgba(245, 158, 11, 0.4);
  border-radius: var(--fe-radius-sm, 6px); padding: 8px 10px; font-size: 13px; margin-top: 8px; color: var(--fe-text);
}
.fx-perm-notice { margin-top: 8px; font-size: 13px; color: var(--fe-text-muted); }
.fx-perm-reveal { margin-top: 6px; font-size: 13px; word-break: break-all; color: var(--fe-text); }
.fx-perm-reveal code { font-family: var(--fe-font-mono); }
.fx-perm-reveal a { color: var(--fe-primary); }
.fx-perm-muted { color: var(--fe-text-muted); font-size: 13px; }
.fx-perm-invite {
  margin-top: 10px; padding: 10px; border-radius: var(--fe-radius-sm, 6px);
  background: var(--fe-bg-elev); border: 1px solid var(--fe-border);
}
</style>
