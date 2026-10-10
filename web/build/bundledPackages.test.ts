import { describe, expect, it } from 'vitest'
import { packageDir } from './bundledPackages'

describe('packageDir', () => {
  it('is the innermost package a module or asset comes from', () => {
    expect(packageDir('/src/web/node_modules/vue/dist/vue.runtime.esm-bundler.js')).toBe('/src/web/node_modules/vue')
    expect(packageDir('/src/web/node_modules/@xterm/xterm/lib/xterm.mjs')).toBe('/src/web/node_modules/@xterm/xterm')
    expect(packageDir('/src/web/node_modules/a/node_modules/@b/c/index.js?v=1')).toBe('/src/web/node_modules/a/node_modules/@b/c')
    expect(packageDir('\0/src/web/node_modules/nuxt/dist/app/entry.js')).toBe('/src/web/node_modules/nuxt')
    expect(packageDir('C:\\src\\web\\node_modules\\shiki\\dist\\index.mjs')).toBe('C:/src/web/node_modules/shiki')
  })

  it('is nothing for the app itself, virtual modules and what is not a package', () => {
    expect(packageDir('/src/web/app/components/TerminalView.vue')).toBeNull()
    expect(packageDir('\0virtual:nuxt:/src/web/.nuxt/nuxt-icon-client-bundle')).toBeNull()
    expect(packageDir('/src/web/node_modules/.vite/deps/chunk.js')).toBeNull()
    expect(packageDir('/src/web/node_modules/@scope')).toBeNull()
  })
})
