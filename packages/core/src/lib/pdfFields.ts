/**
 * pdfFields — the `pdf-fields` node's rules, as pure data.
 *
 * What a field is, which fields a signer may touch, what the node sends
 * back in each mode, and how the editor adds, copies and colours boxes —
 * all DOM-free so the tests can hold the rules without pdf.js or a canvas.
 * `SurfacePdfFields.vue` is the drawing; this is the meaning.
 *
 * Wire shapes follow docs/APP-PLUGINS-API.md → "Frontend needs (M3)":
 *   edit mode  → `data.values[id]` = the whole `fields[]` array
 *   fill mode  → `data.values[id]` = `{fields: [{id, value}]}` for the
 *                signer's own fields that hold a value
 */
import type { PluginText } from '../types/Plugins';
import { clampFrac } from './pdfFieldsGeom';
import { isSignFontKey, type SignFontKey } from './signFonts';
import { normalizeRule, type PdfFieldRule } from './pdfFieldRules';

export const PDF_FIELD_TYPES = ['signature', 'initials', 'date', 'text', 'checkbox'] as const;
export type PdfFieldType = (typeof PDF_FIELD_TYPES)[number];

/**
 * How a signature or initials box is given: a drawing (the pad's draw or
 * upload), or a name typed in one of the shipped faces. Absent = drawn.
 */
export type PdfSignatureStyle = 'drawn' | 'typed';

/** A box whose value is a picture of somebody's hand: signature, initials. */
export function isDrawnType(type: string | undefined): boolean {
  return type === 'signature' || type === 'initials';
}

/**
 * One line a plugin can print under a signature — the node's
 * `stamp_lines[]` (docs/APP-PLUGINS-API.md → pdf-fields).
 *
 * ⚠⚠ The catalogue is the PLUGIN's, and so are the words: `examples` is how
 * the plugin itself would write the line for each signer id (`'*'` for a box
 * that belongs to anyone), so a card previews the plugin's wording instead
 * of this package inventing one that the paper then contradicts. The VALUES
 * printed on the day come from the plugin's signing record — never from
 * anything typed on this screen.
 */
export interface PdfStampLine {
  id: string;
  label: PluginText | string;
  examples?: Record<string, PluginText | string>;
  /** Printed when a box has made no choice of its own. */
  default?: boolean;
}

/**
 * A plugin's catalogue prop (`formats`, `stamp_lines`): the entries that
 * carry an id, each once, in the plugin's order, with their label — and
 * whatever else `more` reads off the raw entry.
 */
function catalogue<T extends { id: string; label: PluginText | string }>(
  raw: unknown,
  more: (entry: T, src: Record<string, unknown>) => void,
): T[] {
  if (!Array.isArray(raw)) return [];
  const out: T[] = [];
  const seen = new Set<string>();
  for (const src of raw as Array<Record<string, unknown>>) {
    if (!src || typeof src !== 'object') continue;
    const id = typeof src.id === 'string' ? src.id.trim() : '';
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const entry = { id, label: (src.label as PluginText | string) ?? id } as T;
    more(entry, src);
    out.push(entry);
  }
  return out;
}

/** The `stamp_lines` prop, dropped of anything that could not be offered. */
export function normalizeStampLines(raw: unknown): PdfStampLine[] {
  return catalogue<PdfStampLine>(raw, (entry, l) => {
    if (l.examples && typeof l.examples === 'object' && !Array.isArray(l.examples)) {
      entry.examples = l.examples as Record<string, PluginText | string>;
    }
    if (l.default === true) entry.default = true;
  });
}

/**
 * The line ids a box prints, in the CATALOGUE's order: its own choice, or
 * the catalogue's defaults when it has none. An explicit empty list is a
 * choice (nothing under the signature) and is kept as one.
 */
export function linesOf(field: Pick<PdfField, 'lines'>, catalogue: PdfStampLine[]): string[] {
  if (Array.isArray(field.lines)) {
    const want = new Set(field.lines);
    return catalogue.filter((l) => want.has(l.id)).map((l) => l.id);
  }
  return catalogue.filter((l) => l.default).map((l) => l.id);
}

