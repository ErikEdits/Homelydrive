<script lang="ts">
  // A round profile picture. Without one, the first letter of the name on a
  // colour that stays the same for each name.
  let { id = 0, name, version = 0, size = 32 }: { id?: number; name: string; version?: number; size?: number } = $props()

  let broken = $state('')
  const src = $derived(id && version ? `/api/v1/users/${id}/avatar?v=${version}` : '')
  const letter = $derived(([...(name || '?').trim()][0] || '?').toUpperCase())
  const hue = $derived([...(name || '')].reduce((h, c) => (h * 31 + (c.codePointAt(0) || 0)) % 360, 210))
</script>

{#if src && src !== broken}
  <img class="avatar" {src} alt="" style:width="{size}px" style:height="{size}px" onerror={() => (broken = src)} />
{:else}
  <span class="avatar letter" style:width="{size}px" style:height="{size}px" style:font-size="{Math.round(size * 0.45)}px" style:--hue={hue} aria-hidden="true">
    {letter}
  </span>
{/if}

<style>
  .avatar {
    flex: none;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 50%;
    object-fit: cover;
    vertical-align: middle;
  }
  .letter {
    background: hsl(var(--hue) 55% 46%);
    color: #fff;
    font-weight: 600;
    line-height: 1;
    user-select: none;
  }
</style>
