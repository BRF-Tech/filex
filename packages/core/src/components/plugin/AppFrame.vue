<script setup lang="ts">
/**
 * AppFrame — the ONE frame an app's own interface runs in, wherever it is
 * opened: a dialog (`modal`), a tab of its own (`page`), a section of the
 * details panel (`inspector`), the app's home screen (`home`), or a file's
 * viewer (`viewer`). The web app, the desktop app and every embed draw this
 * same component (docs/APP-PLUGINS-API.md → An app's own interface).
 *
 * ⚠⚠ What makes the frame a sandbox, each rule measured in Chrome, Firefox
 * and WebKit before it was written (design report, 2026-09-27):
 *
 *  - The element is built by hand with `sandbox="allow-scripts"` set BEFORE
 *    `src` and before it is in the document. A sandbox added afterwards
 *    applies from the NEXT navigation only: the first document runs with
 *    filex's own origin and reads `sessionStorage['filex.bearer']` (lesson
 *    #635). A template `<iframe :src sandbox>` leaves that order to the
 *    renderer, so it is not used.
 *  - No `allow-same-origin`, ever: with it, an interface served from filex's
 *    own origin IS filex (measured: the bearer read in all three).
 *  - `referrerpolicy="no-referrer"` and no `allow` attribute: no camera, no
 *    microphone, no clipboard, no fullscreen for the frame itself.
 *  - The bridge (lib/appBridge) takes the interface's hello only from THIS
 *    element's window, answers with a MessagePort once, and closes it when
 *    the frame loads a second document. Reloading = a new element.
 *
 * A newer version approved while the frame is open (realtime `app.updated`,
 * lib/appUpdates) is said above the frame with "Reload" — which asks about
 * unsaved changes first — and told to the interface (`app.updated`). The
 * first opening after an approval says "X was updated to 1.3.0" once per
 * person (account preferences, lib/appState).
 *
 * Every call the interface makes is decided here: what it may read and save
 * (the files it was opened with, the grants it holds, read-only), what it may
 * ask of the module. The server's policy gives the interface no connection
 * of its own (`connect-src 'none'`), no storage and no cookie — ⚠ not a
 * promise that it cannot send anything out: WebRTC ignores a page's policy
 * (closed by Connection-Allowlist in Chrome, by the bootstrap in Firefox;
 * docs/APP-PLUGINS.md → What a sandbox cannot promise).
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import {
  BRIDGE_VERSION,
  LIMITS,
  type ConfirmParams,
  type DownloadParams,
  type EngineCallParams,
  type FileInfo,
  type JobSubmitParams,
  type ReadParams,
  type SaveAsParams,
  type SaveParams,
  type Session,
  type Theme,
  type ToastParams,
} from '@brftech/filex-app-ui/protocol';
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { FileApi } from '../../composables/useFileApi';
import type { PluginUIRef } from '../../types/Plugins';
import { useLocale } from '../../composables/useLocale';
import { useSystemDark } from '../../composables/useSystemDark';
import { localeDir } from '../../lib/direction';
import { createAppBridge, BridgeFailure, type AppBridge } from '../../lib/appBridge';
import { appStateGet, appStateSet, AppStateTooLarge, appSeenVersion, markAppSeen } from '../../lib/appState';
import { onAppUpdated } from '../../lib/appUpdates';
import { labelOf } from '../../lib/pluginLabel';
import PluginConfirmModal from './PluginConfirmModal.vue';
import Modal from '../../modals/Modal.vue';

/** A file the interface was opened with. `path` is adapter-qualified. */
export interface AppFrameFile {
  path: string;
  name: string;
  size?: number;
  mime?: string;
  /** Cannot be written (read-only storage, view-only opening). */
  readOnly?: boolean;
}

const props = defineProps<{
  api: FileApi;
  /** The app (plugin name) and the view the interface draws. */
  app: string;
  version?: string;
  view: string;
  placement: 'modal' | 'page' | 'inspector' | 'home' | 'viewer';
  ui: PluginUIRef;
  /** The files it was opened with (none for a home screen). */
  files?: AppFrameFile[];
  /**
   * Where `file.save` of the first file writes: the file itself, or — for a
   * draft (issue #71) — the draft the host is editing it as. Absent: the
   * file's own path.
   */
  savePath?: string;
  /** The whole opening is view-only: nothing may be saved over. */
  readOnly?: boolean;
  locale: LocaleCode;
  theme?: ThemeMode;
  /** The person, as the interface may know them: a display name only. */
  userName?: string;
  /** The frame's accessible name (the view's label). */
  title?: string;
  /** Pick a folder for `file.saveAs` (filex's own dialog); null = cancelled.
   *  Absent: save-as is not offered here. */
  pickFolder?: () => Promise<string | null>;
}>();

