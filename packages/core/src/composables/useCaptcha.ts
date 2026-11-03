/**
 * useCaptcha - one protected form's bot check: the provider's widget, the
 * local fallback when the server cannot reach the provider, and the one rule
 * every form follows when it sends (`protect`).
 *
 * ⚠⚠ ONE implementation for every form and every surface (the sign-in page,
 * the demo's one-click sign-in, the PIN a link asks for, the drop box of a
 * file request), drawn by ONE component (`components/CaptchaWidget.vue`). The
 * desktop app, the web app and every embed get the same check, the same
 * words and the same fallback.
 *
 * What `protect(send)` does, in order:
 *
 *   1. waits, briefly, for a token the widget is still producing - a person
 *      who types a PIN and presses Enter at once should not be refused for
 *      being quick;
 *   2. sends with the fields (`captcha_token` or `captcha_local`);
 *   3. starts the widget over: a token is good for ONE request, whatever the
 *      answer was (a wrong PIN, a second factor asked for, a success);
 *   4. on `409 fallback`: solves the challenge in a worker and sends the same
 *      request ONCE more, with `captcha_local`;
 *   5. on `422`/`503` from the check: shows the server's own sentence under
 *      the widget and does NOT offer the fallback - it is for an outage, not
 *      for a token that failed.
 *
 * ⚠ The local challenge is also asked for up front when the provider's script
 * cannot load here (`POST /api/captcha/local/challenge`): the server hands one
 * out only if IT cannot reach the provider either. If it can, the person is
 * told the widget did not load (usually a content blocker) - a blocker must
 * not be a way around the check.
 */
import { computed, ref, watch, type ComputedRef, type Ref } from 'vue';
import {
  captchaAnswerOf,
  captchaFormOn,
  fetchCaptchaConfig,
  loadCaptchaScript,
  providerGlobal,
  requestCaptchaChallenge,
  solveCaptchaChallenge,
  turnstileLanguage,
  type CaptchaChallenge,
  type CaptchaFields,
  type CaptchaForm,
  type CaptchaPublicConfig,
  type CaptchaReply,
  type CaptchaWorkerLike,
  type RecaptchaApi,
  type TurnstileApi,
} from '../lib/captcha';
import { useLocale } from './useLocale';

/**
 * Where the widget stands.
 *
 *   `off`      the form is not protected (or the server says nothing)
 *   `loading`  the config or the provider's script is on its way
 *   `ready`    the widget is drawn, its token not there yet
 *   `solved`   a token (or a local solution) is waiting to be sent
 *   `solving`  the local fallback is running
 *   `error`    neither the widget nor the fallback can help; `message` says why
 */
export type CaptchaState = 'off' | 'loading' | 'ready' | 'solved' | 'solving' | 'error';

/** The server's answer to `/api/captcha/config`, shared by every form of one page. */
export interface CaptchaConfigSource {
  config: Ref<CaptchaPublicConfig | null>;
  /** Asked once; later calls get the same answer. */
  load: () => Promise<CaptchaPublicConfig | null>;
}

export function useCaptchaConfig(opts: {
  base?: string;
  fetchImpl?: typeof fetch;
  /**
   * How the host asks (the admin app's own API client, which its tests mock).
   * Absent: `GET {base}/api/captcha/config` with fetch.
   */
  load?: () => Promise<CaptchaPublicConfig | null>;
}): CaptchaConfigSource {
  const config = ref<CaptchaPublicConfig | null>(null);
  let pending: Promise<CaptchaPublicConfig | null> | null = null;
  return {
    config,
    load: () => {
      if (!pending) {
        const ask = opts.load ? opts.load().catch(() => null) : fetchCaptchaConfig(opts.base, opts.fetchImpl);
        pending = ask.then((c) => {
          config.value = c;
          return c;
        });
      }
      return pending;
    },
  };
}

export interface CaptchaOptions {
  /** The server root (no `/api`); empty = same origin. */
  base?: string;
  /** Which form this is. A function when one button can be either (the demo page). */
  form: CaptchaForm | (() => CaptchaForm);
  /** The page's language: the widget's and the fallback's words. */
  locale: () => string;
  /** The page's mode, `light` or `dark`; the widget follows it. */
  theme?: () => 'light' | 'dark' | 'auto';
  /** One config for the whole page; the composable asks on its own otherwise. */
  config?: CaptchaConfigSource;
  fetchImpl?: typeof fetch;
  /** How long `protect` waits for a token still on its way (ms). */
  settleMs?: number;
  /** A test's worker. */
  createWorker?: () => CaptchaWorkerLike;
}

