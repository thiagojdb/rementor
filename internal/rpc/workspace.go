package rpc

import (
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
)

func (s *ControlPlaneService) workspaceProto(ws *models.Workspace) *rementorv1.Workspace {
	return s.workspaceProtoWithSourcePorts(ws, s.registry.SourceRegistrationPorts())
}
func (s *ControlPlaneService) workspaceProtoWithSourcePorts(ws *models.Workspace, sourcePorts map[string]map[string]int) *rementorv1.Workspace {
	result := toProtoWorkspace(ws)
	if result != nil {
		result.ComputerFollowed = s.registry.IsComputerFollowed(ws.WorkspaceID)
		ports := sourcePorts[ws.WorkspaceID]
		for _, app := range result.Applications {
			if port, ok := ports[app.AppId]; ok {
				value := int32(port)
				app.SourcePort = &value
			}
		}
		result.BrowserUrl = services.PublicOrigin(services.WorkspaceBrowserHost(ws)) + "/"
	}
	return result
}