const emit = defineEmits<{
  (e: 'connected'): void;
  (e: 'dirty', dirty: boolean): void;
  (e: 'title', text: string): void;
  (e: 'toast', t: { text: string; tone: NonNullable<ToastParams['tone']> }): void;
  (e: 'close'): void;
  (e: 'saved', r: { path: string; size: number; index: number }): void;
  (e: 'saved-as', r: { path: string; name: string; size: number }): void;
  (e: 'op', op: Record<string, unknown>): void;
}>();

const { t } = useLocale(() => props.locale);
const systemDark = useSystemDark();
const host = ref<HTMLElement | null>(null);
let frame: HTMLIFrameElement | null = null;
let bridge: AppBridge | null = null;
const connected = ref(false);
/** The interface said it holds changes that are not saved (`ui.dirty`). */
const dirty = ref(false);
/** The unsaved-changes question, while it is on screen. */
const closeAsk = ref<((v: 'save' | 'discard' | 'keep') => void) | null>(null);
const closeBusy = ref(false);
/** Bumped to replace the element (a reload): one bridge per element. */
const generation = ref(0);
/** The interface's newer address after "Reload" on an approved version. */
const liveUI = ref<PluginUIRef | null>(null);
/** What is open: the newer interface once reloaded to it, else the one given. */
const current = computed<PluginUIRef>(() => liveUI.value ?? props.ui);
/** A newer version approved while this frame is open ("" = none). */
const newer = ref('');
/** "Updated to" said once per person, on the first opening after an approval. */
const updatedNote = ref('');

const src = computed(() => props.api.appUIUrl(current.value.url));
const grants = computed(() => new Set(current.value.grants ?? []));
const appVersion = computed(() => liveUI.value?.version || props.version || props.ui.version || '');
const mode = computed<'light' | 'dark'>(() =>
  props.theme === 'dark' || (props.theme !== 'light' && systemDark.value) ? 'dark' : 'light',
);

/** The `--fe-*` tokens the interface is handed, as the explorer resolves them here. */
const TOKENS = [
  '--fe-bg', '--fe-bg-elev', '--fe-bg-hover', '--fe-bg-selected',
  '--fe-border', '--fe-border-soft', '--fe-border-strong',
  '--fe-text', '--fe-text-muted', '--fe-text-on-primary',
  '--fe-primary', '--fe-primary-hover', '--fe-primary-soft', '--fe-primary-ink',
  '--fe-danger', '--fe-danger-hover', '--fe-warning', '--fe-ok',
  '--fe-radius', '--fe-radius-sm', '--fe-radius-md', '--fe-radius-lg',
  '--fe-font', '--fe-font-mono', '--fe-text-xs', '--fe-text-sm', '--fe-text-md',
  '--fe-shadow', '--fe-shadow-sm',
];

function themeInfo(): Theme {
  const tokens: Record<string, string> = {};
  if (host.value && typeof getComputedStyle === 'function') {
    const cs = getComputedStyle(host.value);
    for (const k of TOKENS) {
      const v = cs.getPropertyValue(k).trim();
      if (v) tokens[k] = v;
    }
  }
  return { mode: mode.value, tokens };
}

function extOf(name: string): string {
  const i = name.lastIndexOf('.');
  return i > 0 ? name.slice(i + 1).toLowerCase() : '';
}

function fileInfos(): FileInfo[] {
  const writable = grants.value.has('files:write') && !props.readOnly;
  return (props.files ?? []).map((f, index) => ({
    index,
    name: f.name,
    ext: extOf(f.name),
    size: f.size ?? 0,
    mime: f.mime ?? '',
    readOnly: !writable || !!f.readOnly,
  }));
}

function session(): Session {
  return {
    v: BRIDGE_VERSION,
    app: { name: props.app, version: appVersion.value },
    view: { id: props.view, placement: props.placement },
    locale: props.locale,
    dir: localeDir(props.locale),
    theme: themeInfo(),
    user: { name: props.userName ?? '' },
    files: fileInfos(),
    grants: [...grants.value],
  };
}

function need(grant: string) {
  if (!grants.value.has(grant)) throw new BridgeFailure('not_granted', `this app was not granted ${grant}`);
}

