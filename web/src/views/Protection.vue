<script setup lang="ts">
// koru:k3 — Protection settings page (trash retention, version policy,
// antivirus status AND configuration). Follows the Settings.vue card/form
// pattern.
//
// ⚠ The antivirus block shows two things that can disagree, and the
// disagreement is the useful part: STATUS (is scanning on, does clamd answer)
// versus SETTINGS (what was last saved). Three of the settings — the switch,
// the transport and the clamd address — are in force only after a restart, so
// the page says so when you change them and keeps a band up until it happens.
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import axios from 'axios';
import {
  Save,
  Shield,
  ShieldAlert,
  History,
  Trash2,
  ExternalLink,
  Link2,
  RefreshCw,
} from 'lucide-vue-next';

import { ProtectionApi, type ProtectionAntivirus, type ProtectionPatch } from '@/api/protection';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';

const DOCS_URL = 'https://github.com/BRF-Tech/filex/blob/main/docs/PROTECTION.md';

const { t } = useI18n();
const toast = useToastStore();

const loading = ref(true);
// Older backends don't expose /admin/protection yet — show a calm
// "server doesn't support this" band instead of a scary error.
const unsupported = ref(false);
const loadError = ref<string | null>(null);

const trashDays = ref<number>(30);
const versionsKeepN = ref<number>(0);
const shareMaxTtl = ref<number>(7);
const sharesOverMax = ref<number>(0);
const antivirus = ref<ProtectionAntivirus | null>(null);
// Save-scan window (minutes). Bounds come from the server with the value, so
// the form enforces exactly what the API enforces.
const avSaveWindow = ref<number>(30);
const avWindowMin = ref<number>(2);
const avWindowMax = ref<number>(60);
// Scan-size ceiling (MB). Same pattern: bounds come from the server.
const avMaxScanMb = ref<number>(100);
const avMaxScanMbMin = ref<number>(1);
const avMaxScanMbMax = ref<number>(10240);

// ⚠⚠ The transport block. Unlike every other field on this page, these three
// take effect at the NEXT RESTART — in both directions — because the scan
// pipeline is wired once when the server boots. The form therefore says so
// permanently under the fields, AND says it again in the toast at the moment
// of the change, naming the direction. Deferring silently would be worse than
// not having the control; so would pretending it is live.
const avScanEnabled = ref<boolean>(false);
const avScanMode = ref<string>('binary');
const avClamdAddr = ref<string>('');
const avModes = ref<string[]>(['binary', 'daemon']);
const avRestartPending = ref<boolean>(false);
const savingAvEnabled = ref(false);
const savingAvTransport = ref(false);
const errAvTransport = ref<string | null>(null);

const modeOptions = computed(() =>
  avModes.value.map((m) => ({ value: m, label: t(`protection.av.transport.mode.${m}`) })),
);

const savingTrash = ref(false);
const savingVersions = ref(false);
const savingShare = ref(false);
const savingAvWindow = ref(false);
const savingAvMaxScan = ref(false);
const errTrash = ref<string | null>(null);
const errVersions = ref<string | null>(null);
const errShare = ref<string | null>(null);
const errAvWindow = ref<string | null>(null);
const errAvMaxScan = ref<string | null>(null);

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
    const s = await ProtectionApi.get();
    trashDays.value = s.trash_retention_days;
    versionsKeepN.value = s.versions_keep_n;
    shareMaxTtl.value = s.share_max_ttl_days ?? 0;
    sharesOverMax.value = s.shares_over_max_ttl ?? 0;
    antivirus.value = s.antivirus ?? { enabled: false, binary: '' };
    avSaveWindow.value = s.antivirus?.save_scan_window_minutes ?? 30;
    avWindowMin.value = s.antivirus?.save_scan_window_min ?? 2;
    avWindowMax.value = s.antivirus?.save_scan_window_max ?? 60;
    avMaxScanMb.value = s.antivirus?.max_scan_mb ?? 100;
    avMaxScanMbMin.value = s.antivirus?.max_scan_mb_min ?? 1;
    avMaxScanMbMax.value = s.antivirus?.max_scan_mb_max ?? 10240;
    // ⚠ The SETTINGS, not the status: `enabled` above is "on and reachable",
    // these are what the admin last saved and what the form has to render.
    avScanEnabled.value = s.antivirus?.scan_enabled ?? false;
    avScanMode.value = s.antivirus?.scan_mode ?? 'binary';
    avClamdAddr.value = s.antivirus?.clamd_addr ?? '';
    if (s.antivirus?.modes?.length) avModes.value = s.antivirus.modes;
    avRestartPending.value = s.antivirus?.restart_pending ?? false;
  } catch (e: unknown) {
    if (isMissingEndpoint(e)) {
      unsupported.value = true;
    } else {
      loadError.value = extractError(e, t('errors.generic'));
    }
  } finally {
    loading.value = false;
  }
}

