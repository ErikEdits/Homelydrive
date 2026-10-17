<script lang="ts">
  import { get } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { bytes } from '../../lib/format'
  import type { StorageStats } from '../../lib/types'
  import FileBrowser from '../components/FileBrowser.svelte'

  let st = $state<StorageStats | null>(null)
  let error = $state('')

  get<StorageStats>('/storage').then((s) => (st = s)).catch((e) => (error = errText(e)))

  const colors: Record<string, string> = {
    image: '#e53935', video: '#8e24aa', audio: '#fb8c00', pdf: '#c62828', text: '#1e88e5',
    document: '#43a047', archive: '#6d4c41', app: '#00897b', other: '#757575',
  }
  const cats = $derived(st ? Object.entries(st.by_category).sort((a, b) => b[1] - a[1]) : [])
  const total = $derived(st ? Math.max(st.quota || st.used, 1) : 1)
</script>

<div class="scroll">
  <h1>{t('nav.storage')}</h1>
  {#if error}<p class="error-text">{error}</p>{/if}
  {#if st}
    <div class="big">
      <span class="used">{bytes(st.used)}</span>
      <span class="muted">{st.quota ? t('storage.of', { quota: bytes(st.quota) }) : t('storage.unlimited')}</span>
    </div>
    <div class="stack">
      {#each cats as [cat, size]}
        <div style:width="{(size / total) * 100}%" style:background={colors[cat] || '#999'} title="{t('type.' + cat)}: {bytes(size)}"></div>
      {/each}
      {#if st.trash}<div style:width="{(st.trash / total) * 100}%" style:background="#bdbdbd" title={t('nav.trash')}></div>{/if}
      {#if st.vault}<div style:width="{(st.vault / total) * 100}%" style:background="#37474f" title={t('nav.vault')}></div>{/if}
    </div>
    {#if st.percent >= 90}<p class="warn">{t('storage.almostFull', { percent: st.percent })}</p>{/if}
    <div class="legend">
      {#each cats as [cat, size]}
        <span><i style:background={colors[cat] || '#999'}></i>{t('type.' + cat)} · {bytes(size)}</span>
      {/each}
      <span><i style:background="#bdbdbd"></i>{t('nav.trash')} · {bytes(st.trash)}</span>
      {#if st.vault}<span><i style:background="#37474f"></i>{t('nav.vault')} · {bytes(st.vault)}</span>{/if}
    </div>
    <p class="muted small">{t('storage.files', { n: st.files })} · {t('storage.serverFree', { free: bytes(st.disk_free), total: bytes(st.disk_total) })}</p>
    <h2>{t('storage.largest')}</h2>
  {/if}
</div>
{#if st}
  <FileBrowser entries={st.largest} flat />
{/if}

<style>
  .scroll {
    padding: 18px 20px 0;
  }
  h1 {
    font-weight: 500;
  }
  h2 {
    font-weight: 500;
    margin-top: 24px;
    font-size: 16px;
  }
  .big {
    display: flex;
    align-items: baseline;
    gap: 10px;
    margin-bottom: 12px;
  }
  .used {
    font-size: 32px;
    font-weight: 600;
  }
  .stack {
    display: flex;
    height: 14px;
    border-radius: 7px;
    overflow: hidden;
    background: var(--surface-3);
    max-width: 760px;
  }
  .stack div {
    min-width: 2px;
  }
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 18px;
    margin: 12px 0;
    font-size: 13px;
  }
  .legend i {
    display: inline-block;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    margin-right: 6px;
  }
  .warn {
    color: var(--warning);
    font-weight: 500;
  }
</style>