function fileAt(index: unknown): AppFrameFile {
  const i = typeof index === 'number' ? index : 0;
  const f = (props.files ?? [])[i];
  if (!Number.isInteger(i) || !f) throw new BridgeFailure('not_found', `no file at ${String(index)}`);
  return f;
}

/**
 * What a save hands us, as a body the API sends: a string or bytes as a Blob,
 * a stream AS A STREAM — the API sends it in chunks as it is read, so a large
 * document never sits whole in this page.
 */
async function toBody(data: unknown, mime?: string): Promise<Blob | ReadableStream<Uint8Array>> {
  const type = typeof mime === 'string' && mime.length < 200 ? mime : '';
  if (typeof data === 'string') return new Blob([data], { type: type || 'text/plain;charset=utf-8' });
  if (data instanceof ArrayBuffer) return new Blob([data], { type });
  if (typeof ReadableStream !== 'undefined' && data instanceof ReadableStream) return data as ReadableStream<Uint8Array>;
  throw new BridgeFailure('invalid', 'save takes a string, an ArrayBuffer or a ReadableStream');
}

/* ── the bridge's methods: every decision is made here ─────────────────── */

const confirmAsk = ref<(ConfirmParams & { resolve: (v: boolean) => void }) | null>(null);

/**
 * Who is speaking, in the words filex puts around what an app says (a toast,
 * a question, a request for consent): the view's label, with the app's
 * installed name when the label does not already say it — a label cannot
 * pass itself off as filex (security review UI-14).
 */
const who = computed(() => {
  const label = (props.title || '').trim();
  if (!label) return props.app;
  return label.toLowerCase().includes(props.app.toLowerCase()) ? label : `${label} (${props.app})`;
});

/**
 * What only the PERSON may start (security review UI-5, UI-10, and a
 * download): a job, a copy to the clipboard. On a gesture — the person
 * clicked or typed in the frame a moment ago, which the browser tells this
 * page too (`navigator.userActivation`) — it goes ahead; one gesture stands
 * for ONE such call. Otherwise filex asks, above the frame and in its own
 * words, naming the app; "no" is `cancelled`.
 */
const consentAsk = ref<{ text: string; resolve: (v: boolean) => void } | null>(null);
/** A browser's transient activation lasts about this long (Chrome, Firefox). */
const GESTURE_MS = 5000;
let gestureSpentAt = Number.NEGATIVE_INFINITY;
/**
 * When the person last acted on filex's OWN page — a click, a key, a touch.
 * The browser tells this page about those and never about the frame's, and
 * the activation they give is NOT a gesture in the interface: measured
 * 2026-09-27 (Chrome, Firefox), the double-click that opened an app left the
 * page active long enough for the app to fill the clipboard at once, without
 * a question. So an activation within GESTURE_MS of one of filex's own
 * gestures does not count — and at mount, an activation already live is the
 * one that opened the app.
 */
let hostGestureAt = Number.NEGATIVE_INFINITY;

function activationLive(): boolean {
  const ua = typeof navigator !== 'undefined' ? (navigator as Navigator & { userActivation?: { isActive?: boolean } }).userActivation : undefined;
  return !!ua?.isActive;
}

function onHostGesture(): void {
  hostGestureAt = Date.now();
}

function gestureNow(): boolean {
  if (!activationLive()) return false;
  const now = Date.now();
  if (now - gestureSpentAt < GESTURE_MS || now - hostGestureAt < GESTURE_MS) return false;
  gestureSpentAt = now;
  return true;
}

async function consent(text: string): Promise<void> {
  if (gestureNow()) return;
  await askConsent(text);
}

/**
 * "Allow" answers only once the question has been on screen a moment. The
 * app decides WHEN it asks, so it can ask right under a click it invited
 * ("double-click here"): the second click would land on a button that
 * appeared beneath it. And the row pushes the frame down, which Chromium's
 * hit-testing follows a frame late (measured 2026-09-27: a click at once
 * went to the frame, not the row). So Allow is disabled for CONSENT_ARM_MS;
 * "Don't allow" never is.
 */
const CONSENT_ARM_MS = 600;
const consentArmed = ref(false);
let consentArmTimer: ReturnType<typeof setTimeout> | null = null;