/** The plugin's own wording of one line for a box's signer (`'*'` = anyone). */
export function stampExample(line: PdfStampLine, assignee: string | undefined): PluginText | string {
  const ex = line.examples ?? {};
  return ex[assignee || '*'] ?? ex['*'] ?? '';
}

export interface PdfField {
  id: string;
  type: PdfFieldType;
  /**
   * v3 3.1 - what the field is CALLED ("Ad soyad", "Imza", "Tarih").
   *
   * The reason it exists: a signer was shown a whole document and told to
   * find their boxes. A named field can be listed, asked for and reported -
   * the label shows in the box, in the fill form and in the audit trail - and
   * three boxes of the same type on one page stop being indistinguishable.
   * Absent: the type's own name stands in, which is what every field placed
   * before v3 has.
   */
  label?: string;
  /** 1-based. */
  page: number;
  x: number;
  y: number;
  w: number;
  h: number;
  /** A signer id (`signers[].id`). Absent: anybody's. */
  assignee?: string;
  required?: boolean;
  /**
   * v3 3.3 - false while the box has been DEFINED but not yet put on the
   * page.
   *
   * Naming a box and finding a place for it are two different jobs, and
   * doing them in one screen is what made the wizard's hardest step: a
   * palette of types, a rectangle drawn under the pointer and a strip of
   * properties for whichever box happened to be selected. Defined first,
   * placed after: the `define` screen writes boxes with `placed: false`,
   * and `place` hands them out one at a time. Absent means placed, so every
   * field written before this (and every other plugin's) reads as it did.
   */
  placed?: boolean;
  value?: unknown;
  /**
   * `text` only: what the box accepts (`lib/pdfFieldRules`). Enforced while
   * the person types and echoed back with the value, so the stamping plugin
   * formats what it is handed instead of guessing from the string.
   */
  rule?: PdfFieldRule;
  /**
   * `date` only: which layout the date is written in, as one of the ids the
   * surface offered in its `formats` prop (`DD.MM.YYYY`, `MM/DD/YYYY`,
   * `YYYY-MM-DD` for the signing app). It is the date field's answer to what
   * `rule` is for a text one, and it has to SURVIVE the round trip: a box
   * whose layout is dropped here comes back to the plugin as "no layout" and
   * is stamped in whatever that plugin defaults to — a form that asked for
   * `12/31/2000` quietly starts printing `31.12.2000`.
   *
   * ⚠ Not narrowed to a fixed set on purpose: the catalogue belongs to the
   * plugin, not to this package.
   */
  format?: string;
  /**
   * The face the words are drawn in — one of `lib/signFonts`' five keys. On
   * a `signature`/`initials` field it is the typed-signature face (and only
   * means something when `style` is `typed`); on a `text`/`date` one it is
   * the face the stamper should use. Absent = the screen's default.
   */
  font?: SignFontKey;
  /**
   * `signature`/`initials` only: drawn by hand (absent) or a name typed in
   * `font`. The requester decides; the signer's pad then offers exactly that.
   */
  style?: PdfSignatureStyle;
  /**
   * `signature`/`initials` only: which of the plugin's `stamp_lines` are
   * printed under this signature, by id. Absent = the plugin's default set;
   * `[]` = nothing under it.
   */
  lines?: string[];
}

export interface PdfSigner {
  id: string;
  label: PluginText | string;
  color?: string;
}

/** A sensible first size per type, as fractions of a portrait page. */
export const DEFAULT_FIELD_SIZE: Record<PdfFieldType, { w: number; h: number }> = {
  signature: { w: 0.28, h: 0.06 },
  initials: { w: 0.1, h: 0.05 },
  date: { w: 0.16, h: 0.035 },
  text: { w: 0.26, h: 0.035 },
  checkbox: { w: 0.03, h: 0.022 },
};

/** Six colours that read on both palettes; a signer without `color` takes one by position. */
export const SIGNER_PALETTE = ['#2563eb', '#16a34a', '#d97706', '#9333ea', '#dc2626', '#0891b2'];

