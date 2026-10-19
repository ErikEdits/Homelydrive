<script lang="ts">
  import { Link as LinkIcon, Upload, Trash2, Copy, UserPlus, Lock, Download } from '@lucide/svelte'
  import Modal from '../../lib/ui/Modal.svelte'
  import Avatar from '../../lib/ui/Avatar.svelte'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { del, get, post } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { dateTime } from '../../lib/format'
  import { toast } from '../../lib/ui/ui.svelte'
  import type { FileInfo, Link, Share, UserBrief } from '../../lib/types'
  import { app } from '../state.svelte'
  import { copyText, refOf } from '../actions'

  let { file }: { file: FileInfo } = $props()

  let shares = $state<Share[]>([])
  let links = $state<Link[]>([])
  let canShare = $state(true)
  let users = $state<UserBrief[]>([])
  let newUser = $state('')
  let newWrite = $state(false)
  let creating = $state<'download' | 'upload' | null>(null)
  let opt = $state({ password: '', expires: '', max: '' })
  let loading = $state(true)

  const space = $derived(refOf(file))

  async function load() {
    try {
      const r = await get<{ shares: Share[]; links: Link[]; can_share: boolean }>('/shares', { space, path: file.path })
      shares = r.shares
      links = r.links
      canShare = r.can_share
      if (canShare && users.length === 0) users = await get<UserBrief[]>('/users')
    } catch (e) {
      toast(errText(e), 'error')
    } finally {
      loading = false
    }
  }
  load()

  async function addShare() {
    if (!newUser) return
    try {
      await post('/shares', { space, path: file.path, user_id: Number(newUser), can_write: newWrite })
      newUser = ''
      newWrite = false
      file.shared = true
      toast(t('share.done'), 'success')
      load()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function setWrite(s: Share, canWrite: boolean) {
    try {
      await post('/shares', { space, path: file.path, user_id: s.target_id, can_write: canWrite })
      load()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function unshare(s: Share) {
    await del('/shares/' + s.id)
    load()
  }

  async function createLink() {
    if (!creating) return
    const body: any = { space, path: file.path, kind: creating }
    if (opt.password) body.password = opt.password
    if (opt.expires) body.expires_at = new Date(opt.expires + 'T23:59:59').getTime()
    if (opt.max && creating === 'download') body.max_downloads = Number(opt.max)
    try {
      const l = await post<Link>('/links', body)
      creating = null
      opt = { password: '', expires: '', max: '' }
      file.shared = true
      await load()
      copyText(linkUrl(l))
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function removeLink(l: Link) {
    await del('/links/' + l.id)
    load()
  }

  const linkUrl = (l: Link) => location.origin + (l.kind === 'upload' ? '/u/' : '/s/') + l.token
  const available = $derived(users.filter((u) => !shares.some((s) => s.target_id === u.id)))
</script>

<Modal title={t('share.title')} onclose={() => (app.shareTarget = null)} width="560px">
  <div class="target"><FileIcon name={file.name} isDir={file.is_dir} category={file.category} color={file.color} />{file.name}</div>

  {#if loading}
    <p class="muted">…</p>
  {:else if !canShare}
    <p class="muted">{t('share.notAllowed')}</p>
  {:else}
    <h3>{t('share.people')}</h3>
    <div class="add">
      <select class="input" bind:value={newUser}>
        <option value="">{t('share.chooseUser')}</option>
        {#each available as u}<option value={String(u.id)}>{u.display_name || u.username} ({u.username})</option>{/each}
      </select>
      <select class="input perm" bind:value={newWrite}>
        <option value={false}>{t('share.canView')}</option>
        <option value={true}>{t('share.canEdit')}</option>
      </select>
      <button class="btn primary" disabled={!newUser} onclick={addShare}><UserPlus size={16} />{t('action.share')}</button>
    </div>
    {#if users.length === 0}<p class="muted small">{t('share.noUsers')}</p>{/if}
    {#each shares as s (s.id)}
      <div class="person">
        <Avatar id={s.target_id} name={s.target_name} version={s.target_avatar} size={32} />
        <span class="spacer">{s.target_name}</span>
        <select class="input perm" value={s.can_write} onchange={(e) => setWrite(s, e.currentTarget.value === 'true')}>
          <option value={false}>{t('share.canView')}</option>
          <option value={true}>{t('share.canEdit')}</option>
        </select>
        <button class="icon-btn small" onclick={() => unshare(s)} aria-label={t('share.stop')}><Trash2 size={16} /></button>
      </div>
    {/each}

    <h3 class="links-title">{t('share.links')}</h3>
    <p class="muted small">{t('share.linkHint')}</p>
    {#each links as l (l.id)}
      <div class="link">
        {#if l.kind === 'upload'}<Upload size={18} />{:else}<LinkIcon size={18} />{/if}
        <div class="spacer linkinfo">
          <code class="ellipsis">{linkUrl(l)}</code>
          <span class="small muted">
            {l.kind === 'upload' ? t('link.upload') : t('link.download')}
            {#if l.has_password} · <Lock size={11} /> {t('link.password')}{/if}
            {#if l.expires_at} · {t('link.expiresOn', { date: dateTime(l.expires_at) })}{/if}
            {#if l.kind === 'download'} · {t('link.downloadsCount', { n: l.downloads })}{#if l.max_downloads} / {l.max_downloads}{/if}{/if}
          </span>
        </div>
        <button class="icon-btn small" onclick={() => copyText(linkUrl(l))} aria-label={t('link.copy')}><Copy size={16} /></button>
        <button class="icon-btn small" onclick={() => removeLink(l)} aria-label={t('link.delete')}><Trash2 size={16} /></button>
      </div>
    {/each}

    {#if creating}
      <div class="create card">
        <h3>{creating === 'upload' ? t('link.newUpload') : t('link.newDownload')}</h3>
        <label class="field">{t('link.passwordOptional')}<input class="input" type="text" bind:value={opt.password} autocomplete="off" /></label>
        <div class="two">
          <label class="field">{t('link.expiresOptional')}<input class="input" type="date" bind:value={opt.expires} /></label>
          {#if creating === 'download'}
            <label class="field">{t('link.maxOptional')}<input class="input" type="number" min="1" bind:value={opt.max} /></label>
          {/if}
        </div>
        <div class="row">
          <span class="spacer"></span>
          <button class="btn ghost" onclick={() => (creating = null)}>{t('common.cancel')}</button>
          <button class="btn primary" onclick={createLink}>{t('link.create')}</button>
        </div>
      </div>
    {:else}
      <div class="row">
        <button class="btn" onclick={() => (creating = 'download')}><Download size={16} />{t('link.newDownload')}</button>
        {#if file.is_dir}<button class="btn" onclick={() => (creating = 'upload')}><Upload size={16} />{t('link.newUpload')}</button>{/if}
      </div>
    {/if}
  {/if}

  {#snippet footer()}
    <button class="btn primary" onclick={() => (app.shareTarget = null)}>{t('common.done')}</button>
  {/snippet}
</Modal>

<style>
  .target {
    display: flex;
    align-items: center;
    gap: 10px;
    font-weight: 500;
    margin-bottom: 18px;
  }
  .add {
    display: flex;
    gap: 8px;
    margin-bottom: 10px;
  }
  .perm {
    width: 150px;
    flex: none;
  }
  .person,
  .link {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 0;
  }
  .links-title {
    margin-top: 22px;
  }
  .linkinfo {
    display: flex;
    flex-direction: column;
    min-width: 0;
    gap: 2px;
  }
  .create {
    margin-top: 10px;
    padding: 16px;
  }
  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  @media (max-width: 600px) {
    .add {
      flex-wrap: wrap;
    }
    .two {
      grid-template-columns: 1fr;
    }
  }
</style>
