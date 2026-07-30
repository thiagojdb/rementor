import type { PlainMessage } from '@bufbuild/protobuf'
import type {
  Application,
  GetRoutePatternResponse,
  ToggleAllToRemoteResponse,
  WatchHealthResponse,
} from '../gen/rementor/v1/rementor_pb'

export type HealthStatus = 'healthy' | 'unhealthy' | 'unknown'
export type WorkspaceType = 'routing' | 'local-apps'

export type ApplicationDTO = Omit<PlainMessage<Application>, 'healthStatus' | 'remoteStatus'> & {
  healthStatus: HealthStatus
  remoteStatus?: HealthStatus
}

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
}

export type RoutePatternDTO = PlainMessage<GetRoutePatternResponse>
export type ToggleResultDTO = PlainMessage<ToggleAllToRemoteResponse>

export interface ErrorResponse {
  error: string
}

export interface CreateWorkspaceRequest {
  id: string
  type: WorkspaceType
  name: string
  color?: string
  localDomain: string
  defaultRemoteBaseUrl?: string
  applications: ApplicationConfigInput[]
}

export interface UpdateWorkspaceRequest {
  applications: ApplicationConfigInput[]
  localDomain: string
  defaultRemoteBaseUrl: string
}

export interface ApplicationConfigInput {
  id: string
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
}

export type HealthUpdateDTO = PlainMessage<WatchHealthResponse>
export type HealthUpdate = HealthUpdateDTO
