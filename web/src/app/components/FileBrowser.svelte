<script lang="ts">
  import {
    LayoutGrid, List, Info, Download, Share2, FolderInput, Copy, Trash2, Star, StarOff, Pencil, Palette,
    FolderOpen, PackageOpen, ExternalLink, FolderPlus, FileUp, FolderUp, RefreshCw, Eye, ArrowUp, ArrowDown, X, Link,
  } from '@lucide/svelte'
  import Menu, { type MenuItem } from '../../lib/ui/Menu.svelte'
  import Modal from '../../lib/ui/Modal.svelte'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { t } from '../../lib/i18n.svelte'
  import { bytes, date, ext, folderColors, parentOf } from '../../lib/format'
  import { thumbUrl, tileThumbSize } from '../../lib/api'
  import type { FileInfo } from '../../lib/types'
  import { filesFromDrop, filesFromInput, uploader, uploadEndpoint } from '../../lib/upload.svelte'
  import { app, savePref } from '../state.svelte'
  import {
    refOf, openFile, rename, remove, downloadItems, setStar, setColor, extract, openLocation, transfer, newFolder, newTextFile,
  } from '../actions'

  interface Props {
    entries: FileInfo[]
    loading?: boolean
    error?: string
    dropTarget?: { space: string; folder: string } | null
    flat?: boolean
    emptyText?: string
    emptyIcon?: any
    onrefresh?: () => void
  }
  let { entries, loading = false, error = '', dropTarget = null, flat = false, emptyText = '', emptyIcon, onrefresh }: Props = $props()

  type SortKey = 'name' | 'mtime' | 'size'
  let sort = $state<{ key: SortKey; asc: boolean }>(JSON.parse(localStorage.getItem('hd_sort') || '{"key":"name","asc":true}'))
  let selected = $state<Set<string>>(new Set())
  let anchor = $state<number>(-1)
  let menu = $state<{ x: number; y: number; items: MenuItem[] } | null>(null)
  let colorFor = $state<FileInfo | null>(null)
  let dragOver = $state(false)
  let dropFolder = $state<string>('')
  let fileInput: HTMLInputElement | undefined = $state()
  let folderInput: HTMLInputElement | undefined = $state()

  const key = (f: FileInfo) => refOf(f) + '|' + f.path

  const sorted = $derived.by(() => {
    const list = [...entries]
    const dir = sort.asc ? 1 : -1
    list.sort((a, b) => {
      if (!flat && a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
      let c = 0
      if (sort.key === 'name') c = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' })
      else if (sort.key === 'mtime') c = a.mtime - b.mtime
      else c = a.size - b.size
      return c * dir
    })
    return list
  })

  const selection = $derived(sorted.filter((f) => selected.has(key(f))))
  const folders = $derived(sorted.filter((f) => f.is_dir))
  const files = $derived(sorted.filter((f) => !f.is_dir))

  $effect(() => {
    // Forget selections of items that disappeared.
    const keys = new Set(entries.map(key))
    let changed = false
    for (const k of selected) if (!keys.has(k)) changed = true
    if (changed) selected = new Set([...selected].filter((k) => keys.has(k)))
  })

  $effect(() => {
    app.details = selection.length === 1 ? selection[0] : null
  })

  function setSort(k: SortKey) {
    sort = sort.key === k ? { key: k, asc: !sort.asc } : { key: k, asc: k === 'name' }
    localStorage.setItem('hd_sort', JSON.stringify(sort))
  }

  function setView(v: 'grid' | 'list') {
    app.view = v
    savePref('hd_view', v)
  }

  function click(e: MouseEvent, f: FileInfo) {
    const idx = sorted.indexOf(f)
    const k = key(f)
    if (e.shiftKey && anchor >= 0) {
      const [a, b] = [Math.min(anchor, idx), Math.max(anchor, idx)]
      const next = new Set(e.ctrlKey || e.metaKey ? selected : [])
      for (let i = a; i <= b; i++) next.add(key(sorted[i]))
      selected = next
    } else if (e.ctrlKey || e.metaKey) {
      const next = new Set(selected)
      next.has(k) ? next.delete(k) : next.add(k)
      selected = next
      anchor = idx
    } else {
      selected = new Set([k])
      anchor = idx
    }
  }

  function open(f: FileInfo) {
    openFile(f, files)
  }

  function itemMenu(e: MouseEvent, f: FileInfo) {
    e.preventDefault()
    e.stopPropagation()
    if (!selected.has(key(f))) {
      selected = new Set([key(f)])
      anchor = sorted.indexOf(f)
    }
    const sel = selection.length ? selection : [f]
    const single = sel.length === 1 ? sel[0] : null
    const canWrite = sel.every((s) => s.can_write)
    const items: MenuItem[] = []
    if (single) {
      items.push({ label: single.is_dir ? t('action.open') : t('action.preview'), icon: single.is_dir ? FolderOpen : Eye, onclick: () => open(single) })
    }
    items.push({ label: sel.length > 1 || sel[0].is_dir ? t('action.downloadZip') : t('action.download'), icon: Download, onclick: () => downloadItems(sel) })
    if (single) {
      items.push({ label: t('action.share'), icon: Share2, onclick: () => (app.shareTarget = single) })
      items.push({ label: t('action.getLink'), icon: Link, onclick: () => (app.shareTarget = single) })
    }
    items.push({ separator: true })
    if (single) items.push({ label: t('action.rename'), icon: Pencil, disabled: !canWrite, shortcut: 'F2', onclick: () => rename(single) })
    items.push({ label: t('action.moveTo'), icon: FolderInput, disabled: !canWrite, onclick: () => (app.moveTarget = { items: sel, mode: 'move' }) })
    items.push({ label: t('action.copyTo'), icon: Copy, onclick: () => (app.moveTarget = { items: sel, mode: 'copy' }) })
    if (single) {
      items.push(
        single.starred
          ? { label: t('action.unstar'), icon: StarOff, onclick: () => setStar(single, false) }
          : { label: t('action.star'), icon: Star, onclick: () => setStar(single, true) },
      )
      if (single.is_dir) items.push({ label: t('action.color'), icon: Palette, disabled: !canWrite, onclick: () => (colorFor = single) })
      if (!single.is_dir && ext(single.name) === 'zip') items.push({ label: t('action.extract'), icon: PackageOpen, disabled: !canWrite, onclick: () => extract(single) })
      items.push({ label: t('action.details'), icon: Info, onclick: () => { app.details = single; app.showDetails = true; savePref('hd_details', true) } })
      if (flat) items.push({ label: t('action.showLocation'), icon: ExternalLink, onclick: () => openLocation(single) })
    }
    items.push({ separator: true })
    items.push({ label: t('action.delete'), icon: Trash2, danger: true, disabled: !canWrite, shortcut: 'Entf', onclick: () => remove(sel) })
    menu = { x: e.clientX, y: e.clientY, items }
  }

  function backgroundMenu(e: MouseEvent) {
    if (!dropTarget) return
    e.preventDefault()
    selected = new Set()
    const tgt = dropTarget
    menu = {
      x: e.clientX,
      y: e.clientY,
      items: [
        { label: t('action.newFolder'), icon: FolderPlus, onclick: () => newFolder(tgt.space, tgt.folder) },
        { label: t('action.uploadFiles'), icon: FileUp, onclick: () => fileInput?.click() },
        { label: t('action.uploadFolder'), icon: FolderUp, onclick: () => folderInput?.click() },
        { separator: true },
        { label: t('action.newTextFile'), onclick: () => newTextFile(tgt.space, tgt.folder) },
        { label: t('action.refresh'), icon: RefreshCw, onclick: () => onrefresh?.() },
      ],
    }
  }

  function keydown(e: KeyboardEvent) {
    const tag = (e.target as HTMLElement)?.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA' || app.viewer || app.shareTarget || app.moveTarget || menu) return
    if ((e.ctrlKey || e.metaKey) && e.key === 'a') {
      e.preventDefault()
      selected = new Set(sorted.map(key))
    } else if (e.key === 'Escape') {
      selected = new Set()
    } else if (e.key === 'Delete' && selection.length && selection.every((s) => s.can_write)) {
      remove(selection)
    } else if (e.key === 'F2' && selection.length === 1 && selection[0].can_write) {
      rename(selection[0])
    } else if (e.key === 'Enter' && selection.length === 1) {
      open(selection[0])
    }
  }

  // ----- drag & drop -----
  function dragStart(e: DragEvent, f: FileInfo) {
    if (!selected.has(key(f))) selected = new Set([key(f)])
    const sel = selection.length ? selection : [f]
    e.dataTransfer?.setData('application/x-hd-items', JSON.stringify(sel))
    e.dataTransfer!.effectAllowed = 'copyMove'
  }

  function dragOverZone(e: DragEvent) {
    if (!dropTarget) return
    e.preventDefault()
    dragOver = true
  }

  async function drop(e: DragEvent, folder?: FileInfo) {
    e.preventDefault()
    e.stopPropagation()
    dragOver = false
    dropFolder = ''
    if (!dropTarget && !folder) return
    const internal = e.dataTransfer?.getData('application/x-hd-items')
    if (internal) {
      if (!folder) return
      const items: FileInfo[] = JSON.parse(internal)
      if (items.some((i) => i.path === folder.path && refOf(i) === refOf(folder))) return
      await transfer(items, refOf(folder), folder.path, e.ctrlKey ? 'copy' : 'move')
      return
    }
    if (!e.dataTransfer) return
    const list = await filesFromDrop(e.dataTransfer)
    if (!list.length) return
    const space = folder ? refOf(folder) : dropTarget!.space
    const dest = folder ? folder.path : dropTarget!.folder
    uploader.add(list, { endpoint: uploadEndpoint(), space, folder: dest })
  }

  function uploadFrom(list: FileList | null) {
    if (!dropTarget) return
    const files = filesFromInput(list)
    if (files.length) uploader.add(files, { endpoint: uploadEndpoint(), space: dropTarget.space, folder: dropTarget.folder })
  }

  function thumb(f: FileInfo) {
    return thumbUrl(refOf(f), f.path, tileThumbSize(), f.mtime)
  }

  function location(f: FileInfo) {
    const p = parentOf(f.path)
    const root = f.space_name || (refOf(f) === 'home' ? t('nav.myDrive') : '')
    return [root, p].filter(Boolean).join(' / ')
  }
</script>

<svelte:window onkeydown={keydown} />

<div class="toolbar">
  {#if selection.length > 0}
    <div class="selbar">
      <button class="icon-btn small" onclick={() => (selected = new Set())} aria-label={t('common.clear')}><X size={18} /></button>
      <span class="count">{t('files.selected', { n: selection.length })}</span>
      <button class="icon-btn small" title={t('action.download')} onclick={() => downloadItems(selection)}><Download size={18} /></button>
      {#if selection.length === 1}
        <button class="icon-btn small" title={t('action.share')} onclick={() => (app.shareTarget = selection[0])}><Share2 size={18} /></button>
      {/if}
      <button class="icon-btn small" title={t('action.moveTo')} disabled={!selection.every((s) => s.can_write)} onclick={() => (app.moveTarget = { items: selection, mode: 'move' })}><FolderInput size={18} /></button>
      <button class="icon-btn small" title={t('action.delete')} disabled={!selection.every((s) => s.can_write)} onclick={() => remove(selection)}><Trash2 size={18} /></button>
      <button class="icon-btn small" title={t('action.more')} onclick={(e) => itemMenu(e, selection[0])}>⋮</button>
    </div>
  {/if}
  <span class="spacer"></span>
  <div class="sort">
    <button class="btn ghost small" onclick={() => setSort('name')}>{t('files.name')} {#if sort.key === 'name'}{#if sort.asc}<ArrowUp size={14} />{:else}<ArrowDown size={14} />{/if}{/if}</button>
    <button class="btn ghost small" onclick={() => setSort('mtime')}>{t('files.modified')} {#if sort.key === 'mtime'}{#if sort.asc}<ArrowUp size={14} />{:else}<ArrowDown size={14} />{/if}{/if}</button>
    <button class="btn ghost small" onclick={() => setSort('size')}>{t('files.size')} {#if sort.key === 'size'}{#if sort.asc}<ArrowUp size={14} />{:else}<ArrowDown size={14} />{/if}{/if}</button>
  </div>
  <div class="seg">
    <button class:active={app.view === 'list'} onclick={() => setView('list')} aria-label={t('files.listView')}><List size={18} /></button>
    <button class:active={app.view === 'grid'} onclick={() => setView('grid')} aria-label={t('files.gridView')}><LayoutGrid size={18} /></button>
  </div>
  <button class="icon-btn small" class:active={app.showDetails} onclick={() => { app.showDetails = !app.showDetails; savePref('hd_details', app.showDetails) }} aria-label={t('action.details')}><Info size={19} /></button>
</div>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  class="area"
  class:drag={dragOver}
  ondragover={dragOverZone}
  ondragleave={(e) => { if (e.currentTarget === e.target) dragOver = false }}
  ondrop={(e) => drop(e)}
  oncontextmenu={backgroundMenu}
  onclick={(e) => { if (e.target === e.currentTarget) selected = new Set() }}
>
  {#if loading && entries.length === 0}
    <div class="empty"><div class="spin"></div></div>
  {:else if error}
    <div class="empty error-text">{error}</div>
  {:else if entries.length === 0}
    <div class="empty">
      {#if emptyIcon}{@const Icon = emptyIcon}<Icon size={56} strokeWidth={1.2} />{/if}
      <div>{emptyText || t('files.empty')}</div>
      {#if dropTarget}<div class="small muted">{t('files.dropHint')}</div>{/if}
    </div>
  {:else if app.view === 'grid'}
    {#if folders.length}
      <div class="section">{t('files.folders')}</div>
      <div class="grid folders">
        {#each folders as f (key(f))}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <div
            class="folder"
            class:sel={selected.has(key(f))}
            class:dropping={dropFolder === key(f)}
            tabindex="0"
            draggable="true"
            ondragstart={(e) => dragStart(e, f)}
            ondragover={(e) => { e.preventDefault(); e.stopPropagation(); dropFolder = key(f) }}
            ondragleave={() => (dropFolder = '')}
            ondrop={(e) => drop(e, f)}
            onclick={(e) => click(e, f)}
            ondblclick={() => open(f)}
            oncontextmenu={(e) => itemMenu(e, f)}
            title={f.name}
          >
            <FileIcon name={f.name} isDir color={f.color} size={22} />
            <span class="ellipsis name">{f.name}</span>
            {#if f.starred}<Star size={14} class="star" />{/if}
            {#if f.shared}<Share2 size={14} class="muted-icon" />{/if}
          </div>
        {/each}
      </div>
    {/if}
    {#if files.length}
      <div class="section">{t('files.files')}</div>
      <div class="grid files">
        {#each files as f (key(f))}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <div
            class="card-file"
            class:sel={selected.has(key(f))}
            tabindex="0"
            draggable="true"
            ondragstart={(e) => dragStart(e, f)}
            onclick={(e) => click(e, f)}
            ondblclick={() => open(f)}
            oncontextmenu={(e) => itemMenu(e, f)}
            title={f.name}
          >
            <div class="head">
              <FileIcon name={f.name} category={f.category} size={18} />
              <span class="ellipsis name">{f.name}</span>
              {#if f.starred}<Star size={14} class="star" />{/if}
            </div>
            <div class="preview">
              {#if f.category === 'image' || f.category === 'video'}
                <img src={thumb(f)} alt="" loading="lazy" onerror={(e) => ((e.currentTarget as HTMLImageElement).style.display = 'none')} />
              {/if}
              <span class="bigicon"><FileIcon name={f.name} category={f.category} size={48} /></span>
            </div>
            {#if flat}<div class="loc ellipsis small muted">{location(f)}</div>{/if}
          </div>
        {/each}
      </div>
    {/if}
  {:else}
    <table class="list">
      <thead>
        <tr>
          <th>{t('files.name')}</th>
          {#if flat}<th class="hide-sm">{t('files.location')}</th>{/if}
          <th class="hide-sm">{t('files.modified')}</th>
          <th class="hide-sm num">{t('files.size')}</th>
        </tr>
      </thead>
      <tbody>
        {#each sorted as f (key(f))}
          <tr
            class:sel={selected.has(key(f))}
            class:dropping={dropFolder === key(f)}
            draggable="true"
            ondragstart={(e) => dragStart(e, f)}
            ondragover={(e) => { if (f.is_dir) { e.preventDefault(); e.stopPropagation(); dropFolder = key(f) } }}
            ondragleave={() => (dropFolder = '')}
            ondrop={(e) => f.is_dir && drop(e, f)}
            onclick={(e) => click(e, f)}
            ondblclick={() => open(f)}
            oncontextmenu={(e) => itemMenu(e, f)}
          >
            <td>
              <div class="namecell">
                <FileIcon name={f.name} isDir={f.is_dir} category={f.category} color={f.color} size={20} />
                <span class="ellipsis">{f.name}</span>
                {#if f.starred}<Star size={14} class="star" />{/if}
                {#if f.shared}<Share2 size={14} class="muted-icon" />{/if}
                {#each f.tags || [] as tag}<span class="chip">{tag}</span>{/each}
              </div>
            </td>
            {#if flat}<td class="hide-sm muted ellipsis loc-cell">{location(f)}</td>{/if}
            <td class="hide-sm muted">{date(f.mtime)}</td>
            <td class="hide-sm muted num">{f.is_dir ? (f.size ? bytes(f.size) : '—') : bytes(f.size)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
  {#if dragOver && dropTarget}
    <div class="dropzone">{t('files.dropHere')}</div>
  {/if}
</div>

<input type="file" multiple hidden bind:this={fileInput} onchange={(e) => { uploadFrom(e.currentTarget.files); e.currentTarget.value = '' }} />
<input type="file" multiple hidden webkitdirectory bind:this={folderInput} onchange={(e) => { uploadFrom(e.currentTarget.files); e.currentTarget.value = '' }} />

{#if menu}<Menu {...menu} onclose={() => (menu = null)} />{/if}

{#if colorFor}
  {@const target = colorFor}
  <Modal title={t('action.color')} onclose={() => (colorFor = null)} width="340px">
    <div class="colors">
      {#each folderColors as c}
        <button
          class="swatch"
          class:current={(target.color || '') === c}
          style:background={c || 'var(--surface-3)'}
          aria-label={c || t('common.none')}
          onclick={() => { setColor(target, c); colorFor = null }}
        >{#if !c}<X size={16} />{/if}</button>
      {/each}
    </div>
  </Modal>
{/if}

<style>
  .toolbar {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 16px 8px;
    min-height: 46px;
    flex-wrap: wrap;
  }
  .selbar {
    display: flex;
    align-items: center;
    gap: 2px;
    background: var(--brand-soft);
    border-radius: 20px;
    padding: 2px 8px 2px 2px;
  }
  .count {
    font-weight: 500;
    margin: 0 10px 0 4px;
  }
  .sort {
    display: flex;
  }
  .sort :global(svg) {
    margin-left: -4px;
  }
  .seg {
    display: flex;
    border: 1px solid var(--border);
    border-radius: 18px;
    overflow: hidden;
  }
  .seg button {
    border: none;
    background: none;
    padding: 5px 12px;
    cursor: pointer;
    color: var(--text-2);
  }
  .seg button.active {
    background: var(--selected);
    color: var(--brand-strong);
  }
  .area {
    flex: 1;
    overflow: auto;
    padding: 0 16px 80px;
    position: relative;
    user-select: none;
  }
  .area.drag {
    outline: 2px dashed var(--brand);
    outline-offset: -8px;
    background: color-mix(in srgb, var(--brand-soft) 50%, transparent);
  }
  .dropzone {
    position: sticky;
    bottom: 20px;
    margin: 0 auto;
    width: fit-content;
    background: var(--brand);
    color: #fff;
    padding: 10px 20px;
    border-radius: 20px;
    box-shadow: var(--shadow-lg);
    pointer-events: none;
  }
  .section {
    font-weight: 500;
    color: var(--text-2);
    margin: 14px 4px 10px;
  }
  .grid {
    display: grid;
    gap: 12px;
  }
  .grid.folders {
    grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
  }
  .grid.files {
    grid-template-columns: repeat(auto-fill, minmax(190px, 1fr));
  }
  .folder {
    display: flex;
    align-items: center;
    gap: 12px;
    height: 48px;
    padding: 0 14px;
    border-radius: 12px;
    background: var(--surface-2);
    cursor: pointer;
    outline: none;
  }
  .folder:hover,
  .card-file:hover {
    background: var(--surface-3);
  }
  .folder.sel,
  .card-file.sel,
  tr.sel {
    background: var(--selected) !important;
  }
  .folder.dropping,
  tr.dropping {
    outline: 2px solid var(--brand);
  }
  .name {
    flex: 1;
    font-weight: 500;
  }
  .card-file {
    border-radius: 12px;
    background: var(--surface-2);
    padding: 0 8px 8px;
    cursor: pointer;
    outline: none;
    display: flex;
    flex-direction: column;
  }
  .card-file .head {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 44px;
    padding: 0 6px;
  }
  .preview {
    position: relative;
    height: 150px;
    border-radius: 8px;
    background: var(--surface);
    display: flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
  }
  .preview img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    z-index: 1;
  }
  .bigicon {
    opacity: 0.85;
  }
  .loc {
    margin: 6px 4px 0;
  }
  :global(.star) {
    color: #f4b400;
    fill: #f4b400;
    flex: none;
  }
  :global(.muted-icon) {
    color: var(--text-3);
    flex: none;
  }
  table.list {
    width: 100%;
    border-collapse: collapse;
    table-layout: fixed;
  }
  table.list th {
    text-align: left;
    font-weight: 500;
    color: var(--text-2);
    font-size: 12.5px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: 0;
    background: var(--surface);
    z-index: 2;
  }
  table.list th:nth-child(1) {
    width: auto;
  }
  table.list th.num,
  td.num {
    text-align: right;
    width: 110px;
  }
  table.list th:not(:first-child):not(.num) {
    width: 160px;
  }
  table.list td {
    padding: 0 10px;
    height: 46px;
    border-bottom: 1px solid var(--border);
  }
  tr {
    cursor: pointer;
  }
  tr:hover {
    background: var(--hover);
  }
  .namecell {
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
  }
  .loc-cell {
    max-width: 240px;
  }
  .colors {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: 10px;
  }
  .swatch {
    width: 36px;
    height: 36px;
    border-radius: 50%;
    border: 2px solid transparent;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--text-2);
  }
  .swatch.current {
    border-color: var(--text);
  }
  .spin {
    width: 28px;
    height: 28px;
    border-radius: 50%;
    border: 3px solid var(--surface-3);
    border-top-color: var(--brand);
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (max-width: 800px) {
    .hide-sm,
    .sort {
      display: none;
    }
    .grid.folders,
    .grid.files {
      grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    }
    .preview {
      height: 110px;
    }
  }
</style>
