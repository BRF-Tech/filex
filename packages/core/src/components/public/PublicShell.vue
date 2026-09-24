<script setup lang="ts">
/**
 * PublicShell — the frame every public link is drawn in.
 *
 * ⚠⚠ v3 §1, and the whole point of the round: a share, a file request and an
 * app plugin's page are ONE page with three bodies, not three pages. What
 * the shell owns is exactly what they used to each get wrong on their own —
 *
 *   · the instance's name, logo and colour (§1.2: whitelabel means
 *     whitelabel — a signature request from a renamed instance does not say
 *     "filex");
 *   · the PIN gate, once, with one wording and one lockout behaviour;
 *   · expiry and the visit counter, said out loud rather than discovered;
 *   · "this link is not available", identical for expired, used up and
 *     withdrawn, because a visitor must not be told which;
 *   · the language picker, including a language an app plugin added.
 *
 * The BODY is a slot. Nothing in here knows whether it is showing a document,
 * a folder, a drop box or a plugin's screen, and that is what keeps the
 * three from drifting apart again.
 *
 * ⚠⚠ Unifying them had to level them UP and did not (owner, 2026-09-23:
 * "böyle güzel bir ekranımız vardı share katmanında … iki sayfa aynı olsun
 * dediğim için ikisini de kötü hale çevirmişsin"). The look is the one the
 * Go-rendered gate had until v0.42.2 and it is the shell's, not a page's:
 * the mark centred ABOVE the card, a round tinted badge, a heading, a quiet
 * line, and — on a gate — a full-width field and a full-width accent button
 * on a card that is 400px and centred in the viewport. A body that wants
 * room asks for it through `layout`, which is the only thing that varies
 * (`gate` 400 · `form` 520 · `wide` 880 — the old stylesheet's three cards).
 *
 * ⚠ It lives in `packages/core`, not in the web app, so the desktop app and
 * an embed draw the same public page. `web/` only binds the routes.
 *
 * ⚠ The accent is applied to THIS element, never to `:root` — the shell can
 * be mounted inside another page, and a link must not repaint its host.
 */
import { computed } from 'vue';
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { PublicStatus } from '../../composables/usePublicLink';
import type { PublicLayout } from '../../lib/publicLayout';
import { useLocale } from '../../composables/useLocale';
import { DEFAULT_BRAND_NAME } from '../../composables/usePublicBranding';
import LogoMark from '../LogoMark.vue';
import PublicPinGate from './PublicPinGate.vue';
import PublicLanguagePicker from './PublicLanguagePicker.vue';

const props = defineProps<{
  status: PublicStatus;
  locale: LocaleCode | string;
  /** `light` | `dark` — already resolved; `auto` is the caller's to answer. */
  theme?: 'light' | 'dark' | ThemeMode;
  /** The instance's name (branding, or the product's own). */
  brandName: string;
  logoUrl?: string;
  /** `--fe-*` overrides from the instance's accent colour. */
  accentStyle?: Record<string, string>;
  footerText?: string;
  hidePoweredBy?: boolean;
  /**
   * How much room the body needs (`lib/publicLayout`). Every state that is
   * NOT `ready` overrides it to `gate`: a PIN box, "this link is not
   * available" and "thank you" are the same short, centred card whatever
   * was going to be behind them.
   */
  layout?: PublicLayout;
  /**
   * The round badge a READY body opens with — `file` or `folder`. Every
   * other badge is a state of the shell's own (a padlock, a tick, a warning
   * sign, a spinner) and is not the caller's to pick.
   */
  badge?: 'file' | 'folder' | '';
  /** The heading: a file name, a folder, a plugin page's title. */
  title?: string;
  /** The line under it — a share message, a signature request's subject. */
  subject?: string;
  /** RFC 3339, or null for "does not expire". */
  expiresAt?: string | null;
  /** Visits left before the link stops answering; null = uncounted. */
  visitsLeft?: number | null;
  pinBusy?: boolean;
  pinFailure?: '' | 'wrong' | 'locked';
  lockMessage?: string;
  /** A failure outside the conversation, in the server's words. */
  failure?: string;
}>();

const emit = defineEmits<{
  (e: 'pin', pin: string): void;
  (e: 'retry'): void;
  (e: 'update:locale', v: string): void;
}>();

// ⚠ RTL: `dir` on the page's own root too — the shell is the whole page when
// the admin app mounts it, but an embedder may mount it in a page of its own.
const { t, formatDate, dir } = useLocale(() => props.locale);

/** The body is drawn for these; everything else is a state of the shell's own. */
const showBody = computed(() => props.status === 'ready');

/** A state of the shell's own is always the narrow, centred card. */
const layout = computed<PublicLayout>(() => (showBody.value ? (props.layout ?? 'form') : 'gate'));

