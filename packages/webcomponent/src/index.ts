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
 * `endpoint`, `locale`, `theme`, `time-zone`, `trash-visible`, `sidenav`,
 * `connections`, `ui-profile`.
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
import { resolveUiProfile } from '@brftech/filex-core';
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
    timeZone?: string;
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
  // zaman:z3 — the host's default clock (tier `host` in core's lib/timezone).
  // Passed through as given: core ignores an id the browser does not accept,
  // and a visitor's own pick in the explorer still outranks it.
  if (attrs.timeZone) base.timeZone = attrs.timeZone;
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
  // ⚠ Resolved rather than passed through, and resolved by CORE's own rule
  // (`lib/uiProfile`): an attribute is a string the DOM hands over unexamined,
  // so a typo — or the third profile that was removed after v0.40.0 — must
  // have a defined answer rather than an unknown profile reaching the SFC.
  // That answer is `"standard"`, said once in the console.
  // An absent attribute stays absent, so the core default keeps deciding.
  if (typeof attrs.uiProfile === 'string' && attrs.uiProfile !== '') {
    base.uiProfile = resolveUiProfile(attrs.uiProfile);
  }
  // `config` JS-property overrides win over individual attributes —
  // letting power users feed the whole shape at once.
  return { ...base, ...(override ?? {}) };
}

/**
 * Can the core resolve endpoints from this config yet?
 *
 * Mirrors `resolveEndpoints()` in `@brftech/filex-core`, which THROWS
 * ("config requires either `apiBase` or `endpoint`") when neither is
 * there — including the empty string, which is a valid relative root.
 *
 * ⚠⚠ Load-bearing for every host that cannot set a property before the
 * element is inserted, and React is exactly that host. `@lit/react`'s
 * `createComponent` assigns element properties in a layout effect, which
 * runs AFTER React has put the node in the document — so the element is
 * upgraded, mounted, and inside the core's setup with `config === null`
 * one tick before the config it was given arrives. Without this guard the
 * core threw there, the Vue instance died, and `<FileManager>` rendered a
 * blank page: no explorer, no stylesheet, nothing in the DOM. Measured
 * 2026-09-13 in a real browser with the built packages, and it is the
 * whole of what a React consumer got.
 *
 * The reactive watch below re-runs this the moment a config lands, so the
 * fix is a wait, not a refusal.
 */
function isMountable(cfg: ExplorerConfig): boolean {
  return cfg.apiBase != null || !!cfg.endpoint;
}

/**
 * A host that never supplies one at all would now get an empty element
 * instead of an exception, which is a quieter kind of broken. So the wait
 * says so once, on the next task — after any framework has had its turn
 * at assigning properties.
 */
function warnIfStillUnconfigured(get: () => ExplorerConfig, tag: string): void {
  if (typeof setTimeout !== 'function') return;
  setTimeout(() => {
    if (isMountable(get())) return;
    console.warn(
      `[@brftech/filex] <${tag}> has no apiBase and no endpoint, so there is ` +
        'nothing to render. Set the `config` property (or the `api-base` ' +
        'attribute) on the element.',
    );
  }, 0);
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
    /** Shortcut attribute → config.timeZone (the host's default clock). */
    timeZone: { type: String, default: '' },
    trashVisible: { type: [Boolean, String], default: undefined },
    /** Shortcut attribute → config.sideNav (the navigation panel). */
    sidenav: { type: [Boolean, String], default: undefined },
    /** Shortcut attribute → config.connections (How to connect + API keys). */
    connections: { type: [Boolean, String], default: undefined },
    /** Shortcut attribute → config.uiProfile ('standard' | 'simple'). */
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
          timeZone: props.timeZone,
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
        props.timeZone,
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
            timeZone: props.timeZone,
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

    warnIfStillUnconfigured(() => merged.value, 'filex-explorer');

    return () =>
      !isMountable(merged.value)
        ? null
        : h(FileExplorer as never, {
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
      closable: { type: [Boolean, String], default: undefined },
    },
    /* ⚠ No `initial-tab`, and no `changed`. Both belonged to the storage half
       the panel lost in v0.43.0: the attribute chose between "storages" and
       "connect" when there were two halves, and the event fired when a storage
       was added or removed here. An attribute that can only take one value and
       an event that can never fire are worse than absent — a host wires them
       and believes them. Storages are managed in the admin panel. */
    emits: ['close', 'error'],
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

      // Same wait as the explorer, for the same reason: `useConnections`
      // runs `useFileApi` → `resolveEndpoints`, which throws on a config
      // that carries neither half. The React README tells people to render
      // this element directly and set `config` on the ref, which is the
      // late-property path exactly.
      warnIfStillUnconfigured(() => merged.value, 'filex-connections');

      return () =>
        !isMountable(merged.value)
          ? null
          : h(ConnectionsPanel as never, {
              config: merged.value,
              closable:
                props.closable === true ||
                props.closable === 'true' ||
                props.closable === '',
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
