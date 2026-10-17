// Resumable chunked uploads from the browser. Files of any size are sent in
// 16 MB pieces; after a network error the upload continues where it stopped.

import { ApiError, qs } from './api'

export type UploadStatus = 'queued' | 'uploading' | 'done' | 'exists' | 'error' | 'canceled'

export interface UploadTarget {
  /** e.g. "/api/v1/uploads" or "/api/v1/public/<token>/uploads" */
  endpoint: string
  space?: string
  folder: string
  vaultParent?: number
}

export class UploadItem {
  id = Math.random().toString(36).slice(2)
  file: File
  relPath: string
  target: UploadTarget
  status = $state<UploadStatus>('queued')
  loaded = $state(0)
  error = $state('')
  speed = $state(0)
  finalPath = $state('')
  uploadId = ''
  xhr: XMLHttpRequest | null = null

  constructor(file: File, relPath: string, target: UploadTarget) {
    this.file = file
    this.relPath = relPath
    this.target = target
  }

  get name() {
    return this.relPath.split('/').pop() || this.file.name
  }
  get size() {
    return this.file.size
  }
  get percent() {
    return this.size ? Math.round((this.loaded / this.size) * 100) : 100
  }
}

const CHUNK = 16 * 1024 * 1024
const PARALLEL = 3

async function json(method: string, url: string, body?: unknown): Promise<any> {
  const res = await fetch(url, {
    method,
    headers: { 'X-HD': '1', ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
    credentials: 'same-origin',
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, data.code || 'error', data.error || res.statusText, data.offset)
  return data
}

function sendChunk(item: UploadItem, url: string, offset: number, blob: Blob, onProgress: (n: number) => void): Promise<any> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    item.xhr = xhr
    xhr.open('PATCH', url)
    xhr.setRequestHeader('X-HD', '1')
    xhr.setRequestHeader('Upload-Offset', String(offset))
    xhr.setRequestHeader('Content-Type', 'application/offset+octet-stream')
    xhr.upload.onprogress = (e) => onProgress(e.loaded)
    xhr.onload = () => {
      let data: any = {}
      try {
        data = JSON.parse(xhr.responseText)
      } catch {
        /* ignore */
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data)
      else reject(new ApiError(xhr.status, data.code || 'error', data.error || xhr.statusText, data.offset))
    }
    xhr.onerror = () => reject(new ApiError(0, 'network', 'network error'))
    xhr.onabort = () => reject(new ApiError(0, 'aborted', 'aborted'))
    xhr.send(blob)
  })
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

class Uploader {
  items = $state<UploadItem[]>([])
  private running = 0
  onFinished: ((item: UploadItem) => void) | null = null

  get active() {
    return this.items.filter((i) => i.status === 'queued' || i.status === 'uploading').length
  }
  get totalBytes() {
    return this.items.reduce((s, i) => s + i.size, 0)
  }
  get loadedBytes() {
    return this.items.reduce((s, i) => s + (i.status === 'done' || i.status === 'exists' ? i.size : i.loaded), 0)
  }

  add(files: { file: File; relPath?: string }[], target: UploadTarget) {
    for (const f of files) {
      this.items.push(new UploadItem(f.file, f.relPath || f.file.name, target))
    }
    this.pump()
  }

  cancel(item: UploadItem) {
    item.status = 'canceled'
    item.xhr?.abort()
    if (item.uploadId) {
      fetch(item.target.endpoint + '/' + item.uploadId, { method: 'DELETE', headers: { 'X-HD': '1' } }).catch(() => {})
    }
  }

  retry(item: UploadItem) {
    if (item.status === 'error' || item.status === 'canceled') {
      item.status = 'queued'
      item.error = ''
      this.pump()
    }
  }

  clearFinished() {
    this.items = this.items.filter((i) => i.status === 'queued' || i.status === 'uploading')
  }

  cancelAll() {
    for (const i of this.items) if (i.status === 'queued' || i.status === 'uploading') this.cancel(i)
  }

  private pump() {
    while (this.running < PARALLEL) {
      const next = this.items.find((i) => i.status === 'queued')
      if (!next) return
      this.running++
      next.status = 'uploading'
      this.run(next).finally(() => {
        this.running--
        this.pump()
      })
    }
  }

