import { Component, createMemo, createSignal, For, onCleanup, Show } from 'solid-js'
import type { WorkspaceDTO } from '../../api/types'

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
  const [open, setOpen] = createSignal(false)
  const [focusId, setFocusId] = createSignal<string | null>(null)
  let containerRef!: HTMLDivElement

  const activeWs = createMemo(() =>
    props.workspaces.find(w => w.id === props.activeId) ?? props.workspaces[0]
  )

  const onOutside = (e: MouseEvent) => {
    if (!containerRef?.contains(e.target as Node)) close()
  }

  const onKey = (e: KeyboardEvent) => {
    if (!open()) return
    const ids = props.workspaces.map(w => w.id)
    const cur = focusId() ?? props.activeId
    const i = ids.indexOf(cur)
    if (e.key === 'Escape')    { e.preventDefault(); close() }
    if (e.key === 'ArrowDown') { e.preventDefault(); setFocusId(ids[Math.min(i + 1, ids.length - 1)]) }
    if (e.key === 'ArrowUp')   { e.preventDefault(); setFocusId(ids[Math.max(i - 1, 0)]) }
    if (e.key === 'Enter') {
      const f = focusId()
      if (f) { props.onChange(f); close() }
    }
  }

  function openMenu() {
    setFocusId(props.activeId)
    setOpen(true)
    document.addEventListener('mousedown', onOutside)
    document.addEventListener('keydown', onKey)
  }

  function close() {
    setOpen(false)
    document.removeEventListener('mousedown', onOutside)
    document.removeEventListener('keydown', onKey)
  }

  onCleanup(() => {
    document.removeEventListener('mousedown', onOutside)
    document.removeEventListener('keydown', onKey)
  })

  const select = (id: string) => { props.onChange(id); close() }

  const stats = (ws: WorkspaceDTO) => ({
    active: ws.applications.filter(a => a.active).length,
    total:  ws.applications.length,
  })

  // Derived from the active workspace for the trigger
  const triggerStats = createMemo(() => {
    const ws = activeWs()
    if (!ws) return null
    return stats(ws)
  })

  return (
    <div ref={containerRef!} style={{ position: 'relative', display: 'inline-flex' }}>

      {/* ── Trigger ── */}
      <button
        onClick={() => open() ? close() : openMenu()}
        aria-haspopup="listbox"
        aria-expanded={open()}
        style={{
          display: 'inline-flex',
          'align-items': 'center',
          gap: '0',
          height: '34px',
          border: `1px solid ${open() ? 'var(--border-focus)' : 'var(--border-default)'}`,
          'border-radius': '8px',
          'background-color': open() ? 'var(--bg-hover)' : 'var(--bg-tertiary)',
          cursor: 'pointer',
          'min-width': '172px',
          'max-width': '260px',
          overflow: 'hidden',
          transition: 'border-color 0.15s ease, background-color 0.15s ease',
        }}
      >
        {/* Workspace color bar */}
        <span style={{
          width: '3px',
          height: '100%',
          background: hex(activeWs()?.color),
          'flex-shrink': '0',
          transition: 'background 0.25s ease',
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
          }}>
            {activeWs()?.name ?? activeWs()?.id ?? 'Select workspace'}
          </span>

          <Show when={triggerStats() && triggerStats()!.total > 0}>
            <span style={{
              'font-family': 'var(--font-mono)',
              'font-size': '10.5px',
              color: 'var(--text-tertiary)',
              'flex-shrink': '0',
            }}>
              {triggerStats()!.active}/{triggerStats()!.total}
            </span>
          </Show>

          {/* Chevron */}
          <svg
            width="12" height="12" viewBox="0 0 24 24"
            fill="none" stroke="currentColor" stroke-width="2.5"
            stroke-linecap="round" stroke-linejoin="round"
            style={{
              color: 'var(--text-tertiary)',
              'flex-shrink': '0',
              transition: 'transform 0.18s ease',
              transform: open() ? 'rotate(180deg)' : 'rotate(0deg)',
            }}
          >
            <path d="M6 9l6 6 6-6" />
          </svg>
        </span>
      </button>

      {/* ── Dropdown ── */}
      <Show when={open()}>
        <div
          role="listbox"
          style={{
            position: 'absolute',
            top: 'calc(100% + 5px)',
            left: '0',
            'z-index': '200',
            'min-width': '220px',
            'max-height': '320px',
            'overflow-y': 'auto',
            background: 'var(--bg-secondary)',
            border: '1px solid var(--border-default)',
            'border-radius': '10px',
            'box-shadow': '0 8px 24px rgba(0,0,0,0.28), 0 2px 6px rgba(0,0,0,0.16)',
            animation: 'ws-appear 0.13s ease forwards',
          }}
        >
          <div style={{ padding: '4px 0' }}>
            <For each={props.workspaces}>
              {(ws) => {
                const isActive    = () => ws.id === props.activeId
                const isFocused   = () => ws.id === focusId()
                const s           = () => stats(ws)

                const rowBg = () => {
                  if (isFocused() && isActive()) return 'var(--accent-subtle)'
                  if (isFocused())               return 'var(--bg-hover)'
                  if (isActive())                return 'rgba(var(--accent-rgb), 0.05)'
                  return 'transparent'
                }

                const barColor = () =>
                  isActive() || isFocused() ? hex(ws.color) : 'transparent'

                return (
                  <button
                    role="option"
                    aria-selected={isActive()}
                    onClick={() => select(ws.id)}
                    onMouseEnter={() => setFocusId(ws.id)}
                    style={{
                      display: 'flex',
                      'align-items': 'center',
                      gap: '0',
                      width: '100%',
                      height: '36px',
                      border: 'none',
                      cursor: 'pointer',
                      'background-color': rowBg(),
                      transition: 'background-color 0.1s ease',
                    }}
                  >
                    {/* Color accent bar */}
                    <span style={{
                      width: '3px',
                      height: '100%',
                      background: barColor(),
                      'flex-shrink': '0',
                      transition: 'background 0.12s ease',
                    }} />

                    <span style={{ padding: '0 10px 0 9px', display: 'flex', 'align-items': 'center', gap: '8px', flex: '1', overflow: 'hidden' }}>
                      {/* Color dot */}
                      <span style={{
                        width: '7px',
                        height: '7px',
                        'border-radius': '50%',
                        background: hex(ws.color),
                        'flex-shrink': '0',
                        opacity: '0.9',
                      }} />

                      {/* Name */}
                      <span style={{
                        flex: '1',
                        'font-family': 'var(--font-mono)',
                        'font-size': '12.5px',
                        'font-weight': isActive() ? '500' : '400',
                        color: isActive() ? 'var(--text-primary)' : 'var(--text-secondary)',
                        'white-space': 'nowrap',
                        overflow: 'hidden',
                        'text-overflow': 'ellipsis',
                        transition: 'color 0.1s ease',
                      }}>
                        {ws.name ?? ws.id}
                      </span>

                      {/* Stats */}
                      <span style={{
                        'font-family': 'var(--font-mono)',
                        'font-size': '10.5px',
                        color: 'var(--text-tertiary)',
                        'flex-shrink': '0',
                      }}>{s().active}/{s().total}</span>

                      {/* Check or spacer */}
                      <Show
                        when={isActive()}
                        fallback={<span style={{ width: '13px', 'flex-shrink': '0' }} />}
                      >
                        <svg
                          width="13" height="13" viewBox="0 0 24 24"
                          fill="none" stroke="currentColor" stroke-width="2.5"
                          stroke-linecap="round" stroke-linejoin="round"
                          style={{ color: 'var(--accent-500)', 'flex-shrink': '0' }}
                        >
                          <path d="M20 6L9 17l-5-5" />
                        </svg>
                      </Show>
                    </span>
                  </button>
                )
              }}
            </For>
          </div>
        </div>
      </Show>

      <style>{`
        @keyframes ws-appear {
          from { opacity: 0; transform: translateY(-5px) scale(0.985); }
          to   { opacity: 1; transform: translateY(0)    scale(1);     }
        }
      `}</style>
    </div>
  )
}

export default WorkspaceSwitcher
