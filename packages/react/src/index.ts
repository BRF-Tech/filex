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
 * Resolve the registered class. We do this lazily inside a getter so
 * tree-shaking-time evaluation in some bundlers doesn't trip the
 * `customElements.get(…)` lookup before the side-effect import has run
 * (it has, but TS analysers can be jumpy). At runtime this evaluates
 * once at module load.
 */
const FilexElementClass =
  (typeof customElements !== 'undefined' && customElements.get('filex-explorer')) ||
  // Fallback for SSR — the element class is irrelevant on the server
  // because createComponent only renders the tag string. Provide an
  // empty class shim so types resolve.
  (class {} as unknown as CustomElementConstructor);

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
