// Types for app-locations.mjs (the one table of where the app builds live).

export interface AppLocation {
  env: string;
  dirs: string[];
  build: string;
  repo: string;
}

export const APP_LOCATIONS: Record<string, AppLocation>;

export interface LocatedApp {
  present: boolean;
  wasm: string;
  manifestPath: string;
  // The manifest as parsed JSON; the caller narrows it to its own shape.
  manifest: any;
  dirs: string[];
  how: string;
}

export function locateApp(name: string): LocatedApp;
