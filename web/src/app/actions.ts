// File actions shared by all views (drive, recent, starred, search, ...).
import { contentUrl, download, downloadZipForm, post, put, thumbUrl } from '../lib/api'
import { errText, t } from '../lib/i18n.svelte'
import { confirmDialog, promptDialog, toast } from '../lib/ui/ui.svelte'
import type { FileInfo } from '../lib/types'
import { app, refreshQuota, type ViewerItem } from './state.svelte'
import { openFolder } from './router.svelte'
import { joinPath, parentOf } from '../lib/format'

export const refOf = (f: FileInfo) => f.space || 'home'

async function run<T>(fn: () => Promise<T>, ok?: string): Promise<T | undefined> {
  try {
    const r = await fn()
    if (ok) toast(ok, 'success')
    return r
  } catch (e) {
    toast(errText(e), 'error')
    return undefined
  }
}

export async function newFolder(space: string, folder: string) {
  const name = await promptDialog(t('action.newFolder'), t('action.newFolderDefault'), { okLabel: t('common.create') })
  if (!name) return
  await run(() => post('/files/mkdir', { space, path: joinPath(folder, name) }))
}

export async function newTextFile(space: string, folder: string, extension = 'txt') {
  const name = await promptDialog(t('action.newTextFile'), t('action.newTextFileDefault') + '.' + extension, { okLabel: t('common.create') })
  if (!name) return
  const path = joinPath(folder, name)
  const res = await fetch(contentUrl(space, path).replace('&download=1', ''), {
    method: 'PUT',
    headers: { 'X-HD': '1', 'Content-Type': 'text/plain' },
    body: '',
  })
  if (!res.ok) {
    const j = await res.json().catch(() => ({}))
    toast(j.error || res.statusText, 'error')
    return
  }
  const fi: FileInfo = await res.json()
  fi.space = space
  openFile(fi, [fi])
}

export async function rename(f: FileInfo) {
  const name = await promptDialog(t('action.rename'), f.name, { okLabel: t('action.rename') })
  if (!name || name === f.name) return
  await run(() => post('/files/rename', { space: refOf(f), path: f.path, new_name: name }))
}

export async function remove(items: FileInfo[]) {
  if (items.length === 0) return
  const msg = items.length === 1 ? t('action.deleteOne', { name: items[0].name }) : t('action.deleteMany', { n: items.length })
  if (!(await confirmDialog(t('action.delete'), msg, { okLabel: t('action.moveToTrash'), danger: true }))) return
  // Group by space so one request per space is enough.
  const bySpace = new Map<string, string[]>()
  for (const f of items) bySpace.set(refOf(f), [...(bySpace.get(refOf(f)) || []), f.path])
  for (const [space, paths] of bySpace) {
    const ok = await run(() => post('/files/delete', { space, paths }))
    if (ok === undefined) return
  }
  toast(t('action.deleted', { n: items.length }), 'info', { label: t('nav.trash'), run: () => (location.hash = '#/trash') })
  refreshQuota()
}

export function downloadItems(items: FileInfo[]) {
  if (items.length === 1 && !items[0].is_dir) {
    download(contentUrl(refOf(items[0]), items[0].path, true))
    return
  }
  const space = refOf(items[0])
  if (items.some((f) => refOf(f) !== space)) {
    for (const f of items) downloadItems([f])
    return
  }
  downloadZipForm(space, items.map((f) => f.path))
}

export async function setStar(f: FileInfo, starred: boolean) {
  const ok = await run(() => post('/files/star', { space: refOf(f), path: f.path, starred }))
  if (ok !== undefined) f.starred = starred
}

export async function setColor(f: FileInfo, color: string) {
  const ok = await run(() => put('/files/meta', { space: refOf(f), path: f.path, color }))
  if (ok !== undefined) f.color = color
}

export async function extract(f: FileInfo) {
  toast(t('action.extracting'))
  const r = await run(() => post<{ path: string }>('/files/extract', { space: refOf(f), path: f.path }), t('action.extracted'))
  if (r) openFolder(refOf(f), r.path)
}

export async function transfer(items: FileInfo[], destSpace: string, destPath: string, mode: 'move' | 'copy') {
  const bySpace = new Map<string, string[]>()
  for (const f of items) bySpace.set(refOf(f), [...(bySpace.get(refOf(f)) || []), f.path])
  for (const [space, paths] of bySpace) {
    const r = await run(() => post('/files/' + mode, { space, paths, dest_space: destSpace, dest_path: destPath }))
    if (r === undefined) return false
  }
  toast(mode === 'move' ? t('action.moved', { n: items.length }) : t('action.copied', { n: items.length }), 'success')
  refreshQuota()
  return true
}

export function openLocation(f: FileInfo) {
  openFolder(refOf(f), parentOf(f.path))
}

const browserImages = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'avif', 'bmp', 'ico'])

export function viewerItem(f: FileInfo): ViewerItem {
  const space = refOf(f)
  const e = f.name.split('.').pop()?.toLowerCase() || ''
  let url = contentUrl(space, f.path)
  // Formats browsers cannot show (HEIC, TIFF, RAW) use the server preview.
  if (f.category === 'image' && !browserImages.has(e)) url = thumbUrl(space, f.path, 1280, f.mtime)
  return {
    name: f.name,
    category: f.category || 'other',
    viewer: f.viewer || 'download',
    url,
    download: contentUrl(space, f.path, true),
    thumb: f.category === 'image' || f.category === 'video' ? thumbUrl(space, f.path, 256, f.mtime) : undefined,
    file: f,
    size: f.size,
    mtime: f.mtime,
  }
}

/** Opens a file in the viewer; siblings allow paging with the arrow keys. */
export function openFile(f: FileInfo, siblings: FileInfo[] = [f], slideshow = false) {
  if (f.is_dir) {
    openFolder(refOf(f), f.path)
    return
  }
  const files = siblings.filter((s) => !s.is_dir)
  const index = Math.max(0, files.findIndex((s) => s.id === f.id && s.path === f.path))
  app.viewer = { items: files.map(viewerItem), index, slideshow }
}

export async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast(t('common.copied'), 'success')
  } catch {
    await promptDialog(t('common.copyManually'), text)
  }
}
