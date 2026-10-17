<script lang="ts">
  import { RotateCcw, RotateCw, Crop, Save, Copy, X } from '@lucide/svelte'
  import { post } from '../../lib/api'
  import { errText, t } from '../../lib/i18n.svelte'
  import { toast } from '../../lib/ui/ui.svelte'
  import type { FileInfo } from '../../lib/types'
  import { app } from '../state.svelte'
  import { refOf, viewerItem } from '../actions'

  let { file, src, ondone }: { file: FileInfo; src: string; ondone: () => void } = $props()

  let rotation = $state(0)
  let cropping = $state(false)
  // Crop rectangle in fractions of the (unrotated) image.
  let crop = $state({ x: 0.1, y: 0.1, w: 0.8, h: 0.8 })
  let img: HTMLImageElement | undefined = $state()
  let saving = $state(false)

  function rotate(d: number) {
    rotation = (rotation + d + 360) % 360
  }

  // Translate a screen movement into image coordinates (the box is rotated).
  function toImage(dx: number, dy: number): [number, number] {
    switch (rotation) {
      case 90: return [dy, -dx]
      case 180: return [-dx, -dy]
      case 270: return [-dy, dx]
    }
    return [dx, dy]
  }

  function drag(e: PointerEvent, handle: string) {
    e.preventDefault()
    e.stopPropagation()
    const startX = e.clientX
    const startY = e.clientY
    const start = { ...crop }
    const w = img!.offsetWidth
    const h = img!.offsetHeight
    const move = (ev: PointerEvent) => {
      const [dx, dy] = toImage(ev.clientX - startX, ev.clientY - startY)
      const fx = dx / w
      const fy = dy / h
      let { x, y, w: cw, h: ch } = start
      if (handle === 'move') {
        x = Math.min(Math.max(0, x + fx), 1 - cw)
        y = Math.min(Math.max(0, y + fy), 1 - ch)
      } else {
        if (handle.includes('l')) { const nx = Math.min(Math.max(0, x + fx), x + cw - 0.05); cw += x - nx; x = nx }
        if (handle.includes('r')) cw = Math.min(Math.max(0.05, cw + fx), 1 - x)
        if (handle.includes('t')) { const ny = Math.min(Math.max(0, y + fy), y + ch - 0.05); ch += y - ny; y = ny }
        if (handle.includes('b')) ch = Math.min(Math.max(0.05, ch + fy), 1 - y)
      }
      crop = { x, y, w: cw, h: ch }
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  async function save(asCopy: boolean) {
    if (rotation === 0 && !cropping) {
      ondone()
      return
    }
    saving = true
    try {
      const res = await post<FileInfo>('/files/image-edit', {
        space: refOf(file),
        path: file.path,
        rotate: rotation,
        crop: cropping ? { X: crop.x, Y: crop.y, W: crop.w, H: crop.h } : null,
        copy: asCopy,
      })
      res.space = refOf(file)
      toast(asCopy ? t('editor.savedCopy') : t('editor.saved'), 'success')
      if (app.viewer) {
        const items = [...app.viewer.items]
        const item = viewerItem(res)
        if (asCopy) items.splice(app.viewer.index + 1, 0, item)
        else items[app.viewer.index] = item
        app.viewer = { items, index: asCopy ? app.viewer.index + 1 : app.viewer.index }
      }
      ondone()
    } catch (e) {
      toast(errText(e), 'error')
    } finally {
      saving = false
    }
  }
</script>

<div class="wrap">
  <div class="tools">
    <button class="btn ghost small" onclick={() => rotate(-90)}><RotateCcw size={16} />{t('editor.rotateLeft')}</button>
    <button class="btn ghost small" onclick={() => rotate(90)}><RotateCw size={16} />{t('editor.rotateRight')}</button>
    <button class="btn ghost small" class:on={cropping} onclick={() => (cropping = !cropping)}><Crop size={16} />{t('editor.crop')}</button>
    <span class="spacer"></span>
    <button class="btn ghost small" onclick={ondone}><X size={16} />{t('common.cancel')}</button>
    <button class="btn small" disabled={saving} onclick={() => save(true)}><Copy size={16} />{t('editor.saveCopy')}</button>
    <button class="btn primary small" disabled={saving} onclick={() => save(false)}><Save size={16} />{t('editor.saveOverwrite')}</button>
  </div>
  <div class="canvas">
    <div class="rot" style:transform="rotate({rotation}deg)">
      <img bind:this={img} {src} alt="" draggable="false" />
      {#if cropping}
        <div class="shade" style:clip-path="polygon(0 0, 100% 0, 100% 100%, 0 100%, 0 0, {crop.x * 100}% {crop.y * 100}%, {crop.x * 100}% {(crop.y + crop.h) * 100}%, {(crop.x + crop.w) * 100}% {(crop.y + crop.h) * 100}%, {(crop.x + crop.w) * 100}% {crop.y * 100}%, {crop.x * 100}% {crop.y * 100}%)"></div>
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div
          class="box"
          style:left="{crop.x * 100}%"
          style:top="{crop.y * 100}%"
          style:width="{crop.w * 100}%"
          style:height="{crop.h * 100}%"
          onpointerdown={(e) => drag(e, 'move')}
        >
          {#each ['tl', 'tr', 'bl', 'br'] as h}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <span class="handle {h}" onpointerdown={(e) => drag(e, h)}></span>
          {/each}
        </div>
      {/if}
    </div>
  </div>
  <p class="hint">{t('editor.overwriteHint')}</p>
</div>

<style>
  .wrap {
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .tools {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    background: #1f2329;
    padding: 8px;
    border-radius: 10px;
  }
  .tools :global(.btn.ghost) {
    color: #e8eaed;
  }
  .tools :global(.btn.ghost:hover),
  .tools :global(.btn.on) {
    background: rgba(255, 255, 255, 0.12);
  }
  .canvas {
    flex: 1;
    min-height: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
  }
  .rot {
    position: relative;
    transition: transform 0.2s;
    max-width: 100%;
    max-height: 100%;
    line-height: 0;
  }
  img {
    max-width: min(80vw, 100%);
    max-height: 70vh;
    user-select: none;
  }
  .shade {
    position: absolute;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    pointer-events: none;
  }
  .box {
    position: absolute;
    border: 2px solid #fff;
    cursor: move;
    touch-action: none;
  }
  .handle {
    position: absolute;
    width: 16px;
    height: 16px;
    background: #fff;
    border-radius: 3px;
    touch-action: none;
  }
  .tl { left: -8px; top: -8px; cursor: nwse-resize; }
  .tr { right: -8px; top: -8px; cursor: nesw-resize; }
  .bl { left: -8px; bottom: -8px; cursor: nesw-resize; }
  .br { right: -8px; bottom: -8px; cursor: nwse-resize; }
  .hint {
    text-align: center;
    color: #9aa0a6;
    font-size: 12px;
    margin: 0;
  }
</style>
