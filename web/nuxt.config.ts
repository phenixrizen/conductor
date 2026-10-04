import { AGENT_ICONS } from './app/utils/agentIcons'

// Nuxt configuration for the Conductor workbench. The app is a client-only
// SPA generated into internal/web/dist and embedded in the Go binary.
export default defineNuxtConfig({
  compatibilityDate: '2026-09-01',
  ssr: false,
  modules: ['@nuxt/ui', 'nuxt-charts'],
  css: ['~/assets/css/main.css'],
  // Dark is the theme, whatever the OS prefers; the sidebar's theme button
  // switches to light and that choice is kept (under a key of our own, so a
  // "light" an earlier build saved while following the OS does not carry over).
  colorMode: { preference: 'dark', fallback: 'dark', storageKey: 'conductor-color-mode' },
  // The workbench never fetches icons at runtime: every icon it shows is
  // bundled from @iconify-json/lucide. The scan finds the names in the app's
  // sources (.ts too: event icons are named in app/utils); the built-in
  // catalog's icons come from the server (internal/catalog/defaults.go), so
  // they are listed, in app/utils/agentIcons.ts. A catalog icon outside that
  // list shows the generic agent icon (agentIcon).
  icon: {
    provider: 'none',
    clientBundle: {
      scan: { globInclude: ['app/**/*.{vue,ts}'] },
      icons: AGENT_ICONS.map((name) => name.replace(/^i-lucide-/, 'lucide:')),
      sizeLimitKb: 256,
    },
  },
  devtools: { enabled: false },
  app: {
    head: {
      title: 'Conductor',
      meta: [{ name: 'referrer', content: 'no-referrer' }],
      link: [{ key: 'icon', rel: 'icon', type: 'image/svg+xml', href: '/brand/conductor-favicon.svg' }],
    },
  },
  runtimeConfig: {
    public: {
      // Empty means same origin (the embedded UI). Set NUXT_PUBLIC_API_BASE
      // to http://localhost:8080 when the dev proxy cannot upgrade WebSockets.
      apiBase: '',
    },
  },
  nitro: {
    // Output stays in web/.output/public; `make web-build` copies it into
    // internal/web/dist so Nuxt's own cleanup never empties the embed directory.
    devProxy: {
      '/api': { target: 'http://127.0.0.1:8080/api', changeOrigin: true },
      '/ws': { target: 'ws://127.0.0.1:8080/ws', ws: true, changeOrigin: true },
    },
  },
  vite: {
    optimizeDeps: { include: ['@xterm/xterm', '@xterm/addon-fit', '@xterm/addon-webgl', '@xterm/addon-web-links'] },
  },
  typescript: { strict: true, typeCheck: false },
})
