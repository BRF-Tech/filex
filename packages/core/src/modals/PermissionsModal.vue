<script setup lang="ts">
// PermissionsModal — the RBAC "İzinler" panel for a file/folder. Owners (and
// admins) open it from the explorer context menu. It lists direct + inherited
// grants, lets the owner add/change/remove per-user access, and drives the
// email-invite flow (existing user → grant, admin → create user, else share).
//
// All access decisions are enforced server-side; this is just the UI over
// /api/files/permissions (+ /resolve, /invite).
import { ref, onMounted, computed } from 'vue';
import type { FileApi, Grant } from '../composables/useFileApi';

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

const loading = ref(true);
const err = ref('');
const direct = ref<Grant[]>([]);
const inherited = ref<Grant[]>([]);
const storageRbac = ref(true);

// Add / invite form.
const email = ref('');
const level = ref<'viewer' | 'editor' | 'owner'>('viewer');
const busy = ref(false);
const notice = ref('');
const noAccount = ref(false); // set after a resolve that finds no user
const createRole = ref<'user' | 'viewer'>('user');
const inviteResult = ref<{ link?: string; tempPassword?: string; mode?: string } | null>(null);

const levels: Array<{ v: 'viewer' | 'editor' | 'owner'; l: string }> = [
  { v: 'viewer', l: L('Görüntüleyen', 'Viewer') },
  { v: 'editor', l: L('Düzenleyen', 'Editor') },
  { v: 'owner', l: L('Sahip', 'Owner') },
];

