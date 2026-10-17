<script lang="ts">
  import { Download, Lock, FolderOpen, ChevronRight, Upload, CheckCircle2, AlertCircle, FileQuestion } from '@lucide/svelte'
  import Logo from '../lib/ui/Logo.svelte'
  import FileIcon from '../lib/ui/FileIcon.svelte'
  import { ApiError, qs } from '../lib/api'
  import { errText, i18n, setLang, t } from '../lib/i18n.svelte'
  import { bytes, date } from '../lib/format'
  import { filesFromDrop, filesFromInput, uploader, uploadEndpoint } from '../lib/upload.svelte'
  import type { FileInfo } from '../lib/types'

  const [, kindSeg, token] = location.pathname.split('/')
  const base = `/api/v1/public/${token}`

  let info = $state<any>(null)
  let error = $state('')
  let password = $state('')
  let pwError = $state('')
  let path = $state('')
  let entries = $state<FileInfo[]>([])
  let preview = $state<FileInfo | null>(null)
  let dragOver = $state(false)
  let fileInput: HTMLInputElement | undefined = $state()
  let folderInput: HTMLInputElement | undefined = $state()

  async function call(method: string, p: string, body?: unknown) {
    const res = await fetch(base + p, {
      method,
      headers: body ? { 'Content-Type': 'application/json' } : {},
      body: body ? JSON.stringify(body) : undefined,
    })
    const j = await res.json().catch(() => ({}))
    if (!res.ok) throw new ApiError(res.status, j.code || 'error', j.error || res.statusText)
    return j
  }

  async function load() {
    try {
      info = await call('GET', '')
      if (info.authorized && info.kind === 'download' && info.is_dir) list()
    } catch (e) {
      error = errText(e)
    }
  }
  load()

  async function auth(e: Event) {
    e.preventDefault()
    pwError = ''
    try {
      await call('POST', '/auth', { password })
      await load()
    } catch (err) {
      pwError = errText(err)
    }
  }

  async function list() {
    try {
      const r = await call('GET', '/list' + qs({ path }))
      entries = r.entries
    } catch (e) {
      error = errText(e)
    }
  }

  function open(f: FileInfo) {
    if (f.is_dir) {
      path = f.path
      list()
    } else preview = f
  }

  const contentUrl = (p: string, dl = false) => base + '/content' + qs({ path: p, download: dl ? 1 : undefined })
  const thumbUrl = (p: string) => base + '/thumb' + qs({ path: p, size: 256 })
  const crumbs = $derived(path.split('/').filter(Boolean))

  function upload(files: { file: File; relPath: string }[]) {
    if (files.length) uploader.add(files, { endpoint: uploadEndpoint(token), folder: '' })
  }

  async function onDrop(e: DragEvent) {
    e.preventDefault()
    dragOver = false
    if (e.dataTransfer) upload(await filesFromDrop(e.dataTransfer))
  }

  const isUpload = kindSeg === 'u'
</script>