onMounted(load);

function validInt(v: unknown, min: number): boolean {
  return typeof v === 'number' && Number.isFinite(v) && Number.isInteger(v) && v >= min;
}

async function saveTrash() {
  errTrash.value = null;
  // Backend floor: trash.retention_days <= 0 silently falls back to the
  // default (30), so reject anything below 1 up front.
  if (!validInt(trashDays.value, 1)) {
    errTrash.value = t('protection.trash.errMin');
    return;
  }
  savingTrash.value = true;
  try {
    const s = await ProtectionApi.update({ trash_retention_days: trashDays.value });
    trashDays.value = s.trash_retention_days;
    toast.success(t('protection.savedOk'));
  } catch (e: unknown) {
    errTrash.value = extractError(e, t('errors.generic'));
  } finally {
    savingTrash.value = false;
  }
}

async function saveShare() {
  errShare.value = null;
  if (!validInt(shareMaxTtl.value, 0)) {
    errShare.value = t('protection.share.errMin');
    return;
  }
  savingShare.value = true;
  try {
    const s = await ProtectionApi.update({ share_max_ttl_days: shareMaxTtl.value });
    shareMaxTtl.value = s.share_max_ttl_days;
    sharesOverMax.value = s.shares_over_max_ttl ?? 0;
    toast.success(t('protection.savedOk'));
  } catch (e: unknown) {
    errShare.value = extractError(e, t('errors.generic'));
  } finally {
    savingShare.value = false;
  }
}

// The window is refused, not clamped, when it is out of range — the operator
// finds out while looking at the field rather than from a log line later.
async function saveAvWindow() {
  errAvWindow.value = null;
  const v = avSaveWindow.value;
  if (
    !validInt(v, avWindowMin.value) ||
    v > avWindowMax.value
  ) {
    errAvWindow.value = t('protection.av.window.errRange', {
      min: avWindowMin.value,
      max: avWindowMax.value,
    });
    return;
  }
  savingAvWindow.value = true;
  try {
    const s = await ProtectionApi.update({ av_save_scan_window_minutes: v });
    avSaveWindow.value = s.antivirus?.save_scan_window_minutes ?? v;
    toast.success(t('protection.savedOk'));
  } catch (e: unknown) {
    errAvWindow.value = extractError(e, t('errors.generic'));
  } finally {
    savingAvWindow.value = false;
  }
}

async function saveAvMaxScan() {
  errAvMaxScan.value = null;
  const v = avMaxScanMb.value;
  if (!validInt(v, avMaxScanMbMin.value) || v > avMaxScanMbMax.value) {
    errAvMaxScan.value = t('protection.av.maxScan.errRange', {
      min: avMaxScanMbMin.value,
      max: avMaxScanMbMax.value,
    });
    return;
  }
  savingAvMaxScan.value = true;
  try {
    const s = await ProtectionApi.update({ av_max_scan_mb: v });
    avMaxScanMb.value = s.antivirus?.max_scan_mb ?? v;
    toast.success(t('protection.savedOk'));
  } catch (e: unknown) {
    errAvMaxScan.value = extractError(e, t('errors.generic'));
  } finally {
    savingAvMaxScan.value = false;
  }
}

// ⚠⚠ The switch is stored at once and takes effect at the next restart, in
// BOTH directions. The toast names the direction rather than saying a generic
// "saved", because "antivirus off" and "antivirus off after you restart" are
// different states and the operator is about to act on one of them.
async function saveAvEnabled(next: boolean) {
  savingAvEnabled.value = true;
  const before = avScanEnabled.value;
  avScanEnabled.value = next;
  try {
    const s = await ProtectionApi.update({ av_enabled: next });
    applyAntivirus(s);
    toast.success(
      next ? t('protection.av.power.onAtRestart') : t('protection.av.power.offAtRestart'),
    );
  } catch (e: unknown) {
    avScanEnabled.value = before;
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingAvEnabled.value = false;
  }
}

