/**
 * App-plugin surfaces over HTTP.
 *
 * A surface is data, so the renderer's rules can be measured at the API
 * boundary — before any component exists to draw it, and for every plugin
 * rather than for the one whose screen someone happened to open. This is
 * the browser-side twin of the Go test kit
 * (`backend/pkg/pluginkit/plugintest`): the same rules, applied to what the
 * server actually returns.
 *
 * ⚠ One place, not one per spec. Both plugin specs (signing and
 * converting) drive their wizards through `driveToJob` here; a rule that
 * moves moves once.
 */
import { expect, type APIRequestContext } from '@playwright/test';

/** filex's node catalogue — docs/PLUGIN-KIT.md, "Screens (surfaces)". */
export const NODE_TYPES = new Set([
  'text',
  'divider',
  'row',
  'form',
  'steps',
  'list',
  'progress',
  'people-picker',
  'pin-input',
  'file-chooser',
  'preview',
  'pdf-fields',
  'signature-pad',
]);

export const FIELD_TYPES = new Set(['string', 'password', 'int', 'bool', 'select', 'text', 'date']);

export type Text = Record<string, string>;

export interface Condition {
  key: string;
  equals: string[];
}

export interface Field {
  key: string;
  type: string;
  label?: string;
  help?: string;
  required?: boolean;
  secret?: boolean;
  default?: unknown;
  placeholder?: string;
  options?: { value: string; label: string }[];
  multi?: boolean;
  show_when?: Condition;
  required_when?: Condition;
  min?: number;
  max?: number;
}

export interface Node {
  id?: string;
  type: string;
  props?: Record<string, unknown>;
  children?: Node[];
}

export interface SurfaceAction {
  id: string;
  label: Text;
  primary?: boolean;
  danger?: boolean;
  disabled?: boolean;
}

export interface Surface {
  title?: Text;
  size?: string;
  state?: Record<string, unknown>;
  nodes?: Node[];
  actions?: SurfaceAction[];
  toast?: Text;
  done?: boolean;
  errors?: Record<string, Text>;
  job?: { action_id: string; params?: Record<string, unknown> };
}

export interface OpRow {
  id: number;
  kind: string;
  status: string;
  action?: string;
  plugin?: string;
}

/** Every node of the surface, depth first, with a readable path. */
export function walkNodes(nodes: Node[] | undefined, fn: (where: string, n: Node) => void) {
  const walk = (prefix: string, ns: Node[] | undefined) => {
    (ns ?? []).forEach((n, i) => {
      const where = `${prefix}[${i}](${n.type ?? '?'})`;
      fn(where, n);
      if (n.children?.length) walk(`${where}.children`, n.children);
    });
  };
  walk('nodes', nodes);
}

/** The form fields of a surface, flattened (their values arrive flat too). */
export function fieldsOf(s: Surface): Field[] {
  const out: Field[] = [];
  walkNodes(s.nodes, (_w, n) => {
    if (n.type === 'form') out.push(...((n.props?.fields as Field[]) ?? []));
  });
  return out;
}

export function fieldByKey(s: Surface, key: string): Field | undefined {
  return fieldsOf(s).find((f) => f.key === key);
}

/**
 * The options a `select` renders as buttons. filex draws a select as a ROW
 * OF CHOICE BUTTONS, never a dropdown, so this is literally what the person
 * sees — and a test that reads it is reading the screen, not the markup.
 */
export function choices(s: Surface, key: string): { value: string; label: string }[] {
  const f = fieldByKey(s, key);
  return f && f.type === 'select' ? (f.options ?? []) : [];
}

function conditionHolds(c: Condition | undefined, values: Record<string, unknown>): boolean {
  if (!c) return true;
  const got = values[c.key];
  const asString = typeof got === 'boolean' ? String(got) : ((got ?? '') as string);
  return (c.equals ?? []).includes(asString);
}