async function reload() {
  loading.value = true;
  err.value = '';
  try {
    const r = await props.api.listPermissions(props.path);
    direct.value = r.direct ?? [];
    inherited.value = r.inherited ?? [];
    storageRbac.value = r.storage_rbac;
  } catch (e) {
    err.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

onMounted(reload);

function resetForm() {
  email.value = '';
  level.value = 'viewer';
  noAccount.value = false;
  inviteResult.value = null;
  notice.value = '';
}

// Step 1: resolve the email → existing user (direct grant) or invite flow.
async function submitEmail() {
  const addr = email.value.trim().toLowerCase();
  if (!addr || !addr.includes('@')) {
    notice.value = L('Geçerli bir e-posta girin.', 'Enter a valid email.');
    return;
  }
  busy.value = true;
  notice.value = '';
  inviteResult.value = null;
  try {
    const res = await props.api.resolveEmail(addr);
    if (res.found && res.user) {
      await props.api.addPermission({ path: props.path, user_id: res.user.id, level: level.value });
      resetForm();
      await reload();
      notice.value = L('Yetki verildi.', 'Access granted.');
    } else {
      // No account — offer create-user (admin) or share link.
      noAccount.value = true;
    }
  } catch (e) {
    notice.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}

// Step 2a: admin creates a new account + grants.
async function inviteCreateUser() {
  busy.value = true;
  notice.value = '';
  try {
    const r = await props.api.invitePermission({
      path: props.path,
      email: email.value.trim().toLowerCase(),
      level: level.value,
      create_user: true,
      role: createRole.value,
    });
    inviteResult.value = { mode: r.mode, tempPassword: r.temp_password };
    if (r.emailed) notice.value = L('Kullanıcı açıldı, davet e-postası gönderildi.', 'User created, invite emailed.');
    else notice.value = L('Kullanıcı açıldı. Geçici parolayı iletin (mail yapılandırılmamış).', 'User created. Share the temporary password (mail not configured).');
    noAccount.value = false;
    email.value = '';
    await reload();
  } catch (e) {
    notice.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}

// Step 2b: share a public link to the email (no account created).
async function inviteShare() {
  busy.value = true;
  notice.value = '';
  try {
    const r = await props.api.invitePermission({
      path: props.path,
      email: email.value.trim().toLowerCase(),
      level: level.value,
    });
    inviteResult.value = { mode: r.mode, link: r.url };
    if (r.emailed) notice.value = L('Paylaşım linki e-postayla gönderildi.', 'Share link emailed.');
    else notice.value = L('Paylaşım linki oluşturuldu (mail yapılandırılmamış).', 'Share link created (mail not configured).');
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
  try {
    await props.api.updatePermission(g.id, newLevel);
    await reload();
  } catch (e) {
    notice.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}

async function removeGrant(g: Grant) {
  busy.value = true;
  try {
    await props.api.deletePermission(g.id);
    await reload();
  } catch (e) {
    notice.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}

function label(g: Grant): string {
  return g.user_display_name || g.user_email || `#${g.user_id}`;
}
</script>

<template>
  <div class="fx-perm-overlay" @click.self="emit('close')">
    <div class="fx-perm-modal">
      <header class="fx-perm-head">
        <h3>{{ L('İzinler', 'Permissions') }}</h3>
        <button class="fx-perm-x" @click="emit('close')" aria-label="close">✕</button>
      </header>
      <p class="fx-perm-path">{{ path }}</p>

      <div v-if="!storageRbac" class="fx-perm-warn">
        {{ L('Bu diskte RBAC kapalı — izinler yalnızca RBAC açık disklerde geçerli.', 'RBAC is off on this storage — permissions apply only to RBAC-enabled storages.') }}
      </div>

      <div v-if="err" class="fx-perm-warn">{{ err }}</div>
      <div v-if="loading" class="fx-perm-muted">{{ L('Yükleniyor…', 'Loading…') }}</div>

      <template v-else>
        <!-- Direct grants -->
        <div class="fx-perm-section">
          <h4>{{ L('Doğrudan verilenler', 'Direct') }}</h4>
          <div v-if="!direct.length" class="fx-perm-muted">{{ L('Yok', 'None') }}</div>
          <div v-for="g in direct" :key="g.id" class="fx-perm-row">
            <span class="fx-perm-user">{{ label(g) }}</span>
            <select
              class="fx-perm-sel"
              :value="g.level"
              @change="changeLevel(g, ($event.target as HTMLSelectElement).value)"
            >
              <option v-for="o in levels" :key="o.v" :value="o.v">{{ o.l }}</option>
            </select>
            <button class="fx-perm-del" :disabled="busy" @click="removeGrant(g)">✕</button>
          </div>
        </div>

        <!-- Inherited grants (read-only) -->
        <div v-if="inherited.length" class="fx-perm-section">
          <h4>{{ L('Üst klasörden gelen', 'Inherited') }}</h4>
          <div v-for="g in inherited" :key="g.id" class="fx-perm-row fx-perm-inh">
            <span class="fx-perm-user">{{ label(g) }}</span>
            <span class="fx-perm-badge">{{ g.level }}</span>
            <span class="fx-perm-from" :title="g.path_prefix">↳ {{ g.path_prefix || '/' }}</span>
          </div>
        </div>

        <!-- Add / invite -->
        <div class="fx-perm-section">
          <h4>{{ L('Kişi ekle', 'Add person') }}</h4>
          <div class="fx-perm-add">
            <input
              v-model="email"
              type="email"
              class="fx-perm-input"
              :placeholder="L('e-posta adresi', 'email address')"
              @keyup.enter="submitEmail"
            />
            <select v-model="level" class="fx-perm-sel">
              <option v-for="o in levels" :key="o.v" :value="o.v">{{ o.l }}</option>
            </select>
            <button class="fe-btn fe-btn--primary" :disabled="busy" @click="submitEmail">
              {{ L('Ekle', 'Add') }}
            </button>
          </div>

          <!-- No-account branch -->
          <div v-if="noAccount" class="fx-perm-invite">
            <p class="fx-perm-muted">
              {{ L('Bu e-postada hesap yok. Ne yapmak istersiniz?', 'No account for this email. What next?') }}
            </p>
            <div class="fx-perm-add">
              <select v-model="createRole" class="fx-perm-sel">
                <option value="user">{{ L('Kullanıcı', 'User') }}</option>
                <option value="viewer">{{ L('Görüntüleyen', 'Viewer') }}</option>
              </select>
              <button class="fe-btn" :disabled="busy" @click="inviteCreateUser">
                {{ L('Yeni kullanıcı aç + yetki ver', 'Create user + grant') }}
              </button>
              <button class="fe-btn" :disabled="busy" @click="inviteShare">
                {{ L('Paylaşım linki gönder', 'Send share link') }}
              </button>
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
    </div>
  </div>
</template>

<style scoped>
.fx-perm-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 9999;
}
.fx-perm-modal {
  background: var(--fe-bg, #fff);
  color: var(--fe-fg, #18181b);
  width: min(560px, 94vw);
  max-height: 88vh;
  overflow: auto;
  border-radius: 12px;
  padding: 18px 20px;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.3);
}
:global(html.dark) .fx-perm-modal {
  background: #18181b;
  color: #f4f4f5;
}
.fx-perm-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.fx-perm-head h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}
.fx-perm-x {
  background: none;
  border: none;
  font-size: 18px;
  cursor: pointer;
  color: inherit;
  opacity: 0.6;
}
.fx-perm-x:hover {
  opacity: 1;
}
.fx-perm-path {
  font-size: 12px;
  opacity: 0.6;
  margin: 2px 0 12px;
  word-break: break-all;
}
.fx-perm-section {
  margin-top: 14px;
}
.fx-perm-section h4 {
  margin: 0 0 6px;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.55;
}
.fx-perm-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 0;
}
.fx-perm-inh {
  opacity: 0.7;
  font-size: 13px;
}
.fx-perm-user {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fx-perm-from {
  font-size: 11px;
  opacity: 0.6;
}
.fx-perm-badge {
  font-size: 11px;
  padding: 1px 8px;
  border-radius: 999px;
  background: rgba(120, 120, 120, 0.2);
}
.fx-perm-sel,
.fx-perm-input {
  padding: 5px 8px;
  border-radius: 7px;
  border: 1px solid rgba(120, 120, 120, 0.4);
  background: transparent;
  color: inherit;
  font-size: 13px;
}
.fx-perm-input {
  flex: 1;
}
.fx-perm-add {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.fx-perm-del {
  background: none;
  border: none;
  color: #ef4444;
  cursor: pointer;
  font-size: 14px;
}
.fx-perm-warn {
  background: rgba(245, 158, 11, 0.15);
  border: 1px solid rgba(245, 158, 11, 0.4);
  border-radius: 8px;
  padding: 8px 10px;
  font-size: 13px;
  margin-top: 8px;
}
.fx-perm-notice {
  margin-top: 8px;
  font-size: 13px;
  opacity: 0.85;
}
.fx-perm-reveal {
  margin-top: 6px;
  font-size: 13px;
  word-break: break-all;
}
.fx-perm-muted {
  opacity: 0.55;
  font-size: 13px;
}
.fx-perm-invite {
  margin-top: 10px;
  padding: 10px;
  border-radius: 8px;
  background: rgba(120, 120, 120, 0.08);
}
</style>
