import { mkdtempSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { bundledPackages, moduleFile, packageDir } from './bundledPackages'

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

describe('moduleFile', () => {
  it('is the package file a module was read from, its query dropped', () => {
    expect(moduleFile('/src/web/node_modules/@xterm/xterm/lib/xterm.mjs')).toBe('/src/web/node_modules/@xterm/xterm/lib/xterm.mjs')
    expect(moduleFile('/src/web/node_modules/vue/dist/vue.js?v=3')).toBe('/src/web/node_modules/vue/dist/vue.js')
    expect(moduleFile('C:\\src\\web\\node_modules\\shiki\\dist\\index.mjs')).toBe('C:/src/web/node_modules/shiki/dist/index.mjs')
  })

  it('is nothing for a virtual module or a file of the app itself', () => {
    expect(moduleFile('\0/src/web/node_modules/nuxt/dist/app/entry.js')).toBeNull()
    expect(moduleFile('/src/web/app/components/TerminalView.vue')).toBeNull()
  })
})

describe('bundledPackages', () => {
  it("writes what a worker's build carried with the client's", () => {
    const root = mkdtempSync(join(tmpdir(), 'bundled-'))
    const out = join(root, '.nuxt/bundled-packages.json')
    const { client, worker } = bundledPackages(root, out)
    const call = (hook: unknown, ...args: unknown[]) => (hook as (...a: unknown[]) => void).call({}, ...args)
    call(client.configResolved, { root: join(root, 'app') })
    // A Monaco worker is bundled by a build of its own, before the client's bundle is written.
    call(worker.generateBundle, {}, { 'json.worker.js': { type: 'chunk', modules: { [join(root, 'node_modules/monaco-editor/esm/external/glob.js')]: {} } } })
    call(client.generateBundle, {}, {
      'entry.js': { type: 'chunk', modules: { [join(root, 'node_modules/@xterm/xterm/lib/xterm.mjs')]: {}, '\0virtual:x': {} } },
      'font.woff2': { type: 'asset', originalFileNames: ['../node_modules/@fontsource/inter/inter.woff2'] },
    })
    call(client.writeBundle)
    expect(JSON.parse(readFileSync(out, 'utf8'))).toEqual({
      packages: ['node_modules/@fontsource/inter', 'node_modules/@xterm/xterm', 'node_modules/monaco-editor'],
      modules: ['node_modules/@xterm/xterm/lib/xterm.mjs', 'node_modules/monaco-editor/esm/external/glob.js'],
    })
  })
})