const themeClass = computed(() =>
  props.theme === 'dark' ? 'fe--theme-dark' : props.theme === 'light' ? 'fe--theme-light' : '',
);

const expiry = computed(() => {
  if (!props.expiresAt) return '';
  const ms = Date.parse(props.expiresAt);
  if (!Number.isFinite(ms)) return '';
  return t('public.expires', { when: formatDate(ms, { time: true }) });
});

const visits = computed(() => {
  const n = props.visitsLeft;
  if (n === null || n === undefined || !Number.isFinite(n)) return '';
  return t('public.visits_left', { n });
});

/**
 * Which badge the card opens with. ONE decision, so a state and a body can
 * never both draw one, and a body cannot invent a shape of its own.
 */
const badgeKind = computed(() => {
  if (props.status === 'pin') return 'lock';
  if (props.status === 'accepted' || props.status === 'done') return 'check';
  if (props.status === 'loading') return 'loading';
  if (!showBody.value) return 'alert';
  return props.badge ?? '';
});
const badgeTone = computed(() =>
  badgeKind.value === 'check'
    ? 'fe-ppage__badge--ok'
    : badgeKind.value === 'alert'
      ? 'fe-ppage__badge--err'
      : '',
);

/**
 * Whether the PRODUCT's own mark may be drawn beside the attribution line.
 *
 * ⚠ Only when the instance has neither a logo nor a name of its own — that
 * is, when the line under it reads "Served by filex" and the folder-and-tick
 * is what it is talking about. An operator who has renamed the instance gets
 * their own logo, or their name and nothing else: putting our mark next to
 * "Served by Acme Files" is the whitelabel leak v3 §1.2 exists to stop, and
 * the shell's own test asserts a renamed page never says "filex".
 *
 * ⚠ The mark is `@brftech/filex-core`'s LogoMark — the ONE copy in the
 * product (dup-scan: `brand-mark`). It was hand-typed here for about an hour
 * on 2026-09-23 and the duplicate gate caught it, which is exactly the
 * failure that once shipped the pre-rebrand indigo on the page strangers see.
 */
const ownMark = computed(() => !props.logoUrl && props.brandName === DEFAULT_BRAND_NAME);

function pluginNote(plugin: string): string {
  return t('public.language_from', { app: plugin });
}
</script>

