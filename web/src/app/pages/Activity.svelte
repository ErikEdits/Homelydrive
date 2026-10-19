<script lang="ts">
  import { History } from '@lucide/svelte'
  import { get } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { dayLabel, parentOf } from '../../lib/format'
  import type { Activity } from '../../lib/types'
  import Avatar from '../../lib/ui/Avatar.svelte'
  import { driveHash } from '../router.svelte'

  let items = $state<Activity[]>([])
  let loading = $state(false)
  let done = $state(false)
  let error = $state('')

  async function more() {
    if (loading || done) return
    loading = true
    try {
      const before = items.length ? items[items.length - 1].id : 0
      const page = await get<Activity[]>('/activity', { before, limit: 100 })
      items.push(...page)
      done = page.length < 100
    } catch (e) {
      error = errText(e)
    } finally {
      loading = false
    }
  }
  more()

  const groups = $derived.by(() => {
    const out: { label: string; items: Activity[] }[] = []
    for (const a of items) {
      const label = dayLabel(a.created_at)
      if (!out.length || out[out.length - 1].label !== label) out.push({ label, items: [] })
      out[out.length - 1].items.push(a)
    }
    return out
  })

  const time = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const name = (p: string) => p.split('/').pop() || p
</script>

<h1 class="title">{t('nav.activity')}</h1>
<div class="scroll">
  {#if error}<p class="error-text">{error}</p>{/if}
  {#if !loading && items.length === 0}
    <div class="empty"><History size={56} strokeWidth={1.2} />{t('activity.empty')}</div>
  {/if}
  {#each groups as g (g.label)}
    <h3 class="day">{g.label}</h3>
    {#each g.items as a (a.id)}
      <div class="entry">
        <span class="time">{time(a.created_at)}</span>
        <span class="actor">
          {#if a.actor}<Avatar id={a.actor_id} name={a.actor} version={a.actor_avatar} size={22} />{a.actor}{:else}—{/if}
        </span>
        <span class="what">
          {t('activity.' + a.action)}
          {#if a.path}
            {#if a.space}
              <a href={driveHash(a.space, a.action === 'delete' || a.action === 'purge' ? parentOf(a.path) : parentOf(a.path))}>{name(a.path)}</a>
            {:else}<strong>{name(a.path)}</strong>{/if}
          {/if}
          {#if a.details && (a.action === 'rename' || a.action === 'share')}<span class="muted"> ({a.details})</span>{/if}
        </span>
      </div>
    {/each}
  {/each}
  {#if !done}<button class="btn ghost" onclick={more} disabled={loading}>{t('common.more')}</button>{/if}
</div>

<style>
  .title {
    padding: 18px 20px 0;
    font-weight: 500;
  }
  .scroll {
    flex: 1;
    overflow: auto;
    padding: 0 20px 40px;
  }
  .day {
    font-size: 14px;
    color: var(--text-2);
    font-weight: 500;
    margin: 18px 0 6px;
  }
  .entry {
    display: grid;
    grid-template-columns: 60px 140px 1fr;
    gap: 12px;
    padding: 7px 0;
    border-bottom: 1px solid var(--border);
    align-items: baseline;
  }
  .time {
    color: var(--text-3);
    font-size: 12px;
  }
  .actor {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    font-weight: 500;
    align-self: center;
  }
  @media (max-width: 600px) {
    .entry {
      grid-template-columns: 48px 1fr;
    }
    .what {
      grid-column: 1 / -1;
    }
  }
</style>
