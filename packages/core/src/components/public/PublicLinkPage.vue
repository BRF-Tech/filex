<script setup lang="ts">
/**
 * PublicLinkPage — the whole public page, for whichever link was followed.
 *
 * ⚠⚠ This is the component `web/` mounts for `/s/:token` and `/d/:token`,
 * and it is the ONLY thing the routes do (v3 §1: one public shell, in
 * `packages/core`, so the desktop app and an embed draw the same page). The
 * web app binds addresses; it does not own a public surface.
 *
 * What happens here and nowhere else:
 *   · the visitor's language, which has no account to be read from — the
 *     browser's, or a choice made on this device, over a list that now
 *     includes whatever an app plugin added (`lib/uiLocales`);
 *   · the light/dark answer: the instance's default (branding) unless this
 *     device has chosen otherwise;
 *   · the instance's identity, applied to the shell rather than to `:root`.
 *
 * ⚠ Nothing about the visitor is stored beyond the two display choices
 * above: no session, no token copy, no PIN (the unlock cookie is the
 * server's and HttpOnly).
 */
import { computed, onMounted, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { PublicRequestInfo, PublicShareInfo } from '../../types/Public';
import { useLocale } from '../../composables/useLocale';
import { usePublicBranding } from '../../composables/usePublicBranding';
import { useSystemDark } from '../../composables/useSystemDark';
import { usePublicRequest, usePublicShare } from '../../composables/usePublicLink';
import { hasLocale, normalizeLocaleCode } from '../../lib/uiLocales';
import { localPref, setLocalPref } from '../../lib/prefs';
import { detectLocale } from '../../locales/resolve';
import { labelOf } from '../../lib/pluginLabel';
import { publicLayoutFor } from '../../lib/publicLayout';
import PublicShell from './PublicShell.vue';
import PublicShareBody from './PublicShareBody.vue';
import PublicRequestBody from './PublicRequestBody.vue';
import type { DropRefusal } from '../../lib/dropLimits';

const props = defineProps<{
/**
   * `share` → `/s/<token>`; `request` → `/d/<token>`.
   *
   * ⚠ `/p/<token>` is RETIRED (v3 §1: an app's public page is a share now)
   * and the server 301s it to the share, so there is no third kind here and
   * no second code path for the transition.
   */
  kind: 'share' | 'request';
  /** From the route. Empty (a URL that is not a token) draws "not available". */
  token?: string;
  /** API origin; empty = same origin. */
  base?: string;
  /** Forced by the host; otherwise the device's choice, then the browser's. */
  locale?: LocaleCode | string;
  /** Forced by the host; otherwise the instance's default, then the device's. */
  theme?: ThemeMode;
}>();

/* ── the visitor's language ───────────────────────────────────────────── */

const chosen = ref<string>('');
const locale = computed<string>(() => {
  if (props.locale) return String(props.locale);
  if (chosen.value) return chosen.value;
  const stored = normalizeLocaleCode(localPref('locale'));
  if (stored && hasLocale(stored)) return stored;
  return detectLocale();
});

function chooseLocale(v: string): void {
  const code = normalizeLocaleCode(v);
  if (!code || !hasLocale(code)) return;
  chosen.value = code;
  // ⚠ localStorage and nothing else. There is no account behind a share
  // link, so this is the only place a stranger's choice can live — and it is
  // a display preference, not a fact about them.
  setLocalPref('locale', code);
}

const { t, formatSize } = useLocale(() => locale.value);

/* ── the instance's identity ──────────────────────────────────────────── */

const brand = usePublicBranding({ base: props.base });

/* ⚠ The system's answer as a LIVE ref (#57): read inside the computed below
   it was read once, and an open page kept the mode it was opened in. */
const systemDark = useSystemDark();

/**
 * Light or dark.
 *
 * Order: what the host forced, then what this device chose, then the
 * instance's own default, then the system. ⚠ The instance's default is
 * BELOW the device's choice deliberately — an operator picks the default
 * their visitors start from, not the one they are held to.
 */
const resolvedTheme = computed<'light' | 'dark'>(() => {
  const want = props.theme && props.theme !== 'auto' ? props.theme : localPref('theme') || brand.themeDefault.value;
  if (want === 'light' || want === 'dark') return want;
  return systemDark.value ? 'dark' : 'light';
});

/* ── the link itself ──────────────────────────────────────────────────── */

const opts = {
  base: props.base,
  locale: () => locale.value,
  errorText: () => t('plugin.view.error'),
};

const share = props.kind === 'share' ? usePublicShare(props.token ?? '', opts) : null;
/* ⚠ A file request has no app behind it: its failure is "could not be sent",
   never the app runtime's "The app returned an error" (QA, 2026-09-21). */
const request =
  props.kind === 'request'
    ? usePublicRequest(props.token ?? '', { ...opts, errorText: () => t('public.upload_failed') })
    : null;

/** The words for a file the drop page will not send (lib/dropLimits). */
function refusedText(r: DropRefusal): string {
  if (r.code === 'ext') return t('public.refused_ext', { name: r.name });
  // The same number the limits line states, in the same units.
  if (r.code === 'too_large') return t('public.refused_too_large', { size: formatSize(r.mb * 1_000_000) });
  if (r.count <= 0) return t('public.request_full');
  return t('public.refused_too_many', { count: r.count });
}
const link = (share ?? request)!;

const shareInfo = computed(() => (share?.info.value ?? null) as PublicShareInfo | null);
const requestInfo = computed(() => (request?.info.value ?? null) as PublicRequestInfo | null);

/**
 * The heading.
 *
 * An app link is titled by the app (its manifest page, then the surface it
 * answered with); a file or folder link by the thing behind it; a file
 * request by the folder it drops into. ⚠ The app's title is read from the
 * SURFACE as well, because a wizard renames the screen as it walks ("Who
 * signs" → "Place the boxes") and a heading frozen at the manifest's page
 * label would contradict the step under it.
 */
const title = computed(() => {
  const app = shareInfo.value?.app;
  const fromSurface = labelOf(share?.conv.current.value?.title, locale.value);
  if (app) return fromSurface || labelOf(app.title, locale.value) || '';
  return shareInfo.value?.node?.name || requestInfo.value?.folder || '';
});

const subject = computed(() => {
  const s = link.info.value?.subject ?? '';
  // Not twice: a file share's subject defaults to the file's own name on the
  // server, and printing it again under the heading is noise.
  return s && s !== title.value ? s : '';
});

/**
 * How wide the card is, and which badge it opens with — both from the kind
 * the server answered with, through the one helper the admin preview uses
 * too (`lib/publicLayout`). ⚠ A file request is titled by its folder, so its
 * badge is a folder rather than a document.
 */
const layout = computed(() =>
  publicLayoutFor(shareInfo.value?.kind ?? (request ? 'drop' : ''), {
    list: (shareInfo.value?.entries?.length ?? 0) > 0,
  }),
);
const badge = computed<'file' | 'folder' | ''>(() => {
  if (request) return '';
  const kind = shareInfo.value?.kind;
  if (kind === 'folder') return 'folder';
  if (kind === 'app') return '';
  return 'file';
});

/** Walking into a folder is a reload of the same link at another path. */
function navigate(path: string): void {
  void link.load(path ? `?path=${encodeURIComponent(path)}` : '');
}

watch(title, (v) => {
  if (v && typeof document !== 'undefined') document.title = v;
});

/**
 * THE DOCUMENT ELEMENT follows the language — both halves of it.
 *
 * ⚠⚠ The shell puts `dir` on its own wrapper, which turns the page around
 * visually, and that is what every eye test sees. `<html>` kept the values
 * the SERVER rendered: a stranger who pressed العربية on a share page was
 * reading Arabic inside `<html lang="en" dir="ltr">` (v0.43.0). Nothing on
 * screen says so, and everything that does not look at pixels is misled — a
 * screen reader announces the language it is told, hyphenation and quotation
 * marks follow `lang`, and `dir` on the root is what a printed page and any
 * unwrapped portal inherit.
 *
 * One watcher, `immediate`, so the first paint and every later change agree;
 * `chooseLocale` does not touch the document at all any more, because a
 * language can also arrive from the host (`props.locale`) or from what this
 * device chose last time.
 *
 * ⚠ `lang` only. `dir` is DERIVED from it by the one rule that owns
 * direction (`lib/direction` → `syncDocumentDir`, installed by the host that
 * owns the page), which also re-applies when the language LIST arrives — a
 * pack's language can be on `<html lang>` before the answer saying it is
 * written right to left has landed. Setting `dir` here as well produced
 * exactly that race: the page wrote `ltr` from a list it did not have yet.
 */
watch(
  locale,
  (code) => {
    if (typeof document !== 'undefined') document.documentElement.lang = code;
  },
  { immediate: true },
);

// A language chosen mid-visit has to reach the PLUGIN too: its strings come
// from the server, which reads `Accept-Language` on every call, so the screen
// has to be asked for again in the new language.
//
// ⚠⚠ Asked AGAIN, not started again. Reloading the link re-opens the surface
// at its first screen: somebody three steps into signing, with their title
// typed and their signature drawn, lost all of it by pressing "English" —
// and the language picker sits right next to the wizard. A `change` carries
// the state and the values that are already on screen, so the same step
// comes back in the other language with the answers still in it.
watch(locale, () => {
  if (link.status.value === 'ready' && shareInfo.value?.kind === 'app') {
    if (link.conv.current.value) void link.conv.post('change');
    else void link.load();
  }
});

onMounted(() => {
  // ⚠ One fetch, not two: the branding answer CARRIES the language list
  // (`locales`), and `usePublicBranding` folds it into the registry. A
  // separate call would be a second unauthenticated round trip on every
  // public page for half of what this one already says.
  void brand.load();
  if (!props.token) {
    link.status.value = 'not_found';
    return;
  }
  void link.load();
});
</script>

<template>
  <PublicShell
    :status="link.status.value"
    :locale="locale"
    :theme="resolvedTheme"
    :brand-name="brand.name.value"
    :logo-url="brand.logo.value"
    :accent-style="brand.accentStyle.value"
    :footer-text="brand.footerText.value"
    :hide-powered-by="brand.hidePoweredBy.value"
    :layout="layout"
    :badge="badge"
    :title="title"
    :subject="subject"
    :expires-at="link.info.value?.expires_at ?? null"
    :visits-left="shareInfo?.visits_left ?? requestInfo?.uploads_left ?? null"
    :pin-busy="link.pinBusy.value"
    :pin-failure="link.pinFailure.value"
    :lock-message="link.lockMessage.value"
    :failure="link.failure.value"
    @pin="(p: string) => link.submitPin(p)"
    @retry="() => link.load()"
    @update:locale="chooseLocale"
  >
    <PublicShareBody
      v-if="share"
      :link="share"
      :locale="locale"
      :theme="resolvedTheme"
      @navigate="navigate"
    />
    <PublicRequestBody
      v-else-if="request"
      :info="requestInfo"
      :uploads="request.uploads.value"
      :locale="locale"
      hide-folder
      @files="(f: File[], name: string) => request!.upload(f, { name, refusedText })"
    />
  </PublicShell>
</template>