export function isFieldType(v: unknown): v is PdfFieldType {
  return typeof v === 'string' && (PDF_FIELD_TYPES as readonly string[]).includes(v);
}

/** The palette a surface allows: `types` narrowed to known ones, else every type. */
export function allowedTypes(raw: unknown): PdfFieldType[] {
  if (!Array.isArray(raw)) return [...PDF_FIELD_TYPES];
  const picked = raw.filter(isFieldType);
  return picked.length ? PDF_FIELD_TYPES.filter((t) => picked.includes(t)) : [...PDF_FIELD_TYPES];
}

/** The wire's `fields[]`, dropped of anything a browser cannot draw. */
export function normalizeFields(raw: unknown): PdfField[] {
  if (!Array.isArray(raw)) return [];
  const out: PdfField[] = [];
  const seen = new Set<string>();
  for (const f of raw as Array<Record<string, unknown>>) {
    if (!f || typeof f !== 'object') continue;
    const id = String(f.id ?? '');
    if (!id || seen.has(id) || !isFieldType(f.type)) continue;
    seen.add(id);
    const box = clampFrac({ x: Number(f.x), y: Number(f.y), w: Number(f.w), h: Number(f.h) });
    const page = Math.max(1, Math.trunc(Number(f.page) || 1));
    const field: PdfField = { id, type: f.type, page, ...box };
    // ⚠ EXACTLY as typed: any script, inner spaces, and a trailing one.
    // This array round-trips through the plugin while the name is being
    // typed (`change` is debounced at 300 ms), so a trim here takes the
    // space back off between two words. Only a name that is nothing at all
    // is no name; one that is nothing but spaces is carried so the step can
    // refuse it and say why.
    if (typeof f.label === 'string' && f.label) field.label = f.label;
    if (typeof f.assignee === 'string' && f.assignee) field.assignee = f.assignee;
    if (f.required === true) field.required = true;
    // ⚠ Only `false` is carried. "Placed" is the ordinary state and says
    // nothing; a flag on every box would be noise on the wire and a second
    // thing to keep true.
    if (f.placed === false) field.placed = false;
    if (f.value !== undefined && f.value !== null) field.value = f.value;
    const rule = normalizeRule(f.rule);
    if (rule) field.rule = rule;
    if (typeof f.format === 'string' && f.format.trim()) field.format = f.format.trim();
    if (isSignFontKey(f.font)) field.font = f.font;
    // ⚠ Both ride only on a box whose value is a hand. They must survive the
    // round trip (the wizard's state is rebuilt from what this posts), and
    // they mean nothing on a date or a tick.
    if (isDrawnType(f.type)) {
      if (f.style === 'typed') field.style = 'typed';
      if (Array.isArray(f.lines)) {
        field.lines = [...new Set((f.lines as unknown[]).filter((x): x is string => typeof x === 'string' && !!x))].slice(0, 16);
      }
    }
    out.push(field);
  }
  return out;
}

/** Is this box on the page yet? Absent `placed` means yes. */
export function isPlaced(f: PdfField): boolean {
  return f.placed !== false;
}

/** The boxes still waiting for a place, in the order they were defined. */
export function unplacedFields(list: PdfField[]): PdfField[] {
  return list.filter((f) => !isPlaced(f));
}

/**
 * Define a box without placing it: a name, a type, whose it is — and a
 * sensible rectangle it will WEAR once somebody drops it on a page, so
 * placing is one click rather than a drawing exercise.
 */
export function defineField(
  list: PdfField[],
  type: PdfFieldType,
  opts: { label?: string; assignee?: string; required?: boolean } = {},
): PdfField {
  const size = DEFAULT_FIELD_SIZE[type];
  const f: PdfField = {
    id: newFieldId(type, list),
    type,
    page: 1,
    x: 0.1,
    y: 0.1,
    w: size.w,
    h: size.h,
    placed: false,
  };
  if (opts.label?.trim()) f.label = opts.label.trim();
  if (opts.assignee) f.assignee = opts.assignee;
  if (opts.required) f.required = true;
  return f;
}

