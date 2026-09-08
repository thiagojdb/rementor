import {
  Accessor,
  Component,
  createMemo,
  createSignal,
  createUniqueId,
  For,
  JSX,
  onCleanup,
  Show,
} from 'solid-js'

export interface ComboboxOption {
  value: string
  label: string
  searchText?: string
}

export interface ComboboxOptionState {
  isActive: Accessor<boolean>
  isFocused: Accessor<boolean>
}

interface ComboboxProps {
  value: string
  options: ComboboxOption[]
  onChange: (value: string) => void
  placeholder?: string
  searchPlaceholder?: string
  emptyText?: string
  ariaLabel?: string
  minWidth?: string
  maxWidth?: string
  contentMinWidth?: string
  optionHeight?: string
  optionPadding?: string
  renderValue?: (option: ComboboxOption | undefined) => JSX.Element
  renderOption?: (option: ComboboxOption, state: ComboboxOptionState) => JSX.Element
}

const Combobox: Component<ComboboxProps> = (props) => {
  const [open, setOpen] = createSignal(false)
  const [query, setQuery] = createSignal('')
  const [focusedValue, setFocusedValue] = createSignal<string | null>(null)
  const listId = `combobox-list-${createUniqueId()}`

  let containerRef!: HTMLDivElement
  let triggerRef!: HTMLButtonElement
  let searchRef!: HTMLInputElement

  const activeOption = createMemo(() =>
    props.options.find(option => option.value === props.value)
  )

  const filteredOptions = createMemo(() => {
    const normalizedQuery = query().trim().toLowerCase()
    if (!normalizedQuery) return props.options

    return props.options.filter(option =>
      (option.searchText ?? option.label).toLowerCase().includes(normalizedQuery)
    )
  })

  const focusedOption = createMemo(() => {
    const options = filteredOptions()
    return options.find(option => option.value === focusedValue()) ?? options[0]
  })

  const optionId = (value: string) =>
    `${listId}-option-${encodeURIComponent(value)}`

  const onOutside = (event: MouseEvent) => {
    if (!containerRef?.contains(event.target as Node)) close()
  }

  const close = (restoreFocus = false) => {
    if (!open()) return
    setOpen(false)
    setQuery('')
    setFocusedValue(null)
    document.removeEventListener('mousedown', onOutside)
    if (restoreFocus) queueMicrotask(() => triggerRef?.focus())
  }

  const openMenu = () => {
    if (open()) return
    setQuery('')
    setFocusedValue(props.value || props.options[0]?.value || null)
    setOpen(true)
    document.addEventListener('mousedown', onOutside)
    queueMicrotask(() => searchRef?.focus())
  }

  const select = (value: string) => {
    props.onChange(value)
    close(true)
  }

  const moveFocus = (direction: 1 | -1) => {
    const options = filteredOptions()
    if (options.length === 0) return

    const currentIndex = options.findIndex(option => option.value === focusedOption()?.value)
    const nextIndex = currentIndex === -1
      ? 0
      : Math.max(0, Math.min(currentIndex + direction, options.length - 1))
    setFocusedValue(options[nextIndex].value)
  }

  const onTriggerKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Enter' || event.key === ' ' || event.key === 'ArrowDown') {
      event.preventDefault()
      if (open()) moveFocus(1)
      else openMenu()
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      if (open()) moveFocus(-1)
      else openMenu()
    } else if (event.key === 'Escape' && open()) {
      event.preventDefault()
      close(true)
    }
  }

  const onSearchKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      moveFocus(1)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      moveFocus(-1)
    } else if (event.key === 'Home') {
      event.preventDefault()
      setFocusedValue(filteredOptions()[0]?.value ?? null)
    } else if (event.key === 'End') {
      event.preventDefault()
      const options = filteredOptions()
      setFocusedValue(options[options.length - 1]?.value ?? null)
    } else if (event.key === 'Enter') {
      event.preventDefault()
      const option = focusedOption()
      if (option) select(option.value)
    } else if (event.key === 'Escape') {
      event.preventDefault()
      close(true)
    }
  }

  const onSearchInput = (event: InputEvent & { currentTarget: HTMLInputElement }) => {
    setQuery(event.currentTarget.value)
    queueMicrotask(() => setFocusedValue(filteredOptions()[0]?.value ?? null))
  }

  onCleanup(() => {
    document.removeEventListener('mousedown', onOutside)
  })

  return (
    <div ref={containerRef!} style={{ position: 'relative', display: 'inline-flex' }}>
      <button
        ref={triggerRef!}
        type="button"
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open()}
        aria-controls={listId}
        aria-label={props.ariaLabel}
        onClick={() => open() ? close() : openMenu()}
        onKeyDown={onTriggerKeyDown}
        style={{
          display: 'inline-flex',
          'align-items': 'center',
          gap: '0',
          height: '34px',
          padding: '0',
          border: `1px solid ${open() ? 'var(--border-focus)' : 'var(--border-default)'}`,
          'border-radius': '8px',
          'background-color': open() ? 'var(--bg-hover)' : 'var(--bg-tertiary)',
          color: 'var(--text-primary)',
          cursor: 'pointer',
          'min-width': props.minWidth ?? '130px',
          'max-width': props.maxWidth,
          'white-space': 'nowrap',
          overflow: 'hidden',
          transition: 'border-color 0.15s ease, background-color 0.15s ease',
        }}
      >
        {props.renderValue
          ? props.renderValue(activeOption())
          : (
            <span style={{
              display: 'block',
              flex: '1',
              padding: '0 9px',
              'font-family': 'var(--font-mono)',
              'font-size': '12.5px',
              'font-weight': '500',
              color: activeOption() ? 'var(--text-primary)' : 'var(--text-tertiary)',
              'text-align': 'left',
              overflow: 'hidden',
              'text-overflow': 'ellipsis',
            }}>
              {activeOption()?.label ?? props.placeholder ?? 'Select an option'}
            </span>
          )}

        <svg
          width="12" height="12" viewBox="0 0 24 24"
          fill="none" stroke="currentColor" stroke-width="2.5"
          stroke-linecap="round" stroke-linejoin="round"
          aria-hidden="true"
          style={{
            margin: '0 9px 0 2px',
            color: 'var(--text-tertiary)',
            'flex-shrink': '0',
            transition: 'transform 0.18s ease',
            transform: open() ? 'rotate(180deg)' : 'rotate(0deg)',
          }}
        >
          <path d="M6 9l6 6 6-6" />
        </svg>
      </button>

      <Show when={open()}>
        <div
          style={{
            position: 'absolute',
            top: 'calc(100% + 5px)',
            left: '0',
            'z-index': '200',
            width: props.contentMinWidth ?? '100%',
            'min-width': props.contentMinWidth ?? '100%',
            'max-width': 'calc(100vw - 10px)',
            background: 'var(--bg-secondary)',
            border: '1px solid var(--border-default)',
            'border-radius': '10px',
            'box-shadow': '0 8px 24px rgba(0,0,0,0.28), 0 2px 6px rgba(0,0,0,0.16)',
            overflow: 'hidden',
            animation: 'combobox-appear 0.13s ease forwards',
          }}
        >
          <div style={{ padding: '5px' }}>
            <div style={{ position: 'relative' }}>
              <svg
                width="14" height="14" viewBox="0 0 24 24"
                fill="none" stroke="currentColor" stroke-width="2"
                stroke-linecap="round" stroke-linejoin="round"
                aria-hidden="true"
                style={{
                  position: 'absolute',
                  left: '9px',
                  top: '50%',
                  transform: 'translateY(-50%)',
                  color: 'var(--text-tertiary)',
                  'pointer-events': 'none',
                }}
              >
                <circle cx="11" cy="11" r="7" />
                <path d="m20 20-4-4" />
              </svg>
              <input
                ref={searchRef!}
                type="text"
                role="searchbox"
                aria-label={props.searchPlaceholder ?? 'Search options'}
                aria-controls={listId}
                aria-activedescendant={focusedOption() ? optionId(focusedOption()!.value) : undefined}
                autocomplete="off"
                placeholder={props.searchPlaceholder ?? 'Search options'}
                value={query()}
                onInput={onSearchInput}
                onKeyDown={onSearchKeyDown}
                style={{
                  width: '100%',
                  height: '30px',
                  padding: '0 9px 0 29px',
                  border: '1px solid var(--border-subtle)',
                  'border-radius': '6px',
                  background: 'var(--bg-tertiary)',
                  color: 'var(--text-primary)',
                  'font-family': 'var(--font-mono)',
                  'font-size': '12px',
                }}
              />
            </div>
          </div>

          <Show
            when={filteredOptions().length > 0}
            fallback={
              <div style={{
                padding: '12px 10px 13px',
                color: 'var(--text-tertiary)',
                'font-family': 'var(--font-mono)',
                'font-size': '11.5px',
                'text-align': 'center',
              }}>
                {props.emptyText ?? 'No results found.'}
              </div>
            }
          >
            <div
              id={listId}
              role="listbox"
              aria-label={props.ariaLabel ?? props.placeholder ?? 'Options'}
              style={{
                'max-height': '320px',
                'overflow-y': 'auto',
                padding: '0 5px 5px',
              }}
            >
              <For each={filteredOptions()}>
                {(option) => {
                  const isActive = () => option.value === props.value
                  const isFocused = () => option.value === focusedOption()?.value
                  const rowBackground = () => {
                    if (isFocused() && isActive()) return 'var(--accent-subtle)'
                    if (isFocused()) return 'var(--bg-hover)'
                    if (isActive()) return 'rgba(var(--accent-rgb), 0.05)'
                    return 'transparent'
                  }

                  return (
                    <button
                      id={optionId(option.value)}
                      type="button"
                      role="option"
                      aria-selected={isActive()}
                      tabIndex={-1}
                      onClick={() => select(option.value)}
                      onMouseEnter={() => setFocusedValue(option.value)}
                      style={{
                        display: 'flex',
                        'align-items': 'center',
                        gap: '8px',
                        width: '100%',
                        height: props.optionHeight ?? '34px',
                        padding: props.optionPadding ?? '0 9px',
                        border: 'none',
                        'border-radius': '5px',
                        cursor: 'pointer',
                        'background-color': rowBackground(),
                        transition: 'background-color 0.1s ease',
                      }}
                    >
                      {props.renderOption
                        ? props.renderOption(option, { isActive, isFocused })
                        : (
                          <>
                            <span style={{
                              flex: '1',
                              'font-family': 'var(--font-mono)',
                              'font-size': '12.5px',
                              'font-weight': isActive() ? '500' : '400',
                              color: isActive() ? 'var(--text-primary)' : 'var(--text-secondary)',
                              'white-space': 'nowrap',
                              'text-align': 'left',
                            }}>
                              {option.label}
                            </span>
                            <Show
                              when={isActive()}
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
                          </>
                        )}
                    </button>
                  )
                }}
              </For>
            </div>
          </Show>
        </div>
      </Show>

      <style>{`
        @keyframes combobox-appear {
          from { opacity: 0; transform: translateY(-5px) scale(0.985); }
          to   { opacity: 1; transform: translateY(0) scale(1); }
        }
      `}</style>
    </div>
  )
}

export default Combobox
