import { Component, For } from 'solid-js'
import { toasts, removeToast } from '../../stores/toast'

const ToastContainer: Component = () => {
  const getToastStyles = (type: 'success' | 'error' | 'info') => {
    const base = {
      'background-color': 'var(--bg-secondary)',
      'backdrop-filter': 'blur(4px)'
    }

    if (type === 'success') {
      return {
        ...base,
        'border': '1px solid var(--success)',
        color: 'var(--success)'
      }
    }
    if (type === 'error') {
      return {
        ...base,
        'border': '1px solid var(--error)',
        color: 'var(--error)'
      }
    }
    return {
      ...base,
      'border': '1px solid var(--accent-400)',
      color: 'var(--accent-400)'
    }
  }

  const getIconStyles = (type: 'success' | 'error' | 'info') => {
    if (type === 'success') {
      return {
        'background-color': 'var(--success)',
        color: 'var(--bg-primary)'
      }
    }
    if (type === 'error') {
      return {
        'background-color': 'var(--error)',
        color: 'white'
      }
    }
    return {
      'background-color': 'var(--accent-400)',
      color: 'var(--bg-primary)'
    }
  }

  return (
    <div class="fixed bottom-6 right-6 z-[100] flex flex-col gap-2 pointer-events-none">
      <For each={toasts}>
        {(t) => (
          <div
            class="pointer-events-auto cursor-pointer min-w-[280px] max-w-[400px] px-4 py-3 rounded-lg flex items-center gap-2.5 font-mono text-xs font-medium animate-slide-in-right"
            style={{
              ...getToastStyles(t.type),
              animation: 'slide-in-right 0.25s ease, fade-out-right 0.3s ease 2.7s forwards'
            }}
            onClick={() => removeToast(t.id)}
          >
            <div
              class="w-5 h-5 rounded flex items-center justify-center text-xs font-bold flex-shrink-0"
              style={getIconStyles(t.type)}
            >
              {t.type === 'success' ? '✓' : t.type === 'error' ? '✕' : 'i'}
            </div>
            <span class="truncate" style={{ color: 'var(--text-primary)' }}>{t.message}</span>
          </div>
        )}
      </For>
    </div>
  )
}

export default ToastContainer
