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
import DataTable, { type DataColumn } from './DataTable.vue';
import type { ContextAction } from './ContextMenu.vue';
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
const { t, formatDate } = useLocale(locale);

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

/** The row's verbs, behind its one `Actions` control. ⚠ `remove` keeps the
 *  two-step confirmation it had as a loose button: the first pick arms it and
 *  the label becomes "Confirm", the second pick deletes the key. */
function rowActions(k: SSHPublicKey): ContextAction[] {
  /* The whole control used to be `:disabled="busy"`; the table draws the
     control now, so each verb carries it. */
  return [
    {
      key: 'toggle',
      label: k.disabled_at ? t('conn.sshkeys.enable') : t('conn.sshkeys.disable'),
      icon: k.disabled_at ? 'check' : 'lock',
      disabled: busy.value,
    },
    {
      key: 'remove',
      label: confirmRemove.value === k.id ? t('conn.sshkeys.confirm') : t('conn.sshkeys.remove'),
      icon: 'delete',
      danger: true,
      disabled: busy.value,
    },
  ];
}

/** The list's columns — the product's one table (DataTable). */
const columns = computed<DataColumn<SSHPublicKey>[]>(() => [
  {
    id: 'name',
    label: t('conn.sshkeys.col.name'),
    sortable: true,
    width: 180,
    format: (k) => k.name || t('conn.sshkeys.noName'),
  },
  {
    id: 'fingerprint',
    label: t('conn.sshkeys.col.fingerprint'),
    sortable: true,
    width: 240,
    sortValue: (k) => fingerprintOf(k),
    title: (k) => fingerprintOf(k),
  },
  {
    id: 'lastUsed',
    label: t('conn.sshkeys.col.lastUsed'),
    sortable: true,
    sortDir: 'desc',
    width: 140,
    format: (k) => (k.last_used_at ? shortDate(k.last_used_at) : t('conn.sshkeys.neverUsed')),
    sortValue: (k) => (k.last_used_at ? new Date(k.last_used_at).getTime() : null),
  },
]);

function onRowAction(key: string, k: SSHPublicKey) {
  if (key === 'toggle') void toggle(k);
  else if (key === 'remove') void drop(k);
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

/* zaman:z1 — one date formatter for the package. This was a bare
 * `toLocaleDateString()`: the browser's locale and the browser's zone, so a
 * key minted at 23:30 in Istanbul was dated a day early for a viewer reading
 * UTC and came out in the wrong language besides. */
function shortDate(v?: string | null): string {
  const ms = new Date(v ?? '').getTime();
  return Number.isNaN(ms) ? '' : formatDate(ms);
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

    <!-- ⚠ THE table (DataTable — the explorer's own), not a table of its own:
         reachable from the admin panel's Connections menu and from the
         explorer, so it resizes, sorts, hides and moves columns and remembers
         that on the account (`conn.sshkeys`), like every other table. -->
    <p v-if="loading" class="fe-s3keys__muted">…</p>
    <DataTable
      v-else
      table-id="conn.sshkeys"
      :columns="columns"
      :rows="keys"
      row-key="id"
      :locale="locale"
      :empty="t('conn.sshkeys.empty')"
      :row-class="(k: SSHPublicKey) => (k.disabled_at ? 'is-muted' : undefined)"
      :row-actions="rowActions"
      :row-actions-test-id="(k: SSHPublicKey) => `ssh-key-actions-${k.id}`"
      @row-action="(key: string, k: SSHPublicKey) => onRowAction(key, k)"
    >
      <template #cell-fingerprint="{ row }">
        <code class="tbl-mono">{{ fingerprintOf(row) }}</code>
      </template>
    </DataTable>
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
