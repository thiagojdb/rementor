import { Component, createSignal, For, Show } from 'solid-js'
import Modal from '../ui/Modal'
import ApplicationManager from '../ApplicationManager'
import { createWorkspace } from '../../stores/workspaces'
import { toast } from '../../stores/toast'
import type { ApplicationConfigInput, WorkspaceType } from '../../api/types'

interface WorkspaceCreateModalProps {
  onClose: () => void
  onCreated: (wsId: string) => void
}

const WorkspaceCreateModal: Component<WorkspaceCreateModalProps> = (props) => {
  const [id, setId] = createSignal('')
  const [name, setName] = createSignal('')
  const [color, setColor] = createSignal('bg-cyan-500')
  const [wsType, setWsType] = createSignal<WorkspaceType>('routing')
  const [localDomain, setLocalDomain] = createSignal('')
  const [defaultRemoteBaseUrl, setDefaultRemoteBaseURL] = createSignal('')
  const [apps, setApps] = createSignal<ApplicationConfigInput[]>([])
  const [saving, setSaving] = createSignal(false)

  const isLocalApps = () => wsType() === 'local-apps'

  const handleAppsChange = (newApps: ApplicationConfigInput[]) => {
    setApps(newApps)
  }

  const handleSave = async () => {
    if (saving()) return
    const wsId = id().trim()
    if (!wsId) { toast.error('Workspace ID is required'); return }
    if (!/^[a-z0-9-]+$/.test(wsId)) { toast.error('ID must contain only lowercase letters, numbers, and hyphens'); return }

    if (!isLocalApps()) {
      if (!localDomain().trim()) { toast.error('Local domain is required'); return }
    }

    setSaving(true)
    try {
      const ws = await createWorkspace({
        id: wsId,
        type: wsType(),
        name: name().trim(),
        color: color(),
        localDomain: localDomain().trim(),
        defaultRemoteBaseUrl: defaultRemoteBaseUrl().trim(),
        applications: apps(),
      })
      toast.success(`Workspace '${ws.id}' created`)
      props.onCreated(ws.id)
      props.onClose()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to create workspace')
    } finally {
      setSaving(false)
    }
  }

  const colorOptions = [
    { class: 'bg-cyan-500', hex: '#06b6d4' },
    { class: 'bg-emerald-500', hex: '#10b981' },
    { class: 'bg-violet-500', hex: '#8b5cf6' },
    { class: 'bg-rose-500', hex: '#f43f5e' },
    { class: 'bg-amber-500', hex: '#f59e0b' },
    { class: 'bg-pink-500', hex: '#ec4899' },
    { class: 'bg-indigo-500', hex: '#6366f1' },
    { class: 'bg-teal-500', hex: '#14b8a6' },
  ]

  const sectionStyle = {
    'background-color': 'var(--bg-primary)',
    'border': '1px solid var(--border-subtle)',
  }

  const inputStyle = {
    'background-color': 'var(--bg-secondary)',
    'border-color': 'var(--border-default)',
    color: 'var(--text-primary)',
  }

  const typeButtonStyle = (active: boolean) => active
    ? {
        'background-color': 'var(--accent-500)',
        'border-color': 'var(--accent-500)',
        color: 'var(--bg-primary)',
      }
    : {
        'background-color': 'transparent',
        'border-color': 'var(--border-default)',
        color: 'var(--text-secondary)',
      }

  return (
    <Modal open={true} onClose={props.onClose} title="New Workspace">
      {/* Workspace Info Section */}
      <div class="rounded-lg p-4 mb-4" style={sectionStyle}>
        <div class="font-mono text-[11px] font-semibold uppercase tracking-wider mb-3 flex items-center gap-2" style={{ color: 'var(--text-tertiary)' }}>
          <span class="w-1 h-1 rounded-full" style={{ background: 'var(--accent-400)' }} />
          Workspace
        </div>
        <div class="flex flex-col gap-2.5">
          <div>
            <label class="block text-xs mb-1" style={{ color: 'var(--text-secondary)' }}>ID *</label>
            <input
              class="w-full h-9 px-3 rounded-lg border text-sm focus:outline-none transition-colors"
              style={inputStyle}
              type="text"
              placeholder="my-workspace"
              value={id()}
              onInput={(e) => setId(e.currentTarget.value)}
            />
          </div>
          <div>
            <label class="block text-xs mb-1" style={{ color: 'var(--text-secondary)' }}>Display name</label>
            <input
              class="w-full h-9 px-3 rounded-lg border text-sm focus:outline-none transition-colors"
              style={inputStyle}
              type="text"
              placeholder="My Workspace"
              value={name()}
              onInput={(e) => setName(e.currentTarget.value)}
            />
          </div>
          <div>
            <label class="block text-xs mb-1.5" style={{ color: 'var(--text-secondary)' }}>Type</label>
            <div class="flex gap-1.5">
              <button
                type="button"
                class="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 font-mono text-xs font-semibold border rounded-lg transition-colors"
                style={typeButtonStyle(!isLocalApps())}
                onClick={() => setWsType('routing')}
              >
                Routing
              </button>
              <button
                type="button"
                class="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 font-mono text-xs font-semibold border rounded-lg transition-colors"
                style={typeButtonStyle(isLocalApps())}
                onClick={() => setWsType('local-apps')}
              >
                Local Apps
              </button>
            </div>
          </div>
          <Show when={!isLocalApps()}>
            <div>
              <label class="block text-xs mb-1" style={{ color: 'var(--text-secondary)' }}>Local domain *</label>
              <input
                class="w-full h-9 px-3 rounded-lg border text-sm focus:outline-none transition-colors"
                style={inputStyle}
                type="text"
                placeholder="api.localhost"
                value={localDomain()}
                onInput={(e) => setLocalDomain(e.currentTarget.value)}
              />
            </div>
            <div>
              <label class="block text-xs mb-1" style={{ color: 'var(--text-secondary)' }}>Default remote base URL</label>
              <input
                class="w-full h-9 px-3 rounded-lg border text-sm focus:outline-none transition-colors"
                style={inputStyle}
                type="text"
                placeholder="https://api.example.com"
                value={defaultRemoteBaseUrl()}
                onInput={(e) => setDefaultRemoteBaseURL(e.currentTarget.value)}
              />
            </div>
          </Show>
          <div>
            <label class="block text-xs mb-1.5" style={{ color: 'var(--text-secondary)' }}>Color</label>
            <div class="flex gap-1.5 flex-wrap">
              <For each={colorOptions}>
                {(c) => (
                  <button
                    type="button"
                    class={`w-6 h-6 rounded-full border-2 transition-all ${
                      color() === c.class
                        ? 'border-current scale-110'
                        : 'border-transparent hover:scale-105'
                    }`}
                    style={{
                      background: c.hex,
                      'border-color': color() === c.class ? 'var(--text-primary)' : 'transparent'
                    }}
                    onClick={() => setColor(c.class)}
                    title={c.class}
                  />
                )}
              </For>
            </div>
          </div>
        </div>
      </div>

      {/* Applications Section with ApplicationManager */}
      <div class="rounded-lg p-4 mb-4" style={sectionStyle}>
        <ApplicationManager
          workspaceType={wsType()}
          initialApps={[]}
          onChange={handleAppsChange}
        />
      </div>

      {/* Actions */}
      <div class="flex gap-2 justify-end">
        <button
          class="inline-flex items-center justify-center gap-1.5 px-3 py-2 font-mono text-xs font-semibold border rounded-lg transition-colors"
          style={{
            'background-color': 'transparent',
            'border-color': 'transparent',
            color: 'var(--text-secondary)'
          }}
          onClick={props.onClose}
        >
          Cancel
        </button>
        <button
          class="inline-flex items-center justify-center gap-1.5 px-3 py-2 font-mono text-xs font-semibold border rounded-lg transition-colors disabled:opacity-50"
          style={{
            'background-color': 'var(--accent-500)',
            'border-color': 'var(--accent-500)',
            color: 'var(--bg-primary)'
          }}
          onClick={handleSave}
          disabled={saving()}
        >
          {saving() ? 'Creating…' : 'Create workspace'}
        </button>
      </div>
    </Modal>
  )
}

export default WorkspaceCreateModal
