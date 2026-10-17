<script lang="ts">
  import { Lock, LockOpen, ShieldCheck, FolderPlus, FileUp, Download, Pencil, Trash2, ChevronRight, KeyRound } from '@lucide/svelte'
  import { get, post, download } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { bytes, date } from '../../lib/format'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { confirmDialog, promptDialog, toast } from '../../lib/ui/ui.svelte'
  import type { VaultItem } from '../../lib/types'
  import { filesFromInput, uploader, uploadEndpoint } from '../../lib/upload.svelte'
  import { app, type ViewerItem } from '../state.svelte'

  let vstate = $state<{ configured: boolean; unlocked: boolean } | null>(null)
  let pw = $state('')
  let pw2 = $state('')
  let error = $state('')
  let items = $state<VaultItem[]>([])
  let stack = $state<{ id: number; name: string }[]>([{ id: 0, name: '' }])
  let fileInput: HTMLInputElement | undefined = $state()
  const parent = $derived(stack[stack.length - 1].id)

  async function loadState() {
    vstate = await get('/vault')
    if (vstate?.unlocked) list()
  }
  loadState()

  async function list() {
    try {
      items = await get<VaultItem[]>('/vault/list', { parent })
      items.sort((a, b) => (a.is_dir === b.is_dir ? a.name.localeCompare(b.name) : a.is_dir ? -1 : 1))
    } catch (e: any) {
      if (e.code === 'vault_locked') vstate = { configured: true, unlocked: false }
      else toast(errText(e), 'error')
    }
  }

  async function setup(e: Event) {
    e.preventDefault()
    error = ''
    if (pw !== pw2) {
      error = t('vault.mismatch')
      return
    }
    try {
      await post('/vault/setup', { password: pw })
      pw = pw2 = ''
      await loadState()
    } catch (err) {
      error = errText(err)
    }
  }

  async function unlock(e: Event) {
    e.preventDefault()
    error = ''
    try {
      await post('/vault/unlock', { password: pw })
      pw = ''
      await loadState()
    } catch (err) {
      error = errText(err)
    }
  }

  async function lock() {
    await post('/vault/lock')
    items = []
    stack = [{ id: 0, name: '' }]
    vstate = { configured: true, unlocked: false }
  }

  async function changePassword() {
    const old = await promptDialog(t('vault.changePassword'), '', { message: t('vault.oldPassword'), inputType: 'password' })
    if (!old) return
    const nw = await promptDialog(t('vault.changePassword'), '', { message: t('vault.newPassword'), inputType: 'password' })
    if (!nw) return
    try {
      await post('/vault/password', { password: old, new_password: nw })
      toast(t('vault.passwordChanged'), 'success')
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function reset() {
    if (!(await confirmDialog(t('vault.reset'), t('vault.resetWarning'), { danger: true, okLabel: t('vault.reset') }))) return
    const acc = await promptDialog(t('vault.reset'), '', { message: t('vault.accountPassword'), inputType: 'password' })
    if (!acc) return
    try {
      await post('/vault/reset', { password: acc })
      toast(t('vault.resetDone'))
      loadState()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function mkdir() {
    const name = await promptDialog(t('action.newFolder'), t('action.newFolderDefault'))
    if (!name) return
    try {
      await post('/vault/mkdir', { parent, name })
      list()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function rename(it: VaultItem) {
    const name = await promptDialog(t('action.rename'), it.name)
    if (!name || name === it.name) return
    try {
      await post('/vault/rename', { id: it.id, name })
      list()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function remove(it: VaultItem) {
    if (!(await confirmDialog(t('action.delete'), t('vault.deleteForever', { name: it.name }), { danger: true }))) return
    try {
      await post('/vault/delete', { ids: [it.id] })
      list()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  const contentUrl = (it: VaultItem, dl = false) => `/api/v1/vault/content/${it.id}${dl ? '?download=1' : ''}`

  function open(it: VaultItem) {
    if (it.is_dir) {
      stack = [...stack, { id: it.id, name: it.name }]
      list()
      return
    }
    const files = items.filter((i) => !i.is_dir)
    const viewerItems: ViewerItem[] = files.map((f) => ({
      name: f.name,
      category: f.category || 'other',
      viewer: f.viewer || 'download',
      url: contentUrl(f),
      download: contentUrl(f, true),
      size: f.size,
      mtime: f.mtime,
    }))
    app.viewer = { items: viewerItems, index: files.indexOf(it) }
  }

  function upload(list: FileList | null) {
    const files = filesFromInput(list).map((f) => ({ file: f.file, relPath: f.file.name }))
    if (!files.length) return
    uploader.add(files, { endpoint: uploadEndpoint(), folder: '', vaultParent: parent })
  }

  // Refresh the list whenever a vault upload finishes.
  const vaultDone = $derived(uploader.items.filter((i) => i.target.vaultParent !== undefined && i.status === 'done').length)
  $effect(() => {
    if (vaultDone && vstate?.unlocked) list()
  })
</script>

<div class="page">
  {#if !vstate}
    <div class="empty">…</div>
  {:else if !vstate.configured}
    <form class="card box" onsubmit={setup}>
      <ShieldCheck size={48} color="var(--brand)" />
      <h2>{t('vault.setupTitle')}</h2>
      <p class="muted">{t('vault.setupText')}</p>
      <label class="field">{t('vault.password')}<input class="input" type="password" bind:value={pw} minlength="8" required autocomplete="new-password" /></label>
      <label class="field">{t('vault.repeat')}<input class="input" type="password" bind:value={pw2} minlength="8" required autocomplete="new-password" /></label>
      <p class="warn small">{t('vault.forgetWarning')}</p>
      {#if error}<p class="error-text">{error}</p>{/if}
      <button class="btn primary">{t('vault.create')}</button>
    </form>
  {:else if !vstate.unlocked}
    <form class="card box" onsubmit={unlock}>
      <Lock size={48} color="var(--brand)" />
      <h2>{t('vault.locked')}</h2>
      <p class="muted">{t('vault.unlockText')}</p>
      <label class="field">{t('vault.password')}<input class="input" type="password" bind:value={pw} required autocomplete="current-password" /></label>
      {#if error}<p class="error-text">{error}</p>{/if}
      <button class="btn primary"><LockOpen size={16} />{t('vault.unlock')}</button>
      <button type="button" class="btn ghost small" onclick={reset}>{t('vault.forgot')}</button>
    </form>
  {:else}
    <div class="bar">
      <nav class="crumbs">
        {#each stack as s, i (s.id)}
          {#if i > 0}<ChevronRight size={16} />{/if}
          <button class="crumb" onclick={() => { stack = stack.slice(0, i + 1); list() }}>{i === 0 ? t('nav.vault') : s.name}</button>
        {/each}
      </nav>
      <span class="spacer"></span>
      <button class="btn small" onclick={mkdir}><FolderPlus size={16} />{t('action.newFolder')}</button>
      <button class="btn small" onclick={() => fileInput?.click()}><FileUp size={16} />{t('action.uploadFiles')}</button>
      <button class="icon-btn small" onclick={changePassword} title={t('vault.changePassword')}><KeyRound size={18} /></button>
      <button class="btn primary small" onclick={lock}><Lock size={16} />{t('vault.lock')}</button>
    </div>
    <p class="muted small info"><ShieldCheck size={14} /> {t('vault.info')}</p>
    {#if items.length === 0}
      <div class="empty"><Lock size={48} strokeWidth={1.2} />{t('vault.empty')}</div>
    {:else}
      <table class="table">
        <tbody>
          {#each items as it (it.id)}
            <tr ondblclick={() => open(it)}>
              <td><button class="namebtn" onclick={() => open(it)}><FileIcon name={it.name} isDir={it.is_dir} category={it.category} size={20} /><span class="ellipsis">{it.name}</span></button></td>
              <td class="muted">{date(it.mtime)}</td>
              <td class="muted">{it.is_dir ? '' : bytes(it.size)}</td>
              <td class="actions">
                {#if !it.is_dir}<button class="icon-btn small" onclick={() => download(contentUrl(it, true))} aria-label={t('action.download')}><Download size={16} /></button>{/if}
                <button class="icon-btn small" onclick={() => rename(it)} aria-label={t('action.rename')}><Pencil size={16} /></button>
                <button class="icon-btn small" onclick={() => remove(it)} aria-label={t('action.delete')}><Trash2 size={16} /></button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  {/if}
</div>
<input type="file" multiple hidden bind:this={fileInput} onchange={(e) => { upload(e.currentTarget.files); e.currentTarget.value = '' }} />

<style>
  .page {
    flex: 1;
    overflow: auto;
    padding: 20px;
  }
  .box {
    max-width: 420px;
    margin: 40px auto;
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 6px;
    text-align: center;
  }
  .box :global(svg) {
    align-self: center;
  }
  .box label {
    text-align: left;
  }
  .warn {
    color: var(--warning);
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .crumbs {
    display: flex;
    align-items: center;
    gap: 2px;
  }
  .crumb {
    border: none;
    background: none;
    font-size: 18px;
    padding: 4px 8px;
    border-radius: 14px;
    cursor: pointer;
    color: var(--text);
  }
  .crumb:hover {
    background: var(--hover);
  }
  .info {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .namebtn {
    display: flex;
    align-items: center;
    gap: 12px;
    border: none;
    background: none;
    cursor: pointer;
    color: var(--text);
    padding: 0;
    max-width: 100%;
  }
  .actions {
    text-align: right;
    white-space: nowrap;
  }
</style>
