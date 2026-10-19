<script lang="ts">
  import { Menu as MenuIcon, Search, SlidersHorizontal, Bell, Settings, X, LogOut, Sun, Moon, Monitor, Languages } from '@lucide/svelte'
  import Avatar from '../../lib/ui/Avatar.svelte'
  import Logo from '../../lib/ui/Logo.svelte'
  import Menu from '../../lib/ui/Menu.svelte'
  import { post } from '../../lib/api'
  import { i18n, setLang, t } from '../../lib/i18n.svelte'
  import { app, applyTheme, disconnectEvents, savePref } from '../state.svelte'
  import { go, route } from '../router.svelte'
  import Notifications from './Notifications.svelte'

  let q = $state(route.name === 'search' ? route.query.q || '' : '')
  let showFilters = $state(false)
  let filters = $state({ type: '', when: '', owner: '', min: '', tag: '' })
  let showNotes = $state(false)
  let userMenu = $state<{ x: number; y: number } | null>(null)

  $effect(() => {
    if (route.name === 'search') {
      q = route.query.q || ''
      filters.type = route.query.type || ''
      filters.when = route.query.when || ''
      filters.owner = route.query.owner || ''
      filters.min = route.query.min || ''
      filters.tag = route.query.tag || ''
    }
  })

  function search(e?: Event) {
    e?.preventDefault()
    const p = new URLSearchParams()
    if (q.trim()) p.set('q', q.trim())
    for (const [k, v] of Object.entries(filters)) if (v) p.set(k, v)
    showFilters = false
    if ([...p.keys()].length === 0) return
    go('#/search?' + p.toString())
  }

  function clear() {
    q = ''
    filters = { type: '', when: '', owner: '', min: '', tag: '' }
    if (route.name === 'search') history.back()
  }

  async function logout() {
    try {
      await post('/auth/logout')
    } catch {
      /* ignore */
    }
    disconnectEvents()
    app.me = null
  }

  function setTheme(th: 'auto' | 'light' | 'dark') {
    app.theme = th
    savePref('hd_theme', th)
    applyTheme()
  }
</script>

