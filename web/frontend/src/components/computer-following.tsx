import { useEffect, useState } from 'react'
import { Loader2, RefreshCw } from 'lucide-react'
import { followComputer, getComputerFollowing, getPeerState, setEnvironmentFollowing, stopFollowingComputer, syncComputerConfiguration } from '@/api/client'
import type { WorkspaceDTO } from '@/api/types'
import type { ComputerFollowingStatus, PairedComputer } from '@/gen/rementor/v1/rementor_pb'
import { useWorkspaces } from '@/lib/workspaces'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

const message = (error: unknown) => error instanceof Error ? error.message : 'Request failed'

function useFollowing() {
  const { reload } = useWorkspaces()
  const [state, setState] = useState<ComputerFollowingStatus>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [resume, setResume] = useState<WorkspaceDTO>()
  useEffect(() => {
    let cancelled = false
    const refresh = () => {
      void getComputerFollowing().then((result) => {
        if (!cancelled) setState(result.state)
      }).catch((failure: unknown) => { if (!cancelled) setError(message(failure)) })
    }
    refresh()
    const timer = setInterval(refresh, 5000)
    return () => { cancelled = true; clearInterval(timer) }
  }, [])

  const run = async (action: () => Promise<{ state?: ComputerFollowingStatus }>) => {
    setBusy(true); setError('')
    try {
      const result = await action()
      setState(result.state); setResume(undefined)
      await reload()
      return true
    } catch (failure) {
      setError(message(failure))
      await getComputerFollowing().then((result) => setState(result.state)).catch(() => {})
      return false
    } finally { setBusy(false) }
  }
  const change = async (workspace: WorkspaceDTO, following: boolean) => {
    if (!state) return
    await run(() => setEnvironmentFollowing(workspace.id, following, state.revision, workspace.route?.version?.value ?? 0n))
  }
  return { state, busy, error, run, resume, setResume, change }
}

type Following = ReturnType<typeof useFollowing>

function ResumeDialog({ following, sourceName }: { following: Following; sourceName: string }) {
  const { resume, setResume, busy, error, change } = following
  return <Dialog open={Boolean(resume)} onOpenChange={(open) => { if (!open) setResume(undefined) }}><DialogContent>
    <DialogHeader><DialogTitle>Follow {sourceName} for {resume?.name || resume?.id}?</DialogTitle><DialogDescription>Replace this environment’s local settings with {sourceName}’s configuration.</DialogDescription></DialogHeader>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    <DialogFooter><Button variant="outline" disabled={busy} onClick={() => setResume(undefined)}>Cancel</Button><Button disabled={busy} onClick={() => { if (resume) void change(resume, true) }}>{busy && <Loader2 className="animate-spin" />}Follow environment</Button></DialogFooter>
  </DialogContent></Dialog>
}

function FollowingSwitch({ workspace, following, sourceName }: { workspace: WorkspaceDTO; following: Following; sourceName: string }) {
  const checked = following.state?.environmentIds.includes(workspace.id) ?? false
  return <Switch checked={checked} disabled={following.busy} aria-label={`Follow ${sourceName} for ${workspace.name || workspace.id}`} onCheckedChange={(next) => { if (next) following.setResume(workspace); else void following.change(workspace, false) }} />
}

