export default defineAppConfig({
  ui: {
    // Below lg the bottom bar sits under the layout group's padding; a panel at least a screen tall (the theme's
    // min-h-svh) would run under it and clip what sits at its foot (a phone's terminal bar, the reply bar).
    dashboardPanel: { slots: { root: 'max-lg:min-h-0' } },
    // Brand palette from docs/design/brand.md: forest for primary actions,
    // terracotta only as the restrained accent (never a status light).
    // Operational status uses its own scales: amber needs-input, green running,
    // and the brand's info and error tokens (web/public/brand/tokens.css).
    colors: {
      primary: 'forest',
      secondary: 'terracotta',
      neutral: 'zinc',
      warning: 'signal',
      success: 'active',
      info: 'harbor',
      error: 'brick',
    },
  },
})
