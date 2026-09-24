/**
 * surfaceValues — what a surface's nodes hold, as pure data.
 *
 * A plugin view is a conversation: every event the browser posts carries
 * `data.values`, one entry per node that holds a value (contract: "a node
 * with `id` that holds a value contributes `data.values[id]`"; a `form`'s
 * fields are FLAT, keyed by field key, because that is what the plugin
 * declared them as). The modal and the inspector section both post events,
 * so the walk that seeds the map from a fresh surface lives here, once, and
 * the two cannot seed differently.
 *
 * DOM-free on purpose: tests exercise the seeding and the field mapping
 * without mounting anything.
 */
import type { StorageField } from '../types/Connections';
import type {
  FileChooserNodeProps,
  FormNodeProps,
  PdfFieldsNodeProps,
  PeoplePickerNodeProps,
  PluginField,
  SurfaceNode,
} from '../types/Plugins';
import { fillValues, normalizeFields } from './pdfFields';
import { labelOf } from './pluginLabel';

/** Every `SurfaceNode` in the tree, depth first (rows are transparent). */
export function walkNodes(nodes: SurfaceNode[] | undefined, visit: (n: SurfaceNode) => void): void {
  for (const n of nodes ?? []) {
    visit(n);
    if (n.children?.length) walkNodes(n.children, visit);
  }
}

/**
 * The values a surface arrives with: `form.props.values` (flat, by field key,
 * else each field's `default`), `people-picker.props.value`,
 * `file-chooser.props.value`, a `pdf-fields` node's fields and an empty
 * `signature-pad` under the node's id. Nodes without an id hold nothing the
 * server can address, so they contribute nothing.
 */
export function initialValues(nodes: SurfaceNode[] | undefined): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  walkNodes(nodes, (n) => {
    const p = (n.props ?? {}) as Record<string, unknown>;
    if (n.type === 'form') {
      const fp = p as unknown as FormNodeProps;
      for (const f of fp.fields ?? []) {
        if (!f?.key) continue;
        const given = fp.values?.[f.key];
        if (given !== undefined) out[f.key] = multiSafe(f, given);
        else if (f.default !== undefined) out[f.key] = multiSafe(f, f.default);
        else if (f.multi === true && f.type === 'select') out[f.key] = [];
      }
      return;
    }
    if (!n.id) return;
    if (n.type === 'people-picker') {
      const v = (p as PeoplePickerNodeProps).value;
      out[n.id] = Array.isArray(v) ? v.filter((x) => x && typeof x.email === 'string') : [];
    } else if (n.type === 'file-chooser') {
      const v = (p as FileChooserNodeProps).value;
      out[n.id] = typeof v === 'string' ? v : '';
    } else if (n.type === 'pin-input') {
      out[n.id] = '';
    } else if (n.type === 'signature-pad') {
      out[n.id] = null;
    } else if (n.type === 'pdf-fields') {
      // Edit mode sends the whole array back; fill mode only the signer's
      // own filled fields (lib/pdfFields.fillValues) — seeded from what the
      // surface already carries, so a re-opened screen keeps its values.
      const pp = p as unknown as PdfFieldsNodeProps;
      const fields = normalizeFields(pp.fields);
      out[n.id] = pp.mode === 'fill' ? fillValues(fields, pp.signer) : fields;
    }
  });
  return out;
}

/**
 * A `multi` select always holds a LIST, whatever the plugin seeded it with.
 *
 * ⚠ A plugin that declares `multi: true` and sends `"pdf"` as the default is
 * not wrong on the wire — the contract says the value is "a string (single)
 * or a list of strings (multi)" — but a control that receives a string and
 * emits an array would send a different SHAPE back than it was given, and
 * the plugin would have to handle both. Normalised once, on the way in.
 */
function multiSafe(f: PluginField, v: unknown): unknown {
  if (f.multi !== true || f.type !== 'select') return v;
  if (Array.isArray(v)) return v.map((x) => String(x));
  return v === undefined || v === null || v === '' ? [] : [String(v)];
}

const FIELD_TYPES = new Set(['string', 'int', 'bool', 'password', 'select']);

