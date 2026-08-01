import { Component, createSignal, Show } from 'solid-js'
import type { ApplicationDTO, WorkspaceDTO } from '../../api/types'
import Modal from '../ui/Modal'
import StatusDot from '../ui/StatusDot'
import RoutePatternForm from './RoutePatternForm'

interface AppDetailModalProps {
  workspace: WorkspaceDTO
  app: ApplicationDTO
  onClose: () => void
}

const AppDetailModal: Component<AppDetailModalProps> = (props) => {
  const [showRoutePattern, setShowRoutePattern] = createSignal(false)

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
    <>
      <Modal open={true} onClose={props.onClose} title={props.app.name}>
        {/* Status section */}
        <div class="rounded-lg p-4 mb-4" style={sectionStyle}>
          <div class="font-mono text-[11px] font-semibold uppercase tracking-wider mb-3 flex items-center gap-2" style={{ color: 'var(--text-tertiary)' }}>
            <span class="w-1 h-1 rounded-full" style={{ background: 'var(--accent-400)' }} />
            Status
          </div>
          <div class="flex items-center justify-between py-2" style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
            <span class="text-sm" style={labelStyle}>Local health</span>
            <div class="flex items-center gap-2">
              <StatusDot status={props.app.healthStatus} />
              <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>{props.app.healthStatus}</span>
            </div>
          </div>
          <div class="flex items-center justify-between py-2" style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
            <span class="text-sm" style={labelStyle}>Remote health</span>
            <div class="flex items-center gap-2">
              <StatusDot status={props.app.remoteStatus ?? 'unknown'} />
              <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>{props.app.remoteStatus ?? 'unknown'}</span>
            </div>
          </div>
          <div class="flex items-center justify-between py-2">
            <span class="text-sm" style={labelStyle}>Routing</span>
            <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>{props.app.active ? 'local' : 'remote'}</span>
          </div>
        </div>

        {/* Details section */}
        <div class="rounded-lg p-4 mb-4" style={sectionStyle}>
          <div class="font-mono text-[11px] font-semibold uppercase tracking-wider mb-3 flex items-center gap-2" style={{ color: 'var(--text-tertiary)' }}>
            <span class="w-1 h-1 rounded-full" style={{ background: 'var(--accent-400)' }} />
            Details
          </div>
          <div class="flex items-center justify-between py-2" style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
            <span class="text-sm" style={labelStyle}>ID</span>
            <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>{props.app.id}</span>
          </div>
          <div class="flex items-center justify-between py-2" style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
            <span class="text-sm" style={labelStyle}>Path</span>
            <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>{props.app.path}</span>
          </div>
          {props.app.port > 0 && (
            <div class="flex items-center justify-between py-2" style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
              <span class="text-sm" style={labelStyle}>Local port</span>
              <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>:{props.app.port}</span>
            </div>
          )}
          {props.app.context && (
            <div class="flex items-center justify-between py-2" style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
              <span class="text-sm" style={labelStyle}>Context</span>
              <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>{props.app.context}</span>
            </div>
          )}
          {props.app.routePattern && (
            <div class="flex items-center justify-between py-2">
              <span class="text-sm" style={labelStyle}>Route pattern</span>
              <span class="font-mono px-2 py-0.5 rounded text-xs" style={valueStyle}>{props.app.routePattern}</span>
            </div>
          )}
        </div>

        {/* Actions section */}
        <div class="flex gap-2 flex-wrap">
          <Show when={props.app.port > 0}>
            <button
              class="inline-flex items-center justify-center gap-1.5 px-3 py-2 font-mono text-xs font-semibold border rounded-lg transition-colors"
              style={{
                'background-color': 'transparent',
                'border-color': 'var(--border-default)',
                color: 'var(--text-primary)'
              }}
              onClick={() => setShowRoutePattern(true)}
            >
              Route Pattern
            </button>
          </Show>
        </div>
      </Modal>

      <Show when={showRoutePattern()}>
        <RoutePatternForm
          workspace={props.workspace}
          app={props.app}
          onClose={() => setShowRoutePattern(false)}
        />
      </Show>
    </>
  )
}

export default AppDetailModal
