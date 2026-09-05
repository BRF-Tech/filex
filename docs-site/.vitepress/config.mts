import { defineConfig } from 'vitepress'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'
import { dirname, resolve } from 'node:path'
import fs from 'node:fs'
import { githubSlug } from './github-slug.mjs'

// docs.filex.sh — VitePress site over the existing `docs/` folder.
// Markdown sources are NOT copied: `srcDir` points at ../docs so the
// repository docs stay the single source of truth.

const __dirname = dirname(fileURLToPath(import.meta.url))
const docsDir = resolve(__dirname, '../../docs')

// `srcDir` lives outside this package, so modules generated from the markdown
// (they import `vue` / `vue/server-renderer`) cannot reach docs-site's
// node_modules by walking up from ../docs under pnpm's isolated layout.
// Alias both specifiers to the copies installed for this package.
const require = createRequire(import.meta.url)
const vueDir = dirname(require.resolve('vue/package.json'))

const REPO = 'https://github.com/BRF-Tech/filex'

export default defineConfig({
  title: 'filex',
  titleTemplate: ':title · filex',
  description:
    'Self-hosted file manager — one Go binary, embeddable UI, S3 / SFTP / WebDAV / local storage.',
  lang: 'en-US',
  base: '/',
  srcDir: '../docs',
  // BRF-internal operational docs — kept in the repo, not published on the site.
  // ⚠ Excluded, not merely unlinked. A page VitePress builds is a page the
  // world can reach by URL and a search engine can index — leaving one out of
  // the sidebar hides it from readers who use the sidebar and from nobody else.
  //
  //   DEPLOY_BRF / MIGRATION_FISHAPP — one deployment's runbooks, with real
  //     hostnames and paths in them.
  //   CLOUD — its own first line says "preparation skeleton only, NOT a live
  //     service" and "do not launch a hosted cloud offering yet". Publishing it
  //     on the product's docs site announces a service that does not exist.
  //   handovers/** — ⚠⚠ working notes between maintainers, written on the
  //     assumption nobody outside reads them: private hostnames, internal
  //     board card numbers, named people and undecided questions. A GLOB, not
  //     a list, because the next handover must be excluded by default rather
  //     than by somebody remembering to add it. Measured: the v0.20.0 build
  //     was serving one at /handovers/2026-08-14-write-path-and-slow-storage.
  srcExclude: ['DEPLOY_BRF.md', 'MIGRATION.md', 'MIGRATION_FISHAPP.md', 'CLOUD.md', 'handovers/**'],
  // Example URLs in the docs (dev-server addresses). Real links stay checked.
  ignoreDeadLinks: [/^https?:\/\/localhost/],

  head: [
    ['link', { rel: 'icon', type: 'image/png', href: '/logo.png' }],
    ['meta', { name: 'theme-color', content: '#3b82f6' }],
    ['meta', { property: 'og:title', content: 'filex — self-hosted file manager' }],
    ['meta', { property: 'og:site_name', content: 'filex' }]
  ],

  themeConfig: {
    logo: '/logo.png',

    nav: [
      { text: 'Docs', link: '/INSTALLATION', activeMatch: '^/(?!$|RELEASES)' },
      { text: 'Releases', link: '/RELEASES', activeMatch: '^/RELEASES' },
      { text: 'Demo', link: 'https://demo.filex.sh' },
      { text: 'filex.sh', link: 'https://filex.sh' }
    ],

    sidebar: [
      {
        text: 'Getting Started',
        collapsed: false,
        items: [
          { text: 'Installation', link: '/INSTALLATION' },
          { text: 'Docker', link: '/DOCKER' },
          { text: 'Deployment', link: '/DEPLOYMENT' },
          { text: 'Configuration', link: '/CONFIGURATION' },
          { text: 'Updates', link: '/UPDATES' },
          { text: 'Releases', link: '/RELEASES' }
        ]
      },
      {
        // ⚠ Its own group rather than an item under Features: filex being
        // reachable AS S3/SFTP/FTPS/NFS is a second way to USE the product, not
        // a feature of the web UI, and somebody looking for "how do I point
        // rclone at this" does not read a Features list to find it.
        text: 'Without a browser',
        collapsed: false,
        items: [
          { text: 'Protocols — S3 · SFTP · FTPS · NFS', link: '/PROTOCOLS' },
          { text: 'WebDAV', link: '/WEBDAV' },
          { text: 'filex mount', link: '/PROTOCOLS#filex-mount' }
        ]
      },
      {
        text: 'Apps',
        collapsed: false,
        items: [
          { text: 'Desktop app', link: '/DESKTOP' },
          { text: 'Folder sync', link: '/SYNC' },
          { text: 'CLI', link: '/CLI' }
        ]
      },
      {
        text: 'Features',
        collapsed: false,
        items: [
          { text: 'Storage', link: '/STORAGE' },
          { text: 'Storage plugins', link: '/PLUGINS' },
          { text: 'Uploads & resume', link: '/UPLOADS' },
          { text: 'Quotas', link: '/QUOTAS' },
          { text: 'Search', link: '/SEARCH' },
          { text: 'Sharing & file requests', link: '/SHARING' },
          { text: 'Thumbnails', link: '/thumbnails' },
          { text: 'Protection & Antivirus', link: '/PROTECTION' },
          { text: 'Trash & Versioning', link: '/TRASH-VERSIONING' },
          { text: 'Notifications & Webhooks', link: '/NOTIFICATIONS' },
          { text: 'RBAC & Permissions', link: '/RBAC' },
          { text: 'End-to-end encryption', link: '/E2E-ENCRYPTION' },
          { text: 'Replication', link: '/REPLICATION' },
          { text: 'Multi-tenancy', link: '/MULTI-TENANCY' }
        ]
      },
      {
        text: 'Integration',
        collapsed: false,
        items: [
          { text: 'Embedding the explorer', link: '/INTEGRATION' },
          { text: 'MCP & AI Tokens', link: '/MCP' },
          { text: 'OnlyOffice', link: '/ONLYOFFICE' },
          { text: 'Convert Integration', link: '/CONVERT-INTEGRATION' },
          { text: 'ShareX', link: '/SHAREX' },
          { text: 'SSO (OIDC)', link: '/SSO' },
          { text: 'LDAP', link: '/LDAP' }
        ]
      },
      {
        text: 'Reference',
        collapsed: true,
        items: [
          { text: 'HTTP & component API', link: '/API' },
          { text: 'Metrics (Prometheus)', link: '/METRICS' },
          { text: 'Architecture', link: '/ARCHITECTURE' },
          { text: 'Backend internals', link: '/BACKEND' },
          { text: 'Contributing', link: '/CONTRIBUTING' }
        ]
      }
    ],

    socialLinks: [{ icon: 'github', link: REPO }],

    search: { provider: 'local' },

    outline: { level: [2, 3] },

    footer: {
      message: 'Released under the MIT License.',
      copyright: '© BRF Tech · filex.sh'
    }
  },

  markdown: {
    // ⚠⚠ Heading anchors are slugged GitHub's way, not VitePress's, because
    // every `docs/*.md` page is read on both surfaces and the in-page tables
    // of contents were written against GitHub. The rule, the measurement and
    // the reason are in `./github-slug.mjs`; `scripts/check-doc-anchors.mjs`
    // fails the build if any in-page link stops landing.
    anchor: { slugify: githubSlug },
    config(md) {
      // Repo-relative links in the docs (../deploy/…, ../demo, sharex/*.sxcu)
      // point at repository files that are not part of the site. Rewrite them
      // to the GitHub repo at render time so they keep working — and so the
      // dead-link check stays fully enabled for everything else.
      const fallback = (tokens: any, idx: number, options: any, _env: any, self: any) =>
        self.renderToken(tokens, idx, options)
      const prev = md.renderer.rules.link_open || fallback
      md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
        const token = tokens[idx]
        const href = token.attrGet('href')
        if (href) {
          // SEARCH.md links to QUEUE.md, which never existed — the queue is
          // documented in CONFIGURATION.md#queue. Remap at render time
          // (the markdown source is left untouched).
          if (/^(\.\/)?QUEUE\.md$/.test(href)) {
            token.attrSet('href', './CONFIGURATION.md#queue')
            return prev(tokens, idx, options, env, self)
          }
          let target: string | null = null
          if (/^(\.\.\/)+/.test(href)) {
            target = href.replace(/^(\.\.\/)+/, '')
          } else if (/^(\.\/)?sharex(\/|$)/.test(href)) {
            target = 'docs/' + href.replace(/^\.\//, '')
          }
          if (target) {
            const last = target.replace(/\/$/, '').split('/').pop() || ''
            const kind = /\.[a-z0-9]+$/i.test(last) ? 'blob' : 'tree'
            token.attrSet('href', `${REPO}/${kind}/main/${target.replace(/\/$/, '')}`)
          }
        }
        return prev(tokens, idx, options, env, self)
      }
    }
  },

  vite: {
    resolve: {
      alias: [
        { find: /^vue$/, replacement: resolve(vueDir, 'dist/vue.runtime.esm-bundler.js') },
        { find: /^vue\/server-renderer$/, replacement: resolve(vueDir, 'server-renderer/index.mjs') }
      ]
    },
    plugins: [
      {
        name: 'filex-docs-logo-dev',
        configureServer(server) {
          // Serve the shared logo from docs/ during `vitepress dev`
          // (at build time `buildEnd` copies it into the dist root).
          server.middlewares.use('/logo.png', (_req, res) => {
            res.setHeader('Content-Type', 'image/png')
            fs.createReadStream(resolve(docsDir, 'logo.png')).pipe(res)
          })
        }
      }
    ]
  },

  buildEnd(siteConfig) {
    fs.copyFileSync(resolve(docsDir, 'logo.png'), resolve(siteConfig.outDir, 'logo.png'))
  }
})
