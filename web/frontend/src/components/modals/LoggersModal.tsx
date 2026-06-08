import { Component, createResource, createSignal, For, Show } from 'solid-js'
import type { ApplicationDTO, LoggerDTO, WorkspaceDTO } from '../../api/types'
import Modal from '../ui/Modal'
import { getLoggers, setLoggerLevel } from '../../api/client'
import { toast } from '../../stores/toast'

interface LoggersModalProps {
  workspace: WorkspaceDTO
  app: ApplicationDTO
  onClose: () => void
}

const LoggersModal: Component<LoggersModalProps> = (props) => {
  const [filter, setFilter] = createSignal('')
  const [updating, setUpdating] = createSignal<string | null>(null)

  const [loggersData, { refetch }] = createResource(
    () => ({ wsId: props.workspace.id, appId: props.app.id }),
    ({ wsId, appId }) => getLoggers(wsId, appId)
  )

  const filteredLoggers = () => {
    const data = loggersData()
    if (!data) return []
    const f = filter().toLowerCase()
    return data.loggers.filter((l) => !f || l.name.toLowerCase().includes(f))
  }

  const handleSetLevel = async (logger: LoggerDTO, level: string) => {
    if (updating()) return
    setUpdating(logger.name)
    try {
      await setLoggerLevel(props.workspace.id, props.app.id, logger.name, { level })
      toast.success(`Logger '${logger.name}' set to ${level}`)
      refetch()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to set level')
    } finally {
      setUpdating(null)
    }
  }

  const inputStyle = {
    'background-color': 'var(--bg-primary)',
    'border-color': 'var(--border-default)',
    color: 'var(--text-primary)',
  }

  return (
    <Modal open={true} onClose={props.onClose} title={`Loggers — ${props.app.name}`}>
      <Show when={loggersData.loading}>
        <div class="py-12 text-center" style={{ color: 'var(--text-tertiary)' }}>
          <div class="inline-flex items-center gap-2">
            <svg class="animate-spin w-4 h-4" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
            </svg>
            Loading loggers...
          </div>
        </div>
      </Show>

      <Show when={loggersData.error}>
        <div
          class="p-4 rounded-lg text-sm"
          style={{
            'background-color': 'var(--error-subtle)',
            'border': '1px solid var(--error)',
            color: 'var(--error)'
          }}
        >
          Failed to load loggers: {(loggersData.error as Error).message}
        </div>
      </Show>

      <Show when={loggersData()}>
        <div class="mb-3">
          <input
            class="w-full h-9 px-3 rounded-lg border text-sm placeholder:text-tertiary focus:outline-none transition-colors"
            style={inputStyle}
            type="text"
            placeholder="Filter loggers..."
            value={filter()}
            onInput={(e) => setFilter(e.currentTarget.value)}
          />
        </div>

        <div class="max-h-[400px] overflow-auto">
          <table class="w-full">
            <thead class="sticky top-0">
              <tr style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
                <th class="text-left py-2 px-3 font-mono font-semibold text-[10px] uppercase tracking-wider" style={{ color: 'var(--text-tertiary)', 'background-color': 'var(--bg-secondary)' }}>Logger</th>
                <th class="text-left py-2 px-3 font-mono font-semibold text-[10px] uppercase tracking-wider" style={{ color: 'var(--text-tertiary)', 'background-color': 'var(--bg-secondary)' }}>Effective</th>
                <th class="text-left py-2 px-3 font-mono font-semibold text-[10px] uppercase tracking-wider" style={{ color: 'var(--text-tertiary)', 'background-color': 'var(--bg-secondary)' }}>Configured</th>
                <th class="text-left py-2 px-3 font-mono font-semibold text-[10px] uppercase tracking-wider" style={{ color: 'var(--text-tertiary)', 'background-color': 'var(--bg-secondary)' }}>Set Level</th>
              </tr>
            </thead>
            <tbody>
              <For each={filteredLoggers()}>
                {(logger) => (
                  <tr style={{ 'border-bottom': '1px solid var(--border-subtle)' }}>
                    <td class="py-2 px-3 font-mono text-xs max-w-[260px] truncate" style={{ color: 'var(--text-primary)' }}>
                      {logger.name}
                    </td>
                    <td class="py-2 px-3">
                      <span
                        class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-mono text-[10px]"
                        style={{
                          'background-color': 'var(--bg-tertiary)',
                          color: 'var(--text-secondary)'
                        }}
                      >
                        {logger.effectiveLevel}
                      </span>
                    </td>
                    <td class="py-2 px-3">
                      <span
                        class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-mono text-[10px]"
                        style={{
                          'border': '1px solid var(--border-default)',
                          color: 'var(--text-tertiary)'
                        }}
                      >
                        {logger.configuredLevel || '—'}
                      </span>
                    </td>
                    <td class="py-2 px-3">
                      <select
                        class="h-7 px-2 rounded border text-xs focus:outline-none transition-colors"
                        style={inputStyle}
                        value={logger.configuredLevel || ''}
                        disabled={updating() === logger.name}
                        onChange={(e) => handleSetLevel(logger, e.currentTarget.value)}
                      >
                        <option value="">—</option>
                        <For each={loggersData()?.levels ?? []}>
                          {(level) => <option value={level}>{level}</option>}
                        </For>
                      </select>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </Show>
    </Modal>
  )
}

export default LoggersModal
