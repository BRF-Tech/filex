// Type shims for peer-optional packages that the core SFC imports
// dynamically. Required here as well — vue-tsc resolves the imports
// transitively through the published `dist/index.d.ts` of the core
// package, but the rolled-up declaration file does not include the
// shim. Mirroring the declaration locally keeps the build green.
//
// Keep in sync with packages/core/src/types/peers.d.ts.

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
