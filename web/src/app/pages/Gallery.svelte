<script lang="ts">
  import { onMount } from 'svelte'
  import { Image as ImageIcon, Play } from '@lucide/svelte'
  import { get, thumbUrl } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { dayLabel } from '../../lib/format'
  import type { FileInfo } from '../../lib/types'
  import { openFile, refOf } from '../actions'

  let items = $state<FileInfo[]>([])
  let cursor = $state('')
  let done = $state(false)
  let loading = $state(false)
  let error = $state('')
  let sentinel: HTMLDivElement | undefined = $state()

  async function more() {
    if (loading || done) return
    loading = true
    try {
      const page = await get<{ items: FileInfo[]; cursor?: string }>('/gallery', { cursor, limit: 200 })
      items.push(...page.items)
      cursor = page.cursor || ''
      done = !page.cursor
    } catch (e) {
      error = errText(e)
      done = true
    } finally {
      loading = false
    }
  }

  onMount(() => {
    more()
    const io = new IntersectionObserver((ents) => ents.some((e) => e.isIntersecting) && more(), { rootMargin: '800px' })
    if (sentinel) io.observe(sentinel)
    return () => io.disconnect()
  })

  const groups = $derived.by(() => {
    const out: { label: string; items: FileInfo[] }[] = []
    let last = ''
    for (const f of items) {
      const ts = f.taken_at || f.mtime
      const label = dayLabel(ts)
      if (label !== last) {
        out.push({ label, items: [] })
        last = label
      }
      out[out.length - 1].items.push(f)
    }
    return out
  })
</script>

<div class="head">
  <h1>{t('nav.photos')}</h1>
  {#if items.length > 1}
    <button class="btn" onclick={() => openFile(items[0], items, true)}><Play size={16} />{t('viewer.slideshow')}</button>
  {/if}
</div>

<div class="scroll">
  {#if error}
    <div class="empty error-text">{error}</div>
  {:else if items.length === 0 && done}
    <div class="empty"><ImageIcon size={56} strokeWidth={1.2} />{t('gallery.empty')}</div>
  {/if}
  {#each groups as g (g.label)}
    <h3 class="day">{g.label}</h3>
    <div class="grid">
      {#each g.items as f (refOf(f) + f.path)}
        <button class="tile" onclick={() => openFile(f, items)} title={f.name}>
          <img src={thumbUrl(refOf(f), f.path, 256, f.mtime)} alt="" loading="lazy" />
          {#if f.category === 'video'}<span class="vid"><Play size={16} fill="#fff" /></span>{/if}
        </button>
      {/each}
    </div>
  {/each}
  <div bind:this={sentinel} class="sentinel">{#if loading}…{/if}</div>
</div>

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 18px 20px 6px;
  }
  .head h1 {
    font-weight: 500;
    margin: 0;
  }
  .scroll {
    flex: 1;
    overflow: auto;
    padding: 0 20px 60px;
  }
  .day {
    font-size: 14px;
    font-weight: 500;
    color: var(--text-2);
    margin: 20px 0 10px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 4px;
  }
  .tile {
    position: relative;
    aspect-ratio: 1;
    border: none;
    padding: 0;
    background: var(--surface-2);
    cursor: pointer;
    overflow: hidden;
    border-radius: 4px;
  }
  .tile img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    transition: transform 0.2s;
  }
  .tile:hover img {
    transform: scale(1.04);
  }
  .vid {
    position: absolute;
    right: 6px;
    bottom: 6px;
    background: rgba(0, 0, 0, 0.55);
    border-radius: 12px;
    padding: 3px 7px;
    display: flex;
  }
  .sentinel {
    height: 40px;
    text-align: center;
    color: var(--text-3);
  }
  @media (max-width: 600px) {
    .grid {
      grid-template-columns: repeat(auto-fill, minmax(100px, 1fr));
      gap: 2px;
    }
  }
</style>
