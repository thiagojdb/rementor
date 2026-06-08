import { Component, createEffect, createSignal, For, Show } from 'solid-js'
import { workspaces, loadWorkspaces, loading, deleteWorkspace, syncWorkspaceRouting } from '../stores/workspaces'
import WorkspaceCreateModal from '../components/modals/WorkspaceCreateModal'
import WorkspaceDetailModal from '../components/modals/WorkspaceDetailModal'
import DeleteWorkspaceModal from '../components/modals/DeleteWorkspaceModal'
import type { WorkspaceDTO } from '../api/types'
import { useNavigate } from '@solidjs/router'
import { toast } from '../stores/toast'

const ConfigurationPage: Component = () => {
  const [showCreateModal, setShowCreateModal] = createSignal(false)
  const [editingWorkspace, setEditingWorkspace] = createSignal<WorkspaceDTO | null>(null)
  const [deletingWorkspace, setDeletingWorkspace] = createSignal<WorkspaceDTO | null>(null)
  const navigate = useNavigate()

  createEffect(() => {
    if (workspaces.length === 0) {
      loadWorkspaces()
    }
  })

  const handleCreated = (wsId: string) => {
    navigate(`/?ws=${wsId}`)
  }

  const handleDeleteClick = (ws: WorkspaceDTO, e: MouseEvent) => {
    e.stopPropagation()
    setDeletingWorkspace(ws)
  }

  const handleDeleteConfirm = async () => {
    const ws = deletingWorkspace()
    if (!ws) return
    await deleteWorkspace(ws.id)
    toast.success('Workspace deleted')
    setDeletingWorkspace(null)
  }

  const handleSyncRouting = async (ws: WorkspaceDTO, e: MouseEvent) => {
    e.stopPropagation()
    try {
      await syncWorkspaceRouting(ws.id)
      toast.success(`Routes synced for '${ws.name}'`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to sync routes')
    }
  }

  return (
    <>
      {/* Page header */}
      <div
        class="sticky top-0 z-20 backdrop-blur"
        style={{
          'background-color': 'var(--bg-secondary)',
          'border-bottom': '1px solid var(--border-subtle)'
        }}
      >
        <div class="flex items-center justify-between px-5 py-4">
          <div>
            <h1 class="font-mono text-lg font-bold" style={{ color: 'var(--text-primary)' }}>Configuration</h1>
            <p class="text-xs" style={{ color: 'var(--text-tertiary)' }}>Manage workspaces and applications</p>
          </div>
          <button
            class="inline-flex items-center justify-center gap-1.5 px-3 py-2 font-mono text-xs font-semibold border rounded-lg transition-colors"
            style={{
              'background-color': 'var(--accent-500)',
              'border-color': 'var(--accent-500)',
              color: 'var(--bg-primary)'
            }}
            onClick={() => setShowCreateModal(true)}
          >
            + New workspace
          </button>
        </div>
      </div>

      {/* Content */}
      <div class="flex-1 p-5">
        <Show when={loading() && workspaces.length === 0}>
          <div class="py-12 text-center" style={{ color: 'var(--text-tertiary)' }}>
            <div class="inline-flex items-center gap-2">
              <svg class="animate-spin w-4 h-4" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
              </svg>
              Loading...
            </div>
          </div>
        </Show>

        <Show when={workspaces.length === 0 && !loading()}>
          <div class="py-12 text-center" style={{ color: 'var(--text-tertiary)' }}>
            No workspaces yet. Create one to get started.
          </div>
        </Show>

        <div class="flex flex-col gap-3">
          <For each={workspaces}>
            {(ws) => (
              <div
                class="rounded-lg p-4 cursor-pointer transition-all hover:shadow-lg"
                style={{
                  'background-color': 'var(--bg-secondary)',
                  'border': '1px solid var(--border-subtle)'
                }}
                onClick={() => setEditingWorkspace(ws)}
              >
                <div class="flex items-center justify-between gap-4">
                  <div class="min-w-0">
                    <h3 class="font-mono font-bold text-sm mb-1 truncate" style={{ color: 'var(--text-primary)' }}>{ws.name}</h3>
                    <div class="flex gap-2 flex-wrap">
                      <span
                        class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide"
                        style={{
                          'background-color': 'var(--accent-subtle)',
                          color: 'var(--accent-400)'
                        }}
                      >
                        {ws.type}
                      </span>
                      <Show when={ws.type !== 'local-apps' && ws.routing}>
                        <span
                          class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide"
                          style={{
                            'background-color': 'var(--bg-tertiary)',
                            color: 'var(--text-secondary)'
                          }}
                        >
                          {ws.routing!.localDomain}
                        </span>
                        {ws.routing!.defaultRemoteBaseUrl && (
                          <span
                            class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide"
                            style={{
                              'border': '1px solid var(--border-subtle)',
                              color: 'var(--text-tertiary)'
                            }}
                          >
                            {ws.routing!.defaultRemoteBaseUrl}
                          </span>
                        )}
                      </Show>
                      <span
                        class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide"
                        style={{
                          'border': '1px solid var(--border-subtle)',
                          color: 'var(--text-tertiary)'
                        }}
                      >
                        {ws.applications.length} apps
                      </span>
                    </div>
                  </div>
                  <div class="flex gap-2 flex-shrink-0">
                    <Show when={ws.type !== 'local-apps'}>
                      <button
                        class="inline-flex items-center justify-center gap-1.5 px-3 py-2 font-mono text-xs font-semibold border rounded-lg transition-colors"
                        style={{
                          'background-color': 'transparent',
                          'border-color': 'var(--accent-500)',
                          color: 'var(--accent-400)'
                        }}
                        onClick={(e) => handleSyncRouting(ws, e)}
                        title="Reload nginx routes from current workspace state"
                      >
                        Sync routes
                      </button>
                    </Show>
                    <button
                      class="inline-flex items-center justify-center gap-1.5 px-3 py-2 font-mono text-xs font-semibold border rounded-lg transition-colors"
                      style={{
                        'background-color': 'transparent',
                        'border-color': 'var(--border-default)',
                        color: 'var(--text-secondary)'
                      }}
                    >
                      Edit
                    </button>
                    <button
                      class="inline-flex items-center justify-center gap-1.5 px-3 py-2 font-mono text-xs font-semibold border rounded-lg transition-colors"
                      style={{
                        'background-color': 'transparent',
                        'border-color': 'var(--error)',
                        color: 'var(--error)'
                      }}
                      onClick={(e) => handleDeleteClick(ws, e)}
                      title="Delete workspace"
                    >
                      Delete
                    </button>
                  </div>
                </div>

                <Show when={ws.applications.length > 0}>
                  <div class="mt-3 flex gap-1.5 flex-wrap">
                    <For each={ws.applications}>
                      {(app) => (
                        <span
                          class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-mono text-[10px]"
                          style={{
                            'background-color': 'var(--bg-tertiary)',
                            color: 'var(--text-tertiary)'
                          }}
                        >
                          {app.id}
                        </span>
                      )}
                    </For>
                  </div>
                </Show>
              </div>
            )}
          </For>
        </div>
      </div>

      {/* Modals */}
      <Show when={showCreateModal()}>
        <WorkspaceCreateModal
          onClose={() => setShowCreateModal(false)}
          onCreated={handleCreated}
        />
      </Show>

      <Show when={editingWorkspace()}>
        <WorkspaceDetailModal
          workspace={editingWorkspace()!}
          onClose={() => setEditingWorkspace(null)}
        />
      </Show>

      <DeleteWorkspaceModal
        workspace={deletingWorkspace()!}
        open={!!deletingWorkspace()}
        onClose={() => setDeletingWorkspace(null)}
        onConfirm={handleDeleteConfirm}
      />
    </>
  )
}

export default ConfigurationPage
