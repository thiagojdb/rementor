import { Code, ConnectError } from '@connectrpc/connect'
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import * as api from '@/api/client'
import type { ApplicationConfigInput, ApplicationDTO, CreateWorkspaceRequest, UpdateWorkspaceRequest, WorkspaceDTO } from '@/api/types'

type SessionRefresh = Awaited<ReturnType<typeof api.refreshRoutingSession>>

interface WorkspaceStore {
  workspaces: WorkspaceDTO[]
  loading: boolean
  error: string | null
  reload: () => Promise<void>
  refreshWorkspace: (id: string) => Promise<WorkspaceDTO>
  createWorkspace: (request: CreateWorkspaceRequest) => Promise<WorkspaceDTO>
  updateWorkspace: (id: string, request: UpdateWorkspaceRequest) => Promise<WorkspaceDTO>
  deleteWorkspace: (id: string) => Promise<void>
  upsertApplication: (workspaceId: string, application: ApplicationConfigInput) => Promise<WorkspaceDTO>
  registerSessionApplication: (workspaceId: string, application: { id: string; port: number }) => Promise<WorkspaceDTO>
  deleteApplication: (workspaceId: string, applicationId: string) => Promise<WorkspaceDTO>
  updateRoutePattern: (workspaceId: string, applicationId: string, pattern: string) => Promise<WorkspaceDTO>
  toggleApplication: (workspaceId: string, applicationId: string) => Promise<WorkspaceDTO>
  toggleAll: (workspaceId: string, target: 'local' | 'remote') => Promise<WorkspaceDTO>
  syncRouting: (workspaceId: string) => Promise<WorkspaceDTO>
  createRoutingSession: (environmentId: string, name: string) => Promise<WorkspaceDTO>
  refreshRoutingSession: (id: string, previewToken?: string, apply?: boolean) => Promise<SessionRefresh>
  updateHealth: (workspaceId: string, applicationRef: string, localOk: boolean, remoteOk: boolean) => void
}

const WorkspaceContext = createContext<WorkspaceStore | null>(null)

function message(error: unknown): string {
  return error instanceof Error ? error.message : 'The request could not be completed.'
}

function matchesApplication(application: ApplicationDTO, reference: string): boolean {
  const target = reference.trim().toLowerCase()
  return [application.id, application.appId, ...application.aliases]
    .filter(Boolean)
    .some((value) => value.toLowerCase() === target)
}

