// Global UI helpers: toasts and promise-based confirm/prompt dialogs.

export interface Toast {
  id: number
  text: string
  kind: 'info' | 'error' | 'success'
  action?: { label: string; run: () => void }
}

export const toasts = $state<Toast[]>([])
let nextId = 1

export function toast(text: string, kind: Toast['kind'] = 'info', action?: Toast['action'], ms = 4500) {
  const id = nextId++
  toasts.push({ id, text, kind, action })
  setTimeout(() => {
    const i = toasts.findIndex((t) => t.id === id)
    if (i >= 0) toasts.splice(i, 1)
  }, ms)
}

export interface DialogState {
  kind: 'confirm' | 'prompt'
  title: string
  message?: string
  value?: string
  placeholder?: string
  okLabel?: string
  danger?: boolean
  inputType?: string
  resolve: (v: any) => void
}

export const dialog = $state<{ current: DialogState | null }>({ current: null })

export function confirmDialog(title: string, message?: string, opts: { okLabel?: string; danger?: boolean } = {}): Promise<boolean> {
  return new Promise((resolve) => {
    dialog.current = { kind: 'confirm', title, message, resolve, ...opts }
  })
}

export function promptDialog(
  title: string,
  value = '',
  opts: { message?: string; okLabel?: string; placeholder?: string; inputType?: string } = {},
): Promise<string | null> {
  return new Promise((resolve) => {
    dialog.current = { kind: 'prompt', title, value, resolve, ...opts }
  })
}
