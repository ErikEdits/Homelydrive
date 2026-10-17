<script lang="ts">
  import { X, ChevronLeft, ChevronRight, Download, Share2, Trash2, Play, Pause, Crop, Maximize, FileQuestion, Info } from '@lucide/svelte'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { t } from '../../lib/i18n.svelte'
  import { bytes, dateTime } from '../../lib/format'
  import { download } from '../../lib/api'
  import { app } from '../state.svelte'
  import { remove } from '../actions'
  // The editor (CodeMirror) is large, so it is loaded only when needed.
  const loadEditor = () => import('./TextEditor.svelte')
  import ImageEditor from './ImageEditor.svelte'

  const v = $derived(app.viewer!)
  const item = $derived(v.items[v.index])
  let editing = $state(false)
  let showInfo = $state(false)
  let playing = $state(false)
  let root: HTMLDivElement | undefined = $state()

  $effect(() => {
    playing = !!app.viewer?.slideshow
  })

  $effect(() => {
    if (!playing) return
    const timer = setInterval(() => next(true), 4000)
    return () => clearInterval(timer)
  })

  function close() {
    if (document.fullscreenElement) document.exitFullscreen()
    app.viewer = null
  }

  function next(wrap = false) {
    if (!app.viewer) return
    editing = false
    if (v.index < v.items.length - 1) app.viewer.index++
    else if (wrap) app.viewer.index = 0
  }

  function prev() {
    if (!app.viewer) return
    editing = false
    if (v.index > 0) app.viewer.index--
  }

  function keydown(e: KeyboardEvent) {
    const tag = (e.target as HTMLElement)?.tagName
    const inEditor = (e.target as HTMLElement)?.closest?.('.cm-editor') || tag === 'INPUT' || tag === 'TEXTAREA'
    if (e.key === 'Escape') {
      if (editing) editing = false
      else close()
    } else if (inEditor || editing) {
      return
    } else if (e.key === 'ArrowRight') next()
    else if (e.key === 'ArrowLeft') prev()
    else if (e.key === ' ' && item.category === 'image') {
      e.preventDefault()
      playing = !playing
    }
  }

  function fullscreen() {
    if (document.fullscreenElement) document.exitFullscreen()
    else root?.requestFullscreen?.()
  }

  async function del() {
    if (!item.file) return
    const f = item.file
    await remove([f])
    if (!app.viewer) return
    const items = v.items.filter((i) => i !== item)
    if (items.length === 0) close()
    else app.viewer = { items, index: Math.min(v.index, items.length - 1) }
  }

  const images = $derived(v.items.filter((i) => i.category === 'image').length)
</script>

<svelte:window onkeydown={keydown} />