/**
 * The renderer's rules, measured on what the server returned. Mirrors
 * `plugintest.InspectSurface`: a breach here means filex would draw a hole,
 * a dropdown where a choice belongs, or a step asking two things at once.
 */
export function checkSurface(s: Surface, where = 'surface') {
  expect(s, `${where}: the plugin answered no surface`).toBeTruthy();

  const keys = new Map<string, string>();
  const ids = new Map<string, string>();
  let steps = 0;
  walkNodes(s.nodes, (at, n) => {
    expect(NODE_TYPES.has(n.type), `${where}.${at}: unknown node type "${n.type}"`).toBe(true);
    if (n.id) {
      expect(ids.has(n.id), `${where}.${at}: node id "${n.id}" is already used`).toBe(false);
      ids.set(n.id, at);
    }
    if (n.type === 'steps') {
      steps++;
      const items = (n.props?.items as { id: string; state: string }[]) ?? [];
      expect(items.length, `${where}.${at}: a steps node needs items`).toBeGreaterThan(0);
      const active = items.filter((i) => i.state === 'active').length;
      expect(active, `${where}.${at}: exactly one step is the one the person is on`).toBe(1);
    }
    if (n.type !== 'form') return;
    const fields = (n.props?.fields as Field[]) ?? [];
    const present = new Set(fields.map((f) => f.key));
    const values = (n.props?.values as Record<string, unknown>) ?? {};
    for (const f of fields) {
      expect(f.key, `${where}.${at}: a field with no key carries no value`).toBeTruthy();
      expect(keys.has(f.key), `${where}.${at}: field key "${f.key}" is used twice; values arrive FLAT`).toBe(false);
      keys.set(f.key, at);
      expect(FIELD_TYPES.has(f.type), `${where}.${at}.${f.key}: unknown field type "${f.type}"`).toBe(true);
      if (f.type === 'select') {
        expect(
          (f.options ?? []).length,
          `${where}.${at}.${f.key}: a select renders as a row of buttons, so its options must be there`,
        ).toBeGreaterThan(0);
        const seen = new Set<string>();
        for (const o of f.options ?? []) {
          expect(o.value, `${where}.${at}.${f.key}: an option with no value cannot be chosen`).toBeTruthy();
          expect(o.label, `${where}.${at}.${f.key}: an option with no label is an empty button`).toBeTruthy();
          expect(seen.has(o.value), `${where}.${at}.${f.key}: two buttons carry "${o.value}"`).toBe(false);
          seen.add(o.value);
        }
      } else {
        expect(f.multi, `${where}.${at}.${f.key}: \`multi\` is a select thing`).toBeFalsy();
      }
      for (const [what, c] of [
        ['show_when', f.show_when],
        ['required_when', f.required_when],
      ] as const) {
        if (!c) continue;
        expect(c.key, `${where}.${at}.${f.key}: ${what} names no field`).toBeTruthy();
        expect(
          present.has(c.key),
          `${where}.${at}.${f.key}: ${what} looks at "${c.key}", which is not a field of this form`,
        ).toBe(true);
        expect((c.equals ?? []).length, `${where}.${at}.${f.key}: ${what} has nothing to compare`).toBeGreaterThan(0);
      }
      // A hidden field's value is dropped before the job runs; a screen that
      // still carries one is asking for a surprise.
      if (f.show_when && !conditionHolds(f.show_when, values)) {
        const v = values[f.key];
        expect(
          v === undefined || v === null || v === '',
          `${where}.${at}.${f.key}: hidden by show_when, but the form still carries ${JSON.stringify(v)}`,
        ).toBe(true);
      }
    }
  });

  const primary = (s.actions ?? []).filter((a) => a.primary).length;
  expect(primary, `${where}: one step asks ONE thing — at most one primary button plus Back`).toBeLessThanOrEqual(1);
  const actionIDs = new Set<string>();
  for (const a of s.actions ?? []) {
    expect(a.id, `${where}: a footer button with no id`).toBeTruthy();
    expect(actionIDs.has(a.id), `${where}: two footer buttons share the id "${a.id}"`).toBe(false);
    actionIDs.add(a.id);
  }
  expect(steps, `${where}: more than one steps node`).toBeLessThanOrEqual(1);
}

