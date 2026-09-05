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
 *     // ⚠ Set `config` BEFORE the import that registers the element, not
 *     // after. Registering upgrades and MOUNTS it, and the explorer loads its
 *     // first folder on mount — a config assigned afterwards misses that one
 *     // request, which then goes out unauthenticated against the default
 *     // adapter. The element renders fine and the file list says "Could not
 *     // load this folder", which sends everybody looking at the backend.
 *     document.querySelector('filex-explorer').config = {
 *       apiBase: 'https://files.example.com',
 *       auth: { kind: 'bearer', token: '<jwt>' },
 *       locale: 'tr',
 *     };
 *     await import('@brftech/filex');   // side effect: registers the element
 *   </script>
 *
 * Attributes that map to top-level config keys are auto-parsed: `api-base`,
 * `endpoint`, `locale`, `theme`, `trash-visible`, `sidenav`, `connections`,
 * `ui-profile`.
 * Anything else (auth, shareBase, custom endpoints…) is set via the `config`
 * JS property.
 *
 *   <filex-explorer api-base="https://files.example.com" sidenav ui-profile="simple">
 *   </filex-explorer>
 *
 * Boolean attributes follow the DOM convention: present (or `="true"`) is true,
 * `="false"` is false, absent leaves the core default alone.
 *
 * `shadowRoot: false` — Tailwind / global CSS / OS dark mode propagate
 * naturally into the explorer; `<style>` from the core stylesheet is
 * appended to the host page once on first registration.
 */

import { defineCustomElement, h, ref, watch, type PropType } from 'vue';
import FileExplorer from '@brftech/filex-core/src/FileExplorer.vue';
import ConnectionsPanel from '@brftech/filex-core/src/components/ConnectionsPanel.vue';
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
  attrs: {
    apiBase?: string;
    endpoint?: string;
    locale?: string;
    theme?: string;
    trashVisible?: boolean | string;
    sidenav?: boolean | string;
    connections?: boolean | string;
    uiProfile?: string;
  },
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
  // gezinti:g1 — the navigation panel, for host pages that never touch JS.
  // ⚠ `undefined` is left ALONE rather than coerced to false: the core default
  // is on, and an element that never mentions `sidenav` must keep it. Writing
  // `base.sideNav = attrs.sidenav === true || …` would have every plain
  // `<filex-explorer>` silently opt out of the panel.
  if (attrs.sidenav !== undefined) {
    base.sideNav = attrs.sidenav === true || attrs.sidenav === 'true' || attrs.sidenav === '';
  }
  // The panel's "How to connect" + "API keys" entries. Same `undefined` rule:
  // the core default depends on `uiProfile`, and an element that never mentions
  // the attribute must not overrule it.
  if (attrs.connections !== undefined) {
    base.connections =
      attrs.connections === true || attrs.connections === 'true' || attrs.connections === '';
  }
  // surucu:d1 — 'drive' joins the two. Matched against the union rather than
  // passed through, so a typo ("drve") leaves the core default alone instead of
  // reaching the SFC as an unknown profile.
  if (
    attrs.uiProfile === 'simple' ||
    attrs.uiProfile === 'standard' ||
    attrs.uiProfile === 'drive'
  ) {
    base.uiProfile = attrs.uiProfile;
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
    /** Shortcut attribute → config.sideNav (the navigation panel). */
    sidenav: { type: [Boolean, String], default: undefined },
    /** Shortcut attribute → config.connections (How to connect + API keys). */
    connections: { type: [Boolean, String], default: undefined },
    /** Shortcut attribute → config.uiProfile ('standard' | 'simple' | 'drive'). */
    uiProfile: { type: String, default: '' },
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
          sidenav: props.sidenav,
          connections: props.connections,
          uiProfile: props.uiProfile,
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
        props.sidenav,
        props.connections,
        props.uiProfile,
      ],
      () => {
        merged.value = buildConfig(
          {
            apiBase: props.apiBase,
            endpoint: props.endpoint,
            locale: props.locale,
            theme: props.theme,
            trashVisible: props.trashVisible,
            sidenav: props.sidenav,
            connections: props.connections,
            uiProfile: props.uiProfile,
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

/**
 * `<filex-connections>` — the storage-connection surface as a custom
 * element, so a host with no bundler (the desktop shell, a plain page)
 * mounts the SAME component the admin SPA imports as an SFC. There is no
 * second form and no second set of instructions anywhere.
 *
 * ⚠⚠ Configure it through the `config` PROPERTY, exactly like the
 * explorer:
 *
 *   el.config = { ...el.config, locale: 'tr' };
 *
 * Setting `el.locale = 'tr'` changes a property nothing renders from:
 * `buildConfig` merges `{...attributes, ...config}` and the config object
 * wins, so an attribute is only ever a fallback for a key the config does
 * not carry. That exact mistake shipped in v0.19.0 — the shell went
 * Turkish while the file list stayed English, and the element reported
 * `locale === 'tr'` the whole time.
 */
const FilexConnectionsWrapper = defineCustomElement(
  {
    inheritAttrs: false,
    props: {
      config: {
        type: Object as PropType<ExplorerConfig | null>,
        default: null,
      },
      apiBase: { type: String, default: '' },
      endpoint: { type: String, default: '' },
      locale: { type: String, default: '' },
      theme: { type: String, default: '' },
      /** 'storages' | 'connect' — which half to open on. */
      initialTab: { type: String, default: '' },
      closable: { type: [Boolean, String], default: undefined },
    },
    emits: ['changed', 'close', 'error'],
    setup(props, { emit }) {
      injectStylesOnce();

      const attrs = () => ({
        apiBase: props.apiBase,
        endpoint: props.endpoint,
        locale: props.locale,
        theme: props.theme,
      });
      const merged = ref<ExplorerConfig>(buildConfig(attrs(), props.config));

      watch(
        () => [props.config, props.apiBase, props.endpoint, props.locale, props.theme],
        () => {
          merged.value = buildConfig(attrs(), props.config);
        },
        { deep: true },
      );

      return () =>
        h(ConnectionsPanel as never, {
          config: merged.value,
          initialTab: props.initialTab === 'connect' ? 'connect' : 'storages',
          closable:
            props.closable === true || props.closable === 'true' || props.closable === '',
          onChanged: () => emit('changed'),
          onClose: () => emit('close'),
          onError: (e: unknown) => emit('error', e),
        });
    },
  },
  { shadowRoot: false },
);

/** Public classes — useful for tests / programmatic instantiation. */
export const FilexElement = FilexExplorerWrapper;
export const FilexConnectionsElement = FilexConnectionsWrapper;

/**
 * Self-register on import so consumers can do
 *   `import '@brftech/filex'`
 * and have the elements available immediately. Idempotent — re-import
 * doesn't throw.
 */
if (typeof customElements !== 'undefined' && !customElements.get('filex-explorer')) {
  customElements.define('filex-explorer', FilexExplorerWrapper);
}
if (typeof customElements !== 'undefined' && !customElements.get('filex-connections')) {
  customElements.define('filex-connections', FilexConnectionsWrapper);
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
    'filex-connections': HTMLElement;
  }
}

export type { ExplorerConfig } from '@brftech/filex-core';
