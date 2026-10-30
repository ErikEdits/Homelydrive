// Global state of the drive web app.
import { get } from '../lib/api'
import type { FileInfo, Me, Notification, Release, Root } from '../lib/types'
import { setLang } from '../lib/i18n.svelte'

export interface ViewerItem {
  name: string
  category: string
  viewer: string
  url: string
  download: string
  thumb?: string
  file?: FileInfo
  size?: number
  mtime?: number
}

function loadPref<T>(key: string, def: T): T {
  try {
    const v = localStorage.getItem(key)
    return v === null ? def : (JSON.parse(v) as T)
  } catch {
    return def
  }
}

export function savePref(key: string, v: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(v))
  } catch {
    /* ignore */
  }
}

export const app = $state({
  me: null as Me | null,
  loading: true,
  roots: [] as Root[],
  info: null as { version: string; repo: string; trash_days: number; update?: Release } | null,
  notifications: [] as Notification[],
  unread: 0,
  view: loadPref<'grid' | 'list'>('hd_view', 'grid'),
  theme: loadPref<'auto' | 'light' | 'dark'>('hd_theme', 'auto'),
  showDetails: loadPref<boolean>('hd_details', false),
  sidebarOpen: false,
  viewer: null as { items: ViewerItem[]; index: number; slideshow?: boolean } | null,
  shareTarget: null as FileInfo | null,
  moveTarget: null as { items: FileInfo[]; mode: 'move' | 'copy' } | null,
  /** folder searched for duplicate files */
  duplicates: null as { space: string; path: string; name: string } | null,
  details: null as FileInfo | null,
  /** bumped when a folder changes on the server: key "spaceId:path" */
  changes: {} as Record<string, number>,
  rootsVersion: 0,
})

export function applyTheme() {
  const el = document.documentElement
  if (app.theme === 'auto') el.removeAttribute('data-theme')
  else el.setAttribute('data-theme', app.theme)
}

export async function loadMe(): Promise<boolean> {
  try {
    app.me = await get<Me>('/me')
    if (app.me.lang === 'de' || app.me.lang === 'en') setLang(app.me.lang)
    return true
  } catch {
    app.me = null
    return false
  }
}

export async function loadRoots() {
  try {
    app.roots = await get<Root[]>('/roots')
    app.rootsVersion++
  } catch {
    /* ignore */
  }
}

export async function loadNotifications() {
  try {
    const r = await get<{ items: Notification[]; unread: number }>('/notifications')
    app.notifications = r.items
    app.unread = r.unread
  } catch {
    /* ignore */
  }
}

export async function refreshQuota() {
  try {
    const me = await get<Me>('/me')
    if (app.me) {
      app.me.used = me.used
      app.me.quota = me.quota
    }
  } catch {
    /* ignore */
  }
}

let events: EventSource | null = null
const listeners = new Set<(ev: { type: string; data: any }) => void>()

export function onServerEvent(fn: (ev: { type: string; data: any }) => void) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** Connects to the live event stream (notifications, folder changes). */
export function connectEvents() {
  events?.close()
  events = new EventSource('/api/v1/events')
  events.onmessage = (m) => {
    let ev: { type: string; data: any }
    try {
      ev = JSON.parse(m.data)
    } catch {
      return
    }
    if (ev.type === 'changed') {
      const key = ev.data.space_id + ':' + ev.data.path
      app.changes[key] = (app.changes[key] || 0) + 1
    } else if (ev.type === 'notification') {
      app.notifications.unshift(ev.data)
      app.unread++
      if (ev.data.kind === 'share') loadRoots()
    } else if (ev.type === 'roots_changed') {
      loadRoots()
    }
    listeners.forEach((fn) => fn(ev))
  }
}

export function disconnectEvents() {
  events?.close()
  events = null
}
