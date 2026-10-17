// Calls to the local PC app (served on 127.0.0.1).
export class LocalError extends Error {}

export async function local<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch('/local' + path, {
    method,
    headers: { 'X-HD': '1', ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: 'same-origin',
  })
  const j = await res.json().catch(() => ({}))
  if (!res.ok) throw new LocalError(j.error || res.statusText)
  return j as T
}

export interface Folder {
  id: string
  local: string
  space: string
  remote: string
  enabled: boolean
  files: number
  bytes: number
  error?: string
}

export interface Active {
  name: string
  local: string
  remote: string
  size: number
  done: number
  speed: number
  phase: string
}

export interface Status {
  version: string
  os: string
  server: string
  username: string
  user?: { name: string; quota: number; used: number; is_admin: boolean }
  connected: boolean
  engine: {
    logged_in: boolean
    online: boolean
    paused: boolean
    queue: number
    active: Active[]
    last_error?: string
    uploaded: number
    uploaded_bytes: number
    scanning: boolean
  }
  config: {
    server: string
    username: string
    device_name: string
    folders: Folder[]
    bandwidth_kbps: number
    paused: boolean
    notifications: boolean
    drive_enabled: boolean
    drive_mount: string
    drive_cache_mb: number
    upload_folder: string
    download_dir: string
    language: 'de' | 'en' | ''
    update_check: boolean
    integration: boolean
  }
  drive_supported: boolean
  drive_mounted: boolean
  drive_error?: string
  cache_size: number
  update?: { version: string; url: string }
}

export interface HistoryEntry {
  id: number
  time: number
  kind: string
  local: string
  remote: string
  size: number
  status: string
  error?: string
}
