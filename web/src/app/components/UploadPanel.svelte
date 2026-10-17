<script lang="ts">
  import { ChevronDown, ChevronUp, X, Check, RotateCw, AlertCircle, CircleSlash } from '@lucide/svelte'
  import FileIcon from '../../lib/ui/FileIcon.svelte'
  import { t } from '../../lib/i18n.svelte'
  import { bytes } from '../../lib/format'
  import { uploader } from '../../lib/upload.svelte'

  let collapsed = $state(false)
  const pct = $derived(uploader.totalBytes ? Math.round((uploader.loadedBytes / uploader.totalBytes) * 100) : 0)
  const errors = $derived(uploader.items.filter((i) => i.status === 'error').length)
</script>

{#if uploader.items.length > 0}
  <div class="panel">
    <header>
      <span class="title">
        {#if uploader.active > 0}
          {t('upload.running', { n: uploader.active, pct })}
        {:else if errors}
          {t('upload.errors', { n: errors })}
        {:else}
          {t('upload.done', { n: uploader.items.length })}
        {/if}
      </span>
      <button class="icon-btn small" onclick={() => (collapsed = !collapsed)} aria-label="toggle">
        {#if collapsed}<ChevronUp size={18} />{:else}<ChevronDown size={18} />{/if}
      </button>
      <button
        class="icon-btn small"
        onclick={() => (uploader.active > 0 ? uploader.cancelAll() : uploader.clearFinished())}
        aria-label={t('common.close')}><X size={18} /></button>
    </header>
    {#if uploader.active > 0}<div class="progress bar"><div style:width="{pct}%"></div></div>{/if}
    {#if !collapsed}
      <div class="list">
        {#each uploader.items as it (it.id)}
          <div class="item">
            <FileIcon name={it.name} size={20} />
            <div class="info">
              <span class="ellipsis name" title={it.relPath}>{it.name}</span>
              <span class="sub">
                {#if it.status === 'uploading'}
                  {bytes(it.loaded)} / {bytes(it.size)}{#if it.speed} · {bytes(it.speed)}/s{/if}
                {:else if it.status === 'error'}
                  <span class="error-text">{it.error}</span>
                {:else if it.status === 'exists'}
                  {t('upload.exists')}
                {:else if it.status === 'queued'}
                  {t('upload.queued')}
                {:else if it.status === 'canceled'}
                  {t('upload.canceled')}
                {:else}
                  {bytes(it.size)}
                {/if}
              </span>
            </div>
            {#if it.status === 'uploading'}
              <div class="ring" style:--p={it.percent}></div>
              <button class="icon-btn small" onclick={() => uploader.cancel(it)} aria-label={t('common.cancel')}><X size={16} /></button>
            {:else if it.status === 'queued'}
              <button class="icon-btn small" onclick={() => uploader.cancel(it)} aria-label={t('common.cancel')}><X size={16} /></button>
            {:else if it.status === 'done' || it.status === 'exists'}
              <span class="ok"><Check size={18} /></span>
            {:else if it.status === 'error'}
              <AlertCircle size={18} color="var(--danger)" />
              <button class="icon-btn small" onclick={() => uploader.retry(it)} aria-label={t('upload.retry')}><RotateCw size={16} /></button>
            {:else}
              <CircleSlash size={18} color="var(--text-3)" />
              <button class="icon-btn small" onclick={() => uploader.retry(it)} aria-label={t('upload.retry')}><RotateCw size={16} /></button>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .panel {
    position: fixed;
    right: 24px;
    bottom: 0;
    width: 380px;
    max-width: calc(100vw - 16px);
    background: var(--surface);
    border-radius: 14px 14px 0 0;
    box-shadow: var(--shadow-lg);
    z-index: 70;
    overflow: hidden;
  }
  header {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 8px 8px 8px 18px;
    background: var(--surface-2);
  }
  .title {
    flex: 1;
    font-weight: 500;
  }
  .bar {
    border-radius: 0;
    height: 3px;
  }
  .list {
    max-height: 320px;
    overflow: auto;
  }
  .item {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 8px 12px 8px 18px;
  }
  .info {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .sub {
    font-size: 12px;
    color: var(--text-2);
  }
  .ok {
    color: var(--success);
    display: flex;
  }
  .ring {
    width: 22px;
    height: 22px;
    border-radius: 50%;
    background: conic-gradient(var(--brand) calc(var(--p) * 1%), var(--surface-3) 0);
    mask: radial-gradient(circle, transparent 6px, #000 7px);
    -webkit-mask: radial-gradient(circle, transparent 6px, #000 7px);
  }
  @media (max-width: 800px) {
    .panel {
      right: 8px;
    }
  }
</style>
