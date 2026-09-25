<script setup lang="ts">
// Release awareness page (docs/UPDATES.md).
//
// The whole point of this screen is that an operator should never have to ask
// "am I current, and if not, what happens next?". It answers three things at a
// glance: the running version, whether something newer exists, and who acts —
// filex itself (patch under an allowing policy), one click here, or a manual
// upgrade with the commands spelled out.
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import axios from 'axios';
import {
  ArrowUpCircle, CheckCircle2, RefreshCw, ShieldAlert, Terminal, Copy, AlertTriangle,
} from 'lucide-vue-next';

import { UpdatesApi, type UpdateRelease, type UpdateStatus } from '@/api/updates';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { formatDate } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';
import { DataTable, type DataColumn } from '@brftech/filex-core';

const { t, te, locale } = useI18n();
const toast = useToastStore();

const loading = ref(true);
const checking = ref(false);
const applying = ref(false);
// A backend older than this feature has no /admin/update; say so calmly
// instead of rendering an error.
const unsupported = ref(false);
const loadError = ref<string | null>(null);
const status = ref<UpdateStatus | null>(null);

function isMissingEndpoint(e: unknown): boolean {
  if (!axios.isAxiosError(e)) return false;
  const s = e.response?.status;
  return s === 404 || s === 405 || s === 501;
}

