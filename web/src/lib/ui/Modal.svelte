<script lang="ts">
  import type { Snippet } from 'svelte'
  import { X } from '@lucide/svelte'

  let {
    title = '',
    onclose,
    width = '480px',
    children,
    footer,
  }: { title?: string; onclose: () => void; width?: string; children: Snippet; footer?: Snippet } = $props()

  function onkey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.stopPropagation()
      onclose()
    }
  }
</script>

<svelte:window onkeydown={onkey} />

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="backdrop" onclick={(e) => e.target === e.currentTarget && onclose()}>
  <div class="modal" style:width role="dialog" aria-modal="true" aria-label={title}>
    {#if title}
      <header>
        <h2>{title}</h2>
        <button class="icon-btn small" onclick={onclose} aria-label="Schließen"><X size={18} /></button>
      </header>
    {/if}
    <div class="body">{@render children()}</div>
    {#if footer}
      <footer>{@render footer()}</footer>
    {/if}
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(10, 15, 25, 0.45);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
    padding: 16px;
    animation: fade 0.12s ease-out;
  }
  .modal {
    max-width: 100%;
    max-height: calc(100vh - 32px);
    display: flex;
    flex-direction: column;
    background: var(--surface);
    border-radius: 16px;
    box-shadow: var(--shadow-lg);
    animation: pop 0.14s ease-out;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 18px 18px 6px 24px;
  }
  header h2 {
    margin: 0;
  }
  .body {
    padding: 12px 24px 16px;
    overflow: auto;
  }
  footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 24px 20px;
  }
  @keyframes fade {
    from {
      opacity: 0;
    }
  }
  @keyframes pop {
    from {
      transform: scale(0.97);
      opacity: 0;
    }
  }
</style>
