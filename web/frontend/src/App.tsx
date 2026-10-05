import { useCallback, useEffect, useMemo, useState, type CSSProperties } from 'react'
import {
  ChevronRight,
  CircleDot,
  Copy,
  ExternalLink,
  FileCog,
  GitBranch,
  Globe,
  Laptop,
  Layers3,
  Loader2,
  MoreHorizontal,
  Moon,
  Network,
  Plus,
  RefreshCw,
  Search,
  Server,
  Settings2,
  Sun,
  Trash2,
  Unplug,
  Upload,
  X,
} from 'lucide-react'
import { toast, Toaster } from 'sonner'
import type { ApplicationDTO, WorkspaceDTO } from '@/api/types'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator } from '@/components/ui/breadcrumb'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Sidebar, SidebarContent, SidebarGroup, SidebarGroupAction, SidebarGroupContent, SidebarGroupLabel, SidebarHeader, SidebarInset, SidebarMenu, SidebarMenuAction, SidebarMenuButton, SidebarMenuItem, SidebarMenuSub, SidebarMenuSubButton, SidebarMenuSubItem, SidebarProvider, SidebarSeparator, SidebarTrigger, useSidebar } from '@/components/ui/sidebar'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { BaselineDialog, ConfirmDialog, CreateEnvironmentDialog, CreateSessionDialog, EditEnvironmentDialog, RegisterPortDialog, RoutePatternDialog, ServiceDialog } from '@/components/routing-dialogs'
import { useThemeTransition } from '@/hooks/use-theme-transition'
import { WorkspaceProvider, useWorkspaceHealth, useWorkspaces } from '@/lib/workspaces'

type RouteFilter = 'all' | 'local' | 'remote'
type ConfirmTarget =
  | { kind: 'environment'; workspace: WorkspaceDTO }
  | { kind: 'session'; workspace: WorkspaceDTO }
  | { kind: 'service'; workspace: WorkspaceDTO; application: ApplicationDTO }
  | null

function useAppLocation() {
  const [location, setLocation] = useState(() => ({ pathname: window.location.pathname, search: window.location.search }))
  useEffect(() => {
    const update = () => setLocation({ pathname: window.location.pathname, search: window.location.search })
    window.addEventListener('popstate', update)
    return () => window.removeEventListener('popstate', update)
  }, [])
  const navigate = useCallback((href: string) => {
    window.history.pushState({}, '', href)
    setLocation({ pathname: window.location.pathname, search: window.location.search })
  }, [])
  return { ...location, navigate }
}

function urlForScope(environment: WorkspaceDTO, session?: WorkspaceDTO) {
  const query = new URLSearchParams({ ws: environment.id })
  if (session) query.set('session', session.id)
  return `/?${query}`
}

function browserAddress(workspace: WorkspaceDTO) {
  if (workspace.browserUrl) return workspace.browserUrl
  const hostname = workspace.routing?.localDomain
  return hostname ? `http://${hostname}` : ''
}

function routeAddress(address: string, path: string) {
  if (!address) return path
  return `${address.replace(/\/$/, '')}/${path.replace(/^\//, '')}`
}

function applicationAddress(workspace: WorkspaceDTO, application: ApplicationDTO, address: string) {
  if (workspace.type === 'local-apps' && application.domain) return `http://${application.domain}`
  if (application.domain) return routeAddress(`http://${application.domain}`, application.publicPath || application.path)
  return routeAddress(address, application.publicPath || application.path)
}

function localHealthAddress(application: ApplicationDTO) {
  if (!application.port || !application.health) return ''
  const context = application.upstreamContext || application.context || ''
  return `http://localhost:${application.port}${context}/${application.health}`
}

function healthVariant(status: ApplicationDTO['healthStatus']) {
  if (status === 'unhealthy') return 'destructive' as const
  return 'outline' as const
}

function HealthBadge({ status }: { status: ApplicationDTO['healthStatus'] }) {
  const color = status === 'healthy' ? 'bg-emerald-500' : status === 'unhealthy' ? 'bg-destructive' : 'bg-muted-foreground'
  return <Badge variant={healthVariant(status)} className="gap-1.5 font-normal"><span className={`size-1.5 rounded-full ${color}`} />{status}</Badge>
}