// Mode and address are saved TOGETHER because the server validates them as a
// pair: daemon mode with no address is a scanner that is switched on and can
// reach nothing, and each half is legal on its own.
async function saveAvTransport() {
  errAvTransport.value = null;
  if (avScanMode.value === 'daemon' && !avClamdAddr.value.trim()) {
    errAvTransport.value = t('protection.av.transport.errAddrRequired');
    return;
  }
  savingAvTransport.value = true;
  try {
    // ⚠⚠ The address is only sent in daemon mode, because in binary mode the
    // field is not even on screen. Sending it anyway means a half-typed or
    // stale address REFUSES the mode change, with an error about a field the
    // operator cannot see — found in the browser: switching back to "binary"
    // failed with `clamd address "clamav 3310" contains whitespace`. Leaving
    // the stored address untouched also means switching back to daemon still
    // has it, which is what someone toggling between the two expects.
    const patch: ProtectionPatch = { av_mode: avScanMode.value };
    if (avScanMode.value === 'daemon') patch.av_clamd_addr = avClamdAddr.value.trim();
    const s = await ProtectionApi.update(patch);
    applyAntivirus(s);
    toast.success(t('protection.av.transport.savedAtRestart'));
  } catch (e: unknown) {
    errAvTransport.value = extractError(e, t('errors.generic'));
  } finally {
    savingAvTransport.value = false;
  }
}

// Every antivirus write echoes the whole block back, so the status half
// (badge, reachability, restart_pending) refreshes from the same response
// rather than drifting until the next page load.
function applyAntivirus(s: { antivirus?: ProtectionAntivirus }) {
  if (!s.antivirus) return;
  antivirus.value = s.antivirus;
  avScanEnabled.value = s.antivirus.scan_enabled ?? avScanEnabled.value;
  avScanMode.value = s.antivirus.scan_mode ?? avScanMode.value;
  avClamdAddr.value = s.antivirus.clamd_addr ?? avClamdAddr.value;
  avRestartPending.value = s.antivirus.restart_pending ?? false;
}

async function saveVersions() {
  errVersions.value = null;
  if (!validInt(versionsKeepN.value, 0)) {
    errVersions.value = t('protection.versions.errMin');
    return;
  }
  savingVersions.value = true;
  try {
    const s = await ProtectionApi.update({ versions_keep_n: versionsKeepN.value });
    versionsKeepN.value = s.versions_keep_n;
    toast.success(t('protection.savedOk'));
  } catch (e: unknown) {
    errVersions.value = extractError(e, t('errors.generic'));
  } finally {
    savingVersions.value = false;
  }
}
</script>

