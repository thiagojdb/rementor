import { useEffect, useState } from 'react'
import type { PlainMessage } from '@bufbuild/protobuf'
import { Loader2 } from 'lucide-react'
import { getPeerState, getWorkspaceInheritance, overrideInheritedRoute } from '@/api/client'
import type { ApplicationDTO, WorkspaceDTO } from '@/api/types'
import type { PairedComputer, WorkspaceInheritanceStatus } from '@/gen/rementor/v1/rementor_pb'
import { useWorkspaces } from '@/lib/workspaces'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'

const selectClass = 'h-9 w-full rounded-md border border-input bg-background px-3 text-sm'
const message = (error: unknown) => error instanceof Error ? error.message : 'Request failed'
const version = (ws: WorkspaceDTO) => ws.route?.version?.value ?? 0n

export function WorkspaceInheritanceControls({ workspace }: { workspace: WorkspaceDTO }) {
  const [peers, setPeers] = useState<PairedComputer[]>([])
  const [status, setStatus] = useState<WorkspaceInheritanceStatus>()
  useEffect(() => {
    let cancelled = false
    void getPeerState().then((result) => { if (!cancelled) setPeers(result.computers) }).catch(() => {})
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    if (!workspace.inheritance) return
    let cancelled = false
    const refresh = () => { void getWorkspaceInheritance(workspace.id).then((result) => {
      if (!cancelled) setStatus(result.status)
    }).catch(() => {}) }
    refresh()
    const timer = setInterval(refresh, 5000)
    return () => { cancelled = true; clearInterval(timer) }
  }, [workspace.id, workspace.inheritance])
  const source = workspace.inheritance?.source
  if (!source) return null
  const peerName = peers.find((item) => item.fingerprint === source.ownerFingerprint)?.name || source.ownerFingerprint.slice(0, 12)
  return <div className="space-y-2 text-sm">
    <div className="flex flex-wrap items-center gap-2">
      <span>{workspace.computerFollowed ? `Editing ${peerName}’s configuration` : `Following ${peerName}`}</span>
      <Badge variant={status?.status === 'current' ? 'secondary' : 'outline'}>{status?.status === 'current' ? 'Synced' : 'Cached configuration'}</Badge>
    </div>
    {status?.error && <p role="alert" className="text-destructive">{status.error}</p>}
  </div>
}

export function InheritedRouteDialog({ workspace, application, onClose }: { workspace: WorkspaceDTO; application: ApplicationDTO; onClose: () => void }) {
  const { refreshWorkspace } = useWorkspaces()
  const sourceEdit = Boolean(workspace.computerFollowed)
  const prior = workspace.inheritance?.overrides[application.id]
  const [target, setTarget] = useState(application.active ? application.peerEndpoint ? 'peer' : 'local' : 'remote')
  const [port, setPort] = useState(application.port ? String(application.port) : '')
  const [url, setURL] = useState(sourceEdit ? application.remoteBaseUrl || '' : prior?.remoteBaseUrl || '')
  const [customPattern, setCustomPattern] = useState(sourceEdit ? application.routePattern !== undefined : prior?.routePattern !== undefined)
  const [pattern, setPattern] = useState(prior?.routePattern ?? application.routePattern ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const save = async (reset = false) => {
    setBusy(true); setError('')
    const selection: PlainMessage<import('@/gen/rementor/v1/rementor_pb').InheritedRouteSelection> = { mode: target === 'peer' ? 'local' : target, port: target === 'local' ? Number(port) : 0, remoteBaseUrl: url.trim(), routePattern: customPattern ? pattern.trim() : sourceEdit ? '' : undefined }
    try { await overrideInheritedRoute(workspace.id, application.id, version(workspace), reset ? undefined : selection); await refreshWorkspace(workspace.id); onClose() }
    catch (failure) { setError(message(failure)); await refreshWorkspace(workspace.id).catch(() => {}) }
    finally { setBusy(false) }
  }
  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}><DialogContent><DialogHeader><DialogTitle>{application.name || application.id} route</DialogTitle><DialogDescription>{sourceEdit ? "Changes apply on the source computer and its followers." : `Override this route on ${workspace.name || workspace.id}.`}</DialogDescription></DialogHeader>
    <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); void save() }}>
      <label className="grid gap-2 text-sm font-medium">Target<select className={selectClass} value={target} disabled={busy} onChange={(event) => setTarget(event.target.value)}>{application.peerEndpoint && <option value="peer">Source computer’s registered app</option>}{!sourceEdit && <option value="local">This computer</option>}<option value="remote">Remote server</option></select></label>
      {target === 'local' && <label className="grid gap-2 text-sm font-medium">Port on this computer<Input type="number" min={1} max={65535} value={port} required disabled={busy} onChange={(event) => setPort(event.target.value)} /></label>}
      {target === 'remote' && <label className="grid gap-2 text-sm font-medium">Remote URL<Input type="url" value={url} disabled={busy} onChange={(event) => setURL(event.target.value)} placeholder={application.remoteBaseUrl || workspace.routing?.defaultRemoteBaseUrl} /><span className="text-xs font-normal text-muted-foreground">{sourceEdit ? "Leave empty to use the environment’s remote URL." : "Leave empty to use the source’s remote URL."}</span></label>}
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={customPattern} disabled={busy} onChange={(event) => setCustomPattern(event.target.checked)} />{sourceEdit ? "Custom route pattern" : "Override route pattern"}</label>
      {customPattern && <Input aria-label="Route pattern" value={pattern} disabled={busy} onChange={(event) => setPattern(event.target.value)} placeholder="/api/{path}" />}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      <DialogFooter>{!sourceEdit && prior && <Button type="button" variant="outline" disabled={busy} onClick={() => void save(true)}>Reset to source</Button>}<Button type="button" variant="outline" disabled={busy} onClick={onClose}>Cancel</Button><Button disabled={busy}>{busy && <Loader2 className="animate-spin" />}{sourceEdit ? "Save on source" : "Save override"}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}
