import { api } from './client';

/**
 * Storage usage and cost (issue #20).
 *
 * ⚠ Two things this payload says that the page must not flatten:
 *
 *  - `source` on every row. A provider's own report and filex's own metering
 *    are both useful and must never be added together — a number that looks
 *    authoritative and is not is worse than no number.
 *  - `scope`. The provider's report carries an account-level line beside the
 *    per-bucket ones; summing them counts the same transactions twice. The
 *    server already keeps them apart (`totals` vs `totals.account_ops`), and
 *    the page renders them apart too.
 */
export interface UsageDay {
  date: string;
  provider: string;
  source: 'provider' | 'filex';
  scope: 'bucket' | 'account';
  bucket?: string;
  location?: string;
  stored_bytes: number;
  byte_hours: number;
  uploaded_bytes: number;
  deleted_bytes: number;
  downloaded_bytes: number;
  free_egress_bytes: number;
  ops_a: number;
  ops_b: number;
  ops_c: number;
  ops_d: number;
}

export interface UsageTotals {
  days: number;
  avg_stored_bytes: number;
  peak_stored_bytes: number;
  uploaded_bytes: number;
  deleted_bytes: number;
  downloaded_bytes: number;
  free_egress_bytes: number;
  ops_a: number;
  ops_b: number;
  ops_c: number;
  ops_d: number;
  account_ops: { A: number; B: number; C: number; D: number };
}

export interface UsageCost {
  currency: string;
  days: number;
  avg_stored_gb: number;
  billable_gb_months: number;
  storage_cost: number;
  free_storage_gb: number;
  storage_covered: boolean;
  downloaded_gb: number;
  provider_free_gb: number;
  allowance_gb: number;
  billable_gb: number;
  egress_cost: number;
  egress_covered: boolean;
  billable_ops: number;
  free_ops: number;
  ops_cost: number;
  total: number;
}

export interface UsagePricing {
  currency: string;
  storage_per_gb_month: number;
  free_storage_gb: number;
  egress_per_gb: number;
  free_egress_multiple: number;
  class_a_per_10k: number;
  class_b_per_10k: number;
  class_c_per_10k: number;
  class_d_per_10k: number;
  free_class_per_day: number;
  note?: string;
}

export interface UsageSettings {
  provider: string;
  report_storage: string;
  account_id: string;
  prefix: string;
  pricing: UsagePricing;
  pricing_is_default: boolean;
}

export interface UsageReport {
  settings: UsageSettings;
  configured: boolean;
  from: string;
  to: string;
  days: UsageDay[];
  totals: UsageTotals;
  cost: UsageCost;
  notes?: string[];
}

export const UsageApi = {
  /** The last `days` days, ending today. */
  async report(days = 30): Promise<UsageReport> {
    const { data } = await api.get<UsageReport>('/admin/usage', { params: { days } });
    return data;
  },

  /**
   * Save the configuration.
   *
   * ⚠ Through the ordinary settings endpoint on purpose: these are ordinary
   * settings rows, and that endpoint already reserves every non-branding key
   * to the operator of the whole instance. A second write path would be a
   * second place for that rule to be forgotten.
   */
  async save(s: Partial<UsageSettings>): Promise<void> {
    const body: Record<string, string> = {};
    if (s.provider !== undefined) body['usage.provider'] = s.provider;
    if (s.report_storage !== undefined) body['usage.report_storage'] = s.report_storage;
    if (s.account_id !== undefined) body['usage.account_id'] = s.account_id;
    if (s.prefix !== undefined) body['usage.prefix'] = s.prefix;
    if (s.pricing !== undefined) body['usage.pricing'] = JSON.stringify(s.pricing);
    await api.patch('/admin/settings', body);
  },
};
