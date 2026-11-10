import { useEffect, useState, type FormEvent } from 'react'
import { AlertCircle, Check, ChevronDown, ChevronLeft, ChevronRight, ChevronUp, Download, ExternalLink, Loader2, Pencil, Plus, X } from 'lucide-react'
import { getRoutingWorkspaces, getTailnetPublishing, saveTailnetPublishing } from '@/api/client'
import { TailnetPublicationEntry, TailnetPublishingConfiguration, type GetPeerStateResponse, type PublicWorkspaceHosts, type TailnetPublishedScope, type TailnetPublishingState } from '@/gen/rementor/v1/rementor_pb'
import { EnvironmentCombobox, type EnvironmentOption } from '@/components/environment-combobox'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

const message = (error: unknown) => error instanceof Error ? error.message : 'Request failed'
const key = (entry: TailnetPublicationEntry) => `${entry.computerFingerprint}/${entry.workspaceId}`
const selectClass = 'h-8 w-full min-w-0 rounded-md border border-input bg-background px-2 text-xs'
const computerLabel = (name: string) => name.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63).replace(/-+$/, '') || 'rementor'
const hostnameURL = (host: string, computer: string, suffix: string) => `https://${host.replace(/\.localhost$/, '')}.${computer}.${suffix}`
const sessionParent = (scope: PublicWorkspaceHosts, scopes: PublicWorkspaceHosts[]) => scope.sessionId ? scopes.find((parent) => parent.workspaceId === scope.environmentId)?.name || scope.environmentId : ''
const loginURL = (value: string) => { try { const url = new URL(value); return url.protocol === 'https:' && (url.hostname === 'tailscale.com' || url.hostname.endsWith('.tailscale.com')) ? url.href : '' } catch { return '' } }
type Editor = { configuration: TailnetPublishingConfiguration; entry?: TailnetPublicationEntry }

function URLList({ links }: { links: { localHost: string; url: string }[] }) {
  return <div className="space-y-2">{links.map((link) => <div key={link.localHost} className="min-w-0 font-mono text-[11px] leading-relaxed"><div className="break-all text-[10px] text-muted-foreground">{link.localHost}</div><div className="break-all">↳ {link.url}</div></div>)}</div>
}

