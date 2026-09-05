<script setup lang="ts">
/**
 * SSHKeysPanel — register, disable and remove the keys this account signs in
 * with over SFTP.
 *
 * ⚠⚠ This is not a convenience screen. `ssh-copy-id` — the command everybody
 * reaches for — appends to `~/.ssh/authorized_keys` over a shell, and filex has
 * no shell. Without a box to paste a key into, public-key authentication is
 * unreachable and every user ends up sending their account password to a file
 * server instead. That is why the screen shipped WITH the endpoint rather than
 * after it.
 *
 * One component, every surface: the desktop app and the web app mount this same
 * file (see S3KeysPanel for the same rule stated at length).
 */
import { computed, onMounted, ref } from 'vue';
import type { ExplorerConfig, LocaleCode } from '../types/ExplorerConfig';
import type { FTPSFacts, SSHPublicKey } from '../types/SSHKeys';
import { useLocale } from '../composables/useLocale';
import { useSSHKeys } from '../composables/useSSHKeys';
import { resolveLocale } from '../locales/resolve';

const props = defineProps<{
  config: ExplorerConfig;
  /**
   * Draw the key list and the paste box.
   *
   * ⚠ False for FTPS, which authenticates with a password or an API token and
   * has no use for an SSH key — the panel is still MOUNTED there because it is
   * the call that reports where the FTP endpoint listens and under what login
   * name. Showing a key box on the FTPS page would invite somebody to register
   * a key that protocol will never look at.
   */
  keysVisible?: boolean;
}>();

const showKeys = computed(() => props.keysVisible !== false);

const emit = defineEmits<{
  /** What the guide should render with: where to connect and as whom. */
  (e: 'active', v: {
    host: string;
    port: number;
    login: string;
    enabled: boolean;
    hasKey: boolean;
    ftps?: FTPSFacts;
  }): void;
}>();

const locale = computed<LocaleCode>(() => resolveLocale(props.config.locale));
const { t } = useLocale(locale);

const { keys, connection, loading, error, canAdd, hasUsableKey, load, add, setDisabled, remove } =
  useSSHKeys(props.config);

const pasted = ref('');
const name = ref('');
const busy = ref(false);
const confirmRemove = ref<number | null>(null);

function publish() {
  const c = connection.value;
  if (!c) return;
  emit('active', {
    host: c.host,
    port: c.port,
    login: c.login,
    enabled: c.enabled,
    hasKey: hasUsableKey.value,
    // The FTPS endpoint travels with the SSH facts: for the user this is one
    // question ("how do I reach this from a program?"), and two calls to
    // answer it would be two chances to disagree.
    ftps: c.ftps,
  });
}

onMounted(async () => {
  await load();
  publish();
});

async function submit() {
  if (!pasted.value.trim()) return;
  busy.value = true;
  try {
    if (await add(pasted.value.trim(), name.value.trim() || undefined)) {
      pasted.value = '';
      name.value = '';
    }
    publish();
  } finally {
    busy.value = false;
  }
}

async function toggle(k: SSHPublicKey) {
  busy.value = true;
  try {
    await setDisabled(k.id, !k.disabled_at);
    publish();
  } finally {
    busy.value = false;
  }
}

async function drop(k: SSHPublicKey) {
  if (confirmRemove.value !== k.id) {
    confirmRemove.value = k.id;
    return;
  }
  confirmRemove.value = null;
  busy.value = true;
  try {
    await remove(k.id);
    publish();
  } finally {
    busy.value = false;
  }
}

function shortDate(v?: string | null): string {
  if (!v) return '';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString();
}

/** Shown the way OpenSSH prints it, so it can be compared with `ssh-keygen -lf`. */
function fingerprintOf(k: SSHPublicKey): string {
  return 'SHA256:' + k.fingerprint;
}
</script>

<template>
  <section v-if="showKeys" class="fe-s3keys" data-testid="ssh-keys">
    <header class="fe-s3keys__head">
      <h4 class="fe-s3keys__title">{{ t('conn.sshkeys.title') }}</h4>
      <p class="fe-s3keys__lead">{{ t('conn.sshkeys.lead') }}</p>
    </header>

    <p v-if="connection && !connection.enabled" class="fe-s3keys__warn">
      {{ t('conn.sshkeys.disabled') }}
    </p>
    <p v-if="canAdd === false" class="fe-s3keys__muted">{{ t('conn.sshkeys.cannotAdd') }}</p>
    <p v-if="error" class="fe-s3keys__warn">{{ error }}</p>

    <div v-if="canAdd" class="fe-sshkeys__form">
      <textarea
        v-model="pasted"
        class="fe-cfield__input fe-sshkeys__paste"
        rows="2"
        spellcheck="false"
        :placeholder="t('conn.sshkeys.paste')"
        data-testid="ssh-key-input"
      ></textarea>
      <div class="fe-sshkeys__row">
        <input
          v-model="name"
          class="fe-cfield__input"
          :placeholder="t('conn.sshkeys.name')"
          data-testid="ssh-key-name"
        />
        <button class="fe-s3keys__btn" :disabled="busy" data-testid="ssh-key-add" @click="submit">
          {{ t('conn.sshkeys.add') }}
        </button>
      </div>
      <p class="fe-s3keys__hint">{{ t('conn.sshkeys.noCopyId') }}</p>
    </div>

    <p v-if="loading" class="fe-s3keys__muted">…</p>
    <table v-else-if="keys.length" class="fe-s3keys__table">
      <thead>
        <tr>
          <th>{{ t('conn.sshkeys.col.name') }}</th>
          <th>{{ t('conn.sshkeys.col.fingerprint') }}</th>
          <th>{{ t('conn.sshkeys.col.lastUsed') }}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="k in keys" :key="k.id" :class="{ 'is-off': !!k.disabled_at }">
          <td>{{ k.name || t('conn.sshkeys.noName') }}</td>
          <td><code>{{ fingerprintOf(k) }}</code></td>
          <td>{{ k.last_used_at ? shortDate(k.last_used_at) : t('conn.sshkeys.neverUsed') }}</td>
          <td class="fe-s3keys__actions">
            <button class="fe-s3keys__link" :disabled="busy" @click="toggle(k)">
              {{ k.disabled_at ? t('conn.sshkeys.enable') : t('conn.sshkeys.disable') }}
            </button>
            <button class="fe-s3keys__link is-danger" :disabled="busy" @click="drop(k)">
              {{ confirmRemove === k.id ? t('conn.sshkeys.confirm') : t('conn.sshkeys.remove') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else-if="canAdd" class="fe-s3keys__muted">{{ t('conn.sshkeys.empty') }}</p>
  </section>
</template>

<style>
/* The layout is shared with the access-key panel (fe-s3keys__*); only the
   paste box is different, because a public key is two lines long. */
.fe-sshkeys__form {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.fe-sshkeys__paste {
  width: 100%;
  font-family: var(--fe-font-mono);
  font-size: 12px;
  resize: vertical;
  overflow-wrap: anywhere;
}
.fe-sshkeys__row {
  display: flex;
  gap: 8px;
  align-items: center;
}
.fe-sshkeys__row .fe-cfield__input {
  flex: 1;
}
</style>
