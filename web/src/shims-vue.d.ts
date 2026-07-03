declare module '*.vue' {
  import type { DefineComponent } from 'vue';
  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>;
  export default component;
}

declare module '*.json' {
  const value: unknown;
  export default value;
}

// <filex-explorer> Web Component is loaded at runtime via /embed.js.
// Declare it here so vue-tsc knows it's a valid custom element with
// the supported props.
declare module 'vue' {
  interface GlobalComponents {
    'filex-explorer': {
      'api-base'?: string;
      'storage-id'?: number;
      config?: string;
      class?: string;
    };
  }
}
export {};