<div class="viewer" bind:this={root}>
  <header>
    <button class="icon-btn" onclick={close} aria-label={t('common.close')}><X size={22} /></button>
    <FileIcon name={item.name} category={item.category} size={22} />
    <span class="name ellipsis" title={item.name}>{item.name}</span>
    <span class="counter">{v.index + 1} / {v.items.length}</span>
    <span class="spacer"></span>
    {#if item.category === 'image' && images > 1}
      <button class="icon-btn" class:active={playing} onclick={() => (playing = !playing)} title={t('viewer.slideshow')}>
        {#if playing}<Pause size={20} />{:else}<Play size={20} />{/if}
      </button>
    {/if}
    {#if item.category === 'image' && item.file?.can_write}
      <button class="icon-btn" class:active={editing} onclick={() => { editing = !editing; playing = false }} title={t('viewer.edit')}><Crop size={20} /></button>
    {/if}
    {#if item.category === 'image' || item.category === 'video'}
      <button class="icon-btn" onclick={fullscreen} title={t('viewer.fullscreen')}><Maximize size={20} /></button>
    {/if}
    <button class="icon-btn" onclick={() => download(item.download)} title={t('action.download')}><Download size={20} /></button>
    {#if item.file}
      <button class="icon-btn" onclick={() => (app.shareTarget = item.file!)} title={t('action.share')}><Share2 size={20} /></button>
      <button class="icon-btn" class:active={showInfo} onclick={() => (showInfo = !showInfo)} title={t('action.details')}><Info size={20} /></button>
      {#if item.file.can_write}
        <button class="icon-btn" onclick={del} title={t('action.delete')}><Trash2 size={20} /></button>
      {/if}
    {/if}
  </header>

  <div class="stage">
    {#if v.index > 0 && !editing}
      <button class="nav left" onclick={prev} aria-label={t('viewer.prev')}><ChevronLeft size={32} /></button>
    {/if}
    {#key item.url}
      {#if editing && item.file}
        <ImageEditor file={item.file} src={item.url} ondone={() => (editing = false)} />
      {:else if item.category === 'image'}
        <img class="media" src={item.url} alt={item.name} />
      {:else if item.category === 'video' || item.viewer === 'video'}
        <!-- svelte-ignore a11y_media_has_caption -->
        <video class="media" src={item.url} controls autoplay onended={() => playing && next(true)}></video>
      {:else if item.category === 'audio' || item.viewer === 'audio'}
        <div class="audio">
          <FileIcon name={item.name} category="audio" size={96} />
          <audio src={item.url} controls autoplay></audio>
        </div>
      {:else if item.viewer === 'pdf'}
        <iframe class="doc" src={item.url} title={item.name}></iframe>
      {:else if item.viewer === 'text' || item.viewer === 'markdown'}
        {#await loadEditor() then mod}
          <mod.default {item} />
        {/await}
      {:else}
        <div class="nopreview">
          <FileQuestion size={72} strokeWidth={1.2} />
          <p>{t('viewer.noPreview')}</p>
          <button class="btn primary" onclick={() => download(item.download)}><Download size={18} />{t('action.download')}</button>
        </div>
      {/if}
    {/key}
    {#if v.index < v.items.length - 1 && !editing}
      <button class="nav right" onclick={() => next()} aria-label={t('viewer.next')}><ChevronRight size={32} /></button>
    {/if}
    {#if showInfo && item.file}
      <aside class="info">
        <h3>{t('action.details')}</h3>
        <dl>
          <dt>{t('files.size')}</dt><dd>{bytes(item.file.size)}</dd>
          <dt>{t('files.modified')}</dt><dd>{dateTime(item.file.mtime)}</dd>
          {#if item.file.taken_at && item.file.taken_at !== item.file.mtime}<dt>{t('details.taken')}</dt><dd>{dateTime(item.file.taken_at)}</dd>{/if}
          <dt>{t('details.type')}</dt><dd>{item.file.mime || '—'}</dd>
          <dt>{t('files.location')}</dt><dd class="ellipsis">{item.file.path}</dd>
          {#if item.file.note}<dt>{t('details.note')}</dt><dd>{item.file.note}</dd>{/if}
        </dl>
      </aside>
    {/if}
  </div>
</div>

<style>
  .viewer {
    position: fixed;
    inset: 0;
    z-index: 95;
    background: #111317;
    color: #e8eaed;
    display: flex;
    flex-direction: column;
  }
  header {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 60px;
    padding: 0 12px;
    flex: none;
  }
  header :global(.icon-btn) {
    color: #e8eaed;
  }
  header :global(.icon-btn:hover),
  header :global(.icon-btn.active) {
    background: rgba(255, 255, 255, 0.12);
    color: #fff;
  }
  .name {
    font-size: 16px;
    font-weight: 500;
  }
  .counter {
    font-size: 12px;
    color: #9aa0a6;
    flex: none;
  }
  .stage {
    flex: 1;
    min-height: 0;
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0 72px 24px;
  }
  .media {
    max-width: 100%;
    max-height: 100%;
    object-fit: contain;
    border-radius: 4px;
  }
  video.media {
    background: #000;
  }
  .doc {
    width: 100%;
    height: 100%;
    border: none;
    background: #fff;
    border-radius: 6px;
  }
  .audio {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 24px;
  }
  .audio audio {
    width: min(480px, 80vw);
  }
  .nopreview {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 12px;
    color: #bdc1c6;
  }
  .nav {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    width: 52px;
    height: 52px;
    border-radius: 50%;
    border: none;
    background: rgba(255, 255, 255, 0.1);
    color: #fff;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 2;
  }
  .nav:hover {
    background: rgba(255, 255, 255, 0.2);
  }
  .nav.left {
    left: 12px;
  }
  .nav.right {
    right: 12px;
  }
  .info {
    position: absolute;
    right: 16px;
    top: 0;
    width: 300px;
    background: #1f2329;
    border-radius: 12px;
    padding: 16px 18px;
    z-index: 3;
  }
  dl {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 6px 14px;
    margin: 0;
    font-size: 13px;
  }
  dt {
    color: #9aa0a6;
  }
  dd {
    margin: 0;
    min-width: 0;
    word-break: break-word;
  }
  @media (max-width: 800px) {
    .stage {
      padding: 0 0 12px;
    }
    .nav {
      width: 40px;
      height: 40px;
    }
    .counter {
      display: none;
    }
  }
</style>
