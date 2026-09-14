// Operational alarms are written by the server with an English title
// (`filex 0.42.0 available`, `Replica put failed`), so every Turkish panel
// showed them in English. They are phrased on the reader's side now, like the
// file events. Each row below is the shape the one emit site actually stores.
import { describe, it, expect } from 'vitest';
import { renderNotification } from '@/lib/notificationText';

const rows = [
  {
    event: 'update_available',
    title: 'filex 0.42.0 available',
    body: 'policy notify: a newer release is published',
    meta: { version: '0.42.0', current: '0.41.1', step: 'minor', action: 'notify' },
    want: { tr: /0\.42\.0 yayınlandı/, en: /0\.42\.0 is available/ },
  },
  {
    event: 'replica_fail',
    title: 'Replica put failed',
    body: 'Path docs/a.pdf — timeout',
    meta: { path: 'docs/a.pdf', op: 'put', error: 'timeout', attempt: 2 },
    want: { tr: /Kopyada put başarısız: a\.pdf/, en: /Replica put failed: a\.pdf/ },
  },
  {
    event: 'primary_read_fail',
    title: 'Primary read failed, served from replica',
    body: 'Path docs/a.pdf was served from replica after primary error: EOF',
    meta: { path: 'docs/a.pdf', primary_error: 'EOF' },
    want: { tr: /a\.pdf kopyadan sunuldu/, en: /a\.pdf was served from the replica/ },
  },
  {
    event: 'replica_reconcile_done',
    title: 'Replica reconciliation queued',
    body: 'Queued 1 replica_retry ops; check the queue page for progress',
    meta: { queued: 1 },
    want: { tr: /1 kopya yeniden denemesi/, en: /^1 replica retry queued$/ },
  },
  {
    event: 'replica_status_report',
    title: 'Replica status report',
    body: 'Cron report: 3 unresolved failures, 2 repaired in last 24h',
    meta: { failed_count: 3, repaired_count: 2, total_files: 90 },
    want: { tr: /3 çözülmemiş, 2 onarıldı/, en: /3 unresolved, 2 repaired/ },
  },
];

describe('operational alarms are phrased for the reader', () => {
  for (const r of rows) {
    for (const lang of ['tr', 'en'] as const) {
      it(`${r.event} in ${lang}`, () => {
        const { title, body } = renderNotification(r, lang);
        expect(title).toMatch(r.want[lang]);
        if (lang === 'tr') expect(title, 'not the server’s English title').not.toBe(r.title);
        expect(`${title} ${body}`).not.toMatch(/\{\w+\}/);
      });
    }
  }
});
