import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { AlertTriangle, Check, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import type { ApplicationConfigInput, ApplicationDTO, WorkspaceDTO, WorkspaceType } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useWorkspaces } from '@/lib/workspaces'

function applicationInputFromApplication(application: ApplicationDTO): ApplicationConfigInput {
  return {
    id: application.id,
    appId: application.appId || application.id,
    serviceId: application.serviceId || undefined,
    repository: application.repository || undefined,
    aliases: application.aliases || [],
    name: application.name,
    path: application.path,
    publicPath: application.publicPath || application.path,
    domain: application.domain || undefined,
    remoteBaseUrl: application.remoteBaseUrl || undefined,
    port: application.port || undefined,
    health: application.health || 'actuator/health',
    context: application.context || application.upstreamContext || application.path,
    upstreamContext: application.upstreamContext || application.context || application.path,
    frontendRoot: application.frontendRoot || undefined,
    frontendRootSource: application.frontendRootSource || undefined,
    routeOverride: application.routeOverride,
  }
}

function DialogError({ children }: { children: ReactNode }) {
  return <p role="alert" className="text-sm text-destructive">{children}</p>
}

function RunningLabel({ children }: { children: ReactNode }) {
  return <><Loader2 className="animate-spin" />{children}</>
}

export function CreateEnvironmentDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (workspace: WorkspaceDTO) => void
}) {
  const { createWorkspace } = useWorkspaces()
  const [id, setId] = useState('')
  const [name, setName] = useState('')
  const [type, setType] = useState<WorkspaceType>('routing')
  const [localDomain, setLocalDomain] = useState('')
  const [remoteBaseUrl, setRemoteBaseUrl] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setId('')
    setName('')
    setType('routing')
    setLocalDomain('')
    setRemoteBaseUrl('')
    setError('')
  }, [open])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const normalizedId = id.trim()
    if (!/^[a-z0-9-]+$/.test(normalizedId)) {
      setError('Use lowercase letters, numbers, and hyphens for the environment ID.')
      return
    }
    if (type === 'routing' && !localDomain.trim()) {
      setError('A local hostname is required for a routing environment.')
      return
    }
    setSaving(true)
    setError('')
    try {
      const workspace = await createWorkspace({
        id: normalizedId,
        type,
        name: name.trim() || normalizedId,
        color: 'bg-blue-500',
        localDomain: localDomain.trim(),
        defaultRemoteBaseUrl: remoteBaseUrl.trim(),
        applications: [],
      })
      toast.success('Environment created')
      onCreated(workspace)
      onOpenChange(false)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not create the environment.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>Create environment</DialogTitle>
          <DialogDescription>Set up the shared routing context that sessions branch from.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="environment-id">Environment ID</Label>
              <Input id="environment-id" autoFocus required placeholder="development" value={id} onChange={(event) => { setId(event.target.value); setError('') }} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="environment-name">Display name</Label>
              <Input id="environment-name" placeholder="Development" value={name} onChange={(event) => setName(event.target.value)} />
            </div>
          </div>
          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">Type</legend>
            <div className="flex gap-2">
              <Button type="button" variant={type === 'routing' ? 'default' : 'outline'} size="sm" onClick={() => setType('routing')}>Routing</Button>
              <Button type="button" variant={type === 'local-apps' ? 'default' : 'outline'} size="sm" onClick={() => setType('local-apps')}>Local apps</Button>
            </div>
          </fieldset>
          {type === 'routing' && <>
            <div className="space-y-2">
              <Label htmlFor="environment-host">Local hostname</Label>
              <Input id="environment-host" required placeholder="development.localhost" value={localDomain} onChange={(event) => setLocalDomain(event.target.value)} />
              <p className="text-xs text-muted-foreground">This is the shared browser address for the environment.</p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="environment-remote">Default remote URL</Label>
              <Input id="environment-remote" type="url" placeholder="https://development.example.com" value={remoteBaseUrl} onChange={(event) => setRemoteBaseUrl(event.target.value)} />
            </div>
          </>}
          {error && <DialogError>{error}</DialogError>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={saving}>{saving ? <RunningLabel>Creating</RunningLabel> : 'Create environment'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function EditEnvironmentDialog({
  workspace,
  open,
  onOpenChange,
}: {
  workspace: WorkspaceDTO | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { updateWorkspace } = useWorkspaces()
  const [localDomain, setLocalDomain] = useState('')
  const [remoteBaseUrl, setRemoteBaseUrl] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!workspace || !open) return
    setLocalDomain(workspace.routing?.localDomain || '')
    setRemoteBaseUrl(workspace.routing?.defaultRemoteBaseUrl || '')
    setError('')
  }, [workspace, open])

  if (!workspace) return null
  const routing = workspace.type === 'routing'
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (routing && !localDomain.trim()) {
      setError('A local hostname is required for a routing environment.')
      return
    }
    setSaving(true)
    setError('')
    try {
      await updateWorkspace(workspace.id, {
        expectedVersion: workspace.route?.version?.value,
        applications: workspace.applications.map(applicationInputFromApplication),
        localDomain: localDomain.trim(),
        defaultRemoteBaseUrl: remoteBaseUrl.trim(),
      })
      toast.success('Environment settings saved')
      onOpenChange(false)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not save the environment settings.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>Environment settings</DialogTitle>
          <DialogDescription>{workspace.name || workspace.id}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-2">
            <Label>Environment ID</Label>
            <Input value={workspace.id} disabled />
          </div>
          {routing ? <>
            <div className="space-y-2">
              <Label htmlFor="edit-environment-host">Local hostname</Label>
              <Input id="edit-environment-host" required value={localDomain} onChange={(event) => setLocalDomain(event.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-environment-remote">Default remote URL</Label>
              <Input id="edit-environment-remote" type="url" value={remoteBaseUrl} onChange={(event) => setRemoteBaseUrl(event.target.value)} />
            </div>
          </> : <p className="text-sm text-muted-foreground">Local-app environments have no shared routing hostname.</p>}
          {error && <DialogError>{error}</DialogError>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={saving}>{saving ? <RunningLabel>Saving</RunningLabel> : 'Save changes'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

type ServiceValues = {
  id: string
  name: string
  path: string
  domain: string
  remoteBaseUrl: string
  port: string
  health: string
  upstreamContext: string
}

function serviceValues(application?: ApplicationDTO): ServiceValues {
  return {
    id: application?.appId || application?.id || '',
    name: application?.name || '',
    path: application?.publicPath || application?.path || '',
    domain: application?.domain || '',
    remoteBaseUrl: application?.remoteBaseUrl || '',
    port: application?.port ? String(application.port) : '',
    health: application?.health || 'actuator/health',
    upstreamContext: application?.upstreamContext || application?.context || application?.path || '',
  }
}

export function ServiceDialog({
  workspace,
  application,
  open,
  onOpenChange,
}: {
  workspace: WorkspaceDTO | null
  application?: ApplicationDTO | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { upsertApplication } = useWorkspaces()
  const [values, setValues] = useState<ServiceValues>(() => serviceValues(application || undefined))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const editing = Boolean(application)

  useEffect(() => {
    if (!open) return
    setValues(serviceValues(application || undefined))
    setError('')
  }, [application, open])

  if (!workspace) return null
  const set = (field: keyof ServiceValues, value: string) => setValues((current) => ({ ...current, [field]: value }))
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!values.id.trim()) { setError('A service ID is required.'); return }
    if (workspace.type === 'routing' && !values.path.trim().startsWith('/')) { setError('The route path must start with /.'); return }
    if (workspace.type === 'routing' && !values.upstreamContext.trim().startsWith('/')) { setError('The upstream context must start with /.'); return }
    if (workspace.type === 'local-apps' && !values.domain.trim()) { setError('A local hostname is required for local-app services.'); return }
    const port = values.port ? Number(values.port) : 0
    if (!Number.isInteger(port) || port < 0 || port > 65535) { setError('Use a port from 1 to 65535.'); return }
    if (workspace.type === 'local-apps' && !port) { setError('Local-app services need a port.'); return }
    setSaving(true)
    setError('')
    try {
      const original = application ? applicationInputFromApplication(application) : undefined
      await upsertApplication(workspace.id, {
        ...original,
        id: values.id.trim(),
        appId: values.id.trim(),
        name: values.name.trim() || values.id.trim(),
        path: workspace.type === 'routing' ? values.path.trim() : original?.path || '',
        publicPath: workspace.type === 'routing' ? values.path.trim() : original?.publicPath || '',
        domain: values.domain.trim() || undefined,
        remoteBaseUrl: values.remoteBaseUrl.trim() || undefined,
        port: port || undefined,
        health: values.health.trim(),
        context: workspace.type === 'routing' ? values.upstreamContext.trim() : original?.context || '',
        upstreamContext: workspace.type === 'routing' ? values.upstreamContext.trim() : original?.upstreamContext || '',
      })
      toast.success(editing ? 'Service updated' : 'Service added')
      onOpenChange(false)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not save the service.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{editing ? 'Edit service' : 'Add service'}</DialogTitle>
          <DialogDescription>{workspace.name || workspace.id} · {workspace.session ? 'This session' : 'General routing'}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="service-id">Service ID</Label>
              <Input id="service-id" autoFocus required disabled={editing} placeholder="orders-api" value={values.id} onChange={(event) => set('id', event.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="service-name">Service name</Label>
              <Input id="service-name" placeholder="Orders API" value={values.name} onChange={(event) => set('name', event.target.value)} />
            </div>
          </div>
          {workspace.type === 'routing' && <div className="space-y-2">
            <Label htmlFor="service-path">Route path</Label>
            <Input id="service-path" required placeholder="/orders" value={values.path} onChange={(event) => set('path', event.target.value)} />
          </div>}
          {workspace.type === 'routing' && <div className="space-y-2">
            <Label htmlFor="service-remote">Remote URL</Label>
            <Input id="service-remote" type="url" placeholder="https://development.example.com/orders" value={values.remoteBaseUrl} onChange={(event) => set('remoteBaseUrl', event.target.value)} />
          </div>}
          {workspace.type === 'local-apps' && <div className="space-y-2">
            <Label htmlFor="service-domain">Local hostname</Label>
            <Input id="service-domain" required placeholder="orders.localhost" value={values.domain} onChange={(event) => set('domain', event.target.value)} />
          </div>}
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="service-port">Local port{workspace.type === 'local-apps' ? '' : ' (optional)'}</Label>
              <Input id="service-port" type="number" min="1" max="65535" placeholder="19404" value={values.port} onChange={(event) => set('port', event.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="service-health">Health endpoint</Label>
              <Input id="service-health" placeholder="actuator/health" value={values.health} onChange={(event) => set('health', event.target.value)} />
            </div>
          </div>
          {workspace.type === 'routing' && <div className="space-y-2">
            <Label htmlFor="service-context">Upstream context</Label>
            <Input id="service-context" required placeholder="/orders" value={values.upstreamContext} onChange={(event) => set('upstreamContext', event.target.value)} />
          </div>}
          {error && <DialogError>{error}</DialogError>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={saving}>{saving ? <RunningLabel>Saving</RunningLabel> : editing ? 'Save service' : 'Add service'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function RegisterPortDialog({
  workspace,
  application,
  open,
  onOpenChange,
}: {
  workspace: WorkspaceDTO | null
  application: ApplicationDTO | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { registerSessionApplication, upsertApplication } = useWorkspaces()
  const [port, setPort] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setPort(application?.port ? String(application.port) : '')
    setError('')
  }, [application, open])

  if (!workspace || !application) return null
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const parsed = Number(port)
    if (!Number.isInteger(parsed) || parsed < 1 || parsed > 65535) {
      setError('Use a port from 1 to 65535.')
      return
    }
    setSaving(true)
    setError('')
    try {
      if (workspace.session) await registerSessionApplication(workspace.id, { id: application.appId || application.id, port: parsed })
      else await upsertApplication(workspace.id, { ...applicationInputFromApplication(application), port: parsed })
      toast.success('Local port registered')
      onOpenChange(false)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not register the port.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[460px]">
        <DialogHeader>
          <DialogTitle>{application.port ? 'Edit local port' : 'Register local port'}</DialogTitle>
          <DialogDescription>{application.name} · {workspace.name || workspace.id}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="registered-port">Local port</Label>
            <Input id="registered-port" autoFocus required type="number" min="1" max="65535" placeholder="19404" value={port} onChange={(event) => { setPort(event.target.value); setError('') }} />
          </div>
          {error && <DialogError>{error}</DialogError>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={saving}>{saving ? <RunningLabel>Saving</RunningLabel> : 'Register port'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function RoutePatternDialog({
  workspace,
  application,
  open,
  onOpenChange,
}: {
  workspace: WorkspaceDTO | null
  application: ApplicationDTO | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { updateRoutePattern } = useWorkspaces()
  const [pattern, setPattern] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setPattern(application?.routePattern || '')
    setError('')
  }, [application, open])

  if (!workspace || !application) return null
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      await updateRoutePattern(workspace.id, application.id, pattern.trim())
      toast.success('Route pattern updated')
      onOpenChange(false)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not save the route pattern.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[460px]">
        <DialogHeader>
          <DialogTitle>Route pattern</DialogTitle>
          <DialogDescription>Override the browser-facing path for {application.name}.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="route-pattern">Pattern</Label>
            <Input id="route-pattern" autoFocus placeholder={`${application.path}/*`} value={pattern} onChange={(event) => setPattern(event.target.value)} />
            <p className="text-xs text-muted-foreground">Leave empty to use the service path. A wildcard can only appear at the end.</p>
          </div>
          {error && <DialogError>{error}</DialogError>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={saving}>{saving ? <RunningLabel>Saving</RunningLabel> : 'Save pattern'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function CreateSessionDialog({
  environment,
  open,
  onOpenChange,
  onCreated,
}: {
  environment: WorkspaceDTO | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (workspace: WorkspaceDTO) => void
}) {
  const { createRoutingSession } = useWorkspaces()
  const [name, setName] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setName('')
    setError('')
  }, [open])

  if (!environment) return null
  const hostnamePrefix = name.trim().toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 30).replace(/-+$/, '') || 'feature-x'
  const hostnamePreview = environment.routing?.localDomain ? `${hostnamePrefix}.${environment.routing.localDomain}` : ''
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) { setError('Enter a session name.'); return }
    if (!/^[a-z0-9-]+$/.test(trimmed)) { setError('Use lowercase letters, numbers, and hyphens for the session name.'); return }
    setSaving(true)
    setError('')
    try {
      const workspace = await createRoutingSession(environment.id, trimmed)
      toast.success('Session created')
      onCreated(workspace)
      onOpenChange(false)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not create the session.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[460px]">
        <DialogHeader>
          <DialogTitle>Create session</DialogTitle>
          <DialogDescription>Creates a separate routing branch under {environment.name || environment.id}.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="session-name">Session name</Label>
            <Input id="session-name" autoFocus required maxLength={120} placeholder="feature-x" value={name} onChange={(event) => { setName(event.target.value); setError('') }} />
            <p className="text-xs text-muted-foreground">{hostnamePreview ? <>Main address: <code>{hostnamePreview}</code></> : 'Use lowercase letters, numbers, and hyphens.'}</p>
          </div>
          {error && <DialogError>{error}</DialogError>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={saving}>{saving ? <RunningLabel>Creating</RunningLabel> : 'Create session'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function BaselineDialog({
  workspace,
  open,
  onOpenChange,
}: {
  workspace: WorkspaceDTO | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { refreshRoutingSession } = useWorkspaces()
  const [preview, setPreview] = useState<Awaited<ReturnType<typeof refreshRoutingSession>> | null>(null)
  const [loading, setLoading] = useState(false)
  const [applying, setApplying] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open || !workspace) return
    let active = true
    setPreview(null)
    setError('')
    setLoading(true)
    void refreshRoutingSession(workspace.id)
      .then((result) => { if (active) setPreview(result) })
      .catch((requestError) => { if (active) setError(requestError instanceof Error ? requestError.message : 'Could not review the baseline.') })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [workspace, open, refreshRoutingSession])

  if (!workspace) return null
  const apply = async () => {
    if (!preview?.previewToken) return
    setApplying(true)
    setError('')
    try {
      await refreshRoutingSession(workspace.id, preview.previewToken, true)
      toast.success('Session baseline refreshed')
      onOpenChange(false)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not refresh the baseline.')
    } finally {
      setApplying(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-[540px]">
        <DialogHeader>
          <DialogTitle>Review remote baseline</DialogTitle>
          <DialogDescription>Compare {workspace.name || workspace.id} with the environment it branched from. Local ports and route selections remain intact.</DialogDescription>
        </DialogHeader>
        {loading && <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />Loading baseline changes…</div>}
        {preview && <div className="space-y-4">
          {preview.conflicts.length > 0 && <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3"><p className="mb-2 flex items-center gap-2 text-sm font-medium text-destructive"><AlertTriangle className="size-4" />Resolve these conflicts first</p><ul className="space-y-1 text-sm text-muted-foreground">{preview.conflicts.map((conflict) => <li key={conflict}>• {conflict}</li>)}</ul></div>}
        </div>}
        {error && <DialogError>{error}</DialogError>}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button type="button" onClick={apply} disabled={!preview || preview.conflicts.length > 0 || applying}>{applying ? <RunningLabel>Applying</RunningLabel> : <><Check />Apply refresh</>}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: ReactNode
  confirmLabel: string
  onConfirm: () => Promise<void>
}) {
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => { if (open) setError('') }, [open])

  const confirm = async () => {
    setSaving(true)
    setError('')
    try {
      await onConfirm()
      onOpenChange(false)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not complete the request.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[460px]">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {error && <DialogError>{error}</DialogError>}
        <DialogFooter>
          <Button type="button" variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button type="button" variant="destructive" disabled={saving} onClick={() => void confirm()}>{saving ? <RunningLabel>Removing</RunningLabel> : confirmLabel}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
