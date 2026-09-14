/**
 * @brftech/filex-react — React adapter around the `<filex-explorer>`
 * Web Component.
 *
 * Uses `@lit/react`'s `createComponent` to map element attributes /
 * properties / events into idiomatic React props (camelCased event
 * handlers like `onError`, real prop assignment for the `config`
 * object, no need to imperatively reach for `.addEventListener`).
 *
 * Side-effect: importing this module also imports `@brftech/filex`
 * which registers the underlying custom element.
 */

import * as React from 'react';
import { createComponent } from '@lit/react';
import '@brftech/filex'; // side-effect: registers `<filex-explorer>`
import type { ExplorerConfig } from '@brftech/filex';

/**
 * Property names `<filex-explorer>` accepts, taken from the component
 * definition the element was built from.
 *
 * ⚠⚠ This list is the entire bridge, and handing `createComponent` the
 * registered class instead of it is why this package did not work at all.
 * `createComponent` decides prop-by-prop with `k in elementClass.prototype`:
 * a name it finds there is ASSIGNED to the element (`node[k] = value`),
 * everything else is handed to `React.createElement` and lands as an
 * attribute. Vue's `defineCustomElement` defines its props on each
 * INSTANCE (`_resolveProps`, at connectedCallback), so the class prototype
 * carries nothing but `constructor` — measured 2026-09-13 in a real
 * browser: `Object.getOwnPropertyNames(cls.prototype)` is `['constructor']`.
 *
 * Every prop therefore took the attribute path, and `config` — an object —
 * was stringified by React on the way: the element ended up with
 * `config="[object Object]"` and `el.config === '[object Object]'`. The
 * explorer never received a config, never mounted, and `<FileManager>`
 * rendered an empty page.
 *
 * The names come from `elementClass.def.props`, i.e. from the wrapper
 * component itself, so adding a prop to `@brftech/filex` cannot leave this
 * package one behind. FALLBACK_PROPS is only for an environment with no
 * custom-element registry (SSR) or a future Vue that stops exposing `def`;
 * `packages/react` is pinned to the wrapper's own list by
 * `web/tests/deploy/packageLook.test.ts`.
 */
const FALLBACK_PROPS = [
  'config',
  'apiBase',
  'endpoint',
  'locale',
  'theme',
  'timeZone',
  'trashVisible',
  'sidenav',
  'connections',
  'uiProfile',
] as const;

function elementPropNames(): readonly string[] {
  const registered =
    typeof customElements !== 'undefined'
      ? (customElements.get('filex-explorer') as
          | (CustomElementConstructor & { def?: { props?: unknown } })
          | undefined)
      : undefined;
  const declared = registered?.def?.props;
  if (Array.isArray(declared) && declared.length) return declared as string[];
  if (declared && typeof declared === 'object') {
    const keys = Object.keys(declared as Record<string, unknown>);
    if (keys.length) return keys;
  }
  return FALLBACK_PROPS;
}

/**
 * A stand-in class whose PROTOTYPE carries those names, which is the only
 * thing `createComponent` reads it for (that, and `.name` for the devtools
 * label — we pass `displayName` instead).
 *
 * It extends HTMLElement where there is one so the native properties keep
 * behaving exactly as they did: `id`, `title`, `hidden` and friends stay on
 * the property path, `style`/`className`/`children`/`ref` stay React's.
 * Nothing ever constructs it — an unregistered `class extends HTMLElement`
 * is only illegal to `new`, not to declare.
 */
function propBridgeClass(): CustomElementConstructor {
  const Base = (
    typeof HTMLElement !== 'undefined' ? HTMLElement : class {}
  ) as CustomElementConstructor;
  class FilexExplorerProps extends Base {}
  for (const name of elementPropNames()) {
    if (name in FilexExplorerProps.prototype) continue;
    Object.defineProperty(FilexExplorerProps.prototype, name, {
      value: undefined,
      writable: true,
      configurable: true,
      enumerable: false,
    });
  }
  return FilexExplorerProps;
}

const FilexElementClass = propBridgeClass();

/**
 * Idiomatic React component — used like a normal JSX tag with native
 * props.
 *
 *   <FileManager
 *     config={{ apiBase: 'https://files.example.com',
 *               auth: { kind: 'bearer', token } }}
 *     onError={(e) => console.error(e.detail)}
 *   />
 */
export const FileManager = createComponent({
  react: React,
  tagName: 'filex-explorer',
  elementClass: FilexElementClass,
  displayName: 'FileManager',
  events: {
    onError: 'error',
    onShareCreated: 'share-created',
    onFileOpened: 'file-opened',
    onUploadProgress: 'upload-progress',
    onSelectionChange: 'selection-change',
  },
});

/**
 * Re-export the config types so React consumers don't need a second
 * dependency on `@brftech/filex-core` just to type the `config` prop.
 */
export type {
  ExplorerConfig,
  AuthConfig,
  ThemeMode,
  LocaleCode,
  FileNode,
  ShareInfo,
  Capabilities,
  UploadInitResponse,
  UploadFinalizeResponse,
  ArchiveEntry,
  ViewMode,
} from '@brftech/filex-core';

/**
 * Helper-typed payloads the events carry — matches the SFC's emit
 * declarations so consumers get autocomplete on `event.detail`.
 */
export interface FilexErrorDetail {
  message: string;
  context?: unknown;
}

export interface FilexShareCreatedDetail {
  path: string;
  url: string;
  pin: string | null;
}

export interface FilexFileOpenedDetail {
  path: string;
  basename: string;
}

export interface FilexUploadProgressDetail {
  uploadId: string;
  percent: number;
  done: boolean;
}

export type FilexSelectionChangeDetail = Array<{
  path: string;
  basename: string;
  type: 'file' | 'dir';
}>;
