<script lang="ts">
  import { ChevronRight, ArrowLeft, HardDrive, Users, FolderInput, Share2, FolderPlus } from '@lucide/svelte'
  import Modal from '../../lib/ui/Modal.svelte'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { get, post } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { joinPath, parentOf } from '../../lib/format'
  import { promptDialog, toast } from '../../lib/ui/ui.svelte'
  import type { FileInfo, ListResult } from '../../lib/types'
  import { app } from '../state.svelte'
  import { refOf, transfer } from '../actions'

  const target = app.moveTarget!
  let space = $state<string | null>(refOf(target.items[0]))
  let path = $state(parentOf(target.items[0].path))
  let folders = $state<FileInfo[]>([])
  let canWrite = $state(false)
  let busy = $state(false)

  const roots = $derived(app.roots.filter((r) => r.is_dir && r.can_write))

  async function load() {
    if (space === null) return
    try {
      const r = await get<ListResult>('/files/list', { space, path })
      folders = r.entries.filter((e) => e.is_dir)
      canWrite = r.can_write
    } catch (e) {
      toast(errText(e), 'error')
      space = null
    }
  }

  $effect(() => {
    space
    path
    load()
  })

  function rootName(ref: string) {
    if (ref === 'home') return t('nav.myDrive')
    return app.roots.find((r) => r.ref === ref)?.name || ref
  }

  const iconFor = (kind: string) => (kind === 'home' ? HardDrive : kind === 'team' ? Users : kind === 'mount' ? FolderInput : Share2)

  async function mkdir() {
    const name = await promptDialog(t('action.newFolder'), t('action.newFolderDefault'))
    if (!name || space === null) return
    try {
      await post('/files/mkdir', { space, path: joinPath(path, name) })
      path = joinPath(path, name)
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function go() {
    if (space === null) return
    busy = true
    const ok = await transfer(target.items, space, path, target.mode)
    busy = false
    if (ok) app.moveTarget = null
  }

  const moving = $derived(target.items.map((i) => refOf(i) + '|' + i.path))
</script>

<Modal
  title={(target.mode === 'move' ? t('action.moveTo') : t('action.copyTo')) + ' – ' + (target.items.length === 1 ? target.items[0].name : t('files.selected', { n: target.items.length }))}
  onclose={() => (app.moveTarget = null)}
  width="520px"
>
  <div class="nav">
    {#if space !== null}
      <button class="icon-btn small" onclick={() => (path ? (path = parentOf(path)) : (space = null))} aria-label={t('common.back')}><ArrowLeft size={18} /></button>
      <span class="ellipsis"><strong>{rootName(space)}</strong>{path ? ' / ' + path.split('/').join(' / ') : ''}</span>
    {:else}
      <strong>{t('move.chooseLocation')}</strong>
    {/if}
  </div>
  <div class="list">
    {#if space === null}
      {#each roots as r (r.ref)}
        {@const Icon = iconFor(r.kind)}
        <button class="entry" onclick={() => { space = r.ref; path = '' }}>
          <Icon size={20} /><span class="spacer ellipsis">{r.ref === 'home' ? t('nav.myDrive') : r.name}</span><ChevronRight size={18} />
        </button>
      {/each}
    {:else}
      {#each folders as f (f.path)}
        {@const disabled = moving.includes(space + '|' + f.path)}
        <button class="entry" {disabled} onclick={() => (path = f.path)}>
          <FileIcon name={f.name} isDir color={f.color} size={20} /><span class="spacer ellipsis">{f.name}</span><ChevronRight size={18} />
        </button>
      {:else}
        <p class="muted center">{t('move.noSubfolders')}</p>
      {/each}
    {/if}
  </div>

  {#snippet footer()}
    {#if space !== null && canWrite}
      <button class="btn ghost" onclick={mkdir}><FolderPlus size={16} />{t('action.newFolder')}</button>
    {/if}
    <span class="spacer"></span>
    <button class="btn ghost" onclick={() => (app.moveTarget = null)}>{t('common.cancel')}</button>
    <button class="btn primary" disabled={space === null || !canWrite || busy} onclick={go}>
      {target.mode === 'move' ? t('move.moveHere') : t('move.copyHere')}
    </button>
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
  .entry:hover:not(:disabled) {
    background: var(--hover);
  }
  .entry:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .center {
    text-align: center;
    padding: 30px;
  }
</style>
