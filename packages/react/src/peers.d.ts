// Type shims for peer-optional packages — see explanation in
// packages/webcomponent/src/peers.d.ts. The React adapter doesn't
// import these directly but vue-tsc walks the type graph of @brftech/filex
// (which depends on @brftech/filex-core), so the shim is needed here too.

declare module 'markdown-it';
declare module 'highlight.js';
declare module 'monaco-editor';