async function askConsent(text: string): Promise<void> {
  if (consentAsk.value) throw new BridgeFailure('unavailable', 'a question is already on screen');
  consentArmed.value = false;
  if (consentArmTimer) clearTimeout(consentArmTimer);
  consentArmTimer = setTimeout(() => {
    consentArmed.value = true;
  }, CONSENT_ARM_MS);
  const yes = await new Promise<boolean>((resolve) => {
    consentAsk.value = { text, resolve };
  });
  if (!yes) throw new BridgeFailure('cancelled');
}

/* The consent row's sentences, one function each so the key stays a literal
 * the catalogue checks can find, and none is built once at setup. */
function clipboardQuestion(): string {
  return t('appframe.consent_clipboard', { app: who.value });
}
function jobQuestion(action: string): string {
  return t('appframe.consent_job', { app: who.value, action });
}
function downloadQuestion(name: string): string {
  return t('appframe.consent_download', { app: who.value, name });
}

function answerConsent(yes: boolean) {
  if (yes && !consentArmed.value) return;
  const q = consentAsk.value;
  consentAsk.value = null;
  if (consentArmTimer) clearTimeout(consentArmTimer);
  consentArmTimer = null;
  q?.resolve(yes);
}

/* ── ui.download: a file for the person's own disk ──────────────────── */

/** A name for the person's disk: a leaf — nothing that is a path. */
function downloadName(v: unknown): string {
  const name = typeof v === 'string' ? v.trim() : '';
  if (!name || name.length > 255 || name === '.' || name === '..' || /[\\/\u0000-\u001f]/.test(name)) {
    throw new BridgeFailure('invalid', 'name is a file name, without a folder');
  }
  return name;
}

function tooLarge(): BridgeFailure {
  return new BridgeFailure('too_large', `at most ${LIMITS.maxDownloadBytes >> 20} MiB`);
}

/** The data as byte chunks, whatever it came as; a stream is read as it is
 *  used, and let go of (cancelled) when the reading stops early. */
async function* chunksOf(data: string | ArrayBuffer | ReadableStream<Uint8Array>): AsyncGenerator<Uint8Array> {
  if (typeof data === 'string') {
    yield new TextEncoder().encode(data);
    return;
  }
  if (data instanceof ArrayBuffer) {
    yield new Uint8Array(data);
    return;
  }
  const reader = data.getReader();
  let done = false;
  try {
    for (;;) {
      const r = await reader.read();
      if (r.done) {
        done = true;
        return;
      }
      if (r.value) yield r.value;
    }
  } finally {
    if (!done) await reader.cancel().catch(() => undefined);
  }
}

type SaveFilePicker = (o: { suggestedName: string }) => Promise<{
  createWritable(): Promise<{ write(c: Uint8Array): Promise<void>; close(): Promise<void>; abort?(): Promise<void> }>;
}>;

/**
 * Hand the file to the person's disk. Where the browser has File System
 * Access (Chromium) the bytes are streamed into the file the person picks —
 * never whole in this page; elsewhere (or where the picker is refused here: a
 * frame of another site) they become a Blob the browser downloads. Either
 * way nothing past LIMITS.maxDownloadBytes is written.
 */