async function load() {
  loading.value = true;
  loadError.value = null;
  unsupported.value = false;
  try {
    status.value = await UpdatesApi.status();
  } catch (e: unknown) {
    if (isMissingEndpoint(e)) unsupported.value = true;
    else loadError.value = extractError(e, t('errors.generic'));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

async function checkNow() {
  checking.value = true;
  try {
    status.value = await UpdatesApi.check();
    // The sentence, not the raw error: the page keeps that as a second line.
    if (status.value?.check_error) toast.error(t('updates.checkFailedWords'));
    else if (status.value?.action === 'none') toast.success(t('updates.upToDate'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    checking.value = false;
  }
}

async function applyNow() {
  applying.value = true;
  try {
    const res = await UpdatesApi.apply();
    if (res.error) toast.error(res.error);
    else if (res.applied) toast.success(t('updates.applied', { version: res.applied }));
    else if (res.message) toast.success(res.message);
    status.value = await UpdatesApi.status();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    applying.value = false;
  }
}

async function copyInstructions() {
  const lines = status.value?.instructions ?? [];
  try {
    await navigator.clipboard.writeText(lines.join('\n'));
    toast.success(t('common.copied'));
  } catch {
    toast.error(t('errors.generic'));
  }
}

const s = computed(() => status.value);
const hasUpdate = computed(() => !!s.value && s.value.action !== 'none');
/** Only ActionConfirm gets a button — auto needs no prompt and instruct cannot
 *  be performed from here. */
const canApplyHere = computed(() => s.value?.action === 'confirm' && s.value?.can_self_apply);
/** Who owns the binary, as the mode badge says it. A package manager is
 *  named (a product name, the same in every language); a package install
 *  whose manager is unknown says only that much. */
const modeLabel = computed(() => {
  const st = s.value;
  if (st?.mode === 'package') {
    return st.package_manager_name
      ? t('updates.mode.package', { manager: st.package_manager_name })
      : t('updates.mode.packageUnknown');
  }
  return t('updates.mode.' + (st?.mode === 'docker' ? 'docker' : 'binary'));
});
/** Why the commands below are a package manager's and not filex's own. */
const packageHowTo = computed(() => {
  const st = s.value;
  if (st?.mode !== 'package') return '';
  return st.package_manager_name
    ? t('updates.packageHowTo', { manager: st.package_manager_name })
    : t('updates.packageHowToUnknown');
});
/** The saved policy by its name ("install patches"), not the setting's value. */
const policyName = computed(() => {
  const p = s.value?.policy ?? '';
  return te(`updates.policyName.${p}`) ? t(`updates.policyName.${p}`) : p;
});
/** The policy badge says what this install DOES by itself. The server works
 *  that out (`behavior`, and `policy_limit` when it is less than the saved
 *  policy); the page words it and derives nothing from mode + policy (#72: a
 *  Homebrew install read "install patches", which it never does). */
const policyBadge = computed(() => {
  const st = s.value;
  if (st?.policy_limit && st.behavior && te(`updates.behavior.${st.behavior}`)) {
    return t(`updates.behavior.${st.behavior}`);
  }
  return t('updates.policyIs', { policy: policyName.value });
});
/** Why the saved policy has no (or less) effect here. It is kept as saved —
 *  the sentence names it. A package manager without a name is "your package
 *  manager". */
const policyNote = computed(() => {
  const st = s.value;
  const limit = st?.policy_limit;
  if (!st || !limit) return '';
  const key = limit === 'package' && !st.package_manager_name ? 'packageUnknown' : limit;
  if (!te(`updates.policyLimit.${key}`)) return '';
  return t(`updates.policyLimit.${key}`, { policy: policyName.value, manager: st.package_manager_name ?? '' });
});
/** The manager's command, beside the sentence that names the manager. */
const policyCommand = computed(() =>
  s.value?.policy_limit === 'package' && s.value.package_manager_name ? s.value.upgrade_command ?? '' : '',
);
// ⚠ Badge takes a `tone`; these were `variant`s, which it does not have, so
// every badge on this page was grey — a security release included.
const stepTone = computed(() => {
  switch (s.value?.step) {
    case 'major': return 'amber';
    case 'minor': return 'sky';
    default: return 'emerald';
  }
});

/** The versions a jump goes over: one per row, with what each one carries.
 *
 * The explorer's table (DataTable), remembered under `admin.updates.skipped`.
 * The whole list arrives in one answer, so the table sorts it itself; the
 * version compares digit groups as numbers (0.9 before 0.10), and the flags
 * sort by weight — a security fix outranks a migration. */
const skippedColumns = computed<DataColumn<UpdateRelease>[]>(() => [
  { id: 'version', label: t('versions.col.version'), sortable: true, width: 140 },
  {
    id: 'flags',
    label: t('common.flags'),
    sortable: true,
    sortDir: 'desc',
    width: 200,
    sortValue: (r) => (r.security ? 2 : 0) + (r.migrations ? 1 : 0),
  },
  {
    id: 'notes',
    label: t('common.notes'),
    sortable: true,
    width: 320,
    format: (r) => r.notes || '—',
    sortValue: (r) => r.notes || null,
  },
]);
</script>

<template>
  <div class="space-y-4 max-w-2xl">
    <div class="flex items-center gap-2">
      <ArrowUpCircle class="h-6 w-6 text-brand-600 dark:text-brand-400" />
      <div>
        <h1 class="text-xl font-semibold">{{ t('updates.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('updates.subtitle') }}</p>
      </div>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div
      v-else-if="unsupported"
      class="flex items-start gap-3 rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
    >
      <ShieldAlert class="mt-0.5 h-5 w-5 shrink-0" />
      <p>{{ t('updates.unsupported') }}</p>
    </div>

    <div
      v-else-if="loadError"
      class="space-y-3 rounded-xl border border-rose-300 bg-rose-50 p-4 text-sm text-rose-700 dark:border-rose-700/60 dark:bg-rose-900/20 dark:text-rose-200"
    >
      <p>{{ loadError }}</p>
      <Button size="sm" variant="outline" @click="load">{{ t('common.refresh') }}</Button>
    </div>

    <template v-else-if="s">
      <!-- A binary was installed but this process is still the old one. This
           state is invisible everywhere else, so it leads. -->
      <div
        v-if="s.restart_required"
        class="flex items-start gap-3 rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
      >
        <AlertTriangle class="mt-0.5 h-5 w-5 shrink-0" />
        <p>{{ t('updates.restartRequired', { version: s.last_applied }) }}</p>
      </div>

      <div class="card card-body space-y-3">
        <div class="flex items-start justify-between gap-3">
          <div>
            <div class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('updates.current') }}</div>
            <div class="text-lg font-semibold">{{ s.current }}</div>
          </div>
          <div class="flex flex-wrap items-center justify-end gap-2">
            <Badge data-testid="updates-mode">{{ modeLabel }}</Badge>
            <!-- ⚠ The policy by name ("politika: manual" printed the setting's
                 value); the colon lives in the message. Where the install
                 cannot carry the policy out, what it does instead ("Announces
                 only"), as the server says. -->
            <Badge data-testid="updates-policy">{{ policyBadge }}</Badge>
          </div>
        </div>

        <!-- The saved policy is kept, and said to have no effect here — with
             who upgrades instead and the command. -->
        <p v-if="policyNote" class="text-sm text-zinc-600 dark:text-zinc-300" data-testid="updates-policy-note">
          <span data-testid="updates-policy-note-text">{{ policyNote }}</span>
          <code
            v-if="policyCommand"
            class="ms-2 rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-xs dark:bg-zinc-800"
            data-testid="updates-policy-command"
          >{{ policyCommand }}</code>
        </p>

        <!-- Up to date -->
        <div v-if="!hasUpdate" class="flex items-center gap-2 text-sm text-emerald-700 dark:text-emerald-300">
          <CheckCircle2 class="h-4 w-4" /> {{ t('updates.upToDate') }}
        </div>

        <!-- Something newer exists -->
        <div v-else class="space-y-3">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('updates.available') }}</span>
            <span class="text-lg font-semibold">{{ s.latest?.version }}</span>
            <Badge :tone="stepTone">{{ t('updates.step.' + s.step) }}</Badge>
            <Badge v-if="s.latest?.security" tone="rose">{{ t('updates.security') }}</Badge>
            <Badge v-if="s.latest?.migrations" tone="amber">{{ t('updates.migrations') }}</Badge>
          </div>

          <p v-if="s.reason" class="text-sm text-zinc-600 dark:text-zinc-300">{{ s.reason }}</p>

          <a
            v-if="s.latest?.notes_url"
            :href="s.latest.notes_url"
            target="_blank"
            rel="noopener"
            class="inline-block text-sm text-brand-600 hover:underline dark:text-brand-400"
          >{{ t('updates.releaseNotes') }}</a>

          <!-- Everything being skipped over, so a multi-version jump is never
               a surprise. -->
          <!-- ⚠ A version, its flags and its notes per row is a table, and it
               was a `<ul>` on a `bg-zinc-50 dark:bg-zinc-800/50` ground that
               no palette could move. The shared table, with the heading in
               its own toolbar slot. -->
          <DataTable
            v-if="s.skipped?.length"
            table-id="admin.updates.skipped"
            :columns="skippedColumns"
            :rows="s.skipped"
            row-key="version"
            data-testid="updates-skipped"
          >
            <template #toolbar>
              <span class="text-sm font-medium">{{ t('updates.includes') }}</span>
            </template>
            <template #cell-version="{ row }">
              <span class="tbl-mono">{{ row.version }}</span>
            </template>
            <template #cell-flags="{ row }">
              <!-- ONE root, the pills wrapping inside it: two Badges as two
                   flex items of the cell are squeezed below their labels in a
                   narrow column (web/tests/ui/tablePinnedActions → "a Badge
                   shares its cell with nothing"). -->
              <div class="flex flex-wrap gap-1">
                <Badge v-if="row.migrations" tone="amber">{{ t('updates.migrations') }}</Badge>
                <Badge v-if="row.security" tone="rose">{{ t('updates.security') }}</Badge>
                <template v-if="!row.migrations && !row.security">—</template>
              </div>
            </template>
          </DataTable>

          <div class="flex flex-wrap gap-2">
            <Button v-if="canApplyHere" :disabled="applying" @click="applyNow">
              <Spinner v-if="applying" class="me-2 h-4 w-4" />
              {{ t('updates.applyNow') }}
            </Button>
            <span v-else-if="s.action === 'auto'" class="text-sm text-zinc-500 dark:text-zinc-400">
              {{ t('updates.willAutoApply') }}
            </span>
          </div>

          <!-- Manual path: the commands for THIS install shape, generated by
               the server (only it knows how filex was installed). -->
          <div v-if="s.instructions?.length" class="space-y-2" data-testid="updates-howto">
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2 text-sm font-medium">
                <Terminal class="h-4 w-4" /> {{ t('updates.howTo') }}
              </div>
              <Button size="sm" variant="outline" @click="copyInstructions">
                <Copy class="me-1 h-3 w-3" /> {{ t('common.copy') }}
              </Button>
            </div>
            <!-- A package manager owns the binary: say so before its command,
                 or "brew upgrade" under a page about filex updates reads
                 like a typo. -->
            <p v-if="packageHowTo" class="text-sm text-zinc-600 dark:text-zinc-300" data-testid="updates-package-howto">
              {{ packageHowTo }}
            </p>
            <pre class="overflow-x-auto rounded-lg bg-zinc-900 p-3 text-xs text-zinc-100"><code>{{ s.instructions.join('\n') }}</code></pre>
          </div>
        </div>

        <div class="flex items-center justify-between border-t border-zinc-200 pt-3 text-xs text-zinc-500 dark:border-zinc-700 dark:text-zinc-400">
          <!-- ⚠ A date in the reader's format, and the colon in the message
               (it printed "Son kontrol: 2026-09-22T11:05:58Z"). -->
          <span v-if="s.checked_at" data-testid="updates-checked-at">{{
            t('updates.checkedAtWhen', { when: formatDate(s.checked_at, locale) })
          }}</span>
          <span v-else-if="!s.enabled">{{ t('updates.disabled') }}</span>
          <span v-else>—</span>
          <Button size="sm" variant="outline" :disabled="checking" @click="checkNow">
            <RefreshCw class="me-1 h-3 w-3" :class="checking ? 'animate-spin' : ''" />
            {{ t('updates.checkNow') }}
          </Button>
        </div>

        <!-- ⚠ What happened, in words; the raw error is the second line
             (QA, 2026-09-21: "Check failed: Get \"https://…\": dial tcp…"
             was the whole message). -->
        <div v-if="s.check_error" class="text-xs text-amber-600 dark:text-amber-400" data-testid="updates-check-error">
          <p>{{ t('updates.checkFailedWords') }}</p>
          <p class="font-mono text-[11px] text-zinc-400 dark:text-zinc-500 break-all" data-testid="updates-check-detail">
            {{ s.check_error }}
          </p>
        </div>
        <div v-if="s.last_apply_error" class="text-xs text-rose-600 dark:text-rose-400" data-testid="updates-apply-error">
          <p>{{ t('updates.applyFailedWords') }}</p>
          <p class="font-mono text-[11px] text-zinc-400 dark:text-zinc-500 break-all">{{ s.last_apply_error }}</p>
        </div>
      </div>
    </template>
  </div>
</template>
