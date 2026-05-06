import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { ReplicaApi } from '@/api/replica';
import type {
  ReplicaFailure,
  ReplicaRule,
  ReplicaRuleInput,
  ReplicaSettings,
  ReplicaStatusReport,
} from '@/api/types';
import { extractError } from '@/api/client';

export const useReplicaStore = defineStore('replica', () => {
  // Rules
  const rules = ref<ReplicaRule[]>([]);
  // Failures
  const failures = ref<ReplicaFailure[]>([]);
  const failuresTotal = ref(0);
  const failuresLimit = ref(50);
  const failuresOffset = ref(0);
  const onlyUnresolved = ref(true);
  // Status report
  const report = ref<ReplicaStatusReport | null>(null);
  // Settings
  const settings = ref<ReplicaSettings>({ report_cron: '', report_enabled: false, default_mode: 'mirror' });

  const loading = ref(false);
  const error = ref<string | null>(null);

  const failurePages = computed(() => Math.max(1, Math.ceil(failuresTotal.value / failuresLimit.value)));
  const failureCurrentPage = computed(() => Math.floor(failuresOffset.value / failuresLimit.value) + 1);

  // ── Rules CRUD ───────────────────────────────────────────
  async function fetchRules(): Promise<void> {
    loading.value = true;
    try {
      rules.value = await ReplicaApi.listRules();
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load rules');
    } finally {
      loading.value = false;
    }
  }
  async function createRule(payload: ReplicaRuleInput): Promise<void> {
    const r = await ReplicaApi.createRule(payload);
    rules.value = [...rules.value, r].sort(byPriority);
  }
  async function updateRule(id: number, payload: ReplicaRuleInput): Promise<void> {
    const r = await ReplicaApi.updateRule(id, payload);
    rules.value = rules.value.map((x) => (x.id === id ? r : x)).sort(byPriority);
  }
  async function deleteRule(id: number): Promise<void> {
    await ReplicaApi.deleteRule(id);
    rules.value = rules.value.filter((x) => x.id !== id);
  }

  // ── Failures ─────────────────────────────────────────────
  async function fetchFailures(): Promise<void> {
    loading.value = true;
    try {
      const r = await ReplicaApi.listFailures({
        unresolved: onlyUnresolved.value,
        limit: failuresLimit.value,
        offset: failuresOffset.value,
      });
      failures.value = r.items ?? [];
      failuresTotal.value = r.total;
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load failures');
    } finally {
      loading.value = false;
    }
  }
  async function fixAll(): Promise<{ queued: number }> {
    const r = await ReplicaApi.fixAll();
    await fetchFailures();
    return r;
  }
  async function fixOne(path: string, op: string): Promise<void> {
    await ReplicaApi.fixOne(path, op);
    await fetchFailures();
  }
  function setUnresolvedFilter(v: boolean): void {
    onlyUnresolved.value = v;
    failuresOffset.value = 0;
  }
  function setFailuresPage(p: number): void {
    failuresOffset.value = Math.max(0, (p - 1) * failuresLimit.value);
  }

  // ── Status report ────────────────────────────────────────
  async function fetchReport(): Promise<void> {
    try {
      report.value = await ReplicaApi.getReport();
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load report');
    }
  }
  async function runReportNow(): Promise<void> {
    await ReplicaApi.runReportNow();
    await fetchReport();
  }

  // ── Settings ─────────────────────────────────────────────
  async function fetchSettings(): Promise<void> {
    try {
      settings.value = await ReplicaApi.getSettings();
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load settings');
    }
  }
  async function updateSettings(payload: ReplicaSettings): Promise<void> {
    settings.value = await ReplicaApi.updateSettings(payload);
  }

  return {
    rules,
    failures,
    failuresTotal,
    failuresLimit,
    failuresOffset,
    onlyUnresolved,
    report,
    settings,
    loading,
    error,
    failurePages,
    failureCurrentPage,
    fetchRules,
    createRule,
    updateRule,
    deleteRule,
    fetchFailures,
    fixAll,
    fixOne,
    setUnresolvedFilter,
    setFailuresPage,
    fetchReport,
    runReportNow,
    fetchSettings,
    updateSettings,
  };
});

function byPriority(a: ReplicaRule, b: ReplicaRule): number {
  if (a.priority !== b.priority) return a.priority - b.priority;
  return a.id - b.id;
}
