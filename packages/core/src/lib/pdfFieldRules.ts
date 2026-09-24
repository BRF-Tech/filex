/**
 * pdfFieldRules — what a `text` field on a PDF will accept, as pure data.
 *
 * A signing app does not only want "a box for some words": it wants a tax
 * number that is digits, a date in the shape the paper form prints, an
 * address that fits the line. So a `text` field may carry a `rule`, and the
 * browser enforces it WHILE the person types rather than after they submit —
 * an invalid signature page that only says so on the way out is a page that
 * gets signed twice.
 *
 * ⚠ The rule travels back to the plugin with the value
 * (`{fields: [{id, value, font?, rule?}]}`, docs/APP-PLUGINS-API.md →
 * `pdf-fields` props) so the stamper formats what it is handed instead of
 * guessing from the string: `01/02/2026` is a date to a rule and ambiguous
 * text to a parser.
 *
 * ⚠ This is a CONVENIENCE, not a security boundary. The browser is the only
 * thing that runs it; a plugin that cares re-checks what it is given. Same
 * relationship `applies` has with the server's re-check.
 */

/**
 * What a `text` field accepts. `any` is the default and means "anything".
 *
 * ⚠⚠ v3 §3.3 — there is no `date` rule any more. `date` is a FIELD TYPE,
 * and two ways to ask for the same thing is how you get two answers: a date
 * placed as a date field arrived as `YYYY-MM-DD` and a date typed into a text
 * field under the old rule arrived as `DD/MM/YYYY`, so the stamping plugin
 * had to guess which of its own boxes it was looking at. An older manifest's
 * `kind: "date"` is read as `any` rather than refused — the box keeps taking
 * what the person types, it just stops pretending to be a second date
 * control.
 */
export type PdfRuleKind = 'any' | 'number' | 'email';

export const PDF_RULE_KINDS: readonly PdfRuleKind[] = ['any', 'number', 'email'];

export interface PdfFieldRule {
  kind: PdfRuleKind;
  /** Shortest accepted length in characters (0 = no floor). */
  min?: number;
  /** Longest accepted length in characters (0 = no ceiling). */
  max?: number;
}

export function isRuleKind(v: unknown): v is PdfRuleKind {
  return typeof v === 'string' && PDF_RULE_KINDS.includes(v as PdfRuleKind);
}

/** The wire's `rule`, reduced to something drawable; `null` when there is none. */
export function normalizeRule(raw: unknown): PdfFieldRule | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const kind: PdfRuleKind = isRuleKind(r.kind) ? r.kind : 'any';
  const out: PdfFieldRule = { kind };
  const min = Math.trunc(Number(r.min));
  const max = Math.trunc(Number(r.max));
  if (Number.isFinite(min) && min > 0) out.min = min;
  if (Number.isFinite(max) && max > 0) out.max = max;
  // A bare `{kind: "any"}` with no bounds says nothing; treat it as no rule so
  // it does not travel back as noise.
  if (out.kind === 'any' && out.min === undefined && out.max === undefined) return null;
  return out;
}

/** `inputmode` for the on-screen keyboard a rule wants. */
export function ruleInputMode(rule: PdfFieldRule | null): 'text' | 'numeric' | 'email' {
  if (!rule) return 'text';
  if (rule.kind === 'number') return 'numeric';
  if (rule.kind === 'email') return 'email';
  return 'text';
}

/** The longest string the rule allows to be typed at all. */
export function ruleMaxLength(rule: PdfFieldRule | null): number | undefined {
  if (!rule) return undefined;
  return rule.max && rule.max > 0 ? rule.max : undefined;
}

/**
 * Type-as-you-go shaping. Returns what the field should hold after this
 * keystroke — never an error: a rule refuses a CHARACTER here, and says why
 * only in `ruleError` once there is something to judge.
 */
export function applyRule(raw: string, rule: PdfFieldRule | null): string {
  let v = String(raw ?? '');
  if (!rule) return v;
  if (rule.kind === 'number') {
    // Digits, one leading minus, one decimal separator — the separator kept
    // as typed so a Turkish keyboard's comma is not silently rewritten.
    const neg = v.startsWith('-');
    let seen = false;
    v = v
      .split('')
      .filter((ch) => {
        if (ch >= '0' && ch <= '9') return true;
        if ((ch === '.' || ch === ',') && !seen) {
          seen = true;
          return true;
        }
        return false;
      })
      .join('');
    if (neg) v = `-${v}`;
  } else if (rule.kind === 'email') {
    v = v.replace(/\s+/g, '');
  }
  const max = ruleMaxLength(rule);
  if (max && v.length > max) v = v.slice(0, max);
  return v;
}

/** Codes `ruleErrorText` turns into words; `''` when the value is acceptable. */
export type PdfRuleError = '' | 'number' | 'email' | 'min' | 'max';

/** Is `value` acceptable under `rule`? An empty value is never wrong here — `required` is a separate question. */
export function ruleError(value: unknown, rule: PdfFieldRule | null): PdfRuleError {
  const v = typeof value === 'string' ? value.trim() : '';
  if (!rule || v === '') return '';
  if (rule.min && v.length < rule.min) return 'min';
  if (rule.max && v.length > rule.max) return 'max';
  if (rule.kind === 'number') return /^-?\d+([.,]\d+)?$/.test(v) ? '' : 'number';
  if (rule.kind === 'email') return /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(v) ? '' : 'email';
  return '';
}

