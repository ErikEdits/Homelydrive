<script lang="ts">
  import { SearchX } from '@lucide/svelte'
  import { get } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import type { FileInfo } from '../../lib/types'
  import FileBrowser from '../components/FileBrowser.svelte'
  import { route } from '../router.svelte'

  let entries = $state<FileInfo[]>([])
  let loading = $state(true)
  let error = $state('')

  async function load() {
    const q = route.query
    const params: Record<string, any> = { q: q.q, type: q.type, owner: q.owner, tag: q.tag, min_size: q.min }
    if (q.when) params.from = Date.now() - Number(q.when) * 86400000
    loading = true
    try {
      entries = await get<FileInfo[]>('/search', params)
      error = ''
    } catch (e) {
      error = errText(e)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    JSON.stringify(route.query)
    load()
  })
</script>

<h1 class="title">
  {#if route.query.q}{t('search.resultsFor', { q: route.query.q })}{:else}{t('search.results')}{/if}
  {#if !loading}<span class="muted small">({entries.length})</span>{/if}
</h1>
<FileBrowser {entries} {loading} {error} flat emptyIcon={SearchX} emptyText={t('search.none')} onrefresh={load} />

<style>
  .title {
    padding: 18px 20px 0;
    font-weight: 500;
    display: flex;
    align-items: baseline;
    gap: 8px;
  }
</style>