/** Every Text on the surface, with where it was found. */
export function surfaceTexts(s: Surface, langs: string[]): { where: string; text: Text }[] {
  const out: { where: string; text: Text }[] = [];
  const isText = (v: unknown): v is Text => {
    if (!v || typeof v !== 'object' || Array.isArray(v)) return false;
    const e = Object.entries(v as Record<string, unknown>);
    if (!e.length) return false;
    if (!e.every(([k, val]) => /^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$/.test(k) && typeof val === 'string')) return false;
    return 'en' in (v as object) || e.every(([k]) => langs.includes(k));
  };
  const collect = (prefix: string, v: unknown) => {
    if (isText(v)) {
      out.push({ where: prefix, text: v });
      return;
    }
    if (Array.isArray(v)) {
      v.forEach((e, i) => collect(`${prefix}[${i}]`, e));
      return;
    }
    if (v && typeof v === 'object') {
      for (const [k, val] of Object.entries(v as Record<string, unknown>)) collect(`${prefix}.${k}`, val);
    }
  };
  if (s.title) out.push({ where: 'title', text: s.title });
  if (s.toast) out.push({ where: 'toast', text: s.toast });
  for (const [k, t] of Object.entries(s.errors ?? {})) out.push({ where: `errors.${k}`, text: t });
  (s.actions ?? []).forEach((a, i) => a.label && out.push({ where: `actions[${i}](${a.id}).label`, text: a.label }));
  walkNodes(s.nodes, (at, n) => collect(at + '.props', n.props));
  return out;
}

/**
 * Every Text carries every language the plugin promised. This is the rule
 * that keeps a screen from coming up half in one language — the pad saying
 * "Çiz / Yaz / Yükle" under an English heading.
 */
export function checkLanguages(s: Surface, langs: string[], where = 'surface') {
  if (!langs.length) return;
  for (const { where: at, text } of surfaceTexts(s, langs)) {
    for (const lang of langs) {
      expect(
        (text[lang] ?? '').trim(),
        `${where}.${at}: the manifest promises "${lang}", this Text has ${JSON.stringify(text)}`,
      ).not.toBe('');
    }
  }
}

const ONE_PIXEL_PNG =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

/**
 * Plausible values for every field a person can see, so a wizard can be
 * walked without the spec knowing that plugin's form by heart: the first
 * option of a choice, today for a date, the admin for a person, a one-pixel
 * signature for a pad. `given` wins over all of it.
 */
export function autoFill(s: Surface, given: Record<string, unknown> = {}): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  walkNodes(s.nodes, (_w, n) => {
    if (n.type === 'form') {
      for (const [k, v] of Object.entries((n.props?.values as Record<string, unknown>) ?? {})) values[k] = v;
    }
  });
  for (const f of fieldsOf(s)) {
    if (f.key in given) continue;
    if (f.show_when && !conditionHolds(f.show_when, { ...values, ...given })) continue;
    if (values[f.key] !== undefined && values[f.key] !== '' && values[f.key] !== null) continue;
    switch (f.type) {
      case 'select':
        if (f.options?.length) values[f.key] = f.multi ? [f.options[0].value] : f.options[0].value;
        break;
      case 'bool':
        values[f.key] = f.default ?? true;
        break;
      case 'int':
        values[f.key] = f.default ?? f.min ?? 1;
        break;
      case 'date':
        values[f.key] = new Date().toISOString().slice(0, 10);
        break;
      default:
        values[f.key] = f.default ?? 'e2e';
    }
  }
  walkNodes(s.nodes, (_w, n) => {
    const id = (n.props?.id as string) ?? n.id;
    if (!id || id in given || values[id] !== undefined) return;
    if (n.type === 'signature-pad') values[id] = { png_b64: ONE_PIXEL_PNG, mode: 'draw' };
    if (n.type === 'pin-input') values[id] = '1234';
    if (n.type === 'people-picker') values[id] = [{ email: 'admin@local', name: 'Admin' }];
    if (n.type === 'pdf-fields') values[id] = pdfFieldsValue(n);
  });
  return { ...values, ...given };
}

