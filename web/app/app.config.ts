export default defineAppConfig({
  ui: {
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
