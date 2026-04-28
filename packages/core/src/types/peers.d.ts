/**
 * Type shims for peer-style optional dependencies.
 *
 * These libraries are lazy-loaded via dynamic import and listed under
 * `peerDependenciesMeta.optional` (and `optionalDependencies` for
 * convenience). We don't want hard `@types/*` deps just to please the
 * compiler, so we declare them as `any` here. The dynamic import call
 * sites cast through `any` anyway.
 *
 * Keep in sync with packages/webcomponent/src/peers.d.ts and
 * packages/react/src/peers.d.ts — the rolled-up `dist/index.d.ts`
 * forwards these references and vue-tsc walks the type graph in the
 * sibling packages, so the same shims must live there too.
 */
declare module 'markdown-it';
declare module 'highlight.js';
declare module 'monaco-editor';
declare module '@google/model-viewer';
declare module 'epubjs';
declare module 'mermaid';
declare module 'utif';
declare module 'ag-psd';
declare module 'pdfjs-dist';
declare module 'pdfjs-dist/legacy/build/pdf';
declare module 'pdfjs-dist/build/pdf.worker.min';
declare module 'papaparse';
declare module 'katex';

/**
 * Custom element registered by `@google/model-viewer` — keeps the
 * compiler happy when the SFC template includes `<model-viewer>`.
 */
declare namespace JSX {
  interface IntrinsicElements {
    'model-viewer': {
      src?: string;
      alt?: string;
      'auto-rotate'?: boolean | string;
      'camera-controls'?: boolean | string;
      'touch-action'?: string;
      'shadow-intensity'?: string | number;
      [key: string]: unknown;
    };
  }
}
