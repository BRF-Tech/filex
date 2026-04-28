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
