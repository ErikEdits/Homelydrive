// Small fetch wrapper for the Homelydrive API. The web UI authenticates with
// an HttpOnly cookie; every request carries the X-HD header the server
// requires for cookie-based changes (CSRF protection).

export class ApiError extends Error {
  status: number
  code: string
  offset?: number
  constructor(status: number, code: string, message: string, offset?: number) {
    super(message)
    this.status = status
    this.code = code
    this.offset = offset
  }
}

type Params = Record<string, string | number | boolean | undefined | null | string[]>

export function qs(params: Params = {}): string {
  const u = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue
    if (Array.isArray(v)) v.forEach((x) => u.append(k, x))
    else u.set(k, String(v))
  }
  const s = u.toString()
  return s ? '?' + s : ''
}

let onUnauthorized: (() => void) | null = null

/** Called when the session expired or the user was logged out elsewhere. */
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export async function api<T = any>(method: string, path: string, body?: unknown, params?: Params): Promise<T> {
  const headers: Record<string, string> = { 'X-HD': '1' }
  let payload: BodyInit | undefined
  if (body instanceof Blob) {
    headers['Content-Type'] = body.type || 'application/octet-stream'
    payload = body
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  let res: Response
  try {
    res = await fetch('/api/v1' + path + qs(params), { method, headers, body: payload, credentials: 'same-origin' })
  } catch (e) {
    throw new ApiError(0, 'network', 'Server nicht erreichbar')
  }
  if (!res.ok) {
    let code = 'error'
    let message = res.statusText
    let offset: number | undefined
    try {
      const j = await res.json()
      code = j.code || code
      message = j.error || message
      offset = j.offset
    } catch {
      /* not JSON */
    }
    if (res.status === 401 && code === 'unauthorized' && onUnauthorized && !path.startsWith('/auth/') && !path.startsWith('/admin/')) {
      onUnauthorized()
    }
    throw new ApiError(res.status, code, message, offset)
  }
  if (res.status === 204) return undefined as T
  const ct = res.headers.get('Content-Type') || ''
  if (ct.includes('application/json')) return (await res.json()) as T
  return (await res.text()) as unknown as T
}

export const get = <T = any>(path: string, params?: Params) => api<T>('GET', path, undefined, params)
export const post = <T = any>(path: string, body?: unknown) => api<T>('POST', path, body ?? {})
export const put = <T = any>(path: string, body?: unknown) => api<T>('PUT', path, body ?? {})
export const del = <T = any>(path: string, params?: Params) => api<T>('DELETE', path, undefined, params)

export function contentUrl(space: string | undefined, path: string, download = false): string {
  return '/api/v1/files/content' + qs({ space: space || 'home', path, download: download ? 1 : undefined })
}

export function thumbUrl(space: string | undefined, path: string, size = 256, version?: number): string {
  return '/api/v1/files/thumb' + qs({ space: space || 'home', path, size, v: version })
}

export function zipUrl(space: string | undefined, paths: string[], name?: string): string {
  return '/api/v1/files/zip' + qs({ space: space || 'home', path: paths, name })
}

/** Triggers a browser download of a URL. */
export function download(url: string) {
  const a = document.createElement('a')
  a.href = url
  a.download = ''
  document.body.appendChild(a)
  a.click()
  a.remove()
}

/** Downloads many items as one ZIP via a POST form (no URL length limit). */
export function downloadZipForm(space: string | undefined, paths: string[], name?: string) {
  const form = document.createElement('form')
  form.method = 'POST'
  form.action = '/api/v1/files/zip'
  const add = (k: string, v: string) => {
    const i = document.createElement('input')
    i.type = 'hidden'
    i.name = k
    i.value = v
    form.appendChild(i)
  }
  add('space', space || 'home')
  paths.forEach((p) => add('path', p))
  if (name) add('name', name)
  document.body.appendChild(form)
  form.submit()
  form.remove()
}
