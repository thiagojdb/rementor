package rpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/config"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/ingress"
	"github.com/thiagojdb/rementor/internal/models"
)

type publicDeviceCatalog interface {
	Devices(context.Context) ([]ingress.Device, error)
}

func (s *ControlPlaneService) SetPublicIngress(runtime *ingress.Runtime, devices publicDeviceCatalog) {
	s.publicIngress = runtime
	s.publicDevices = devices
}
func accessProto(current access.Configuration) *rementorv1.PublicAccessConfiguration {
	doc := current.Document
	result := &rementorv1.PublicAccessDocument{SchemaVersion: uint32(doc.SchemaVersion), SharedWorkspaceId: doc.SharedWorkspaceID, AllowedUserIds: append([]uint64(nil), doc.AllowedUserIDs...)}
	for _, origin := range doc.Origins {
		result.Origins = append(result.Origins, &rementorv1.PublicOrigin{Id: origin.ID, Url: origin.URL})
	}
	for _, binding := range doc.Bindings {
		result.Bindings = append(result.Bindings, &rementorv1.PublicOriginBinding{WorkspaceId: binding.WorkspaceID, OriginId: binding.OriginID, LocalHost: binding.LocalHost})
	}
	for _, device := range doc.Devices {
		result.Devices = append(result.Devices, &rementorv1.PublicDeviceSelection{NodeId: device.NodeID, WorkspaceId: device.WorkspaceID})
	}
	return &rementorv1.PublicAccessConfiguration{Revision: current.Revision, Document: result}
}
func accessDocument(msg *rementorv1.PublicAccessDocument) (access.Document, error) {
	if msg == nil {
		return access.Document{}, errors.New("public access document is required")
	}
	doc := access.Document{SchemaVersion: int(msg.SchemaVersion), SharedWorkspaceID: msg.SharedWorkspaceId, AllowedUserIDs: append([]uint64(nil), msg.AllowedUserIds...)}
	for _, origin := range msg.Origins {
		if origin == nil {
			return doc, errors.New("public origin is required")
		}
		doc.Origins = append(doc.Origins, access.Origin{ID: origin.Id, URL: origin.Url})
	}
	for _, binding := range msg.Bindings {
		if binding == nil {
			return doc, errors.New("origin binding is required")
		}
		doc.Bindings = append(doc.Bindings, access.Binding{WorkspaceID: binding.WorkspaceId, OriginID: binding.OriginId, LocalHost: binding.LocalHost})
	}
	for _, device := range msg.Devices {
		if device == nil {
			return doc, errors.New("device selection is required")
		}
		doc.Devices = append(doc.Devices, access.Device{NodeID: device.NodeId, WorkspaceID: device.WorkspaceId})
	}
	return doc, doc.Validate()
}
func (s *ControlPlaneService) GetPublicAccess(ctx context.Context, req *connect.Request[rementorv1.GetPublicAccessRequest]) (*connect.Response[rementorv1.GetPublicAccessResponse], error) {
	result := &rementorv1.GetPublicAccessResponse{Configuration: accessProto(s.registry.PublicAccessConfiguration())}
	if s.publicIngress != nil {
		state := s.publicIngress.State()
		result.Ingress = &rementorv1.PublicIngressState{ListenAddress: state.ListenAddress, NginxAddress: state.NginxAddress, Active: state.Active, Error: state.Error}
	}
	if s.publicDevices != nil {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		devices, err := s.publicDevices.Devices(ctx)
		if err != nil {
			result.DeviceError = err.Error()
		} else {
			for _, d := range devices {
				result.Devices = append(result.Devices, &rementorv1.PublicAccessDevice{NodeId: d.NodeID, UserId: d.UserID, Name: d.Name, DnsName: d.DNSName, Addresses: d.Addresses, Online: d.Online, Tagged: d.Tagged, Self: d.Self})
			}
		}
	}
	result.Workspaces = publicWorkspaceHostsProto(s.registry.PublicWorkspaceHosts())
	return connect.NewResponse(result), nil
}
func (s *ControlPlaneService) SavePublicAccess(ctx context.Context, req *connect.Request[rementorv1.SavePublicAccessRequest]) (*connect.Response[rementorv1.SavePublicAccessResponse], error) {
	doc, err := accessDocument(req.Msg.Document)
	if err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	if len(doc.Origins) > 0 {
		if s.publicIngress == nil {
			return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("public data plane is unavailable"))
		}
		if err := s.publicIngress.Start(); err != nil {
			return nil, newRPCError(connect.CodeFailedPrecondition, err)
		}
	}
	saved, err := s.registry.SavePublicAccessConfiguration(doc, req.Msg.ExpectedRevision)
	if err != nil {
		code := connect.CodeInvalidArgument
		if errors.Is(err, config.ErrPublicAccessConflict) {
			code = connect.CodeAborted
		}
		return nil, newRPCError(code, err)
	}
	if s.tailscaleSharing != nil {
		s.tailscaleSharing.Notify()
	}
	return connect.NewResponse(&rementorv1.SavePublicAccessResponse{Configuration: accessProto(saved)}), nil
}
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
		result.BrowserUrl = s.registry.PublicOriginForHost(ws.WorkspaceID, ws.GetLocalDomain()) + "/"
	}
	return result
}

func publicWorkspaceHostsProto(entries []access.WorkspaceHosts) []*rementorv1.PublicWorkspaceHosts {
	var result []*rementorv1.PublicWorkspaceHosts
	for _, entry := range entries {
		result = append(result, &rementorv1.PublicWorkspaceHosts{WorkspaceId: entry.WorkspaceID, Name: entry.Name, LocalHosts: append([]string(nil), entry.LocalHosts...)})
	}
	return result
}
func (s *ControlPlaneService) GetRoutingWorkspaces(ctx context.Context, req *connect.Request[rementorv1.GetRoutingWorkspacesRequest]) (*connect.Response[rementorv1.GetRoutingWorkspacesResponse], error) {
	if s.peers == nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("paired computer catalog unavailable"))
	}
	self := s.peers.State().Fingerprint
	owner := req.Msg.RoutingOwnerFingerprint
	if owner == "" {
		owner = self
	}
	var entries []access.WorkspaceHosts
	if owner == self {
		entries = s.registry.PublicWorkspaceHosts()
	} else {
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		catalog, err := s.peers.PublicWorkspaceCatalog(check, owner)
		cancel()
		if err != nil {
			return nil, newRPCError(connect.CodeUnavailable, err)
		}
		entries = catalog.Workspaces
	}
	return connect.NewResponse(&rementorv1.GetRoutingWorkspacesResponse{RoutingOwnerFingerprint: owner, Workspaces: publicWorkspaceHostsProto(entries)}), nil
}
