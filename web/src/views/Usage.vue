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
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { BarChart3, Save, RefreshCw, Info, ExternalLink } from 'lucide-vue-next';

import { UsageApi, type UsageBucket, type UsageReport } from '@/api/usage';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { localeTag } from '@brftech/filex-core';
import { formatBytes, formatNumber } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';
import { DataTable, type DataColumn } from '@brftech/filex-core';

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

/** One row per bucket over the window, as the server added it up
 *  (usage.PerBucket: the mean daily storage, the traffic, the transactions,
 *  of the same source rows as the totals) - largest first. Before 0.54 the
 *  page summed the day rows itself, of whichever source, beside a server
 *  that refuses to add an estimate to an invoice (usage.SumBuckets). */
const perBucket = computed<UsageBucket[]>(() => report.value?.buckets ?? []);

type BucketRow = UsageBucket;

/* The explorer's table (DataTable), remembered under `admin.usage.buckets`.
 * Every bucket of the window is on screen, so the table sorts the rows
 * itself; the figures are raw numbers (the cells format them), so a sort by
 * Stored compares bytes, not the "1,2 GB" text. Until somebody picks a column
 * the rows keep `perBucket`'s own order — largest first. */
const bucketColumns = computed<DataColumn<BucketRow>[]>(() => [
  { id: 'label', label: t('usage.buckets.bucket'), sortable: true, width: 220 },
  { id: 'avg_stored_bytes', label: t('usage.buckets.stored'), align: 'right', sortable: true, sortDir: 'desc', width: 120 },
  { id: 'uploaded_bytes', label: t('usage.buckets.uploaded'), align: 'right', sortable: true, sortDir: 'desc', width: 120 },
  { id: 'downloaded_bytes', label: t('usage.buckets.downloaded'), align: 'right', sortable: true, sortDir: 'desc', width: 120 },
  { id: 'ops', label: t('usage.buckets.ops'), align: 'right', sortable: true, sortDir: 'desc', width: 110 },
]);

/** The daily trend, one point per day across every bucket, as the server
 *  added it up (usage.Trend). Only the bars' heights are worked out here. */
const trend = computed(() => (report.value?.trend ?? []).map((p) => ({ date: p.date, bytes: p.stored_bytes })));

const trendMax = computed(() => Math.max(1, ...trend.value.map((p) => p.bytes)));

/** The account-level transactions, beside the table (never inside it). */
const accountOps = computed(() => totals.value?.account_ops_total ?? 0);

function money(n: number | undefined): string {
  if (n === undefined) return '-';
  const cur = cost.value?.currency || 'USD';
  try {
    return new Intl.NumberFormat(localeTag(locale.value), { style: 'currency', currency: cur }).format(n);
  } catch {
    return `${formatNumber(n, locale.value)} ${cur}`;
  }
}

function bytes(n: number): string {
  return formatBytes(n, locale.value);
}

onMounted(load);
// The notes are the server's sentences, in the screen's language: a
// language switch asks again.
watch(locale, () => {
  if (report.value) void load();
});
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
                {{ bytes((cost?.avg_stored_gb ?? 0) * 1e9) }}
                <Badge v-if="cost?.storage_covered" tone="emerald">{{ t('usage.cost.covered') }}</Badge>
              </div>
            </div>
            <div>
              <div class="text-xs text-zinc-500">{{ t('usage.cost.egress') }}</div>
              <div class="tabular-nums">{{ money(cost?.egress_cost) }}</div>
              <div class="text-xs text-zinc-500">
                {{ bytes((cost?.downloaded_gb ?? 0) * 1e9) }}
                <Badge v-if="cost?.egress_covered" tone="emerald">{{ t('usage.cost.covered') }}</Badge>
              </div>
            </div>
            <div>
              <div class="text-xs text-zinc-500">{{ t('usage.cost.transactions') }}</div>
              <div class="tabular-nums">{{ money(cost?.ops_cost) }}</div>
              <!-- ⚠ One message with both numbers in it — "free" was a
                   separately translated word glued after the numbers, which a
                   language that puts it first, or makes it agree with the
                   number, could not say. ⚠ NOT a plural: it was written as a
                   choice whose two English forms were byte-identical, so every
                   translator had to write one sentence twice (v0.43.0
                   translation sweep). English does not inflect here; a
                   language that must would need two DIFFERENT English forms
                   first, and the count back as the third argument. -->
              <div class="text-xs text-zinc-500">
                {{
                  t('usage.cost.billableAndFree', {
                    billable: formatNumber(cost?.billable_ops ?? 0, locale),
                    free: formatNumber(cost?.free_ops ?? 0, locale),
                  })
                }}
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
              :title="`${p.date} - ${bytes(p.bytes)}`"
            />
          </div>
          <div class="flex justify-between text-[11px] text-zinc-500">
            <span>{{ trend[0]?.date }}</span>
            <span>{{ trend[trend.length - 1]?.date }}</span>
          </div>
        </div>

        <!-- Per bucket. -->
        <div v-if="perBucket.length" class="space-y-2" data-testid="usage-buckets">
          <DataTable
            table-id="admin.usage.buckets"
            :columns="bucketColumns"
            :rows="perBucket"
            row-key="label"
          >
            <template #toolbar>
              <h2 class="text-sm font-semibold">{{ t('usage.buckets.title') }}</h2>
            </template>
            <template #cell-label="{ row }">
              <span class="tbl-mono">{{ row.label }}</span>
            </template>
            <template #cell-avg_stored_bytes="{ row }">
              <span class="tabular-nums">{{ bytes(row.avg_stored_bytes) }}</span>
            </template>
            <template #cell-uploaded_bytes="{ row }">
              <span class="tabular-nums">{{ bytes(row.uploaded_bytes) }}</span>
            </template>
            <template #cell-downloaded_bytes="{ row }">
              <span class="tabular-nums">{{ bytes(row.downloaded_bytes) }}</span>
            </template>
            <template #cell-ops="{ row }">
              <span class="tabular-nums">{{ formatNumber(row.ops, locale) }}</span>
            </template>
          </DataTable>

          <!-- ⚠ Beside the table, never inside it. -->
          <p v-if="accountOps > 0" class="help-text" data-testid="usage-account-ops">
            {{ t('usage.buckets.accountOps', { n: formatNumber(accountOps, locale) }) }}
          </p>
        </div>
      </template>
    </template>
  </div>
</template>
