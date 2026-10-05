/** The folder picker's path arithmetic, on the server's POSIX paths. */

/** The directory above `path`: '/a/b' → '/a', '/a' → '/', '/' → '/'. */
export function parentDir(path: string): string {
  const p = path.replace(/\/+$/, '')
  const at = p.lastIndexOf('/')
  return at <= 0 ? '/' : p.slice(0, at)
}

/** The breadcrumb of a directory: '/' first, then each directory down to it. */
export function crumbs(path: string): Array<{ name: string; path: string }> {
  const out = [{ name: '/', path: '/' }]
  let acc = ''
  for (const part of path.split('/').filter(Boolean)) {
    acc += `/${part}`
    out.push({ name: part, path: acc })
  }
  return out
}

/** Whether `path` is an allowed root or lies under one: the desktop settings' own rule. */
export function underRoots(path: string, roots: readonly string[]): boolean {
  const p = path.replace(/\/+$/, '') || '/'
  return roots.some((r) => {
    const root = r.replace(/\/+$/, '') || '/'
    return p === root || p.startsWith(root === '/' ? '/' : `${root}/`)
  })
}

export type PickerField = 'root' | 'defaultCwd' | 'dataDir'

/** Where the picker opens for a field: its own value; a new root opens on the first root, else the default directory. */
export function pickerStart(field: PickerField, form: { allowedRoots: readonly string[]; defaultCwd: string; dataDir: string }): string {
  if (field === 'root') return form.allowedRoots[0] || form.defaultCwd
  return form[field]
}