export function ComputerFollowingSettings({ peers }: { peers: PairedComputer[] }) {
  const { workspaces } = useWorkspaces()
  const following = useFollowing()
  const { state, busy, error, run } = following
  const [peer, setPeer] = useState('')
  const [stop, setStop] = useState(false)
  const [search, setSearch] = useState('')
  const name = peers.find((item) => item.fingerprint === state?.ownerFingerprint)?.name || state?.ownerFingerprint.slice(0, 12) || 'source'
  const ids = [...new Set([...(state?.environmentIds ?? []), ...(state?.excludedEnvironmentIds ?? [])])]
  const environments = ids.map((id) => workspaces.find((item) => item.id === id) ?? { id, name: id, type: 'routing' as const, applications: [] })
    .filter((workspace) => `${workspace.name} ${workspace.id}`.toLowerCase().includes(search.toLowerCase()))
    .sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id))
  return <section id="computer-following" className="space-y-4 rounded-lg border p-5">
    <h2 className="font-semibold">Follow a computer</h2>
    {state?.ownerFingerprint ? <>
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span>Following {name} · {state.environmentIds.length} environments · {workspaces.filter((workspace) => workspace.computerFollowed && workspace.session).length} sessions</span>
        <Badge variant={state.status === 'current' ? 'secondary' : 'outline'}>{state.status === 'current' ? 'Synced' : 'Cached configuration'}</Badge>
      </div>
      <Input aria-label="Find environment" placeholder="Find environment" value={search} onChange={(event) => setSearch(event.target.value)} />
      <div className="max-h-80 overflow-y-auto divide-y rounded-md border">
        {environments.map((workspace) => <div key={workspace.id} className="flex items-center justify-between gap-3 px-3 py-3 text-sm">
          <div className="min-w-0"><div className="truncate font-medium">{workspace.name || workspace.id}</div><div className="text-xs text-muted-foreground">{state.environmentIds.includes(workspace.id) ? `Following ${name}` : 'Local configuration'}</div></div>
          <FollowingSwitch workspace={workspace} following={following} sourceName={name} />
        </div>)}
        {!environments.length && <p className="px-3 py-3 text-sm text-muted-foreground">No environments found.</p>}
      </div>
      <div className="flex gap-2">
        <Button size="sm" variant="outline" disabled={busy} onClick={() => void run(syncComputerConfiguration)}><RefreshCw className={busy ? 'animate-spin' : ''} />Sync now</Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={() => setStop(true)}>Stop following</Button>
      </div>
    </> : <form className="flex flex-wrap items-end gap-3" onSubmit={(event) => { event.preventDefault(); if (state) void run(() => followComputer(peer, state.revision)) }}>
      <label className="grid flex-1 gap-2 text-sm">Computer
        <select aria-label="Computer to follow" className="h-9 rounded-md border bg-background px-3" value={peer} required disabled={busy} onChange={(event) => setPeer(event.target.value)}>
          <option value="">Choose a computer</option>
          {peers.map((item) => <option key={item.fingerprint} value={item.fingerprint}>{item.name}</option>)}
        </select>
      </label>
      <Button disabled={busy || !state || !peer}>{busy && <Loader2 className="animate-spin" />}Follow all environments</Button>
    </form>}
    {(error || state?.error) && <p role="alert" className="text-sm text-destructive">{error || state?.error}</p>}
    <ResumeDialog following={following} sourceName={name} />
    <Dialog open={stop} onOpenChange={setStop}><DialogContent>
      <DialogHeader><DialogTitle>Stop following {name}?</DialogTitle><DialogDescription>Restore previous settings for followed environments. Local environments stay as they are.</DialogDescription></DialogHeader>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      <DialogFooter><Button variant="outline" disabled={busy} onClick={() => setStop(false)}>Cancel</Button><Button disabled={busy} onClick={() => { if (state) void run(() => stopFollowingComputer(state.revision)).then((ok) => { if (ok) setStop(false) }) }}>Stop following</Button></DialogFooter>
    </DialogContent></Dialog>
  </section>
}

export function EnvironmentFollowingControl({ workspace }: { workspace: WorkspaceDTO }) {
  const following = useFollowing()
  const [peers, setPeers] = useState<PairedComputer[]>([])
  useEffect(() => { void getPeerState().then((result) => setPeers(result.computers)).catch(() => {}) }, [])
  const { state } = following
  if (workspace.session || !state?.ownerFingerprint || (!state.environmentIds.includes(workspace.id) && !state.excludedEnvironmentIds.includes(workspace.id))) return null
  const name = peers.find((peer) => peer.fingerprint === state.ownerFingerprint)?.name || state.ownerFingerprint.slice(0, 12)
  return <div className="space-y-2 text-sm">
    <div className="flex items-center gap-3"><FollowingSwitch workspace={workspace} following={following} sourceName={name} /><span>{state.environmentIds.includes(workspace.id) ? `Follow ${name}` : 'Local configuration'}</span></div>
    {following.error && <p role="alert" className="text-destructive">{following.error}</p>}
    <ResumeDialog following={following} sourceName={name} />
  </div>
}