<template>
  <div class="space-y-4 max-w-2xl">
    <div class="flex items-center gap-2">
      <Shield class="h-6 w-6 text-brand-600 dark:text-brand-400" />
      <div>
        <h1 class="text-xl font-semibold">{{ t('protection.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('protection.subtitle') }}</p>
      </div>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <!-- Backend predates the protection API -->
    <div
      v-else-if="unsupported"
      class="flex items-start gap-3 rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
    >
      <ShieldAlert class="mt-0.5 h-5 w-5 shrink-0" />
      <p>{{ t('protection.unsupported') }}</p>
    </div>

    <div
      v-else-if="loadError"
      class="space-y-3 rounded-xl border border-rose-300 bg-rose-50 p-4 text-sm text-rose-700 dark:border-rose-700/60 dark:bg-rose-900/20 dark:text-rose-200"
    >
      <p>{{ loadError }}</p>
      <Button size="sm" variant="outline" @click="load">{{ t('common.refresh') }}</Button>
    </div>

    <template v-else>
      <!-- Trash retention. `novalidate` so our under-field i18n errors
           render instead of the browser's native min/step bubbles. -->
      <form class="card card-body space-y-3" novalidate @submit.prevent="saveTrash">
        <h2 class="flex items-center gap-2 text-base font-semibold">
          <Trash2 class="h-4 w-4" /> {{ t('protection.trash.title') }}
        </h2>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('protection.trash.desc') }}</p>
        <Input
          :model-value="trashDays"
          type="number"
          :min="1"
          :step="1"
          :label="t('protection.trash.label')"
          :hint="t('protection.trash.hint')"
          :error="errTrash"
          class="max-w-xs"
          @update:model-value="(v) => ((trashDays = v as number), (errTrash = null))"
        />
        <div class="flex justify-end pt-1">
          <Button type="submit" :loading="savingTrash">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <!-- Version policy -->
      <form class="card card-body space-y-3" novalidate @submit.prevent="saveVersions">
        <h2 class="flex items-center gap-2 text-base font-semibold">
          <History class="h-4 w-4" /> {{ t('protection.versions.title') }}
        </h2>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('protection.versions.desc') }}</p>
        <Input
          :model-value="versionsKeepN"
          type="number"
          :min="0"
          :step="1"
          :label="t('protection.versions.label')"
          :hint="t('protection.versions.hint')"
          :error="errVersions"
          class="max-w-xs"
          @update:model-value="(v) => ((versionsKeepN = v as number), (errVersions = null))"
        />
        <div class="flex justify-end pt-1">
          <Button type="submit" :loading="savingVersions">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <!-- Share-link ceiling. Applies to NEW links only; existing ones are
           counted so the operator can decide about them by hand. -->
      <form class="card card-body space-y-3" novalidate @submit.prevent="saveShare">
        <h2 class="flex items-center gap-2 text-base font-semibold">
          <Link2 class="h-4 w-4" /> {{ t('protection.share.title') }}
        </h2>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('protection.share.desc') }}</p>
        <Input
          :model-value="shareMaxTtl"
          type="number"
          :min="0"
          :step="1"
          :label="t('protection.share.label')"
          :hint="t('protection.share.hint')"
          :error="errShare"
          class="max-w-xs"
          @update:model-value="(v) => ((shareMaxTtl = v as number), (errShare = null))"
        />
        <p
          v-if="shareMaxTtl > 0 && sharesOverMax > 0"
          class="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
          data-testid="shares-over-max"
        >
          {{ t('protection.share.overMax', { n: sharesOverMax }) }}
        </p>
        <div class="flex justify-end pt-1">
          <Button type="submit" :loading="savingShare">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <!-- Antivirus -->
      <div class="card card-body space-y-3">
        <div class="flex items-center justify-between gap-2">
          <h2 class="flex items-center gap-2 text-base font-semibold">
            <Shield class="h-4 w-4" /> {{ t('protection.av.title') }}
          </h2>
          <div class="flex items-center gap-2">
            <Badge :tone="antivirus?.enabled ? 'emerald' : 'zinc'">
              {{ antivirus?.enabled ? t('common.enabled') : t('common.disabled') }}
            </Badge>
            <!-- ⚠⚠ Configured and reachable are different questions. A clamd
                 that is down would otherwise sit behind a green badge while
                 nothing at all was being scanned. -->
            <Badge
              v-if="antivirus?.enabled"
              :tone="antivirus?.reachable ? 'emerald' : 'amber'"
              data-testid="av-reachable"
            >
              {{ antivirus?.reachable ? t('protection.av.reachable') : t('protection.av.unreachable') }}
            </Badge>
          </div>
        </div>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('protection.av.desc') }}</p>

        <!-- A change to the switch, the mode or the address is stored now and
             in force after a restart. This band is what stops that message
             from being a toast the operator already scrolled past — and it
             clears itself once the restart has happened. -->
        <p
          v-if="avRestartPending"
          class="flex items-start gap-2 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
          data-testid="av-restart-pending"
        >
          <RefreshCw class="mt-0.5 h-4 w-4 flex-shrink-0" />
          {{ t('protection.av.restartPending') }}
        </p>

        <p
          v-if="antivirus?.enabled && !antivirus?.reachable && antivirus?.health"
          class="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 font-mono text-xs text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
          data-testid="av-health"
        >
          {{ antivirus.health }}
        </p>

        <div v-if="antivirus?.enabled" class="space-y-1 text-sm">
          <div>
            <span class="text-zinc-500 dark:text-zinc-400">{{ t('protection.av.binary') }}:</span>
            <code class="ml-2 rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-xs dark:bg-zinc-800">
              {{ antivirus.binary || '—' }}
            </code>
          </div>
          <div v-if="antivirus.address">
            <span class="text-zinc-500 dark:text-zinc-400">{{ t('protection.av.address') }}:</span>
            <code class="ml-2 rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-xs dark:bg-zinc-800">
              {{ antivirus.address }}
            </code>
          </div>
          <div v-if="antivirus.version" class="text-xs text-zinc-500 dark:text-zinc-400">
            {{ antivirus.version }}
          </div>
        </div>
        <p v-else class="text-sm text-zinc-600 dark:text-zinc-300">
          {{ t('protection.av.setupHint') }}
          <a
            :href="DOCS_URL"
            target="_blank"
            rel="noopener"
            class="inline-flex items-center gap-1 text-brand-600 hover:underline dark:text-brand-400"
          >
            docs/PROTECTION.md
            <ExternalLink class="h-3.5 w-3.5" />
          </a>
        </p>

        <!-- Switch + transport. ⚠⚠ Deferred to the next restart, both ways. -->
        <div
          v-if="antivirus?.scan_enabled !== undefined"
          class="space-y-3 border-t border-zinc-200 pt-3 dark:border-zinc-800"
        >
          <Toggle
            :model-value="avScanEnabled"
            :disabled="savingAvEnabled"
            :label="t('protection.av.power.label')"
            :description="t('protection.av.power.desc')"
            name="av-scan-enabled"
            data-testid="av-scan-enabled"
            @update:model-value="saveAvEnabled"
          />

          <form class="space-y-3" novalidate @submit.prevent="saveAvTransport">
            <Select
              :model-value="avScanMode"
              :options="modeOptions"
              :label="t('protection.av.transport.label')"
              :hint="t(`protection.av.transport.hint.${avScanMode}`)"
              class="max-w-xs"
              name="av-scan-mode"
              data-testid="av-scan-mode"
              @update:model-value="(v) => ((avScanMode = String(v)), (errAvTransport = null))"
            />
            <!-- Only the field the chosen mode actually uses is shown: an
                 address box under "binary" invites someone to fill it in and
                 wonder why nothing changed. -->
            <Input
              v-if="avScanMode === 'daemon'"
              :model-value="avClamdAddr"
              type="text"
              :label="t('protection.av.transport.addrLabel')"
              :hint="t('protection.av.transport.addrHint')"
              placeholder="clamav:3310"
              :error="errAvTransport"
              class="max-w-md"
              data-testid="av-clamd-addr"
              @update:model-value="(v) => ((avClamdAddr = String(v)), (errAvTransport = null))"
            />
            <p
              v-else-if="errAvTransport"
              class="text-sm text-rose-600 dark:text-rose-400"
            >
              {{ errAvTransport }}
            </p>
            <p class="text-sm text-zinc-500 dark:text-zinc-400">
              {{ t('protection.av.transport.binaryNote') }}
            </p>
            <div class="flex justify-end pt-1">
              <Button type="submit" :loading="savingAvTransport">
                <Save class="h-4 w-4" />
                {{ t('common.save') }}
              </Button>
            </div>
          </form>
        </div>

        <!-- Scan-size ceiling. Writable, unlike enabled/binary, which stay an
             environment concern: the binary is a path this server executes. -->
        <form
          v-if="antivirus?.max_scan_mb !== undefined"
          class="space-y-3 border-t border-zinc-200 pt-3 dark:border-zinc-800"
          novalidate
          @submit.prevent="saveAvMaxScan"
        >
          <h3 class="text-sm font-semibold">{{ t('protection.av.maxScan.title') }}</h3>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">
            {{ t('protection.av.maxScan.desc') }}
          </p>
          <Input
            :model-value="avMaxScanMb"
            type="number"
            :min="avMaxScanMbMin"
            :max="avMaxScanMbMax"
            :step="1"
            :label="t('protection.av.maxScan.label')"
            :hint="t('protection.av.maxScan.hint', { min: avMaxScanMbMin, max: avMaxScanMbMax })"
            :error="errAvMaxScan"
            class="max-w-xs"
            data-testid="av-max-scan-mb"
            @update:model-value="(v) => ((avMaxScanMb = v as number), (errAvMaxScan = null))"
          />
          <div class="flex justify-end pt-1">
            <Button type="submit" :loading="savingAvMaxScan">
              <Save class="h-4 w-4" />
              {{ t('common.save') }}
            </Button>
          </div>
        </form>

        <!-- Editor save-scan window. -->
        <form
          v-if="antivirus?.save_scan_window_minutes !== undefined"
          class="space-y-3 border-t border-zinc-200 pt-3 dark:border-zinc-800"
          novalidate
          @submit.prevent="saveAvWindow"
        >
          <h3 class="text-sm font-semibold">{{ t('protection.av.window.title') }}</h3>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">
            {{ t('protection.av.window.desc') }}
          </p>
          <Input
            :model-value="avSaveWindow"
            type="number"
            :min="avWindowMin"
            :max="avWindowMax"
            :step="1"
            :label="t('protection.av.window.label')"
            :hint="t('protection.av.window.hint', { min: avWindowMin, max: avWindowMax })"
            :error="errAvWindow"
            class="max-w-xs"
            data-testid="av-save-window"
            @update:model-value="(v) => ((avSaveWindow = v as number), (errAvWindow = null))"
          />
          <div class="flex justify-end pt-1">
            <Button type="submit" :loading="savingAvWindow">
              <Save class="h-4 w-4" />
              {{ t('common.save') }}
            </Button>
          </div>
        </form>
      </div>
    </template>
  </div>
</template>
