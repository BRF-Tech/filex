// Display helpers — bytes, dates, durations.
// All functions are pure and locale-aware.

export function formatBytes(n: number, locale = 'en'): string {
  if (!Number.isFinite(n) || n < 0) return '—';
  if (n === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  const idx = Math.min(units.length - 1, Math.floor(Math.log10(n) / 3));
  const value = n / Math.pow(1000, idx);
  const fmt = new Intl.NumberFormat(locale, {
    maximumFractionDigits: idx === 0 ? 0 : value < 10 ? 2 : 1,
  });
  return `${fmt.format(value)} ${units[idx]}`;
}

export function formatNumber(n: number | null | undefined, locale = 'en'): string {
  if (n == null || !Number.isFinite(n)) return '—';
  return new Intl.NumberFormat(locale).format(n);
}

export function formatDate(input: string | Date | null | undefined, locale = 'en'): string {
  if (!input) return '—';
  const d = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(d.getTime())) return '—';
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(d);
}

export function formatRelative(input: string | Date | null | undefined, locale = 'en'): string {
  if (!input) return '—';
  const d = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(d.getTime())) return '—';

  const diffMs = d.getTime() - Date.now();
  const abs = Math.abs(diffMs);
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  const week = 7 * day;

  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });

  if (abs < minute) return rtf.format(Math.round(diffMs / 1000), 'second');
  if (abs < hour) return rtf.format(Math.round(diffMs / minute), 'minute');
  if (abs < day) return rtf.format(Math.round(diffMs / hour), 'hour');
  if (abs < week) return rtf.format(Math.round(diffMs / day), 'day');
  return formatDate(d, locale);
}

export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—';
  if (seconds < 60) return `${Math.round(seconds)}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

export function truncate(s: string, max = 40): string {
  if (!s) return '';
  return s.length <= max ? s : s.slice(0, max - 1) + '…';
}
