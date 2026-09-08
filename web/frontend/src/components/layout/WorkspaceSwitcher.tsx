import { Component, createMemo, Show } from 'solid-js'
import type { WorkspaceDTO } from '../../api/types'
import Combobox from '../ui/Combobox'

const COLOR_HEX: Record<string, string> = {
  'bg-cyan-500':    '#06b6d4',
  'bg-emerald-500': '#10b981',
  'bg-violet-500':  '#8b5cf6',
  'bg-rose-500':    '#f43f5e',
  'bg-amber-500':   '#f59e0b',
  'bg-pink-500':    '#ec4899',
  'bg-indigo-500':  '#6366f1',
  'bg-teal-500':    '#14b8a6',
  'bg-blue-500':    '#3b82f6',
}

const hex = (cls: string = '') => COLOR_HEX[cls] ?? 'var(--accent-500)'

interface Props {
  workspaces: WorkspaceDTO[]
  activeId: string
  onChange: (id: string) => void
}

const WorkspaceSwitcher: Component<Props> = (props) => {
  const activeWs = createMemo(() =>
    props.workspaces.find(w => w.id === props.activeId) ?? props.workspaces[0]
  )

  const stats = (ws: WorkspaceDTO) => ({
    active: ws.applications.filter(a => a.active).length,
    total:  ws.applications.length,
  })

  return (
    <Combobox
      value={props.activeId}
      options={props.workspaces.map((workspace) => ({
        value: workspace.id,
        label: workspace.name || workspace.id,
        searchText: `${workspace.name} ${workspace.id}`,
      }))}
      onChange={props.onChange}
      placeholder="Select workspace"
      searchPlaceholder="Search workspaces..."
      emptyText="No matching workspace."
      ariaLabel="Select workspace"
      minWidth="172px"
      maxWidth="260px"
      contentMinWidth="260px"
      optionHeight="36px"
      optionPadding="0"
      renderValue={(option) => {
        const workspace = props.workspaces.find(item => item.id === option?.value) ?? activeWs()
        const workspaceStats = workspace ? stats(workspace) : null

        return (
          <>
            <span style={{
              width: '3px',
              height: '100%',
              background: hex(workspace?.color),
              'flex-shrink': '0',
            }} />
            <span style={{ padding: '0 9px', display: 'flex', 'align-items': 'center', gap: '7px', flex: '1', overflow: 'hidden' }}>
              <span style={{
                'font-family': 'var(--font-mono)',
                'font-size': '12.5px',
                'font-weight': '500',
                color: 'var(--text-primary)',
                flex: '1',
                'white-space': 'nowrap',
                overflow: 'hidden',
                'text-overflow': 'ellipsis',
                'text-align': 'left',
              }}>
                {workspace?.name ?? workspace?.id ?? 'Select workspace'}
              </span>

              <Show when={workspaceStats && workspaceStats.total > 0}>
                <span style={{
                  'font-family': 'var(--font-mono)',
                  'font-size': '10.5px',
                  color: 'var(--text-tertiary)',
                  'flex-shrink': '0',
                }}>
                  {workspaceStats!.active}/{workspaceStats!.total}
                </span>
              </Show>
            </span>
          </>
        )
      }}
      renderOption={(option, state) => {
        const workspace = props.workspaces.find(item => item.id === option.value)
        const workspaceStats = workspace ? stats(workspace) : { active: 0, total: 0 }
        const color = workspace ? hex(workspace.color) : 'var(--accent-500)'

        return (
          <>
            <span style={{
              width: '3px',
              height: '100%',
              background: state.isActive() || state.isFocused() ? color : 'transparent',
              'flex-shrink': '0',
            }} />
            <span style={{ padding: '0 10px 0 9px', display: 'flex', 'align-items': 'center', gap: '8px', flex: '1', overflow: 'hidden' }}>
              <span style={{
                width: '7px',
                height: '7px',
                'border-radius': '50%',
                background: color,
                'flex-shrink': '0',
                opacity: '0.9',
              }} />

              <span style={{
                flex: '1',
                'font-family': 'var(--font-mono)',
                'font-size': '12.5px',
                'font-weight': state.isActive() ? '500' : '400',
                color: state.isActive() ? 'var(--text-primary)' : 'var(--text-secondary)',
                'white-space': 'nowrap',
                overflow: 'hidden',
                'text-overflow': 'ellipsis',
                'text-align': 'left',
              }}>
                {workspace?.name ?? option.label}
              </span>

              <span style={{
                'font-family': 'var(--font-mono)',
                'font-size': '10.5px',
                color: 'var(--text-tertiary)',
                'flex-shrink': '0',
              }}>
                {workspaceStats.active}/{workspaceStats.total}
              </span>

              <Show
                when={state.isActive()}
                fallback={<span style={{ width: '13px', 'flex-shrink': '0' }} />}
              >
                <svg
                  width="13" height="13" viewBox="0 0 24 24"
                  fill="none" stroke="currentColor" stroke-width="2.5"
                  stroke-linecap="round" stroke-linejoin="round"
                  aria-hidden="true"
                  style={{ color: 'var(--accent-500)', 'flex-shrink': '0' }}
                >
                  <path d="M20 6L9 17l-5-5" />
                </svg>
              </Show>
            </span>
          </>
        )
      }}
    />
  )
}

export default WorkspaceSwitcher
