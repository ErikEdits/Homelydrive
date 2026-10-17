<script lang="ts">
  import { User, KeyRound, MonitorSmartphone, FileType as FileTypeIcon, Globe, Info, Trash2, Plus, Pencil, Download } from '@lucide/svelte'
  import { del, get, put } from '../../lib/api'
  import { errText, i18n, setLang, t, type Lang } from '../../lib/i18n.svelte'
  import { dateTime, relative } from '../../lib/format'
  import Modal from '../../lib/ui/Modal.svelte'
  import { confirmDialog, toast } from '../../lib/ui/ui.svelte'
  import type { Device, FileType, Me } from '../../lib/types'
  import { app, applyTheme, savePref } from '../state.svelte'
  import { copyText } from '../actions'

  let displayName = $state(app.me?.display_name || '')
  let oldPw = $state('')
  let newPw = $state('')
  let newPw2 = $state('')
  let devices = $state<Device[]>([])
  let types = $state<FileType[]>([])
  let typeFilter = $state('')
  let editType = $state<FileType | null>(null)

  async function loadDevices() {
    devices = await get<Device[]>('/me/devices')
  }
  async function loadTypes() {
    types = await get<FileType[]>('/filetypes')
  }
  loadDevices()
  loadTypes()

  async function saveProfile() {
    try {
      const me = await put<Me>('/me', { display_name: displayName, lang: i18n.lang })
      app.me = me
      toast(t('settings.saved'), 'success')
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function changeLang(l: Lang) {
    setLang(l)
    try {
      await put('/me', { lang: l })
    } catch {
      /* ignore */
    }
  }

  function setTheme(th: 'auto' | 'light' | 'dark') {
    app.theme = th
    savePref('hd_theme', th)
    applyTheme()
  }

  async function changePassword(e: Event) {
    e.preventDefault()
    if (newPw !== newPw2) {
      toast(t('vault.mismatch'), 'error')
      return
    }
    try {
      await put('/me', { old_password: oldPw, new_password: newPw })
      oldPw = newPw = newPw2 = ''
      toast(t('settings.passwordChanged'), 'success')
      loadDevices()
    } catch (err) {
      toast(errText(err), 'error')
    }
  }

  async function revoke(d: Device) {
    if (!(await confirmDialog(t('settings.logoutDevice'), d.device))) return
    await del('/me/devices/' + d.id)
    loadDevices()
  }

  async function saveType() {
    if (!editType) return
    try {
      await put('/filetypes', editType)
      editType = null
      loadTypes()
      toast(t('settings.saved'), 'success')
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function resetType(ft: FileType) {
    await del('/filetypes/' + encodeURIComponent(ft.ext))
    loadTypes()
  }

  const shownTypes = $derived(
    types.filter((ft) => !typeFilter || ft.ext.includes(typeFilter.toLowerCase()) || ft.category.includes(typeFilter.toLowerCase())),
  )
  const davUrl = $derived(location.origin + '/dav/')
  const categories = ['image', 'video', 'audio', 'pdf', 'text', 'document', 'archive', 'app', 'other']
  const viewers = ['image', 'video', 'audio', 'pdf', 'text', 'markdown', 'download']
</script>

<div class="scroll">
  <h1>{t('nav.settings')}</h1>

  <section class="card">
    <h2><User size={18} />{t('settings.profile')}</h2>
    <label class="field">{t('settings.displayName')}
      <div class="row"><input class="input" bind:value={displayName} placeholder={app.me?.username} /><button class="btn" onclick={saveProfile}>{t('common.save')}</button></div>
    </label>
    <div class="grid2">
      <label class="field">{t('settings.language')}
        <select class="input" value={i18n.lang} onchange={(e) => changeLang(e.currentTarget.value as Lang)}>
          <option value="de">Deutsch</option>
          <option value="en">English</option>
        </select>
      </label>
      <label class="field">{t('settings.theme')}
        <select class="input" value={app.theme} onchange={(e) => setTheme(e.currentTarget.value as any)}>
          <option value="auto">{t('theme.auto')}</option>
          <option value="light">{t('theme.light')}</option>
          <option value="dark">{t('theme.dark')}</option>
        </select>
      </label>
    </div>
  </section>

  <section class="card">
    <h2><KeyRound size={18} />{t('settings.password')}</h2>
    <form onsubmit={changePassword} class="grid3">
      <input class="input" type="password" placeholder={t('settings.oldPassword')} bind:value={oldPw} required autocomplete="current-password" />
      <input class="input" type="password" placeholder={t('settings.newPassword')} bind:value={newPw} required minlength="6" autocomplete="new-password" />
      <input class="input" type="password" placeholder={t('vault.repeat')} bind:value={newPw2} required minlength="6" autocomplete="new-password" />
      <button class="btn primary">{t('settings.changePassword')}</button>
    </form>
  </section>

  <section class="card">
    <h2><MonitorSmartphone size={18} />{t('settings.devices')}</h2>
    <p class="muted small">{t('settings.devicesHint')}</p>
    <table class="table">
      <tbody>
        {#each devices as d (d.id)}
          <tr>
            <td>{d.device || '—'}{#if d.current} <span class="chip">{t('settings.thisDevice')}</span>{/if}</td>
            <td class="muted">{t('settings.lastUsed')}: {relative(d.last_used_at)}</td>
            <td class="muted hide-sm">{t('settings.since')} {dateTime(d.created_at)}</td>
            <td>{#if !d.current}<button class="icon-btn small" onclick={() => revoke(d)} aria-label={t('settings.logoutDevice')}><Trash2 size={16} /></button>{/if}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </section>

  <section class="card">
    <h2><FileTypeIcon size={18} />{t('settings.fileTypes')}</h2>
    <p class="muted small">{t('settings.fileTypesHint')}</p>
    <div class="row">
      <input class="input" style="max-width:240px" placeholder={t('settings.filter')} bind:value={typeFilter} />
      <span class="spacer"></span>
      <button class="btn" onclick={() => (editType = { ext: '', mime: '', category: 'text', viewer: 'text', auto: false, builtin: false })}><Plus size={16} />{t('settings.addType')}</button>
    </div>
    <div class="types">
      <table class="table">
        <thead><tr><th>{t('settings.extension')}</th><th>{t('settings.category')}</th><th>{t('settings.viewer')}</th><th></th></tr></thead>
        <tbody>
          {#each shownTypes as ft (ft.ext)}
            <tr>
              <td><code>.{ft.ext}</code>{#if ft.auto} <span class="chip">{t('settings.autoDetected')}</span>{/if}</td>
              <td>{t('type.' + ft.category)}</td>
              <td>{t('viewer.' + ft.viewer)}</td>
              <td class="act">
                <button class="icon-btn small" onclick={() => (editType = { ...ft })} aria-label={t('action.rename')}><Pencil size={15} /></button>
                {#if !ft.builtin || ft.auto}<button class="icon-btn small" onclick={() => resetType(ft)} aria-label={t('action.delete')}><Trash2 size={15} /></button>{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </section>

  <section class="card">
    <h2><Globe size={18} />{t('settings.access')}</h2>
    <p>{t('settings.serverAddress')}: <code>{location.origin}</code> <button class="btn ghost small" onclick={() => copyText(location.origin)}>{t('link.copy')}</button></p>
    <p>{t('settings.webdav')}: <code>{davUrl}</code> <button class="btn ghost small" onclick={() => copyText(davUrl)}>{t('link.copy')}</button></p>
    <p class="muted small">{t('settings.webdavHint')}</p>
    <p class="muted small">{t('settings.appsHint')}</p>
    {#if app.info}<a class="btn" href={app.info.repo + '/releases/latest'} target="_blank" rel="noopener"><Download size={16} />{t('settings.downloadApps')}</a>{/if}
  </section>

  <section class="card">
    <h2><Info size={18} />{t('settings.about')}</h2>
    <p>Homelydrive {app.info?.version} · GPL-3.0 · <a href={app.info?.repo} target="_blank" rel="noopener">GitHub</a></p>
    {#if app.info?.update}
      <p class="update">{t('notify.update', { version: app.info.update.version })} <a href={app.info.update.url} target="_blank" rel="noopener">{t('settings.toRelease')}</a></p>
    {/if}
  </section>
</div>

{#if editType}
  {@const ft = editType}
  <Modal title={t('settings.editType')} onclose={() => (editType = null)} width="420px">
    <label class="field">{t('settings.extension')}<input class="input" bind:value={ft.ext} disabled={ft.builtin && !ft.auto && types.some((x) => x.ext === ft.ext)} placeholder="z. B. nfo" /></label>
    <label class="field">{t('settings.category')}
      <select class="input" bind:value={ft.category}>{#each categories as c}<option value={c}>{t('type.' + c)}</option>{/each}</select>
    </label>
    <label class="field">{t('settings.viewer')}
      <select class="input" bind:value={ft.viewer}>{#each viewers as v}<option value={v}>{t('viewer.' + v)}</option>{/each}</select>
    </label>
    {#snippet footer()}
      <button class="btn ghost" onclick={() => (editType = null)}>{t('common.cancel')}</button>
      <button class="btn primary" onclick={saveType}>{t('common.save')}</button>
    {/snippet}
  </Modal>
{/if}

<style>
  .scroll {
    flex: 1;
    overflow: auto;
    padding: 18px 20px 60px;
  }
  h1 {
    font-weight: 500;
  }
  section {
    max-width: 860px;
    margin-bottom: 16px;
  }
  h2 {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 16px;
  }
  .grid2 {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  .grid3 {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr auto;
    gap: 8px;
  }
  .types {
    max-height: 360px;
    overflow: auto;
    margin-top: 10px;
  }
  .act {
    text-align: right;
    white-space: nowrap;
  }
  .update {
    color: var(--brand);
    font-weight: 500;
  }
  @media (max-width: 800px) {
    .grid2,
    .grid3 {
      grid-template-columns: 1fr;
    }
    .hide-sm {
      display: none;
    }
  }
</style>