  private async run(item: UploadItem): Promise<void> {
    const t = item.target
    try {
      if (!item.uploadId) {
        const rel = t.folder ? t.folder.replace(/\/+$/, '') + '/' + item.relPath : item.relPath
        const body: any = { path: rel, size: item.size, mtime: item.file.lastModified, conflict: 'rename' }
        if (t.space) body.space = t.space
        if (t.vaultParent !== undefined) body.vault_parent = t.vaultParent
        const st = await json('POST', t.endpoint, body)
        if (st.status !== 'pending') {
          item.loaded = item.size
          item.finalPath = st.path || ''
          item.status = st.status === 'exists' ? 'exists' : 'done'
          this.onFinished?.(item)
          return
        }
        item.uploadId = st.id
      }
      const url = t.endpoint + '/' + item.uploadId
      let offset = item.loaded
      let failures = 0
      while (offset < item.size) {
        if ((item.status as UploadStatus) === 'canceled') return
        const end = Math.min(offset + CHUNK, item.size)
        const started = performance.now()
        try {
          const st = await sendChunk(item, url, offset, item.file.slice(offset, end), (n) => {
            item.loaded = offset + n
            const secs = (performance.now() - started) / 1000
            if (secs > 0.5) item.speed = n / secs
          })
          failures = 0
          offset = st.offset ?? end
          item.loaded = offset
          if (st.status === 'done') {
            item.finalPath = st.path || ''
            item.status = 'done'
            this.onFinished?.(item)
            return
          }
        } catch (e) {
          const err = e as ApiError
          if ((item.status as UploadStatus) === 'canceled') return
          if (err.code === 'offset_mismatch' && err.offset !== undefined) {
            offset = err.offset
            continue
          }
          if (err.status >= 400 && err.status < 500 && err.status !== 408 && err.status !== 429) throw err
          if (++failures > 20) throw err
          // Network trouble: wait, ask the server how much it has, continue.
          await sleep(Math.min(30000, 1000 * 2 ** Math.min(failures, 5)))
          try {
            const st = await json('GET', url)
            offset = st.offset
            item.loaded = offset
          } catch (e2) {
            if ((e2 as ApiError).status === 404) {
              item.uploadId = ''
              item.loaded = 0
              return this.run(item)
            }
          }
        }
      }
      item.status = 'done'
      this.onFinished?.(item)
    } catch (e) {
      if ((item.status as UploadStatus) === 'canceled') return
      item.status = 'error'
      item.error = (e as Error).message
    }
  }
}

export const uploader = new Uploader()

/** Reads files (and folders) from a drag & drop event. */
export async function filesFromDrop(dt: DataTransfer): Promise<{ file: File; relPath: string }[]> {
  const out: { file: File; relPath: string }[] = []
  const entries: any[] = []
  for (const item of Array.from(dt.items)) {
    const entry = (item as any).webkitGetAsEntry?.()
    if (entry) entries.push(entry)
    else if (item.kind === 'file') {
      const f = item.getAsFile()
      if (f) out.push({ file: f, relPath: f.name })
    }
  }
  const walk = async (entry: any, prefix: string): Promise<void> => {
    if (entry.isFile) {
      const file: File = await new Promise((res, rej) => entry.file(res, rej))
      out.push({ file, relPath: prefix + file.name })
    } else if (entry.isDirectory) {
      const reader = entry.createReader()
      let batch: any[]
      do {
        batch = await new Promise((res, rej) => reader.readEntries(res, rej))
        for (const child of batch) await walk(child, prefix + entry.name + '/')
      } while (batch.length > 0)
    }
  }
  for (const e of entries) await walk(e, '')
  return out
}

/** Converts an <input type=file webkitdirectory> selection. */
export function filesFromInput(list: FileList | null): { file: File; relPath: string }[] {
  if (!list) return []
  return Array.from(list).map((f) => ({ file: f, relPath: (f as any).webkitRelativePath || f.name }))
}

export function uploadEndpoint(token?: string): string {
  return token ? `/api/v1/public/${token}/uploads` : '/api/v1/uploads'
}

export { qs }
