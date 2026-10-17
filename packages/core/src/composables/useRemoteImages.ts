/**
 * useRemoteImages — what a preview does with the pictures from other servers
 * it held back (lib/sanitizeHtml, RemoteImages): one gate per opening of a
 * file, shared by the surfaces that draw it.
 *
 *   - a drawing asks `policy()` which pictures it may draw now: the person's
 *     setting (lib/remoteImages), "all of this file", or the ones they already
 *     allowed in this opening; every other one is a placeholder;
 *   - a click (or Enter / Space) on a placeholder puts the question on screen
 *     (`asking`, drawn by components/RemoteImageDialog, filex's own dialog -
 *     never the browser's `confirm()`): the server's name, what loading it
 *     tells that server;
 *   - `accept` draws that picture (every placeholder of the same address),
 *     `acceptAll` every one of the file, for this opening only (`reset` on the
 *     next file); the setting turned on draws them all at once.
 *
 * A drawn picture is an `<img>` made in the page's own document, with
 * `referrerpolicy="no-referrer"` set before its address, so the server learns
 * nothing about the page it is on.
 *
 * ⚠ One mechanism for every surface - the explorer's preview, a notebook, an
 * app's release notes, in the web app, the desktop app and every embed. A
 * viewer inside the preview takes the preview's gate (`injectRemoteImages`)
 * so the question is asked once, above everything; a surface on its own makes
 * one and draws its own dialog.
 */
import { computed, getCurrentInstance, inject, onBeforeUnmount, provide, ref, watch, type ComputedRef, type InjectionKey, type Ref } from 'vue';
import { HELD_IMAGE_ATTRS, remoteImageUrl, remoteSvgPicture, type RemoteImages } from '../lib/sanitizeHtml';
import { REMOTE_IMAGES_EVENT, remoteImagesOn } from '../lib/remoteImages';
import { onPrefs } from '../lib/prefs';
import { useLocale } from './useLocale';
import type { LocaleCode } from '../types/ExplorerConfig';

/** The question on screen: which picture, on which server, and how many others wait. */
export interface RemoteImageAsk {
  /** The picture's absolute address. */
  src: string;
  /** Its server, as the address names it (`host[:port]`, an IDN in its xn-- form). */
  host: string;
  /** Placeholders of other addresses still waiting in this file. */
  others: number;
  /**
   * The question is about the whole file (a book whose chapters are drawn in
   * a frame): only "Load all remote images in this file" can be answered.
   */
  whole?: boolean;
}

export interface RemoteImageGate {
  asking: Ref<RemoteImageAsk | null>;
  /** The person's setting: previews load pictures from elsewhere without asking. */
  settingOn: Ref<boolean>;
  /** Every picture of this opening may be drawn: the setting, or "Load all". */
  allLoaded: ComputedRef<boolean>;
  /** Ask about the whole file: these are the addresses from elsewhere it names (RemoteImageAsk.whole). */
  askWhole(urls: string[]): void;
  /** The sanitizer's answer for a drawing made now (lib/sanitizeHtml). */
  policy(): RemoteImages;
  /** A container whose placeholders this gate answers for. */
  watchRoot(el: Ref<HTMLElement | null | undefined>): void;
  /** For the container: a click on a placeholder asks. */
  onClick(e: MouseEvent): void;
  /** For the container: Enter or Space on a placeholder asks. */
  onKeydown(e: KeyboardEvent): void;
  /** Draw the picture asked about. */
  accept(): void;
  /** Draw every picture from elsewhere in this file, for this opening. */
  acceptAll(): void;
  dismiss(): void;
  /** Another file: nothing allowed for the last one is carried over. */
  reset(): void;
}

export const REMOTE_IMAGE_GATE: InjectionKey<RemoteImageGate> = Symbol('fe-remote-images');

const PLACEHOLDER = '[data-remote-image]';

/** Draw the picture a placeholder holds, in its place. */
export function revealRemoteImage(el: Element): HTMLImageElement | null {
  const u = remoteImageUrl(el.getAttribute('data-remote-image') ?? '');
  if (!u) return null;
  if (el.namespaceURI === 'http://www.w3.org/2000/svg') {
    // A diagram's picture: an HTML one in the box the placeholder held.
    const fo = remoteSvgPicture(el.ownerDocument, u.href, (name) => el.getAttribute(`data-remote-${name.toLowerCase()}`));
    const t = el.getAttribute('transform');
    if (t) fo.setAttribute('transform', t);
    el.replaceWith(fo);
    return fo.querySelector('img');
  }
  const img = el.ownerDocument.createElement('img');
  // ⚠ Before the address: the request starts when `src` is set.
  img.setAttribute('referrerpolicy', 'no-referrer');
  for (const name of HELD_IMAGE_ATTRS) {
    const v = el.getAttribute(`data-remote-${name}`);
    if (v !== null) img.setAttribute(name, v);
  }
  img.setAttribute('src', u.href);
  el.replaceWith(img);
  return img;
}

