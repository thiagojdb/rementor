import { Code, ConnectError, createClient, type CallOptions, type Interceptor } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { ControlPlaneService } from '../gen/rementor/v1/rementor_connect'
import { StructuredError } from '../gen/rementor/v1/rementor_pb'
import type {
  ApplicationDTO,
  ApplicationConfigInput,
  BrowserURLResolutionDTO,
  CreateWorkspaceRequest,
  OperationMetadataDTO,
  RoutePatternDTO,
  ToggleResultDTO,
  UpdateRoutePatternRequest,
  UpdateWorkspaceRequest,
  WorkspaceDTO,
} from './types'

function normalizeError(error: unknown): Error {
  const connectError = ConnectError.from(error)
  const detail = connectError.findDetails(StructuredError)[0]
  const normalized = new Error(detail?.message || connectError.rawMessage || connectError.message)
  if (detail) {
    Object.assign(normalized, { code: detail.code, metadata: detail.metadata })
  }
  return normalized
}

function isInvalidCSRFToken(error: unknown): boolean {
  const connectError = ConnectError.from(error)
  const detail = connectError.findDetails(StructuredError)[0]
  const message = detail?.message || connectError.rawMessage || connectError.message
  return connectError.code === Code.PermissionDenied && message.trim().toLowerCase() === 'invalid csrf token'
}

function reloadAfterStaleCSRFToken() {
  const key = 'rementor-csrf-reload-at'
  try {
    const now = Date.now()
    const previous = Number(sessionStorage.getItem(key))
    if (Number.isFinite(previous) && now - previous < 5_000) return
    sessionStorage.setItem(key, String(now))
  } catch {
    // A blocked session store should not prevent recovery from a service restart.
  }
  window.location.reload()
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
    if (isInvalidCSRFToken(error)) reloadAfterStaleCSRFToken()
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
  return call(async () => {
    const response = await client.createWorkspace(req)
    return { ...response.workspace, operation: response.operation, warnings: response.warnings } as WorkspaceDTO
  })
}

export function updateWorkspace(wsId: string, req: UpdateWorkspaceRequest): Promise<WorkspaceDTO> {
  return call(async () => {
    const response = await client.updateWorkspace({ ...req, workspaceId: wsId })
    return { ...response.workspace, operation: response.operation, warnings: response.warnings } as WorkspaceDTO
  })
}

export async function deleteWorkspace(wsId: string): Promise<OperationMetadataDTO | undefined> {
  const response = await call(() => client.deleteWorkspace({ workspaceId: wsId }))
  return response.operation
}

export function listApplications(wsId: string): Promise<ApplicationDTO[]> {
  return call(async () => (await client.listApplications({ workspaceId: wsId })).applications as ApplicationDTO[])
}

export function getApplication(wsId: string, appId: string): Promise<ApplicationDTO> {
  return call(async () => (await client.getApplication({ workspaceId: wsId, applicationId: appId })).application as ApplicationDTO)
}

export function resolveApplication(wsId: string, applicationRef: string): Promise<ApplicationDTO> {
  return call(async () => (await client.resolveApplication({ workspaceId: wsId, applicationRef })).application as ApplicationDTO)
}

export function resolveBrowserURL(wsId: string, applicationRef: string): Promise<BrowserURLResolutionDTO> {
  return call(async () => (await client.resolveBrowserURL({ workspaceId: wsId, applicationRef })).resolution as BrowserURLResolutionDTO)
}

export const resolveURL = resolveBrowserURL
export const resolveApplicationURL = resolveBrowserURL

export function registerApplicationAlias(wsId: string, applicationRef: string, alias: string): Promise<ApplicationDTO> {
  return call(async () => {
    const response = await client.registerApplicationAlias({ workspaceId: wsId, applicationRef, alias })
    return { ...response.application, operation: response.operation } as ApplicationDTO
  })
}

export function deleteApplication(wsId: string, appId: string): Promise<OperationMetadataDTO | undefined> {
  return call(async () => {
    const response = await client.deleteApplication({ workspaceId: wsId, applicationId: appId })
    return response.operation
  })
}