function ScopeNavigation({
  environments,
  environment,
  active,
  configuration,
  onSelectScope,
  onConfiguration,
  onCreateEnvironment,
  onCreateSession,
  onEditEnvironment,
  onDeleteEnvironment,
  onSyncEnvironment,
}: {
  environments: WorkspaceDTO[]
  environment?: WorkspaceDTO
  active?: WorkspaceDTO
  configuration: boolean
  onSelectScope: (workspace: WorkspaceDTO) => void
  onConfiguration: () => void
  onCreateEnvironment: () => void
  onCreateSession: (workspace: WorkspaceDTO) => void
  onEditEnvironment: (workspace: WorkspaceDTO) => void
  onDeleteEnvironment: (workspace: WorkspaceDTO) => void
  onSyncEnvironment: (workspace: WorkspaceDTO) => void
}) {
  const { workspaces } = useWorkspaces()
  const { isMobile, setOpenMobile } = useSidebar()
  const [environmentFilter, setEnvironmentFilter] = useState('')
  const normalizedFilter = environmentFilter.trim().toLocaleLowerCase()
  const visibleEnvironments = environments.filter((workspace) =>
    !normalizedFilter || [workspace.name, workspace.id].some((value) => value?.toLocaleLowerCase().includes(normalizedFilter)),
  )
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const closeMobile = () => { if (isMobile) setOpenMobile(false) }
  const sessionsFor = (workspace: WorkspaceDTO) => workspaces.filter((item) => item.session?.environmentId === workspace.id)

  useEffect(() => {
    if (!environment) return
    setExpanded((current) => current[environment.id] ? current : { ...current, [environment.id]: true })
  }, [environment?.id])

  return (
    <Sidebar variant="sidebar" collapsible="offcanvas">
      <SidebarHeader className="p-4">
        <div className="flex items-center gap-3">
          <div className="flex size-9 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Network className="size-5" /></div>
          <div><p className="text-sm font-semibold tracking-tight">Rementor</p><p className="text-xs text-muted-foreground">Local routing</p></div>
        </div>
      </SidebarHeader>
      <SidebarGroup className="px-3">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton isActive={configuration} onClick={() => { closeMobile(); onConfiguration() }}><FileCog /><span>Configuration</span></SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarGroup>
      <SidebarSeparator />
      <SidebarGroup className="shrink-0 px-3 pt-4">
        <SidebarGroupLabel>Environments</SidebarGroupLabel>
        <Tooltip>
          <TooltipTrigger asChild><SidebarGroupAction aria-label="Create environment" onClick={() => { closeMobile(); onCreateEnvironment() }}><Plus /></SidebarGroupAction></TooltipTrigger>
          <TooltipContent side="right">Create environment</TooltipContent>
        </Tooltip>
        <div className="relative mt-2">
          <Search aria-hidden="true" className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted-foreground" />
          <Input
            type="search"
            aria-label="Search environments"
            placeholder="Search environments…"
            className="pl-9 pr-9 [&::-webkit-search-cancel-button]:appearance-none"
            value={environmentFilter}
            onChange={(event) => setEnvironmentFilter(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Escape') setEnvironmentFilter('')
              if (event.key === 'Enter' && visibleEnvironments.length === 1) {
                closeMobile()
                onSelectScope(visibleEnvironments[0])
              }
            }}
          />
          {environmentFilter && <Button variant="ghost" size="icon-xs" className="absolute right-1 top-1" aria-label="Clear environment search" onClick={() => setEnvironmentFilter('')}><X /></Button>}
        </div>
      </SidebarGroup>
      <SidebarContent>
        <SidebarGroup className="px-3 pt-0">
          <SidebarGroupContent>
            {visibleEnvironments.length === 0 && <p role="status" className="px-2 py-4 text-sm text-muted-foreground">{environments.length ? 'No environments match your search.' : 'No environments yet.'}</p>}
            <SidebarMenu className="gap-1 pt-2">
              {visibleEnvironments.map((workspace) => {
                const sessions = sessionsFor(workspace)
                const isEnvironmentActive = !configuration && environment?.id === workspace.id
                const isGeneral = isEnvironmentActive && !active?.session
                const isLocalApps = workspace.type === 'local-apps'
                return <Collapsible key={workspace.id} open={expanded[workspace.id] ?? isEnvironmentActive} onOpenChange={(open) => setExpanded((current) => ({ ...current, [workspace.id]: open }))} className="group/environment">
                  <SidebarMenuItem>
                    <SidebarMenuButton isActive={isEnvironmentActive} className="font-medium" onClick={() => { closeMobile(); onSelectScope(workspace) }}>
                      <Globe /><span>{workspace.name || workspace.id}</span>{sessions.length > 0 && <span className="ml-auto mr-5 text-xs font-normal text-muted-foreground">{sessions.length}</span>}
                    </SidebarMenuButton>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <SidebarMenuAction showOnHover aria-label={`Actions for ${workspace.name || workspace.id}`} onClick={(event) => event.stopPropagation()}><MoreHorizontal /></SidebarMenuAction>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuLabel>{workspace.name || workspace.id}</DropdownMenuLabel>
                        <DropdownMenuItem onSelect={() => onEditEnvironment(workspace)}><Settings2 />Environment settings</DropdownMenuItem>
                        {workspace.type === 'routing' && <DropdownMenuItem onSelect={() => onSyncEnvironment(workspace)}><RefreshCw />Sync routes</DropdownMenuItem>}
                        <DropdownMenuSeparator />
                        <DropdownMenuItem variant="destructive" onSelect={() => onDeleteEnvironment(workspace)}><Trash2 />Delete environment</DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                    <CollapsibleTrigger asChild>
                      <SidebarMenuAction className="right-7" aria-label={`Expand or collapse ${workspace.name || workspace.id}`}><ChevronRight className="transition-transform group-data-[state=open]/environment:rotate-90" /></SidebarMenuAction>
                    </CollapsibleTrigger>
                    <CollapsibleContent>
                      <SidebarMenuSub className="ml-4 gap-1 border-l pl-3">
                        <SidebarMenuSubItem>
                          <SidebarMenuSubButton asChild isActive={isGeneral}><button onClick={() => { closeMobile(); onSelectScope(workspace) }}><Network /><span>{isLocalApps ? 'Services' : 'General routing'}</span></button></SidebarMenuSubButton>
                        </SidebarMenuSubItem>
                        {!isLocalApps && <>
                          <li className="px-2 pb-1 pt-3 text-[11px] text-muted-foreground">Sessions</li>
                          {sessions.map((session) => <SidebarMenuSubItem key={session.id}>
                            <SidebarMenuSubButton asChild isActive={!configuration && active?.id === session.id}><button onClick={() => { closeMobile(); onSelectScope(session) }}><GitBranch /><span>{session.name || session.id}</span></button></SidebarMenuSubButton>
                          </SidebarMenuSubItem>)}
                          <SidebarMenuSubItem><SidebarMenuSubButton asChild className="text-muted-foreground"><button onClick={() => { closeMobile(); onCreateSession(workspace) }}><Plus /><span>New session</span></button></SidebarMenuSubButton></SidebarMenuSubItem>
                        </>}
                      </SidebarMenuSub>
                    </CollapsibleContent>
                  </SidebarMenuItem>
                </Collapsible>
              })}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
    </Sidebar>
  )
}

