// Vitest config for the filex admin UI.
//
// Inherits everything from vite.config.ts (aliases, plugins) so component
// tests resolve "@/foo" exactly the same way as runtime code does.
import { defineConfig, mergeConfig } from 'vitest/config';
import viteConfig from './vite.config';

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'happy-dom',
      environmentOptions: {
        happyDOM: {
          settings: {
            // ⚠ An <iframe src> a component renders stays a BLANK frame at
            // that URL: its contentWindow is there for a test to talk to and
            // its load event fires, but happy-dom does not fetch the page.
            // Left on, every app/draw.io/download frame was a real request
            // to localhost:3000 (or the internet) that came back ECONNREFUSED
            // whenever it liked (task #127). tests/helpers/noNetwork refuses
            // whatever else still tries.
            navigation: { disableChildFrameNavigation: true },
          },
        },
      },
      globals: true,
      setupFiles: ['./tests/setup.ts'],
      include: ['tests/**/*.test.ts', 'src/**/*.test.ts'],
      coverage: {
        provider: 'v8',
        reporter: ['text', 'html', 'lcov'],
        exclude: [
          'node_modules/**',
          'dist/**',
          'tests/**',
          '**/*.config.*',
          '**/*.d.ts',
        ],
      },
    },
  }),
);