export function upsertApplication(wsId: string, application: ApplicationConfigInput): Promise<ApplicationDTO> {
  return call(async () => {
    const response = await client.upsertApplication({ workspaceId: wsId, application })
    return { ...response.application, operation: response.operation } as ApplicationDTO
  })
}

export function toggleApplication(wsId: string, appId: string): Promise<ApplicationDTO> {
  return call(async () => {
    const response = await client.toggleApplication({ workspaceId: wsId, applicationId: appId })
    return { ...response.application, operation: response.operation } as ApplicationDTO
  })
}

export function toggleAllToRemote(wsId: string): Promise<ToggleResultDTO> {
  return call(() => client.toggleAllToRemote({ workspaceId: wsId }) as Promise<ToggleResultDTO>)
}

export function toggleAllToLocal(wsId: string): Promise<ToggleResultDTO> {
  return call(() => client.toggleAllToLocal({ workspaceId: wsId }) as Promise<ToggleResultDTO>)
}

export function syncWorkspaceRouting(wsId: string): Promise<{ status: string; operation?: OperationMetadataDTO }> {
  return call(async () => {
    const response = await client.syncWorkspaceRouting({ workspaceId: wsId })
    return { status: response.status, operation: response.operation }
  })
}

export function getRoutePattern(wsId: string, appId: string): Promise<RoutePatternDTO> {
  return call(() => client.getRoutePattern({ workspaceId: wsId, applicationId: appId }) as Promise<RoutePatternDTO>)
}

export function updateRoutePattern(
  wsId: string,
  appId: string,
  req: UpdateRoutePatternRequest
): Promise<ApplicationDTO> {
  return call(async () => {
    const response = await client.updateRoutePattern({ workspaceId: wsId, applicationId: appId, pattern: req.pattern, correlationId: req.correlationId })
    return { ...response.application, operation: response.operation } as ApplicationDTO
  })
}

export function watchHealth(wsId: string, options?: CallOptions) {
  return client.watchHealth({ workspaceId: wsId }, options)
}

export function createRoutingSession(environmentId: string, name: string): Promise<WorkspaceDTO> {
  return call(async () => (await client.createRoutingSession({ environmentId, name })).workspace as WorkspaceDTO)
}

export function refreshRoutingSession(id: string, previewToken = '', apply = false) {
  return call(() => client.refreshRoutingSession({ id, previewToken, apply }))
}

export function registerSessionApplication(workspaceId: string, application: { id: string; port: number }) {
  return call(() => client.upsertApplication({ workspaceId, application }))
}

export function getFleetConfiguration() {
  return call(() => client.getFleetConfiguration({}))
}

export function reviewFleetConfiguration(request: import('../gen/rementor/v1/rementor_pb').ReviewFleetConfigurationRequest) {
  return call(() => client.reviewFleetConfiguration(request))
}

export function saveFleetConfiguration(request: import('../gen/rementor/v1/rementor_pb').SaveFleetConfigurationRequest) {
  return call(() => client.saveFleetConfiguration(request))
}

export const getPeerState = () => call(() => client.getPeerState({}))
export const configurePeerConnection = (listenAddress: string, url: string) => call(async () => { const result = await client.configurePeerConnection({ listenAddress, url }); if (!result.state) throw new Error('Missing connection state'); return result.state })
export const createPeerInvitation = () => call(() => client.createPeerInvitation({}))
export const cancelPeerInvitation = () => call(() => client.cancelPeerInvitation({}))
export const inspectPeerInvitation = (invitation: string) => call(() => client.inspectPeerInvitation({ invitation }))
export const pairComputer = (invitation: string, confirmedFingerprint: string) => call(() => client.pairComputer({ invitation, confirmedFingerprint }))
export const unpairComputer = (fingerprint: string) => call(async () => { const result = await client.unpairComputer({ fingerprint }); if (!result.state) throw new Error('Missing connection state'); return result.state })
export const checkPeerConnection = (fingerprint: string) => call(() => client.checkPeerConnection({ fingerprint }))

