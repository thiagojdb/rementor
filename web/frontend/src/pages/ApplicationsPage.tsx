import { Component, createEffect, createMemo, createSignal, For, onCleanup, Show } from 'solid-js'
import { useSearchParams } from '@solidjs/router'
import type { ApplicationDTO } from '../api/types'
import { workspaces, loadWorkspaces, loading, error, toggleAllToRemote, toggleAllToLocal, findWorkspace } from '../stores/workspaces'
import { connectHealthEvents } from '../stores/health'
import { toast } from '../stores/toast'
import AppCard from '../components/apps/AppCard'
import AppTable from '../components/apps/AppTable'
import AppDetailModal from '../components/modals/AppDetailModal'
import WorkspaceSwitcher from '../components/layout/WorkspaceSwitcher'
import SelectDropdown from '../components/ui/SelectDropdown'

type ViewMode = 'card' | 'table'

const ApplicationsPage: Component = () => {
  const [searchParams, setSearchParams] = useSearchParams()
  const [viewMode, setViewMode] = createSignal<ViewMode>((localStorage.getItem('view_mode') as ViewMode) || 'card')
  const [filter, setFilter] = createSignal('')
  const [statusFilter, setStatusFilter] = createSignal<'' | 'active' | 'inactive'>('')
  const [selectedApp, setSelectedApp] = createSignal<ApplicationDTO | null>(null)
  const [toggling, setToggling] = createSignal(false)

  // Load on mount
  createEffect(() => {
    loadWorkspaces()
  })

  // Active workspace
  const activeWsId = (): string => {
    const ws = searchParams.ws
    const wsStr = Array.isArray(ws) ? ws[0] : ws
    return wsStr || (workspaces.length > 0 ? workspaces[0]?.id : '') || ''
  }

  const activeWorkspace = createMemo(() => findWorkspace(activeWsId()))

  // Connect server stream for active workspace
  let disconnectHealthStream: (() => void) | null = null
  createEffect(() => {
    const wsId = activeWsId()
    if (!wsId) return
    if (disconnectHealthStream) disconnectHealthStream()
    disconnectHealthStream = connectHealthEvents(wsId)
  })
  onCleanup(() => { if (disconnectHealthStream) disconnectHealthStream() })

  // Filtered apps
  const filteredApps = createMemo(() => {
    const ws = activeWorkspace()
    if (!ws) return []
    return ws.applications.filter((app) => {
      const matchesFilter = !filter() || app.id.toLowerCase().includes(filter().toLowerCase()) || app.name.toLowerCase().includes(filter().toLowerCase())
      const matchesStatus = !statusFilter() ||
        (statusFilter() === 'active' && app.active) ||
        (statusFilter() === 'inactive' && !app.active)
      return matchesFilter && matchesStatus
    })
  })

  const isLocalApps = createMemo(() => activeWorkspace()?.type === 'local-apps')
  const activeCount = createMemo(() => activeWorkspace()?.applications.filter((a) => a.active).length ?? 0)
  const totalCount = createMemo(() => activeWorkspace()?.applications.length ?? 0)
  const healthyCount = createMemo(() => activeWorkspace()?.applications.filter((a) => a.healthStatus === 'healthy').length ?? 0)

  const handleSetViewMode = (mode: ViewMode) => {
    setViewMode(mode)
    localStorage.setItem('view_mode', mode)
  }

  const handleToggleAll = async (direction: 'remote' | 'local') => {
    const wsId = activeWsId()
    if (!wsId || toggling()) return
    setToggling(true)
    try {
      if (direction === 'remote') await toggleAllToRemote(wsId)
      else await toggleAllToLocal(wsId)
      toast.success(`All apps switched to ${direction}`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : `Failed to toggle to ${direction}`)
    } finally {
      setToggling(false)
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
        <div class="flex items-center gap-2 flex-wrap px-5 py-3">
          {/* Workspace switcher */}
          <WorkspaceSwitcher
            workspaces={workspaces}
            activeId={activeWsId()}
            onChange={(id) => setSearchParams({ ws: id })}
          />

          {/* Filter input */}
          <input
            class="flex-1 min-w-0 h-[34px] px-3 rounded-lg border text-sm placeholder:text-tertiary focus:outline-none transition-colors"
            style={{
              'background-color': 'var(--bg-tertiary)',
              'border-color': 'var(--border-default)',
              color: 'var(--text-primary)'
            }}
            type="text"
            placeholder="Filter applications..."
            value={filter()}
            onInput={(e) => setFilter(e.currentTarget.value)}
          />

          {/* Status filter — only for routing workspaces */}
          <Show when={!isLocalApps()}>
            <SelectDropdown
              value={statusFilter()}
              options={[
                { value: '',         label: 'All status'      },
                { value: 'active',   label: 'Local (active)'  },
                { value: 'inactive', label: 'Remote (inactive)'},
              ]}
              onChange={(v) => setStatusFilter(v as '' | 'active' | 'inactive')}
            />
          </Show>

          {/* Bulk ops — only for routing workspaces */}
          <Show when={!isLocalApps()}>
            <span class="w-px h-5 shrink-0" style={{ 'background-color': 'var(--border-subtle)' }} />
            <button
              class="inline-flex items-center justify-center gap-1.5 h-[34px] px-3 font-mono text-xs font-semibold border rounded-lg transition-colors disabled:opacity-50"
              style={{
                'background-color': 'transparent',
                'border-color': 'var(--border-default)',
                color: 'var(--text-secondary)'
              }}
              onClick={() => handleToggleAll('remote')}
              disabled={toggling()}
              title="Switch all apps to remote"
            >
              {/* cloud-arrow-up */}
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">
                <path d="M12 16.5V9.75m0 0 3 3m-3-3-3 3M6.75 19.5a4.5 4.5 0 0 1-1.41-8.775 5.25 5.25 0 0 1 10.338-2.32A5.25 5.25 0 0 1 18 19.5H6.75Z" />
              </svg>
              Remote
            </button>
            <button
              class="inline-flex items-center justify-center gap-1.5 h-[34px] px-3 font-mono text-xs font-semibold border rounded-lg transition-colors disabled:opacity-50"
              style={{
                'background-color': 'transparent',
                'border-color': 'var(--border-default)',
                color: 'var(--text-secondary)'
              }}
              onClick={() => handleToggleAll('local')}
              disabled={toggling()}
              title="Switch all apps to local"
            >
              {/* arrow-down-tray */}
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">
                <path d="M9 8.25H7.5a2.25 2.25 0 0 0-2.25 2.25v9a2.25 2.25 0 0 0 2.25 2.25h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25H15m0-3-3 3m0 0-3-3m3 3V2.25" />
              </svg>
              Local
            </button>
          </Show>

          {/* Divider */}
          <span class="w-px h-5 shrink-0" style={{ 'background-color': 'var(--border-subtle)' }} />

          {/* View toggle */}
          <div
            class="flex gap-0.5 p-[3px] rounded-lg shrink-0"
            style={{
              'background-color': 'var(--bg-primary)',
              'border': '1px solid var(--border-subtle)'
            }}
          >
            <button
              class={`w-7 h-7 flex items-center justify-center rounded-md transition-colors ${
                viewMode() === 'card'
                  ? 'shadow-sm'
                  : ''
              }`}
              style={viewMode() === 'card'
                ? {
                    'background-color': 'var(--bg-hover)',
                    color: 'var(--accent-400)'
                  }
                : { color: 'var(--text-tertiary)' }
              }
              onClick={() => handleSetViewMode('card')}
              title="Card view"
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <rect x="3" y="3" width="7" height="7" rx="1" />
                <rect x="14" y="3" width="7" height="7" rx="1" />
                <rect x="3" y="14" width="7" height="7" rx="1" />
                <rect x="14" y="14" width="7" height="7" rx="1" />
              </svg>
            </button>
            <button
              class={`w-7 h-7 flex items-center justify-center rounded-md transition-colors ${
                viewMode() === 'table'
                  ? 'shadow-sm'
                  : ''
              }`}
              style={viewMode() === 'table'
                ? {
                    'background-color': 'var(--bg-hover)',
                    color: 'var(--accent-400)'
                  }
                : { color: 'var(--text-tertiary)' }
              }
              onClick={() => handleSetViewMode('table')}
              title="Table view"
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path stroke-linecap="round" stroke-linejoin="round" d="M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0zM3.75 12h.007v.008H3.75V12zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0zm-.375 5.25h.007v.008H3.75v-.008zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0z" />
              </svg>
            </button>
          </div>
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
              Loading workspaces...
            </div>
          </div>
        </Show>

        <Show when={error()}>
          <div
            class="rounded-lg text-sm mb-4 px-4 py-3"
            style={{
              'background-color': 'var(--error-subtle)',
              'border': '1px solid var(--error)',
              color: 'var(--error)'
            }}
          >
            {error()}
          </div>
        </Show>

        <Show when={!loading() && workspaces.length === 0 && !error()}>
          <div class="py-12 text-center" style={{ color: 'var(--text-tertiary)' }}>
            No workspaces configured. Go to Configuration to add one.
          </div>
        </Show>

        <Show when={activeWorkspace()}>
          {/* Empty states */}
          <Show when={filteredApps().length === 0}>
            <div
              class="flex flex-col items-center justify-center py-16 gap-3"
              style={{ color: 'var(--text-tertiary)' }}
            >
              <Show
                when={totalCount() === 0}
                fallback={
                  /* Filter returned nothing */
                  <>
                    <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" style={{ opacity: '0.4' }}>
                      <circle cx="11" cy="11" r="8" />
                      <path d="m21 21-4.35-4.35M11 8v6M8 11h6" />
                    </svg>
                    <p class="font-mono text-sm">No applications match the current filters</p>
                    <button
                      class="font-mono text-xs underline underline-offset-2 transition-colors"
                      style={{ color: 'var(--accent-400)' }}
                      onClick={() => { setFilter(''); setStatusFilter('') }}
                    >
                      Clear filters
                    </button>
                  </>
                }
              >
                /* Workspace has no apps */
                <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" style={{ opacity: '0.4' }}>
                  <rect x="2" y="3" width="20" height="14" rx="2" />
                  <path d="M8 21h8M12 17v4" />
                </svg>
                <p class="font-mono text-sm">No applications configured in this workspace</p>
              </Show>
            </div>
          </Show>

          {/* Content */}
          <Show when={filteredApps().length > 0}>
            {viewMode() === 'card' ? (
              <div class="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
                <For each={filteredApps()}>
                  {(app) => (
                    <AppCard
                      workspace={activeWorkspace()!}
                      app={app}
                      onOpenDetail={setSelectedApp}
                    />
                  )}
                </For>
              </div>
            ) : (
              <AppTable
                workspace={activeWorkspace()!}
                apps={filteredApps()}
                onOpenDetail={setSelectedApp}
              />
            )}
          </Show>
        </Show>
      </div>

      {/* Footer stats */}
      <div
        class="px-5 py-2 flex gap-4 items-center font-mono text-[11px] font-medium"
        style={{
          'border-top': '1px solid var(--border-subtle)',
          'background-color': 'var(--bg-secondary)',
          color: 'var(--text-tertiary)'
        }}
      >
        <Show when={!isLocalApps()} fallback={<span>{totalCount()} apps</span>}>
          <span>{activeCount()} / {totalCount()} local</span>
        </Show>
        <span>·</span>
        <span>{healthyCount()} healthy</span>
        <span>·</span>
        <span>{filteredApps().length} shown</span>
      </div>

      {/* App detail modal */}
      <Show when={selectedApp() && activeWorkspace()}>
        <AppDetailModal
          workspace={activeWorkspace()!}
          app={selectedApp()!}
          onClose={() => setSelectedApp(null)}
        />
      </Show>
    </>
  )
}

export default ApplicationsPage
