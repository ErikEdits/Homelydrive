// Checks that every translation key used in the sources exists in both
// languages and that both dictionaries have the same keys.
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

function dict(file) {
  const src = readFileSync(file, 'utf8')
  return new Set([...src.matchAll(/^\s*'([^']+)':/gm)].map((m) => m[1]))
}

function walk(dir, out = []) {
  for (const f of readdirSync(dir)) {
    const p = join(dir, f)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.(svelte|ts)$/.test(p) && !p.includes('/lang/')) out.push(p)
  }
  return out
}

const de = dict('src/lib/lang/de.ts')
const en = dict('src/lib/lang/en.ts')
const used = new Set()
for (const f of walk('src')) {
  for (const m of readFileSync(f, 'utf8').matchAll(/\bt\('([a-zA-Z0-9_.]+)'/g)) used.add(m[1])
}
// Keys built at runtime.
const dynamic = {
  type: ['folder', 'image', 'video', 'audio', 'pdf', 'text', 'document', 'archive', 'app', 'other'],
  viewer: ['image', 'video', 'audio', 'pdf', 'text', 'markdown', 'download'],
  activity: ['upload', 'mkdir', 'rename', 'move', 'copy', 'delete', 'restore', 'purge', 'edit', 'extract', 'share', 'unshare', 'link_create', 'link_delete', 'vault_setup', 'vault_reset'],
  err: ['not_found', 'forbidden', 'exists', 'quota', 'unauthorized', 'vault_locked', 'link_expired', 'network'],
}
for (const [p, keys] of Object.entries(dynamic)) keys.forEach((k) => used.add(p + '.' + k))
used.delete('type.')
used.delete('viewer.')
used.delete('activity.')

let bad = 0
for (const k of used) {
  if (!de.has(k)) (console.log('missing de:', k), bad++)
  if (!en.has(k)) (console.log('missing en:', k), bad++)
}
for (const k of de) if (!en.has(k)) (console.log('only in de:', k), bad++)
for (const k of en) if (!de.has(k)) (console.log('only in en:', k), bad++)
console.log(bad ? `${bad} problems` : `i18n ok (${de.size} keys)`)
process.exit(bad ? 1 : 0)
