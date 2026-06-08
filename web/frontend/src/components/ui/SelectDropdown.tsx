import { Component, createSignal, For, onCleanup, Show } from 'solid-js'

export interface SelectOption {
  value: string
  label: string
}

interface Props {
  value: string
  options: SelectOption[]
  onChange: (value: string) => void
  minWidth?: string
}

const SelectDropdown: Component<Props> = (props) => {
  const [open, setOpen] = createSignal(false)
  const [focusIdx, setFocusIdx] = createSignal(-1)
  let containerRef!: HTMLDivElement

  const activeLabel = () =>
    props.options.find(o => o.value === props.value)?.label ?? props.options[0]?.label ?? ''

  const onOutside = (e: MouseEvent) => {
    if (!containerRef?.contains(e.target as Node)) close()
  }

  const onKey = (e: KeyboardEvent) => {
    if (!open()) return
    const last = props.options.length - 1
    if (e.key === 'Escape')    { e.preventDefault(); close() }
    if (e.key === 'ArrowDown') { e.preventDefault(); setFocusIdx(i => Math.min(i + 1, last)) }
    if (e.key === 'ArrowUp')   { e.preventDefault(); setFocusIdx(i => Math.max(i - 1, 0)) }
    if (e.key === 'Enter') {
      const idx = focusIdx()
      if (idx >= 0) { select(props.options[idx].value) }
    }
  }

  function openMenu() {
    setFocusIdx(props.options.findIndex(o => o.value === props.value))
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

  const select = (value: string) => { props.onChange(value); close() }

  return (
    <div ref={containerRef!} style={{ position: 'relative', display: 'inline-flex' }}>

      {/* Trigger */}
      <button
        onClick={() => open() ? close() : openMenu()}
        aria-haspopup="listbox"
        aria-expanded={open()}
        style={{
          display: 'inline-flex',
          'align-items': 'center',
          gap: '7px',
          height: '34px',
          padding: '0 9px',
          border: `1px solid ${open() ? 'var(--border-focus)' : 'var(--border-default)'}`,
          'border-radius': '8px',
          'background-color': open() ? 'var(--bg-hover)' : 'var(--bg-tertiary)',
          cursor: 'pointer',
          'min-width': props.minWidth ?? '130px',
          'white-space': 'nowrap',
          transition: 'border-color 0.15s ease, background-color 0.15s ease',
        }}
      >
        <span style={{
          'font-family': 'var(--font-mono)',
          'font-size': '12.5px',
          'font-weight': '500',
          color: 'var(--text-primary)',
          flex: '1',
          'text-align': 'left',
        }}>
          {activeLabel()}
        </span>

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
      </button>

      {/* Dropdown */}
      <Show when={open()}>
        <div
          role="listbox"
          style={{
            position: 'absolute',
            top: 'calc(100% + 5px)',
            left: '0',
            'z-index': '200',
            'min-width': '100%',
            background: 'var(--bg-secondary)',
            border: '1px solid var(--border-default)',
            'border-radius': '10px',
            'box-shadow': '0 8px 24px rgba(0,0,0,0.28), 0 2px 6px rgba(0,0,0,0.16)',
            overflow: 'hidden',
            animation: 'ws-appear 0.13s ease forwards',
          }}
        >
          <div style={{ padding: '4px 0' }}>
            <For each={props.options}>
              {(opt, i) => {
                const isActive   = () => opt.value === props.value
                const isFocused  = () => i() === focusIdx()

                const rowBg = () => {
                  if (isFocused() && isActive()) return 'var(--accent-subtle)'
                  if (isFocused())               return 'var(--bg-hover)'
                  if (isActive())                return 'rgba(var(--accent-rgb), 0.05)'
                  return 'transparent'
                }

                return (
                  <button
                    role="option"
                    aria-selected={isActive()}
                    onClick={() => select(opt.value)}
                    onMouseEnter={() => setFocusIdx(i())}
                    style={{
                      display: 'flex',
                      'align-items': 'center',
                      gap: '8px',
                      width: '100%',
                      height: '34px',
                      padding: '0 10px',
                      border: 'none',
                      cursor: 'pointer',
                      'background-color': rowBg(),
                      transition: 'background-color 0.1s ease',
                    }}
                  >
                    <span style={{
                      flex: '1',
                      'font-family': 'var(--font-mono)',
                      'font-size': '12.5px',
                      'font-weight': isActive() ? '500' : '400',
                      color: isActive() ? 'var(--text-primary)' : 'var(--text-secondary)',
                      'white-space': 'nowrap',
                      'text-align': 'left',
                      transition: 'color 0.1s ease',
                    }}>
                      {opt.label}
                    </span>

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
                  </button>
                )
              }}
            </For>
          </div>
        </div>
      </Show>
    </div>
  )
}

export default SelectDropdown