/**
 * One date layout a surface offers: the id that travels as `format`, the
 * name to put on the button, and an EXAMPLE of a date written that way —
 * `DD.MM.YYYY` tells somebody nothing that `31.12.2000` does not tell them
 * better.
 */
export interface PdfDateFormat {
  id: string;
  /** A `Text` from the plugin, as `signers[].label` is; resolve with `lib/pluginLabel`. */
  label: PluginText | string;
  example?: string;
  /**
   * The TWO ANSWERS that made this layout, when the plugin sends them: the
   * arrangement (`DDMMYYYY`, `MMDDYY`, `YYYYMMDD`…) and the separator
   * (`.`, `/`, `-`, a space), each with the plugin's own name for it.
   * Present: the editor asks the two questions separately, which is what a
   * grid of layouts really is. Absent: one row of whole layouts, as before.
   */
  order?: string;
  orderLabel?: PluginText | string;
  separator?: string;
  separatorLabel?: PluginText | string;
}

/** The `formats` prop, dropped of anything that could not be drawn. */
export function normalizeFormats(raw: unknown): PdfDateFormat[] {
  return catalogue<PdfDateFormat>(raw, (entry, f) => {
    if (typeof f.example === 'string' && f.example.trim()) entry.example = f.example.trim();
    if (typeof f.order === 'string' && f.order.trim()) entry.order = f.order.trim();
    if (f.order_label) entry.orderLabel = f.order_label as PluginText | string;
    // ⚠ NOT trimmed and NOT tested for truthiness: a single space is one of
    // the separators offered, and both would throw it away.
    if (typeof f.separator === 'string' && f.separator !== '') entry.separator = f.separator;
    if (f.separator_label) entry.separatorLabel = f.separator_label as PluginText | string;
  });
}

/** The three captions the date control needs, as the plugin words them. */
export interface PdfDateLabels {
  order?: PluginText | string;
  separator?: PluginText | string;
  example?: PluginText | string;
}

/** The `date_labels` prop, with anything unusable dropped. */
export function normalizeDateLabels(raw: unknown): PdfDateLabels {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {};
  const src = raw as Record<string, unknown>;
  const out: PdfDateLabels = {};
  for (const k of ['order', 'separator', 'example'] as const) {
    const v = src[k];
    if (typeof v === 'string' ? v.trim() : v && typeof v === 'object') out[k] = v as PluginText | string;
  }
  return out;
}

/** One button in a date control: the value that travels, the plugin's name for it. */
export interface PdfDateChoice {
  id: string;
  label: PluginText | string;
}

/**
 * The two questions a `formats` catalogue really asks, or null when it
 * does not ask them — the order and the separator, each once, in the
 * catalogue's own order.
 *
 * ⚠ Null when ANY entry is missing half of itself: half a grid drawn as
 * two rows of buttons would offer combinations the plugin never listed,
 * and a layout the plugin does not know is a date stamped in a shape it
 * did not agree to.
 */
export function dateChoices(
  formats: PdfDateFormat[],
): { orders: PdfDateChoice[]; separators: PdfDateChoice[] } | null {
  if (!formats.length) return null;
  const orders: PdfDateChoice[] = [];
  const separators: PdfDateChoice[] = [];
  for (const f of formats) {
    if (f.order === undefined || f.separator === undefined) return null;
    if (!orders.some((o) => o.id === f.order)) orders.push({ id: f.order, label: f.orderLabel ?? f.order });
    if (!separators.some((s) => s.id === f.separator)) {
      separators.push({ id: f.separator, label: f.separatorLabel ?? f.separator });
    }
  }
  return orders.length && separators.length ? { orders, separators } : null;
}

/**
 * The layout id for a pair of answers: the catalogue's own entry for it,
 * or — when the plugin listed no such pair — the first entry with that
 * arrangement, so changing the separator can never leave the box with a
 * layout the plugin has never heard of.
 */
