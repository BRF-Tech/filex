/**
 * @brftech/filex — Web Component (custom element) wrapper around
 * `@brftech/filex-core`'s FileExplorer.
 *
 * Importing this file (side-effect) registers `<filex-explorer>` as a
 * global custom element. Use it in any framework / no framework:
 *
 *   <filex-explorer api-base="https://files.example.com" locale="tr"></filex-explorer>
 *
 *   <script type="module">
 *     // Side-effect import registers the element.
 *     import '@brftech/filex';
 *
 *     // Pass complex props as JS properties (auth, theme overrides, …).
 *     document.querySelector('filex-explorer').config = {
 *       apiBase: 'https://files.example.com',
 *       auth: { kind: 'bearer', token: '<jwt>' },
 *       locale: 'tr',
 *     };
 *   </script>
 *
 * Attributes that map to top-level config keys are auto-parsed: `api-base`,
 * `endpoint`, `locale`, `theme`, `trash-visible`. Anything else (auth,
 * shareBase, custom endpoints…) is set via the `config` JS property.
 *
 * `shadowRoot: false` — Tailwind / global CSS / OS dark mode propagate
 * naturally into the explorer; `<style>` from the core stylesheet is
 * appended to the host page once on first registration.
 */

import { defineCustomElement, h, ref, watch, type PropType } from 'vue';
import FileExplorer from '@brftech/filex-core/src/FileExplorer.vue';
import type { ExplorerConfig, LocaleCode, ThemeMode } from '@brftech/filex-core';
import coreCss from '@brftech/filex-core/style.css?inline';

/**
 * Inject the core stylesheet into <head> once. We `?inline` it (handled
 * by Vite) so the CSS string lives inside the JS bundle — no separate
 * stylesheet to wire up on the host page. The bundle ALSO emits a
 * `style.css` file alongside in case the consumer wants the link tag
 * approach instead.
 */
let stylesInjected = false;
function injectStylesOnce() {
  if (stylesInjected) return;
  if (typeof document === 'undefined') return;
  const tag = document.createElement('style');
  tag.setAttribute('data-filex', '');
  tag.textContent = coreCss as unknown as string;
  document.head.appendChild(tag);
  stylesInjected = true;
}

/**
 * Build the `config` object passed to the underlying FileExplorer SFC.
 * Pulls simple attributes (api-base, locale, theme, trash-visible) and
 * merges anything the consumer set via the `config` JS property.
 */
function buildConfig(
  attrs: { apiBase?: string; endpoint?: string; locale?: string; theme?: string; trashVisible?: boolean | string },
  override: ExplorerConfig | null,
): ExplorerConfig {
  const base: ExplorerConfig = {};
  if (attrs.apiBase) base.apiBase = attrs.apiBase;
  if (attrs.endpoint) base.endpoint = attrs.endpoint;
  if (attrs.locale === 'tr' || attrs.locale === 'en') base.locale = attrs.locale as LocaleCode;
  if (attrs.theme === 'light' || attrs.theme === 'dark' || attrs.theme === 'auto') {
    base.theme = attrs.theme as ThemeMode;
  }
  if (attrs.trashVisible !== undefined) {
    base.trashVisible =
      attrs.trashVisible === true || attrs.trashVisible === 'true' || attrs.trashVisible === '';
  }
  // `config` JS-property overrides win over individual attributes —
  // letting power users feed the whole shape at once.
  return { ...base, ...(override ?? {}) };
}

/**
 * Wrapper component — translates element attributes/properties into the
 * SFC's single `config` prop and forwards every event back out as a
 * native CustomEvent. Vue's `defineCustomElement` discovers props via
 * the `props` option below; events are dispatched against the host
 * element by Vue itself when we `emit(...)` here.
 */
const FilexExplorerWrapper = defineCustomElement({
  /**
   * Host attrs (style/class) must NOT fall through onto the inner `.fe`
   * root: an embedder's `el.style.cssText = 'display:block;height:100%'`
   * would get copied verbatim, and the inline display:block overrides the
   * core `.fe{display:flex}` — the flex column collapses and internal
   * scrolling dies in height-constrained embeds. The host element keeps
   * its own style/class regardless (it is a real DOM element).
   */
  inheritAttrs: false,
  props: {
    /** Full ExplorerConfig as a JS property (preferred for complex shape). */
    config: {
      type: Object as PropType<ExplorerConfig | null>,
      default: null,
    },
    /** Shortcut attribute → config.apiBase. */
    apiBase: { type: String, default: '' },
    /** Shortcut attribute → config.endpoint (legacy). */
    endpoint: { type: String, default: '' },
    locale: { type: String, default: '' },
    theme: { type: String, default: '' },
    trashVisible: { type: [Boolean, String], default: undefined },
  },
  emits: [
    'share-created',
    'file-opened',
    'error',
    'upload-progress',
    'selection-change',
  ],
  setup(props, { emit }) {
    injectStylesOnce();

    // Reactive config — recomputed when any input attribute or the
    // `config` JS property changes.
    const merged = ref<ExplorerConfig>(
      buildConfig(
        {
          apiBase: props.apiBase,
          endpoint: props.endpoint,
          locale: props.locale,
          theme: props.theme,
          trashVisible: props.trashVisible,
        },
        props.config,
      ),
    );

    watch(
      () => [
        props.config,
        props.apiBase,
        props.endpoint,
        props.locale,
        props.theme,
        props.trashVisible,
      ],
      () => {
        merged.value = buildConfig(
          {
            apiBase: props.apiBase,
            endpoint: props.endpoint,
            locale: props.locale,
            theme: props.theme,
            trashVisible: props.trashVisible,
          },
          props.config,
        );
      },
      { deep: true },
    );

    return () =>
      h(FileExplorer as never, {
        config: merged.value,
        onShareCreated: (p: unknown) => emit('share-created', p),
        onFileOpened: (f: unknown) => emit('file-opened', f),
        onError: (e: unknown) => emit('error', e),
        onUploadProgress: (p: unknown) => emit('upload-progress', p),
        onSelectionChange: (s: unknown) => emit('selection-change', s),
      });
  },
}, { shadowRoot: false });

/** Public class — useful for tests / programmatic instantiation. */
export const FilexElement = FilexExplorerWrapper;

/**
 * Self-register on import so consumers can do
 *   `import '@brftech/filex'`
 * and have the element available immediately. Idempotent — re-import
 * doesn't throw.
 */
if (typeof customElements !== 'undefined' && !customElements.get('filex-explorer')) {
  customElements.define('filex-explorer', FilexExplorerWrapper);
}

/**
 * Augment the global JSX/HTML element typings so TypeScript projects
 * embedding the WC get autocomplete + type checking on `<filex-explorer>`.
 *
 * We deliberately resolve to `HTMLElement` rather than the wrapper class
 * because `InstanceType<typeof FilexExplorerWrapper>` triggers a TS2502
 * "self-referential type annotation" error inside this very file.
 */
declare global {
  interface HTMLElementTagNameMap {
    'filex-explorer': HTMLElement;
  }
}

export type { ExplorerConfig } from '@brftech/filex-core';
