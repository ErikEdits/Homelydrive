// Hash based routing: #/drive/<space>/<path>, #/recent, #/search?q=...

export interface Route {
  name: string
  ref: string
  path: string
  id: number
  query: Record<string, string>
}

function parse(): Route {
  const raw = location.hash.replace(/^#/, '') || '/drive/home/'
  const [p, q = ''] = raw.split('?')
  const parts = p.split('/').filter(Boolean).map((s) => {
    try {
      return decodeURIComponent(s)
    } catch {
      return s
    }
  })
  const query: Record<string, string> = {}
  new URLSearchParams(q).forEach((v, k) => (query[k] = v))
  const name = parts[0] || 'drive'
  if (name === 'drive') {
    return { name, ref: parts[1] || 'home', path: parts.slice(2).join('/'), id: 0, query }
  }
  return { name, ref: '', path: parts.slice(1).join('/'), id: Number(parts[1]) || 0, query }
}

export const route = $state<Route>(parse())

window.addEventListener('hashchange', () => {
  Object.assign(route, parse())
})

export function go(hash: string) {
  if (location.hash === hash) Object.assign(route, parse())
  else location.hash = hash
}

export function driveHash(ref: string, path = ''): string {
  const segs = path.split('/').filter(Boolean).map(encodeURIComponent).join('/')
  return '#/drive/' + encodeURIComponent(ref || 'home') + '/' + segs
}

export function openFolder(ref: string, path: string) {
  go(driveHash(ref, path))
}
