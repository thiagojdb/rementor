import { Component, createSignal, Show } from 'solid-js'
import { routeModeLabel, type ApplicationDTO, type WorkspaceDTO } from '../../api/types'
import StatusDot from '../ui/StatusDot'
import { toggleApplication } from '../../stores/workspaces'
import { toast } from '../../stores/toast'

interface AppTableRowProps {
  workspace: WorkspaceDTO
  app: ApplicationDTO
  onOpenDetail: (app: ApplicationDTO) => void
}

const AppTableRow: Component<AppTableRowProps> = (props) => {
  const [toggling, setToggling] = createSignal(false)

  const isLocalApps = () => props.workspace.type === 'local-apps'
  const desiredRoute = () => routeModeLabel(props.app.route?.desiredMode)
  const effectiveRoute = () => routeModeLabel(props.app.route?.effectiveMode)

  const handleToggle = async (e: MouseEvent) => {
    e.stopPropagation()
    if (toggling()) return
    setToggling(true)
    try {
      await toggleApplication(props.workspace.id, props.app.id)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Toggle failed')
    } finally {
      setToggling(false)
    }
  }

  return (
    <tr
      class="last:border-b-0 cursor-pointer transition-colors"
      style={{
        'border-bottom': '1px solid var(--border-subtle)'
      }}
      onClick={() => props.onOpenDetail(props.app)}
    >
      <td class="py-2.5 px-4">
        <div class="flex items-center gap-2">
          <StatusDot status={props.app.healthStatus} title={`Local: ${props.app.healthStatus}`} />
          <span class="font-mono font-semibold text-sm" style={{ color: 'var(--text-primary)' }}>{props.app.name}</span>
        </div>
      </td>
      <td class="py-2.5 px-4">
        <div class="flex flex-col gap-0.5">
          <span class="font-mono text-sm" style={{ color: 'var(--accent-400)' }}>
            {isLocalApps() ? (props.app.domain ?? props.app.path) : props.app.path}
          </span>
          <Show when={!isLocalApps() && props.app.domain}>
            <span class="font-mono text-[10px]" style={{ color: 'var(--text-tertiary)' }}>
              {props.app.domain}
            </span>
          </Show>
        </div>
      </td>
      <td class="py-2.5 px-4">
        {props.app.port > 0 ? (
          <span
            class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide"
            style={{
              'border': '1px solid var(--border-subtle)',
              color: 'var(--text-secondary)'
            }}
          >
            :{props.app.port}
          </span>
        ) : (
          <span style={{ color: 'var(--text-tertiary)' }}>—</span>
        )}
      </td>
      <Show when={!isLocalApps()}>
        <td class="py-2.5 px-4">
          <div class="flex items-center gap-1.5">
            <StatusDot status={props.app.remoteStatus ?? 'unknown'} title={`Remote: ${props.app.remoteStatus ?? 'unknown'}`} />
            <span class="text-[11px]" style={{ color: 'var(--text-tertiary)' }}>remote</span>
          </div>
        </td>
      </Show>
      <td class="py-2.5 px-4">
        {desiredRoute() === 'local' ? (
          <span
            class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide"
            style={{
              'background-color': 'var(--accent-subtle)',
              color: 'var(--accent-400)'
            }}
          >
            {desiredRoute()} → {effectiveRoute()}
          </span>
        ) : (
          <span
            class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide"
            style={{
              'background-color': 'var(--bg-tertiary)',
              color: 'var(--text-tertiary)'
            }}
          >
            {desiredRoute()} → {effectiveRoute()}
          </span>
        )}
        <span class="ml-1 font-mono text-[10px]" style={{ color: 'var(--text-tertiary)' }} title="Proxy health">
          {props.app.route?.proxyHealth || 'unknown'}
        </span>
      </td>
      <Show when={!isLocalApps()}>
        <td class="py-2.5 px-4" onClick={(e) => e.stopPropagation()}>
          <button
            class={`relative inline-flex h-6 w-11 items-center rounded-full border transition-colors ${
              props.app.active ? 'switch-checked' : 'switch-unchecked'
            } ${!props.app.port ? 'opacity-35 cursor-not-allowed' : ''}`}
            style={props.app.active
              ? {
                  'background-color': 'var(--accent-500)',
                  'border-color': 'var(--accent-500)'
                }
              : {
                  'background-color': 'var(--bg-tertiary)',
                  'border-color': 'var(--border-default)'
                }
            }
            disabled={!props.app.port || toggling()}
            onClick={handleToggle}
            title={props.app.active ? 'Switch to remote' : 'Switch to local'}
          >
            <span class="switch-thumb" />
          </button>
        </td>
      </Show>
    </tr>
  )
}

export default AppTableRow
