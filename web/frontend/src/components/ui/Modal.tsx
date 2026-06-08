import { ParentComponent, Show, createSignal, onCleanup, onMount } from 'solid-js'
import { Portal } from 'solid-js/web'

interface ModalProps {
  open: boolean
  onClose: () => void
  title: string
}

const Modal: ParentComponent<ModalProps> = (props) => {
  const [isClosing, setIsClosing] = createSignal(false)

  const handleClose = () => {
    setIsClosing(true)
    setTimeout(() => {
      setIsClosing(false)
      props.onClose()
    }, 230)
  }

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape') handleClose()
  }

  onMount(() => document.addEventListener('keydown', handleKeyDown))
  onCleanup(() => document.removeEventListener('keydown', handleKeyDown))

  return (
    <Show when={props.open}>
      <Portal>
        {/* Outer shell — layout only, handles backdrop click */}
        <div
          class="fixed inset-0 z-50 flex items-center justify-center p-4"
          onClick={(e) => { if (e.target === e.currentTarget) handleClose() }}
        >
          {/* Animated backdrop layer */}
          <div
            class="absolute inset-0 backdrop-blur-sm pointer-events-none"
            style={{
              'background-color': 'var(--bg-primary)',
              opacity: '0.85',
              animation: isClosing()
                ? 'modal-overlay-exit 0.2s ease forwards'
                : 'modal-overlay-enter 0.2s ease forwards'
            }}
          />

          {/* Panel */}
          <div
            class="rounded-xl shadow-2xl max-w-2xl w-full max-h-[90vh] overflow-hidden relative"
            style={{
              'background-color': 'var(--bg-secondary)',
              'border': '1px solid var(--border-default)',
              animation: isClosing()
                ? 'modal-panel-exit 0.22s ease forwards'
                : 'modal-panel-enter 0.25s ease 0.05s forwards',
              opacity: isClosing() ? undefined : '0'
            }}
            role="dialog"
            aria-modal="true"
            aria-label={props.title}
            onClick={(e) => e.stopPropagation()}
          >
            {/* Header */}
            <div
              class="flex items-center justify-between px-6 py-5"
              style={{ 'border-bottom': '1px solid var(--border-subtle)' }}
            >
              <div class="flex items-center gap-3">
                <div
                  class="w-10 h-10 rounded-lg flex items-center justify-center"
                  style={{
                    'background-color': 'var(--accent-subtle)',
                    'border': '1px solid var(--accent-400)',
                    color: 'var(--accent-400)'
                  }}
                >
                  <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <rect x="3" y="3" width="7" height="7" rx="1" />
                    <rect x="14" y="3" width="7" height="7" rx="1" />
                    <rect x="3" y="14" width="7" height="7" rx="1" />
                    <rect x="14" y="14" width="7" height="7" rx="1" />
                  </svg>
                </div>
                <h2 class="font-mono text-lg font-bold" style={{ color: 'var(--text-primary)' }}>{props.title}</h2>
              </div>
              <button
                class="w-8 h-8 rounded-lg flex items-center justify-center border border-transparent transition-colors"
                style={{ color: 'var(--text-tertiary)' }}
                onClick={handleClose}
                aria-label="Close modal"
              >
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18 18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            {/* Body */}
            <div class="p-6 max-h-[calc(90vh-5rem)] overflow-y-auto overflow-x-hidden">
              {props.children}
            </div>
          </div>
        </div>

        <style>{`
          button[aria-label="Close modal"]:hover {
            color: var(--error) !important;
            background-color: var(--error-subtle) !important;
            border-color: var(--error-subtle) !important;
          }
        `}</style>
      </Portal>
    </Show>
  )
}

export default Modal