async function saveToDisk(name: string, data: string | ArrayBuffer | ReadableStream<Uint8Array>, type: string) {
  const picker = (window as unknown as { showSaveFilePicker?: SaveFilePicker }).showSaveFilePicker;
  if (typeof picker === 'function') {
    let handle: Awaited<ReturnType<SaveFilePicker>> | null = null;
    try {
      handle = await picker.call(window, { suggestedName: name });
    } catch (e) {
      if ((e as DOMException)?.name === 'AbortError') throw new BridgeFailure('cancelled');
      handle = null; // not offered here: the Blob below
    }
    if (handle) {
      const w = await handle.createWritable();
      let size = 0;
      try {
        for await (const chunk of chunksOf(data)) {
          size += chunk.byteLength;
          if (size > LIMITS.maxDownloadBytes) throw tooLarge();
          await w.write(chunk);
        }
      } catch (e) {
        await w.abort?.().catch(() => undefined);
        throw e;
      }
      await w.close();
      return { saved: true as const, size };
    }
  }
  const parts: Uint8Array[] = [];
  let size = 0;
  for await (const chunk of chunksOf(data)) {
    size += chunk.byteLength;
    if (size > LIMITS.maxDownloadBytes) throw tooLarge();
    parts.push(chunk);
  }
  const url = URL.createObjectURL(new Blob(parts as BlobPart[], { type }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.rel = 'noopener';
  a.style.display = 'none';
  document.body.appendChild(a);
  try {
    a.click();
  } finally {
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  }
  return { saved: true as const, size };
}

/** An action's label, for the question; its id when the list cannot say. */
async function actionLabel(id: string): Promise<string> {
  try {
    const list = await props.api.pluginActions();
    const a = list.actions.find((x) => x.plugin === props.app && x.id === id);
    const label = a ? labelOf(a.label, props.locale) : '';
    if (label) return label;
  } catch {
    /* no list: the id */
  }
  return id;
}

const handlers = {
  'session.get': () => session(),

  async 'file.read'(params: unknown) {
    need('files:read');
    const p = (params ?? {}) as ReadParams;
    const f = fileAt(p.index);
    const res = await props.api.fetchResponse(f.path);
    const size = Number(res.headers.get('content-length') ?? f.size ?? 0) || (f.size ?? 0);
    const mime = f.mime || res.headers.get('content-type') || '';
    const out = { name: f.name, size, mime };
    switch (p.as ?? 'stream') {
      case 'text': {
        if (size > LIMITS.maxTextBytes) throw new BridgeFailure('too_large', 'read a file this large as a stream');
        return { ...out, text: await res.text() };
      }
      case 'bytes':
        return { ...out, bytes: await res.arrayBuffer() };
      case 'stream':
        if (!res.body) return { ...out, bytes: await res.arrayBuffer() };
        return { ...out, stream: res.body };
      default:
        throw new BridgeFailure('invalid', 'as is stream, bytes or text');
    }
  },

  async 'file.save'(params: unknown) {
    need('files:write');
    const p = (params ?? {}) as SaveParams;
    const index = typeof p.index === 'number' ? p.index : 0;
    const f = fileAt(index);
    if (props.readOnly || f.readOnly) throw new BridgeFailure('read_only', 'this file cannot be saved over here');
    const body = await toBody(p.data, p.mime);
    const target = index === 0 && props.savePath ? props.savePath : f.path;
    const r = await props.api.pluginUISave(props.app, props.view, { path: target }, body);
    emit('saved', { path: r.path || target, size: r.size, index });
    // Saved: whatever the interface held is on the storage now. It says
    // `dirty(true)` again with its next change.
    dirty.value = false;
    emit('dirty', false);
    return { saved: true, size: r.size };
  },

  async 'file.saveAs'(params: unknown) {
    need('files:write');
    const p = (params ?? {}) as SaveAsParams;
    const name = typeof p.name === 'string' ? p.name.trim() : '';
    if (!name || name.includes('/') || name.includes('\\') || name === '.' || name === '..' || name.length > 255) {
      throw new BridgeFailure('invalid', 'name is a file name, without a folder');
    }
    if (!props.pickFolder) throw new BridgeFailure('unavailable', 'save-as is not offered here');
    const body = await toBody(p.data, p.mime);
    const dir = await props.pickFolder();
    if (!dir) throw new BridgeFailure('cancelled');
    const r = await props.api.pluginUISave(props.app, props.view, { dir, name }, body);
    emit('saved-as', { path: r.path, name: r.name, size: r.size });
    return { saved: true, name: r.name, size: r.size };
  },

  'ui.dirty'(params: unknown) {
    dirty.value = !!(params as { dirty?: boolean } | null)?.dirty;
    emit('dirty', dirty.value);
    return null;
  },

  'ui.title'(params: unknown) {
    const text = String((params as { text?: unknown } | null)?.text ?? '').slice(0, LIMITS.maxLineChars);
    emit('title', text);
    return null;
  },

  'ui.toast'(params: unknown) {
    const p = (params ?? {}) as ToastParams;
    const tone = p.tone === 'success' || p.tone === 'warning' || p.tone === 'error' ? p.tone : 'info';
    emit('toast', { text: `${who.value}: ${String(p.text ?? '').slice(0, LIMITS.maxLineChars)}`, tone });
    return null;
  },

  'ui.confirm'(params: unknown) {
    const p = (typeof params === 'object' && params ? params : {}) as ConfirmParams;
    if (confirmAsk.value) throw new BridgeFailure('unavailable', 'a question is already on screen');
    return new Promise<boolean>((resolve) => {
      confirmAsk.value = {
        text: String(p.text ?? '').slice(0, 1000),
        title: p.title ? String(p.title).slice(0, LIMITS.maxLineChars) : undefined,
        danger: !!p.danger,
        resolve,
      };
    });
  },

  'ui.close'() {
    emit('close');
    return null;
  },

  async 'clipboard.write'(params: unknown) {
    const text = String((params as { text?: unknown } | null)?.text ?? '');
    if (text.length > 1 << 20) throw new BridgeFailure('too_large', 'at most 1 MiB of text');
    if (typeof navigator === 'undefined' || !navigator.clipboard?.writeText) {
      throw new BridgeFailure('unavailable', 'this browser offers no clipboard here');
    }
    // On a gesture in the frame the page writes at once — except where the
    // browser wants the gesture in THIS document (WebKit, measured: the
    // write is refused): then the person is asked, and their click on
    // Allow is the gesture.
    if (gestureNow()) {
      try {
        await navigator.clipboard.writeText(text);
        return null;
      } catch (e) {
        if ((e as DOMException)?.name !== 'NotAllowedError') throw e;
      }
    }
    await askConsent(clipboardQuestion());
    await navigator.clipboard.writeText(text);
    return null;
  },

  async 'ui.download'(params: unknown) {
    need('ui:download');
    const p = (params ?? {}) as DownloadParams;
    const name = downloadName(p.name);
    const data = p.data as unknown;
    const isStream = typeof ReadableStream !== 'undefined' && data instanceof ReadableStream;
    if (typeof data !== 'string' && !(data instanceof ArrayBuffer) && !isStream) {
      throw new BridgeFailure('invalid', 'download takes a string, an ArrayBuffer or a ReadableStream');
    }
    const known = typeof data === 'string' ? new Blob([data]).size : data instanceof ArrayBuffer ? data.byteLength : 0;
    if (known > LIMITS.maxDownloadBytes) throw tooLarge();
    const type = typeof p.mime === 'string' && /^[\w.+-]+\/[\w.+-]+$/.test(p.mime) ? p.mime : 'application/octet-stream';
    try {
      await consent(downloadQuestion(name));
    } catch (e) {
      if (isStream) await (data as ReadableStream).cancel().catch(() => undefined);
      throw e;
    }
    return saveToDisk(name, data as string | ArrayBuffer | ReadableStream<Uint8Array>, type);
  },

  async 'engine.call'(params: unknown) {
    if (!current.value.engine) throw new BridgeFailure('unavailable', 'this app has no module');
    const p = (params ?? {}) as EngineCallParams;
    if (typeof p.method !== 'string' || !p.method) throw new BridgeFailure('invalid', 'method is required');
    const r = await props.api.pluginUICall(props.app, props.view, {
      method: p.method,
      params: p.params,
      paths: (props.files ?? []).map((f) => f.path),
    });
    return r.result;
  },

  async 'job.submit'(params: unknown) {
    if (!current.value.engine) throw new BridgeFailure('unavailable', 'this app has no module');
    const p = (params ?? {}) as JobSubmitParams;
    if (typeof p.action !== 'string' || !p.action) throw new BridgeFailure('invalid', 'action is required');
    if (!gestureNow()) await askConsent(jobQuestion(await actionLabel(p.action)));
    const r = await props.api.pluginActionRun(props.app, p.action, {
      paths: (props.files ?? []).map((f) => f.path),
      params: p.params && typeof p.params === 'object' ? p.params : {},
    });
    const op = (r as { op?: Record<string, unknown> }).op;
    if (!op) throw new BridgeFailure('failed', 'nothing was queued');
    emit('op', op);
    return { op };
  },

  'state.get'(params: unknown) {
    const key = String((params as { key?: unknown } | null)?.key ?? '');
    return appStateGet(props.app, key) ?? null;
  },

  'state.set'(params: unknown) {
    const p = (params ?? {}) as { key?: unknown; value?: unknown };
    try {
      appStateSet(props.app, String(p.key ?? ''), p.value);
    } catch (e) {
      if (e instanceof AppStateTooLarge) throw new BridgeFailure('too_large', e.message);
      throw e;
    }
    return null;
  },
};

function answerConfirm(yes: boolean) {
  const q = confirmAsk.value;
  confirmAsk.value = null;
  q?.resolve(yes);
}

/* ── the element ────────────────────────────────────────────────────── */

/**
 * Build the frame: the sandbox, the referrer policy and the title first, the
 * address last, and only then into the document. The ORDER is the security
 * (see the file's header) — tests read it back.
 */
function buildFrame(): HTMLIFrameElement {
  const f = document.createElement('iframe');
  f.setAttribute('sandbox', 'allow-scripts');
  f.setAttribute('referrerpolicy', 'no-referrer');
  f.setAttribute('title', props.title || props.app);
  f.className = 'fe-appframe__frame';
  f.dataset.testid = 'app-frame';
  f.setAttribute('src', src.value);
  return f;
}

function mount() {
  unmount();
  if (!host.value) return;
  const f = buildFrame();
  const b = createAppBridge({
    frame: () => f,
    handlers,
    onConnect: () => {
      connected.value = true;
      emit('connected');
    },
    onDisconnect: () => {
      connected.value = false;
    },
  });
  f.addEventListener('load', () => b.frameLoaded());
  frame = f;
  bridge = b;
  host.value.appendChild(f);
}

function unmount() {
  bridge?.destroy();
  bridge = null;
  if (frame) {
    frame.remove();
    frame = null;
  }
  connected.value = false;
  dirty.value = false;
  if (confirmAsk.value) answerConfirm(false);
  if (consentAsk.value) answerConsent(false);
  if (closeAsk.value) answerClose('keep');
}

let offUpdated: (() => void) | null = null;

const HOST_GESTURES = ['pointerdown', 'keydown', 'touchstart'] as const;

onMounted(() => {
  if (activationLive()) hostGestureAt = Date.now();
  for (const ev of HOST_GESTURES) window.addEventListener(ev, onHostGesture, true);
  mount();
  offUpdated = onAppUpdated((app, version) => {
    if (app !== props.app || !version || version === appVersion.value) return;
    newer.value = version;
    notifyUpdated(version);
  });
  // Said once per person: the first opening after the version they last saw.
  const v = appVersion.value;
  if (v) {
    const seen = appSeenVersion(props.app);
    if (seen && seen !== v) updatedNote.value = v;
    if (seen !== v) markAppSeen(props.app, v);
  }
});
onBeforeUnmount(() => {
  for (const ev of HOST_GESTURES) window.removeEventListener(ev, onHostGesture, true);
  offUpdated?.();
  offUpdated = null;
  unmount();
});
watch([src, generation], () => mount());
// A parent that hands a different interface wins over a reload's.
watch(
  () => props.ui,
  () => {
    liveUI.value = null;
  },
);

// The look and the language follow the explorer while the frame is open.
watch(mode, () => bridge?.emit('theme', themeInfo()));
watch(
  () => props.locale,
  (l) => bridge?.emit('locale', { locale: l, dir: localeDir(l) }),
);

/**
 * Ask the interface to save (the host's Save, a draft's "Save to disk"):
 * resolves true once it saved, false when it has nothing to save with or
 * refused. Never throws.
 */
async function requestSave(): Promise<boolean> {
  if (!bridge || !connected.value) return false;
  try {
    await bridge.ask('save');
    return true;
  } catch {
    return false;
  }
}

/**
 * May the frame's surroundings close? True at once when nothing is unsaved;
 * otherwise the person is asked — save and close (the interface saves
 * through the bridge first; a failed save keeps it open), close without
 * saving, or keep editing. Every placement's close goes through here, so the
 * question is the same in a dialog, a tab and the viewer.
 */
async function confirmClose(): Promise<boolean> {
  if (!dirty.value || !connected.value) {
    notifyClosing();
    return true;
  }
  const answer = await new Promise<'save' | 'discard' | 'keep'>((resolve) => {
    closeAsk.value = resolve;
  });
  if (answer === 'keep') return false;
  if (answer === 'save') {
    closeBusy.value = true;
    try {
      if (!(await requestSave())) return false;
    } finally {
      closeBusy.value = false;
    }
  }
  notifyClosing();
  return true;
}

function answerClose(v: 'save' | 'discard' | 'keep') {
  const r = closeAsk.value;
  closeAsk.value = null;
  r?.(v);
}

/** A new version of the app was approved: tell the interface. */
function notifyUpdated(version: string) {
  bridge?.emit('app.updated', { version });
}

/** Tell the interface its surroundings are closing. */
function notifyClosing() {
  bridge?.emit('close.request');
}

/** Replace the frame with a fresh one (a reload: one bridge per element). */
function reload() {
  generation.value++;
}

/**
 * "Reload" on a newer approved version: unsaved changes are asked about
 * first (the same question as closing), then the frame opens the newer
 * interface — its address comes from the server's list, which carries the
 * new bundle's hash.
 */
async function reloadNewer() {
  if (!(await confirmClose())) return;
  const version = newer.value;
  let next: PluginUIRef | null = null;
  try {
    const list = await props.api.pluginActions();
    next =
      list.views.find((v) => v.plugin === props.app && v.id === props.view && v.ui)?.ui ??
      list.actions.find((a) => a.plugin === props.app && a.view === props.view && a.ui)?.ui ??
      null;
  } catch {
    /* no list: reload what is open */
  }
  newer.value = '';
  updatedNote.value = '';
  if (next) {
    liveUI.value = next;
    markAppSeen(props.app, next.version || version);
  }
  reload();
}

function dismissNote() {
  newer.value = '';
  updatedNote.value = '';
}

defineExpose({ requestSave, confirmClose, notifyUpdated, notifyClosing, reload, reloadNewer, connected, dirty });
</script>

<template>
  <div
    ref="host"
    class="fe-appframe"
    :class="`fe-appframe--${placement}`"
    :data-app="app"
    :data-connected="connected ? 'true' : 'false'"
  >
    <div v-if="newer || updatedNote" class="fe-appframe__note" role="status" data-testid="appframe-updated">
      <span class="fe-appframe__note-text">
        {{ newer ? t('appframe.updated_reload', { app: title || app }) : t('appframe.updated', { app: title || app, version: updatedNote }) }}
      </span>
      <button v-if="newer" type="button" class="fe-btn fe-btn--primary" data-testid="appframe-reload" @click="reloadNewer">
        {{ t('appframe.reload') }}
      </button>
      <button type="button" class="fe-btn" data-testid="appframe-dismiss" @click="dismissNote">
        {{ t('appframe.dismiss') }}
      </button>
    </div>
    <div
      v-if="consentAsk"
      class="fe-appframe__note fe-appframe__consent"
      role="alertdialog"
      aria-live="assertive"
      data-testid="appframe-consent"
    >
      <span class="fe-appframe__note-text">{{ consentAsk.text }}</span>
      <button
        type="button"
        class="fe-btn fe-btn--primary"
        data-testid="appframe-consent-allow"
        :disabled="!consentArmed"
        @click="answerConsent(true)"
      >
        {{ t('appframe.consent_allow') }}
      </button>
      <button type="button" class="fe-btn" data-testid="appframe-consent-deny" @click="answerConsent(false)">
        {{ t('appframe.consent_deny') }}
      </button>
    </div>
    <PluginConfirmModal
      v-if="confirmAsk"
      :open="true"
      :locale="locale"
      :theme="theme"
      :title="`${who}: ${confirmAsk.title || t('appframe.confirm_title')}`"
      :message="confirmAsk.text"
      :danger="confirmAsk.danger"
      @close="answerConfirm(false)"
      @confirm="answerConfirm(true)"
    />
    <Modal
      v-if="closeAsk"
      :open="true"
      size="sm"
      :locale="locale"
      :theme="theme"
      :busy="closeBusy"
      :title="t('appframe.unsaved_title')"
      @close="answerClose('keep')"
    >
      <p data-testid="appframe-unsaved">{{ t('appframe.unsaved_text', { app: title || app }) }}</p>
      <template #actions>
        <button type="button" class="fe-btn" data-testid="appframe-keep" :disabled="closeBusy" @click="answerClose('keep')">
          {{ t('appframe.keep_open') }}
        </button>
        <button type="button" class="fe-btn fe-btn--danger" data-testid="appframe-discard" :disabled="closeBusy" @click="answerClose('discard')">
          {{ t('appframe.discard') }}
        </button>
        <button type="button" class="fe-btn fe-btn--primary" data-testid="appframe-save-close" :disabled="closeBusy" @click="answerClose('save')">
          {{ t('appframe.save_close') }}
        </button>
      </template>
    </Modal>
  </div>
</template>

<style>
/* ⚠ Not scoped: the web-component build does not match scoped hashes (see
   DrawioViewer). Every selector is prefixed. */
.fe-appframe {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 0;
  background: var(--fe-bg, #fff);
}
.fe-appframe--inspector {
  height: 320px;
}
.fe-appframe--viewer,
.fe-appframe--page,
.fe-appframe--home {
  min-height: 70vh;
}
.fe-appframe--modal {
  min-height: 60vh;
}
.fe-appframe__note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-block-end: 1px solid var(--fe-border, #e4e4e7);
  background: var(--fe-bg-elev, #f4f4f5);
  color: var(--fe-text, #18181b);
  font-size: 13px;
}
.fe-appframe__note-text {
  flex: 1 1 16rem;
  min-width: 0;
}
.fe-appframe__frame {
  flex: 1;
  width: 100%;
  min-height: 0;
  border: 0;
  background: transparent;
  color-scheme: normal;
}
</style>
