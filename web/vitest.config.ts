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
