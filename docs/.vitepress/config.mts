import path from 'node:path'
import { defineConfig } from 'vitepress'
import { withMermaid } from 'vitepress-plugin-mermaid'

// GitHub Pages serves the site under /<repo>/; CI sets DOCS_BASE accordingly.
const srcDir = path.resolve(import.meta.dirname, '../../docs')

// Source files keep GitHub-friendly names; the site gets clean URLs.
const rewrites: Record<string, string> = {
  'README.md': 'guide/index.md',
  '01_overview.md': 'guide/overview.md',
  '02_architecture.md': 'guide/architecture.md',
  '03_getting_started.md': 'guide/getting-started.md',
  '07_screenshots.md': 'guide/screenshots.md',
  '08_brand.md': 'guide/brand.md',
  '09_node_red.md': 'guide/node-red.md',
  '04_api_reference/README.md': 'api/index.md',
  '04_api_reference/:page': 'api/:page',
  '05_core_concepts/README.md': 'concepts/index.md',
  '05_core_concepts/:page': 'concepts/:page',
  '06_database/:page': 'database/:page',
  'refactor/GO_REDPANDA_TSDB_PLAN.md': 'design/refactor-plan.md',
  'refactor/MESSAGE_FORMATS.md': 'design/message-formats.md',
  'refactor/baseline.md': 'design/performance-baseline.md',
  'refactor/PLATFORM_REVIEW.md': 'design/platform-review.md',
}

/** Site URL (without base) for a source file path relative to docs/. */
function siteUrl(source: string): string | undefined {
  let out = rewrites[source]
  if (!out) {
    for (const [from, to] of Object.entries(rewrites)) {
      if (!from.endsWith(':page')) continue
      const dir = from.slice(0, -':page'.length)
      if (source.startsWith(dir) && !source.slice(dir.length).includes('/')) {
        out = to.replace(':page', source.slice(dir.length))
        break
      }
    }
  }
  out ??= source
  return '/' + out.replace(/(^|\/)index\.md$/, '$1').replace(/\.md$/, '')
}
const base = process.env.DOCS_BASE ?? '/'
const repo = 'https://github.com/taha2samy-3/qauk-qauk'

export default withMermaid(
  defineConfig({
    title: 'Quack Quack',
    description: 'Real-time IoT dashboards, device control and history. Go, WebSockets, Redpanda, TimescaleDB.',
    lang: 'en-US',
    base,
    srcDir: '../docs',
    cleanUrls: true,
    lastUpdated: true,
    // Source files keep GitHub-friendly names; the site gets clean URLs.
    rewrites,
    markdown: {
      // Keep GitHub-friendly relative links in the sources (./01_overview.md)
      // and point them at the clean site URLs when rendering.
      config(md) {
        md.core.ruler.push('quack-rewrite-links', (state) => {
          const from = (state.env?.realPath ?? state.env?.path) as string | undefined
          if (!from) return
          for (const block of state.tokens) {
            for (const tok of block.children ?? []) {
              if (tok.type !== 'link_open') continue
              const href = tok.attrGet('href')
              if (!href || /^([a-z]+:|#|\/)/i.test(href)) continue
              const [file, hash] = href.split('#')
              if (!file.endsWith('.md')) continue
              const target = path.relative(srcDir, path.resolve(path.dirname(from), file)).split(path.sep).join('/')
              const url = siteUrl(target)
              if (url) tok.attrSet('href', url + (hash ? `#${hash}` : ''))
            }
          }
        })
      },
    },
    head: [
      ['link', { rel: 'icon', type: 'image/svg+xml', href: `${base}logo-mark.svg` }],
      ['link', { rel: 'apple-touch-icon', href: `${base}apple-touch-icon.png` }],
      ['meta', { name: 'theme-color', content: '#4F46E5' }],
      ['meta', { property: 'og:type', content: 'website' }],
      ['meta', { property: 'og:title', content: 'Quack Quack' }],
      ['meta', { property: 'og:description', content: 'Real-time IoT dashboards, device control and history.' }],
      ['meta', { property: 'og:image', content: `${base}social-card.png` }],
      ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ],
    themeConfig: {
      logo: '/logo-mark.svg',
      siteTitle: 'Quack Quack',
      nav: [
        { text: 'Guide', link: '/guide/getting-started', activeMatch: '/guide/' },
        { text: 'Concepts', link: '/concepts/', activeMatch: '/concepts/' },
        { text: 'API', link: '/api/', activeMatch: '/api/' },
        { text: 'Design', link: '/design/refactor-plan', activeMatch: '/design/' },
      ],
      sidebar: [
        {
          text: 'Guide',
          items: [
            { text: 'Introduction', link: '/guide/' },
            { text: 'Overview', link: '/guide/overview' },
            { text: 'Getting started', link: '/guide/getting-started' },
            { text: 'Node-RED integration', link: '/guide/node-red' },
            { text: 'Architecture', link: '/guide/architecture' },
            { text: 'Screenshots', link: '/guide/screenshots' },
            { text: 'Brand & logos', link: '/guide/brand' },
          ],
        },
        {
          text: 'Core concepts',
          items: [
            { text: 'Concepts', link: '/concepts/' },
            { text: 'Authentication', link: '/concepts/authentication' },
            { text: 'Permissions', link: '/concepts/permissions' },
            { text: 'Realtime events', link: '/concepts/realtime_events' },
            { text: 'History storage', link: '/concepts/history' },
            { text: 'Dashboards', link: '/concepts/dashboards' },
          ],
        },
        {
          text: 'API reference',
          items: [
            { text: 'Overview', link: '/api/' },
            { text: 'Device WebSocket', link: '/api/device_api' },
            { text: 'Browser WebSocket', link: '/api/browser_api' },
            { text: 'REST', link: '/api/rest_api' },
          ],
        },
        { text: 'Database', items: [{ text: 'Schema', link: '/database/schema' }] },
        {
          text: 'Design notes',
          collapsed: true,
          items: [
            { text: 'Refactor plan', link: '/design/refactor-plan' },
            { text: 'Message formats', link: '/design/message-formats' },
            { text: 'Performance baseline', link: '/design/performance-baseline' },
            { text: 'Platform review & roadmap', link: '/design/platform-review' },
          ],
        },
      ],
      search: { provider: 'local' },
      socialLinks: [{ icon: 'github', link: repo }],
      editLink: { pattern: `${repo}/edit/main/docs/:path`, text: 'Edit this page on GitHub' },
      outline: { level: [2, 3] },
      footer: {
        message: 'Released under the MIT License.',
        copyright: 'Quack Quack, real-time IoT platform',
      },
    },
    // Mermaid measures labels at render time; use a system font that is always
    // available (not the lazily loaded web font) so labels never get clipped.
    mermaid: {
      theme: 'default',
      fontFamily: 'ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, Arial, sans-serif',
      themeVariables: { fontFamily: 'ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, Arial, sans-serif' },
      flowchart: { padding: 14, htmlLabels: true },
    },
    vite: {
      // srcDir lives outside this project folder
      server: { fs: { allow: ['..'] } },
    },
  }),
)
