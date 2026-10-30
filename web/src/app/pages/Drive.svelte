<script lang="ts">
  import { ChevronRight, FolderOpen, Users, FolderInput, Share2, HardDrive, CopyCheck } from '@lucide/svelte'
  import { get } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import type { FileInfo, ListResult } from '../../lib/types'
  import FileBrowser from '../components/FileBrowser.svelte'
  import { app } from '../state.svelte'
  import { route, driveHash } from '../router.svelte'
  import { openFile } from '../actions'

  let result = $state<ListResult | null>(null)
  let entries = $state<FileInfo[]>([])
  let loading = $state(true)
  let error = $state('')
  let seq = 0

  async function load() {
    const my = ++seq
    loading = true
    error = ''
    try {
      const r = await get<ListResult>('/files/list', { space: route.ref, path: route.path })
      if (my !== seq) return
      r.entries.forEach((e) => (e.space = r.space))
      result = r
      entries = r.entries
    } catch (e: any) {
      if (my !== seq) return
      // A shared single file opens directly in the viewer.
      if (e?.code === 'bad_request' && route.ref.startsWith('share:') && !route.path) {
        try {
          const fi = await get<FileInfo>('/files/stat', { space: route.ref, path: '' })
          fi.space = route.ref
          entries = [fi]
          result = null
          openFile(fi)
          error = ''
          return
        } catch {
          /* fall through */
        }
      }
      error = errText(e)
      entries = []
    } finally {
      if (my === seq) loading = false
    }
  }

  $effect(() => {
    route.ref
    route.path
    load()
  })

  // Live refresh when something changes in this folder.
  $effect(() => {
    if (!result) return
    const full = result.base ? (route.path ? result.base + '/' + route.path : result.base) : route.path
    const k = result.space_id + ':' + full
    if (app.changes[k]) {
      const timer = setTimeout(load, 250)
      return () => clearTimeout(timer)
    }
  })

  const root = $derived(app.roots.find((r) => r.ref === route.ref))
  const rootName = $derived(route.ref === 'home' ? t('nav.myDrive') : root?.name || result?.label || '…')
  const RootIcon = $derived(route.ref === 'home' ? HardDrive : route.ref.startsWith('team:') ? Users : route.ref.startsWith('mount:') ? FolderInput : Share2)
  const crumbs = $derived.by(() => {
    const parts = route.path.split('/').filter(Boolean)
    return parts.map((name, i) => ({ name, path: parts.slice(0, i + 1).join('/') }))
  })
</script>

<div class="head">
  <nav class="crumbs">
    {#if route.ref.startsWith('share:')}
      <a href="#/shared" class="crumb">{t('nav.shared')}</a><ChevronRight size={18} />
    {/if}
    <a href={driveHash(route.ref)} class="crumb" class:last={crumbs.length === 0}><RootIcon size={18} />{rootName}</a>
    {#each crumbs as c, i}
      <ChevronRight size={18} />
      <a href={driveHash(route.ref, c.path)} class="crumb" class:last={i === crumbs.length - 1}>{c.name}</a>
    {/each}
  </nav>
  {#if root?.owner}<span class="chip">{t('share.from', { name: root.owner })}</span>{/if}
  {#if result && !result.can_write}<span class="chip">{t('files.readOnly')}</span>{/if}
  {#if result}
    <span class="spacer"></span>
    <button class="btn ghost small" title={t('dup.findLong')}
      onclick={() => (app.duplicates = { space: route.ref, path: route.path, name: crumbs.at(-1)?.name || rootName })}>
      <CopyCheck size={16} />{t('dup.find')}
    </button>
  {/if}
</div>

<FileBrowser
  {entries}
  {loading}
  {error}
  dropTarget={result?.can_write ? { space: route.ref, folder: route.path } : null}
  emptyIcon={FolderOpen}
  emptyText={t('files.emptyFolder')}
  onrefresh={load}
/>

<style>
  .head {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 16px 4px;
    flex-wrap: wrap;
  }
  .crumbs {
    display: flex;
    align-items: center;
    gap: 2px;
    color: var(--text-2);
    min-width: 0;
    flex-wrap: wrap;
  }
  .crumb {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    padding: 4px 10px;
    border-radius: 16px;
    color: var(--text-2);
    font-size: 18px;
  }
  .crumb:hover {
    background: var(--hover);
    text-decoration: none;
  }
  .crumb.last {
    color: var(--text);
    font-weight: 500;
  }
</style>
