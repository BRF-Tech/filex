// ESLint v9 flat config for the admin SPA.
//
// Minimal on purpose: catches obvious bugs (unused vars / unreachable
// code / `===` etc) without imposing style. The team's TypeScript +
// Vue tooling already takes care of formatting via Prettier-on-save +
// vue-tsc; ESLint's job here is the JavaScript-aware behaviours
// neither of those covers.
//
// Plugins:
//   - @eslint/js — built-in recommended rule set
//   - eslint-plugin-vue — .vue parser + flat-config presets
//   - typescript-eslint — .ts parser + recommended rules
//
// Files outside src/ (config files at the root, dist artifacts, the
// auto-generated routeTree, …) are excluded so a stale build doesn't
// red the lint job.
import js from '@eslint/js';
import pluginVue from 'eslint-plugin-vue';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    ignores: [
      'dist/**',
      'node_modules/**',
      'public/**',
      '*.config.{js,ts}',
      'vite.config.*',
      'env.d.ts',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/recommended'],
  {
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: {
        parser: tseslint.parser,
      },
    },
  },
  {
    rules: {
      // Allow `_` prefixed args/vars (helper destructures, …).
      '@typescript-eslint/no-unused-vars': [
        'warn',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      // Vue 3 recommended is opinionated about template-side
      // attribute order; loosen so the existing codebase passes.
      'vue/attributes-order': 'off',
      'vue/multi-word-component-names': 'off',
      'vue/html-self-closing': 'off',
      // Codebase has legacy patterns we'll clean up incrementally.
      // Demoted to `warn` so CI doesn't gate on them — promote to
      // `error` once the existing call sites are migrated.
      'vue/no-deprecated-filter': 'warn',          // Vue 2 `{{ x | f }}` filters
      '@typescript-eslint/no-explicit-any': 'warn', // some api/* surfaces
    },
  },
  {
    // Cypress e2e specs assert with chai getter-properties
    // (`expect(x).to.exist`, `.to.be.true`), which are bare member
    // expressions — precisely what no-unused-expressions flags. They ARE
    // the assertion, not dead code, so turn the rule off for the spec tree
    // (covers existing + future chai getters without per-line disables).
    files: ['cypress/**/*.cy.ts'],
    rules: {
      '@typescript-eslint/no-unused-expressions': 'off',
      'no-unused-expressions': 'off',
    },
  },
);
