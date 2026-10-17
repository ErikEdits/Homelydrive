<script lang="ts" module>
  export interface MenuItem {
    label?: string
    icon?: any
    onclick?: () => void
    danger?: boolean
    disabled?: boolean
    separator?: boolean
    shortcut?: string
  }
</script>

<script lang="ts">
  let { x, y, items, onclose }: { x: number; y: number; items: MenuItem[]; onclose: () => void } = $props()
  let el: HTMLDivElement | undefined = $state()
  let pos = $state({ left: 0, top: 0 })

  $effect(() => {
    // Keep the menu inside the window.
    const w = el?.offsetWidth ?? 220
    const h = el?.offsetHeight ?? 300
    pos = {
      left: Math.max(8, Math.min(x, window.innerWidth - w - 8)),
      top: Math.max(8, Math.min(y, window.innerHeight - h - 8)),
    }
  })
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onclose()} onresize={onclose} />

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="layer" onclick={onclose} oncontextmenu={(e) => { e.preventDefault(); onclose() }}></div>
<div class="menu" bind:this={el} style:left="{pos.left}px" style:top="{pos.top}px" role="menu">
  {#each items as it}
    {#if it.separator}
      <div class="sep"></div>
    {:else}
      <button
        class="item"
        class:danger={it.danger}
        disabled={it.disabled}
        role="menuitem"
        onclick={() => { onclose(); it.onclick?.() }}
      >
        {#if it.icon}<it.icon size={18} />{:else}<span style="width:18px"></span>{/if}
        <span class="label">{it.label}</span>
        {#if it.shortcut}<span class="sc">{it.shortcut}</span>{/if}
      </button>
    {/if}
  {/each}
</div>

<style>
  .layer {
    position: fixed;
    inset: 0;
    z-index: 90;
  }
  .menu {
    position: fixed;
    z-index: 91;
    min-width: 220px;
    max-height: calc(100vh - 16px);
    overflow: auto;
    background: var(--surface);
    border-radius: 10px;
    box-shadow: var(--shadow-lg);
    border: 1px solid var(--border);
    padding: 6px 0;
    animation: pop 0.08s ease-out;
  }
  .item {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    padding: 8px 16px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: var(--text);
  }
  .item :global(svg) {
    color: var(--text-2);
  }
  .item:hover:not(:disabled) {
    background: var(--hover);
  }
  .item:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .item.danger,
  .item.danger :global(svg) {
    color: var(--danger);
  }
  .label {
    flex: 1;
  }
  .sc {
    font-size: 12px;
    color: var(--text-3);
  }
  .sep {
    height: 1px;
    background: var(--border);
    margin: 6px 0;
  }
  @keyframes pop {
    from {
      opacity: 0;
      transform: scale(0.98);
    }
  }
</style>
