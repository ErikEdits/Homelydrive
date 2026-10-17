<script lang="ts">
  import {
    Plus, HardDrive, Users, FolderInput, Share2, Clock, Star, Image, Lock, Trash2, History, Cloud,
    FolderPlus, FileUp, FolderUp, FileText, FileCode,
  } from '@lucide/svelte'
  import Menu from '../../lib/ui/Menu.svelte'
  import { t } from '../../lib/i18n.svelte'
  import { bytes } from '../../lib/format'
  import { filesFromInput, uploader, uploadEndpoint } from '../../lib/upload.svelte'
  import { app } from '../state.svelte'
  import { route, driveHash } from '../router.svelte'
  import { newFolder, newTextFile } from '../actions'

  let newMenu = $state<{ x: number; y: number } | null>(null)
  let fileInput: HTMLInputElement | undefined = $state()
  let folderInput: HTMLInputElement | undefined = $state()

  // "Neu" creates things in the folder that is currently open (if writable).
  const target = $derived.by(() => {
    if (route.name === 'drive') {
      const root = app.roots.find((r) => r.ref === route.ref)
      if (!root || root.can_write) return { space: route.ref, path: route.path }
    }
    return { space: 'home', path: '' }
  })

  function upload(list: FileList | null) {
    const files = filesFromInput(list)
    if (files.length) uploader.add(files, { endpoint: uploadEndpoint(), space: target.space, folder: target.path })
  }

  const teams = $derived(app.roots.filter((r) => r.kind === 'team'))
  const mounts = $derived(app.roots.filter((r) => r.kind === 'mount'))
  const isActive = (name: string, ref?: string) => route.name === name && (ref === undefined || route.ref === ref)
  const pct = $derived(app.me && app.me.quota > 0 ? Math.min(100, (app.me.used / app.me.quota) * 100) : 0)
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
{#if app.sidebarOpen}<div class="scrim" onclick={() => (app.sidebarOpen = false)}></div>{/if}
<nav class="side" class:open={app.sidebarOpen}>
  <button class="new" onclick={(e) => (newMenu = { x: e.clientX, y: e.clientY })}>
    <Plus size={24} /><span>{t('nav.new')}</span>
  </button>

  <a class="nav" class:active={isActive('drive', 'home')} href={driveHash('home')}><HardDrive size={20} />{t('nav.myDrive')}</a>
  {#if teams.length}
    <div class="group">{t('nav.teamDrives')}</div>
    {#each teams as r (r.ref)}
      <a class="nav sub" class:active={isActive('drive', r.ref)} href={driveHash(r.ref)}><Users size={18} /><span class="ellipsis">{r.name}</span></a>
    {/each}
  {/if}
  {#if mounts.length}
    <div class="group">{t('nav.mounts')}</div>
    {#each mounts as r (r.ref)}
      <a class="nav sub" class:active={isActive('drive', r.ref)} href={driveHash(r.ref)}><FolderInput size={18} /><span class="ellipsis">{r.name}</span></a>
    {/each}
  {/if}
  <div class="sep"></div>
  <a class="nav" class:active={isActive('shared') || (route.name === 'drive' && route.ref.startsWith('share:'))} href="#/shared"><Share2 size={20} />{t('nav.shared')}</a>
  <a class="nav" class:active={isActive('recent')} href="#/recent"><Clock size={20} />{t('nav.recent')}</a>
  <a class="nav" class:active={isActive('starred')} href="#/starred"><Star size={20} />{t('nav.starred')}</a>
  <a class="nav" class:active={isActive('gallery')} href="#/gallery"><Image size={20} />{t('nav.photos')}</a>
  <a class="nav" class:active={isActive('vault')} href="#/vault"><Lock size={20} />{t('nav.vault')}</a>
  <a class="nav" class:active={isActive('activity')} href="#/activity"><History size={20} />{t('nav.activity')}</a>
  <a class="nav" class:active={isActive('trash')} href="#/trash"><Trash2 size={20} />{t('nav.trash')}</a>
  <div class="sep"></div>
  <a class="nav" class:active={isActive('storage')} href="#/storage"><Cloud size={20} />{t('nav.storage')}</a>
  {#if app.me}
    <a class="quota" href="#/storage">
      {#if app.me.quota > 0}
        <div class="progress" class:warn={pct >= 80} class:full={pct >= 95}><div style:width="{pct}%"></div></div>
        <span>{t('storage.usedOf', { used: bytes(app.me.used), quota: bytes(app.me.quota) })}</span>
      {:else}
        <span>{t('storage.usedUnlimited', { used: bytes(app.me.used) })}</span>
      {/if}
    </a>
  {/if}
</nav>

<input type="file" multiple hidden bind:this={fileInput} onchange={(e) => { upload(e.currentTarget.files); e.currentTarget.value = '' }} />
<input type="file" multiple hidden webkitdirectory bind:this={folderInput} onchange={(e) => { upload(e.currentTarget.files); e.currentTarget.value = '' }} />

{#if newMenu}
  <Menu
    x={newMenu.x}
    y={newMenu.y}
    onclose={() => (newMenu = null)}
    items={[
      { label: t('action.newFolder'), icon: FolderPlus, onclick: () => newFolder(target.space, target.path) },
      { separator: true },
      { label: t('action.uploadFiles'), icon: FileUp, onclick: () => fileInput?.click() },
      { label: t('action.uploadFolder'), icon: FolderUp, onclick: () => folderInput?.click() },
      { separator: true },
      { label: t('action.newTextFile'), icon: FileText, onclick: () => newTextFile(target.space, target.path, 'txt') },
      { label: t('action.newMarkdown'), icon: FileCode, onclick: () => newTextFile(target.space, target.path, 'md') },
    ]}
  />
{/if}

<style>
  .side {
    width: 256px;
    flex: none;
    padding: 8px 12px 16px 12px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .new {
    display: flex;
    align-items: center;
    gap: 12px;
    height: 56px;
    padding: 0 22px 0 16px;
    margin: 4px 0 16px;
    align-self: flex-start;
    border: none;
    border-radius: 16px;
    background: var(--surface);
    box-shadow: var(--shadow);
    font-size: 15px;
    font-weight: 500;
    cursor: pointer;
    transition: box-shadow 0.15s, background 0.15s;
  }
  .new:hover {
    background: var(--brand-soft);
    box-shadow: var(--shadow-lg);
  }
  .nav {
    display: flex;
    align-items: center;
    gap: 14px;
    height: 34px;
    padding: 0 14px;
    border-radius: 17px;
    color: var(--text);
    font-size: 14px;
  }
  .nav:hover {
    background: var(--hover);
    text-decoration: none;
  }
  .nav.active {
    background: var(--selected);
    color: var(--brand-strong);
    font-weight: 600;
  }
  .nav :global(svg) {
    flex: none;
    color: var(--text-2);
  }
  .nav.active :global(svg) {
    color: var(--brand-strong);
  }
  .nav.sub {
    padding-left: 24px;
    height: 32px;
  }
  .group {
    margin: 10px 0 2px 14px;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-3);
    font-weight: 600;
  }
  .sep {
    height: 1px;
    margin: 8px 6px;
    background: var(--border);
  }
  .quota {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 6px 14px 0 48px;
    color: var(--text-2);
    font-size: 12px;
  }
  .quota:hover {
    text-decoration: none;
  }
  .scrim {
    display: none;
  }
  @media (max-width: 800px) {
    .side {
      position: fixed;
      top: 0;
      bottom: 0;
      left: 0;
      z-index: 80;
      background: var(--bg);
      transform: translateX(-100%);
      transition: transform 0.2s;
      box-shadow: var(--shadow-lg);
      padding-top: 16px;
    }
    .side.open {
      transform: none;
    }
    .scrim {
      display: block;
      position: fixed;
      inset: 0;
      z-index: 79;
      background: rgba(0, 0, 0, 0.35);
    }
  }
</style>