/**
 * What a `pdf-fields` node contributes.
 *
 * In `edit` mode it is the list of boxes placed on the page — fractions of
 * the page, origin top-left — so a wizard that asks "place a signature box"
 * gets one placed, assigned to the first signer it named. In `fill` mode it
 * is `{fields: [{id, value}]}`: a drawn box takes a picture, a text box
 * takes text. (docs/PLUGIN-KIT.md, the `pdf-fields` row.)
 */
function pdfFieldsValue(n: Node): unknown {
  const declared = (n.props?.fields as { id: string; type?: string; required?: boolean }[]) ?? [];
  if ((n.props?.mode as string) === 'fill') {
    const drawn = new Set(['signature', 'initials', 'stamp']);
    return {
      fields: declared.map((f) => ({
        id: f.id,
        value: drawn.has(f.type ?? 'signature') ? ONE_PIXEL_PNG : 'e2e',
      })),
    };
  }
  if (declared.length) return declared;
  const signers = (n.props?.signers as { id?: string }[]) ?? [];
  const assignee = signers[0]?.id;
  return [
    {
      id: 'e2e-sig-1',
      type: 'signature',
      page: 1,
      x: 0.1,
      y: 0.8,
      w: 0.3,
      h: 0.08,
      label: 'Signature',
      ...(assignee ? { assignee } : {}),
    },
  ];
}

/** Open a view (`event: "open"`) on a file. */
export async function openView(
  request: APIRequestContext,
  plugin: string,
  view: string,
  path: string,
): Promise<Surface> {
  const res = await request.get(
    `/api/files/plugins/views/${plugin}/${view}?path=${encodeURIComponent(path)}`,
  );
  expect(res.ok(), `open ${plugin}/${view}: ${res.status()} ${await res.text()}`).toBe(true);
  return ((await res.json()) as { surface: Surface }).surface;
}

export interface EventResult {
  /** The next screen, when the plugin answered one. */
  surface?: Surface;
  /** The queued job, when the surface asked for one (HTTP 202). */
  op?: OpRow;
  status: number;
  body: unknown;
}

/** Post a view event and say what came back: a screen, or a queued job. */
export async function surfaceEvent(
  request: APIRequestContext,
  plugin: string,
  view: string,
  body: Record<string, unknown>,
): Promise<EventResult> {
  const res = await request.post(`/api/files/plugins/views/${plugin}/${view}/event`, { data: body });
  const status = res.status();
  const parsed = (await res.json().catch(() => ({}))) as { surface?: Surface; op?: OpRow };
  expect([200, 202].includes(status), `${plugin}/${view} event: ${status} ${JSON.stringify(parsed)}`).toBe(true);
  return { surface: parsed.surface, op: parsed.op, status, body: parsed };
}

/** Start an action from the menu; it may answer a screen or queue a job. */
export async function runAction(
  request: APIRequestContext,
  plugin: string,
  action: string,
  paths: string[],
  params?: Record<string, unknown>,
): Promise<EventResult> {
  const res = await request.post(`/api/files/plugins/actions/${plugin}/${action}/run`, {
    data: { paths, ...(params ? { params } : {}) },
  });
  const status = res.status();
  const parsed = (await res.json().catch(() => ({}))) as { surface?: Surface; op?: OpRow };
  expect([200, 202].includes(status), `${plugin}/${action} run: ${status} ${JSON.stringify(parsed)}`).toBe(true);
  return { surface: parsed.surface, op: parsed.op, status, body: parsed };
}

