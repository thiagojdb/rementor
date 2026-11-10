import { useEffect, useRef, useState } from 'react'
import { ExternalLink, Loader2, ShieldCheck } from 'lucide-react'
import { getPublicAccess, getTailscaleSharing, getSharedIngress, getRoutingWorkspaces, prepareTailscaleSharing, savePublicAccess, saveSharedIngress, setTailscaleSharing } from '@/api/client'
import { GetPublicAccessResponse, PublicAccessConfiguration, SharedIngressConfiguration, type GetPeerStateResponse, type SharedIngressRecord, type PublicAccessDocument, type TailscaleSharingState } from '@/gen/rementor/v1/rementor_pb'
import { AccessDialog } from '@/components/public-access'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { useWorkspaces } from '@/lib/workspaces'

const message = (error: unknown) => error instanceof Error ? error.message : 'Request failed'
const statuses: Record<string, string> = { off: 'Off', unavailable: 'Gateway unavailable', setting_up: 'Publishing', ready: 'Available', needs_setup: 'Gateway setup required', needs_attention: 'Needs attention', awaiting_verification: 'Checking access', removing: 'Stopping', cleanup_failed: 'Removal needs attention', policy_changed: 'URL settings changed' }

export function LegacyTailscaleSharingSettings({ peers, onAdvancedSettings }: { peers: GetPeerStateResponse; onAdvancedSettings: () => void }) {
  const { reload } = useWorkspaces()
  const [state, setState] = useState<TailscaleSharingState>()
  const [access, setAccess] = useState<GetPublicAccessResponse>()
  const [group, setGroup] = useState<SharedIngressRecord>()
  const [workspace, setWorkspace] = useState('')
  const [setup, setSetup] = useState<{ response: GetPublicAccessResponse; enable: boolean }>()
  const [showSetup, setShowSetup] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const savedAccessRevision = useRef(0n)
  const editingGroup = useRef<SharedIngressRecord | undefined>(undefined)
  const refresh = async () => {
    const [sharing, policy, shared] = await Promise.all([getTailscaleSharing(), getPublicAccess(), getSharedIngress()])
    setState(sharing.state); setAccess(policy); setGroup(shared.state?.record)
  }
  useEffect(() => {
    let active = true
    let polling = false
    const update = async () => {
      if (polling) return
      polling = true
      try {
        const [sharing, policy, shared] = await Promise.all([getTailscaleSharing(), getPublicAccess(), getSharedIngress()])
        if (active) { setState(sharing.state); setAccess(policy); setGroup(shared.state?.record) }
      } catch (failure) { if (active) setError(message(failure)) }
      finally { polling = false }
    }
    void update()
    const timer = setInterval(() => { void update() }, 5000)
    return () => { active = false; clearInterval(timer) }
  }, [])

  const policy = group?.policy || access?.configuration?.document
  const isOwner = !group || group.ownerFingerprint === peers.fingerprint
  const isGateway = !group || group.gateways.includes(peers.fingerprint)
  const gatewayReady = Boolean(state?.dnsSuffix && !state.setupMessage && !state.error)
  const canEnable = gatewayReady && isGateway
  const ready = state?.enabled ? state.publications.filter((publication) => publication.status === 'ready').length || 0 : 0
  const cleanupFailed = state?.publications.some((publication) => publication.status === 'cleanup_failed')
  const stopping = state && !state.enabled && state.publications.some((publication) => publication.status !== 'off' && publication.status !== 'cleanup_failed')
  const status = !state ? 'Checking Tailscale…' : cleanupFailed ? 'Removal needs attention' : stopping ? 'Stopping sharing' : !gatewayReady ? 'Gateway setup required' : state.enabled ? ready === state.publications.length && ready > 0 ? 'Sharing is available' : ready > 0 ? 'Some URLs need attention' : 'Sharing requested · not ready' : 'Sharing is off'
  const defaultEnvironment = access?.workspaces.find((scope) => scope.workspaceId === policy?.sharedWorkspaceId)?.name || policy?.sharedWorkspaceId
  const computerName = (id: string) => id === peers.fingerprint ? peers.name : peers.computers.find((peer) => peer.fingerprint === id)?.name || id.slice(0, 12)

  const configure = async () => {
    if (!access || !isOwner) return
    setBusy(true); setError('')
    try {
      const current = await getPublicAccess()
      const record = (await getSharedIngress()).state?.record
      const selected = workspace || record?.policy?.sharedWorkspaceId || current.configuration?.document?.sharedWorkspaceId
      if (!selected) throw new Error('Choose the environment or session you want to open on your other devices.')
      const suggested = await prepareTailscaleSharing(selected)
      savedAccessRevision.current = record?.version || current.configuration?.revision || 0n
      editingGroup.current = record
      const workspaces = record ? (await getRoutingWorkspaces(record.routingOwnerFingerprint)).workspaces : current.workspaces
      setSetup({ response: new GetPublicAccessResponse({ ...current, workspaces, configuration: new PublicAccessConfiguration({ revision: savedAccessRevision.current, document: suggested.document }) }), enable: canEnable })
    } catch (failure) { setError(message(failure)) }
    finally { setBusy(false) }
  }
  const toggle = async (enabled: boolean) => {
    if (!state || (enabled && !canEnable)) return
    if (enabled && !state.publications.length) { await configure(); return }
    setBusy(true); setError('')
    try { setState((await setTailscaleSharing(enabled, state.revision)).state); await refresh() }
    catch (failure) { setError(message(failure)); await refresh().catch(() => {}) }
    finally { setBusy(false) }
  }
  const save = async (document: PublicAccessDocument) => {
    if (setup?.enable && !canEnable) throw new Error('The gateway is no longer ready. Close this dialog and check its setup before starting sharing.')
    const record = editingGroup.current
    if (record) {
      const saved = await saveSharedIngress(new SharedIngressConfiguration({ policy: document, routingOwnerFingerprint: record.routingOwnerFingerprint, gateways: record.gateways }), savedAccessRevision.current)
      savedAccessRevision.current = saved.state?.record?.version || savedAccessRevision.current
      editingGroup.current = saved.state?.record
    } else {
      const saved = await savePublicAccess(document, savedAccessRevision.current)
      savedAccessRevision.current = saved.configuration?.revision || savedAccessRevision.current
    }
    if (!setup?.enable) return
    try {
      const current = await getTailscaleSharing()
      setState((await setTailscaleSharing(true, current.state?.revision || 0n)).state)
    } catch (failure) { throw new Error(`URL settings were saved, but sharing could not start: ${message(failure)}`) }
  }

  return <section id="legacy-tailscale-sharing" className="space-y-5 rounded-lg border p-5" aria-labelledby="legacy-tailscale-sharing-title">
    <div className="flex flex-wrap items-start justify-between gap-3"><div className="max-w-xl"><h2 id="legacy-tailscale-sharing-title" className="text-lg font-semibold">Access apps over Tailscale</h2><p className="mt-1 text-sm leading-relaxed text-muted-foreground">Open your apps from your phone or another computer using private HTTPS links. Those devices need to be connected to your Tailscale network.</p></div><Badge variant={ready > 0 && gatewayReady ? 'default' : 'secondary'} role="status">{status}</Badge></div>
    <p className="text-sm text-muted-foreground">This shares access to apps. To inherit another computer’s routing configuration, use <a href="#computer-following" className="text-primary underline underline-offset-4">Follow a computer</a> above.</p>
    {!state && !error && <p className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />Checking this computer’s gateway…</p>}
    {state && !gatewayReady && <div className="space-y-3 rounded-md border bg-muted/40 p-4"><div><p className="text-sm font-medium">{state.gatewayName || peers.name} is not ready to publish app links</p><p className="mt-1 text-sm leading-relaxed text-muted-foreground">Stable app names use a separate Tailscale publishing identity (a tagged gateway). An administrator needs to connect that gateway and enable HTTPS before these links work.</p></div><Button variant="outline" size="sm" aria-expanded={showSetup} aria-controls="tailscale-setup-steps" onClick={() => setShowSetup((open) => !open)}>{showSetup ? 'Hide setup steps' : 'Show setup steps'}</Button></div>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {state?.error && <p role="alert" className="text-sm text-destructive">{state.error}</p>}
    {group && <p className="text-sm text-muted-foreground">App routes run on <strong className="text-foreground">{computerName(group.routingOwnerFingerprint)}</strong>. Shared URL settings are managed on <strong className="text-foreground">{computerName(group.ownerFingerprint)}</strong>. {!isGateway && 'This computer is not selected as a gateway.'} <a href="#advanced-access" onClick={onAdvancedSettings} className="text-primary underline underline-offset-4">Manage shared gateways</a></p>}
    {defaultEnvironment && <dl className="grid gap-3 rounded-md bg-muted/30 p-4 text-sm sm:grid-cols-3"><div><dt className="text-xs text-muted-foreground">Default environment</dt><dd className="mt-1 font-medium">{defaultEnvironment}</dd></div><div><dt className="text-xs text-muted-foreground">Who can connect</dt><dd className="mt-1">{policy?.allowedUserIds.length || 0} allowed Tailscale {policy?.allowedUserIds.length === 1 ? 'account' : 'accounts'}</dd></div><div><dt className="text-xs text-muted-foreground">Gateway</dt><dd className="mt-1">{state?.gatewayName || 'Not available'}</dd></div></dl>}
    <div className="space-y-3">
      {isOwner && !policy?.origins.length && <label className="grid max-w-lg gap-2 text-sm font-medium">Environment or session to open on other devices<select aria-label="Environment or session to share" className="h-9 min-w-0 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" value={workspace} disabled={busy} onChange={(event) => setWorkspace(event.target.value)}><option value="">Choose an environment or session</option>{access?.workspaces.map((scope) => <option key={scope.workspaceId} value={scope.workspaceId}>{scope.name}</option>)}</select></label>}
      <div className="flex flex-wrap gap-2">{isOwner && <Button variant={canEnable ? 'default' : 'outline'} size="sm" disabled={!state?.dnsSuffix || busy || !access || (!policy?.origins.length && !workspace)} onClick={() => void configure()}>{busy && <Loader2 className="animate-spin" />}{policy?.origins.length ? 'Review URLs and access' : 'Preview app links'}</Button>}{!state?.enabled && Boolean(state?.publications.length) && <Button size="sm" disabled={!canEnable || busy} onClick={() => void toggle(true)}>Start sharing</Button>}{(state?.enabled || cleanupFailed) && <Button variant="outline" size="sm" disabled={busy} onClick={() => void toggle(false)}>{busy && <Loader2 className="animate-spin" />}{cleanupFailed && !state?.enabled ? 'Retry stopping sharing' : 'Stop sharing from this gateway'}</Button>}</div>
      {!canEnable && isOwner && state?.dnsSuffix && <p className="text-xs text-muted-foreground">You can preview and save URL settings now. Sharing can start after gateway setup is complete.</p>}
      {!isOwner && <p className="text-sm text-muted-foreground">Change app links and access on {group && computerName(group.ownerFingerprint)}. This computer publishes that shared configuration.</p>}
    </div>
    {state?.publications.map((publication) => <div key={publication.serviceName} className="space-y-2 border-t pt-4"><div className="flex flex-wrap items-center justify-between gap-2"><div className="min-w-0"><p className="mb-1 text-xs font-medium text-muted-foreground">{publication.originId}</p>{publication.status === 'ready' && state.enabled && gatewayReady ? <a href={publication.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 break-all text-sm text-primary hover:underline">{publication.url}<ExternalLink className="size-3.5 shrink-0" /><span className="sr-only"> (opens in a new tab)</span></a> : <code className="break-all text-xs">{publication.url}</code>}</div><Badge variant={publication.status === 'ready' && state.enabled && gatewayReady ? 'default' : 'secondary'}>{!state.enabled && publication.status === 'ready' ? 'Stopping' : statuses[publication.status] || publication.status}</Badge></div>{publication.message && <p className="text-sm text-muted-foreground">{publication.message}</p>}</div>)}
    <details id="tailscale-setup-steps" open={showSetup} onToggle={(event) => setShowSetup(event.currentTarget.open)} className="border-t pt-4 text-sm"><summary className="cursor-pointer font-medium">How stable app links are set up</summary><div className="mt-4 space-y-4 leading-relaxed text-muted-foreground">{state?.setupMessage && <p className="rounded-md bg-muted p-3 text-xs"><span className="font-medium text-foreground">Gateway check: </span>{state.setupMessage}</p>}<ol className="list-decimal space-y-3 pl-5"><li><strong className="text-foreground">Connect a publishing gateway.</strong> An administrator sets up a separate tagged Tailscale gateway and connects Rementor to it. Keep your normal {peers.name} device signed in as you.</li><li><strong className="text-foreground">Allow stable HTTPS app names.</strong> Enable MagicDNS and HTTPS in Tailscale, create each suggested Service, and approve the gateway to serve it. {state?.automaticCreation ? 'Automatic Service creation is configured on this gateway.' : 'Without automatic Service creation, an administrator creates these once in the Tailscale console.'}</li><li><strong className="text-foreground">Review links and who can use them.</strong> Choose an environment, check the suggested URLs and allowed accounts, then start sharing. A link becomes available after Rementor verifies it.</li></ol><a href="https://console.tailscale.com/admin/services" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">Open Tailscale Services<ExternalLink className="size-3.5" /></a><p className="text-xs">A phone needs the Tailscale app and an allowed account. It does not need Rementor or nginx. Your apps’ own login requirements still apply.</p></div></details>
    <p className="flex items-start gap-2 text-xs leading-relaxed text-muted-foreground"><ShieldCheck className="mt-0.5 size-4 shrink-0" />Local .localhost URLs keep working. If an explicitly selected app instance goes offline, its requests fail visibly.</p>
    {setup && <AccessDialog title="App links and access" description={setup.enable ? 'Review the app URLs, default environment and allowed accounts. Saving also starts publishing from this gateway; links stay pending until verified.' : 'Preview the app URLs, default environment and allowed accounts. Saving records these settings; it does not start sharing while gateway setup is incomplete.'} submitLabel={setup.enable ? 'Save and start sharing' : 'Save URL settings'} useLocalNames={!editingGroup.current} state={setup.response} onClose={() => setSetup(undefined)} onSaved={async () => { await refresh(); await reload() }} onSave={save} />}
  </section>
}

export { TailnetPublishingSettings as TailscaleSharingSettings } from '@/components/tailnet-publishing'
