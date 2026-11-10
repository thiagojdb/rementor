import { useEffect, useState, type FormEvent } from 'react'
import { Loader2, Plus, X } from 'lucide-react'
import { toast } from 'sonner'
import { getPublicAccess, savePublicAccess } from '@/api/client'
import { PublicAccessDocument, type GetPublicAccessResponse } from '@/gen/rementor/v1/rementor_pb'
import { useWorkspaces } from '@/lib/workspaces'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'

const selectClass = 'h-9 w-full rounded-md border border-input bg-background px-3 text-sm'
const message = (error: unknown) => error instanceof Error ? error.message : 'Request failed'
type Origin = { id: string; url: string; bindingKey: string }

export function AccessDialog({ state, onClose, onSaved, onSave, title = "Custom app URLs", description = "Map HTTPS app addresses to routing environments and choose who can access them. Network and HTTPS setup is managed separately.", useLocalNames = true, submitLabel = 'Save URLs' }: { state: GetPublicAccessResponse; onClose: () => void; onSaved: () => Promise<void>; onSave?: (document: PublicAccessDocument) => Promise<unknown>; title?: string; description?: string; useLocalNames?: boolean; submitLabel?: string }) {
  const { workspaces, reload } = useWorkspaces()
  const doc = state.configuration?.document
  const self = state.devices.find((device) => device.self)
  const [origins, setOrigins] = useState<Origin[]>(doc?.origins.map((origin) => ({ id: origin.id, url: origin.url, bindingKey: origin.id })) || [])
  const [scopes, setScopes] = useState<string[]>([...new Set(doc?.bindings.map((binding) => binding.workspaceId) || [])])
  const [bindings, setBindings] = useState<Record<string, string>>(Object.fromEntries(doc?.bindings.map((binding) => [`${binding.workspaceId}/${binding.originId}`, binding.localHost]) || []))
  const [devices, setDevices] = useState<Record<string, string>>(Object.fromEntries(doc?.devices.map((device) => [device.nodeId, device.workspaceId]) || []))
  const [accounts, setAccounts] = useState<string[]>(doc?.allowedUserIds.length ? doc.allowedUserIds.map(String) : self?.userId ? [String(self.userId)] : [])
  const [shared, setShared] = useState(doc?.sharedWorkspaceId || '')
  const [addScope, setAddScope] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const name = (id: string) => state.workspaces.find((workspace) => workspace.workspaceId === id)?.name || (useLocalNames ? workspaces.find((workspace) => workspace.id === id)?.name : undefined) || id
  const scopeOptions = scopes.map((id) => <option key={id} value={id}>{name(id)}</option>)
  const accountOptions = [...new Set([...state.devices.filter((device) => !device.tagged && device.userId !== 0n).map((device) => String(device.userId)), ...accounts])]
  const configuredDevices = [...state.devices, ...Object.keys(devices).filter((id) => !state.devices.some((device) => device.nodeId === id)).map((nodeId) => ({ nodeId, name: nodeId, online: false, self: false }))]
  const updateOrigin = (index: number, field: 'id' | 'url', value: string) => setOrigins((current) => current.map((origin, i) => i === index ? { ...origin, [field]: value } : origin))
  const deviceRow = (device: typeof configuredDevices[number]) => <label key={device.nodeId} className="grid gap-2 text-sm sm:grid-cols-2 sm:items-center sm:gap-3"><span className="min-w-0 break-words">{device.name}{device.self ? ' · this computer' : !device.online ? ' · offline' : ''}</span><select className={selectClass} aria-label={`${device.name} environment`} value={devices[device.nodeId] || ''} onChange={(event) => setDevices((current) => ({ ...current, [device.nodeId]: event.target.value }))}><option value="">{'tagged' in device && device.tagged ? 'No access' : 'Default environment'}</option>{scopeOptions}</select></label>
  const serviceDevices = configuredDevices.filter((device) => 'tagged' in device && device.tagged)
  const add = () => {
    if (!addScope) return
    setScopes((current) => [...current, addScope])
    if (!shared) setShared(addScope)
    setAddScope('')
  }
  const save = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError('')
    try {
      const currentOrigins = origins.map((origin) => ({ id: origin.id.trim(), url: origin.url.trim() }))
      const document = new PublicAccessDocument({
        schemaVersion: 1, origins: currentOrigins,
        bindings: scopes.flatMap((id) => currentOrigins.map((origin, index) => ({ workspaceId: id, originId: origin.id, localHost: bindings[`${id}/${origins[index].bindingKey}`] || state.workspaces.find((workspace) => workspace.workspaceId === id)?.localHosts[0] || '' }))),
        devices: Object.entries(devices).filter(([, scope]) => scope).map(([nodeId, workspaceId]) => ({ nodeId, workspaceId })),
        sharedWorkspaceId: shared, allowedUserIds: accounts.map(BigInt),
      })
      if (onSave) await onSave(document)
      else await savePublicAccess(document, state.configuration?.revision || 0n)
      await onSaved(); await reload(); toast.success(`${title} saved`); onClose()
    } catch (failure) { setError(message(failure)) } finally { setBusy(false) }
  }
  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}><DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-3xl"><DialogHeader><DialogTitle>{title}</DialogTitle><DialogDescription>{description}</DialogDescription></DialogHeader><form onSubmit={save} className="space-y-6">
    <fieldset disabled={busy} className="space-y-6">
      <div className="space-y-3"><h3 className="text-sm font-semibold">App addresses</h3><p className="text-xs leading-relaxed text-muted-foreground">Each address can serve a frontend, API, login service or WebSocket endpoint. Keep separate addresses where your apps need them.</p>{origins.map((origin, index) => <div key={index} className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 sm:grid-cols-[8rem_minmax(0,1fr)_auto]"><Input className="col-span-2 sm:col-span-1" aria-label={`App address ${index + 1} name`} placeholder="api" required value={origin.id} onChange={(event) => updateOrigin(index, 'id', event.target.value)} /><Input aria-label={`App address ${index + 1} URL`} placeholder="https://…" type="url" required value={origin.url} onChange={(event) => updateOrigin(index, 'url', event.target.value)} /><Button type="button" variant="ghost" size="icon" aria-label={`Remove app address ${index + 1}`} onClick={() => setOrigins((current) => current.filter((_, i) => i !== index))}><X /></Button></div>)}<Button type="button" variant="outline" size="sm" onClick={() => setOrigins((current) => [...current, { id: current.length ? '' : 'api', url: current.length ? '' : self?.dnsName ? `https://${self.dnsName}:8444` : '', bindingKey: crypto.randomUUID() }])}><Plus />Add app address</Button></div>
      <div className="space-y-3"><h3 className="text-sm font-semibold">Environments and sessions</h3>{scopes.map((id) => <div key={id} className="space-y-3 rounded-md border p-3"><div className="flex items-center justify-between"><span className="text-sm font-medium">{name(id)}</span><Button type="button" variant="ghost" size="icon" aria-label={`Remove ${name(id)} from public access`} onClick={() => { setScopes((current) => current.filter((scope) => scope !== id)); if (shared === id) setShared(''); setDevices((current) => Object.fromEntries(Object.entries(current).filter(([, scope]) => scope !== id))) }}><X /></Button></div>{origins.map((origin, index) => <label key={index} className="grid gap-2 text-sm sm:grid-cols-[8rem_minmax(0,1fr)] sm:items-center">{origin.id || `Origin ${index + 1}`}<select className={selectClass} aria-label={`${name(id)} ${origin.id} host`} value={bindings[`${id}/${origin.bindingKey}`] || state.workspaces.find((workspace) => workspace.workspaceId === id)?.localHosts[0] || ''} onChange={(event) => setBindings((current) => ({ ...current, [`${id}/${origin.bindingKey}`]: event.target.value }))}>{state.workspaces.find((workspace) => workspace.workspaceId === id)?.localHosts.map((host) => <option key={host} value={host}>{host}</option>)}</select></label>)}</div>)}<div className="flex gap-2"><select className={selectClass} aria-label="Add environment or session" value={addScope} onChange={(event) => setAddScope(event.target.value)}><option value="">Choose environment or session</option>{state.workspaces.filter((workspace) => !scopes.includes(workspace.workspaceId)).map((workspace) => <option key={workspace.workspaceId} value={workspace.workspaceId}>{name(workspace.workspaceId)}</option>)}</select><Button type="button" variant="outline" disabled={!addScope} onClick={add}>Add</Button></div></div>
      <label className="grid gap-2 text-sm font-medium">Default environment for other devices<select required className={selectClass} value={shared} onChange={(event) => setShared(event.target.value)}><option value="">Choose environment or session</option>{scopeOptions}</select><span className="text-xs font-normal text-muted-foreground">Allowed devices use this environment unless you give them a different selection below.</span></label>
      <div className="space-y-2"><h3 className="text-sm font-semibold">Who can open these apps</h3><p className="text-xs leading-relaxed text-muted-foreground">Choose allowed Tailscale accounts. Tailscale network permissions and each app’s own login still apply.</p>{accountOptions.map((id) => <label key={id} className="flex items-center gap-2 text-sm"><input type="checkbox" checked={accounts.includes(id)} onChange={(event) => setAccounts((current) => event.target.checked ? [...current, id] : current.filter((account) => account !== id))} />{self?.userId.toString() === id ? 'Your Tailscale account' : state.devices.filter((device) => device.userId.toString() === id).map((device) => device.name).join(', ') || id}</label>)}{state.deviceError && <p className="text-sm text-destructive">{state.deviceError}</p>}</div>
      <details className="space-y-3 rounded-md border p-3"><summary className="cursor-pointer text-sm font-medium">Different environments per device (optional)</summary><p className="text-xs leading-relaxed text-muted-foreground">Leave devices on the default environment, or select a different environment for a particular phone or computer.</p>{configuredDevices.filter((device) => !('tagged' in device && device.tagged)).map(deviceRow)}{serviceDevices.length > 0 && <details className="space-y-3 border-t pt-3"><summary className="cursor-pointer text-xs text-muted-foreground">Service devices ({serviceDevices.length})</summary><p className="text-xs text-muted-foreground">Tagged devices have no access unless you explicitly select an environment.</p>{serviceDevices.map(deviceRow)}</details>}</details>
    </fieldset>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    <DialogFooter><Button type="button" variant="outline" disabled={busy} onClick={onClose}>Cancel</Button><Button disabled={busy || !origins.length || !scopes.length || !shared || !accounts.length}>{busy && <Loader2 className="animate-spin" />}{submitLabel}</Button></DialogFooter>
  </form></DialogContent></Dialog>
}

export function PublicAccessSettings() {
  const { reload } = useWorkspaces()
  const [state, setState] = useState<GetPublicAccessResponse>()
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const refresh = async () => { const result = await getPublicAccess(); setState(result); setError('') }
  useEffect(() => { let active = true; void getPublicAccess().then((result) => { if (active) setState(result) }).catch((failure: unknown) => { if (active) setError(message(failure)) }); return () => { active = false } }, [])
  const clear = async () => {
    setBusy(true); setError('')
    try { await savePublicAccess(new PublicAccessDocument({ schemaVersion: 1 }), state?.configuration?.revision || 0n); await refresh(); await reload() }
    catch (failure) { setError(message(failure)) } finally { setBusy(false) }
  }
  const document = state?.configuration?.document
  const self = state?.devices.find((device) => device.self)
  const commands = [...new Set(document?.origins.flatMap((origin) => {
    try { const url = new URL(origin.url); return url.hostname === self?.dnsName ? [`sudo tailscale serve --bg --https=${url.port || '443'} http://${state?.ingress?.listenAddress}`] : [] } catch { return [] }
  }) || [])]
  return <section className="space-y-4" aria-labelledby="public-urls"><div className="flex items-center justify-between gap-3"><h2 id="public-urls" className="text-lg font-semibold">Custom app URLs</h2><Button variant="outline" disabled={!state || busy} onClick={() => setOpen(true)}>Configure custom URLs</Button></div>{error && <p role="alert" className="text-sm text-destructive">{error}</p>}{state?.ingress?.error && <p role="alert" className="text-sm text-destructive">{state.ingress.error}</p>}{document?.origins.length ? <div className="space-y-4 rounded-lg border p-4">{document.origins.map((origin) => <div key={origin.id} className="grid grid-cols-[8rem_1fr] gap-3 text-sm"><span className="font-medium">{origin.id}</span><a className="break-all text-primary underline-offset-4 hover:underline" href={origin.url} target="_blank" rel="noreferrer">{origin.url}</a></div>)}{commands.length > 0 && <details className="text-sm"><summary className="cursor-pointer text-muted-foreground">Connect these URLs with Tailscale</summary><p className="my-3 text-xs text-muted-foreground">Run on this computer:</p>{commands.map((command) => <code key={command} className="my-2 block break-all rounded-md bg-muted p-3 text-xs">{command}</code>)}</details>}<Button variant="outline" size="sm" disabled={busy} onClick={() => void clear()}>{busy && <Loader2 className="animate-spin" />}Remove custom URLs</Button></div> : null}{open && state && <AccessDialog state={state} onClose={() => setOpen(false)} onSaved={refresh} />}</section>
}
