// Nuxt configuration for the Conductor workbench. The app is a client-only
// SPA generated into internal/web/dist and embedded in the Go binary.
export default defineNuxtConfig({
  compatibilityDate: '2026-09-01',
  ssr: false,
  modules: ['@nuxt/ui'],
  css: ['~/assets/css/main.css'],
  // The workbench never fetches icons at runtime: every icon it shows is
  // bundled from @iconify-json/lucide. The scan finds the names in the app's
  // sources (.ts too: event icons are named in app/utils); the built-in
  // catalog's icons come from the server (internal/catalog/defaults.go), so
  // they are listed. A catalog icon outside the bundle shows nothing.
  icon: {
    provider: 'none',
    clientBundle: {
      scan: { globInclude: ['app/**/*.{vue,ts}'] },
      icons: [
        'lucide:sparkles',
        'lucide:code-xml',
        'lucide:rocket',
        'lucide:github',
        'lucide:mouse-pointer-2',
        'lucide:braces',
        'lucide:pi',
        'lucide:pi-square',
        'lucide:git-commit',
        'lucide:feather',
        'lucide:zap',
        'lucide:cpu',
        'lucide:terminal',
      ],
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