export function formatFor(formats: PdfDateFormat[], order: string, separator: string): string {
  const exact = formats.find((f) => f.order === order && f.separator === separator);
  if (exact) return exact.id;
  return formats.find((f) => f.order === order)?.id ?? formats[0]?.id ?? '';
}

/** The two answers behind a layout id, as the catalogue records them. */
export function splitFormat(
  formats: PdfDateFormat[],
  id: string,
): { order: string; separator: string } | null {
  const hit = formats.find((f) => f.id === id);
  if (!hit || hit.order === undefined || hit.separator === undefined) return null;
  return { order: hit.order, separator: hit.separator };
}

/** `DDMMYYYY` → ['DD','MM','YYYY']; anything else in the id is ignored. */
function orderTokens(order: string): string[] {
  const out: string[] = [];
  let i = 0;
  while (i < order.length) {
    if (order.startsWith('YYYY', i)) {
      out.push('YYYY');
      i += 4;
    } else if (order.startsWith('YY', i)) {
      out.push('YY');
      i += 2;
    } else if (order.startsWith('DD', i)) {
      out.push('DD');
      i += 2;
    } else if (order.startsWith('MM', i)) {
      out.push('MM');
      i += 2;
    } else i += 1;
  }
  return out;
}

/**
 * A real date written the chosen way — what the card shows under the two
 * questions, and what the paper will carry.
 *
 * ⚠ Plain ASCII digits, and no `Intl` anywhere near it. The reader's
 * language decides NOTHING here: the arrangement and the separator are the
 * ones just chosen, and the digits are the ones the stamper will print. An
 * example localised into Arabic-Indic numerals would be a promise the PDF
 * then breaks.
 */
export function dateExample(order: string, separator: string, on: Date): string {
  const parts = orderTokens(order).map((t) => {
    if (t === 'YYYY') return String(on.getFullYear()).padStart(4, '0');
    if (t === 'YY') return String(on.getFullYear() % 100).padStart(2, '0');
    if (t === 'MM') return String(on.getMonth() + 1).padStart(2, '0');
    return String(on.getDate()).padStart(2, '0');
  });
  return parts.join(separator);
}

export function normalizeSigners(raw: unknown): PdfSigner[] {
  if (!Array.isArray(raw)) return [];
  return (raw as Array<Record<string, unknown>>)
    .filter((s) => s && typeof s === 'object' && typeof s.id === 'string' && s.id)
    .map((s) => ({
      id: s.id as string,
      label: (s.label as PluginText | string) ?? (s.id as string),
      color: typeof s.color === 'string' ? s.color : undefined,
    }));
}

/** The colour a field is drawn in: its signer's, else by the signer's position, else the first. */
export function signerColor(signers: PdfSigner[], assignee: string | undefined): string {
  const idx = signers.findIndex((s) => s.id === assignee);
  if (idx < 0) return SIGNER_PALETTE[0];
  return signers[idx].color || SIGNER_PALETTE[idx % SIGNER_PALETTE.length];
}

/**
 * Fill mode: may `signer` act on this field? Their own, and any field that
 * was never assigned (a one-signer document rarely bothers with assignees).
 */
export function isFieldOf(field: Pick<PdfField, 'assignee'>, signer: string | undefined): boolean {
  if (!field.assignee) return true;
  return !!signer && field.assignee === signer;
}

/** What a filled value looks like on the wire, per type. */
export function hasValue(field: Pick<PdfField, 'type' | 'value'>): boolean {
  const v = field.value;
  if (field.type === 'checkbox') return v === true;
  return typeof v === 'string' ? v !== '' : v !== undefined && v !== null;
}

/** One entry of fill mode's answer. `label`/`font`/`rule` ride along when the field has them. */
export interface PdfFillEntry {
  id: string;
  value: unknown;
  /** v3 3.1 - the field's name, so the audit trail says what was filled in. */
  label?: string;
  font?: SignFontKey;
  rule?: PdfFieldRule;
}

