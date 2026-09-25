/**
 * usePluginSurface — one plugin view's conversation with the server.
 *
 * A `modal` view (PluginViewModal) and an `inspector` section
 * (PluginInspectorSection) draw the same kind of screen and post the same
 * events; what differs is only the frame around it. So the conversation
 * lives here, once: the current surface, the values its nodes hold, the
 * debounced `change`, the footer press, the list row action, and what an
 * answer does — a new screen, a toast, `done`, or `{op}` (the server queued
 * a job; the caller registers the row and closes).
 *
 * Values: seeded from every fresh surface (`lib/surfaceValues.initialValues`),
 * replaced as the components edit them, and sent whole on every event as
 * `data.values`. After a `change` answer the person's current entries WIN
 * over the echo — they may have typed more while the request was out.
 */
import { computed, getCurrentInstance, onBeforeUnmount, ref } from 'vue';
import type { FileApi } from './useFileApi';
import type {
  PluginSurface,
  PluginViewEventBody,
  PluginViewEventData,
  SurfaceAction,
  SurfaceOpenRequest,
} from '../types/Plugins';
import { foreignText } from '../lib/direction';
import { isOpenRequest } from '../lib/surfaceOpen';
import { labelOf } from '../lib/pluginLabel';
import { initialValues } from '../lib/surfaceValues';
import { missingRequired, stripHiddenValues } from '../lib/surfaceConditions';

/** The contract's debounce for `form` edits → `event: "change"`. */
export const SURFACE_CHANGE_DEBOUNCE_MS = 300;

export interface PluginSurfaceHost {
  api: Pick<FileApi, 'pluginViewEvent'>;
  plugin: string;
  view: string;
  /** Adapter-qualified path the view was opened on — echoed on every event. */
  path?: () => string | undefined;
  /**
   * Every row the view was opened on (a selection) — echoed on every event
   * as `paths`, beside `path`. Absent or empty for a frame opened on one
   * row or on none (a deep link, a home page, the inspector, a public page),
   * which then sends `path` alone, exactly as before.
   */
  paths?: () => readonly string[] | undefined;
  locale: () => string;
  /** The words for an answer without a surface. */
  errorText: () => string;
}

export interface PluginSurfaceEvents {
  /** The server enqueued a job: the raw ops row. */
  onOp: (op: Record<string, unknown>) => void;
  /** The conversation is over (`done`, or after `{op}`). */
  onDone: () => void;
  onToast: (message: string) => void;
  /**
   * v3 §3.0 — the answer says "go to this file" (`lib/surfaceOpen`).
   *
   * ⚠ Raised AFTER `onDone` when the answer also carries `done: true`: the
   * screen that asked for the navigation is closed first, or the person
   * arrives at the new one with the old one still over it.
   *
   * ⚠ A frame with nowhere to navigate (a public page has no explorer
   * behind it) simply does not pass this in, and the request is dropped.
   */
  onOpen?: (req: SurfaceOpenRequest) => void;
}

