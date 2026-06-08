import { ConnectError, createClient, type CallOptions, type Interceptor } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { ControlPlaneService } from '../gen/rementor/v1/rementor_connect'
import type {
  ApplicationDTO,
  CreateWorkspaceRequest,
  LoggersResponseDTO,
  RoutePatternDTO,
  SetLoggerLevelRequest,
  ToggleResultDTO,
  UpdateRoutePatternRequest,
  UpdateWorkspaceRequest,
  WorkspaceDTO,
} from './types'

function normalizeError(error: unknown): Error {
  const connectError = ConnectError.from(error)
  return new Error(connectError.rawMessage || connectError.message)
}

function csrfToken(): string {
  return document.querySelector<HTMLMetaElement>('meta[name="rementor-csrf"]')?.content ?? ''
}

const csrfInterceptor: Interceptor = (next) => async (req) => {
  const token = csrfToken()
  if (token) {
    req.header.set('X-Rementor-CSRF', token)
  }
  return next(req)
}

const transport = createConnectTransport({
  baseUrl: `${window.location.origin}/rpc`,
  credentials: 'same-origin',
  interceptors: [csrfInterceptor],
  useBinaryFormat: import.meta.env.VITE_CONNECT_JSON !== 'true' && import.meta.env.PROD,
})

const client = createClient(ControlPlaneService, transport)

async function call<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn()
  } catch (error) {
    throw normalizeError(error)
  }
}

export function listWorkspaces(): Promise<WorkspaceDTO[]> {
  return call(async () => (await client.listWorkspaces({})).workspaces as WorkspaceDTO[])
}

export function getWorkspace(wsId: string): Promise<WorkspaceDTO> {
  return call(async () => (await client.getWorkspace({ workspaceId: wsId })).workspace as WorkspaceDTO)
}

export function createWorkspace(req: CreateWorkspaceRequest): Promise<WorkspaceDTO> {
  return call(async () => (await client.createWorkspace(req)).workspace as WorkspaceDTO)
}

export function updateWorkspace(wsId: string, req: UpdateWorkspaceRequest): Promise<WorkspaceDTO> {
  return call(async () => (await client.updateWorkspace({ ...req, workspaceId: wsId })).workspace as WorkspaceDTO)
}

export async function deleteWorkspace(wsId: string): Promise<void> {
  await call(() => client.deleteWorkspace({ workspaceId: wsId }))
}

export function listApplications(wsId: string): Promise<ApplicationDTO[]> {
  return call(async () => (await client.listApplications({ workspaceId: wsId })).applications as ApplicationDTO[])
}

export function getApplication(wsId: string, appId: string): Promise<ApplicationDTO> {
  return call(async () => (await client.getApplication({ workspaceId: wsId, applicationId: appId })).application as ApplicationDTO)
}

export function toggleApplication(wsId: string, appId: string): Promise<ApplicationDTO> {
  return call(async () => (await client.toggleApplication({ workspaceId: wsId, applicationId: appId })).application as ApplicationDTO)
}

export function toggleAllToRemote(wsId: string): Promise<ToggleResultDTO> {
  return call(() => client.toggleAllToRemote({ workspaceId: wsId }) as Promise<ToggleResultDTO>)
}

export function toggleAllToLocal(wsId: string): Promise<ToggleResultDTO> {
  return call(() => client.toggleAllToLocal({ workspaceId: wsId }) as Promise<ToggleResultDTO>)
}

export function syncWorkspaceRouting(wsId: string): Promise<{ status: string }> {
  return call(() => client.syncWorkspaceRouting({ workspaceId: wsId }))
}

export function getRoutePattern(wsId: string, appId: string): Promise<RoutePatternDTO> {
  return call(() => client.getRoutePattern({ workspaceId: wsId, applicationId: appId }) as Promise<RoutePatternDTO>)
}

export function updateRoutePattern(
  wsId: string,
  appId: string,
  req: UpdateRoutePatternRequest
): Promise<ApplicationDTO> {
  return call(async () =>
    (await client.updateRoutePattern({ workspaceId: wsId, applicationId: appId, pattern: req.pattern })).application as ApplicationDTO
  )
}

export function getLoggers(wsId: string, appId: string): Promise<LoggersResponseDTO> {
  return call(() => client.getLoggers({ workspaceId: wsId, applicationId: appId }) as Promise<LoggersResponseDTO>)
}

export async function setLoggerLevel(
  wsId: string,
  appId: string,
  loggerName: string,
  req: SetLoggerLevelRequest
): Promise<void> {
  await call(() =>
    client.setLoggerLevel({
      workspaceId: wsId,
      applicationId: appId,
      loggerName,
      level: req.level,
    })
  )
}

export function watchHealth(wsId: string, options?: CallOptions) {
  return client.watchHealth({ workspaceId: wsId }, options)
}