function PublicationDialog({ editor, peers, onClose, onSave, onSetup }: { editor: Editor; peers: GetPeerStateResponse; onClose: () => void; onSave: (configuration: TailnetPublishingConfiguration) => Promise<void>; onSetup: () => void }) {
  const base = editor.configuration
  const [computer, setComputer] = useState(editor.entry?.computerFingerprint || peers.fingerprint)
  const [workspace, setWorkspace] = useState(editor.entry?.workspaceId || '')
  const [sessions, setSessions] = useState(editor.entry?.includeSessions ?? true)
  const [scopes, setScopes] = useState<PublicWorkspaceHosts[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const computers = [{ fingerprint: peers.fingerprint, name: peers.name }, ...peers.computers]
  const computerName = computers.find((item) => item.fingerprint === computer)?.name || editor.entry?.computerName || 'Unknown computer'
  const label = editor.entry?.computerFingerprint === computer ? editor.entry.computerName : computerLabel(computerName)
  useEffect(() => {
    let active = true
    setLoading(true); setScopes([]); setError('')
    void getRoutingWorkspaces(computer).then((result) => { if (active) setScopes(result.workspaces) }).catch((failure: unknown) => { if (active) setError(message(failure)) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [computer])
  const selected = scopes.find((scope) => scope.workspaceId === workspace)
  const children = selected && !selected.sessionId ? scopes.filter((scope) => scope.sessionId && scope.environmentId === selected.workspaceId) : []
  const options: EnvironmentOption[] = scopes.map((scope) => ({ id: scope.workspaceId, name: scope.name, parent: sessionParent(scope, scopes), hosts: scope.localHosts.length, included: base.entries.some((entry) => key(entry) !== (editor.entry && key(editor.entry)) && entry.computerFingerprint === computer && (entry.workspaceId === scope.workspaceId || (entry.includeSessions && Boolean(scope.sessionId) && entry.workspaceId === scope.environmentId))) })).sort((a, b) => Number(Boolean(a.parent)) - Number(Boolean(b.parent)) || a.name.localeCompare(b.name) || (a.parent || '').localeCompare(b.parent || ''))
  const links = (scope: PublicWorkspaceHosts) => scope.localHosts.map((host) => ({ localHost: host, url: hostnameURL(host, label, base.suffix) }))
  const save = async (event: FormEvent) => {
    event.preventDefault()
    if (!selected || options.find((option) => option.id === workspace)?.included) return
    setBusy(true); setError('')
    const entry = new TailnetPublicationEntry({ computerFingerprint: computer, computerName: label, workspaceId: workspace, includeSessions: !selected.sessionId && sessions })
    try {
      const entries = editor.entry ? base.entries.map((current) => key(current) === key(editor.entry!) ? entry : current) : [...base.entries, entry]
      await onSave(new TailnetPublishingConfiguration({ ...base, entries })); onClose()
    } catch (failure) { setError(message(failure)) } finally { setBusy(false) }
  }
  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}><DialogContent aria-describedby={undefined} onEscapeKeyDown={(event) => { if (document.activeElement?.getAttribute('aria-expanded') === 'true') event.preventDefault() }} className="max-h-[90dvh] gap-3 overflow-y-auto p-4 sm:max-w-lg"><DialogHeader><DialogTitle className="text-base">{editor.entry ? 'Edit publication' : 'Publish environment'}</DialogTitle></DialogHeader>
    <form onSubmit={save} className="space-y-3">
      <fieldset disabled={busy} className="space-y-3">
        <div className="grid grid-cols-[6.5rem_minmax(0,1fr)] items-start gap-2 sm:grid-cols-[8rem_minmax(0,1fr)]"><label className="grid gap-1 text-xs font-medium">Computer<select className={selectClass} value={computer} onChange={(event) => { setWorkspace(''); setSessions(true); setComputer(event.target.value) }}>{computers.map((item) => <option key={item.fingerprint} value={item.fingerprint}>{item.name}</option>)}</select></label><EnvironmentCombobox options={options} value={workspace} onChange={(id) => { setWorkspace(id); setSessions(true) }} loading={loading} disabled={busy} /></div>
        {selected && <div className="space-y-2 border-t pt-2"><div className="flex justify-between text-[10px] text-muted-foreground"><span>{selected.localHosts.length} URLs</span><span>HTTPS · 443</span></div><URLList links={links(selected)} />
          {!selected.sessionId && <div className="flex items-center justify-between gap-2 border-y py-2"><label htmlFor="publish-sessions" className="text-xs">Include current & new sessions</label><Switch id="publish-sessions" checked={sessions} onCheckedChange={setSessions} /></div>}
          {sessions && children.length > 0 && <details className="text-[11px]"><summary className="cursor-pointer text-muted-foreground">{children.length} sessions</summary><div className="mt-2 max-h-44 space-y-3 overflow-y-auto">{children.map((scope) => <div key={scope.workspaceId}><p className="mb-1 text-[10px] text-muted-foreground">{sessionParent(scope, scopes)} / {scope.name}</p><URLList links={links(scope)} /></div>)}</div></details>}
        </div>}
      </fieldset>
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
      {!base.listenAddress && !base.nodeName && <button type="button" className="text-xs text-muted-foreground underline underline-offset-2" onClick={onSetup}>Configure DNS & HTTPS</button>}
      <DialogFooter className="flex-row justify-between pt-1"><Button type="button" variant="outline" size="sm" disabled={busy} onClick={onClose}>Cancel</Button><Button size="sm" disabled={busy || loading || !selected || options.find((option) => option.id === workspace)?.included}>{busy && <Loader2 className="size-3 animate-spin" />}{editor.entry ? 'Save' : base.enabled ? 'Publish' : 'Save'}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}

function NetworkDialog({ state, configuration, name, onSave, onClose }: { state: TailnetPublishingState; configuration: TailnetPublishingConfiguration; name: string; onSave: (configuration: TailnetPublishingConfiguration) => Promise<void>; onClose: () => void }) {
  const [suffix, setSuffix] = useState(configuration.suffix)
  const [address, setAddress] = useState(configuration.listenAddress || state.addresses[0] || '')
  const [mode, setMode] = useState(configuration.certificateMode || 'private')
  const occupied = state.usedServePorts.includes('443')
  const [dedicated, setDedicated] = useState(Boolean(configuration.nodeName) || occupied || Boolean(state.discoveryError))
  const [nodeName, setNodeName] = useState(configuration.nodeName || state.suggestedNodeName)
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const download = () => {
    const url = URL.createObjectURL(new Blob([state.caCertificate], { type: 'application/x-pem-file' }))
    const link = document.createElement('a'); link.href = url; link.download = 'rementor-tailnet-ca.crt'; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  const save = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError('')
    try {
      const base = saved ? state.configuration || configuration : configuration
      await onSave(new TailnetPublishingConfiguration({ ...base, suffix: suffix.trim().toLowerCase().replace(/\.$/, ''), listenAddress: dedicated ? '' : address, nodeName: dedicated ? nodeName.trim().toLowerCase() : '', certificateMode: mode }))
      setSaved(true)
      if (!dedicated || (state.nodeStatus === 'Running' && base.nodeName === nodeName.trim().toLowerCase())) onClose()
    }
    catch (failure) { setError(message(failure)) } finally { setBusy(false) }
  }
  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}><DialogContent aria-describedby={undefined} className="max-h-[90dvh] gap-3 overflow-y-auto p-4 sm:max-w-lg"><DialogHeader><DialogTitle className="text-base">DNS & HTTPS</DialogTitle></DialogHeader><form onSubmit={save} className="space-y-3"><fieldset disabled={busy} className="space-y-3">
    <label className="grid gap-1 text-xs font-medium">Hostname suffix<Input className="h-8 font-mono text-xs" required value={suffix} onChange={(event) => setSuffix(event.target.value)} placeholder="rementor.internal" autoComplete="off" spellCheck={false} /></label>
    <label className="grid gap-1 text-xs font-medium">Publishing address<select className={selectClass} value={dedicated ? 'dedicated' : 'computer'} onChange={(event) => setDedicated(event.target.value === 'dedicated')}><option value="dedicated">Dedicated Tailscale address{occupied ? ' · Recommended' : ''}</option><option value="computer">{name}{occupied ? ' · Port 443 in use' : state.discoveryError ? ' · Usage unknown' : ' · Port 443 available'}</option></select></label>
    {dedicated ? <div className="space-y-2"><label className="grid gap-1 text-xs font-medium">Tailscale device name<Input className="h-8 font-mono text-xs" required pattern="[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?" maxLength={63} value={nodeName} onChange={(event) => setNodeName(event.target.value.toLowerCase())} autoComplete="off" spellCheck={false} /></label>
      {state.configuration?.nodeName === nodeName && <div className="text-xs">{state.nodeStatus === 'Running' && !state.nodeError ? <span className="inline-flex items-center gap-1"><Check className="size-3" /><span className="break-all font-mono text-[11px]">{state.nodeDnsName} · {state.nodeAddresses[0]}</span></span> : loginURL(state.nodeLoginUrl) ? <a className="inline-flex items-center gap-1 underline" target="_blank" rel="noreferrer" href={loginURL(state.nodeLoginUrl)}>Authorize in Tailscale<ExternalLink className="size-3" /></a> : <span className="text-muted-foreground">{state.nodeStatus === 'NeedsMachineAuth' ? 'Waiting for tailnet administrator approval' : 'Connecting to Tailscale…'}</span>}</div>}
    </div> : <label className="grid gap-1 text-xs font-medium">Tailscale IP<select className={selectClass} required={configuration.enabled} value={address} onChange={(event) => setAddress(event.target.value)}><option value="">Select Tailscale address…</option>{[...new Set([...state.addresses, ...(configuration.listenAddress ? [configuration.listenAddress] : [])])].map((ip) => <option key={ip}>{ip}</option>)}</select></label>}
    {state.usedServePorts.length > 0 && <details className="text-[11px] text-muted-foreground"><summary className="cursor-pointer">In use on {name}</summary><div className="mt-1 break-words font-mono">{state.usedServePorts.join(', ')}</div></details>}
    {state.discoveryError && <p role="status" className="text-xs text-destructive">{state.discoveryError}</p>}
    <label className="grid gap-1 text-xs font-medium">HTTPS certificates<select className={selectClass} value={mode} onChange={(event) => setMode(event.target.value)}><option value="private">Private CA · trust on each device</option><option value="external">Installed certificate · own domain</option></select></label>
    {mode === 'private' ? state.caCertificate && <div className="space-y-2 text-xs text-muted-foreground"><Button type="button" variant="outline" size="sm" onClick={download}><Download className="size-3" />Download CA certificate</Button><details className="text-[10px]"><summary className="cursor-pointer">Certificate fingerprint</summary><code className="mt-1 block break-all">SHA-256: {state.caFingerprint}</code></details></div> : <p className="text-xs leading-relaxed text-muted-foreground">Install the certificate and key on this computer using <code className="text-[10px]">REMENTOR_PUBLISH_CERT_FILE</code> and <code className="text-[10px]">REMENTOR_PUBLISH_KEY_FILE</code>. They must cover the environment and session names.</p>}
    <details className="border-t pt-2 text-xs"><summary className="cursor-pointer text-muted-foreground">Connect tailnet DNS</summary><p className="mt-2 leading-relaxed">In Tailscale DNS settings, add <code>{dedicated ? (state.configuration?.nodeName === nodeName && state.nodeAddresses[0]) || 'the address assigned after connecting' : address || 'this computer’s Tailscale IP'}</code> as a nameserver restricted to <code>{suffix}</code>. Allow TCP/UDP 53 and TCP 443 to this {dedicated ? 'device' : 'computer'}.</p><a href="https://console.tailscale.com/admin/dns" target="_blank" rel="noreferrer" className="mt-1 inline-flex items-center gap-1 underline">Tailscale DNS settings<ExternalLink className="size-3" /></a></details>
  </fieldset>
  {(dedicated ? state.nodeError : state.deviceError || state.dnsError || state.httpsError) && <p role="status" className="text-xs text-destructive">{dedicated ? state.nodeError : state.deviceError || state.dnsError || state.httpsError}</p>}
  {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  <DialogFooter className="flex-row justify-between pt-1"><Button type="button" variant="outline" size="sm" disabled={busy} onClick={onClose}>Close</Button><Button size="sm" disabled={busy || (!dedicated && occupied)}>{busy && <Loader2 className="size-3 animate-spin" />}{dedicated && (state.configuration?.nodeName !== nodeName || !state.nodeStatus) ? 'Connect' : 'Save'}</Button></DialogFooter></form></DialogContent></Dialog>
}

export function TailnetPublishingSettings({ peers }: { peers: GetPeerStateResponse; onAdvancedSettings?: () => void }) {
  const [state, setState] = useState<TailnetPublishingState>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [filter, setFilter] = useState('')
  const [page, setPage] = useState(0)
  const [expanded, setExpanded] = useState('')
  const [editor, setEditor] = useState<Editor>()
  const [network, setNetwork] = useState<TailnetPublishingConfiguration>()
  useEffect(() => {
    let active = true, pending = false
    const refresh = async () => {
      if (pending) return; pending = true
      try { const result = await getTailnetPublishing(); if (active) { setState(result.state); setError('') } }
      catch (failure) { if (active) setError(message(failure)) } finally { pending = false }
    }
    void refresh(); const timer = setInterval(() => { void refresh() }, 5000)
    return () => { active = false; clearInterval(timer) }
  }, [])
  const configuration = state?.configuration
  const save = async (value: TailnetPublishingConfiguration) => {
    const result = await saveTailnetPublishing(value, value.revision); setState(result.state)
  }
  const run = async (work: () => Promise<void>) => {
    setBusy(true); setError(''); try { await work() } catch (failure) { setError(message(failure)) } finally { setBusy(false) }
  }
  const scopesFor = (entry: TailnetPublicationEntry) => state?.scopes.filter((scope) => scope.entry && key(scope.entry) === key(entry)) || []
  const sourceName = (entry: TailnetPublicationEntry) => entry.computerFingerprint === peers.fingerprint ? peers.name : peers.computers.find((peer) => peer.fingerprint === entry.computerFingerprint)?.name || entry.computerName
  const scopeName = (entry: TailnetPublicationEntry) => scopesFor(entry).find((scope) => scope.scope?.workspaceId === entry.workspaceId)?.scope?.name || entry.workspaceId
  const entries = configuration?.entries.filter((entry) => `${scopeName(entry)} ${sourceName(entry)}`.toLowerCase().includes(filter.toLowerCase())) || []
  const maxPage = Math.max(0, Math.ceil(entries.length / 5) - 1), currentPage = Math.min(page, maxPage)
  const serving = state?.dnsActive && state.httpsActive
  const remove = (entry: TailnetPublicationEntry) => configuration && run(async () => { await save(new TailnetPublishingConfiguration({ ...configuration, entries: configuration.entries.filter((current) => key(current) !== key(entry)) })) })
  const scopeURLs = (scope: TailnetPublishedScope) => <div key={scope.scope?.workspaceId} className="space-y-1">{scope.scope?.sessionId && <p className="text-[10px] text-muted-foreground">{scope.scope.environmentId} / {scope.scope.name}</p>}{scope.error && <p className="text-xs text-destructive">{scope.error}</p>}<URLList links={scope.links} /></div>
  return <section id="tailscale-sharing" className="rounded-lg border p-4" aria-labelledby="tailscale-sharing-title">
    <div className="flex items-center justify-between gap-3"><h2 id="tailscale-sharing-title" className="text-base font-semibold">Publish over Tailscale</h2><Switch aria-label="Publish over Tailscale" checked={configuration?.enabled || false} disabled={!configuration || busy} onCheckedChange={(enabled) => { if (!configuration) return; if (enabled && !configuration.listenAddress && !configuration.nodeName) { setNetwork(new TailnetPublishingConfiguration({ ...configuration, enabled })); return } void run(() => save(new TailnetPublishingConfiguration({ ...configuration, enabled }))) }} /></div>
    <div className="mt-2 flex items-center justify-between gap-2 border-b pb-2 text-[11px] text-muted-foreground"><span className="min-w-0 truncate">{configuration?.suffix || 'Loading…'}</span><button className="inline-flex shrink-0 items-center gap-1 py-1" disabled={!configuration || busy} onClick={() => configuration && setNetwork(configuration)}>DNS & HTTPS {serving ? <Check className="size-3" /> : <span>· Setup required</span>}</button></div>
    <div className="mb-1 mt-3 flex items-center justify-between gap-2"><Input className="h-8 min-w-0 flex-1 text-xs sm:max-w-72" placeholder="Find environment or computer…" aria-label="Find published environment" value={filter} onChange={(event) => { setFilter(event.target.value); setPage(0) }} /><Button size="sm" className="h-8 shrink-0" disabled={!configuration || busy} onClick={() => configuration && setEditor({ configuration })}><Plus className="size-3" />Add</Button></div>
    {entries.slice(currentPage * 5, currentPage * 5 + 5).map((entry) => {
      const all = scopesFor(entry), primary = all.find((scope) => scope.scope?.workspaceId === entry.workspaceId), children = all.filter((scope) => scope.scope?.sessionId && scope.scope.workspaceId !== entry.workspaceId)
      const failure = all.find((scope) => scope.error)?.error
      const status = !configuration?.enabled ? 'Paused' : failure ? 'Unavailable' : !serving ? 'Needs setup' : !primary ? 'Checking' : 'Serving'
      const entryKey = key(entry), open = expanded === entryKey
      return <div key={entryKey}><div className="grid min-h-12 grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2 border-b py-2"><div className="min-w-0"><span className="break-words text-xs font-semibold">{scopeName(entry)}</span><span className="ml-2 text-[11px] text-muted-foreground max-sm:ml-0 max-sm:block">{sourceName(entry)}</span>{primary?.scope?.sessionId && <p className="mt-0.5 text-[10px] text-muted-foreground">{primary.scope.environmentId} / session</p>}</div><button aria-label={`Show URLs for ${scopeName(entry)} on ${sourceName(entry)}`} aria-expanded={open} className="flex items-center gap-1 text-[10px] text-muted-foreground" onClick={() => setExpanded(open ? '' : entryKey)}>{primary?.links.length ?? '…'} URLs{entry.includeSessions && ` · ${children.length} ${children.length === 1 ? 'session' : 'sessions'}`}{open ? <ChevronUp className="size-3" /> : <ChevronDown className="size-3" />}</button><div className="flex items-center"><span title={failure || status} role="status" aria-label={status} className={`mr-1 text-[10px] ${failure ? 'text-destructive' : 'text-muted-foreground'}`}>{failure ? <AlertCircle className="size-3" /> : status === 'Serving' ? '●' : status === 'Checking' ? <Loader2 className="size-3 animate-spin" /> : '○'}</span><Button variant="ghost" size="icon" className="size-7" aria-label={`Edit ${scopeName(entry)} on ${sourceName(entry)}`} disabled={busy} onClick={() => configuration && setEditor({ configuration, entry })}><Pencil className="size-3" /></Button><Button variant="ghost" size="icon" className="size-7" aria-label={`Unpublish ${scopeName(entry)} on ${sourceName(entry)}`} disabled={busy} onClick={() => void remove(entry)}><X className="size-3" /></Button></div></div>{open && <div className="space-y-2 border-b py-2 pl-2">{primary && scopeURLs(primary)}{children.length > 0 && <details className="text-[11px]"><summary className="cursor-pointer text-muted-foreground">{children.length} sessions</summary><div className="mt-2 max-h-52 space-y-3 overflow-y-auto">{children.map(scopeURLs)}</div></details>}{!primary && <p className="text-xs text-muted-foreground">Checking source computer…</p>}</div>}{failure && !open && <p role="alert" className="py-1 text-xs text-destructive">{failure}</p>}</div>
    })}
    {configuration && !entries.length && <p className="py-3 text-xs text-muted-foreground">{filter ? 'No matching environments.' : 'No published environments.'}</p>}
    {entries.length > 5 && <div className="mt-2 flex items-center justify-between text-[10px] text-muted-foreground"><span>{currentPage * 5 + 1}–{Math.min(currentPage * 5 + 5, entries.length)} of {entries.length}</span><div className="flex"><Button variant="ghost" size="icon" className="size-7" aria-label="Previous page" disabled={!currentPage} onClick={() => setPage(currentPage - 1)}><ChevronLeft className="size-3" /></Button><Button variant="ghost" size="icon" className="size-7" aria-label="Next page" disabled={currentPage >= maxPage} onClick={() => setPage(currentPage + 1)}><ChevronRight className="size-3" /></Button></div></div>}
    {configuration?.nodeName && state?.nodeStatus !== 'Running' && <div className="mt-2 text-xs">{loginURL(state?.nodeLoginUrl || '') ? <a className="inline-flex items-center gap-1 underline" href={loginURL(state?.nodeLoginUrl || '')} target="_blank" rel="noreferrer">Authorize {configuration.nodeName} in Tailscale<ExternalLink className="size-3" /></a> : <span className="text-muted-foreground">{state?.nodeStatus === 'NeedsMachineAuth' ? 'Waiting for tailnet administrator approval' : 'Connecting to Tailscale…'}</span>}</div>}
    {(error || state?.dnsError || state?.httpsError) && <p role="alert" className="mt-2 text-xs text-destructive">{error || state?.dnsError || state?.httpsError}</p>}
    {editor && <PublicationDialog editor={editor} peers={peers} onClose={() => setEditor(undefined)} onSave={save} onSetup={() => { setEditor(undefined); setNetwork(editor.configuration) }} />}
    {network && state && <NetworkDialog state={state} configuration={network} name={peers.name} onClose={() => setNetwork(undefined)} onSave={save} />}
  </section>
}