export interface CaptchaStore {
  config: Ref<CaptchaPublicConfig | null>;
  /** The form is protected here: the widget is drawn. */
  active: ComputedRef<boolean>;
  form: ComputedRef<CaptchaForm>;
  provider: ComputedRef<CaptchaPublicConfig['provider']>;
  state: Ref<CaptchaState>;
  /** One sentence under the widget ('' = none). */
  message: Ref<string>;
  messageKind: Ref<'' | 'info' | 'ok' | 'error'>;
  /** 0..100 while the fallback runs. */
  progress: Ref<number>;
  /** How long the last local solution took (ms), for `data-captcha-solve-ms`. */
  solveMs: Ref<number | null>;
  /** The widget is not drawn (the fallback took its place). */
  widgetHidden: Ref<boolean>;
  /** Reads the config (once per page). */
  load: () => Promise<void>;
  /** Draws the widget into the element (CaptchaWidget calls this). */
  attach: (el: HTMLElement) => Promise<void>;
  /** Takes the widget away again. */
  detach: () => void;
  /** What the next request carries. */
  fields: () => CaptchaFields;
  /** Starts over: no token, no solution, the widget anew. */
  reset: () => void;
  /** Sends with the check; see the file's header. */
  protect: <T>(send: (fields: CaptchaFields) => Promise<T>, replyOf: (outcome: CaptchaOutcome<T>) => CaptchaReply | null) => Promise<T>;
}

/** What one send came back with, for `replyOf`. */
export type CaptchaOutcome<T> = { ok: true; value: T } | { ok: false; error: unknown };

/** Do not ask the server for a second local challenge within this long of it saying "the provider is fine". */
const PROVIDER_CONFIRMED_MS = 30_000;

