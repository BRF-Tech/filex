// Type shims for peer-optional packages — see explanation in
// packages/webcomponent/src/peers.d.ts. The React adapter doesn't
// import these directly but vue-tsc walks the type graph of @brftech/filex
// (which depends on @brftech/filex-core), so the shim is needed here too.

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
