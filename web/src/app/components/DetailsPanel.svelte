<script lang="ts">
  import { X, Tag, StickyNote, Palette, Share2 } from '@lucide/svelte'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { put, thumbUrl } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { bytes, dateTime, folderColors } from '../../lib/format'
  import { toast } from '../../lib/ui/ui.svelte'
  import type { FileInfo } from '../../lib/types'
  import { app, savePref } from '../state.svelte'
  import { refOf, setColor } from '../actions'

  let { file }: { file: FileInfo } = $props()

  let note = $state('')
  let tagInput = $state('')
  let lastKey = ''

  $effect(() => {
    const k = refOf(file) + '|' + file.path
    if (k !== lastKey) {
      lastKey = k
      note = file.note || ''
      tagInput = ''
    }
  })

  async function saveNote() {
    if ((file.note || '') === note) return
    try {
      await put('/files/meta', { space: refOf(file), path: file.path, note })
      file.note = note
      toast(t('details.noteSaved'), 'success')
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function setTags(tags: string[]) {
    try {
      await put('/files/tags', { space: refOf(file), path: file.path, tags })
      file.tags = tags
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  function addTag(e: Event) {
    e.preventDefault()
    const v = tagInput.trim()
    if (!v) return
    const tags = [...(file.tags || [])]
    if (!tags.some((x) => x.toLowerCase() === v.toLowerCase())) tags.push(v)
    tagInput = ''
    setTags(tags)
  }

  function close() {
    app.showDetails = false
    savePref('hd_details', false)
  }
</script>

<aside class="details">
  <header>
    <FileIcon name={file.name} isDir={file.is_dir} category={file.category} color={file.color} />
    <h3 class="ellipsis">{file.name}</h3>
    <button class="icon-btn small" onclick={close} aria-label={t('common.close')}><X size={18} /></button>
  </header>
  {#if file.category === 'image' || file.category === 'video'}
    <img class="thumb" src={thumbUrl(refOf(file), file.path, 512, file.mtime)} alt="" />
  {/if}
  <dl>
    <dt>{t('details.type')}</dt><dd>{file.is_dir ? t('type.folder') : file.mime || '—'}</dd>
    <dt>{t('files.size')}</dt><dd>{bytes(file.size)}</dd>
    <dt>{t('files.modified')}</dt><dd>{dateTime(file.mtime)}</dd>
    {#if file.taken_at && file.taken_at !== file.mtime}<dt>{t('details.taken')}</dt><dd>{dateTime(file.taken_at)}</dd>{/if}
    <dt>{t('files.location')}</dt><dd>{file.space_name || (refOf(file) === 'home' ? t('nav.myDrive') : '')}{file.path.includes('/') ? ' / ' + file.path.split('/').slice(0, -1).join(' / ') : ''}</dd>
    {#if file.shared}<dt>{t('details.sharing')}</dt><dd><button class="linklike" onclick={() => (app.shareTarget = file)}><Share2 size={14} />{t('details.shared')}</button></dd>{/if}
  </dl>

  {#if file.is_dir && file.can_write}
    <h4><Palette size={15} />{t('action.color')}</h4>
    <div class="colors">
      {#each folderColors as c}
        <button class="swatch" class:current={(file.color || '') === c} style:background={c || 'var(--surface-3)'} aria-label={c || t('common.none')} onclick={() => setColor(file, c)}></button>
      {/each}
    </div>
  {/if}

  <h4><Tag size={15} />{t('details.tags')}</h4>
  <div class="tags">
    {#each file.tags || [] as tag}
      <span class="chip">{tag}<button onclick={() => setTags((file.tags || []).filter((x) => x !== tag))} aria-label="remove">×</button></span>
    {/each}
  </div>
  <form onsubmit={addTag}><input class="input" bind:value={tagInput} placeholder={t('details.addTag')} /></form>

  <h4><StickyNote size={15} />{t('details.note')}</h4>
  {#if file.can_write}
    <textarea class="input" bind:value={note} onblur={saveNote} placeholder={t('details.notePlaceholder')}></textarea>
  {:else}
    <p class="muted">{file.note || '—'}</p>
  {/if}
</aside>

<style>
  .details {
    width: 320px;
    flex: none;
    background: var(--surface);
    border-radius: 16px 16px 0 0;
    margin-right: 12px;
    padding: 14px 18px;
    overflow: auto;
  }
  header {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 12px;
  }
  header h3 {
    flex: 1;
    margin: 0;
  }
  .thumb {
    width: 100%;
    border-radius: 10px;
    margin-bottom: 12px;
    max-height: 240px;
    object-fit: cover;
  }
  dl {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 8px 14px;
    margin: 0 0 8px;
    font-size: 13px;
  }
  dt {
    color: var(--text-2);
  }
  dd {
    margin: 0;
    word-break: break-word;
  }
  h4 {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 18px 0 8px;
    font-size: 13px;
    color: var(--text-2);
    font-weight: 600;
  }
  .colors {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .swatch {
    width: 22px;
    height: 22px;
    border-radius: 50%;
    border: 2px solid transparent;
    cursor: pointer;
  }
  .swatch.current {
    border-color: var(--text);
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 8px;
  }
  .tags button {
    border: none;
    background: none;
    cursor: pointer;
    color: var(--text-2);
    padding: 0 0 0 4px;
  }
  .linklike {
    display: inline-flex;
    gap: 4px;
    align-items: center;
    border: none;
    background: none;
    color: var(--brand);
    cursor: pointer;
    padding: 0;
  }
  @media (max-width: 1100px) {
    .details {
      position: fixed;
      right: 0;
      top: 64px;
      bottom: 0;
      z-index: 40;
      box-shadow: var(--shadow-lg);
      margin: 0;
    }
  }
</style>
