<script lang="ts">
  import { CopyCheck, Trash2 } from '@lucide/svelte'
  import Modal from '../../lib/ui/Modal.svelte'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { get, thumbUrl } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { bytes, date, parentOf } from '../../lib/format'
  import type { FileInfo } from '../../lib/types'
  import { app } from '../state.svelte'
  import { remove } from '../actions'

  interface Group {
    size: number
    files: FileInfo[]
  }

  const target = app.duplicates!
  let groups = $state<Group[]>([])
  let loading = $state(true)
  let error = $state('')
  let selected = $state<Set<string>>(new Set())
  let busy = $state(false)

  const key = (f: FileInfo) => f.path

  async function search() {
    loading = true
    error = ''
    try {
      const r = await get<{ groups: Group[] }>('/files/duplicates', { space: target.space, path: target.path })
      for (const g of r.groups) g.files.forEach((f) => (f.space = target.space))
      groups = r.groups
      // Suggest deleting everything but the first file of each group.
      selected = new Set(r.groups.flatMap((g) => g.files.slice(1).filter((f) => f.can_write).map(key)))
    } catch (e) {
      error = errText(e)
    } finally {
      loading = false
    }
  }
  search()

  function toggle(f: FileInfo) {
    const s = new Set(selected)
    if (s.has(key(f))) s.delete(key(f))
    else s.add(key(f))
    selected = s
  }

  async function deleteSelected() {
    busy = true
    try {
      if (await remove(chosen)) await search()
    } finally {
      busy = false
    }
  }

  /** Where a file lies, relative to the searched folder. */
  function folderOf(f: FileInfo) {
    let p = parentOf(f.path)
    if (target.path && (p === target.path || p.startsWith(target.path + '/'))) p = p.slice(target.path.length + 1)
    return p || t('dup.thisFolder')
  }

  const chosen = $derived(groups.flatMap((g) => g.files.filter((f) => selected.has(key(f)))))
  const freed = $derived(chosen.reduce((n, f) => n + f.size, 0))
  const wasted = $derived(groups.reduce((n, g) => n + g.size * (g.files.length - 1), 0))
  const allGone = (g: Group) => g.files.every((f) => selected.has(key(f)))
  const blocked = $derived(groups.some(allGone))
</script>

<Modal title={t('dup.title')} onclose={() => (app.duplicates = null)} width="680px">
  <p class="muted small where">{t('dup.in', { name: target.name })}</p>

  {#if loading}
    <div class="state"><div class="spin"></div>{t('dup.searching')}</div>
  {:else if error}
    <p class="error-text">{error}</p>
  {:else if groups.length === 0}
    <div class="state"><CopyCheck size={44} strokeWidth={1.4} />{t('dup.none')}</div>
  {:else}
    <p class="summary">{t('dup.summary', { n: groups.length, size: bytes(wasted) })}</p>
    <p class="muted small">{t('dup.hint')}</p>
    <div class="groups">
      {#each groups as g (g.files[0].path)}
        <section class="group" class:bad={allGone(g)}>
          <div class="ghead">{t('dup.copies', { n: g.files.length })} · {bytes(g.size)}</div>
          {#each g.files as f (key(f))}
            <label class="file" class:keep={!selected.has(key(f))}>
              <input type="checkbox" checked={selected.has(key(f))} disabled={!f.can_write} onchange={() => toggle(f)} />
              <span class="pic">
                {#if f.category === 'image' || f.category === 'video'}
                  <img src={thumbUrl(f.space, f.path, 256, f.mtime)} alt="" loading="lazy" onerror={(e) => ((e.currentTarget as HTMLImageElement).style.display = 'none')} />
                {/if}
                <FileIcon name={f.name} category={f.category} size={22} />
              </span>
              <span class="info">
                <span class="name ellipsis">{f.name}</span>
                <span class="small muted ellipsis">{folderOf(f)} · {date(f.mtime, false)}</span>
              </span>
              {#if !selected.has(key(f))}<span class="chip">{t('dup.keep')}</span>{/if}
            </label>
          {/each}
          {#if allGone(g)}<p class="warn small">{t('dup.keepOne')}</p>{/if}
        </section>
      {/each}
    </div>
  {/if}

  {#snippet footer()}
    <button class="btn ghost" onclick={() => (app.duplicates = null)}>{t('common.close')}</button>
    {#if groups.length}
      <button class="btn danger" disabled={busy || chosen.length === 0 || blocked} onclick={deleteSelected}>
        <Trash2 size={16} />{t('dup.delete', { n: chosen.length, size: bytes(freed) })}
      </button>
    {/if}
  {/snippet}
</Modal>

<style>
  .where {
    margin-top: 0;
  }
  .state {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 14px;
    padding: 40px 10px;
    color: var(--text-2);
    text-align: center;
  }
  .spin {
    width: 32px;
    height: 32px;
    border: 3px solid var(--surface-3);
    border-top-color: var(--brand);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  .summary {
    font-weight: 500;
    margin-bottom: 2px;
  }
  .groups {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-top: 12px;
  }
  .group {
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 6px 6px 4px;
  }
  .group.bad {
    border-color: var(--danger);
  }
  .ghead {
    font-size: 13px;
    color: var(--text-2);
    padding: 4px 8px 6px;
  }
  .file {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 8px;
    border-radius: 8px;
    cursor: pointer;
    min-width: 0;
  }
  .file:hover {
    background: var(--hover);
  }
  .file input {
    flex: none;
    width: 18px;
    height: 18px;
  }
  .pic {
    position: relative;
    flex: none;
    width: 44px;
    height: 44px;
    border-radius: 6px;
    overflow: hidden;
    background: var(--surface-2);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .pic img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    z-index: 1;
  }
  .info {
    display: flex;
    flex-direction: column;
    min-width: 0;
    flex: 1;
  }
  .keep .name {
    font-weight: 500;
  }
  .warn {
    color: var(--danger);
    margin: 2px 8px 6px;
  }
</style>
