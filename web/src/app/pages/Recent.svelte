<script lang="ts">
  import { Clock } from '@lucide/svelte'
  import { get } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import type { FileInfo } from '../../lib/types'
  import FileBrowser from '../components/FileBrowser.svelte'

  let entries = $state<FileInfo[]>([])
  let loading = $state(true)
  let error = $state('')

  async function load() {
    loading = true
    try {
      entries = await get<FileInfo[]>('/recent', { limit: 100 })
      error = ''
    } catch (e) {
      error = errText(e)
    } finally {
      loading = false
    }
  }
  load()
</script>

<h1 class="title">{t('nav.recent')}</h1>
<FileBrowser {entries} {loading} {error} flat emptyIcon={Clock} emptyText={t('recent.empty')} onrefresh={load} />

<style>
  .title {
    padding: 18px 20px 0;
    font-weight: 500;
  }
</style>