export function usePluginSurface(host: PluginSurfaceHost, events: PluginSurfaceEvents) {
  const current = ref<PluginSurface | null>(null);
  const values = ref<Record<string, unknown>>({});
  const busy = ref(false);
  const failure = ref('');
  /**
   * v3 §2.1 — the visible, required fields a blocked submit is waiting for.
   *
   * ⚠ Pointed at, not merely refused. A submit that does nothing and says
   * nothing is the worst of the three possible behaviours; this list is what
   * puts the star and the "required" line on the fields themselves.
   */
  const invalidKeys = ref<string[]>([]);

  const footer = computed<SurfaceAction[]>(() => current.value?.actions ?? []);
  const errors = computed(() => current.value?.errors);

  /** A new screen. `keepValues` overlays what the person already entered. */
  function setSurface(s: PluginSurface | null, keepValues = false): void {
    current.value = s;
    failure.value = '';
    invalidKeys.value = [];
    const seeded = initialValues(s?.nodes);
    values.value = keepValues ? { ...seeded, ...values.value } : seeded;
  }

  function updateValues(v: Record<string, unknown>): void {
    values.value = v;
    // Answering one of them clears its mark as it is typed, rather than on
    // the next press: a field that has been filled must not stay red.
    if (invalidKeys.value.length) {
      const still = missingRequired(current.value?.nodes, v).map((f) => f.key);
      if (still.length !== invalidKeys.value.length) invalidKeys.value = still;
    }
  }

  let changeTimer: ReturnType<typeof setTimeout> | null = null;
  function cancelChange(): void {
    if (changeTimer !== null) clearTimeout(changeTimer);
    changeTimer = null;
  }
  /** A form edit: one `change` event 300 ms after the last keystroke. */
  function scheduleChange(): void {
    cancelChange();
    changeTimer = setTimeout(() => {
      changeTimer = null;
      void post('change');
    }, SURFACE_CHANGE_DEBOUNCE_MS);
  }

  let seq = 0;

  async function post(
    event: PluginViewEventBody['event'],
    actionId?: string,
    extra: Omit<PluginViewEventData, 'values'> = {},
  ): Promise<void> {
    if (!current.value) return;
    const blocking = event !== 'change';
    if (blocking) {
      cancelChange();
      if (busy.value) return;
      busy.value = true;
    }
    const mine = ++seq;
    failure.value = '';
    // ⚠⚠ The WHOLE selection, on every event (#64). The opening `run` carried
    // it; echoing only `path` here made the server answer every later event
    // about the first file, and the submit queue the job on that file alone.
    const selection = host.paths?.();
    try {
      const res = await host.api.pluginViewEvent(host.plugin, host.view, {
        path: host.path?.(),
        ...(selection?.length ? { paths: [...selection] } : {}),
        state: current.value?.state ?? {},
        event,
        action_id: actionId,
        // ⚠⚠ The values a HIDDEN field holds are dropped here, not blanked
        // (v3 §2.1: "a value belonging to a hidden field is dropped before
        // the job runs — it cannot arrive as a surprise"). The plugin is
        // re-asked the same question by the host at submit; this is the
        // client half of the same promise, and it also keeps a `change`
        // event from echoing back a field the step no longer shows.
        data: { values: stripHiddenValues(current.value?.nodes, values.value), ...extra },
      });
      // A `change` answer that arrives after a later event started is stale:
      // the later event's answer is the screen that counts.
      if (!blocking && mine !== seq) return;
      if (res.op) {
        events.onOp(res.op);
        events.onDone();
        return;
      }
      const next = res.surface;
      if (!next) {
        failure.value = host.errorText();
        return;
      }
      const toast = labelOf(next.toast, host.locale());
      if (toast) events.onToast(toast);
      // ⚠ The order is the contract's: `done` closes the screen, and only
      // then does the navigation happen. Reversed, the person lands on the
      // new screen with the finished one still drawn over it.
      const go = isOpenRequest(next.open) ? next.open : null;
      if (next.done) {
        events.onDone();
        if (go) events.onOpen?.(go);
        return;
      }
      setSurface(next, !blocking);
      if (go) events.onOpen?.(go);
    } catch (e) {
      if (!blocking && mine !== seq) return;
      // ⚠ An app's own error text never met the catalogue — isolate it.
      failure.value = foreignText(host.locale(), String((e as Error)?.message ?? e)) || host.errorText();
    } finally {
      if (blocking) busy.value = false;
    }
  }

  /**
   * A footer button: `submit` when it is the primary one, `action` otherwise.
   *
   * ⚠ A submit is refused locally while a visible, required field is empty.
   * The server refuses it too — that is the boundary — but a round trip to
   * be told "this is required" about a box that is on the screen is a round
   * trip the person should not have to make.
   */
  function press(a: SurfaceAction): Promise<void> {
    if (busy.value || a.disabled) return Promise.resolve();
    if (a.primary) {
      const missing = missingRequired(current.value?.nodes, values.value);
      invalidKeys.value = missing.map((f) => f.key);
      if (missing.length) return Promise.resolve();
    }
    return post(a.primary ? 'submit' : 'action', a.id);
  }

  /** A `list` row's button. */
  function rowAction(p: { action_id: string; row_id: string }): Promise<void> {
    return post('action', p.action_id, { row_id: p.row_id });
  }

  if (getCurrentInstance()) onBeforeUnmount(cancelChange);

  return {
    current,
    values,
    busy,
    failure,
    footer,
    errors,
    invalidKeys,
    setSurface,
    updateValues,
    scheduleChange,
    post,
    press,
    rowAction,
    dispose: cancelChange,
  };
}

export type PluginSurfaceStore = ReturnType<typeof usePluginSurface>;