/**
 * Is this `bool` a DECISION the person must read, or an on/off switch?
 *
 * The wire HAS a word for it now — `wire.Field.Style` (`switch` | `choice`)
 * — so the plugin's own answer comes first and the guess only runs when the
 * plugin said nothing:
 *
 *   - `style: "choice"` → two buttons, `style: "switch"` → the toggle. Said
 *     outright, it beats every heuristic below, including `options`: a
 *     plugin that names its two labels and still wants a switch gets one.
 *   - the plugin NAMED the two answers (`options`), which nobody does for a
 *     switch: "Keep the original / Replace it" is a decision;
 *   - the field is `required`, i.e. the form will not go until it is
 *     answered — and an unticked checkbox is indistinguishable from a
 *     question nobody has reached yet.
 *
 * Everything else stays the checkbox a settings toggle wants. ⚠ That last
 * line is a GUESS, and a wrong one for a question like "should this
 * installation add time stamps?" — which is why the contract asks a plugin
 * to say `style` when the answer matters, rather than this file getting
 * cleverer about bools it was told nothing about.
 *
 * ⚠ `select` never asks: it is buttons whatever `style` says (`storageFieldOf`
 * below), because a surface has no dropdown to fall back to.
 */
function boolIsDecision(f: PluginField): boolean {
  const style = String(f.style ?? '').toLowerCase();
  if (style === 'choice') return true;
  if (style === 'switch') return false;
  if ((f.options ?? []).length > 0) return true;
  return f.required === true;
}

/**
 * A plugin's field as the shared field renderer (`components/StorageFields`)
 * reads it. `i18n_key` is empty on purpose: the renderer asks the catalogue
 * first and falls back to `label` when the key is unknown, and `''` is
 * unknown — so a plugin's own words (already read in the viewer's language)
 * are what show. `secret` fields are drawn masked whatever their type says.
 *
 * ⚠⚠ v3: `choice` is set HERE, for every plugin `select`, and `advanced` is
 * not carried at all. Those two lines are the whole of "no dropdowns and no
 * hidden sections in a surface" — applied to the declaration on its way in,
 * so the renderer stays one renderer and no plugin can opt out of the rule
 * by drawing its own control.
 *
 * ⚠⚠ This is the ONLY mapper. Every screen that turns a plugin's `Field`
 * into something drawable goes through it: the surface form
 * (`components/plugin/nodes/SurfaceForm.vue`) AND the admin's own app
 * settings screen (web `components/plugins/AppPluginDetail.vue`). There used
 * to be a second one in the admin (`api/appPlugins.fieldToStorageField`); it
 * never set `choice`, dropped `multi` and carried an `advanced` the contract
 * had already deleted, so one manifest field meant two different things on
 * two screens. A new surface adds a CALLER here, never a mapper of its own.
 */
export function storageFieldOf(f: PluginField, locale: string): StorageField {
  const type = f.secret ? 'password' : FIELD_TYPES.has(String(f.type)) ? f.type : 'string';
  return {
    key: f.key,
    type: type as StorageField['type'],
    label: labelOf(f.label, locale) || f.key,
    help: labelOf(f.help, locale) || undefined,
    i18n_key: '',
    required: f.required === true,
    secret: f.secret === true,
    default: f.default,
    placeholder: labelOf(f.placeholder, locale) || undefined,
    options: (f.options ?? []).map((o) => ({ value: String(o.value), label: labelOf(o.label, locale) || String(o.value) })),
    min: f.min,
    max: f.max,
    monospace: f.monospace === true,
    // ⚠⚠ `text` IS the contract's long text (wire.Field: "string | password
    // | int | bool | select | text"), and the field renderer draws a textarea
    // only on `multiline`. Nothing mapped the two, so every long field a
    // plugin asked for arrived as a one-line box — including the signing
    // app's list of signers, under a help line that says "one per line".
    multiline: f.multiline === true || String(f.type) === 'text',
    choice: type === 'select' || (type === 'bool' && boolIsDecision(f)),
    multi: f.multi === true,
  };
}

/** A `pin-input`'s length: 4..8, default 6 — the contract's bounds. */
export function pinLength(raw: unknown): number {
  const n = Number(raw);
  if (!Number.isFinite(n)) return 6;
  return Math.min(8, Math.max(4, Math.trunc(n)));
}

/** Good enough for "this looks like an address" — the server validates. */
export function looksLikeEmail(s: string): boolean {
  const v = s.trim();
  const at = v.indexOf('@');
  return at > 0 && at < v.length - 1 && !/\s/.test(v) && v.indexOf('@', at + 1) === -1;
}
