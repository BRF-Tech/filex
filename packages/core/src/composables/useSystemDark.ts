/**
 * useSystemDark — is the operating system in dark mode, as a ref that turns
 * with it while the component is mounted.
 *
 * ⚠⚠ `window.matchMedia('(prefers-color-scheme: dark)').matches` read inside
 * a `computed` is read ONCE: Vue cannot track a media query, so a page that
 * resolves "auto" that way keeps the mode it was opened in. The public page
 * (a share, a file request, an app's signing page) did exactly that — opened
 * dark, the OS turned light, and the page stayed dark with `fe--theme-dark`
 * pinned on its root (#57, measured in a browser). Every screen that resolves
 * `auto` in script takes it from here, so the listener and its cleanup exist
 * once.
 */
import { onBeforeUnmount, onMounted, ref, type Ref } from 'vue';

export function useSystemDark(): Readonly<Ref<boolean>> {
  let mq: MediaQueryList | undefined;
  try {
    mq = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia('(prefers-color-scheme: dark)')
      : undefined;
  } catch {
    mq = undefined;
  }
  const dark = ref(!!mq?.matches);
  const onChange = (e: { matches: boolean }) => {
    dark.value = e.matches;
  };
  onMounted(() => mq?.addEventListener?.('change', onChange));
  onBeforeUnmount(() => mq?.removeEventListener?.('change', onChange));
  return dark;
}