export const listPeerWorkspaces = (fingerprint: string) => call(() => client.listPeerWorkspaces({ fingerprint }))
export const getWorkspaceInheritance = (workspaceId: string) => call(() => client.getWorkspaceInheritance({ workspaceId }))
export const followPeerWorkspace = (workspaceId: string, fingerprint: string, scope: import('@bufbuild/protobuf').PlainMessage<import('../gen/rementor/v1/rementor_pb').PeerWorkspaceScope>, expectedRouteVersion: bigint) => call(() => client.followPeerWorkspace({ workspaceId, fingerprint, scope, expectedRouteVersion }))
export const syncPeerWorkspace = (workspaceId: string) => call(() => client.syncPeerWorkspace({ workspaceId }))
export const overrideInheritedRoute = (workspaceId: string, applicationId: string, expectedRouteVersion: bigint, selection?: import('@bufbuild/protobuf').PlainMessage<import('../gen/rementor/v1/rementor_pb').InheritedRouteSelection>) => call(() => client.overrideInheritedRoute({ workspaceId, applicationId, expectedRouteVersion, selection }))
export const stopFollowingWorkspace = (workspaceId: string, expectedRouteVersion: bigint) => call(() => client.stopFollowingWorkspace({ workspaceId, expectedRouteVersion }))

export const getPublicAccess = () => call(() => client.getPublicAccess({}))
export const getTailscaleSharing = () => call(() => client.getTailscaleSharing({}))
export const setTailscaleSharing = (enabled: boolean, expectedRevision: bigint) => call(() => client.setTailscaleSharing({ enabled, expectedRevision }))
export const prepareTailscaleSharing = (workspaceId: string) => call(() => client.prepareTailscaleSharing({ workspaceId }))
export const savePublicAccess = (document: import('../gen/rementor/v1/rementor_pb').PublicAccessDocument, expectedRevision: bigint) => call(() => client.savePublicAccess({ document, expectedRevision }))

export const getSharedIngress = () => call(() => client.getSharedIngress({}))
export const createSharedIngress = (configuration: import('../gen/rementor/v1/rementor_pb').SharedIngressConfiguration, expectedRevision: bigint) => call(() => client.createSharedIngress({ configuration, expectedRevision }))
export const saveSharedIngress = (configuration: import('../gen/rementor/v1/rementor_pb').SharedIngressConfiguration, expectedVersion: bigint) => call(() => client.saveSharedIngress({ configuration, expectedVersion }))
export const joinSharedIngress = (ownerFingerprint: string, expectedRevision: bigint) => call(() => client.joinSharedIngress({ ownerFingerprint, expectedRevision }))
export const transferSharedIngress = (destinationFingerprint: string, expectedVersion: bigint) => call(() => client.transferSharedIngress({ destinationFingerprint, expectedVersion }))
export const recoverSharedIngress = (expectedRevision: bigint) => call(() => client.recoverSharedIngress({ expectedRevision }))
export const cancelSharedIngress = (expectedRevision: bigint) => call(() => client.cancelSharedIngress({ expectedRevision }))
export const syncSharedIngress = () => call(() => client.syncSharedIngress({}))

export const getRoutingWorkspaces = (routingOwnerFingerprint: string) => call(() => client.getRoutingWorkspaces({ routingOwnerFingerprint }))

export const getComputerFollowing = () => call(() => client.getComputerFollowing({}))
export const followComputer = (fingerprint: string, expectedRevision: bigint) => call(() => client.followComputer({fingerprint, expectedRevision}))
export const syncComputerConfiguration = () => call(() => client.syncComputerConfiguration({}))
export const stopFollowingComputer = (expectedRevision: bigint) => call(() => client.stopFollowingComputer({expectedRevision}))
export const setEnvironmentFollowing = (workspaceId: string, following: boolean, expectedRevision: bigint, expectedWorkspaceVersion: bigint) => call(() => client.setEnvironmentFollowing({workspaceId, following, expectedRevision, expectedWorkspaceVersion}))

export const editFollowedConfiguration = (request: import('@bufbuild/protobuf').PartialMessage<import('../gen/rementor/v1/rementor_pb').EditFollowedConfigurationRequest>) => call(() => client.editFollowedConfiguration(request))

export const getTailnetPublishing = () => call(() => client.getTailnetPublishing({}))
export const saveTailnetPublishing = (configuration: import('../gen/rementor/v1/rementor_pb').TailnetPublishingConfiguration, expectedRevision: bigint) => call(() => client.saveTailnetPublishing({configuration, expectedRevision}))
