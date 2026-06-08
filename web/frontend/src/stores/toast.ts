import { createStore } from 'solid-js/store'

export type ToastType = 'success' | 'error' | 'info'

export interface Toast {
  id: number
  type: ToastType
  message: string
}

let nextId = 0
const [toasts, setToasts] = createStore<Toast[]>([])

export { toasts }

export function addToast(type: ToastType, message: string): void {
  const id = nextId++
  setToasts((t) => [...t, { id, type, message }])
  setTimeout(() => removeToast(id), 3000)
}

export function removeToast(id: number): void {
  setToasts((t) => t.filter((toast) => toast.id !== id))
}

export const toast = {
  success: (msg: string) => addToast('success', msg),
  error: (msg: string) => addToast('error', msg),
  info: (msg: string) => addToast('info', msg),
}
