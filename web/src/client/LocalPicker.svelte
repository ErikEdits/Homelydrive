<script lang="ts">
  import { FolderOpen, ChevronUp, HardDrive } from '@lucide/svelte'
  import Modal from '../lib/ui/Modal.svelte'
  import { t } from '../lib/i18n.svelte'
  import { local } from './api'

  let { onpick, onclose }: { onpick: (p: string) => void; onclose: () => void } = $props()
  let cur = $state<{ path: string; parent: string; dirs: { name: string; path: string }[] }>({ path: '', parent: '', dirs: [] })
  let error = $state('')

  async function go(path: string) {
    try {
      cur = await local('GET', '/browse?path=' + encodeURIComponent(path))
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }
  go('')
</script>

<Modal title={t('client.chooseLocal')} {onclose} width="560px">
  <code class="cur">{cur.path || t('client.places')}</code>
  <div class="list">
    {#if cur.path}<button class="dir" onclick={() => go(cur.parent)}><ChevronUp size={18} />..</button>{/if}
    {#each cur.dirs as d}
      <button class="dir" onclick={() => go(d.path)}>
        {#if !cur.path}<HardDrive size={18} />{:else}<FolderOpen size={18} />{/if}{d.name}
      </button>
    {/each}
  </div>
  {#if error}<p class="error-text">{error}</p>{/if}
  {#snippet footer()}
    <button class="btn ghost" onclick={onclose}>{t('common.cancel')}</button>
    <button class="btn primary" disabled={!cur.path} onclick={() => onpick(cur.path)}>{t('admin.useFolder')}</button>
  {/snippet}
</Modal>

<style>
  .cur {
    display: block;
    margin-bottom: 8px;
  }
  .list {
    height: 340px;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: 10px;
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
</style>
