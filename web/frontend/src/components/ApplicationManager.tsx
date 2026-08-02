import { Component, createSignal, For, Show, createMemo } from 'solid-js'
import { createStore, produce } from 'solid-js/store'
import type { ApplicationConfigInput, WorkspaceType } from '../api/types'
import { toast } from '../stores/toast'

interface ApplicationManagerProps {
  workspaceType: WorkspaceType
  initialApps: ApplicationConfigInput[]
  onChange: (apps: ApplicationConfigInput[]) => void
  readOnly?: boolean
}

interface ValidationErrors {
  [key: string]: string
}

const ApplicationManager: Component<ApplicationManagerProps> = (props) => {
  const [apps, setApps] = createStore<ApplicationConfigInput[]>(props.initialApps.length > 0 ? [...props.initialApps] : [])
  const [expandedIndex, setExpandedIndex] = createSignal<number | null>(null)
  const [errors, setErrors] = createSignal<ValidationErrors>({})
  const [confirmDelete, setConfirmDelete] = createSignal<number | null>(null)

  const isLocalApps = createMemo(() => props.workspaceType === 'local-apps')

  const generateId = () => `app-${Date.now()}`

  const addApp = () => {
    const newApp: ApplicationConfigInput = {
      id: '',
      name: '',
      path: isLocalApps() ? '' : '/',
      publicPath: isLocalApps() ? '' : '/',
      domain: isLocalApps() ? '' : undefined,
      remoteBaseUrl: '',
      port: 0,
      health: 'actuator/health',
      context: '',
      upstreamContext: '',
      frontendRoot: '',
      frontendRootSource: '',
    }
    setApps(produce((draft) => draft.push(newApp)))
    setExpandedIndex(apps.length)
    props.onChange([...apps])
  }

  const removeApp = (idx: number) => {
    if (confirmDelete() === idx) {
      setApps(produce((draft) => draft.splice(idx, 1)))
      setConfirmDelete(null)
      setExpandedIndex(null)
      props.onChange([...apps])
      toast.success('Application removed')
    } else {
      setConfirmDelete(idx)
      setTimeout(() => setConfirmDelete(null), 3000)
    }
  }

  const updateApp = (idx: number, field: keyof ApplicationConfigInput, value: string | number) => {
    setApps(produce((draft) => {
      ;(draft[idx] as any)[field] = value
      // Auto-generate ID from name if ID is empty
      if (field === 'name' && !draft[idx].id && value) {
        draft[idx].id = (value as string).toLowerCase().replace(/[^a-z0-9]+/g, '-')
      }
    }))
    props.onChange([...apps])

    // Clear error for this field
    if (errors()[`${idx}-${field}`]) {
      setErrors((prev) => {
        const next = { ...prev }
        delete next[`${idx}-${field}`]
        return next
      })
    }
  }

  const validateApp = (app: ApplicationConfigInput, idx: number): boolean => {
    const newErrors: ValidationErrors = {}

    if (!app.id?.trim()) {
      newErrors[`${idx}-id`] = 'ID is required'
    } else if (!/^[a-z0-9-]+$/.test(app.id)) {
      newErrors[`${idx}-id`] = 'Only lowercase letters, numbers, and hyphens'
    }

    if (isLocalApps()) {
      if (!app.domain?.trim()) {
        newErrors[`${idx}-domain`] = 'Domain is required for local-apps'
      }
    } else {
      const publicPath = app.publicPath?.trim() || app.path?.trim()
      if (!publicPath) {
        newErrors[`${idx}-path`] = 'Path is required'
      } else if (!publicPath.startsWith('/')) {
        newErrors[`${idx}-path`] = 'Path must start with /'
      }
    }

    if (app.domain?.trim() && !isValidHostname(app.domain)) {
      newErrors[`${idx}-domain`] = 'Must be a valid hostname'
    }

    if (!app.port || app.port < 1 || app.port > 65535) {
      newErrors[`${idx}-port`] = 'Valid port (1-65535) required'
    }

    if (app.remoteBaseUrl && !isValidUrl(app.remoteBaseUrl)) {
      newErrors[`${idx}-remoteBaseUrl`] = 'Must be a valid URL'
    }

    setErrors((prev) => ({ ...prev, ...newErrors }))
    return Object.keys(newErrors).length === 0
  }

  const isValidUrl = (url: string): boolean => {
    try {
      new URL(url)
      return true
    } catch {
      return false
    }
  }

  const isValidHostname = (hostname: string): boolean => {
    return /^(?=.{1,253}$)(?!-)[a-z0-9-]+(\.[a-z0-9-]+)*\.localhost$/i.test(hostname.trim())
  }

  const validateAll = (): boolean => {
    let valid = true
    apps.forEach((app, idx) => {
      if (!validateApp(app, idx)) valid = false
    })
    return valid
  }

  const toggleExpand = (idx: number) => {
    setExpandedIndex(expandedIndex() === idx ? null : idx)
  }

  const duplicateApp = (idx: number) => {
    const app = apps[idx]
    const duplicated: ApplicationConfigInput = {
      ...app,
      id: `${app.id}-copy`,
      name: app.name ? `${app.name} (Copy)` : '',
    }
    setApps(produce((draft) => draft.push(duplicated)))
    setExpandedIndex(apps.length - 1)
    props.onChange([...apps])
    toast.success('Application duplicated')
  }

  // Expose validation method to parent
  const managerRef = {
    validateAll,
    getApps: () => [...apps],
  }

  // Styles
  const cardStyle = {
    'background-color': 'var(--bg-primary)',
    'border': '1px solid var(--border-subtle)',
  }

  const cardExpandedStyle = {
    'background-color': 'var(--bg-primary)',
    'border': '1px solid var(--accent-500)',
    'box-shadow': '0 0 0 1px var(--accent-500)',
  }

  const inputStyle = {
    'background-color': 'var(--bg-secondary)',
    'border-color': 'var(--border-default)',
    color: 'var(--text-primary)',
  }

  const inputErrorStyle = {
    'background-color': 'var(--bg-secondary)',
    'border-color': 'var(--error)',
    color: 'var(--text-primary)',
  }

  const labelStyle = {
    color: 'var(--text-secondary)',
  }

  const helpStyle = {
    color: 'var(--text-tertiary)',
  }

  const sectionHeaderStyle = {
    color: 'var(--accent-400)',
    'background-color': 'var(--accent-subtle)',
  }

  return (
    <div class="space-y-3">
      {/* Header with count */}
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <span
            class="font-mono text-xs font-semibold uppercase tracking-wider px-2 py-1 rounded"
            style={sectionHeaderStyle}
          >
            Applications
          </span>
          <span class="text-sm" style={{ color: 'var(--text-tertiary)' }}>
            {apps.length} configured
          </span>
        </div>
        <Show when={!props.readOnly}>
          <button
            onClick={addApp}
            class="inline-flex items-center gap-1.5 px-3 py-1.5 font-mono text-xs font-semibold border rounded-lg transition-all hover:scale-105 active:scale-95"
            style={{
              'background-color': 'var(--accent-500)',
              'border-color': 'var(--accent-500)',
              color: 'var(--bg-primary)',
            }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M12 5v14M5 12h14" />
            </svg>
            Add Application
          </button>
        </Show>
      </div>

      {/* Empty state */}
      <Show when={apps.length === 0}>
        <div
          class="rounded-lg border border-dashed p-8 text-center"
          style={{
            'border-color': 'var(--border-default)',
            'background-color': 'var(--bg-secondary)',
          }}
        >
          <div
            class="w-12 h-12 mx-auto mb-3 rounded-lg flex items-center justify-center"
            style={{ 'background-color': 'var(--accent-subtle)' }}
          >
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="var(--accent-400)" stroke-width="1.5">
              <rect x="3" y="3" width="7" height="7" rx="1" />
              <rect x="14" y="3" width="7" height="7" rx="1" />
              <rect x="3" y="14" width="7" height="7" rx="1" />
              <rect x="14" y="14" width="7" height="7" rx="1" />
            </svg>
          </div>
          <p class="text-sm font-medium mb-1" style={{ color: 'var(--text-primary)' }}>
            No applications configured
          </p>
          <p class="text-xs mb-4" style={{ color: 'var(--text-tertiary)' }}>
            Add your first application to start routing traffic
          </p>
          <Show when={!props.readOnly}>
            <button
              onClick={addApp}
              class="inline-flex items-center gap-1.5 px-4 py-2 font-mono text-xs font-semibold border rounded-lg transition-all"
              style={{
                'background-color': 'var(--accent-500)',
                'border-color': 'var(--accent-500)',
                color: 'var(--bg-primary)',
              }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M12 5v14M5 12h14" />
              </svg>
              Add Application
            </button>
          </Show>
        </div>
      </Show>

      {/* Application Cards */}
      <For each={apps}>
        {(app, idx) => (
          <div
            class="rounded-lg overflow-hidden transition-all duration-200"
            style={expandedIndex() === idx() ? cardExpandedStyle : cardStyle}
          >
            {/* Card Header - Always visible */}
            <div
              class="flex items-center justify-between px-4 py-3 cursor-pointer select-none"
              onClick={() => toggleExpand(idx())}
            >
              <div class="flex items-center gap-3 min-w-0">
                <div
                  class="w-8 h-8 rounded flex items-center justify-center flex-shrink-0"
                  style={{ 'background-color': 'var(--accent-subtle)' }}
                >
                  <span class="font-mono text-xs font-bold" style={{ color: 'var(--accent-400)' }}>
                    {idx() + 1}
                  </span>
                </div>
                <div class="min-w-0">
                  <div class="flex items-center gap-2">
                    <span class="font-mono text-sm font-semibold truncate" style={{ color: 'var(--text-primary)' }}>
                      {app.id || 'Untitled Application'}
                    </span>
                    {app.name && (
                      <span class="text-xs truncate" style={{ color: 'var(--text-tertiary)' }}>
                        ({app.name})
                      </span>
                    )}
                  </div>
                  <div class="flex items-center gap-2 text-xs" style={{ color: 'var(--text-tertiary)' }}>
                    <Show when={isLocalApps()}>
                      <span class="font-mono">{app.domain || 'no-domain'}</span>
                    </Show>
                    <Show when={!isLocalApps() && app.domain}>
                      <span class="font-mono">{app.domain}</span>
                      <span>•</span>
                    </Show>
                    <Show when={!isLocalApps()}>
                      <span class="font-mono">{app.path || 'no-path'}</span>
                    </Show>
                    <span>•</span>
                    <span class="font-mono">port {app.port || '—'}</span>
                    {app.remoteBaseUrl && (
                      <>
                        <span>•</span>
                        <span class="font-mono text-[10px] px-1.5 py-0.5 rounded" style={{ 'background-color': 'var(--accent-subtle)', color: 'var(--accent-400)' }}>
                          custom remote
                        </span>
                      </>
                    )}
                  </div>
                </div>
              </div>

              <div class="flex items-center gap-1">
                <Show when={!props.readOnly}>
                  <button
                    onClick={(e) => {
                      e.stopPropagation()
                      duplicateApp(idx())
                    }}
                    class="w-7 h-7 rounded flex items-center justify-center transition-colors"
                    style={{ color: 'var(--text-tertiary)' }}
                    title="Duplicate"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <rect x="9" y="9" width="13" height="13" rx="2" />
                      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                    </svg>
                  </button>
                  <button
                    onClick={(e) => {
                      e.stopPropagation()
                      removeApp(idx())
                    }}
                    class="w-7 h-7 rounded flex items-center justify-center transition-colors"
                    style={{
                      color: confirmDelete() === idx() ? 'var(--error)' : 'var(--text-tertiary)',
                      'background-color': confirmDelete() === idx() ? 'var(--error-subtle)' : 'transparent',
                    }}
                    title={confirmDelete() === idx() ? 'Click again to confirm' : 'Remove'}
                  >
                    <Show when={confirmDelete() === idx()}>
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                      </svg>
                    </Show>
                    <Show when={confirmDelete() !== idx()}>
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M18 6 6 18M6 6l12 12" />
                      </svg>
                    </Show>
                  </button>
                </Show>
                <div
                  class="w-7 h-7 rounded flex items-center justify-center transition-transform duration-200"
                  style={{
                    color: 'var(--text-tertiary)',
                    transform: expandedIndex() === idx() ? 'rotate(180deg)' : 'rotate(0deg)',
                  }}
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="m6 9 6 6 6-6" />
                  </svg>
                </div>
              </div>
            </div>

            {/* Expanded Form */}
            <Show when={expandedIndex() === idx()}>
              <div
                class="px-4 pb-4 pt-2 border-t"
                style={{ 'border-color': 'var(--border-subtle)' }}
              >
                <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                  {/* ID Field */}
                  <div class="space-y-1">
                    <label class="flex items-center justify-between text-xs font-medium" style={labelStyle}>
                      <span>Application ID <span style={{ color: 'var(--error)' }}>*</span></span>
                      <Show when={errors()[`${idx()}-id`]}>
                        <span class="text-[10px]" style={{ color: 'var(--error)' }}>{errors()[`${idx()}-id`]}</span>
                      </Show>
                    </label>
                    <input
                      type="text"
                      value={app.id}
                      onInput={(e) => updateApp(idx(), 'id', e.currentTarget.value)}
                      placeholder="my-service"
                      disabled={props.readOnly}
                      class="w-full h-9 px-3 rounded-lg border text-sm font-mono focus:outline-none transition-colors disabled:opacity-50"
                      style={errors()[`${idx()}-id`] ? inputErrorStyle : inputStyle}
                    />
                    <p class="text-[10px]" style={helpStyle}>
                      Unique identifier (lowercase letters, numbers, hyphens)
                    </p>
                  </div>

                  {/* Name Field */}
                  <div class="space-y-1">
                    <label class="text-xs font-medium" style={labelStyle}>
                      Display Name
                    </label>
                    <input
                      type="text"
                      value={app.name}
                      onInput={(e) => updateApp(idx(), 'name', e.currentTarget.value)}
                      placeholder="My Service"
                      disabled={props.readOnly}
                      class="w-full h-9 px-3 rounded-lg border text-sm focus:outline-none transition-colors disabled:opacity-50"
                      style={inputStyle}
                    />
                    <p class="text-[10px]" style={helpStyle}>
                      Human-readable name (optional)
                    </p>
                  </div>

                  {/* Domain Field */}
                  <div class="space-y-1">
                    <label class="flex items-center justify-between text-xs font-medium" style={labelStyle}>
                      <span>
                        {isLocalApps() ? 'Domain' : 'Custom Domain'}
                        <Show when={isLocalApps()}>
                          <span style={{ color: 'var(--error)' }}> *</span>
                        </Show>
                      </span>
                      <Show when={errors()[`${idx()}-domain`]}>
                        <span class="text-[10px]" style={{ color: 'var(--error)' }}>{errors()[`${idx()}-domain`]}</span>
                      </Show>
                    </label>
                    <input
                      type="text"
                      value={app.domain || ''}
                      onInput={(e) => updateApp(idx(), 'domain', e.currentTarget.value)}
                      placeholder={isLocalApps() ? 'app.localhost' : 'front.localhost'}
                      disabled={props.readOnly}
                      class="w-full h-9 px-3 rounded-lg border text-sm font-mono focus:outline-none transition-colors disabled:opacity-50"
                      style={errors()[`${idx()}-domain`] ? inputErrorStyle : inputStyle}
                    />
                    <p class="text-[10px]" style={helpStyle}>
                      {isLocalApps()
                        ? 'Local domain for this application'
                        : 'Optional hostname override for this routing app'}
                    </p>
                  </div>

                  {/* Path Field */}
                  <Show when={!isLocalApps()}>
                    <div class="space-y-1">
                      <label class="flex items-center justify-between text-xs font-medium" style={labelStyle}>
                        <span>Path <span style={{ color: 'var(--error)' }}>*</span></span>
                        <Show when={errors()[`${idx()}-path`]}>
                          <span class="text-[10px]" style={{ color: 'var(--error)' }}>{errors()[`${idx()}-path`]}</span>
                        </Show>
                      </label>
                      <input
                        type="text"
                        value={app.publicPath || app.path}
                        onInput={(e) => {
                          updateApp(idx(), 'publicPath', e.currentTarget.value)
                          updateApp(idx(), 'path', e.currentTarget.value)
                        }}
                        placeholder="/api/users"
                        disabled={props.readOnly}
                        class="w-full h-9 px-3 rounded-lg border text-sm font-mono focus:outline-none transition-colors disabled:opacity-50"
                        style={errors()[`${idx()}-path`] ? inputErrorStyle : inputStyle}
                      />
                      <p class="text-[10px]" style={helpStyle}>
                        Public/browser path prefix (e.g., /portal, /users)
                      </p>
                    </div>
                  </Show>

                  {/* Port Field */}
                  <div class="space-y-1">
                    <label class="flex items-center justify-between text-xs font-medium" style={labelStyle}>
                      <span>Local Port <span style={{ color: 'var(--error)' }}>*</span></span>
                      <Show when={errors()[`${idx()}-port`]}>
                        <span class="text-[10px]" style={{ color: 'var(--error)' }}>{errors()[`${idx()}-port`]}</span>
                      </Show>
                    </label>
                    <input
                      type="number"
                      value={app.port || ''}
                      onInput={(e) => updateApp(idx(), 'port', parseInt(e.currentTarget.value) || 0)}
                      placeholder="8080"
                      min="1"
                      max="65535"
                      disabled={props.readOnly}
                      class="w-full h-9 px-3 rounded-lg border text-sm font-mono focus:outline-none transition-colors disabled:opacity-50"
                      style={errors()[`${idx()}-port`] ? inputErrorStyle : inputStyle}
                    />
                    <p class="text-[10px]" style={helpStyle}>
                      Local development server port (1-65535)
                    </p>
                  </div>

                  {/* Health Endpoint Field */}
                  <div class="space-y-1">
                    <label class="text-xs font-medium" style={labelStyle}>
                      Health Endpoint
                    </label>
                    <input
                      type="text"
                      value={app.health || ''}
                      onInput={(e) => updateApp(idx(), 'health', e.currentTarget.value)}
                      placeholder="actuator/health"
                      disabled={props.readOnly}
                      class="w-full h-9 px-3 rounded-lg border text-sm font-mono focus:outline-none transition-colors disabled:opacity-50"
                      style={inputStyle}
                    />
                    <p class="text-[10px]" style={helpStyle}>
                      Health check endpoint path
                    </p>
                  </div>

                  {/* Upstream Context Field */}
                  <div class="space-y-1">
                    <label class="text-xs font-medium" style={labelStyle}>
                      Upstream Context
                    </label>
                    <input
                      type="text"
                      value={app.upstreamContext || app.context || ''}
                      onInput={(e) => {
                        updateApp(idx(), 'upstreamContext', e.currentTarget.value)
                        updateApp(idx(), 'context', e.currentTarget.value)
                      }}
                      placeholder="/service"
                      disabled={props.readOnly}
                      class="w-full h-9 px-3 rounded-lg border text-sm font-mono focus:outline-none transition-colors disabled:opacity-50"
                      style={inputStyle}
                    />
                    <p class="text-[10px]" style={helpStyle}>
                      Backend context/prefix. This is independent from the public path.
                    </p>
                  </div>

                  {/* Frontend Root Field */}
                  <Show when={!isLocalApps()}>
                    <div class="space-y-1">
                      <label class="text-xs font-medium" style={labelStyle}>
                        Frontend Root
                      </label>
                      <input
                        type="text"
                        value={app.frontendRoot || ''}
                        onInput={(e) => updateApp(idx(), 'frontendRoot', e.currentTarget.value)}
                        placeholder="/portal"
                        disabled={props.readOnly}
                        class="w-full h-9 px-3 rounded-lg border text-sm font-mono focus:outline-none transition-colors disabled:opacity-50"
                        style={inputStyle}
                      />
                      <p class="text-[10px]" style={helpStyle}>
                        Optional manifest/registration base used to verify SPA asset routing.
                      </p>
                    </div>
                  </Show>

                  {/* Remote Base URL Field */}
                  <Show when={!isLocalApps()}>
                    <div class="space-y-1 md:col-span-2">
                      <label class="flex items-center justify-between text-xs font-medium" style={labelStyle}>
                        <span>Remote Base URL</span>
                        <Show when={errors()[`${idx()}-remoteBaseUrl`]}>
                          <span class="text-[10px]" style={{ color: 'var(--error)' }}>{errors()[`${idx()}-remoteBaseUrl`]}</span>
                        </Show>
                      </label>
                      <input
                        type="text"
                        value={app.remoteBaseUrl || ''}
                        onInput={(e) => updateApp(idx(), 'remoteBaseUrl', e.currentTarget.value)}
                        placeholder="https://api.remote.example.test"
                        disabled={props.readOnly}
                        class="w-full h-9 px-3 rounded-lg border text-sm font-mono focus:outline-none transition-colors disabled:opacity-50"
                        style={errors()[`${idx()}-remoteBaseUrl`] ? inputErrorStyle : inputStyle}
                      />
                      <div class="flex items-center justify-between">
                        <p class="text-[10px]" style={helpStyle}>
                          Override workspace remote URL for this application
                        </p>
                        <Show when={app.remoteBaseUrl}>
                          <span
                            class="text-[10px] px-1.5 py-0.5 rounded font-mono"
                            style={{ 'background-color': 'var(--accent-subtle)', color: 'var(--accent-400)' }}
                          >
                            Custom remote configured
                          </span>
                        </Show>
                      </div>
                    </div>
                  </Show>
                </div>

                {/* Quick Actions */}
                <Show when={!props.readOnly}>
                  <div class="flex items-center justify-end gap-2 mt-4 pt-4 border-t" style={{ 'border-color': 'var(--border-subtle)' }}>
                    <button
                      onClick={() => duplicateApp(idx())}
                      class="inline-flex items-center gap-1.5 px-3 py-1.5 font-mono text-xs font-semibold border rounded-lg transition-colors hover:opacity-80"
                      style={{
                        'background-color': 'transparent',
                        'border-color': 'var(--border-default)',
                        color: 'var(--text-secondary)',
                      }}
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <rect x="9" y="9" width="13" height="13" rx="2" />
                        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                      </svg>
                      Duplicate
                    </button>
                    <button
                      onClick={() => removeApp(idx())}
                      class="inline-flex items-center gap-1.5 px-3 py-1.5 font-mono text-xs font-semibold border rounded-lg transition-colors hover:opacity-80"
                      style={{
                        'background-color': confirmDelete() === idx() ? 'var(--error)' : 'transparent',
                        'border-color': 'var(--error)',
                        color: confirmDelete() === idx() ? 'var(--bg-primary)' : 'var(--error)',
                      }}
                    >
                      <Show when={confirmDelete() === idx()}>
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                          <path d="M20 6 9 17l-5-5" />
                        </svg>
                        Confirm
                      </Show>
                      <Show when={confirmDelete() !== idx()}>
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                          <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                        </svg>
                        Remove
                      </Show>
                    </button>
                  </div>
                </Show>
              </div>
            </Show>
          </div>
        )}
      </For>
    </div>
  )
}

export default ApplicationManager
