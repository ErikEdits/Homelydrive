<script lang="ts">
  import {
    Folder, FileImage, FileVideo, FileAudio, FileText, FileCode, FileArchive, FileSpreadsheet,
    Presentation, File, Package, BookOpen, FolderLock,
  } from '@lucide/svelte'
  import { ext } from '../format'

  let { name, isDir = false, category = '', color = '', size = 22, locked = false }:
    { name: string; isDir?: boolean; category?: string; color?: string; size?: number; locked?: boolean } = $props()

  const codeExt = new Set(['js', 'ts', 'tsx', 'jsx', 'go', 'py', 'java', 'kt', 'c', 'h', 'cpp', 'cs', 'rs', 'rb', 'php', 'sh', 'json', 'xml', 'yaml', 'yml', 'html', 'css', 'sql', 'svelte', 'vue', 'toml', 'ini'])

  const info = $derived.by(() => {
    if (isDir) return { icon: locked ? FolderLock : Folder, color: color || '#5f6c80', fill: true }
    const e = ext(name)
    switch (category) {
      case 'image': return { icon: FileImage, color: '#d93025' }
      case 'video': return { icon: FileVideo, color: '#d93025' }
      case 'audio': return { icon: FileAudio, color: '#e8710a' }
      case 'pdf': return { icon: FileText, color: '#c5221f' }
      case 'archive': return { icon: FileArchive, color: '#795548' }
      case 'app': return { icon: Package, color: '#188038' }
    }
    if (['xls', 'xlsx', 'ods', 'csv'].includes(e)) return { icon: FileSpreadsheet, color: '#188038' }
    if (['ppt', 'pptx', 'odp', 'key'].includes(e)) return { icon: Presentation, color: '#e37400' }
    if (['doc', 'docx', 'odt', 'rtf'].includes(e)) return { icon: FileText, color: '#1a73e8' }
    if (e === 'epub') return { icon: BookOpen, color: '#8e24aa' }
    if (codeExt.has(e)) return { icon: FileCode, color: '#1967d2' }
    if (category === 'text') return { icon: FileText, color: '#5f6c80' }
    return { icon: File, color: '#5f6c80' }
  })
</script>

<info.icon {size} color={info.color} fill={info.fill ? info.color : 'none'} fill-opacity={info.fill ? 0.25 : 0} strokeWidth={1.8} />
