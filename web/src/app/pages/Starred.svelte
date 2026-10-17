<script lang="ts">
  import { Star } from '@lucide/svelte'
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
      entries = await get<FileInfo[]>('/starred')
      error = ''
    } catch (e) {
      error = errText(e)
    } finally {
      loading = false
    }
  }
  load()
  const visible = $derived(entries.filter((e) => e.starred !== false))
</script>

<h1 class="title">{t('nav.starred')}</h1>
<FileBrowser entries={visible} {loading} {error} flat emptyIcon={Star} emptyText={t('starred.empty')} onrefresh={load} />

<style>
  .title {
    padding: 18px 20px 0;
    font-weight: 500;
  }
</style>
