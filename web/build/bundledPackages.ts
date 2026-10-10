import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, relative, resolve, sep } from 'node:path'
import type { Plugin } from 'vite'

/**
 * packageDir is the directory of the npm package a module or asset of the bundle comes from (`…/node_modules/@scope/name`, the
 * innermost one), or null for the app's own files and virtual modules.
 */
export function packageDir(id: string): string | null {
  const clean = id.replace(/^\0/, '').replace(/[?#].*$/, '').split('\\').join('/')
  const at = clean.lastIndexOf('/node_modules/')
  if (at < 0) return null
  const rest = clean.slice(at + '/node_modules/'.length).split('/')
  const n = rest[0]?.startsWith('@') ? 2 : 1
  if (rest.length < n || rest.slice(0, n).some((p) => !p || p === '.vite')) return null
  return clean.slice(0, at) + '/node_modules/' + rest.slice(0, n).join('/')
}

/**
 * bundledPackages writes the npm packages whose code or assets the client bundle carries, as package directories relative to root,
 * sorted, to out: scripts/notices.py reads it for THIRD_PARTY_NOTICES, so the notices list what ships and not what builds it.
 */
export function bundledPackages(root: string, out: string): Plugin {
  const dirs = new Set<string>()
  // Module ids are absolute; an asset's original names are relative to Vite's root (the app's source directory).
  let viteRoot = root
  const add = (id: string) => {
    const d = packageDir(id.startsWith('\0') ? id : resolve(viteRoot, id))
    if (d) dirs.add(relative(root, d).split(sep).join('/'))
  }
  return {
    name: 'conductor:bundled-packages',
    apply: 'build',
    applyToEnvironment: (env) => env.name === 'client',
    configResolved(config) {
      viteRoot = config.root
    },
    generateBundle(_, bundle) {
      for (const file of Object.values(bundle)) {
        if (file.type === 'chunk') Object.keys(file.modules).forEach(add)
        else file.originalFileNames.forEach(add)
      }
    },
    writeBundle() {
      mkdirSync(dirname(out), { recursive: true })
      writeFileSync(out, JSON.stringify([...dirs].sort(), null, 2) + '\n')
    },
  }
}