function WorkspaceApp() {
  const { workspaces, loading, error, reload, toggleApplication, toggleAll, syncRouting, deleteWorkspace, deleteApplication } = useWorkspaces()
  const { pathname, search, navigate } = useAppLocation()
  const { dark, toggleTheme } = useThemeTransition()
  const [filter, setFilter] = useState('')
  const [routeFilter, setRouteFilter] = useState<RouteFilter>('all')
  const [busyApplications, setBusyApplications] = useState<string[]>([])
  const [createEnvironment, setCreateEnvironment] = useState(false)
  const [editEnvironment, setEditEnvironment] = useState<WorkspaceDTO | null>(null)
  const [createSessionFor, setCreateSessionFor] = useState<WorkspaceDTO | null>(null)
  const [serviceTarget, setServiceTarget] = useState<ApplicationDTO | null | 'new'>(null)
  const [portTarget, setPortTarget] = useState<ApplicationDTO | null>(null)
  const [patternTarget, setPatternTarget] = useState<ApplicationDTO | null>(null)
  const [baselineTarget, setBaselineTarget] = useState<WorkspaceDTO | null>(null)
  const [confirmTarget, setConfirmTarget] = useState<ConfirmTarget>(null)

  const environments = useMemo(() => workspaces.filter((workspace) => !workspace.session), [workspaces])
  const selection = useMemo(() => {
    const params = new URLSearchParams(search)
    const requested = params.get('ws') || ''
    const requestedSession = params.get('session') || ''
    const legacyScope = workspaces.find((workspace) => workspace.id === requested)
    const environmentId = legacyScope?.session?.environmentId || requested || environments[0]?.id || ''
    const environment = environments.find((workspace) => workspace.id === environmentId) || environments[0]
    const sessionId = requestedSession || (legacyScope?.session ? legacyScope.id : '')
    const active = sessionId
      ? workspaces.find((workspace) => workspace.id === sessionId && workspace.session?.environmentId === environmentId)
      : environment
    return { environment, active }
  }, [environments, search, workspaces])

  const { environment, active } = selection
  const configuration = pathname === '/configuration'
  useWorkspaceHealth(configuration ? undefined : active?.id)

  useEffect(() => { setFilter(''); setRouteFilter('all') }, [active?.id])

  const selectScope = useCallback((workspace: WorkspaceDTO) => {
    const parent = workspace.session ? workspaces.find((item) => item.id === workspace.session?.environmentId) : workspace
    if (!parent) return
    navigate(urlForScope(parent, workspace.session ? workspace : undefined))
  }, [navigate, workspaces])

  const activeApplications = active?.applications || []
  const visibleApplications = useMemo(() => activeApplications.filter((application) => {
    const needle = filter.trim().toLowerCase()
    const textMatches = !needle || [application.id, application.appId, application.name, application.path, application.publicPath, application.domain].filter(Boolean).some((value) => value.toLowerCase().includes(needle))
    const local = Boolean(application.active)
    if (active?.type === 'local-apps' || routeFilter === 'all') return textMatches
    return textMatches && (routeFilter === 'local' ? local : !local)
  }), [activeApplications, filter, routeFilter])
  const address = active?.type === 'routing' ? browserAddress(active) : ''

  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      toast.success('Copied to clipboard')
    } catch {
      toast.error('Could not copy to the clipboard')
    }
  }

  const toggle = async (application: ApplicationDTO) => {
    if (!active || !application.port || busyApplications.includes(application.id)) return
    setBusyApplications((current) => [...current, application.id])
    try {
      await toggleApplication(active.id, application.id)
      toast.success(`${application.name} now uses ${application.active ? 'remote' : 'local'} routing`)
    } catch (requestError) {
      toast.error(requestError instanceof Error ? requestError.message : 'Could not change the route')
    } finally {
      setBusyApplications((current) => current.filter((id) => id !== application.id))
    }
  }

  const runAll = async (target: 'local' | 'remote') => {
    if (!active) return
    try {
      await toggleAll(active.id, target)
      toast.success(`All services now use ${target} routing`)
    } catch (requestError) {
      toast.error(requestError instanceof Error ? requestError.message : 'Could not change every route')
    }
  }

  const sync = async (workspace: WorkspaceDTO) => {
    try {
      await syncRouting(workspace.id)
      toast.success('Routes synchronized')
    } catch (requestError) {
      toast.error(requestError instanceof Error ? requestError.message : 'Could not synchronize routes')
    }
  }

  const confirm = async () => {
    if (!confirmTarget) return
    if (confirmTarget.kind === 'service') {
      await deleteApplication(confirmTarget.workspace.id, confirmTarget.application.id)
      toast.success(confirmTarget.workspace.session ? 'Service removed from the session' : 'Service removed')
      return
    }
    await deleteWorkspace(confirmTarget.workspace.id)
    toast.success(confirmTarget.kind === 'session' ? 'Session closed' : 'Environment deleted')
    if (confirmTarget.kind === 'session') {
      const parent = workspaces.find((workspace) => workspace.id === confirmTarget.workspace.session?.environmentId)
      if (parent) selectScope(parent)
    } else if (environment?.id === confirmTarget.workspace.id) {
      const next = environments.find((workspace) => workspace.id !== confirmTarget.workspace.id)
      if (next) selectScope(next)
      else navigate('/')
    }
  }

  const confirmation = confirmTarget?.kind === 'environment'
    ? { title: 'Delete environment?', label: 'Delete environment', description: <>This permanently removes <strong>{confirmTarget.workspace.name || confirmTarget.workspace.id}</strong>, including its registered services and sessions.</> }
    : confirmTarget?.kind === 'session'
      ? { title: 'Close session?', label: 'Close session', description: <>This removes <strong>{confirmTarget.workspace.name || confirmTarget.workspace.id}</strong> and its route settings. Registered processes keep running.</> }
      : confirmTarget?.kind === 'service'
        ? { title: confirmTarget.workspace.session ? 'Remove service from this session?' : 'Delete service?', label: confirmTarget.workspace.session ? 'Remove service' : 'Delete service', description: <>This removes <strong>{confirmTarget.application.name}</strong> from {confirmTarget.workspace.session ? 'this session only.' : 'the environment.'}</> }
        : null

  const title = active?.session ? active.name || active.id : active?.type === 'local-apps' ? active.name || active.id : active ? 'General routing' : 'Routing'
  const serviceDialogWorkspace = active && !active.session ? active : null

  return <TooltipProvider delayDuration={250}>
    <SidebarProvider style={{ '--sidebar-width': '17rem' } as CSSProperties}>
      <ScopeNavigation
        environments={environments}
        environment={environment}
        active={active}
        configuration={configuration}
        onSelectScope={selectScope}
        onConfiguration={() => navigate('/configuration')}
        onCreateEnvironment={() => setCreateEnvironment(true)}
        onCreateSession={setCreateSessionFor}
        onEditEnvironment={setEditEnvironment}
        onDeleteEnvironment={(workspace) => setConfirmTarget({ kind: 'environment', workspace })}
        onSyncEnvironment={sync}
      />
      <SidebarInset className="min-w-0 bg-background">
        <header className="flex h-16 shrink-0 items-center justify-between gap-3 border-b px-4 md:px-6">
          <div className="flex min-w-0 items-center gap-3">
            <SidebarTrigger />
            <Separator orientation="vertical" className="!h-4" />
            <Breadcrumb>
              {configuration ? <BreadcrumbList><BreadcrumbItem><BreadcrumbPage>Configuration</BreadcrumbPage></BreadcrumbItem></BreadcrumbList> : <BreadcrumbList className="flex-nowrap">
                {environment && <><BreadcrumbItem><BreadcrumbLink asChild><button onClick={() => selectScope(environment)}>{environment.name || environment.id}</button></BreadcrumbLink></BreadcrumbItem><BreadcrumbSeparator /></>}
                {active?.session && <><BreadcrumbItem className="hidden sm:block"><span>Sessions</span></BreadcrumbItem><BreadcrumbSeparator className="hidden sm:block" /></>}
                <BreadcrumbItem><BreadcrumbPage className="max-w-44 truncate">{title}</BreadcrumbPage></BreadcrumbItem>
              </BreadcrumbList>}
            </Breadcrumb>
          </div>
          <Tooltip>
            <TooltipTrigger asChild><Button variant="ghost" size="icon" aria-label={dark ? 'Switch to light theme' : 'Switch to dark theme'} onClick={toggleTheme}>{dark ? <Sun /> : <Moon />}</Button></TooltipTrigger>
            <TooltipContent>Switch to {dark ? 'light' : 'dark'} theme</TooltipContent>
          </Tooltip>
        </header>
        {configuration ? <main className="mx-auto w-full max-w-[1440px] flex-1 p-5 md:p-10"><h1 className="text-2xl font-semibold tracking-tight">Configuration</h1></main> : <main className="mx-auto w-full max-w-[1440px] flex-1 space-y-7 p-5 md:p-8 lg:p-10">
          {error && <Alert variant="destructive"><AlertTitle>Could not load environments</AlertTitle><AlertDescription><p>{error}</p><Button variant="outline" size="sm" className="mt-2" onClick={() => void reload()}>Retry</Button></AlertDescription></Alert>}
          {loading && workspaces.length === 0 ? <div className="space-y-4"><Skeleton className="h-9 w-56" /><Skeleton className="h-5 w-80" /><Skeleton className="h-72 w-full" /></div> : !active ? <div className="flex min-h-72 flex-col items-start justify-center gap-4"><div><h1 className="text-2xl font-semibold tracking-tight">No environment selected</h1><p className="mt-2 text-sm text-muted-foreground">Create an environment to register services and branch routing sessions.</p></div><Button onClick={() => setCreateEnvironment(true)}><Plus />Create environment</Button></div> : <>
            <div className="flex flex-wrap items-start justify-between gap-5">
              <div><h1 className="text-2xl font-semibold tracking-tight md:text-3xl">{title}</h1>{active.session && <p className="mt-1 text-sm text-muted-foreground">Branch of {environment?.name || environment?.id}</p>}</div>
              <div className="flex items-center gap-2">
                {!active.session && <Button onClick={() => setServiceTarget('new')}><Plus />Add service</Button>}
                <DropdownMenu>
                  <DropdownMenuTrigger asChild><Button variant="outline" size="icon" aria-label="Scope actions"><MoreHorizontal /></Button></DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuLabel>{active.session ? active.name || active.id : title}</DropdownMenuLabel>
                    {address && <DropdownMenuItem onSelect={() => void copy(address)}><Copy />Copy address</DropdownMenuItem>}
                    {active.type === 'routing' && <><DropdownMenuSeparator /><DropdownMenuItem onSelect={() => void runAll('remote')}><Upload />Route all remotely</DropdownMenuItem><DropdownMenuItem onSelect={() => void runAll('local')}><Laptop />Route all locally</DropdownMenuItem></>}
                    {active.session && <><DropdownMenuSeparator /><DropdownMenuItem onSelect={() => setBaselineTarget(active)}><RefreshCw />Review remote baseline</DropdownMenuItem><DropdownMenuSeparator /><DropdownMenuItem variant="destructive" onSelect={() => setConfirmTarget({ kind: 'session', workspace: active })}><Unplug />Close session</DropdownMenuItem></>}
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
            </div>
            {address && <div className="flex min-w-0 items-center gap-2 text-muted-foreground"><Globe className="size-4 shrink-0" /><code className="break-all text-xs">{address}</code><Tooltip><TooltipTrigger asChild><Button variant="ghost" size="icon-xs" className="shrink-0" aria-label="Copy address" onClick={() => void copy(address)}><Copy /></Button></TooltipTrigger><TooltipContent>Copy address</TooltipContent></Tooltip></div>}
            <section aria-label="Services" className="space-y-4">
              <div className="flex items-center justify-between gap-3">
                <div className="relative w-full max-w-sm"><Search className="absolute top-2.5 left-3 size-4 text-muted-foreground" /><Input aria-label="Filter services" placeholder="Filter services…" className="pl-9" value={filter} onChange={(event) => setFilter(event.target.value)} /></div>
                {active.type === 'routing' && <DropdownMenu>
                  <DropdownMenuTrigger asChild><Button variant="outline"><Settings2 /><span className="hidden sm:inline">{routeFilter === 'all' ? 'All routes' : routeFilter === 'local' ? 'Local only' : 'Remote only'}</span></Button></DropdownMenuTrigger>
                  <DropdownMenuContent align="end"><DropdownMenuLabel>Show routes</DropdownMenuLabel><DropdownMenuRadioGroup value={routeFilter} onValueChange={(value) => setRouteFilter(value as RouteFilter)}><DropdownMenuRadioItem value="all">All routes</DropdownMenuRadioItem><DropdownMenuRadioItem value="local">Local only</DropdownMenuRadioItem><DropdownMenuRadioItem value="remote">Remote only</DropdownMenuRadioItem></DropdownMenuRadioGroup></DropdownMenuContent>
                </DropdownMenu>}
              </div>
              <div className="overflow-x-auto rounded-lg border">
                <Table>
                  <TableHeader><TableRow className="bg-muted/30 hover:bg-muted/30"><TableHead className="pl-4">Service</TableHead><TableHead>Local port</TableHead>{active.type === 'routing' && <TableHead>Use local</TableHead>}<TableHead>Health</TableHead><TableHead className="w-12"><span className="sr-only">Actions</span></TableHead></TableRow></TableHeader>
                  <TableBody>
                    {visibleApplications.map((application) => {
                      const useLocal = Boolean(application.active)
                      const currentHealth = useLocal ? application.healthStatus : application.remoteStatus || 'unknown'
                      const isBusy = busyApplications.includes(application.id)
                      const healthAddress = localHealthAddress(application)
                      return <TableRow key={application.id}>
                        <TableCell className="py-5 pl-4"><div className="flex items-center gap-3"><Server className="size-4 shrink-0 text-muted-foreground" />{healthAddress ? <Tooltip><TooltipTrigger asChild><a className="inline-flex items-center gap-1 font-medium text-primary underline-offset-4 hover:underline focus-visible:underline" href={healthAddress} target="_blank" rel="noreferrer" aria-label={`Open ${application.name || application.id} local health check in a new tab`}>{application.name || application.id}<ExternalLink className="size-3.5" aria-hidden="true" /><span className="sr-only"> (opens in a new tab)</span></a></TooltipTrigger><TooltipContent>Open local health check in a new tab</TooltipContent></Tooltip> : <span className="font-medium">{application.name || application.id}</span>}</div></TableCell>
                        <TableCell><Button variant="link" className="h-auto px-0 py-1 text-xs font-normal" onClick={() => setPortTarget(application)}>{application.port ? <code>{application.port}</code> : 'Register port'}</Button></TableCell>
                        {active.type === 'routing' && <TableCell><Tooltip><TooltipTrigger asChild><span className="inline-flex"><Switch checked={useLocal} disabled={!application.port || isBusy} aria-label={`Route ${application.name || application.id} locally`} onCheckedChange={() => void toggle(application)} /></span></TooltipTrigger><TooltipContent>{application.port ? `Changes ${active.session ? active.name || active.id : 'general routing'} only` : 'Register a local port to enable this route'}</TooltipContent></Tooltip></TableCell>}
                        <TableCell><HealthBadge status={currentHealth} /></TableCell>
                        <TableCell><DropdownMenu><DropdownMenuTrigger asChild><Button variant="ghost" size="icon" aria-label={`Actions for ${application.name || application.id}`}><MoreHorizontal /></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuLabel>{application.name || application.id}</DropdownMenuLabel><DropdownMenuItem onSelect={() => setPortTarget(application)}><CircleDot />{application.port ? 'Edit local port' : 'Register local port'}</DropdownMenuItem>{!active.session && <DropdownMenuItem onSelect={() => setServiceTarget(application)}><Settings2 />Edit service</DropdownMenuItem>}<DropdownMenuItem onSelect={() => void copy(applicationAddress(active, application, address))}><Copy />Copy route address</DropdownMenuItem>{active.type === 'routing' && <DropdownMenuItem onSelect={() => setPatternTarget(application)}><Layers3 />Route pattern</DropdownMenuItem>}<DropdownMenuSeparator /><DropdownMenuItem variant="destructive" onSelect={() => setConfirmTarget({ kind: 'service', workspace: active, application })}><Trash2 />{active.session ? 'Remove from session' : 'Delete service'}</DropdownMenuItem></DropdownMenuContent></DropdownMenu></TableCell>
                      </TableRow>
                    })}
                    {visibleApplications.length === 0 && <TableRow><TableCell colSpan={active.type === 'routing' ? 5 : 4} className="h-32 text-center text-muted-foreground">{activeApplications.length ? 'No services match this filter.' : active.session ? 'No services are inherited by this session.' : 'No services yet. Add a service to configure its route.'}</TableCell></TableRow>}
                  </TableBody>
                </Table>
              </div>
            </section>
          </>}
        </main>}
      </SidebarInset>
    </SidebarProvider>
    <CreateEnvironmentDialog open={createEnvironment} onOpenChange={setCreateEnvironment} onCreated={selectScope} />
    <EditEnvironmentDialog workspace={editEnvironment} open={Boolean(editEnvironment)} onOpenChange={(open) => { if (!open) setEditEnvironment(null) }} />
    <CreateSessionDialog environment={createSessionFor} open={Boolean(createSessionFor)} onOpenChange={(open) => { if (!open) setCreateSessionFor(null) }} onCreated={selectScope} />
    <ServiceDialog workspace={serviceDialogWorkspace} application={serviceTarget === 'new' ? null : serviceTarget} open={serviceTarget !== null} onOpenChange={(open) => { if (!open) setServiceTarget(null) }} />
    <RegisterPortDialog workspace={active || null} application={portTarget} open={Boolean(portTarget)} onOpenChange={(open) => { if (!open) setPortTarget(null) }} />
    <RoutePatternDialog workspace={active || null} application={patternTarget} open={Boolean(patternTarget)} onOpenChange={(open) => { if (!open) setPatternTarget(null) }} />
    <BaselineDialog workspace={baselineTarget} open={Boolean(baselineTarget)} onOpenChange={(open) => { if (!open) setBaselineTarget(null) }} />
    {confirmation && <ConfirmDialog open={Boolean(confirmTarget)} onOpenChange={(open) => { if (!open) setConfirmTarget(null) }} title={confirmation.title} description={confirmation.description} confirmLabel={confirmation.label} onConfirm={confirm} />}
    <Toaster theme={dark ? 'dark' : 'light'} position="bottom-right" richColors closeButton />
  </TooltipProvider>
}

export default function App() {
  return <WorkspaceProvider><WorkspaceApp /></WorkspaceProvider>
}
