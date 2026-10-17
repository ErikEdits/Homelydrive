<script lang="ts">
  import Modal from './Modal.svelte'
  import { dialog, toasts } from './ui.svelte'
  import { t } from '../i18n.svelte'

  let value = $state('')
  let input: HTMLInputElement | undefined = $state()

  $effect(() => {
    const d = dialog.current
    if (d?.kind === 'prompt') {
      value = d.value ?? ''
      queueMicrotask(() => {
        input?.focus()
        // Select the name without the extension, like file managers do.
        const dot = value.lastIndexOf('.')
        input?.setSelectionRange(0, dot > 0 ? dot : value.length)
      })
    }
  })

  function close(result: any) {
    const d = dialog.current
    dialog.current = null
    d?.resolve(result)
  }
</script>

{#if dialog.current}
  {@const d = dialog.current}
  <Modal title={d.title} onclose={() => close(d.kind === 'confirm' ? false : null)} width="440px">
    {#if d.message}<p class="muted" style="margin-top:0;white-space:pre-line">{d.message}</p>{/if}
    {#if d.kind === 'prompt'}
      <form onsubmit={(e) => { e.preventDefault(); close(value) }}>
        <input class="input" bind:this={input} bind:value placeholder={d.placeholder} type={d.inputType || 'text'} />
      </form>
    {/if}
    {#snippet footer()}
      <button class="btn ghost" onclick={() => close(d.kind === 'confirm' ? false : null)}>{t('common.cancel')}</button>
      <button
        class="btn {d.danger ? 'danger' : 'primary'}"
        onclick={() => close(d.kind === 'confirm' ? true : value)}
      >{d.okLabel || t('common.ok')}</button>
    {/snippet}
  </Modal>
{/if}

<div class="toasts">
  {#each toasts as tst (tst.id)}
    <div class="toast {tst.kind}">
      <span>{tst.text}</span>
      {#if tst.action}
        <button class="btn ghost small" onclick={() => { tst.action?.run(); toasts.splice(toasts.indexOf(tst), 1) }}>{tst.action.label}</button>
      {/if}
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    left: 24px;
    bottom: 24px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    z-index: 200;
    max-width: calc(100vw - 48px);
  }
  .toast {
    display: flex;
    align-items: center;
    gap: 12px;
    background: #2a2f3a;
    color: #fff;
    padding: 10px 16px;
    border-radius: 10px;
    box-shadow: var(--shadow-lg);
    min-width: 260px;
    max-width: 480px;
    animation: up 0.15s ease-out;
  }
  .toast.error {
    background: #b3261e;
  }
  .toast.success {
    background: #146c2e;
  }
  .toast :global(.btn) {
    color: #9ec1ff;
  }
  @keyframes up {
    from {
      transform: translateY(10px);
      opacity: 0;
    }
  }
</style>
