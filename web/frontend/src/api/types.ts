import type { PlainMessage } from '@bufbuild/protobuf'
import type {
  Application,
  BrowserURLResolution,
  CanonicalApplicationRef,
  GetRoutePatternResponse,
  OperationMetadata,
  RouteState,
  ToggleAllToRemoteResponse,
  WorkspaceEnvironmentRef,
  WatchHealthResponse,
} from '../gen/rementor/v1/rementor_pb'

export type HealthStatus = 'healthy' | 'unhealthy' | 'unknown'
export type WorkspaceType = 'routing' | 'local-apps'

export type ApplicationDTO = Omit<PlainMessage<Application>, 'healthStatus' | 'remoteStatus'> & {
  healthStatus: HealthStatus
  remoteStatus?: HealthStatus
  operation?: OperationMetadataDTO
}

export type CanonicalApplicationRefDTO = PlainMessage<CanonicalApplicationRef>
export type WorkspaceEnvironmentRefDTO = PlainMessage<WorkspaceEnvironmentRef>
export type RouteStateDTO = PlainMessage<RouteState>
export type OperationMetadataDTO = PlainMessage<OperationMetadata>
export type BrowserURLResolutionDTO = PlainMessage<BrowserURLResolution>

export interface RoutingDTO {
  mode: string
  localDomain: string
  defaultRemoteBaseUrl: string
}

export interface WorkspaceDTO {
  id: string
  type: WorkspaceType
  name: string
  color?: string
  routing?: RoutingDTO
  applications: ApplicationDTO[]
  environment?: WorkspaceEnvironmentRefDTO
  route?: RouteStateDTO
  operation?: OperationMetadataDTO
}

export type RoutePatternDTO = PlainMessage<GetRoutePatternResponse>
export type ToggleResultDTO = PlainMessage<ToggleAllToRemoteResponse>

export interface ErrorResponse {
  error: string
  code?: string
}

export interface CreateWorkspaceRequest {
  id: string
  type: WorkspaceType
  name: string
  color?: string
  localDomain: string
  defaultRemoteBaseUrl?: string
  applications: ApplicationConfigInput[]
  correlationId?: string
}

export interface UpdateWorkspaceRequest {
  applications: ApplicationConfigInput[]
  localDomain: string
  defaultRemoteBaseUrl: string
  correlationId?: string
}

export interface ApplicationConfigInput {
  id: string
  appId?: string
  serviceId?: string
  repository?: string
  aliases?: string[]
  name: string
  path: string
  domain?: string
  remoteBaseUrl?: string
  port?: number
  health?: string
  context?: string
}

export interface UpdateRoutePatternRequest {
  pattern: string
  correlationId?: string
}

export type HealthUpdateDTO = PlainMessage<WatchHealthResponse>
export type HealthUpdate = HealthUpdateDTO
