<script setup lang="ts">
/**
 * Admin → Storage usage & cost (issue #20).
 *
 * ⚠ Three things this page refuses to do, each because it is how a cost view
 * misleads the person reading it:
 *
 *  1. It never renders an empty chart for an unconfigured instance. "You spent
 *     nothing" and "nothing is set up" look identical as a zero, so the form
 *     is shown instead.
 *  2. It never adds the account-level line to the per-bucket lines. The
 *     provider reports both, and summing them counts the same transactions
 *     twice — by exactly the amount nobody notices.
 *  3. It never presents the estimate as a bill. When the prices are filex's
 *     defaults rather than the operator's contract, it says so, with the date
 *     they were read.
 */
import { computed, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { BarChart3, Save, RefreshCw, Info, ExternalLink } from 'lucide-vue-next';

import { UsageApi, type UsageReport, type UsageDay } from '@/api/usage';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { formatBytes, formatNumber } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';

const DOCS_URL = 'https://github.com/BRF-Tech/filex/blob/main/docs/USAGE.md';

const { t, locale } = useI18n();
const toast = useToastStore();

const report = ref<UsageReport | null>(null);
const loading = ref(false);
const saving = ref(false);
const range = ref<string | number | null>(30);

const form = reactive({
  provider: '',
  report_storage: '',
  account_id: '',
  prefix: '',
});

async function load() {
  loading.value = true;
  try {
    const r = await UsageApi.report(Number(range.value) || 30);
    report.value = r;
    form.provider = r.settings.provider;
    form.report_storage = r.settings.report_storage;
    form.account_id = r.settings.account_id;
    form.prefix = r.settings.prefix;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  try {
    await UsageApi.save({ ...form });
    toast.success(t('usage.savedOk'));
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

const providerOptions = computed(() => [
  { value: '', label: t('usage.fields.providerNone') },
  { value: 'b2', label: 'Backblaze B2' },
]);
const rangeOptions = computed(() => [
  { value: 7, label: t('usage.ranges.d7') },
  { value: 30, label: t('usage.ranges.d30') },
  { value: 90, label: t('usage.ranges.d90') },
]);

const configured = computed(() => report.value?.configured === true);
const cost = computed(() => report.value?.cost);
const totals = computed(() => report.value?.totals);

/** Bucket rows only — the account line is rendered on its own. */
const bucketDays = computed<UsageDay[]>(() =>
  (report.value?.days ?? []).filter((d) => d.scope === 'bucket'),
);

/** One row per bucket, summed over the window. */
const perBucket = computed(() => {
  const by = new Map<string, { bucket: string; stored: number; down: number; up: number; ops: number }>();
  for (const d of bucketDays.value) {
    const key = d.location ? `${d.bucket} (${d.location})` : d.bucket || '—';
    const cur = by.get(key) ?? { bucket: key, stored: 0, down: 0, up: 0, ops: 0 };
    // Storage is a daily reading, so the bucket's figure is its mean, not a sum.
    cur.stored += d.byte_hours > 0 ? d.byte_hours / 24 : d.stored_bytes;
    cur.down += d.downloaded_bytes;
    cur.up += d.uploaded_bytes;
    cur.ops += d.ops_a + d.ops_b + d.ops_c + d.ops_d;
    by.set(key, cur);
  }
  const days = new Set(bucketDays.value.map((d) => d.date.slice(0, 10))).size || 1;
  return [...by.values()]
    .map((b) => ({ ...b, stored: b.stored / days }))
    .sort((a, b) => b.stored - a.stored);
});

/** The daily trend, as one point per day across every bucket. */
const trend = computed(() => {
  const by = new Map<string, number>();
  for (const d of bucketDays.value) {
    const key = d.date.slice(0, 10);
    by.set(key, (by.get(key) ?? 0) + (d.byte_hours > 0 ? d.byte_hours / 24 : d.stored_bytes));
  }
  return [...by.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([date, bytes]) => ({ date, bytes }));
});

const trendMax = computed(() => Math.max(1, ...trend.value.map((p) => p.bytes)));

function money(n: number | undefined): string {
  if (n === undefined) return '—';
  const cur = cost.value?.currency || 'USD';
  try {
    return new Intl.NumberFormat(locale.value, { style: 'currency', currency: cur }).format(n);
  } catch {
    return `${n.toFixed(4)} ${cur}`;
  }
}

function bytes(n: number): string {
  return formatBytes(n, locale.value);
}

onMounted(load);
</script>

<template>
  <div class="space-y-4 max-w-4xl">
    <div class="flex items-start justify-between gap-3">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <BarChart3 class="h-5 w-5" />
          {{ t('usage.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('usage.subtitle') }}</p>
      </div>
      <a
        :href="DOCS_URL"
        target="_blank"
        rel="noopener"
        class="text-zinc-500 hover:text-brand-600 dark:hover:text-brand-400"
      >
        <ExternalLink class="h-4 w-4" />
      </a>
    </div>

    <div v-if="loading && !report" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <template v-else>
      <!-- Configuration. Always visible: it is where an operator comes back to
           when a number looks wrong. -->
      <div class="card card-body space-y-3" data-testid="usage-config">
        <h2 class="text-sm font-semibold">{{ t('usage.config.title') }}</h2>
        <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('usage.config.hint') }}</p>

        <Select
          v-model="form.provider"
          :label="t('usage.fields.provider')"
          :options="providerOptions"
        />

        <template v-if="form.provider">
          <Input
            v-model="form.report_storage"
            :label="t('usage.fields.reportStorage')"
            :hint="t('usage.fields.reportStorageHint')"
            monospace
            placeholder="b2-reports"
          />
          <Input
            v-model="form.account_id"
            :label="t('usage.fields.accountId')"
            :hint="t('usage.fields.accountIdHint')"
            monospace
          />
          <Input
            v-model="form.prefix"
            :label="t('usage.fields.prefix')"
            :hint="t('usage.fields.prefixHint')"
            monospace
          />
        </template>

        <div class="flex items-center justify-between gap-2 pt-1">
          <Select
            v-model="range"
            :label="t('usage.fields.range')"
            :options="rangeOptions"
            class="max-w-[12rem]"
            @change="load"
          />
          <div class="flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" :loading="loading" @click="load">
              <RefreshCw class="h-4 w-4" />
              {{ t('common.refresh') }}
            </Button>
            <Button size="sm" :loading="saving" @click="save">
              <Save class="h-4 w-4" />
              {{ t('common.save') }}
            </Button>
          </div>
        </div>
      </div>

      <!-- Nothing configured: say so. An empty chart reads as "you spent
           nothing", which is a different statement. -->
      <div
        v-if="!configured"
        class="card card-body flex items-start gap-2 text-sm text-sky-800 dark:text-sky-300 bg-sky-50 dark:bg-sky-950/30"
        data-testid="usage-unconfigured"
      >
        <Info class="h-4 w-4 shrink-0 mt-0.5" />
        <span>{{ t('usage.unconfigured') }}</span>
      </div>

      <template v-else>
        <!-- What it costs, with the free part beside the billable part. -->
        <div class="card card-body space-y-3" data-testid="usage-cost">
          <div class="flex items-baseline justify-between">
            <h2 class="text-sm font-semibold">{{ t('usage.cost.title') }}</h2>
            <span class="text-xs text-zinc-500">{{ report?.from }} → {{ report?.to }}</span>
          </div>

          <div class="text-3xl font-semibold tabular-nums">{{ money(cost?.total) }}</div>

          <div class="grid gap-3 sm:grid-cols-3 text-sm">
            <div>
              <div class="text-xs text-zinc-500">{{ t('usage.cost.storage') }}</div>
              <div class="tabular-nums">{{ money(cost?.storage_cost) }}</div>
              <div class="text-xs text-zinc-500">
                {{ formatNumber(Number((cost?.avg_stored_gb ?? 0).toFixed(2)), locale) }} GB
                <Badge v-if="cost?.storage_covered" tone="emerald">{{ t('usage.cost.covered') }}</Badge>
              </div>
            </div>
            <div>
              <div class="text-xs text-zinc-500">{{ t('usage.cost.egress') }}</div>
              <div class="tabular-nums">{{ money(cost?.egress_cost) }}</div>
              <div class="text-xs text-zinc-500">
                {{ formatNumber(Number((cost?.downloaded_gb ?? 0).toFixed(2)), locale) }} GB
                <Badge v-if="cost?.egress_covered" tone="emerald">{{ t('usage.cost.covered') }}</Badge>
              </div>
            </div>
            <div>
              <div class="text-xs text-zinc-500">{{ t('usage.cost.transactions') }}</div>
              <div class="tabular-nums">{{ money(cost?.ops_cost) }}</div>
              <div class="text-xs text-zinc-500">
                {{ formatNumber(cost?.billable_ops ?? 0, locale) }} / {{ formatNumber(cost?.free_ops ?? 0, locale) }}
                {{ t('usage.cost.billableFree') }}
              </div>
            </div>
          </div>

          <p
            v-for="n in report?.notes ?? []"
            :key="n"
            class="text-xs text-amber-700 dark:text-amber-400 flex items-start gap-1.5"
            data-testid="usage-note"
          >
            <Info class="h-3.5 w-3.5 shrink-0 mt-px" />
            <span>{{ n }}</span>
          </p>
        </div>

        <!-- The daily trend. A plain bar row rather than a charting library:
             one series, thirty points, and no dependency to keep current. -->
        <div v-if="trend.length" class="card card-body space-y-2" data-testid="usage-trend">
          <h2 class="text-sm font-semibold">{{ t('usage.trend.title') }}</h2>
          <div class="flex items-end gap-px h-24">
            <div
              v-for="p in trend"
              :key="p.date"
              class="flex-1 bg-brand-500/70 hover:bg-brand-500 rounded-t"
              :style="{ height: `${Math.max(2, (p.bytes / trendMax) * 100)}%` }"
              :title="`${p.date} — ${bytes(p.bytes)}`"
            />
          </div>
          <div class="flex justify-between text-[11px] text-zinc-500">
            <span>{{ trend[0]?.date }}</span>
            <span>{{ trend[trend.length - 1]?.date }}</span>
          </div>
        </div>

        <!-- Per bucket. -->
        <div v-if="perBucket.length" class="card card-body space-y-2" data-testid="usage-buckets">
          <h2 class="text-sm font-semibold">{{ t('usage.buckets.title') }}</h2>
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead class="text-xs text-zinc-500 text-left">
                <tr>
                  <th class="py-1">{{ t('usage.buckets.bucket') }}</th>
                  <th class="py-1 text-right">{{ t('usage.buckets.stored') }}</th>
                  <th class="py-1 text-right">{{ t('usage.buckets.uploaded') }}</th>
                  <th class="py-1 text-right">{{ t('usage.buckets.downloaded') }}</th>
                  <th class="py-1 text-right">{{ t('usage.buckets.ops') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="b in perBucket" :key="b.bucket" class="border-t border-zinc-100 dark:border-zinc-800">
                  <td class="py-1 font-mono text-xs">{{ b.bucket }}</td>
                  <td class="py-1 text-right tabular-nums">{{ bytes(b.stored) }}</td>
                  <td class="py-1 text-right tabular-nums">{{ bytes(b.up) }}</td>
                  <td class="py-1 text-right tabular-nums">{{ bytes(b.down) }}</td>
                  <td class="py-1 text-right tabular-nums">{{ formatNumber(b.ops, locale) }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <!-- ⚠ Beside the table, never inside it. -->
          <p
            v-if="(totals?.account_ops?.A ?? 0) + (totals?.account_ops?.B ?? 0) + (totals?.account_ops?.C ?? 0) + (totals?.account_ops?.D ?? 0) > 0"
            class="text-xs text-zinc-500"
            data-testid="usage-account-ops"
          >
            {{
              t('usage.buckets.accountOps', {
                n: formatNumber(
                  (totals?.account_ops?.A ?? 0) +
                    (totals?.account_ops?.B ?? 0) +
                    (totals?.account_ops?.C ?? 0) +
                    (totals?.account_ops?.D ?? 0),
                  locale,
                ),
              })
            }}
          </p>
        </div>
      </template>
    </template>
  </div>
</template>