export function useRemoteImages(locale: () => LocaleCode | string | undefined = () => 'en'): RemoteImageGate {
  const { t } = useLocale(() => locale() ?? 'en');
  const asking = ref<RemoteImageAsk | null>(null);
  const settingOn = ref(remoteImagesOn());
  const all = ref(false);
  const allowed = new Set<string>();
  const roots: Ref<HTMLElement | null | undefined>[] = [];

  function placeholders(): Element[] {
    const out: Element[] = [];
    for (const r of roots) {
      if (r.value) out.push(...Array.from(r.value.querySelectorAll(PLACEHOLDER)));
    }
    return out;
  }

  function revealWhere(match: (src: string) => boolean): void {
    for (const el of placeholders()) {
      if (match(el.getAttribute('data-remote-image') ?? '')) revealRemoteImage(el);
    }
  }

  function loads(src: string): boolean {
    return settingOn.value || all.value || allowed.has(src);
  }

  function ask(el: Element): void {
    const u = remoteImageUrl(el.getAttribute('data-remote-image') ?? '');
    if (!u) return;
    if (loads(u.href)) {
      revealRemoteImage(el);
      return;
    }
    const others = placeholders().filter((p) => p.getAttribute('data-remote-image') !== u.href).length;
    asking.value = { src: u.href, host: u.host, others };
  }

  function placeholderOf(e: Event): Element | null {
    const target = e.target as Element | null;
    const el = target && typeof target.closest === 'function' ? target.closest(PLACEHOLDER) : null;
    const box = e.currentTarget as Element | null;
    return el && (!box || typeof box.contains !== 'function' || box.contains(el)) ? el : null;
  }

  const sync = (): void => {
    settingOn.value = remoteImagesOn();
  };
  const offPrefs = onPrefs(sync);
  if (typeof window !== 'undefined') window.addEventListener(REMOTE_IMAGES_EVENT, sync);
  if (getCurrentInstance()) {
    onBeforeUnmount(() => {
      offPrefs();
      if (typeof window !== 'undefined') window.removeEventListener(REMOTE_IMAGES_EVENT, sync);
    });
  }
  watch(settingOn, (on) => {
    if (!on) return;
    asking.value = null;
    revealWhere(() => true);
  });

  return {
    asking,
    settingOn,
    allLoaded: computed(() => settingOn.value || all.value),
    askWhole(urls) {
      const u = urls.map((s) => remoteImageUrl(s)).find((x) => !!x);
      if (!u || settingOn.value || all.value) return;
      asking.value = { src: u.href, host: u.host, others: Math.max(0, urls.length - 1), whole: true };
    },
    policy: () => ({ label: t('viewer.remote_image.label'), load: loads }),
    watchRoot(el) {
      roots.push(el);
    },
    onClick(e) {
      const el = placeholderOf(e);
      if (!el) return;
      // A placeholder inside a link (a badge that links somewhere) asks; it
      // does not follow the link.
      e.preventDefault();
      e.stopPropagation();
      ask(el);
    },
    onKeydown(e) {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      const el = placeholderOf(e);
      if (!el) return;
      e.preventDefault();
      e.stopPropagation();
      ask(el);
    },
    accept() {
      const q = asking.value;
      asking.value = null;
      if (!q) return;
      allowed.add(q.src);
      revealWhere((src) => src === q.src);
    },
    acceptAll() {
      asking.value = null;
      all.value = true;
      revealWhere(() => true);
    },
    dismiss() {
      asking.value = null;
    },
    reset() {
      asking.value = null;
      all.value = false;
      allowed.clear();
    },
  };
}

/** Make `gate` the one the viewers inside this component answer to. */
export function provideRemoteImages(gate: RemoteImageGate): void {
  provide(REMOTE_IMAGE_GATE, gate);
}

/** The gate of the preview this component is drawn in, or null on its own. */
export function injectRemoteImages(): RemoteImageGate | null {
  return getCurrentInstance() ? inject(REMOTE_IMAGE_GATE, null) : null;
}
