import { createStore, produce } from 'solid-js/store'
import { createSignal } from 'solid-js'
import type { WorkspaceDTO, ApplicationDTO, CreateWorkspaceRequest, UpdateWorkspaceRequest } from '../api/types'
import * as api from '../api/client'

// Store state
const [workspaces, setWorkspaces] = createStore<WorkspaceDTO[]>([])
const [loading, setLoading] = createSignal(false)
const [error, setError] = createSignal<string | null>(null)

export { workspaces, loading, error }

export async function loadWorkspaces(): Promise<void> {
  setLoading(true)
  setError(null)
  try {
    const data = await api.listWorkspaces()
    setWorkspaces(data)
  } catch (e) {
    setError(e instanceof Error ? e.message : 'Failed to load workspaces')
  } finally {
    setLoading(false)
  }
}

export function findWorkspace(wsId: string): WorkspaceDTO | undefined {
  return workspaces.find((w) => w.id === wsId)
}

export function findApplication(wsId: string, appId: string): ApplicationDTO | undefined {
  return findWorkspace(wsId)?.applications.find((a) => a.id === appId)
}

export async function toggleApplication(wsId: string, appId: string): Promise<void> {
  await api.toggleApplication(wsId, appId)
  const ws = await api.getWorkspace(wsId)
  setWorkspaces((w) => w.id === wsId, ws)
}

export async function toggleAllToRemote(wsId: string): Promise<void> {
  await api.toggleAllToRemote(wsId)
  // Reload workspace to get updated states
  const ws = await api.getWorkspace(wsId)
  setWorkspaces((w) => w.id === wsId, ws)
}

export async function toggleAllToLocal(wsId: string): Promise<void> {
  await api.toggleAllToLocal(wsId)
  const ws = await api.getWorkspace(wsId)
  setWorkspaces((w) => w.id === wsId, ws)
}

export async function updateRoutePattern(wsId: string, appId: string, pattern: string): Promise<void> {
  const updatedApp = await api.updateRoutePattern(wsId, appId, { pattern })
  setWorkspaces(
    (w) => w.id === wsId,
    'applications',
    (a) => a.id === appId,
    updatedApp
  )
}

export async function createWorkspace(req: CreateWorkspaceRequest): Promise<WorkspaceDTO> {
  const ws = await api.createWorkspace(req)
  setWorkspaces(produce((draft) => { draft.push(ws) }))
  return ws
}

export async function updateWorkspaceApplications(wsId: string, req: UpdateWorkspaceRequest): Promise<void> {
  const ws = await api.updateWorkspace(wsId, req)
  setWorkspaces((w) => w.id === wsId, ws)
}

export async function deleteWorkspace(wsId: string): Promise<void> {
  await api.deleteWorkspace(wsId)
  setWorkspaces(produce((draft) => {
    const idx = draft.findIndex((w) => w.id === wsId)
    if (idx !== -1) draft.splice(idx, 1)
  }))
}

export async function syncWorkspaceRouting(wsId: string): Promise<void> {
  await api.syncWorkspaceRouting(wsId)
  const ws = await api.getWorkspace(wsId)
  setWorkspaces((w) => w.id === wsId, ws)
}

// Update a single application's health/remote status from the Connect health stream.
export function updateAppHealth(wsId: string, appId: string, localOk: boolean, remoteOk: boolean): void {
  setWorkspaces(
    (w) => w.id === wsId,
    'applications',
    (a) => a.id === appId,
    produce((app: ApplicationDTO) => {
      app.healthStatus = localOk ? 'healthy' : 'unhealthy'
      app.remoteStatus = remoteOk ? 'healthy' : 'unhealthy'
    })
  )
}
