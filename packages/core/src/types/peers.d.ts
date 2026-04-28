/**
 * Type shims for peer-style optional dependencies.
 *
 * These libraries are lazy-loaded via dynamic import and listed under
 * `peerDependenciesMeta.optional` (and `optionalDependencies` for
 * convenience). We don't want hard `@types/*` deps just to please the
 * compiler, so we declare them as `any` here. The dynamic import call
 * sites cast through `any` anyway.
 */
declare module 'markdown-it';
declare module 'highlight.js';
declare module 'monaco-editor';