<header class="top">
  <button class="icon-btn mobile" onclick={() => (app.sidebarOpen = !app.sidebarOpen)} aria-label={t('nav.menu')}><MenuIcon size={22} /></button>
  <a class="brand" href="#/drive/home/"><Logo size={34} /><span>Homelydrive</span></a>

  <form class="search" onsubmit={search}>
    <Search size={20} />
    <input bind:value={q} placeholder={t('search.placeholder')} aria-label={t('search.placeholder')} />
    {#if q || route.name === 'search'}
      <button type="button" class="icon-btn small" onclick={clear} aria-label={t('common.clear')}><X size={18} /></button>
    {/if}
    <button type="button" class="icon-btn small" class:active={showFilters} onclick={() => (showFilters = !showFilters)} aria-label={t('search.filters')}>
      <SlidersHorizontal size={18} />
    </button>
    {#if showFilters}
      <div class="filters card">
        <label class="field">{t('search.type')}
          <select class="input" bind:value={filters.type}>
            <option value="">{t('search.any')}</option>
            <option value="folder">{t('type.folder')}</option>
            <option value="image">{t('type.image')}</option>
            <option value="video">{t('type.video')}</option>
            <option value="audio">{t('type.audio')}</option>
            <option value="pdf">PDF</option>
            <option value="text">{t('type.text')}</option>
            <option value="document">{t('type.document')}</option>
            <option value="archive">{t('type.archive')}</option>
            <option value="app">{t('type.app')}</option>
            <option value="other">{t('type.other')}</option>
          </select>
        </label>
        <label class="field">{t('search.modified')}
          <select class="input" bind:value={filters.when}>
            <option value="">{t('search.any')}</option>
            <option value="1">{t('search.today')}</option>
            <option value="7">{t('search.last7')}</option>
            <option value="30">{t('search.last30')}</option>
            <option value="365">{t('search.lastYear')}</option>
          </select>
        </label>
        <label class="field">{t('search.size')}
          <select class="input" bind:value={filters.min}>
            <option value="">{t('search.any')}</option>
            <option value="10485760">&gt; 10 MB</option>
            <option value="104857600">&gt; 100 MB</option>
            <option value="1073741824">&gt; 1 GB</option>
            <option value="10737418240">&gt; 10 GB</option>
          </select>
        </label>
        <label class="field">{t('search.owner')}
          <select class="input" bind:value={filters.owner}>
            <option value="">{t('search.anyone')}</option>
            <option value="me">{t('search.ownerMe')}</option>
            <option value="others">{t('search.ownerOthers')}</option>
          </select>
        </label>
        <label class="field">{t('search.tag')}
          <input class="input" bind:value={filters.tag} placeholder="z. B. Steuer" />
        </label>
        <div class="row">
          <button type="button" class="btn ghost" onclick={() => (filters = { type: '', when: '', owner: '', min: '', tag: '' })}>{t('search.reset')}</button>
          <span class="spacer"></span>
          <button class="btn primary">{t('search.submit')}</button>
        </div>
      </div>
    {/if}
  </form>

  <div class="actions">
    <button class="icon-btn" onclick={() => (showNotes = !showNotes)} aria-label={t('notify.title')}>
      <Bell size={21} />
      {#if app.unread > 0}<span class="badge">{app.unread > 9 ? '9+' : app.unread}</span>{/if}
    </button>
    <a class="icon-btn hide-mobile" href="#/settings" aria-label={t('nav.settings')}><Settings size={21} /></a>
    <button class="me" onclick={(e) => (userMenu = { x: e.clientX - 200, y: e.clientY + 16 })} aria-label={app.me?.name}>
      <Avatar id={app.me?.id} name={app.me?.name || ''} version={app.me?.avatar} size={36} />
    </button>
  </div>
</header>

{#if showNotes}<Notifications onclose={() => (showNotes = false)} />{/if}
{#if userMenu}
  <Menu
    x={userMenu.x}
    y={userMenu.y}
    onclose={() => (userMenu = null)}
    items={[
      { label: app.me?.name + ' (' + app.me?.username + ')', disabled: true },
      { separator: true },
      { label: t('nav.settings'), icon: Settings, onclick: () => go('#/settings') },
      { label: t('theme.auto'), icon: Monitor, onclick: () => setTheme('auto') },
      { label: t('theme.light'), icon: Sun, onclick: () => setTheme('light') },
      { label: t('theme.dark'), icon: Moon, onclick: () => setTheme('dark') },
      { label: i18n.lang === 'de' ? 'English' : 'Deutsch', icon: Languages, onclick: () => setLang(i18n.lang === 'de' ? 'en' : 'de') },
      { separator: true },
      { label: t('nav.logout'), icon: LogOut, onclick: logout },
    ]}
  />
{/if}

<style>
  .top {
    display: flex;
    align-items: center;
    gap: 12px;
    height: 64px;
    padding: 0 12px 0 16px;
    flex: none;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--text);
    font-size: 20px;
    font-weight: 600;
    width: 232px;
    flex: none;
  }
  .brand:hover {
    text-decoration: none;
  }
  .search {
    position: relative;
    flex: 1;
    min-width: 0;
    max-width: 720px;
    display: flex;
    align-items: center;
    gap: 8px;
    height: 46px;
    padding: 0 6px 0 16px;
    border-radius: 23px;
    background: var(--surface-3);
    color: var(--text-2);
  }
  .search:focus-within {
    background: var(--surface);
    box-shadow: var(--shadow);
  }
  .search input {
    flex: 1;
    border: none;
    outline: none;
    background: none;
    font-size: 15px;
    min-width: 0;
  }
  .filters {
    position: absolute;
    top: 52px;
    left: 0;
    right: 0;
    z-index: 50;
    box-shadow: var(--shadow-lg);
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0 16px;
  }
  .filters .row {
    grid-column: 1 / -1;
  }
  .actions {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .badge {
    position: absolute;
    top: 4px;
    right: 4px;
    min-width: 16px;
    height: 16px;
    padding: 0 4px;
    border-radius: 8px;
    background: var(--danger);
    color: #fff;
    font-size: 10px;
    font-weight: 700;
    line-height: 16px;
  }
  .me {
    display: flex;
    padding: 0;
    border: none;
    border-radius: 50%;
    background: none;
    cursor: pointer;
    margin-left: 4px;
  }
  .me:focus-visible {
    outline: 2px solid var(--brand);
    outline-offset: 2px;
  }
  .mobile {
    display: none;
  }
  @media (max-width: 800px) {
    .mobile {
      display: inline-flex;
    }
    .brand {
      width: auto;
    }
    .brand span,
    .hide-mobile {
      display: none;
    }
    .top {
      gap: 4px;
      padding: 0 6px;
    }
    .search {
      padding-left: 12px;
      gap: 4px;
    }
    .search > :global(svg) {
      display: none;
    }
    .filters {
      grid-template-columns: 1fr;
      position: fixed;
      top: 64px;
      left: 8px;
      right: 8px;
    }
  }
</style>
