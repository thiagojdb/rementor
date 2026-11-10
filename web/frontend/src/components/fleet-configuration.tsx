import { useEffect, useState, type FormEvent } from 'react'
import { Copy, Laptop, Loader2, Plus, Settings2 } from 'lucide-react'
import { toast } from 'sonner'
import { cancelPeerInvitation, checkPeerConnection, configurePeerConnection, createPeerInvitation, getPeerState, inspectPeerInvitation, pairComputer, unpairComputer } from '@/api/client'
import type { CreatePeerInvitationResponse, GetPeerStateResponse, InspectPeerInvitationResponse, PairedComputer } from '@/gen/rementor/v1/rementor_pb'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

import { ComputerFollowingSettings } from '@/components/computer-following'
import { PublicAccessSettings } from '@/components/public-access'
import { TailscaleSharingSettings, LegacyTailscaleSharingSettings } from '@/components/tailscale-sharing'
import { SharedIngressSettings } from '@/components/shared-ingress'

const selectClass = 'h-9 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
const message = (error: unknown) => error instanceof Error ? error.message : 'Request failed'
const fingerprint = (value: string) => value.match(/.{1,8}/g)?.join(' ') || value
const address = (ip: string, port: string) => `${ip.includes(':') ? `[${ip}]` : ip}:${port}`

function Identity({ value }: { value: string }) {
  return <code className="block break-all rounded-md bg-muted p-3 text-xs leading-relaxed" aria-label="Computer fingerprint">{fingerprint(value)}</code>
}

function ConnectionSettings({ state, onClose, onSave }: { state: GetPeerStateResponse; onClose: () => void; onSave: (state: GetPeerStateResponse) => void }) {
  const split = state.listenAddress.match(/^\[?(.+?)\]?:(\d+)$/)
  const [ip, setIP] = useState(split?.[1] || state.privateAddresses[0] || '127.0.0.1')
  const [port, setPort] = useState(split?.[2] || '9443')
  const [url, setURL] = useState(state.url || `https://${address(ip, port)}`)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const updateAddress = (nextIP: string, nextPort: string) => {
    if (url === `https://${address(ip, port)}`) setURL(`https://${address(nextIP, nextPort)}`)
    setIP(nextIP); setPort(nextPort)
  }
  const save = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError('')
    try { onSave(await configurePeerConnection(address(ip, port), url.trim())); onClose() }
    catch (failure) { setError(message(failure)) }
    finally { setBusy(false) }
  }
  const disable = async () => {
    setBusy(true); setError('')
    try { onSave(await configurePeerConnection('', '')); onClose() }
    catch (failure) { setError(message(failure)) }
    finally { setBusy(false) }
  }
  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}><DialogContent className="max-h-[90dvh] overflow-y-auto" aria-describedby={undefined}><DialogHeader><DialogTitle>This computer’s connection</DialogTitle></DialogHeader>
    <form onSubmit={save} className="space-y-4">
      <div className="grid grid-cols-[1fr_6rem] gap-3"><label className="grid gap-2 text-sm font-medium">Private address<select className={selectClass} value={ip} disabled={busy} onChange={(event) => updateAddress(event.target.value, port)}>{[...new Set([ip, ...state.privateAddresses])].map((value) => <option key={value} value={value}>{value}{value === '127.0.0.1' ? ' · this computer only' : ''}</option>)}</select></label><label className="grid gap-2 text-sm font-medium">Port<Input type="number" min={1} max={65535} required value={port} disabled={busy} onChange={(event) => updateAddress(ip, event.target.value)} /></label></div>
      <label className="grid gap-2 text-sm font-medium">Address for other computers<Input required value={url} disabled={busy} onChange={(event) => setURL(event.target.value)} placeholder="https://100.64.0.1:9443" /></label>
      <p className="text-xs text-muted-foreground">Allow this port through your firewall.</p>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      <DialogFooter>{state.active && <Button type="button" variant="destructive" disabled={busy} onClick={() => void disable()}>Disable connection</Button>}<Button type="button" variant="outline" disabled={busy} onClick={onClose}>Cancel</Button><Button disabled={busy}>{busy && <Loader2 className="animate-spin" />}Save connection</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}

