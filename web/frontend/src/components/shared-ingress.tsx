import { useEffect, useState, type FormEvent } from 'react'
import { Loader2 } from 'lucide-react'
import { getPublicAccess, getSharedIngress, getRoutingWorkspaces, createSharedIngress, saveSharedIngress, joinSharedIngress, transferSharedIngress, recoverSharedIngress, cancelSharedIngress, syncSharedIngress } from '@/api/client'
import { GetPublicAccessResponse, PublicAccessConfiguration, SharedIngressConfiguration, type PublicWorkspaceHosts, type GetPeerStateResponse, type SharedIngressState } from '@/gen/rementor/v1/rementor_pb'
import { AccessDialog } from '@/components/public-access'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

const selectClass = 'h-9 w-full rounded-md border border-input bg-background px-3 text-sm'
const message = (error: unknown) => error instanceof Error ? error.message : 'Request failed'
type Action = 'create' | 'configure' | 'join' | 'transfer' | 'policy'

export function SharedIngressSettings({ peers }: { peers: GetPeerStateResponse }) {
  const [state, setState] = useState<SharedIngressState>()
  const [access, setAccess] = useState<GetPublicAccessResponse>()
  const [action, setAction] = useState<Action>()
  const [editing, setEditing] = useState<SharedIngressState>()
  const [routingWorkspaces, setRoutingWorkspaces] = useState<PublicWorkspaceHosts[]>([])
  const [target, setTarget] = useState('')
  const [routing, setRouting] = useState('')
  const [gateways, setGateways] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const refresh = async () => { setState((await getSharedIngress()).state) }
  useEffect(() => {
    let active = true
    const update = () => { void getSharedIngress().then((result) => { if (active) setState(result.state) }).catch((failure: unknown) => { if (active) setError(message(failure)) }) }
    update()
    void getPublicAccess().then((result) => { if (active) setAccess(result) }).catch((failure: unknown) => { if (active) setError(message(failure)) })
    const timer = setInterval(update, 5000)
    return () => { active = false; clearInterval(timer) }
  }, [])
  const record = state?.record
  const self = state?.selfFingerprint || peers.fingerprint
  const name = (id: string) => id === self ? `${peers.name} · this computer` : peers.computers.find((computer) => computer.fingerprint === id)?.name || id.slice(0, 12)
  const candidates = [{ fingerprint: self, name: peers.name }, ...peers.computers]
  const owner = record?.ownerFingerprint === self
  const policy = record?.policy || access?.configuration?.document
  const open = async (next: Action) => {
    setBusy(true); setError('')
    try {
      if (next === 'create' || next === 'policy') setAccess(await getPublicAccess())
      if (next === 'policy' && record) setRoutingWorkspaces((await getRoutingWorkspaces(record.routingOwnerFingerprint)).workspaces)
      setEditing(state); setTarget(''); setRouting(record?.routingOwnerFingerprint || self)
      setGateways(record?.gateways || [self]); setAction(next)
    } catch (failure) { setError(message(failure)) } finally { setBusy(false) }
  }
  const run = async (work: () => Promise<unknown>) => {
    setBusy(true); setError('')
    try { await work(); await refresh(); setAction(undefined) }
    catch (failure) { setError(message(failure)); await refresh().catch(() => {}) }
    finally { setBusy(false) }
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!editing) return
    const before = editing.record
    void run(() => {
      if (action === 'join') return joinSharedIngress(target, editing.revision)
      if (action === 'transfer' && before) return transferSharedIngress(target, before.version)
      const configuration = new SharedIngressConfiguration({ policy: before?.policy || access?.configuration?.document, routingOwnerFingerprint: routing, gateways })
      return before ? saveSharedIngress(configuration, before.version) : createSharedIngress(configuration, editing.revision)
    })
  }
  // A moved selector edits the existing routing host bindings, rather than
  // substituting workspaces from its own computer into that namespace.
  const editingRecord = editing?.record
  const policyState = access && editingRecord ? new GetPublicAccessResponse({
    configuration: new PublicAccessConfiguration({ revision: editingRecord.version, document: editingRecord.policy }), devices: access.devices, deviceError: access.deviceError,
    workspaces: routingWorkspaces,
  }) : undefined
  const choices = action === 'transfer' ? peers.computers.filter((computer) => record?.gateways.includes(computer.fingerprint)) : peers.computers
  return <section className="space-y-4" aria-labelledby="shared-urls">
    <div className="flex items-center justify-between gap-3"><h2 id="shared-urls" className="text-lg font-semibold">Shared URLs</h2>{record && !state?.pending && <Button variant="outline" disabled={busy} onClick={() => owner ? void open('configure') : void run(syncSharedIngress)}>{owner ? 'Gateways' : 'Synchronize'}</Button>}</div>
    {!record && <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={!state || busy} onClick={() => open('create')}>Share published URLs</Button><Button variant="outline" disabled={!state || !peers.computers.length || busy} onClick={() => open('join')}>Join shared URLs</Button></div>}
    {record && <div className="space-y-4 rounded-lg border p-4"><dl className="grid grid-cols-[9rem_1fr] gap-2 text-sm"><dt className="text-muted-foreground">Owner</dt><dd>{name(record.ownerFingerprint)}</dd><dt className="text-muted-foreground">Routing computer</dt><dd>{name(record.routingOwnerFingerprint)}</dd><dt className="text-muted-foreground">Gateways</dt><dd>{record.gateways.map(name).join(', ')}</dd></dl>{record.policy?.origins.map((origin) => <a key={origin.id} href={origin.url} target="_blank" rel="noreferrer" className="block break-all text-sm text-primary hover:underline">{origin.url}</a>)}{state?.pending ? <div className="space-y-3"><p className="text-sm">Transfer to {name(state.pending.next?.ownerFingerprint || '')}: {state.pending.committed ? 'committed; awaiting acknowledgement' : state.pending.outgoing ? 'awaiting successor' : 'prepared; awaiting commit'}</p><div className="flex gap-2"><Button variant="outline" disabled={busy} onClick={() => void run(() => recoverSharedIngress(state.revision))}>Resume transfer</Button>{state.pending.outgoing && !state.pending.committed && <Button variant="outline" disabled={busy} onClick={() => void run(() => cancelSharedIngress(state.revision))}>Cancel transfer</Button>}</div></div> : owner && <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy || !access} onClick={() => open('policy')}>Edit shared URLs</Button><Button variant="outline" disabled={busy || !peers.computers.some((computer) => record.gateways.includes(computer.fingerprint))} onClick={() => open('transfer')}>Transfer ownership</Button></div>}</div>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {action === 'policy' && policyState && editingRecord && <AccessDialog title="Shared URLs" useLocalNames={editingRecord.routingOwnerFingerprint === self} state={policyState} onClose={() => setAction(undefined)} onSaved={refresh} onSave={(document) => saveSharedIngress(new SharedIngressConfiguration({ policy: document, routingOwnerFingerprint: editingRecord.routingOwnerFingerprint, gateways: editingRecord.gateways }), editingRecord.version)} />}
    {action && action !== 'policy' && <Dialog open onOpenChange={(isOpen) => { if (!isOpen && !busy) setAction(undefined) }}><DialogContent aria-describedby={undefined}><DialogHeader><DialogTitle>{action === 'join' ? 'Join shared URLs' : action === 'transfer' ? 'Transfer ownership' : action === 'create' ? 'Share published URLs' : 'Shared URL gateways'}</DialogTitle></DialogHeader><form onSubmit={submit} className="space-y-5"><fieldset disabled={busy} className="space-y-4">
      {(action === 'join' || action === 'transfer') ? <label className="grid gap-2 text-sm font-medium">{action === 'join' ? 'Current owner' : 'New owner'}<select required className={selectClass} value={target} onChange={(event) => setTarget(event.target.value)}><option value="">Choose computer</option>{choices.map((computer) => <option key={computer.fingerprint} value={computer.fingerprint}>{computer.name}</option>)}</select>{action === 'transfer' && record && <span className="text-xs font-normal text-muted-foreground">Routes remain on {name(record.routingOwnerFingerprint)}.</span>}{action === 'join' && <span className="text-xs font-normal text-muted-foreground">The owner must select this computer as a gateway or routing computer.</span>}</label> : <><label className="grid gap-2 text-sm font-medium">Routing computer<select required className={selectClass} value={routing} onChange={(event) => setRouting(event.target.value)}>{candidates.map((computer) => <option key={computer.fingerprint} value={computer.fingerprint}>{name(computer.fingerprint)}</option>)}</select></label><div className="space-y-2"><h3 className="text-sm font-medium">Gateways</h3>{candidates.map((computer) => <label key={computer.fingerprint} className="flex items-center gap-2 text-sm"><input type="checkbox" disabled={computer.fingerprint === self} checked={gateways.includes(computer.fingerprint)} onChange={(event) => setGateways((current) => event.target.checked ? [...current, computer.fingerprint] : current.filter((id) => id !== computer.fingerprint))} />{name(computer.fingerprint)}</label>)}</div>{action === 'create' && <p className="text-xs text-muted-foreground">On each selected computer, choose Join shared URLs and select {peers.name}.</p>}{action === 'create' && !policy?.origins.length && <p className="text-sm">Add public URLs before sharing.</p>}{action === 'create' && policy?.origins.map((origin) => <p key={origin.id} className="break-all text-sm">{origin.url}</p>)}</>}
    </fieldset>{error && <p role="alert" className="text-sm text-destructive">{error}</p>}<DialogFooter><Button type="button" variant="outline" disabled={busy} onClick={() => setAction(undefined)}>Cancel</Button><Button disabled={busy || (action === 'create' && !policy?.origins.length) || ((action === 'join' || action === 'transfer') && !target)}>{busy && <Loader2 className="animate-spin" />}{action === 'join' ? 'Join' : action === 'transfer' ? 'Transfer ownership' : 'Save'}</Button></DialogFooter></form></DialogContent></Dialog>}
  </section>
}
