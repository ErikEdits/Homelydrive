<script lang="ts">
  import { onMount } from 'svelte'
  import {
    LayoutDashboard, FolderUp, Files, History, Settings, Pause, Play, Globe, HardDrive, Plus, Trash2, RotateCw,
    CheckCircle2, AlertTriangle, WifiOff, Download, Upload, FolderPlus, ArrowLeft, ChevronRight, LogOut, Gauge,
    Bell, MousePointerClick, Info, Users, FolderInput, Share2, RefreshCw,
  } from '@lucide/svelte'
  import Logo from '../lib/ui/Logo.svelte'
  import Dialogs from '../lib/ui/Dialogs.svelte'
  import FileIcon from '../lib/ui/FileIcon.svelte'
  import { confirmDialog, promptDialog, toast } from '../lib/ui/ui.svelte'
  import { i18n, setLang, t } from '../lib/i18n.svelte'
  import { bytes, dateTime, relative } from '../lib/format'
  import { local, type Folder, type HistoryEntry, type Status } from './api'
  import LocalPicker from './LocalPicker.svelte'
  import RemotePicker from './RemotePicker.svelte'

  type Tab = 'overview' | 'folders' | 'files' | 'history' | 'settings'
  const initialTab = (location.hash.replace('#/', '') as Tab) || 'overview'
  let tab = $state<Tab>(['overview', 'folders', 'files', 'history', 'settings'].includes(initialTab) ? initialTab : 'overview')
  let st = $state<Status | null>(null)
  let login = $state({ server: '', username: '', password: '' })
  let loginError = $state('')
  let busy = $state(false)

  // folders
  let folders = $state<Folder[]>([])
  let adding = $state<{ local: string; space: string; remote: string } | null>(null)
  let pickLocal = $state(false)
  let pickRemote = $state<null | 'folder' | 'upload'>(null)

  // files
  let fSpace = $state<string | null>('home')
  let fPath = $state('')
  let fEntries = $state<any[]>([])
  let fRoots = $state<any[]>([])
  let fileInput: HTMLInputElement | undefined = $state()
  let putting = $state<{ name: string; pct: number }[]>([])

  // history
  let history = $state<HistoryEntry[]>([])
  let onlyErrors = $state(false)

  onMount(() => {
    const es = new EventSource('/local/events')
    es.onmessage = (m) => {
      st = JSON.parse(m.data)
      if (st?.config.language && st.config.language !== i18n.lang) setLang(st.config.language)
      if (st && !login.server && st.config.server) login.server = st.config.server.replace(/^https?:\/\//, '').replace(/:8420$/, '')
    }
    return () => es.close()
  })

  $effect(() => {
    if (!st?.engine.logged_in) return
    if (tab === 'folders') loadFolders()
    if (tab === 'history') loadHistory()
    if (tab === 'files') loadFiles()
  })

  async function doLogin(e: Event) {
    e.preventDefault()
    loginError = ''
    busy = true
    try {
      await local('POST', '/login', login)
      login.password = ''
      tab = 'folders'
    } catch (err) {
      loginError = (err as Error).message
    } finally {
      busy = false
    }
  }

  async function logout() {
    if (!(await confirmDialog(t('nav.logout'), t('client.logoutConfirm')))) return
    await local('POST', '/logout')
  }

  const run = async (fn: () => Promise<any>, okMsg?: string) => {
    try {
      const r = await fn()
      if (okMsg) toast(okMsg, 'success')
      return r
    } catch (e) {
      toast((e as Error).message, 'error')
    }
  }

  // ----- folders -----
  async function loadFolders() {
    folders = (await run(() => local('GET', '/folders'))) || []
  }
  function startAdd() {
    adding = { local: '', space: 'home', remote: '' }
    pickLocal = true
  }
  async function saveFolder() {
    if (!adding) return
    const r = await run(() => local('POST', '/folders', adding), t('client.folderAdded'))
    if (r) {
      adding = null
      loadFolders()
    }
  }
  async function toggleFolder(f: Folder) {
    await run(() => local('PUT', '/folders/' + f.id, { enabled: !f.enabled }))
    loadFolders()
  }
  async function removeFolder(f: Folder) {
    if (!(await confirmDialog(t('client.removeFolder'), t('client.removeFolderText', { path: f.local })))) return
    await run(() => local('DELETE', '/folders/' + f.id))
    loadFolders()
  }

  // ----- files -----
  async function loadFiles() {
    if (fSpace === null) {
      fRoots = (await run(() => local('GET', '/roots'))) || []
      return
    }
    const r = await run(() => local('GET', `/remote/list?space=${encodeURIComponent(fSpace!)}&path=${encodeURIComponent(fPath)}`))
    fEntries = r?.entries || []
  }
  $effect(() => {
    fSpace
    fPath
    if (tab === 'files' && st?.engine.logged_in) loadFiles()
  })
  async function downloadEntry(e: any) {
    const r = await run(() => local('POST', '/download', { space: fSpace, path: e.path, is_dir: e.is_dir }))
    if (r) toast(t('client.downloading', { name: e.name }))
  }
  async function newRemoteFolder() {
    const name = await promptDialog(t('action.newFolder'), t('action.newFolderDefault'))
    if (!name || fSpace === null) return
    await run(() => local('POST', '/remote/mkdir', { space: fSpace, path: fPath ? fPath + '/' + name : name }))
    loadFiles()
  }
  async function putFiles(list: FileList | null) {
    if (!list || fSpace === null) return
    for (const file of Array.from(list)) {
      const item = { name: file.name, pct: 0 }
      putting.push(item)
      const target = fPath ? fPath + '/' + file.name : file.name
      await new Promise<void>((resolve) => {
        const xhr = new XMLHttpRequest()
        xhr.open('POST', `/local/put?space=${encodeURIComponent(fSpace!)}&path=${encodeURIComponent(target)}&mtime=${file.lastModified}`)
        xhr.setRequestHeader('X-HD', '1')
        xhr.upload.onprogress = (e) => (item.pct = Math.round((e.loaded / e.total) * 100))
        xhr.onload = () => {
          if (xhr.status >= 300) toast(JSON.parse(xhr.responseText || '{}').error || xhr.statusText, 'error')
          resolve()
        }
        xhr.onerror = () => {
          toast(t('err.network'), 'error')
          resolve()
        }
        xhr.send(file)
      })
      putting.splice(putting.indexOf(item), 1)
    }
    loadFiles()
  }
  async function copyLink(e: any) {
    const r = await run(() => local('POST', '/link', { space: fSpace, remote: e.path }))
    if (r) toast(r.copied ? t('client.linkCopied') : r.url, 'success')
  }

  // ----- history -----
  async function loadHistory() {
    history = (await run(() => local('GET', '/history' + (onlyErrors ? '?errors=1' : '')))) || []
  }
  async function retry(id: number, all = false) {
    await run(() => local('POST', '/history/retry', { id, all }), t('client.retrying'))
    loadHistory()
  }

  // ----- settings -----
  // Logarithmic slider: 0 = 64 KB/s ... 99 = ~100 MB/s, 100 = unlimited.
  const posToKBps = (pos: number) => (pos >= 100 ? 0 : Math.round(64 * Math.pow(2, (pos / 99) * 10.6)))
  const kbpsToPos = (k: number) => (k <= 0 ? 100 : Math.max(0, Math.min(99, Math.round((Math.log2(k / 64) / 10.6) * 99))))
  let bwPos = $state(100)
  let bwSynced = false
  $effect(() => {
    if (st && !bwSynced) {
      bwPos = kbpsToPos(st.config.bandwidth_kbps)
      bwSynced = true
    }
  })
  const bwLabel = $derived(bwPos >= 100 ? t('client.unlimited') : bytes(posToKBps(bwPos) * 1024) + '/s')
  let bwTimer: any
  function onBandwidth() {
    clearTimeout(bwTimer)
    bwTimer = setTimeout(() => local('PUT', '/settings', { bandwidth_kbps: posToKBps(bwPos) }), 300)
  }
  async function setting(body: Record<string, unknown>) {
    await run(() => local('PUT', '/settings', body), t('settings.saved'))
  }
  async function setDrive(enabled: boolean, mount?: string) {
    busy = true
    await run(() => local('POST', '/drive', { enabled, mount }), enabled ? t('client.driveMounted') : undefined)
    busy = false
  }

  const roots = $derived(fRoots)
  const pct = $derived(st?.user && st.user.quota > 0 ? Math.min(100, (st.user.used / st.user.quota) * 100) : 0)
  const driveLetters = 'DEFGHIJKLMNOPQRSTUVWXYZ'.split('').map((c) => c + ':')
  const rootIcon = (k: string) => (k === 'home' ? HardDrive : k === 'team' ? Users : k === 'mount' ? FolderInput : Share2)
</script>

{#if !st}
  <div class="center">…</div>
{:else if !st.engine.logged_in}
  <div class="center">
    <form class="card auth" onsubmit={doLogin}>
      <Logo size={56} />
      <h1>Homelydrive</h1>
      <p class="muted">{t('client.loginText')}</p>
      <label class="field">{t('client.server')}
        <input class="input" bind:value={login.server} placeholder="meinpc.tail1234.ts.net" required autocapitalize="off" />
        <span class="hint">{t('client.serverHint')}</span>
      </label>
      <label class="field">{t('login.username')}<input class="input" bind:value={login.username} required autocapitalize="off" /></label>
      <label class="field">{t('login.password')}<input class="input" type="password" bind:value={login.password} required /></label>
      {#if loginError}<p class="error-text">{loginError}</p>{/if}
      <button class="btn primary" disabled={busy}>{t('login.submit')}</button>
      <div class="langs">
        <button type="button" class:active={i18n.lang === 'de'} onclick={() => { setLang('de'); local('PUT', '/settings', { language: 'de' }) }}>Deutsch</button>
        <button type="button" class:active={i18n.lang === 'en'} onclick={() => { setLang('en'); local('PUT', '/settings', { language: 'en' }) }}>English</button>
      </div>
    </form>
  </div>
{:else}
  <div class="layout">
    <nav class="side">
      <div class="brand"><Logo size={30} /><span>Homelydrive<br /><small>{t('client.pcApp')}</small></span></div>
      <button class:active={tab === 'overview'} onclick={() => (tab = 'overview')}><LayoutDashboard size={19} />{t('admin.overview')}</button>
      <button class:active={tab === 'folders'} onclick={() => (tab = 'folders')}><FolderUp size={19} />{t('client.folders')}</button>
      <button class:active={tab === 'files'} onclick={() => (tab = 'files')}><Files size={19} />{t('client.files')}</button>
      <button class:active={tab === 'history'} onclick={() => (tab = 'history')}><History size={19} />{t('client.history')}</button>
      <button class:active={tab === 'settings'} onclick={() => (tab = 'settings')}><Settings size={19} />{t('nav.settings')}</button>
      <span class="spacer"></span>
      <div class="state">
        {#if st.engine.paused}<Pause size={16} />{t('client.paused')}
        {:else if !st.engine.online}<WifiOff size={16} />{t('client.offline')}
        {:else if st.engine.active.length || st.engine.queue}<Upload size={16} />{t('client.uploading')}
        {:else}<CheckCircle2 size={16} />{t('client.allDone')}{/if}
      </div>
    </nav>

    <main>
      {#if st.update}
        <div class="banner"><Download size={18} />{t('notify.update', { version: st.update.version })}
          <button class="btn small" onclick={() => local('POST', '/open', { target: 'release' })}>{t('settings.toRelease')}</button></div>
      {/if}

      {#if tab === 'overview'}
        <h1>{t('admin.overview')}</h1>
        <div class="tiles">
          <div class="card tile">
            <h3>{t('client.connection')}</h3>
            {#if st.engine.online && st.connected}<p class="ok"><CheckCircle2 size={18} />{t('admin.tsConnected')}</p>
            {:else if st.engine.online}<p class="ok"><CheckCircle2 size={18} />{t('client.reachable')}</p>
            {:else}<p class="bad"><WifiOff size={18} />{t('client.offline')}</p>{/if}
            <p class="muted small ellipsis">{st.server} · {st.username}</p>
          </div>
          <div class="card tile">
            <h3>{t('nav.storage')}</h3>
            {#if st.user}
              {#if st.user.quota > 0}
                <div class="progress" class:warn={pct >= 80} class:full={pct >= 95}><div style:width="{pct}%"></div></div>
                <p class="muted small">{t('storage.usedOf', { used: bytes(st.user.used), quota: bytes(st.user.quota) })}</p>
              {:else}<p class="muted small">{t('storage.usedUnlimited', { used: bytes(st.user.used) })}</p>{/if}
            {/if}
          </div>
          <div class="card tile">
            <h3>{t('client.thisSession')}</h3>
            <p class="num">{st.engine.uploaded}</p>
            <p class="muted small">{t('client.filesUploaded', { size: bytes(st.engine.uploaded_bytes) })}</p>
          </div>
        </div>

        <div class="card">
          <div class="row">
            <h3 style="margin:0">{t('client.uploads')}</h3>
            <span class="spacer"></span>
            {#if st.engine.paused}
              <button class="btn primary small" onclick={() => local('POST', '/pause', { paused: false })}><Play size={16} />{t('client.resume')}</button>
            {:else}
              <button class="btn small" onclick={() => local('POST', '/pause', { paused: true })}><Pause size={16} />{t('client.pause')}</button>
            {/if}
          </div>
          {#if st.engine.active.length === 0 && st.engine.queue === 0}
            <p class="muted">{st.engine.scanning ? t('client.scanning') : st.config.folders.length ? t('client.nothingToDo') : t('client.noFolders')}</p>
          {/if}
          {#each st.engine.active as a (a.local)}
            <div class="active">
              <FileIcon name={a.name} size={20} />
              <div class="spacer">
                <div class="row"><span class="ellipsis">{a.name}</span><span class="spacer"></span>
                  <span class="muted small">{a.phase === 'hashing' ? t('client.checking') : bytes(a.done) + ' / ' + bytes(a.size) + (a.speed ? ' · ' + bytes(a.speed) + '/s' : '')}</span></div>
                <div class="progress"><div style:width="{a.size ? (a.done / a.size) * 100 : 0}%"></div></div>
              </div>
            </div>
          {/each}
          {#if st.engine.queue}<p class="muted small">{t('client.queued', { n: st.engine.queue })}</p>{/if}
          {#if st.engine.last_error}<p class="error-text small">{st.engine.last_error}</p>{/if}
        </div>

        <div class="row wrap">
          <button class="btn" onclick={() => local('POST', '/open', { target: 'web' })}><Globe size={16} />{t('client.openWeb')}</button>
          {#if st.drive_mounted}<button class="btn" onclick={() => local('POST', '/open', { target: 'drive' })}><HardDrive size={16} />{t('client.openDrive')}</button>{/if}
          <button class="btn" onclick={() => { tab = 'folders'; startAdd() }}><Plus size={16} />{t('client.addFolder')}</button>
        </div>
      {:else if tab === 'folders'}
        <div class="head"><h1>{t('client.folders')}</h1><button class="btn primary" onclick={startAdd}><Plus size={16} />{t('client.addFolder')}</button></div>
        <p class="muted">{t('client.foldersHint')}</p>
        {#each folders as f (f.id)}
          <div class="card folder">
            <FolderUp size={22} color="var(--brand)" />
            <div class="spacer info">
              <strong class="ellipsis">{f.local}</strong>
              <span class="muted small">→ {f.space === 'home' ? t('nav.myDrive') : f.space} / {f.remote} · {t('client.folderStats', { n: f.files, size: bytes(f.bytes) })}</span>
              {#if f.error}<span class="error-text small">{f.error}</span>{/if}
            </div>
            <label class="switch" title={t('client.active')}><input type="checkbox" checked={f.enabled} onchange={() => toggleFolder(f)} /><span></span></label>
            <button class="icon-btn small" onclick={() => removeFolder(f)} aria-label={t('action.delete')}><Trash2 size={16} /></button>
          </div>
        {:else}
          <div class="empty"><FolderUp size={48} strokeWidth={1.2} />{t('client.noFolders')}</div>
        {/each}
        <button class="btn ghost small" onclick={() => local('POST', '/rescan').then(() => toast(t('admin.rescanStarted')))}><RefreshCw size={14} />{t('admin.rescan')}</button>
      {:else if tab === 'files'}
        <div class="head">
          <div class="crumbs">
            {#if fSpace !== null}
              <button class="icon-btn small" onclick={() => (fPath ? (fPath = fPath.includes('/') ? fPath.slice(0, fPath.lastIndexOf('/')) : '') : (fSpace = null))} aria-label={t('common.back')}><ArrowLeft size={18} /></button>
              <h1 class="ellipsis">{fSpace === 'home' ? t('nav.myDrive') : fSpace}{fPath ? ' / ' + fPath.split('/').join(' / ') : ''}</h1>
            {:else}<h1>{t('client.files')}</h1>{/if}
          </div>
          {#if fSpace !== null}
            <div class="row">
              <button class="btn small" onclick={newRemoteFolder}><FolderPlus size={16} />{t('action.newFolder')}</button>
              <button class="btn primary small" onclick={() => fileInput?.click()}><Upload size={16} />{t('action.uploadFiles')}</button>
            </div>
          {/if}
        </div>
        {#each putting as p}<div class="progress mb"><div style:width="{p.pct}%"></div></div><p class="small muted">{p.name}</p>{/each}
        <table class="table">
          <tbody>
            {#if fSpace === null}
              {#each roots as r (r.ref)}
                {@const I = rootIcon(r.kind)}
                <tr><td><button class="name" onclick={() => { fSpace = r.ref; fPath = '' }}><I size={20} />{r.ref === 'home' ? t('nav.myDrive') : r.name}<ChevronRight size={16} /></button></td></tr>
              {/each}
            {:else}
              {#each fEntries as e (e.path)}
                <tr>
                  <td><button class="name" onclick={() => e.is_dir && (fPath = e.path)}><FileIcon name={e.name} isDir={e.is_dir} category={e.category} size={20} /><span class="ellipsis">{e.name}</span></button></td>
                  <td class="muted small">{dateTime(e.mtime)}</td>
                  <td class="muted small">{e.is_dir ? '' : bytes(e.size)}</td>
                  <td class="act">
                    <button class="icon-btn small" title={t('action.download')} onclick={() => downloadEntry(e)}><Download size={16} /></button>
                    <button class="icon-btn small" title={t('action.getLink')} onclick={() => copyLink(e)}><Share2 size={16} /></button>
                  </td>
                </tr>
              {:else}
                <tr><td class="muted">{t('files.emptyFolder')}</td></tr>
              {/each}
            {/if}
          </tbody>
        </table>
        <p class="muted small">{t('client.downloadsTo', { dir: st.config.download_dir })} <button class="btn ghost small" onclick={() => local('POST', '/open', { target: 'downloads' })}>{t('action.open')}</button></p>
        <input type="file" multiple hidden bind:this={fileInput} onchange={(e) => { putFiles(e.currentTarget.files); e.currentTarget.value = '' }} />
      {:else if tab === 'history'}
        <div class="head">
          <h1>{t('client.history')}</h1>
          <div class="row">
            <label class="check"><input type="checkbox" bind:checked={onlyErrors} onchange={loadHistory} />{t('client.onlyErrors')}</label>
            <button class="btn small" onclick={() => retry(0, true)}><RotateCw size={16} />{t('client.retryAll')}</button>
            <button class="btn ghost small" onclick={async () => { await local('POST', '/history/clear'); loadHistory() }}><Trash2 size={16} />{t('common.clear')}</button>
          </div>
        </div>
        <table class="table">
          <tbody>
            {#each history as h (h.id)}
              <tr>
                <td class="muted small nowrap">{relative(h.time)}</td>
                <td>
                  <div class="ellipsis" title={h.local}>{h.local.split(/[\\/]/).pop()}</div>
                  <div class="muted small ellipsis">{h.remote || h.local}</div>
                  {#if h.error}<div class="error-text small">{h.error}</div>{/if}
                </td>
                <td class="muted small nowrap">{h.size ? bytes(h.size) : ''}</td>
                <td class="nowrap">
                  {#if h.status === 'error'}
                    <span class="bad small"><AlertTriangle size={14} />{t('client.failed')}</span>
                    <button class="icon-btn small" onclick={() => retry(h.id)} title={t('upload.retry')}><RotateCw size={15} /></button>
                  {:else if h.status === 'exists'}<span class="muted small">{t('upload.exists')}</span>
                  {:else if h.status === 'retried'}<span class="muted small">{t('client.retried')}</span>
                  {:else}<span class="ok small"><CheckCircle2 size={14} />{t('client.uploaded')}</span>{/if}
                </td>
              </tr>
            {:else}
              <tr><td class="muted">{t('client.noHistory')}</td></tr>
            {/each}
          </tbody>
        </table>
      {:else if tab === 'settings'}
        <h1>{t('nav.settings')}</h1>
        <section class="card">
          <h2><Gauge size={18} />{t('client.speed')}</h2>
          <p class="muted small">{t('client.speedHint')}</p>
          <div class="slider">
            <input type="range" min="0" max="100" bind:value={bwPos} oninput={onBandwidth} />
            <strong>{bwLabel}</strong>
          </div>
        </section>

        <section class="card">
          <h2><HardDrive size={18} />{t('client.drive')}</h2>
          <p class="muted small">{t('client.driveHint')}</p>
          {#if !st.drive_supported}
            <p class="error-text">{t('client.driveUnsupported')}</p>
          {:else}
            <div class="row wrap">
              <label class="check"><input type="checkbox" checked={st.config.drive_enabled} disabled={busy} onchange={(e) => setDrive(e.currentTarget.checked)} />{t('client.driveEnable')}</label>
              {#if st.os === 'windows'}
                <select class="input narrow" value={st.config.drive_mount} onchange={(e) => setDrive(st!.config.drive_enabled, e.currentTarget.value)}>
                  {#each driveLetters as l}<option value={l}>{l}</option>{/each}
                </select>
              {:else}
                <code>{st.config.drive_mount}</code>
              {/if}
            </div>
            {#if st.drive_error}<p class="error-text small">{st.drive_error}</p>{/if}
            {#if st.drive_mounted}<p class="ok small"><CheckCircle2 size={14} />{t('client.driveActive', { mount: st.config.drive_mount })}</p>{/if}
            <p class="muted small">{t('client.cache', { size: bytes(st.cache_size), max: bytes(st.config.drive_cache_mb * 1024 * 1024) })}
              <button class="btn ghost small" onclick={() => local('POST', '/drive/clear-cache')}>{t('client.clearCache')}</button></p>
          {/if}
        </section>

        <section class="card">
          <h2><MousePointerClick size={18} />{t('client.contextMenu')}</h2>
          <p class="muted small">{t('client.contextMenuHint')}</p>
          <label class="check"><input type="checkbox" checked={st.config.integration} onchange={(e) => local('POST', '/integration', { install: e.currentTarget.checked })} />{t('client.contextMenuEnable')}</label>
          <label class="field">{t('client.uploadTarget')}
            <div class="row">
              <input class="input" value={st.config.upload_folder} onchange={(e) => setting({ upload_folder: e.currentTarget.value })} />
              <button class="btn" onclick={() => (pickRemote = 'upload')}>…</button>
            </div>
          </label>
        </section>

        <section class="card">
          <h2><Bell size={18} />{t('client.general')}</h2>
          <label class="check"><input type="checkbox" checked={st.config.notifications} onchange={(e) => setting({ notifications: e.currentTarget.checked })} />{t('client.notifications')}</label>
          <label class="check"><input type="checkbox" checked={st.config.update_check} onchange={(e) => setting({ update_check: e.currentTarget.checked })} />{t('admin.updateCheck')}</label>
          <div class="grid2">
            <label class="field">{t('settings.language')}
              <select class="input" value={i18n.lang} onchange={(e) => { setLang(e.currentTarget.value as any); setting({ language: e.currentTarget.value }) }}>
                <option value="de">Deutsch</option><option value="en">English</option>
              </select>
            </label>
            <label class="field">{t('client.downloadDir')}
              <div class="row"><input class="input" value={st.config.download_dir} onchange={(e) => setting({ download_dir: e.currentTarget.value })} /></div>
            </label>
          </div>
        </section>

        <section class="card">
          <h2><Info size={18} />{t('settings.about')}</h2>
          <p>Homelydrive {st.version} · {st.config.device_name}</p>
          <p class="muted small">{t('client.account', { user: st.username, server: st.server })}</p>
          <button class="btn" onclick={logout}><LogOut size={16} />{t('nav.logout')}</button>
        </section>
      {/if}
    </main>
  </div>
{/if}

{#if pickLocal}
  <LocalPicker
    onclose={() => { pickLocal = false; if (adding && !adding.local) adding = null }}
    onpick={(p) => {
      pickLocal = false
      if (adding) {
        adding.local = p
        adding.remote = 'PC/' + p.split(/[\\/]/).filter(Boolean).pop()
      }
    }}
  />
{/if}
{#if adding && adding.local && !pickLocal}
  {@const ad = adding}
  <div class="backdrop">
    <div class="card dialog">
      <h2>{t('client.addFolder')}</h2>
      <label class="field">{t('client.localFolder')}<div class="row"><input class="input" bind:value={ad.local} /><button class="btn" onclick={() => (pickLocal = true)}>…</button></div></label>
      <label class="field">{t('client.target')}
        <div class="row">
          <input class="input" bind:value={ad.remote} />
          <button class="btn" onclick={() => (pickRemote = 'folder')}>…</button>
        </div>
        <span class="hint">{ad.space === 'home' ? t('nav.myDrive') : ad.space} / {ad.remote}</span>
      </label>
      <p class="muted small">{t('client.uploadOnlyHint')}</p>
      <div class="row"><span class="spacer"></span>
        <button class="btn ghost" onclick={() => (adding = null)}>{t('common.cancel')}</button>
        <button class="btn primary" onclick={saveFolder}>{t('client.addFolder')}</button>
      </div>
    </div>
  </div>
{/if}
{#if pickRemote}
  <RemotePicker
    initial={adding?.remote.split('/').pop()}
    onclose={() => (pickRemote = null)}
    onpick={(space, path) => {
      if (pickRemote === 'folder' && adding) {
        adding.space = space
        adding.remote = path
      } else if (pickRemote === 'upload') {
        setting({ upload_folder: path })
      }
      pickRemote = null
    }}
  />
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
    width: 420px;
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
  .hint {
    font-size: 12px;
    font-weight: 400;
    color: var(--text-3);
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
    width: 220px;
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
  .state {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 14px;
    color: var(--text-2);
    font-size: 13px;
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
    flex-wrap: wrap;
  }
  .head h1 {
    margin: 0;
  }
  .crumbs {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
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
    display: inline-flex;
    align-items: center;
    gap: 6px;
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
    padding: 10px 16px;
    background: var(--brand-soft);
    border-radius: 10px;
    margin-bottom: 14px;
  }
  .active {
    display: flex;
    align-items: center;
    gap: 12px;
    margin: 12px 0;
  }
  .active .progress {
    margin-top: 6px;
  }
  .row.wrap {
    flex-wrap: wrap;
  }
  .folder {
    display: flex;
    align-items: center;
    gap: 14px;
    margin-bottom: 10px;
    padding: 14px 18px;
  }
  .info {
    display: flex;
    flex-direction: column;
    min-width: 0;
    gap: 2px;
  }
  .switch {
    position: relative;
    width: 40px;
    height: 22px;
    flex: none;
  }
  .switch input {
    opacity: 0;
    width: 0;
    height: 0;
  }
  .switch span {
    position: absolute;
    inset: 0;
    border-radius: 11px;
    background: var(--surface-3);
    cursor: pointer;
    transition: background 0.15s;
  }
  .switch span::after {
    content: '';
    position: absolute;
    left: 3px;
    top: 3px;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: #fff;
    transition: transform 0.15s;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.3);
  }
  .switch input:checked + span {
    background: var(--brand);
  }
  .switch input:checked + span::after {
    transform: translateX(18px);
  }
  .name {
    display: flex;
    align-items: center;
    gap: 12px;
    border: none;
    background: none;
    padding: 0;
    cursor: pointer;
    color: var(--text);
    max-width: 100%;
    text-align: left;
  }
  .act,
  .nowrap {
    white-space: nowrap;
    text-align: right;
  }
  .mb {
    margin: 8px 0 2px;
  }
  section {
    max-width: 760px;
  }
  section h2 {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 16px;
  }
  .slider {
    display: flex;
    align-items: center;
    gap: 16px;
  }
  .slider input {
    flex: 1;
    accent-color: var(--brand);
  }
  .slider strong {
    min-width: 110px;
    text-align: right;
  }
  .narrow {
    width: 90px;
  }
  .grid2 {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(10, 15, 25, 0.45);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 80;
    padding: 16px;
  }
  .dialog {
    width: 560px;
    max-width: 100%;
    box-shadow: var(--shadow-lg);
  }
</style>
