// Nuxt configuration for the Conductor workbench. The app is a client-only
// SPA generated into internal/web/dist and embedded in the Go binary.
export default defineNuxtConfig({
  compatibilityDate: '2026-09-01',
  ssr: false,
  modules: ['@nuxt/ui'],
  css: ['~/assets/css/main.css'],
  devtools: { enabled: false },
  app: {
    head: {
      title: 'Conductor',
      meta: [{ name: 'referrer', content: 'no-referrer' }],
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/brand/conductor-favicon.svg' }],
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
