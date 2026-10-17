<script lang="ts">
  import { onMount } from 'svelte'
  import {
    LayoutDashboard, Users, FolderTree, FolderInput, HardDrive, Settings, ScrollText, LogOut, Copy, Plus, Trash2,
    Pencil, RefreshCw, CheckCircle2, AlertTriangle, XCircle, FolderOpen, Download, ChevronUp,
  } from '@lucide/svelte'
  import Logo from '../lib/ui/Logo.svelte'
  import Modal from '../lib/ui/Modal.svelte'
  import Dialogs from '../lib/ui/Dialogs.svelte'
  import { api, del, get, post, put } from '../lib/api'
  import { errText, i18n, setLang, t } from '../lib/i18n.svelte'
  import { bytes, dateTime } from '../lib/format'
  import { confirmDialog, toast } from '../lib/ui/ui.svelte'

  type Tab = 'overview' | 'users' | 'teams' | 'mounts' | 'storage' | 'settings' | 'logs'
  let tab = $state<Tab>('overview')
  let phase = $state<'loading' | 'setup' | 'login' | 'ready'>('loading')
  let form = $state({ username: '', password: '', password2: '' })
  let error = $state('')

  let status = $state<any>(null)
  let users = $state<any[]>([])
  let defaultQuota = $state(100)
  let teams = $state<any[]>([])
  let mounts = $state<any[]>([])
  let locations = $state<any[]>([])
  let settings = $state<any>(null)
  let logs = $state<any[]>([])
  let logLevel = $state('')

  let userEdit = $state<any>(null)
  let spaceEdit = $state<any>(null)
  let picker = $state<{ path: string; parent: string; dirs: { name: string; path: string }[]; onpick: (p: string) => void } | null>(null)

  async function init() {
    const st = await get('/admin/state')
    if (st.language && !localStorage.getItem('hd_lang')) setLang(st.language)
    if (st.setup_needed) {
      phase = 'setup'
      return
    }
    try {
      await get('/admin/status')
      phase = 'ready'
      loadTab()
    } catch {
      phase = 'login'
    }
  }

  onMount(() => {
    init().catch((e) => (error = errText(e)))
    const timer = setInterval(() => {
      if (phase === 'ready' && (tab === 'logs' || tab === 'overview')) loadTab(true)
    }, 3000)
    return () => clearInterval(timer)
  })

  async function setup(e: Event) {
    e.preventDefault()
    error = ''
    if (form.password !== form.password2) {
      error = t('vault.mismatch')
      return
    }
    try {
      await post('/admin/setup', { username: form.username, password: form.password, language: i18n.lang })
      phase = 'ready'
      loadTab()
    } catch (err) {
      error = errText(err)
    }
  }

  async function login(e: Event) {
    e.preventDefault()
    error = ''
    try {
      await post('/admin/login', { username: form.username, password: form.password })
      form.password = ''
      phase = 'ready'
      loadTab()
    } catch (err: any) {
      error = err.status === 401 ? t('admin.loginFailed') : errText(err)
    }
  }

  async function logout() {
    await post('/admin/logout').catch(() => {})
    phase = 'login'
  }

  async function loadTab(quiet = false) {
    try {
      switch (tab) {
        case 'overview':
          status = await get('/admin/status')
          break
        case 'users': {
          const r = await get('/admin/users')
          users = r.users
          defaultQuota = r.default_quota_gb
          break
        }
        case 'teams':
        case 'mounts': {
          const r = await get('/admin/spaces')
          teams = r.teams
          mounts = r.mounts
          if (users.length === 0) users = (await get('/admin/users')).users
          break
        }
        case 'storage':
          locations = await get('/admin/locations')
          break
        case 'settings':
          settings = await get('/admin/settings')
          break
        case 'logs': {
          const since = logs.length ? logs[logs.length - 1].seq : 0
          const more = await get('/admin/logs', { since })
          logs = [...logs, ...more].slice(-2000)
          break
        }
      }
    } catch (e: any) {
      if (e.status === 401) phase = 'login'
      else if (!quiet) toast(errText(e), 'error')
    }
  }

  function switchTab(t2: Tab) {
    tab = t2
    loadTab()
  }

  // ----- users -----
  function newUser() {
    userEdit = { isNew: true, username: '', display_name: '', password: '', is_admin: false, quota_gb: defaultQuota }
  }
  function editUser(u: any) {
    userEdit = { isNew: false, id: u.id, username: u.username, display_name: u.display_name, password: '', is_admin: u.is_admin, quota_gb: Math.round(u.quota_bytes / 2 ** 30) }
  }
  async function saveUser() {
    const u = userEdit
    const quota = Math.max(0, Number(u.quota_gb) || 0) * 2 ** 30
    try {
      if (u.isNew) {
        await post('/admin/users', { username: u.username, display_name: u.display_name, password: u.password, is_admin: u.is_admin, quota_bytes: quota })
      } else {
        const body: any = { display_name: u.display_name, is_admin: u.is_admin, quota_bytes: quota }
        if (u.password) body.password = u.password
        await put('/admin/users/' + u.id, body)
      }
      userEdit = null
      toast(t('settings.saved'), 'success')
      loadTab()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }
  async function deleteUser(u: any) {
    if (!(await confirmDialog(t('admin.deleteUser'), t('admin.deleteUserConfirm', { name: u.username }), { danger: true }))) return
    const files = await confirmDialog(t('admin.deleteUserFiles'), t('admin.deleteUserFilesText'), { danger: true, okLabel: t('admin.deleteFilesToo') })
    try {
      await del('/admin/users/' + u.id, { delete_files: files ? 1 : undefined })
      loadTab()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  // ----- teams & mounts -----
  function newSpace(kind: 'team' | 'mount') {
    spaceEdit = { isNew: true, kind, name: '', root_path: '', members: users.map((u) => ({ user_id: u.id, username: u.username, on: kind === 'team', can_write: true })) }
  }
  function editSpace(s: any) {
    spaceEdit = {
      isNew: false, id: s.id, kind: s.kind, name: s.name, root_path: s.root_path,
      members: users.map((u) => {
        const m = s.members.find((x: any) => x.user_id === u.id)
        return { user_id: u.id, username: u.username, on: !!m, can_write: m ? m.can_write : true }
      }),
    }
  }
  async function saveSpace() {
    const s = spaceEdit
    const members = s.members.filter((m: any) => m.on).map((m: any) => ({ user_id: m.user_id, can_write: m.can_write }))
    try {
      if (s.isNew) await post('/admin/spaces', { kind: s.kind, name: s.name, root_path: s.root_path, members })
      else await put('/admin/spaces/' + s.id, { name: s.name, members })
      spaceEdit = null
      toast(t('settings.saved'), 'success')
      loadTab()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }
  async function deleteSpace(s: any) {
    const msg = s.kind === 'team' ? t('admin.deleteTeamConfirm', { name: s.name }) : t('admin.deleteMountConfirm', { name: s.name })
    if (!(await confirmDialog(t('action.delete'), msg, { danger: true }))) return
    let files = false
    if (s.kind === 'team') files = await confirmDialog(t('admin.deleteUserFiles'), t('admin.deleteTeamFilesText'), { danger: true, okLabel: t('admin.deleteFilesToo') })
    try {
      await del('/admin/spaces/' + s.id, { delete_files: files ? 1 : undefined })
      loadTab()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  // ----- folder picker -----
  async function browse(path: string, onpick: (p: string) => void) {
    try {
      const r = await get('/admin/browse', { path })
      picker = { ...r, onpick }
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function addLocation(p: string) {
    try {
      await post('/admin/locations', { path: p })
      loadTab()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function removeLocation(p: string) {
    if (!(await confirmDialog(t('admin.removeLocation'), p, { danger: true }))) return
    try {
      await del('/admin/locations', { path: p })
      loadTab()
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function saveSettings() {
    try {
      settings = await put('/admin/settings', {
        port: Number(settings.port),
        default_quota_gb: Number(settings.default_quota_gb),
        trash_days: Number(settings.trash_days),
        https: settings.https,
        update_check: settings.update_check,
        language: settings.language,
        scan_interval_minutes: Number(settings.scan_interval_minutes),
      })
      setLang(settings.language)
      toast(t('settings.saved'), 'success')
    } catch (e) {
      toast(errText(e), 'error')
    }
  }

  async function rescan() {
    await post('/admin/rescan')
    toast(t('admin.rescanStarted'))
  }

  function copy(text: string) {
    navigator.clipboard?.writeText(text).then(() => toast(t('common.copied'), 'success'))
  }

  const pct = (l: any) => (l.total ? Math.round(100 - (l.free / l.total) * 100) : 0)
  const shownLogs = $derived(logs.filter((l) => !logLevel || l.level === logLevel))
  const uptime = (s: number) => (s < 3600 ? Math.round(s / 60) + ' min' : s < 86400 ? (s / 3600).toFixed(1) + ' h' : (s / 86400).toFixed(1) + ' d')
  void api
</script>

{#if phase === 'loading'}
  <div class="center">…{#if error}<p class="error-text">{error}</p>{/if}</div>
{:else if phase === 'setup' || phase === 'login'}
  <div class="center">
    <form class="card auth" onsubmit={phase === 'setup' ? setup : login}>
      <Logo size={56} />
      <h1>{phase === 'setup' ? t('admin.welcome') : t('admin.title')}</h1>
      <p class="muted">{phase === 'setup' ? t('admin.setupText') : t('admin.loginText')}</p>
      <label class="field">{t('login.username')}<input class="input" bind:value={form.username} required autocapitalize="off" /></label>
      <label class="field">{t('login.password')}<input class="input" type="password" bind:value={form.password} required minlength={phase === 'setup' ? 6 : undefined} /></label>
      {#if phase === 'setup'}
        <label class="field">{t('vault.repeat')}<input class="input" type="password" bind:value={form.password2} required /></label>
      {/if}
      {#if error}<p class="error-text">{error}</p>{/if}
      <button class="btn primary">{phase === 'setup' ? t('admin.createAdmin') : t('login.submit')}</button>
      <div class="langs">
        <button type="button" class:active={i18n.lang === 'de'} onclick={() => setLang('de')}>Deutsch</button>
        <button type="button" class:active={i18n.lang === 'en'} onclick={() => setLang('en')}>English</button>
      </div>
    </form>
  </div>
{:else}
  <div class="layout">
    <nav class="side">
      <div class="brand"><Logo size={30} /><span>Homelydrive<br /><small>{t('admin.server')}</small></span></div>
      <button class:active={tab === 'overview'} onclick={() => switchTab('overview')}><LayoutDashboard size={19} />{t('admin.overview')}</button>
      <button class:active={tab === 'users'} onclick={() => switchTab('users')}><Users size={19} />{t('admin.users')}</button>
      <button class:active={tab === 'teams'} onclick={() => switchTab('teams')}><FolderTree size={19} />{t('nav.teamDrives')}</button>
      <button class:active={tab === 'mounts'} onclick={() => switchTab('mounts')}><FolderInput size={19} />{t('nav.mounts')}</button>
      <button class:active={tab === 'storage'} onclick={() => switchTab('storage')}><HardDrive size={19} />{t('admin.locations')}</button>
      <button class:active={tab === 'settings'} onclick={() => switchTab('settings')}><Settings size={19} />{t('nav.settings')}</button>
      <button class:active={tab === 'logs'} onclick={() => switchTab('logs')}><ScrollText size={19} />{t('admin.logs')}</button>
      <span class="spacer"></span>
      <button onclick={logout}><LogOut size={19} />{t('nav.logout')}</button>
    </nav>

    <main>
      {#if tab === 'overview' && status}
        <h1>{t('admin.overview')}</h1>
        {#if status.update}
          <div class="banner"><Download size={18} />{t('notify.update', { version: status.update.version })} <a href={status.update.url} target="_blank" rel="noopener">{t('settings.toRelease')}</a></div>
        {/if}
        {#each status.disk_warnings || [] as w}
          <div class="banner warn"><AlertTriangle size={18} />{t('admin.diskWarning', { path: w })}</div>
        {/each}
        <div class="tiles">
          <div class="card tile">
            <h3>Tailscale</h3>
            {#if status.tailscale.running}
              <p class="ok"><CheckCircle2 size={18} />{t('admin.tsConnected')}</p>
            {:else if status.tailscale.installed}
              <p class="bad"><AlertTriangle size={18} />{t('admin.tsNotConnected')}</p>
            {:else}
              <p class="bad"><XCircle size={18} />{t('admin.tsMissing')}</p>
            {/if}
            {#if status.tailscale.error}<p class="muted small">{status.tailscale.error}</p>{/if}
          </div>
          <div class="card tile">
            <h3>{t('admin.users')}</h3>
            <p class="num">{status.users}</p>
            <p class="muted small">{t('admin.connections', { n: status.connections })}</p>
          </div>
          <div class="card tile">
            <h3>{t('admin.version')}</h3>
            <p class="num">{status.version}</p>
            <p class="muted small">{status.os} · {t('admin.uptime')} {uptime(status.uptime_seconds)}</p>
          </div>
        </div>

        <div class="card">
          <h3>{t('admin.addresses')}</h3>
          <p class="muted small">{t('admin.addressesHint')}</p>
          {#each status.addresses || [] as a}
            <div class="addr"><code>{a}</code><button class="icon-btn small" onclick={() => copy(a)} aria-label={t('link.copy')}><Copy size={16} /></button></div>
          {:else}
            <p class="bad">{t('admin.noAddress')}</p>
          {/each}
          {#if status.https && status.https_error}<p class="bad small">HTTPS: {status.https_error}</p>{/if}
          <p class="muted small">{t('admin.listening')}: {(status.listening || []).join(', ')}</p>
        </div>

        <div class="card">
          <h3>{t('admin.locations')}</h3>
          {#each status.locations as l}
            <div class="loc">
              <span class="ellipsis"><code>{l.path}</code></span>
              <div class="progress" class:warn={pct(l) >= 80} class:full={pct(l) >= 90}><div style:width="{pct(l)}%"></div></div>
              <span class="muted small">{t('admin.freeOf', { free: bytes(l.free), total: bytes(l.total) })}</span>
            </div>
          {/each}
        </div>

        <div class="card">
          <h3>{t('admin.paths')}</h3>
          <p class="small">{t('admin.dataDir')}: <code>{status.data_dir}</code></p>
          <p class="small">{t('admin.configFile')}: <code>{status.config_file}</code></p>
          <p class="small">{t('admin.logFile')}: <code>{status.log_file}</code></p>
          <p class="small muted">{status.ffmpeg ? t('admin.ffmpegYes') : t('admin.ffmpegNo')}</p>
        </div>
      {:else if tab === 'users'}
        <div class="head"><h1>{t('admin.users')}</h1><button class="btn primary" onclick={newUser}><Plus size={16} />{t('admin.newUser')}</button></div>
        <table class="table card">
          <thead><tr><th>{t('login.username')}</th><th>{t('settings.displayName')}</th><th>{t('admin.role')}</th><th>{t('admin.usage')}</th><th></th></tr></thead>
          <tbody>
            {#each users as u (u.id)}
              <tr>
                <td><strong>{u.username}</strong></td>
                <td>{u.display_name}</td>
                <td>{u.is_admin ? 'Admin' : t('admin.user')}</td>
                <td>
                  <div class="usage">
                    {#if u.quota_bytes > 0}
                      <div class="progress" class:warn={u.used / u.quota_bytes > 0.8} class:full={u.used / u.quota_bytes > 0.9}><div style:width="{Math.min(100, (u.used / u.quota_bytes) * 100)}%"></div></div>
                      <span class="small muted">{bytes(u.used)} / {bytes(u.quota_bytes)}</span>
                    {:else}
                      <span class="small muted">{bytes(u.used)} · {t('storage.unlimited')}</span>
                    {/if}
                  </div>
                </td>
                <td class="act">
                  <button class="icon-btn small" onclick={() => editUser(u)} aria-label={t('action.rename')}><Pencil size={16} /></button>
                  <button class="icon-btn small" onclick={() => deleteUser(u)} aria-label={t('action.delete')}><Trash2 size={16} /></button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        <p class="muted small">{t('admin.defaultQuotaHint', { gb: defaultQuota })}</p>
      {:else if tab === 'teams' || tab === 'mounts'}
        {@const list = tab === 'teams' ? teams : mounts}
        <div class="head">
          <h1>{tab === 'teams' ? t('nav.teamDrives') : t('nav.mounts')}</h1>
          <button class="btn primary" onclick={() => newSpace(tab === 'teams' ? 'team' : 'mount')}><Plus size={16} />{tab === 'teams' ? t('admin.newTeam') : t('admin.newMount')}</button>
        </div>
        <p class="muted">{tab === 'teams' ? t('admin.teamsHint') : t('admin.mountsHint')}</p>
        {#each list as s (s.id)}
          <div class="card spacecard">
            <div class="row">
              <strong>{s.name}</strong>
              {#if s.root_path}<code>{s.root_path}</code>{/if}
              <span class="spacer"></span>
              <span class="muted small">{bytes(s.size)}</span>
              <button class="icon-btn small" onclick={() => editSpace(s)} aria-label={t('action.rename')}><Pencil size={16} /></button>
              <button class="icon-btn small" onclick={() => deleteSpace(s)} aria-label={t('action.delete')}><Trash2 size={16} /></button>
            </div>
            <div class="members">
              {#each s.members as m}<span class="chip">{m.username} · {m.can_write ? t('share.canEdit') : t('share.canView')}</span>{:else}<span class="muted small">{t('admin.noMembers')}</span>{/each}
            </div>
          </div>
        {:else}
          <div class="empty">{t('admin.noneYet')}</div>
        {/each}
      {:else if tab === 'storage'}
        <div class="head">
          <h1>{t('admin.locations')}</h1>
          <button class="btn primary" onclick={() => browse('', (p) => addLocation(p))}><Plus size={16} />{t('admin.addLocation')}</button>
        </div>
        <p class="muted">{t('admin.locationsHint')}</p>
        {#each locations as l}
          <div class="card loc big">
            <HardDrive size={22} />
            <span class="ellipsis"><code>{l.path}</code></span>
            <div class="progress" class:warn={pct(l) >= 80} class:full={pct(l) >= 90}><div style:width="{pct(l)}%"></div></div>
            <span class="muted small">{t('admin.freeOf', { free: bytes(l.free), total: bytes(l.total) })}</span>
            {#if locations.length > 1}<button class="icon-btn small" onclick={() => removeLocation(l.path)} aria-label={t('action.delete')}><Trash2 size={16} /></button>{/if}
          </div>
        {/each}
      {:else if tab === 'settings' && settings}
        <h1>{t('nav.settings')}</h1>
        <div class="card form">
          <label class="field">{t('admin.port')}<input class="input" type="number" min="1" max="65535" bind:value={settings.port} /></label>
          <label class="field">{t('admin.defaultQuota')}<input class="input" type="number" min="0" bind:value={settings.default_quota_gb} /></label>
          <label class="field">{t('admin.trashDays')}<input class="input" type="number" min="1" bind:value={settings.trash_days} /></label>
          <label class="field">{t('admin.scanInterval')}<input class="input" type="number" min="1" bind:value={settings.scan_interval_minutes} /></label>
          <label class="field">{t('settings.language')}
            <select class="input" bind:value={settings.language}><option value="de">Deutsch</option><option value="en">English</option></select>
          </label>
          <label class="check"><input type="checkbox" bind:checked={settings.update_check} />{t('admin.updateCheck')}</label>
          <label class="check"><input type="checkbox" bind:checked={settings.https} />{t('admin.https')}</label>
          <p class="muted small">{t('admin.httpsHint')}</p>
          <div class="row">
            <button class="btn" onclick={rescan}><RefreshCw size={16} />{t('admin.rescan')}</button>
            <span class="spacer"></span>
            <button class="btn primary" onclick={saveSettings}>{t('common.save')}</button>
          </div>
        </div>
      {:else if tab === 'logs'}
        <div class="head">
          <h1>{t('admin.logs')}</h1>
          <select class="input" style="width:160px" bind:value={logLevel}>
            <option value="">{t('admin.allLevels')}</option>
            <option value="ERROR">{t('admin.errors')}</option>
            <option value="WARN">{t('admin.warnings')}</option>
            <option value="INFO">Info</option>
          </select>
        </div>
        <div class="logs card">
          {#each shownLogs as l (l.seq)}
            <div class="logline {l.level.toLowerCase()}">{l.text}</div>
          {:else}
            <p class="muted">{t('admin.noLogs')}</p>
          {/each}
        </div>
      {/if}
    </main>
  </div>
{/if}

{#if userEdit}
  {@const u = userEdit}
  <Modal title={u.isNew ? t('admin.newUser') : t('admin.editUser', { name: u.username })} onclose={() => (userEdit = null)} width="440px">
    {#if u.isNew}<label class="field">{t('login.username')}<input class="input" bind:value={u.username} autocapitalize="off" /></label>{/if}
    <label class="field">{t('settings.displayName')}<input class="input" bind:value={u.display_name} /></label>
    <label class="field">{u.isNew ? t('login.password') : t('admin.newPasswordOptional')}<input class="input" type="password" bind:value={u.password} autocomplete="new-password" /></label>
    <label class="field">{t('admin.quotaGb')}<input class="input" type="number" min="0" bind:value={u.quota_gb} /></label>
    <label class="check"><input type="checkbox" bind:checked={u.is_admin} />{t('admin.isAdmin')}</label>
    {#snippet footer()}
      <button class="btn ghost" onclick={() => (userEdit = null)}>{t('common.cancel')}</button>
      <button class="btn primary" onclick={saveUser}>{t('common.save')}</button>
    {/snippet}
  </Modal>
{/if}

{#if spaceEdit}
  {@const s = spaceEdit}
  <Modal title={s.isNew ? (s.kind === 'team' ? t('admin.newTeam') : t('admin.newMount')) : s.name} onclose={() => (spaceEdit = null)} width="520px">
    <label class="field">{t('admin.name')}<input class="input" bind:value={s.name} /></label>
    {#if s.kind === 'mount'}
      <label class="field">{t('admin.folderOnServer')}
        <div class="row">
          <input class="input" bind:value={s.root_path} disabled={!s.isNew} placeholder={navigator.platform.startsWith('Win') ? 'D:\\Fotos' : '/mnt/daten/fotos'} />
          {#if s.isNew}<button class="btn" onclick={() => browse(s.root_path || '', (p) => (s.root_path = p))}><FolderOpen size={16} /></button>{/if}
        </div>
      </label>
    {/if}
    <h3>{t('admin.access')}</h3>
    {#each s.members as m}
      <div class="member">
        <label class="check"><input type="checkbox" bind:checked={m.on} />{m.username}</label>
        <span class="spacer"></span>
        <select class="input perm" bind:value={m.can_write} disabled={!m.on}>
          <option value={true}>{t('share.canEdit')}</option>
          <option value={false}>{t('share.canView')}</option>
        </select>
      </div>
    {/each}
    {#snippet footer()}
      <button class="btn ghost" onclick={() => (spaceEdit = null)}>{t('common.cancel')}</button>
      <button class="btn primary" onclick={saveSpace}>{t('common.save')}</button>
    {/snippet}
  </Modal>
{/if}

{#if picker}
  {@const p = picker}
  <Modal title={t('admin.chooseFolder')} onclose={() => (picker = null)} width="520px">
    <div class="row"><code class="ellipsis">{p.path || t('admin.drives')}</code></div>
    <div class="dirs">
      {#if p.path}<button class="dir" onclick={() => browse(p.parent, p.onpick)}><ChevronUp size={18} />..</button>{/if}
      {#each p.dirs as d}
        <button class="dir" onclick={() => browse(d.path, p.onpick)}><FolderOpen size={18} />{d.name}</button>
      {/each}
    </div>
    {#snippet footer()}
      <button class="btn ghost" onclick={() => (picker = null)}>{t('common.cancel')}</button>
      <button class="btn primary" disabled={!p.path} onclick={() => { p.onpick(p.path); picker = null }}>{t('admin.useFolder')}</button>
    {/snippet}
  </Modal>
{/if}

<Dialogs />

<style>
  .center {
    min-height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
  }
  .auth {
    width: 400px;
    max-width: 100%;
    display: flex;
    flex-direction: column;
    align-items: stretch;
    text-align: center;
    gap: 4px;
    padding: 32px;
  }
  .auth :global(svg) {
    align-self: center;
  }
  .auth label {
    text-align: left;
  }
  .langs {
    display: flex;
    justify-content: center;
    gap: 6px;
    margin-top: 12px;
  }
  .langs button {
    border: none;
    background: none;
    cursor: pointer;
    color: var(--text-3);
    padding: 4px 8px;
    border-radius: 6px;
  }
  .langs button.active {
    color: var(--brand);
    background: var(--brand-soft);
  }
  .layout {
    display: flex;
    height: 100%;
  }
  .side {
    width: 230px;
    flex: none;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 16px 10px;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    font-weight: 600;
    margin: 0 8px 18px;
    line-height: 1.2;
  }
  .brand small {
    font-weight: 400;
    color: var(--text-2);
  }
  .side button {
    display: flex;
    align-items: center;
    gap: 12px;
    border: none;
    background: none;
    height: 38px;
    padding: 0 14px;
    border-radius: 19px;
    cursor: pointer;
    color: var(--text);
    text-align: left;
  }
  .side button:hover {
    background: var(--hover);
  }
  .side button.active {
    background: var(--selected);
    color: var(--brand-strong);
    font-weight: 600;
  }
  main {
    flex: 1;
    overflow: auto;
    padding: 24px 28px 60px;
    background: var(--surface);
    border-radius: 16px 0 0 0;
  }
  main > :global(.card) {
    margin-bottom: 14px;
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 8px;
  }
  .head h1 {
    margin: 0;
  }
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: 14px;
    margin-bottom: 14px;
  }
  .tile .num {
    font-size: 24px;
    font-weight: 600;
    margin: 0;
  }
  .ok,
  .bad {
    display: flex;
    align-items: center;
    gap: 8px;
    font-weight: 500;
  }
  .ok {
    color: var(--success);
  }
  .bad {
    color: var(--danger);
  }
  .banner {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 16px;
    background: var(--brand-soft);
    border-radius: 10px;
    margin-bottom: 12px;
  }
  .banner.warn {
    background: var(--danger-soft);
    color: var(--danger);
  }
  .addr {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 6px 0;
  }
  .addr code {
    font-size: 14px;
  }
  .loc {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 200px auto;
    align-items: center;
    gap: 14px;
    margin: 10px 0;
  }
  .loc.big {
    grid-template-columns: auto minmax(0, 1fr) 200px auto auto;
    margin: 0 0 10px;
  }
  .usage {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 160px;
  }
  .act {
    text-align: right;
    white-space: nowrap;
  }
  .spacecard {
    margin-bottom: 10px;
  }
  .members {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 10px;
  }
  .member {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .perm {
    width: 160px;
  }
  .form {
    max-width: 520px;
  }
  .logs {
    font-family: var(--mono);
    font-size: 12px;
    max-height: calc(100vh - 160px);
    overflow: auto;
    padding: 10px 14px;
  }
  .logline {
    white-space: pre-wrap;
    word-break: break-all;
    padding: 2px 0;
    border-bottom: 1px solid var(--border);
  }
  .logline.error {
    color: var(--danger);
  }
  .logline.warn {
    color: var(--warning);
  }
  .dirs {
    height: 320px;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: 10px;
    margin-top: 10px;
  }
  .dir {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    border: none;
    background: none;
    padding: 8px 14px;
    cursor: pointer;
    text-align: left;
    color: var(--text);
  }
  .dir:hover {
    background: var(--hover);
  }
  @media (max-width: 760px) {
    .side {
      width: 64px;
    }
    .side button {
      font-size: 0;
      gap: 0;
      justify-content: center;
      padding: 0;
    }
    .brand span {
      display: none;
    }
    .loc,
    .loc.big {
      grid-template-columns: 1fr;
    }
  }
</style>
