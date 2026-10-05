import { useEffect, useRef, useState } from 'react'
import { ExternalLink, Loader2 } from 'lucide-react'
import { getPublicAccess, getTailscaleSharing, getSharedIngress, getRoutingWorkspaces, prepareTailscaleSharing, savePublicAccess, saveSharedIngress, setTailscaleSharing } from '@/api/client'
import { GetPublicAccessResponse, PublicAccessConfiguration, SharedIngressConfiguration, type SharedIngressRecord, type PublicAccessDocument, type TailscaleSharingState } from '@/gen/rementor/v1/rementor_pb'
import { AccessDialog } from '@/components/public-access'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Badge } from '@/components/ui/badge'
import { useWorkspaces } from '@/lib/workspaces'

const message = (error: unknown) => error instanceof Error ? error.message : 'Request failed'
const statuses: Record<string, string> = { off: 'Off', unavailable: 'Gateway unavailable', setting_up: 'Setting up', ready: 'Ready', needs_setup: 'Needs setup', needs_attention: 'Needs attention', awaiting_verification: 'Waiting for verification', removing: 'Removing', cleanup_failed: 'Removal needs attention', policy_changed: 'Policy changed' }

export function TailscaleSharingSettings() {
  const { reload } = useWorkspaces()
  const [state, setState] = useState<TailscaleSharingState>()
  const [access, setAccess] = useState<GetPublicAccessResponse>()
  const [shared, setShared] = useState(false)
  const [workspace, setWorkspace] = useState('')
  const [setup, setSetup] = useState<GetPublicAccessResponse>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const savedAccessRevision = useRef(0n)
  const editingGroup = useRef<SharedIngressRecord | undefined>(undefined)
  const refresh = async () => {
    const [sharing, policy, group] = await Promise.all([getTailscaleSharing(), getPublicAccess(), getSharedIngress()])
    setState(sharing.state); setAccess(policy); setShared(Boolean(group.state?.record))
  }
  useEffect(() => {
    let active = true
    let polling = false
    const update = async () => {
      if (polling) return
      polling = true
      try {
        const [sharing, policy, group] = await Promise.all([getTailscaleSharing(), getPublicAccess(), getSharedIngress()])
        if (active) { setState(sharing.state); setAccess(policy); setShared(Boolean(group.state?.record)) }
      } catch (failure) { if (active) setError(message(failure)) }
      finally { polling = false }
    }
    void update()
    const timer = setInterval(() => { void update() }, 5000)
    return () => { active = false; clearInterval(timer) }
  }, [])
  const configure = async () => {
    if (!access) return
    setBusy(true); setError('')
    try {
      const current = await getPublicAccess()
      const group = (await getSharedIngress()).state?.record
      const suggested = await prepareTailscaleSharing(workspace || current.configuration?.document?.sharedWorkspaceId || current.workspaces[0]?.workspaceId || '')
      savedAccessRevision.current = group?.version || current.configuration?.revision || 0n
      editingGroup.current = group
      const workspaces = group ? (await getRoutingWorkspaces(group.routingOwnerFingerprint)).workspaces : current.workspaces
      setSetup(new GetPublicAccessResponse({ ...current, workspaces, configuration: new PublicAccessConfiguration({ revision: savedAccessRevision.current, document: suggested.document }) }))
    } catch (failure) { setError(message(failure)) }
    finally { setBusy(false) }
  }
  const toggle = async (enabled: boolean) => {
    if (!state) return
    // First publication previews the proposed URLs and access policy. Once
    // configured, off/on is a single persistent action, with no shell command.
    if (enabled && !state.publications.length) { await configure(); return }
    setBusy(true); setError('')
    try { setState((await setTailscaleSharing(enabled, state.revision)).state); await refresh() }
    catch (failure) { setError(message(failure)); await refresh().catch(() => {}) }
    finally { setBusy(false) }
  }
  const save = async (document: PublicAccessDocument) => {
    const group = editingGroup.current
    if (group) {
      const saved = await saveSharedIngress(new SharedIngressConfiguration({ policy: document, routingOwnerFingerprint: group.routingOwnerFingerprint, gateways: group.gateways }), savedAccessRevision.current)
      savedAccessRevision.current = saved.state?.record?.version || savedAccessRevision.current
      editingGroup.current = saved.state?.record
    } else {
      const saved = await savePublicAccess(document, savedAccessRevision.current)
      savedAccessRevision.current = saved.configuration?.revision || savedAccessRevision.current
    }
    try {
      const current = await getTailscaleSharing()
      setState((await setTailscaleSharing(true, current.state?.revision || 0n)).state)
    } catch (failure) { throw new Error(`URLs were saved, but sharing could not be enabled: ${message(failure)}`) }
  }
  return <section className="space-y-4 rounded-lg border p-5" aria-labelledby="tailscale-sharing">
    <div className="flex items-start justify-between gap-4"><div><h2 id="tailscale-sharing" className="text-lg font-semibold">Share over Tailscale</h2><p className="mt-1 text-sm text-muted-foreground">Named HTTPS URLs for your other devices. Local .localhost URLs keep working.</p></div><Switch aria-label="Share over Tailscale" checked={state?.enabled || false} disabled={!state || busy} onCheckedChange={(enabled) => void toggle(enabled)} /></div>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {state?.error && <p role="status" className="text-sm text-destructive">{state.error}</p>}
    {state?.setupMessage && <p role="status" className="text-sm text-muted-foreground">{state.setupMessage}</p>}
    {state?.publications.map((publication) => <div key={publication.serviceName} className="space-y-2 border-t pt-4"><div className="flex flex-wrap items-center justify-between gap-2"><a href={publication.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 break-all text-sm text-primary hover:underline">{publication.url}<ExternalLink className="size-3.5 shrink-0" /></a><Badge variant={publication.status === 'ready' ? 'default' : 'secondary'}>{statuses[publication.status] || publication.status}</Badge></div>{publication.message && <p className="text-sm text-muted-foreground">{publication.message}</p>}</div>)}
    <div className="flex flex-wrap gap-2">{!shared && !access?.configuration?.document?.origins.length && <select aria-label="Environment or session to share" className="h-9 rounded-md border border-input bg-background px-3 text-sm" value={workspace || access?.workspaces[0]?.workspaceId || ''} onChange={(event) => setWorkspace(event.target.value)}>{access?.workspaces.map((scope) => <option key={scope.workspaceId} value={scope.workspaceId}>{scope.name}</option>)}</select>}<Button variant="outline" size="sm" disabled={!state?.dnsSuffix || busy || (!shared && !access?.workspaces.length)} onClick={() => void configure()}>{busy && <Loader2 className="animate-spin" />}Configure sharing</Button></div>
    {shared && <p className="text-sm text-muted-foreground">This toggle publishes the shared group’s existing URLs from this gateway. Edit their names and access policy in Shared URLs.</p>}
    <details className="text-sm"><summary className="cursor-pointer text-muted-foreground">Tailscale setup</summary><div className="mt-3 space-y-2 text-muted-foreground"><p>Use a dedicated tagged gateway with HTTPS enabled. Rementor can create service definitions when a Services API token is configured; otherwise create the suggested internal services once in Tailscale. Approve this gateway, or set an auto-approval policy.</p><a href="https://console.tailscale.com/admin/services" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">Open Tailscale Services<ExternalLink className="size-3.5" /></a></div></details>
    {setup && <AccessDialog title="Share over Tailscale" submitLabel="Save and enable sharing" useLocalNames={!editingGroup.current} state={setup} onClose={() => setSetup(undefined)} onSaved={async () => { await refresh(); await reload() }} onSave={save} />}
  </section>
}
