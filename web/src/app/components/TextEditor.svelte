<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { EditorView, basicSetup } from 'codemirror'
  import { EditorState, Compartment } from '@codemirror/state'
  import { keymap } from '@codemirror/view'
  import { LanguageDescription } from '@codemirror/language'
  import { languages } from '@codemirror/language-data'
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'
  import { Save, Eye, Code, Columns2 } from '@lucide/svelte'
  import { t, errText } from '../../lib/i18n.svelte'
  import { toast } from '../../lib/ui/ui.svelte'
  import type { ViewerItem } from '../state.svelte'

  let { item }: { item: ViewerItem } = $props()

  let host: HTMLDivElement | undefined = $state()
  let view: EditorView | null = null
  let dirty = $state(false)
  let saving = $state(false)
  let loading = $state(true)
  let error = $state('')
  let text = $state('')
  const isMarkdown = $derived(item.viewer === 'markdown')
  let mode = $state<'edit' | 'split' | 'preview'>('edit')
  const canWrite = $derived(!!item.file?.can_write)
  const lang = new Compartment()

  const theme = EditorView.theme({
    '&': { height: '100%', fontSize: '13.5px', backgroundColor: 'var(--surface)', color: 'var(--text)' },
    '.cm-scroller': { fontFamily: 'var(--mono)', lineHeight: '1.55' },
    '.cm-gutters': { backgroundColor: 'var(--surface-2)', color: 'var(--text-3)', border: 'none' },
    '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'var(--hover)' },
    '&.cm-focused': { outline: 'none' },
    '.cm-cursor': { borderLeftColor: 'var(--text)' },
    '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { backgroundColor: 'var(--selected) !important' },
  })

  async function save() {
    if (!view || !item.file || saving) return
    saving = true
    try {
      const body = view.state.doc.toString()
      const res = await fetch(item.url, { method: 'PUT', headers: { 'X-HD': '1', 'Content-Type': 'text/plain; charset=utf-8' }, body })
      if (!res.ok) {
        const j = await res.json().catch(() => ({}))
        throw Object.assign(new Error(j.error || res.statusText), { code: j.code })
      }
      dirty = false
      toast(t('editor.saved'), 'success')
    } catch (e) {
      toast(errText(e), 'error')
    } finally {
      saving = false
    }
  }

  onMount(async () => {
    if (isMarkdown) mode = 'split'
    try {
      const res = await fetch(item.url)
      if (!res.ok) throw new Error(res.statusText)
      text = await res.text()
    } catch (e) {
      error = errText(e)
      loading = false
      return
    }
    loading = false
    await Promise.resolve()
    view = new EditorView({
      parent: host!,
      state: EditorState.create({
        doc: text,
        extensions: [
          basicSetup,
          theme,
          lang.of([]),
          EditorView.lineWrapping,
          EditorState.readOnly.of(!canWrite),
          keymap.of([{ key: 'Mod-s', preventDefault: true, run: () => (save(), true) }]),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) {
              dirty = true
              text = u.state.doc.toString()
            }
          }),
        ],
      }),
    })
    const desc = LanguageDescription.matchFilename(languages, item.name)
    if (desc) {
      const support = await desc.load()
      view.dispatch({ effects: lang.reconfigure(support) })
    }
  })

  onDestroy(() => view?.destroy())

  const html = $derived(isMarkdown && mode !== 'edit' ? DOMPurify.sanitize(marked.parse(text, { async: false }) as string) : '')
</script>

<div class="editor">
  <div class="bar">
    {#if isMarkdown}
      <div class="seg">
        <button class:active={mode === 'edit'} onclick={() => (mode = 'edit')}><Code size={16} />{t('editor.edit')}</button>
        <button class:active={mode === 'split'} onclick={() => (mode = 'split')}><Columns2 size={16} />{t('editor.split')}</button>
        <button class:active={mode === 'preview'} onclick={() => (mode = 'preview')}><Eye size={16} />{t('editor.preview')}</button>
      </div>
    {/if}
    <span class="spacer"></span>
    {#if !canWrite}<span class="chip">{t('files.readOnly')}</span>{/if}
    {#if dirty}<span class="muted small">{t('editor.unsaved')}</span>{/if}
    {#if canWrite}
      <button class="btn primary small" onclick={save} disabled={!dirty || saving}><Save size={16} />{t('editor.save')}</button>
    {/if}
  </div>
  {#if loading}
    <div class="msg">…</div>
  {:else if error}
    <div class="msg error-text">{error}</div>
  {/if}
  <div class="panes" class:split={mode === 'split'}>
    <div class="code" bind:this={host} class:hidden={mode === 'preview'}></div>
    {#if isMarkdown && mode !== 'edit'}
      <article class="md">{@html html}</article>
    {/if}
  </div>
</div>

<style>
  .editor {
    width: min(1200px, 100%);
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--surface);
    color: var(--text);
    border-radius: 10px;
    overflow: hidden;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--border);
  }
  .seg {
    display: flex;
    border: 1px solid var(--border);
    border-radius: 16px;
    overflow: hidden;
  }
  .seg button {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    border: none;
    background: none;
    padding: 4px 12px;
    cursor: pointer;
    color: var(--text-2);
  }
  .seg button.active {
    background: var(--selected);
    color: var(--brand-strong);
  }
  .panes {
    flex: 1;
    min-height: 0;
    display: flex;
  }
  .code {
    flex: 1;
    min-width: 0;
    height: 100%;
    overflow: hidden;
  }
  .code.hidden {
    display: none;
  }
  .panes.split .code,
  .panes.split .md {
    width: 50%;
    flex: none;
  }
  .md {
    flex: 1;
    overflow: auto;
    padding: 20px 28px;
    border-left: 1px solid var(--border);
    line-height: 1.65;
  }
  .md :global(pre) {
    background: var(--surface-2);
    padding: 12px;
    border-radius: 8px;
    overflow: auto;
  }
  .md :global(img) {
    max-width: 100%;
  }
  .md :global(table) {
    border-collapse: collapse;
  }
  .md :global(td),
  .md :global(th) {
    border: 1px solid var(--border);
    padding: 4px 8px;
  }
  .msg {
    padding: 20px;
  }
</style>
