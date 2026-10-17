import { i18n } from './i18n.svelte'

const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function bytes(n: number | undefined | null, digits = 1): string {
  if (n === undefined || n === null || isNaN(n)) return ''
  if (n < 1024) return n + ' B'
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return v.toLocaleString(i18n.lang, { maximumFractionDigits: v < 10 ? digits : 0 }) + ' ' + units[i]
}

export function date(ms: number | undefined, withTime = true): string {
  if (!ms) return ''
  const d = new Date(ms)
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  if (sameDay && withTime) return d.toLocaleTimeString(i18n.lang, { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString(i18n.lang, {
    day: '2-digit',
    month: '2-digit',
    year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric',
    ...(withTime ? {} : {}),
  })
}

export function dateTime(ms: number | undefined): string {
  if (!ms) return ''
  return new Date(ms).toLocaleString(i18n.lang, { dateStyle: 'medium', timeStyle: 'short' })
}

export function dayLabel(ms: number): string {
  const d = new Date(ms)
  return d.toLocaleDateString(i18n.lang, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' })
}

export function relative(ms: number): string {
  const diff = (Date.now() - ms) / 1000
  const rtf = new Intl.RelativeTimeFormat(i18n.lang, { numeric: 'auto' })
  if (diff < 60) return rtf.format(-Math.round(diff), 'second')
  if (diff < 3600) return rtf.format(-Math.round(diff / 60), 'minute')
  if (diff < 86400) return rtf.format(-Math.round(diff / 3600), 'hour')
  if (diff < 86400 * 30) return rtf.format(-Math.round(diff / 86400), 'day')
  return dateTime(ms)
}

export function ext(name: string): string {
  const i = name.lastIndexOf('.')
  return i > 0 ? name.slice(i + 1).toLowerCase() : ''
}

export function joinPath(a: string, b: string): string {
  if (!a) return b
  if (!b) return a
  return a.replace(/\/+$/, '') + '/' + b.replace(/^\/+/, '')
}

export function parentOf(p: string): string {
  const i = p.lastIndexOf('/')
  return i < 0 ? '' : p.slice(0, i)
}

export function baseName(p: string): string {
  const i = p.lastIndexOf('/')
  return i < 0 ? p : p.slice(i + 1)
}

/** Folder colours offered in the colour picker (like Google Drive). */
export const folderColors = [
  '', '#e53935', '#fb8c00', '#fdd835', '#43a047', '#00897b', '#039be5', '#3949ab', '#8e24aa', '#d81b60', '#6d4c41', '#757575',
]