<template>
  <div
    class="fe fe-ppage"
    :dir="dir"
    :class="[themeClass, `fe-ppage--${layout}`]"
    :style="accentStyle"
    data-testid="public-page"
    :data-state="status"
    :data-layout="layout"
  >
    <!-- ⚠ ABOVE the card and centred on it. Inside the card it read as a
         caption on somebody else's document; above it, it says whose page
         this is — which is the first thing a stranger needs.
         ⚠ The mark is the INSTANCE's logo, or — only while the instance is
         still called by the product's name — the product's own (`ownMark`).
         It is DRAWN, never re-typed: LogoMark is the one home of that path
         data (dup-scan `brand-mark`), and a second hand copy is how the page
         strangers see shipped the pre-rebrand indigo for a whole wave. -->
    <header class="fe-ppage__brand" data-testid="public-brand">
      <img v-if="logoUrl" class="fe-ppage__logo" :src="logoUrl" :alt="brandName" />
      <LogoMark v-else-if="ownMark" class="fe-ppage__logo" />
      <span class="fe-ppage__brandname">{{ brandName }}</span>
    </header>

    <main class="fe-ppage__card">
      <!-- The badge the card opens with. The shell draws the one its state
           calls for; a READY body names `file` or `folder` and gets the same
           circle in the same place, so a document, a folder and a locked
           link are one family rather than three drawings. -->
      <div
        v-if="badgeKind"
        class="fe-ppage__badge"
        :class="badgeTone"
        data-testid="public-page-badge"
        :data-badge="badgeKind"
        aria-hidden="true"
      >
        <span v-if="badgeKind === 'loading'" class="fe-ppage__spinner"></span>
        <svg
          v-else
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          :stroke-width="badgeKind === 'check' ? 2 : 1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <template v-if="badgeKind === 'lock'">
            <rect x="4.5" y="10.5" width="15" height="9.5" rx="2" />
            <path d="M8 10.5V7a4 4 0 0 1 8 0v3.5" />
            <path d="M12 14.5v2" />
          </template>
          <template v-else-if="badgeKind === 'check'">
            <path d="M4.5 12.5l5 5 10-11" />
          </template>
          <template v-else-if="badgeKind === 'alert'">
            <path d="M10.3 4.2 2.6 17.9a2 2 0 0 0 1.7 3h15.4a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0z" />
            <path d="M12 9.5v4.5" />
            <path d="M12 17.4h.01" />
          </template>
          <template v-else-if="badgeKind === 'folder'">
            <path d="M3.5 7a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7z" />
            <path d="M12 10.5V16" />
            <path d="M9.5 13.5 12 16l2.5-2.5" />
          </template>
          <template v-else>
            <path d="M7 3.5h7l4 4V20a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 6 20V5A1.5 1.5 0 0 1 7.5 3.5z" />
            <path d="M13.5 3.5V8H18" />
            <path d="M12 11.5V17" />
            <path d="M9.5 14.5 12 17l2.5-2.5" />
          </template>
        </svg>
      </div>

      <!-- ⚠⚠ `&& showBody`, on purpose and confirmed 2026-09-23: the heading
           is drawn only once the link has OPENED. A locked gate used to print
           the file's name above the PIN box, which tells somebody who cannot
           get in what is behind it — and a stranger typing at a token they
           were not given learns the name of a real document. It is the same
           reasoning as the one sentence for expired / used up / withdrawn
           below: the page says nothing about what it is guarding until it is
           satisfied the person may see it. Do not "restore" the heading here;
           if a gate ever needs to name its subject, that is a decision about
           what the SERVER sends before an unlock, not a template change. -->
      <header v-if="(title || subject) && showBody" class="fe-ppage__head">
        <h1 v-if="title" class="fe-ppage__title" data-testid="public-page-title"><bdi>{{ title }}</bdi></h1>
        <p v-if="subject" class="fe-ppage__subject" data-testid="public-page-subject">{{ subject }}</p>
      </header>

      <!-- ⚠ Said out loud, not discovered. A link that stops working
           tomorrow, or after one more visit, is a fact the person holding it
           needs BEFORE they put it aside for the weekend. -->
      <p v-if="(expiry || visits) && showBody" class="fe-ppage__meta" data-testid="public-page-meta">
        <span v-if="expiry">{{ expiry }}</span>
        <span v-if="expiry && visits" aria-hidden="true">·</span>
        <span v-if="visits">{{ visits }}</span>
      </p>

      <p
        v-if="status === 'loading'"
        class="fe-surface__text fe-surface__text--muted"
        data-testid="public-page-loading"
      >
        {{ t('plugin.page.loading') }}
      </p>

      <PublicPinGate
        v-else-if="status === 'pin'"
        :locale="locale"
        :busy="pinBusy"
        :failure="pinFailure"
        :lock-message="lockMessage"
        @submit="(p: string) => emit('pin', p)"
      />

      <slot v-else-if="showBody" />

      <div
        v-else-if="status === 'accepted'"
        class="fe-ppage__state fe-ppage__state--ok"
        data-testid="public-page-accepted"
      >
        <slot name="accepted">
          <h2 class="fe-ppage__state-title">{{ t('plugin.page.accepted_title') }}</h2>
          <p class="fe-surface__text">{{ t('plugin.page.accepted_text') }}</p>
        </slot>
      </div>

      <div v-else-if="status === 'done'" class="fe-ppage__state fe-ppage__state--ok" data-testid="public-page-done">
        <slot name="done">
          <h2 class="fe-ppage__state-title">{{ t('plugin.page.done_title') }}</h2>
          <p class="fe-surface__text">{{ t('plugin.page.done_text') }}</p>
        </slot>
      </div>

      <div v-else-if="status === 'error'" class="fe-ppage__state" data-testid="public-page-error">
        <h2 class="fe-ppage__state-title">{{ t('plugin.page.error_title') }}</h2>
        <p v-if="failure" class="fe-ppage__error">{{ failure }}</p>
        <button type="button" class="fe-btn fe-btn--primary" @click="emit('retry')">
          {{ t('plugin.page.retry') }}
        </button>
      </div>

      <!-- ⚠ ONE sentence for expired, used up and withdrawn. Telling a
           visitor WHICH confirms that the link existed, which is more than
           somebody guessing tokens should learn. -->
      <div v-else class="fe-ppage__state" data-testid="public-page-unavailable">
        <h2 class="fe-ppage__state-title">{{ t('plugin.page.unavailable_title') }}</h2>
        <p class="fe-surface__text fe-surface__text--muted">{{ t('plugin.page.unavailable_text') }}</p>
      </div>

      <slot name="extra" />
    </main>

    <footer class="fe-ppage__foot">
      <PublicLanguagePicker
        :model-value="String(locale)"
        :plugin-note="pluginNote"
        @update:model-value="(v: string) => emit('update:locale', v)"
      />
      <p v-if="footerText" class="fe-ppage__footline" data-testid="public-footer-text">{{ footerText }}</p>
      <!-- The modest brand line the old page ended on: a small mark and one
           sentence, centred under the card. -->
      <span v-if="!hidePoweredBy" class="fe-ppage__footmark" data-testid="public-footer-powered">
        <img v-if="logoUrl" :src="logoUrl" alt="" />
        <LogoMark v-else-if="ownMark" />
        {{ t('public.served_by', { name: brandName }) }}
      </span>
    </footer>
  </div>
</template>
