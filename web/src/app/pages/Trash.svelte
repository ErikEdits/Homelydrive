<script lang="ts">
  import { Trash2, RotateCcw, X } from '@lucide/svelte'
  import { get, post } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { bytes, dateTime } from '../../lib/format'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { confirmDialog, toast } from '../../lib/ui/ui.svelte'
  import type { TrashItem } from '../../lib/types'
  import { app, refreshQuota } from '../state.svelte'

  let items = $state<TrashItem[]>([])
  let loading = $state(true)
  let selected = $state<Set<number>>(new Set())

  async function load() {
    loading = true
    try {
      items = await get<TrashItem[]>('/trash')
      selected = new Set()
    } catch (e) {
      toast(errText(e), 'error')
    } finally {
      loading = false
    }
  }
  load()

  async function restore(ids: number[]) {
    try {
      await post('/trash/restore', { ids })
      toast(t('trash.restored', { n: ids.length }), 'success')
      load()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function purge(ids: number[]) {
    if (!(await confirmDialog(t('trash.deleteForever'), t('trash.deleteForeverConfirm', { n: ids.length }), { danger: true }))) return
    try {
      await post('/trash/delete', { ids })
      load()
      refreshQuota()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function empty() {
    if (!(await confirmDialog(t('trash.empty'), t('trash.emptyConfirm'), { danger: true, okLabel: t('trash.empty') }))) return
    try {
      await post('/trash/empty')
      load()
      refreshQuota()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  function toggle(id: number) {
    const s = new Set(selected)
    s.has(id) ? s.delete(id) : s.add(id)
    selected = s
  }
  const days = $derived(app.info?.trash_days ?? 30)
</script>

<div class="head">
  <h1>{t('nav.trash')}</h1>
  <span class="spacer"></span>
  {#if selected.size}
    <button class="btn small" onclick={() => restore([...selected])}><RotateCcw size={16} />{t('trash.restore')}</button>
    <button class="btn small" onclick={() => purge([...selected])}><X size={16} />{t('trash.deleteForever')}</button>
  {/if}
  {#if items.length}<button class="btn ghost small" onclick={empty}><Trash2 size={16} />{t('trash.empty')}</button>{/if}
</div>
<p class="muted note">{t('trash.info', { days })}</p>
<div class="scroll">
  {#if !loading && items.length === 0}
    <div class="empty"><Trash2 size={56} strokeWidth={1.2} />{t('trash.isEmpty')}</div>
  {:else}
    <table class="table">
      <thead><tr><th style="width:36px"></th><th>{t('files.name')}</th><th>{t('trash.origin')}</th><th>{t('trash.deletedAt')}</th><th>{t('files.size')}</th><th></th></tr></thead>
      <tbody>
        {#each items as it (it.id)}
          <tr class:sel={selected.has(it.id)}>
            <td><input type="checkbox" checked={selected.has(it.id)} onchange={() => toggle(it.id)} /></td>
            <td><span class="row"><FileIcon name={it.name} isDir={it.is_dir} size={20} /><span class="ellipsis">{it.name}</span></span></td>
            <td class="muted ellipsis">{it.space_name ? it.space_name + ' / ' : ''}{it.path.split('/').slice(0, -1).join(' / ')}</td>
            <td class="muted">{dateTime(it.deleted_at)}{#if it.deleted_by}<br /><span class="small">{it.deleted_by}</span>{/if}</td>
            <td class="muted">{bytes(it.size)}</td>
            <td class="act">
              <button class="icon-btn small" title={t('trash.restore')} onclick={() => restore([it.id])}><RotateCcw size={16} /></button>
              <button class="icon-btn small" title={t('trash.deleteForever')} onclick={() => purge([it.id])}><X size={16} /></button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<style>
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 18px 20px 0;
    flex-wrap: wrap;
  }
  .head h1 {
    font-weight: 500;
    margin: 0;
  }
  .note {
    padding: 0 20px;
    margin: 8px 0;
  }
  .scroll {
    flex: 1;
    overflow: auto;
    padding: 0 20px 40px;
  }
  tr.sel {
    background: var(--selected);
  }
  .act {
    white-space: nowrap;
    text-align: right;
  }
</style>