export function useCaptcha(opts: CaptchaOptions): CaptchaStore {
  const source = opts.config ?? useCaptchaConfig({ base: opts.base, fetchImpl: opts.fetchImpl });
  const { t } = useLocale(() => opts.locale());

  const form = computed<CaptchaForm>(() => (typeof opts.form === 'function' ? opts.form() : opts.form));
  const config = source.config;
  const active = computed(() => captchaFormOn(config.value, form.value));
  const provider = computed(() => config.value?.provider ?? 'none');

  const state = ref<CaptchaState>('loading');
  const message = ref('');
  const messageKind = ref<'' | 'info' | 'ok' | 'error'>('');
  const progress = ref(0);
  const solveMs = ref<number | null>(null);
  const widgetHidden = ref(false);

  let token = '';
  /**
   * The local solution waiting to be sent. ⚠ Its expiry is the SERVER's to
   * judge: a browser whose clock runs ahead would otherwise throw away every
   * solution it made and never get through (422 `expired` restarts it).
   */
  let local = '';
  /** `widget` - the provider's own; `local` - its script cannot load here, the server's challenge instead. */
  let mode: 'widget' | 'local' = 'widget';
  let host: HTMLElement | null = null;
  let widgetId: string | number | null = null;
  let providerConfirmedAt = 0;
  let solving: Promise<boolean> | null = null;
  let abort: AbortController | null = null;
  let loaded = false;

  function say(text: string, kind: '' | 'info' | 'ok' | 'error'): void {
    message.value = text;
    messageKind.value = text ? kind : '';
  }

  async function load(): Promise<void> {
    await source.load();
    loaded = true;
    if (!active.value) state.value = 'off';
    else if (state.value === 'off') state.value = 'loading';
  }

  watch(active, (on) => {
    if (!loaded) return;
    if (!on) {
      state.value = 'off';
      say('', '');
    }
  });

  /**
   * The widget's token arrived (or expired). ⚠ A local solution is kept: it
   * was made for an outage the server is still in, and a widget that renewed
   * itself in the meantime does not end that outage.
   */
  function setToken(v: string): void {
    token = v;
    if (solving || local) return;
    if (v) {
      // ⚠ A refusal's sentence stays: the widget renews itself a second
      // after a 422, and the person has not read it yet. The next send
      // clears it.
      state.value = 'solved';
    } else if (state.value === 'solved') {
      state.value = widgetId !== null ? 'ready' : 'loading';
    }
  }

  function renderWidget(el: HTMLElement): boolean {
    const p = provider.value;
    const g = providerGlobal(p);
    const key = config.value?.site_key ?? '';
    if (!g || !key) return false;
    el.innerHTML = '';
    const theme = opts.theme?.() ?? 'auto';
    if (p === 'turnstile') {
      widgetId =
        (g as TurnstileApi).render(el, {
          sitekey: key,
          theme,
          language: turnstileLanguage(opts.locale()),
          size: 'flexible',
          callback: (v: string) => setToken(v),
          'expired-callback': () => setToken(''),
          'timeout-callback': () => setToken(''),
          'error-callback': () => {
            void fallbackFromServer();
            return true;
          },
        }) ?? null;
    } else {
      widgetId = (g as RecaptchaApi).render(el, {
        sitekey: key,
        theme: theme === 'dark' ? 'dark' : 'light',
        callback: (v: string) => setToken(v),
        'expired-callback': () => setToken(''),
        'error-callback': () => {
          void fallbackFromServer();
        },
      });
    }
    state.value = 'ready';
    return true;
  }

  async function attach(el: HTMLElement): Promise<void> {
    host = el;
    if (!loaded) await load();
    if (!active.value || host !== el) return;
    const c = config.value!;
    if (c.misconfigured || !c.site_key || !c.script) {
      // The server knows it cannot verify a token: no widget to wait for.
      mode = 'local';
      await fallbackFromServer();
      return;
    }
    state.value = 'loading';
    try {
      await loadCaptchaScript(c.provider, c.script, { lang: opts.locale() });
    } catch {
      if (host !== el) return;
      mode = 'local';
      await fallbackFromServer();
      return;
    }
    if (host !== el) return;
    mode = 'widget';
    widgetHidden.value = false;
    if (!renderWidget(el)) {
      mode = 'local';
      await fallbackFromServer();
    }
  }

  function removeWidget(): void {
    const g = providerGlobal(provider.value);
    if (widgetId !== null && g && provider.value === 'turnstile') {
      try {
        (g as TurnstileApi).remove(String(widgetId));
      } catch {
        /* already gone */
      }
    }
    widgetId = null;
  }

  function detach(): void {
    abort?.abort();
    removeWidget();
    host = null;
    token = '';
    local = '';
  }

  /** The provider's widget anew (a spent token, a failed check). */
  function restartWidget(): void {
    token = '';
    const g = providerGlobal(provider.value);
    if (widgetId === null || !g) return;
    try {
      if (provider.value === 'turnstile') (g as TurnstileApi).reset(String(widgetId));
      else (g as RecaptchaApi).reset(Number(widgetId));
      state.value = 'ready';
    } catch {
      /* the widget went away */
    }
  }

  function reset(): void {
    local = '';
    widgetHidden.value = false;
    progress.value = 0;
    if (mode === 'widget') restartWidget();
    else {
      token = '';
      if (state.value === 'solved') state.value = 'loading';
    }
  }

  function fields(): CaptchaFields {
    if (local) return { captcha_local: local };
    if (token) return { captcha_token: token };
    return {};
  }

  /** Runs the local fallback on a challenge; true when a solution is waiting. */
  function solve(challenge: CaptchaChallenge): Promise<boolean> {
    if (solving) return solving;
    abort = new AbortController();
    widgetHidden.value = true;
    progress.value = 0;
    state.value = 'solving';
    say(t('captcha.fallback_intro'), 'info');
    solving = solveCaptchaChallenge(challenge, {
      signal: abort.signal,
      createWorker: opts.createWorker,
      onProgress: (p) => {
        progress.value = p;
      },
    })
      .then((s) => {
        local = s.payload;
        token = '';
        solveMs.value = s.ms;
        state.value = 'solved';
        say(t('captcha.fallback_done'), 'ok');
        return true;
      })
      .catch((e: Error) => {
        if (e?.message !== 'aborted') {
          state.value = 'error';
          say(t('captcha.fallback_failed'), 'error');
        }
        return false;
      })
      .finally(() => {
        solving = null;
      });
    return solving;
  }

  /**
   * The widget could not help (its script did not load, it reported an
   * error): ask the server whether it is an outage. Only then is there a
   * challenge to solve.
   */
  async function fallbackFromServer(): Promise<boolean> {
    if (Date.now() - providerConfirmedAt < PROVIDER_CONFIRMED_MS) {
      state.value = 'error';
      say(t('captcha.widget_failed'), 'error');
      return false;
    }
    const r = await requestCaptchaChallenge(opts.base, { fetchImpl: opts.fetchImpl, locale: opts.locale() });
    if (r.kind === 'challenge') {
      mode = 'local';
      return solve(r.challenge);
    }
    if (r.kind === 'disabled') {
      state.value = 'off';
      say('', '');
      return false;
    }
    if (r.kind === 'provider') providerConfirmedAt = Date.now();
    state.value = 'error';
    say(r.kind === 'provider' ? t('captcha.widget_failed') : r.message || t('captcha.unavailable'), 'error');
    return false;
  }

  /** Waits for a token still on its way, at most `settleMs`. */
  function settle(): Promise<void> {
    if (state.value !== 'loading' && state.value !== 'ready' && state.value !== 'solving') return Promise.resolve();
    if (solving) return solving.then(() => undefined);
    return new Promise<void>((resolve) => {
      const timer = setTimeout(done, opts.settleMs ?? 8000);
      const stop = watch(state, (s) => {
        if (s !== 'loading' && s !== 'ready') done();
      });
      function done(): void {
        clearTimeout(timer);
        stop();
        resolve();
      }
    });
  }

  async function attempt<T>(send: (f: CaptchaFields) => Promise<T>, f: CaptchaFields): Promise<CaptchaOutcome<T>> {
    try {
      return { ok: true, value: await send(f) };
    } catch (error) {
      return { ok: false, error };
    }
  }

  /** A sent token or solution is spent, whatever the answer was. */
  function spend(f: CaptchaFields): void {
    if (f.captcha_local) local = '';
    if (f.captcha_token) restartWidget();
    if (state.value === 'solved' || state.value === 'ready') {
      state.value = token ? 'solved' : mode === 'widget' && widgetId !== null ? 'ready' : 'loading';
    }
  }

  function refused(reply: CaptchaReply): void {
    const said = typeof reply.body?.message === 'string' ? (reply.body.message as string) : '';
    local = '';
    widgetHidden.value = false;
    progress.value = 0;
    if (mode === 'widget') restartWidget();
    else if (state.value !== 'error') state.value = 'loading';
    // ⚠ The widget did not load here and the server can reach the provider:
    // "verification required" would hide the one thing the person can do
    // about it (turn the blocker off), so that sentence stays.
    if (mode === 'local' && Date.now() - providerConfirmedAt < PROVIDER_CONFIRMED_MS) {
      state.value = 'error';
      say(t('captcha.widget_failed'), 'error');
      return;
    }
    say(said || t(captchaAnswerOf(reply) === 'unavailable' ? 'captcha.unavailable' : 'captcha.invalid'), 'error');
  }

  async function protect<T>(
    send: (f: CaptchaFields) => Promise<T>,
    replyOf: (outcome: CaptchaOutcome<T>) => CaptchaReply | null,
  ): Promise<T> {
    if (!loaded) await load();
    // The last refusal's sentence was about the last send.
    if (messageKind.value === 'error' && state.value !== 'error') say('', '');
    if (active.value) {
      // The widget's script could not load here and the solution was spent:
      // a new challenge first, while the person waits on the button.
      if (mode === 'local' && !local && state.value !== 'error' && state.value !== 'solving') await fallbackFromServer();
      await settle();
    }
    let f = fields();
    let out = await attempt(send, f);
    spend(f);
    let reply = replyOf(out);
    if (captchaAnswerOf(reply) === 'fallback') {
      const solved = await solve(reply!.body!.challenge as CaptchaChallenge);
      if (solved) {
        f = fields();
        out = await attempt(send, f);
        spend(f);
        reply = replyOf(out);
      }
    }
    const kind = captchaAnswerOf(reply);
    if (kind === 'refused' || kind === 'unavailable') refused(reply!);
    else {
      // "Verification complete" was about the request that just went; a
      // wrong PIN after it must not leave the next try looking checked.
      if (messageKind.value === 'ok' || messageKind.value === 'info') say('', '');
      if (mode === 'widget') widgetHidden.value = false;
    }
    if (out.ok) return out.value;
    throw out.error;
  }

  return {
    config,
    active,
    form,
    provider,
    state,
    message,
    messageKind,
    progress,
    solveMs,
    widgetHidden,
    load,
    attach,
    detach,
    fields,
    reset,
    protect,
  };
}
