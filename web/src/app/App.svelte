<script lang="ts">
  import { onMount } from 'svelte'
  import { get, setUnauthorizedHandler } from '../lib/api'
  import Dialogs from '../lib/ui/Dialogs.svelte'
  import { app, applyTheme, connectEvents, disconnectEvents, loadMe, loadNotifications, loadRoots, refreshQuota } from './state.svelte'
  import { route } from './router.svelte'
  import { uploader } from '../lib/upload.svelte'
  import Login from './pages/Login.svelte'
  import TopBar from './components/TopBar.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import UploadPanel from './components/UploadPanel.svelte'
  import Viewer from './components/Viewer.svelte'
  import ShareDialog from './components/ShareDialog.svelte'
  import MoveDialog from './components/MoveDialog.svelte'
  import DuplicatesDialog from './components/DuplicatesDialog.svelte'
  import DetailsPanel from './components/DetailsPanel.svelte'
  import Drive from './pages/Drive.svelte'
  import Recent from './pages/Recent.svelte'
  import Starred from './pages/Starred.svelte'
  import Shared from './pages/Shared.svelte'
  import Search from './pages/Search.svelte'
  import Gallery from './pages/Gallery.svelte'
  import Vault from './pages/Vault.svelte'
  import Trash from './pages/Trash.svelte'
  import Activity from './pages/Activity.svelte'
  import Storage from './pages/Storage.svelte'
  import Settings from './pages/Settings.svelte'

  async function start() {
    app.loading = true
    if (await loadMe()) {
      await loadRoots()
      loadNotifications()
      get('/info').then((i) => (app.info = i)).catch(() => {})
      connectEvents()
    }
    app.loading = false
  }

  onMount(() => {
    applyTheme()
    setUnauthorizedHandler(() => {
      app.me = null
      disconnectEvents()
    })
    uploader.onFinished = () => refreshQuota()
    start()
  })

  $effect(() => {
    // Close the mobile drawer when navigating.
    route.name
    route.path
    app.sidebarOpen = false
  })
</script>

<svelte:window
  onbeforeunload={(e) => {
    if (uploader.active > 0) e.preventDefault()
  }}
/>

{#if app.loading}
  <div class="splash"><div class="spinner"></div></div>
{:else if !app.me}
  <Login onlogin={start} />
{:else}
  <div class="shell">
    <TopBar />
    <div class="main">
      <Sidebar />
      <main>
        {#key route.name}
          {#if route.name === 'drive'}<Drive />
          {:else if route.name === 'recent'}<Recent />
          {:else if route.name === 'starred'}<Starred />
          {:else if route.name === 'shared'}<Shared />
          {:else if route.name === 'search'}<Search />
          {:else if route.name === 'gallery'}<Gallery />
          {:else if route.name === 'vault'}<Vault />
          {:else if route.name === 'trash'}<Trash />
          {:else if route.name === 'activity'}<Activity />
          {:else if route.name === 'storage'}<Storage />
          {:else if route.name === 'settings'}<Settings />
          {:else}<Drive />{/if}
        {/key}
      </main>
      {#if app.showDetails && app.details}
        <DetailsPanel file={app.details} />
      {/if}
    </div>
  </div>
  <UploadPanel />
  {#if app.viewer}<Viewer />{/if}
  {#if app.shareTarget}<ShareDialog file={app.shareTarget} />{/if}
  {#if app.moveTarget}<MoveDialog />{/if}
  {#if app.duplicates}<DuplicatesDialog />{/if}
{/if}
<Dialogs />

<style>
  .splash {
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .spinner {
    width: 36px;
    height: 36px;
    border-radius: 50%;
    border: 3px solid var(--surface-3);
    border-top-color: var(--brand);
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  .shell {
    height: 100%;
    display: flex;
    flex-direction: column;
  }
  .main {
    flex: 1;
    display: flex;
    min-height: 0;
  }
  main {
    flex: 1;
    min-width: 0;
    background: var(--surface);
    border-radius: 16px 16px 0 0;
    margin-right: 12px;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  @media (max-width: 800px) {
    main {
      margin-right: 0;
      border-radius: 0;
    }
  }
</style>
