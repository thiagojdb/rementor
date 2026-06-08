import { Component, createSignal, Show } from 'solid-js'
import { Portal } from 'solid-js/web'
import type { WorkspaceDTO } from '../../api/types'

interface DeleteWorkspaceModalProps {
  workspace: WorkspaceDTO
  open: boolean
  onClose: () => void
  onConfirm: () => Promise<void>
}

const DeleteWorkspaceModal: Component<DeleteWorkspaceModalProps> = (props) => {
  const [deleting, setDeleting] = createSignal(false)
  const [isClosing, setIsClosing] = createSignal(false)

  const handleClose = () => {
    if (deleting()) return
    setIsClosing(true)
    setTimeout(() => {
      setIsClosing(false)
      props.onClose()
    }, 230)
  }

  const handleConfirm = async () => {
    if (deleting()) return
    setDeleting(true)
    try {
      await props.onConfirm()
      props.onClose()
    } finally {
      setDeleting(false)
    }
  }

  const handleBackdropClick = (e: MouseEvent) => {
    if (e.target === e.currentTarget) handleClose()
  }

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape') handleClose()
  }

  return (
    <Show when={props.open}>
      <Portal>
        <div
          class="fixed inset-0 z-50 flex items-center justify-center p-4"
          onClick={handleBackdropClick}
          onKeyDown={handleKeyDown}
          role="presentation"
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

          <div
            class="rounded-xl shadow-2xl max-w-md w-full overflow-hidden relative"
            style={{
              'background-color': 'var(--bg-secondary)',
              'border': '1px solid var(--border-default)',
              animation: isClosing()
                ? 'modal-panel-exit 0.22s ease forwards'
                : 'modal-panel-enter 0.25s ease 0.05s forwards',
              opacity: isClosing() ? undefined : '0'
            }}
            role="alertdialog"
            aria-modal="true"
            aria-labelledby="delete-modal-title"
            aria-describedby="delete-modal-desc"
            onClick={(e) => e.stopPropagation()}
          >
            {/* Icon and Title */}
            <div class="px-6 pt-6 pb-4 text-center">
              <div
                class="w-14 h-14 rounded-full flex items-center justify-center mx-auto mb-4"
                style={{
                  'background-color': 'var(--error-subtle)',
                  'border': '1px solid var(--error)'
                }}
              >
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  width="28"
                  height="28"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  style={{ color: 'var(--error)' }}
                  stroke-width="2"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z"
                  />
                </svg>
              </div>

              <h2
                id="delete-modal-title"
                class="font-mono text-lg font-bold mb-2"
                style={{ color: 'var(--text-primary)' }}
              >
                Delete workspace?
              </h2>

              <p
                id="delete-modal-desc"
                class="text-sm leading-relaxed"
                style={{ color: 'var(--text-secondary)' }}
              >
                This will permanently delete{' '}
                <strong class="font-mono" style={{ color: 'var(--text-primary)' }}>
                  {props.workspace.name}
                </strong>{' '}
                and all its {props.workspace.applications.length} application
                {props.workspace.applications.length === 1 ? '' : 's'}. This action cannot be
                undone.
              </p>
            </div>

            {/* Workspace Summary */}
            <div class="px-6 pb-5">
              <div
                class="rounded-lg p-3 flex items-center gap-3"
                style={{
                  'background-color': 'var(--bg-primary)',
                  'border': '1px solid var(--border-subtle)'
                }}
              >
                <div
                  class="w-9 h-9 rounded-lg flex items-center justify-center flex-shrink-0"
                  style={{ background: props.workspace.color || 'var(--accent-500)' }}
                >
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    width="18"
                    height="18"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="white"
                    stroke-width="2"
                  >
                    <rect x="3" y="3" width="7" height="7" rx="1" />
                    <rect x="14" y="3" width="7" height="7" rx="1" />
                    <rect x="3" y="14" width="7" height="7" rx="1" />
                    <rect x="14" y="14" width="7" height="7" rx="1" />
                  </svg>
                </div>
                <div class="min-w-0">
                  <div class="font-mono text-sm font-semibold truncate" style={{ color: 'var(--text-primary)' }}>
                    {props.workspace.name}
                  </div>
                  <div class="text-xs mt-0.5" style={{ color: 'var(--text-tertiary)' }}>
                    {props.workspace.routing?.localDomain ?? props.workspace.type}
                  </div>
                </div>
              </div>
            </div>

            {/* Actions */}
            <div class="px-6 pb-6 flex gap-3 justify-center">
              <button
                class="inline-flex items-center justify-center gap-1.5 px-4 py-2 font-mono text-xs font-semibold border rounded-lg transition-colors min-w-[100px]"
                style={{
                  'background-color': 'transparent',
                  'border-color': 'var(--border-default)',
                  color: 'var(--text-primary)'
                }}
                onClick={handleClose}
                disabled={deleting()}
              >
                Cancel
              </button>
              <button
                class="inline-flex items-center justify-center gap-1.5 px-4 py-2 font-mono text-xs font-semibold border rounded-lg transition-colors min-w-[100px]"
                style={{
                  'background-color': 'var(--error)',
                  'border-color': 'var(--error)',
                  color: 'white'
                }}
                onClick={handleConfirm}
                disabled={deleting()}
              >
                <Show when={deleting()} fallback={<>Delete</>}>
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    width="14"
                    height="14"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    stroke-width="2"
                    class="animate-spin"
                  >
                    <circle cx="12" cy="12" r="10" stroke-dasharray="60" stroke-dashoffset="20" />
                  </svg>
                  <span>Deleting...</span>
                </Show>
              </button>
            </div>
          </div>
        </div>
      </Portal>
    </Show>
  )
}

export default DeleteWorkspaceModal