type PairingMode = 'join' | 'invite'

function PairingDialog({ mode, state, initialInvitation, onClose, onPaired }: { mode: PairingMode; state: GetPeerStateResponse; initialInvitation?: CreatePeerInvitationResponse; onClose: () => void; onPaired: () => void }) {
  const [code, setCode] = useState('')
  const [candidate, setCandidate] = useState<InspectPeerInvitationResponse>()
  const [invitation, setInvitation] = useState<CreatePeerInvitationResponse | undefined>(initialInvitation)
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer) }, [])
  const expired = Boolean(invitation?.expiresAt && invitation.expiresAt.toDate().getTime() <= now)
  const run = async (work: () => Promise<void>) => {
    setBusy(true); setError('')
    try { await work() } catch (failure) { setError(message(failure)) } finally { setBusy(false) }
  }
  const inspect = (event: FormEvent) => {
    event.preventDefault()
    void run(async () => { setCandidate(await inspectPeerInvitation(code)); setConfirmed(false) })
  }
  const pair = () => void run(async () => {
    if (!candidate || !confirmed) return
    const result = await pairComputer(code, candidate.fingerprint)
    toast.success(`Paired with ${result.computer?.name}`); onPaired(); onClose()
  })
  const create = () => void run(async () => { setInvitation(await createPeerInvitation()); onPaired() })
  const cancel = () => void run(async () => { await cancelPeerInvitation(); setInvitation(undefined) })
  const copy = () => void run(async () => {
    if (!invitation || expired) return
    await navigator.clipboard.writeText(invitation.invitation); toast.success('Invitation copied')
  })
  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}><DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-xl"><DialogHeader><DialogTitle>{mode === 'join' ? 'Connect to another computer' : `Invite a computer to ${state.name}`}</DialogTitle><DialogDescription>{mode === 'join' ? 'Get an invitation from Invite computer on the other computer, or run:' : 'Paste this invitation into Connect to computer on the other computer.'}</DialogDescription></DialogHeader>
    {mode === 'join' && <code className="block rounded-md bg-muted p-3 text-xs">rementorctl peer invite</code>}
    {mode === 'join' ? <form onSubmit={inspect} className="space-y-4">
      <label className="grid gap-2 text-sm font-medium">Invitation<textarea className="min-h-24 w-full rounded-md border bg-background p-3 font-mono text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" value={code} disabled={busy} onChange={(event) => { setCode(event.target.value); setCandidate(undefined); setConfirmed(false) }} required placeholder="rementor:…" spellCheck={false} autoComplete="off" /></label>
      {!candidate ? <Button disabled={busy || !code.trim()}>{busy && <Loader2 className="animate-spin" />}Review computer</Button> : <div className="space-y-3 rounded-md border p-4"><div><p className="font-medium">{candidate.name}</p><p className="break-all font-mono text-xs text-muted-foreground">{candidate.url}</p></div><Identity value={candidate.fingerprint} /><label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={confirmed} disabled={busy} onChange={(event) => setConfirmed(event.target.checked)} className="mt-1" />This fingerprint matches the one shown on the other computer.</label><Button type="button" disabled={busy || !confirmed} onClick={pair}>{busy && <Loader2 className="animate-spin" />}Pair with {candidate.name}</Button></div>}
    </form> : <div className="space-y-4">
      <details className="text-sm"><summary className="cursor-pointer text-muted-foreground">Generate from the terminal instead</summary><code className="mt-3 block rounded-md bg-muted p-3 text-xs">rementorctl peer invite</code></details>
      <p className="text-sm text-muted-foreground">Single use · expires in 5 minutes. Share only with the intended computer.</p>
      {invitation ? <><label className="grid gap-2 text-sm font-medium">Invitation<textarea readOnly className="min-h-20 w-full rounded-md border bg-muted p-3 font-mono text-xs" value={expired ? '' : invitation.invitation} aria-label="Generated invitation" spellCheck={false} /></label><div className="flex flex-wrap items-center gap-2"><Button disabled={busy || expired} onClick={copy}><Copy />Copy invitation</Button><Button variant="outline" disabled={busy} onClick={cancel}>Cancel invitation</Button><span className="text-xs text-muted-foreground">{expired ? 'Expired' : `Expires at ${invitation.expiresAt?.toDate().toLocaleTimeString()}`}</span></div><p className="text-sm font-medium">Compare this fingerprint on the other computer</p><Identity value={invitation.fingerprint} />{expired && <Button disabled={busy} onClick={create}>Create new invitation</Button>}</> : <Button disabled={busy} onClick={create}>{busy && <Loader2 className="animate-spin" />}Generate invitation</Button>}
    </div>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    <DialogFooter><Button variant="outline" disabled={busy} onClick={onClose}>Close</Button></DialogFooter>
  </DialogContent></Dialog>
}