/**
 * Fill mode's `data.values[id]`: the signer's own fields that hold a value.
 *
 * ⚠ `font` and `rule` travel WITH the value. The plugin stamps the PDF from
 * this array alone — it never sees the screen — so a signature typed in
 * Caveat that arrives as a bare string is stamped in whatever the plugin
 * happens to default to, which is how a signed document comes back in a
 * face the signer never chose.
 */
export function fillValues(fields: PdfField[], signer: string | undefined): { fields: PdfFillEntry[] } {
  return {
    fields: fields
      .filter((f) => isFieldOf(f, signer) && hasValue(f))
      .map((f) => {
        const e: PdfFillEntry = { id: f.id, value: f.value };
        if (f.label) e.label = f.label;
        if (f.font) e.font = f.font;
        if (f.rule) e.rule = f.rule;
        return e;
      }),
  };
}

/** The fields with the values a previous fill answer (`{fields: [{id, value}]}`) carried. */
export function applyFillValues(fields: PdfField[], raw: unknown): PdfField[] {
  const given = (raw as { fields?: unknown } | null)?.fields;
  if (!Array.isArray(given)) return fields;
  const byId = new Map<string, { value: unknown; font?: SignFontKey }>();
  for (const e of given as Array<{ id?: unknown; value?: unknown; font?: unknown }>) {
    if (e && typeof e.id === 'string') {
      byId.set(e.id, { value: e.value, font: isSignFontKey(e.font) ? e.font : undefined });
    }
  }
  return fields.map((f) => {
    const seeded = byId.get(f.id);
    if (!seeded) return f;
    return { ...f, value: seeded.value, ...(seeded.font ? { font: seeded.font } : {}) };
  });
}

/** One field's face changed (the signer picked another one from the field's own picker). */
export function withFont(fields: PdfField[], id: string, font: SignFontKey): PdfField[] {
  return fields.map((f) => (f.id === id ? { ...f, font } : f));
}

/** One field changed value. */
export function withValue(fields: PdfField[], id: string, value: unknown): PdfField[] {
  return fields.map((f) => {
    if (f.id !== id) return f;
    const next = { ...f };
    if (value === undefined || value === null || value === '' || value === false) delete next.value;
    else next.value = value;
    return next;
  });
}

/** Are all the signer's required fields filled? */
export function missingRequired(fields: PdfField[], signer: string | undefined): PdfField[] {
  return fields.filter((f) => f.required && isFieldOf(f, signer) && !hasValue(f));
}

/** `sig-1`, `sig-2`, … — never one already on the page. */
export function newFieldId(type: PdfFieldType, existing: Array<Pick<PdfField, 'id'>>): string {
  const prefix = { signature: 'sig', initials: 'ini', date: 'date', text: 'text', checkbox: 'chk' }[type];
  const ids = new Set(existing.map((f) => f.id));
  for (let n = 1; ; n++) {
    const id = `${prefix}-${n}`;
    if (!ids.has(id)) return id;
  }
}

/** A field placed at `at` (fractions, top-left) with the type's default size, kept inside the page. */
export function placeField(
  fields: PdfField[],
  type: PdfFieldType,
  page: number,
  at: { x: number; y: number },
  size: { w: number; h: number } = DEFAULT_FIELD_SIZE[type],
  assignee?: string,
): PdfField {
  const box = clampFrac({ x: at.x, y: at.y, w: size.w, h: size.h });
  const f: PdfField = { id: newFieldId(type, fields), type, page, ...box };
  if (assignee) f.assignee = assignee;
  return f;
}

/** The same box on another page, as a new field. */
export function copyToPage(fields: PdfField[], id: string, page: number): PdfField[] {
  const src = fields.find((f) => f.id === id);
  if (!src) return fields;
  const copy: PdfField = { ...src, id: newFieldId(src.type, fields), page: Math.max(1, Math.trunc(page)) };
  delete copy.value;
  return [...fields, copy];
}

/** Today as `YYYY-MM-DD` in the local calendar — the `date` field's auto value. */
export function todayIso(now: Date = new Date()): string {
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, '0');
  const d = String(now.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
}
