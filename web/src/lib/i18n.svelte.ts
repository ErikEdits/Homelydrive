// Tiny i18n: German and English. t("key", {n: 3}) replaces {n}.
import { de } from './lang/de'
import { en } from './lang/en'

export type Lang = 'de' | 'en'

const dicts: Record<Lang, Record<string, string>> = { de, en }

function initialLang(): Lang {
  try {
    const saved = localStorage.getItem('hd_lang')
    if (saved === 'de' || saved === 'en') return saved
  } catch {
    /* storage blocked */
  }
  return navigator.language?.toLowerCase().startsWith('de') ? 'de' : 'en'
}

export const i18n = $state({ lang: initialLang() as Lang })

export function setLang(lang: Lang) {
  i18n.lang = lang
  document.documentElement.lang = lang
  try {
    localStorage.setItem('hd_lang', lang)
  } catch {
    /* ignore */
  }
}

export function t(key: string, params?: Record<string, string | number>): string {
  let s = dicts[i18n.lang][key] ?? dicts.de[key] ?? key
  if (params) {
    for (const [k, v] of Object.entries(params)) s = s.replaceAll('{' + k + '}', String(v))
  }
  return s
}

/** Translates an API error code, falling back to the server's message. */
export function errText(e: unknown): string {
  const err = e as { code?: string; message?: string }
  if (err?.code && err.code !== 'bad_request' && err.code !== 'internal') {
    const key = 'err.' + err.code
    const s = t(key)
    if (s !== key) return s
  }
  return err?.message || String(e)
}