<div class="page">
  <header>
    <Logo size={32} /><strong>Homelydrive</strong>
    <span class="spacer"></span>
    <button class="lang" onclick={() => setLang(i18n.lang === 'de' ? 'en' : 'de')}>{i18n.lang === 'de' ? 'English' : 'Deutsch'}</button>
  </header>

  <main class="card">
    {#if error}
      <div class="state"><AlertCircle size={48} color="var(--danger)" /><h2>{error}</h2></div>
    {:else if !info}
      <div class="state">…</div>
    {:else if !info.authorized}
      <form class="state" onsubmit={auth}>
        <Lock size={44} color="var(--brand)" />
        <h2>{t('public.protected')}</h2>
        <p class="muted">{t('public.enterPassword', { owner: info.owner })}</p>
        <input class="input" type="password" bind:value={password} placeholder={t('login.password')} required />
        {#if pwError}<p class="error-text">{pwError}</p>{/if}
        <button class="btn primary">{t('public.open')}</button>
      </form>
    {:else if isUpload || info.kind === 'upload'}
      <div class="state">
        <Upload size={44} color="var(--brand)" />
        <h2>{t('public.uploadTitle', { owner: info.owner, folder: info.name })}</h2>
        <p class="muted">{t('public.uploadText', { folder: info.name })}</p>
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div
          class="drop"
          class:over={dragOver}
          ondragover={(e) => { e.preventDefault(); dragOver = true }}
          ondragleave={() => (dragOver = false)}
          ondrop={onDrop}
        >
          <p>{t('public.dropHere')}</p>
          <div class="row">
            <button class="btn primary" onclick={() => fileInput?.click()}>{t('action.uploadFiles')}</button>
            <button class="btn" onclick={() => folderInput?.click()}>{t('action.uploadFolder')}</button>
          </div>
        </div>
        {#if info.expires_at}<p class="muted small">{t('link.expiresOn', { date: date(info.expires_at) })}</p>{/if}
        <div class="uploads">
          {#each uploader.items as it (it.id)}
            <div class="up">
              <FileIcon name={it.name} size={18} />
              <span class="ellipsis spacer">{it.relPath}</span>
              {#if it.status === 'done' || it.status === 'exists'}<CheckCircle2 size={18} color="var(--success)" />
              {:else if it.status === 'error'}<span class="error-text small">{it.error}</span>
              {:else}<span class="small muted">{it.percent}%</span>{/if}
            </div>
          {/each}
        </div>
      </div>
      <input type="file" multiple hidden bind:this={fileInput} onchange={(e) => { upload(filesFromInput(e.currentTarget.files)); e.currentTarget.value = '' }} />
      <input type="file" multiple hidden webkitdirectory bind:this={folderInput} onchange={(e) => { upload(filesFromInput(e.currentTarget.files)); e.currentTarget.value = '' }} />
    {:else if !info.is_dir}
      {@const f = info.file}
      <div class="single">
        <p class="muted">{t('public.sharedBy', { owner: info.owner })}</p>
        <h2 class="row"><FileIcon name={info.name} category={f?.category} />{info.name}</h2>
        <p class="muted">{bytes(f?.size)}</p>
        <div class="previewbox">
          {#if f?.category === 'image'}<img src={contentUrl('')} alt="" />
          {:else if f?.category === 'video'}<!-- svelte-ignore a11y_media_has_caption --><video src={contentUrl('')} controls></video>
          {:else if f?.category === 'audio'}<audio src={contentUrl('')} controls></audio>
          {:else if f?.viewer === 'pdf'}<iframe src={contentUrl('')} title={info.name}></iframe>
          {:else}<FileQuestion size={64} strokeWidth={1.2} />{/if}
        </div>
        <a class="btn primary" href={contentUrl('', true)}><Download size={18} />{t('action.download')}</a>
        {#if info.max_downloads}<p class="muted small">{t('public.downloadsLeft', { n: Math.max(0, info.max_downloads - info.downloads) })}</p>{/if}
      </div>
    {:else}
      <div class="folderhead">
        <div>
          <p class="muted">{t('public.sharedBy', { owner: info.owner })}</p>
          <nav class="crumbs">
            <button onclick={() => { path = ''; list() }}><FolderOpen size={18} />{info.name}</button>
            {#each crumbs as c, i}
              <ChevronRight size={16} /><button onclick={() => { path = crumbs.slice(0, i + 1).join('/'); list() }}>{c}</button>
            {/each}
          </nav>
        </div>
        <a class="btn primary" href={base + '/zip' + qs({ path: path || undefined })}><Download size={18} />{t('public.downloadAll')}</a>
      </div>
      <table class="table">
        <tbody>
          {#each entries as f (f.path)}
            <tr>
              <td>
                <button class="name" onclick={() => open(f)}>
                  {#if f.category === 'image'}<img class="mini" src={thumbUrl(f.path)} alt="" loading="lazy" />{:else}<FileIcon name={f.name} isDir={f.is_dir} category={f.category} />{/if}
                  <span class="ellipsis">{f.name}</span>
                </button>
              </td>
              <td class="muted small hide-sm">{date(f.mtime)}</td>
              <td class="muted small">{f.is_dir ? '' : bytes(f.size)}</td>
              <td class="act">{#if !f.is_dir}<a class="icon-btn small" href={contentUrl(f.path, true)} aria-label={t('action.download')}><Download size={16} /></a>{/if}</td>
            </tr>
          {:else}
            <tr><td class="muted">{t('files.emptyFolder')}</td></tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </main>
  <p class="foot muted small">{t('public.footer')}</p>
</div>

{#if preview}
  {@const p = preview}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="overlay" onclick={() => (preview = null)}>
    <div class="ov-inner" onclick={(e) => e.stopPropagation()}>
      <div class="row"><strong class="ellipsis spacer">{p.name}</strong><a class="btn primary small" href={contentUrl(p.path, true)}><Download size={16} />{t('action.download')}</a><button class="btn small" onclick={() => (preview = null)}>{t('common.close')}</button></div>
      <div class="previewbox big">
        {#if p.category === 'image'}<img src={contentUrl(p.path)} alt="" />
        {:else if p.category === 'video'}<!-- svelte-ignore a11y_media_has_caption --><video src={contentUrl(p.path)} controls autoplay></video>
        {:else if p.category === 'audio'}<audio src={contentUrl(p.path)} controls autoplay></audio>
        {:else if p.viewer === 'pdf'}<iframe src={contentUrl(p.path)} title={p.name}></iframe>
        {:else}<FileQuestion size={64} strokeWidth={1.2} /><p>{t('viewer.noPreview')}</p>{/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .page {
    max-width: 960px;
    margin: 0 auto;
    padding: 16px;
  }
  header {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 18px;
    margin-bottom: 16px;
  }
  .lang {
    border: none;
    background: none;
    color: var(--text-2);
    cursor: pointer;
  }
  main {
    padding: 28px;
  }
  .state {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    gap: 10px;
    max-width: 520px;
    margin: 0 auto;
  }
  .state .input {
    max-width: 320px;
  }
  .drop {
    width: 100%;
    border: 2px dashed var(--border);
    border-radius: 14px;
    padding: 30px;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
  }
  .drop.over {
    border-color: var(--brand);
    background: var(--brand-soft);
  }
  .uploads {
    width: 100%;
  }
  .up {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 0;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  .single {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    gap: 8px;
  }
  .previewbox {
    width: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    margin: 12px 0;
    color: var(--text-3);
  }
  .previewbox img,
  .previewbox video {
    max-width: 100%;
    max-height: 60vh;
    border-radius: 8px;
  }
  .previewbox iframe {
    width: 100%;
    height: 70vh;
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  .previewbox.big img,
  .previewbox.big video {
    max-height: 80vh;
  }
  .folderhead {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
    flex-wrap: wrap;
  }
  .folderhead p {
    margin: 0 0 4px;
  }
  .crumbs {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 2px;
  }
  .crumbs button {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    border: none;
    background: none;
    font-size: 17px;
    cursor: pointer;
    padding: 4px 8px;
    border-radius: 12px;
    color: var(--text);
  }
  .crumbs button:hover {
    background: var(--hover);
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
  .mini {
    width: 32px;
    height: 32px;
    object-fit: cover;
    border-radius: 4px;
  }
  .act {
    text-align: right;
    width: 50px;
  }
  .foot {
    text-align: center;
    margin-top: 16px;
  }
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.7);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
    z-index: 50;
  }
  .ov-inner {
    background: var(--surface);
    border-radius: 14px;
    padding: 16px;
    width: min(1000px, 100%);
    max-height: 100%;
    overflow: auto;
  }
  @media (max-width: 600px) {
    main {
      padding: 18px;
    }
    .hide-sm {
      display: none;
    }
  }
</style>