export function FleetConfiguration() {
  const [state, setState] = useState<GetPeerStateResponse>()
  const [error, setError] = useState('')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [advancedAccessOpen, setAdvancedAccessOpen] = useState(false)
  const [pairingMode, setPairingMode] = useState<PairingMode>()
  const [generatedInvitation, setGeneratedInvitation] = useState<CreatePeerInvitationResponse>()
  const [disconnect, setDisconnect] = useState<PairedComputer>()
  const [busy, setBusy] = useState('')
  const [checks, setChecks] = useState<Record<string, { ok: boolean; text: string }>>({})
  useEffect(() => {
    let cancelled = false
    const refresh = () => { void getPeerState().then((result) => { if (!cancelled) { setState(result); setError('') } }).catch((failure: unknown) => { if (!cancelled) setError(message(failure)) }) }
    refresh(); const timer = setInterval(refresh, 5000)
    return () => { cancelled = true; clearInterval(timer) }
  }, [])
  const refresh = () => { void getPeerState().then(setState).catch((failure: unknown) => setError(message(failure))) }
  const invite = async () => {
    setBusy('invite'); setError('')
    try {
      setGeneratedInvitation(await createPeerInvitation()); setPairingMode('invite'); refresh()
    } catch (failure) { setError(message(failure)) }
    finally { setBusy('') }
  }
  const check = async (peer: PairedComputer) => {
    setBusy(peer.fingerprint)
    try { await checkPeerConnection(peer.fingerprint); setChecks((current) => ({ ...current, [peer.fingerprint]: { ok: true, text: `Connected at ${new Date().toLocaleTimeString()}` } })) }
    catch (failure) { setChecks((current) => ({ ...current, [peer.fingerprint]: { ok: false, text: message(failure) } })) }
    finally { setBusy('') }
  }
  const remove = async () => {
    if (!disconnect) return
    setBusy(disconnect.fingerprint); setError('')
    try { setState(await unpairComputer(disconnect.fingerprint)); setDisconnect(undefined) }
    catch (failure) { setError(message(failure)) }
    finally { setBusy('') }
  }
  return <main className="mx-auto w-full max-w-5xl flex-1 space-y-8 p-5 md:p-8 lg:p-10">
    <h1 className="text-2xl font-semibold tracking-tight md:text-3xl">Configuration</h1>
    {error && <Alert variant="destructive"><AlertTitle>Could not update connections</AlertTitle><AlertDescription>{error}</AlertDescription></Alert>}
    {!state ? <p className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />Loading computers…</p> : <>
      <section className="rounded-lg border p-5" aria-labelledby="this-computer"><div className="flex flex-wrap items-start justify-between gap-3"><div><h2 id="this-computer" className="flex items-center gap-2 font-semibold"><Laptop className="size-5" />{state.name} <span className="font-normal text-muted-foreground">· this computer</span></h2>{state.active && <p className="mt-2 break-all font-mono text-xs text-muted-foreground">{state.url}</p>}</div><div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" onClick={() => setSettingsOpen(true)}><Settings2 />Connection settings</Button><Button size="sm" disabled={Boolean(busy)} onClick={() => void invite()}>{busy === 'invite' ? <Loader2 className="animate-spin" /> : <Plus />}Invite computer</Button></div></div>{state.listenerError && <p role="alert" className="mt-3 text-sm text-destructive">{state.listenerError}</p>}<details className="mt-4 text-sm"><summary className="cursor-pointer text-muted-foreground">Identity</summary><div className="mt-3"><Identity value={state.fingerprint} /></div></details></section>
      <section className="space-y-4" aria-labelledby="paired-computers"><div className="flex flex-wrap items-center justify-between gap-3"><h2 id="paired-computers" className="text-lg font-semibold">Paired computers</h2><Button variant="outline" onClick={() => setPairingMode('join')}><Plus />Connect to computer</Button></div>
        <div className="overflow-x-auto rounded-lg border"><Table><TableHeader><TableRow><TableHead className="pl-4">Computer</TableHead><TableHead>Connection</TableHead><TableHead><span className="sr-only">Actions</span></TableHead></TableRow></TableHeader><TableBody>{state.computers.map((peer) => <TableRow key={peer.fingerprint}><TableCell className="min-w-48 pl-4"><p className="font-medium">{peer.name}</p><p className="mt-1 break-all font-mono text-xs text-muted-foreground">{peer.url}</p><details className="mt-2 text-xs"><summary className="cursor-pointer text-muted-foreground">Identity</summary><div className="mt-2"><Identity value={peer.fingerprint} /></div></details></TableCell><TableCell className="min-w-40"><Badge variant={checks[peer.fingerprint]?.ok === false ? 'destructive' : 'secondary'}>{checks[peer.fingerprint]?.ok === false ? 'Unreachable' : 'Paired'}</Badge>{checks[peer.fingerprint] && <p className="mt-2 max-w-xs break-words text-xs text-muted-foreground">{checks[peer.fingerprint].text}</p>}</TableCell><TableCell><div className="flex flex-wrap justify-end gap-2"><Button variant="outline" size="sm" disabled={Boolean(busy)} onClick={() => void check(peer)}>{busy === peer.fingerprint && <Loader2 className="animate-spin" />}Check connection</Button><Button variant="ghost" size="sm" disabled={Boolean(busy)} onClick={() => setDisconnect(peer)}>Disconnect</Button></div></TableCell></TableRow>)}{!state.computers.length && <TableRow><TableCell colSpan={3} className="h-24 text-center text-muted-foreground">No paired computers.</TableCell></TableRow>}</TableBody></Table></div>
      </section>
      <ComputerFollowingSettings peers={state.computers} />
      <TailscaleSharingSettings peers={state} onAdvancedSettings={() => setAdvancedAccessOpen(true)} />
      <details id="advanced-access" open={advancedAccessOpen} onToggle={(event) => setAdvancedAccessOpen(event.currentTarget.open)} className="rounded-lg border p-5">
        <summary className="cursor-pointer text-sm font-semibold">Advanced URL and gateway settings</summary>
        <div className="mt-5 space-y-8"><p className="text-sm text-muted-foreground">Use custom addresses with your own network setup, or manage the computers that publish the same app links.</p><PublicAccessSettings /><SharedIngressSettings peers={state} /><details><summary className="cursor-pointer text-sm font-medium">Existing Tailscale Service links</summary><div className="mt-3"><LegacyTailscaleSharingSettings peers={state} onAdvancedSettings={() => setAdvancedAccessOpen(true)} /></div></details></div>
      </details>
      {settingsOpen && <ConnectionSettings state={state} onClose={() => setSettingsOpen(false)} onSave={setState} />}
      {pairingMode && !settingsOpen && <PairingDialog mode={pairingMode} state={state} onClose={() => setPairingMode(undefined)} initialInvitation={pairingMode === 'invite' ? generatedInvitation : undefined} onPaired={refresh} />}
      <Dialog open={Boolean(disconnect)} onOpenChange={(open) => { if (!open && !busy) setDisconnect(undefined) }}><DialogContent><DialogHeader><DialogTitle>Disconnect {disconnect?.name}?</DialogTitle><DialogDescription>Pair again with a new invitation to reconnect.</DialogDescription></DialogHeader>{error && <p role="alert" className="text-sm text-destructive">{error}</p>}<DialogFooter><Button variant="outline" disabled={Boolean(busy)} onClick={() => setDisconnect(undefined)}>Cancel</Button><Button variant="destructive" disabled={Boolean(busy)} onClick={() => void remove()}>Disconnect</Button></DialogFooter></DialogContent></Dialog>
    </>}
  </main>
}
