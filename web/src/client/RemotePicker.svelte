<script lang="ts">
  import { ChevronRight, ArrowLeft, HardDrive, Users, FolderInput, Share2, FolderPlus } from '@lucide/svelte'
  import Modal from '../lib/ui/Modal.svelte'
  import FileIcon from '../lib/ui/FileIcon.svelte'
  import { t } from '../lib/i18n.svelte'
  import { promptDialog } from '../lib/ui/ui.svelte'
  import { local } from './api'

  let { onpick, onclose, initial = '' }: { onpick: (space: string, path: string) => void; onclose: () => void; initial?: string } = $props()
  let roots = $state<any[]>([])
  let space = $state<string | null>('home')
  let path = $state('')
  let folders = $state<any[]>([])
  let error = $state('')

  local('GET', '/roots').then((r) => (roots = r.filter((x: any) => x.is_dir && x.can_write))).catch((e) => (error = e.message))

  async function load() {
    if (space === null) return
    try {
      const r = await local('GET', `/remote/list?space=${encodeURIComponent(space)}&path=${encodeURIComponent(path)}`)
      folders = r.entries.filter((e: any) => e.is_dir)
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }
  $effect(() => {
    space
    path
    load()
  })

  async function mkdir() {
    const name = await promptDialog(t('action.newFolder'), initial || t('action.newFolderDefault'))
    if (!name || space === null) return
    const p = path ? path + '/' + name : name
    try {
      await local('POST', '/remote/mkdir', { space, path: p })
      path = p
    } catch (e) {
      error = (e as Error).message
    }
  }

  const name = (ref: string) => (ref === 'home' ? t('nav.myDrive') : roots.find((r) => r.ref === ref)?.name || ref)
  const icon = (k: string) => (k === 'home' ? HardDrive : k === 'team' ? Users : k === 'mount' ? FolderInput : Share2)
  const up = () => {
    if (path) path = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''
    else space = null
  }
</script>

<Modal title={t('client.chooseRemote')} {onclose} width="560px">
  <div class="nav">
    {#if space !== null}
      <button class="icon-btn small" onclick={up} aria-label={t('common.back')}><ArrowLeft size={18} /></button>
      <span class="ellipsis"><strong>{name(space)}</strong>{path ? ' / ' + path.split('/').join(' / ') : ''}</span>
    {:else}
      <strong>{t('move.chooseLocation')}</strong>
    {/if}
  </div>
  <div class="list">
    {#if space === null}
      {#each roots as r (r.ref)}
        {@const I = icon(r.kind)}
        <button class="entry" onclick={() => { space = r.ref; path = '' }}><I size={20} /><span class="spacer">{name(r.ref)}</span><ChevronRight size={18} /></button>
      {/each}
    {:else}
      {#each folders as f (f.path)}
        <button class="entry" onclick={() => (path = f.path)}><FileIcon name={f.name} isDir size={20} /><span class="spacer ellipsis">{f.name}</span><ChevronRight size={18} /></button>
      {:else}
        <p class="muted center">{t('move.noSubfolders')}</p>
      {/each}
    {/if}
  </div>
  {#if error}<p class="error-text">{error}</p>{/if}
  {#snippet footer()}
    {#if space !== null}<button class="btn ghost" onclick={mkdir}><FolderPlus size={16} />{t('action.newFolder')}</button>{/if}
    <span class="spacer"></span>
    <button class="btn ghost" onclick={onclose}>{t('common.cancel')}</button>
    <button class="btn primary" disabled={space === null} onclick={() => space !== null && onpick(space, path)}>{t('client.chooseThis')}</button>
  {/snippet}
</Modal>

<style>
  .nav {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 34px;
    margin-bottom: 8px;
  }
  .list {
    height: 320px;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: 10px;
  }
  .entry {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    padding: 10px 14px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: var(--text);
  }
  .entry:hover {
    background: var(--hover);
  }
  .center {
    text-align: center;
    padding: 30px;
  }
</style>
