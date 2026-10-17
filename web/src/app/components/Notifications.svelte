<script lang="ts">
  import { Share2, Upload, HardDrive, Download, BellOff } from '@lucide/svelte'
  import { post } from '../../lib/api'
  import { t } from '../../lib/i18n.svelte'
  import { relative, bytes } from '../../lib/format'
  import type { Notification } from '../../lib/types'
  import { app } from '../state.svelte'
  import { go, driveHash } from '../router.svelte'

  let { onclose }: { onclose: () => void } = $props()

  $effect(() => {
    if (app.unread > 0) {
      post('/notifications/read', { ids: [] }).then(() => {
        app.unread = 0
        app.notifications.forEach((n) => (n.read = true))
      })
    }
  })

  function text(n: Notification): string {
    const d = n.data || {}
    switch (n.kind) {
      case 'share':
        return t(d.is_dir ? 'notify.shareFolder' : 'notify.shareFile', { from: d.from, name: d.name })
      case 'link_upload':
        return t('notify.linkUpload', { name: d.name, folder: d.folder })
      case 'quota_warning':
        return t('notify.quota', { percent: d.percent, used: bytes(d.used), quota: bytes(d.quota) })
      case 'disk_warning':
        return t('notify.disk', { path: d.path, percent: d.percent })
      case 'update':
        return t('notify.update', { version: d.version })
    }
    return n.kind
  }

  function open(n: Notification) {
    const d = n.data || {}
    onclose()
    switch (n.kind) {
      case 'share':
        go(driveHash(d.space, ''))
        break
      case 'link_upload':
        go(driveHash(d.space || 'home', d.path || ''))
        break
      case 'quota_warning':
        go('#/storage')
        break
      case 'update':
        window.open(d.url, '_blank', 'noopener')
        break
    }
  }

  const icons: Record<string, any> = { share: Share2, link_upload: Upload, quota_warning: HardDrive, disk_warning: HardDrive, update: Download }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="layer" onclick={onclose}></div>
<div class="panel card">
  <h3>{t('notify.title')}</h3>
  {#if app.notifications.length === 0}
    <div class="empty"><BellOff size={32} />{t('notify.empty')}</div>
  {:else}
    <div class="list">
      {#each app.notifications as n (n.id)}
        {@const Icon = icons[n.kind] || Share2}
        <button class="item" class:unread={!n.read} onclick={() => open(n)}>
          <span class="ico"><Icon size={18} /></span>
          <span class="txt">{text(n)}<span class="time">{relative(n.created_at)}</span></span>
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .layer {
    position: fixed;
    inset: 0;
    z-index: 60;
  }
  .panel {
    position: fixed;
    top: 60px;
    right: 16px;
    width: 380px;
    max-width: calc(100vw - 32px);
    max-height: 70vh;
    display: flex;
    flex-direction: column;
    z-index: 61;
    box-shadow: var(--shadow-lg);
    padding: 16px 0 8px;
  }
  h3 {
    padding: 0 18px;
  }
  .list {
    overflow: auto;
  }
  .item {
    display: flex;
    gap: 12px;
    width: 100%;
    text-align: left;
    border: none;
    background: none;
    padding: 10px 18px;
    cursor: pointer;
    color: var(--text);
  }
  .item:hover {
    background: var(--hover);
  }
  .item.unread {
    background: var(--brand-soft);
  }
  .ico {
    width: 34px;
    height: 34px;
    border-radius: 50%;
    background: var(--surface-2);
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--brand);
    flex: none;
  }
  .txt {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .time {
    font-size: 12px;
    color: var(--text-3);
  }
  .empty {
    padding: 30px;
  }
</style>