export interface DriveOptions {
  /** Values the spec insists on, by field key, per step or for all steps. */
  values?: Record<string, unknown>;
  /** Languages every Text must carry ([] to skip the check). */
  languages?: string[];
  /** How many screens to walk before giving up (default 8). */
  maxSteps?: number;
  /** Called with each screen, for extra assertions. */
  onSurface?: (s: Surface, step: number) => void;
}

/**
 * Walk a wizard to the job it queues: check each screen against the
 * renderer's rules, fill what it asks for, press its primary button, and
 * repeat until the server answers 202. Returns the queued op.
 *
 * It throws with the screen it got stuck on rather than timing out
 * somewhere unhelpful — a wizard that cannot be finished is the finding.
 */
export async function driveToJob(
  request: APIRequestContext,
  plugin: string,
  view: string,
  path: string,
  opts: DriveOptions = {},
): Promise<{ op: OpRow; steps: number }> {
  const { values = {}, languages = [], maxSteps = 8 } = opts;
  let surface = await openView(request, plugin, view, path);
  for (let step = 0; step < maxSteps; step++) {
    checkSurface(surface, `${plugin}/${view} step ${step + 1}`);
    if (languages.length) checkLanguages(surface, languages, `${plugin}/${view} step ${step + 1}`);
    opts.onSurface?.(surface, step);

    const primary = (surface.actions ?? []).find((a) => a.primary);
    if (!primary) {
      throw new Error(
        `${plugin}/${view}: step ${step + 1} has no primary button, so the wizard cannot be finished — ` +
          `buttons: ${JSON.stringify(surface.actions ?? [])}`,
      );
    }
    const filled = autoFill(surface, values);
    const res = await surfaceEvent(request, plugin, view, {
      path,
      event: 'submit',
      action_id: primary.id,
      state: surface.state ?? {},
      data: { values: filled },
    });
    if (res.op) return { op: res.op, steps: step + 1 };
    if (!res.surface) throw new Error(`${plugin}/${view}: step ${step + 1} answered neither a screen nor a job`);
    if (res.surface.errors && Object.keys(res.surface.errors).length) {
      throw new Error(
        `${plugin}/${view}: step ${step + 1} refused the values ${JSON.stringify(filled)} — ` +
          `errors: ${JSON.stringify(res.surface.errors)}`,
      );
    }
    if (res.surface.done) throw new Error(`${plugin}/${view}: the wizard closed at step ${step + 1} without a job`);
    surface = res.surface;
  }
  throw new Error(`${plugin}/${view}: no job after ${maxSteps} screens`);
}

/** The manifest an installed app answers with, as the admin API reports it. */
export async function installedApp(
  request: APIRequestContext,
  name: string,
): Promise<{ id: number; name: string; status?: string; manifest?: Record<string, unknown> } | undefined> {
  const res = await request.get('/api/admin/app-plugins');
  if (!res.ok()) return undefined;
  const body = (await res.json()) as { plugins?: { id: number; name: string }[] };
  return (body.plugins ?? []).find((p) => p.name === name);
}

/** Remove every copy of an app, so a rerun starts from nothing. */
export async function removeApp(request: APIRequestContext, name: string) {
  const res = await request.get('/api/admin/app-plugins');
  if (!res.ok()) return;
  const body = (await res.json()) as { plugins?: { id: number; name: string }[] };
  for (const p of body.plugins ?? []) {
    if (p.name === name) await request.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

/** Poll the ops queue for this plugin's first matching job row. */
export async function pollForPluginOp(
  request: APIRequestContext,
  match: (o: OpRow) => boolean,
  timeoutMs = 20_000,
): Promise<OpRow | undefined> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const res = await request.get('/api/files/ops');
    if (res.ok()) {
      const found = ((await res.json()).ops as OpRow[]).find((o) => o && o.kind === 'plugin-action' && match(o));
      if (found) return found;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  return undefined;
}
