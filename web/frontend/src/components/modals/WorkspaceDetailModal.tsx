import { Component, createSignal, Show } from 'solid-js'
import type { ApplicationConfigInput, WorkspaceDTO } from '../../api/types'
import Modal from '../ui/Modal'
import ApplicationManager from '../ApplicationManager'
import { updateWorkspaceApplications } from '../../stores/workspaces'
import { toast } from '../../stores/toast'

interface WorkspaceDetailModalProps {
  workspace: WorkspaceDTO
  onClose: () => void
}

const WorkspaceDetailModal: Component<WorkspaceDetailModalProps> = (props) => {
  const isLocalApps = () => props.workspace.type === 'local-apps'

  const [apps, setApps] = createSignal<ApplicationConfigInput[]>(
    props.workspace.applications.map((a) => ({
      id: a.id,
      name: a.name,
      path: a.path,
      domain: a.domain,
      remoteBaseUrl: a.remoteBaseUrl,
      port: a.port,
      health: a.health ?? 'actuator/health',
      context: a.context,
    }))
  )
  const [localDomain, setLocalDomain] = createSignal(props.workspace.routing?.localDomain ?? '')
  const [defaultRemoteBaseUrl, setDefaultRemoteBaseURL] = createSignal(props.workspace.routing?.defaultRemoteBaseUrl ?? '')
  const [saving, setSaving] = createSignal(false)

  const handleAppsChange = (newApps: ApplicationConfigInput[]) => {
    setApps(newApps)
  }

  const handleSave = async () => {
    if (saving()) return
    setSaving(true)
    try {
      await updateWorkspaceApplications(props.workspace.id, {
        applications: apps(),
        localDomain: localDomain(),
        defaultRemoteBaseUrl: defaultRemoteBaseUrl(),
      })
      toast.success('Workspace updated')
      props.onClose()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update workspace')
    } finally {
      setSaving(false)
    }
  }

  const sectionStyle = {
    'background-color': 'var(--bg-primary)',
    'border': '1px solid var(--border-subtle)',
  }

  const labelStyle = {
    color: 'var(--text-secondary)',
  }

  const valueStyle = {
    color: 'var(--accent-400)',
    'background-color': 'var(--accent-subtle)',
  }

  return (
    <Modal open={true} onClose={props.onClose} title={`Edit — ${props.workspace.name}`}>
      {/* Workspace Info Section */}
      <div class="rounded-lg p-4 mb-4" style={sectionStyle}>
        <div class="font-mono text-[11px] font-semibold uppercase tracking-wider mb-3 flex items-center gap-2" style={{ color: 'var(--text-tertiary)' }}>
          <span class="w-1 h-1 rounded-full" style={{ background: 'var(--accent-400)' }} />
          Workspace info
        </div>
        <div class="flex items-center justify-between py-2" style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
          <span class="text-sm" style={labelStyle}>ID</span>
          <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>{props.workspace.id}</span>
        </div>
        <div class="flex items-center justify-between py-2" style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
          <span class="text-sm" style={labelStyle}>Type</span>
          <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>{props.workspace.type}</span>
        </div>
        <Show when={!isLocalApps() && props.workspace.routing}>
          <div class="flex flex-col gap-1 py-2" style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
            <label class="text-xs" style={{ color: 'var(--text-secondary)' }}>Local domain</label>
            <input
              class="w-full h-8 px-2.5 rounded border text-sm font-mono focus:outline-none transition-colors"
              style={{
                'background-color': 'var(--bg-secondary)',
                'border-color': 'var(--border-default)',
                color: 'var(--text-primary)',
              }}
              type="text"
              value={localDomain()}
              onInput={(e) => setLocalDomain(e.currentTarget.value)}
            />
          </div>
          <div class="flex flex-col gap-1 py-2">
            <label class="text-xs" style={{ color: 'var(--text-secondary)' }}>Default remote base</label>
            <input
              class="w-full h-8 px-2.5 rounded border text-sm font-mono focus:outline-none transition-colors"
              style={{
                'background-color': 'var(--bg-secondary)',
                'border-color': 'var(--border-default)',
                color: 'var(--text-primary)',
              }}
              type="text"
              value={defaultRemoteBaseUrl()}
              onInput={(e) => setDefaultRemoteBaseURL(e.currentTarget.value)}
            />
          </div>
        </Show>
      </div>

      {/* Applications Section with new ApplicationManager */}
      <div class="rounded-lg p-4 mb-4" style={sectionStyle}>
        <ApplicationManager
          workspaceType={props.workspace.type}
          initialApps={props.workspace.applications.map((a) => ({
            id: a.id,
            name: a.name,
            path: a.path,
            domain: a.domain,
            remoteBaseUrl: a.remoteBaseUrl,
            port: a.port,
            health: a.health ?? 'actuator/health',
            context: a.context,
          }))}
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
          {saving() ? 'Saving…' : 'Save changes'}
        </button>
      </div>
    </Modal>
  )
}

export default WorkspaceDetailModal