export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const [workspaces, setWorkspaces] = useState<WorkspaceDTO[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const replaceWorkspace = useCallback((workspace: WorkspaceDTO) => {
    setWorkspaces((current) => {
      const index = current.findIndex((item) => item.id === workspace.id)
      if (index === -1) return [...current, workspace]
      return current.map((item) => item.id === workspace.id ? workspace : item)
    })
  }, [])

  const reload = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      setWorkspaces(await api.listWorkspaces())
    } catch (requestError) {
      setError(message(requestError))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void reload() }, [reload])

  const refreshWorkspace = useCallback(async (id: string) => {
    const workspace = await api.getWorkspace(id)
    replaceWorkspace(workspace)
    return workspace
  }, [replaceWorkspace])

  const createWorkspace = useCallback(async (request: CreateWorkspaceRequest) => {
    const workspace = await api.createWorkspace(request)
    replaceWorkspace(workspace)
    return workspace
  }, [replaceWorkspace])

  const updateWorkspace = useCallback(async (id: string, request: UpdateWorkspaceRequest) => {
    const workspace = await api.updateWorkspace(id, request)
    replaceWorkspace(workspace)
    return workspace
  }, [replaceWorkspace])

  const deleteWorkspace = useCallback(async (id: string) => {
    await api.deleteWorkspace(id)
    setWorkspaces((current) => current.filter((workspace) => workspace.id !== id && workspace.session?.environmentId !== id))
  }, [])

  const upsertApplication = useCallback(async (workspaceId: string, application: ApplicationConfigInput) => {
    await api.upsertApplication(workspaceId, application)
    return refreshWorkspace(workspaceId)
  }, [refreshWorkspace])

  const registerSessionApplication = useCallback(async (workspaceId: string, application: { id: string; port: number }) => {
    await api.registerSessionApplication(workspaceId, application)
    return refreshWorkspace(workspaceId)
  }, [refreshWorkspace])

  const deleteApplication = useCallback(async (workspaceId: string, applicationId: string) => {
    await api.deleteApplication(workspaceId, applicationId)
    return refreshWorkspace(workspaceId)
  }, [refreshWorkspace])

  const updateRoutePattern = useCallback(async (workspaceId: string, applicationId: string, pattern: string) => {
    await api.updateRoutePattern(workspaceId, applicationId, { pattern })
    return refreshWorkspace(workspaceId)
  }, [refreshWorkspace])

  const toggleApplication = useCallback(async (workspaceId: string, applicationId: string) => {
    await api.toggleApplication(workspaceId, applicationId)
    return refreshWorkspace(workspaceId)
  }, [refreshWorkspace])

  const toggleAll = useCallback(async (workspaceId: string, target: 'local' | 'remote') => {
    if (target === 'local') await api.toggleAllToLocal(workspaceId)
    else await api.toggleAllToRemote(workspaceId)
    return refreshWorkspace(workspaceId)
  }, [refreshWorkspace])

  const syncRouting = useCallback(async (workspaceId: string) => {
    await api.syncWorkspaceRouting(workspaceId)
    return refreshWorkspace(workspaceId)
  }, [refreshWorkspace])

  const createRoutingSession = useCallback(async (environmentId: string, name: string) => {
    const workspace = await api.createRoutingSession(environmentId, name)
    replaceWorkspace(workspace)
    return workspace
  }, [replaceWorkspace])

  const refreshRoutingSession = useCallback(async (id: string, previewToken = '', apply = false) => {
    const result = await api.refreshRoutingSession(id, previewToken, apply)
    if (apply && result.workspace) replaceWorkspace(result.workspace as WorkspaceDTO)
    return result
  }, [replaceWorkspace])

  const updateHealth = useCallback((workspaceId: string, applicationRef: string, localOk: boolean, remoteOk: boolean) => {
    setWorkspaces((current) => current.map((workspace) => {
      if (workspace.id !== workspaceId) return workspace
      return {
        ...workspace,
        applications: workspace.applications.map((application) => matchesApplication(application, applicationRef)
          ? { ...application, healthStatus: localOk ? 'healthy' : 'unhealthy', remoteStatus: remoteOk ? 'healthy' : 'unhealthy' }
          : application),
      }
    }))
  }, [])

  const value = useMemo<WorkspaceStore>(() => ({
    workspaces,
    loading,
    error,
    reload,
    refreshWorkspace,
    createWorkspace,
    updateWorkspace,
    deleteWorkspace,
    upsertApplication,
    registerSessionApplication,
    deleteApplication,
    updateRoutePattern,
    toggleApplication,
    toggleAll,
    syncRouting,
    createRoutingSession,
    refreshRoutingSession,
    updateHealth,
  }), [
    workspaces, loading, error, reload, refreshWorkspace, createWorkspace, updateWorkspace, deleteWorkspace,
    upsertApplication, registerSessionApplication, deleteApplication, updateRoutePattern, toggleApplication,
    toggleAll, syncRouting, createRoutingSession, refreshRoutingSession, updateHealth,
  ])

  return <WorkspaceContext.Provider value={value}>{children}</WorkspaceContext.Provider>
}

export function useWorkspaces(): WorkspaceStore {
  const value = useContext(WorkspaceContext)
  if (!value) throw new Error('useWorkspaces must be used within WorkspaceProvider')
  return value
}

export function useWorkspaceHealth(workspaceId: string | undefined) {
  const { updateHealth } = useWorkspaces()

  useEffect(() => {
    if (!workspaceId) return
    const controller = new AbortController()
    let retryTimer: ReturnType<typeof setTimeout> | undefined
    let retryDelay = 1000
    const watch = async () => {
      try {
        for await (const update of api.watchHealth(workspaceId, { signal: controller.signal })) {
          if (controller.signal.aborted) return
          retryDelay = 1000
          if (update.workspaceId && update.applicationName) {
            updateHealth(update.workspaceId, update.applicationName, update.localOk ?? false, update.remoteOk ?? false)
          }
        }
      } catch (requestError) {
        if (controller.signal.aborted || ConnectError.from(requestError).code === Code.Canceled) return
      }
      if (!controller.signal.aborted) {
        retryTimer = setTimeout(() => { void watch() }, retryDelay)
        retryDelay = Math.min(retryDelay * 2, 30000)
      }
    }
    void watch()
    return () => {
      controller.abort()
      clearTimeout(retryTimer)
    }
  }, [workspaceId, updateHealth])
}
