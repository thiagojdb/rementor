import { Component, createSignal } from 'solid-js'
import type { ApplicationDTO, WorkspaceDTO } from '../../api/types'
import Modal from '../ui/Modal'
import { updateRoutePattern } from '../../stores/workspaces'
import { toast } from '../../stores/toast'

interface RoutePatternFormProps {
  workspace: WorkspaceDTO
  app: ApplicationDTO
  onClose: () => void
}

const RoutePatternForm: Component<RoutePatternFormProps> = (props) => {
  const [pattern, setPattern] = createSignal(props.app.routePattern ?? '')
  const [saving, setSaving] = createSignal(false)

  const handleSave = async () => {
    if (saving()) return
    setSaving(true)
    try {
      await updateRoutePattern(props.workspace.id, props.app.id, pattern())
      toast.success('Route pattern updated')
      props.onClose()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update route pattern')
    } finally {
      setSaving(false)
    }
  }

  const sectionStyle = {
    'background-color': 'var(--bg-primary)',
    'border': '1px solid var(--border-subtle)',
  }

  const inputStyle = {
    'background-color': 'var(--bg-secondary)',
    'border-color': 'var(--border-default)',
    color: 'var(--text-primary)',
  }

  return (
    <Modal open={true} onClose={props.onClose} title={`Route Pattern — ${props.app.name}`}>
      <div class="rounded-lg p-4 mb-4" style={sectionStyle}>
        <div class="font-mono text-[11px] font-semibold uppercase tracking-wider mb-3 flex items-center gap-2" style={{ color: 'var(--text-tertiary)' }}>
          <span class="w-1 h-1 rounded-full" style={{ background: 'var(--accent-400)' }} />
          Custom Route Pattern
        </div>
        <p class="text-sm mb-3" style={{ color: 'var(--text-secondary)' }}>
          Override the default path routing. Leave empty to use the default path{' '}
          <code
            class="font-mono text-xs px-1.5 py-0.5 rounded"
            style={{
              'background-color': 'var(--bg-secondary)',
              color: 'var(--accent-400)'
            }}
          >
            {props.app.path}/*
          </code>.
        </p>
        <input
          class="w-full h-9 px-3 rounded-lg border text-sm placeholder:text-tertiary focus:outline-none transition-colors"
          style={inputStyle}
          type="text"
          placeholder={`${props.app.path}/*`}
          value={pattern()}
          onInput={(e) => setPattern(e.currentTarget.value)}
        />
      </div>

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
          {saving() ? 'Saving…' : 'Save'}
        </button>
      </div>
    </Modal>
  )
}

export default RoutePatternForm
