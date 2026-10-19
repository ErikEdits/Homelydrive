<script lang="ts">
  import { Share2, Link as LinkIcon, Trash2, Copy, Upload, Download, Users } from '@lucide/svelte'
  import { del, get } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { dateTime } from '../../lib/format'
  import type { FileInfo, Link, Share } from '../../lib/types'
  import Avatar from '../../lib/ui/Avatar.svelte'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { confirmDialog, toast } from '../../lib/ui/ui.svelte'
  import FileBrowser from '../components/FileBrowser.svelte'
  import { app } from '../state.svelte'
  import { driveHash } from '../router.svelte'
  import { copyText } from '../actions'

  let tab = $state<'incoming' | 'outgoing' | 'links'>('incoming')
  let outgoing = $state<Share[]>([])
  let links = $state<Link[]>([])
  let loading = $state(false)

  const incoming = $derived<FileInfo[]>(
    app.roots
      .filter((r) => r.kind === 'share')
      .map((r) => ({
        id: -Number(r.ref.split(':')[1]),
        name: r.name,
        path: '',
        space: r.ref,
        space_name: r.owner,
        is_dir: r.is_dir,
        size: 0,
        mtime: 0,
        can_write: r.can_write,
      })),
  )

  async function loadTab() {
    loading = true
    try {
      if (tab === 'outgoing') outgoing = await get<Share[]>('/shares/outgoing')
      if (tab === 'links') links = await get<Link[]>('/links')
    } catch (e) {
      toast(errText(e), 'error')
    } finally {
      loading = false
    }
  }

  $effect(() => {
    tab
    loadTab()
  })

  async function unshare(s: Share) {
    if (!(await confirmDialog(t('share.stop'), t('share.stopConfirm', { name: s.name, user: s.target_name }), { danger: true }))) return
    await del('/shares/' + s.id)
    loadTab()
  }

  async function removeLink(l: Link) {
    if (!(await confirmDialog(t('link.delete'), t('link.deleteConfirm', { name: l.name }), { danger: true }))) return
    await del('/links/' + l.id)
    loadTab()
  }

  const linkUrl = (l: Link) => location.origin + (l.kind === 'upload' ? '/u/' : '/s/') + l.token
</script>

<div class="head">
  <h1>{t('nav.shared')}</h1>
  <div class="tabs">
    <button class:active={tab === 'incoming'} onclick={() => (tab = 'incoming')}><Users size={16} />{t('shared.withMe')}</button>
    <button class:active={tab === 'outgoing'} onclick={() => (tab = 'outgoing')}><Share2 size={16} />{t('shared.byMe')}</button>
    <button class:active={tab === 'links'} onclick={() => (tab = 'links')}><LinkIcon size={16} />{t('shared.links')}</button>
  </div>
</div>

{#if tab === 'incoming'}
  <FileBrowser entries={incoming} flat emptyIcon={Users} emptyText={t('shared.noneIncoming')} />
{:else if tab === 'outgoing'}
  <div class="pad">
    {#if outgoing.length === 0 && !loading}
      <div class="empty"><Share2 size={48} strokeWidth={1.2} />{t('shared.noneOutgoing')}</div>
    {:else}
      <table class="table">
        <thead><tr><th>{t('files.name')}</th><th>{t('share.with')}</th><th>{t('share.permission')}</th><th>{t('share.since')}</th><th></th></tr></thead>
        <tbody>
          {#each outgoing as s (s.id)}
            <tr>
              <td><a class="row" href={driveHash(s.space || 'home', s.is_dir ? s.path : s.path.split('/').slice(0, -1).join('/'))}><FileIcon name={s.name} isDir={s.is_dir} size={18} />{s.name}</a></td>
              <td><span class="who"><Avatar id={s.target_id} name={s.target_name} version={s.target_avatar} size={24} />{s.target_name}</span></td>
              <td>{s.can_write ? t('share.canEdit') : t('share.canView')}</td>
              <td class="muted">{dateTime(s.created_at)}</td>
              <td><button class="icon-btn small" onclick={() => unshare(s)} aria-label={t('share.stop')}><Trash2 size={16} /></button></td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
{:else}
  <div class="pad">
    {#if links.length === 0 && !loading}
      <div class="empty"><LinkIcon size={48} strokeWidth={1.2} />{t('shared.noneLinks')}</div>
    {:else}
      <table class="table">
        <thead><tr><th>{t('files.name')}</th><th>{t('link.kind')}</th><th>{t('link.downloads')}</th><th>{t('link.expires')}</th><th></th></tr></thead>
        <tbody>
          {#each links as l (l.id)}
            <tr>
              <td><span class="row"><FileIcon name={l.name} isDir={l.is_dir} size={18} />{l.name}{#if l.has_password}<span class="chip">🔒</span>{/if}</span></td>
              <td>{#if l.kind === 'upload'}<span class="row"><Upload size={14} />{t('link.upload')}</span>{:else}<span class="row"><Download size={14} />{t('link.download')}</span>{/if}</td>
              <td>{l.kind === 'upload' ? '—' : l.downloads + (l.max_downloads ? ' / ' + l.max_downloads : '')}</td>
              <td class="muted">{l.expires_at ? dateTime(l.expires_at) : t('link.never')}</td>
              <td class="row">
                <button class="icon-btn small" onclick={() => copyText(linkUrl(l))} aria-label={t('link.copy')}><Copy size={16} /></button>
                <button class="icon-btn small" onclick={() => removeLink(l)} aria-label={t('link.delete')}><Trash2 size={16} /></button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
{/if}

<style>
  .who {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  .head {
    padding: 18px 20px 0;
  }
  .head h1 {
    font-weight: 500;
  }
  .tabs {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .tabs button {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    border: 1px solid var(--border);
    background: var(--surface);
    border-radius: 18px;
    padding: 6px 14px;
    cursor: pointer;
  }
  .tabs button.active {
    background: var(--selected);
    border-color: transparent;
    color: var(--brand-strong);
    font-weight: 500;
  }
  .pad {
    padding: 16px 20px;
    overflow: auto;
  }
  td .row {
    gap: 10px;
    color: var(--text);
  }
</style>
