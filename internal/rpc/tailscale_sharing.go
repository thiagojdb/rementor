package rpc

import (
	"context"
	"errors"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/access"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/tailshare"
)

func (s *ControlPlaneService) SetTailscalePublisher(manager *tailshare.Manager) {
	s.tailscaleSharing = manager
}
func sharingProto(state tailshare.State) *rementorv1.TailscaleSharingState {
	result := &rementorv1.TailscaleSharingState{Revision: state.Revision, Enabled: state.Enabled, GatewayName: state.Gateway.Name, DnsSuffix: state.Gateway.DNSSuffix, SetupMessage: state.Gateway.Setup, AutomaticCreation: state.Gateway.CanCreate, Error: state.Error}
	for _, p := range state.Publications {
		result.Publications = append(result.Publications, &rementorv1.TailscalePublication{OriginId: p.Origin.ID, ServiceName: p.Origin.Name, Url: p.Origin.URL, Status: p.Status, Message: p.Message})
	}
	return result
}
func (s *ControlPlaneService) sharingPolicy() access.Configuration {
	if s.sharedIngress != nil {
		state := s.sharedIngress.State()
		if state.Record != nil {
			return access.Configuration{Revision: state.Record.Version, Document: state.Record.Policy.Clone()}
		}
	}
	return s.registry.PublicAccessConfiguration()
}
func (s *ControlPlaneService) GetTailscaleSharing(ctx context.Context, req *connect.Request[rementorv1.GetTailscaleSharingRequest]) (*connect.Response[rementorv1.GetTailscaleSharingResponse], error) {
	if s.tailscaleSharing == nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("Tailscale sharing is unavailable"))
	}
	return connect.NewResponse(&rementorv1.GetTailscaleSharingResponse{State: sharingProto(s.tailscaleSharing.State())}), nil
}
func (s *ControlPlaneService) SetTailscaleSharing(ctx context.Context, req *connect.Request[rementorv1.SetTailscaleSharingRequest]) (*connect.Response[rementorv1.SetTailscaleSharingResponse], error) {
	if s.tailscaleSharing == nil || s.publicIngress == nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("Tailscale sharing is unavailable"))
	}
	var origins []tailshare.Origin
	if req.Msg.Enabled {
		if s.sharedIngress != nil {
			state := s.sharedIngress.State()
			if state.Record != nil && (s.peers == nil || !slices.Contains(state.Record.Gateways, s.peers.State().Fingerprint)) {
				return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("this computer is not an enrolled shared gateway"))
			}
		}
		if err := s.publicIngress.Start(); err != nil {
			return nil, newRPCError(connect.CodeFailedPrecondition, err)
		}
		var err error
		origins, err = tailshare.Origins(s.sharingPolicy().Document, s.tailscaleSharing.State().Gateway.DNSSuffix, "http://"+s.publicIngress.State().ListenAddress)
		if err != nil {
			return nil, newRPCError(connect.CodeInvalidArgument, err)
		}
	}
	state, err := s.tailscaleSharing.Set(req.Msg.Enabled, origins, req.Msg.ExpectedRevision)
	if err != nil {
		code := connect.CodeInvalidArgument
		if errors.Is(err, tailshare.ErrConflict) {
			code = connect.CodeAborted
		}
		return nil, newRPCError(code, err)
	}
	return connect.NewResponse(&rementorv1.SetTailscaleSharingResponse{State: sharingProto(state)}), nil
}

// Preparation is read-only. Existing origin IDs, device selections, and host
// topology are retained; only URLs receive suggested service names.
func (s *ControlPlaneService) PrepareTailscaleSharing(ctx context.Context, req *connect.Request[rementorv1.PrepareTailscaleSharingRequest]) (*connect.Response[rementorv1.PrepareTailscaleSharingResponse], error) {
	if s.tailscaleSharing == nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("Tailscale sharing is unavailable"))
	}
	suffix := s.tailscaleSharing.State().Gateway.DNSSuffix
	if suffix == "" {
		return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("connect the Tailscale gateway to discover its HTTPS domain"))
	}
	doc := s.registry.PublicAccessConfiguration().Document.Clone()
	if s.sharedIngress != nil {
		state := s.sharedIngress.State()
		if state.Record != nil {
			if s.peers == nil || state.Record.Owner != s.peers.State().Fingerprint {
				return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("configure shared URLs on their owner; this gateway publishes the existing policy"))
			}
			doc = state.Record.Policy.Clone()
		}
	}
	if len(doc.Origins) == 0 {
		ws := s.registry.GetWorkspaceView(req.Msg.WorkspaceId)
		if ws == nil {
			return nil, newRPCError(connect.CodeInvalidArgument, errors.New("choose an environment or session"))
		}
		var hosts []string
		for _, entry := range s.registry.PublicWorkspaceHosts() {
			if entry.WorkspaceID == ws.WorkspaceID {
				hosts = entry.LocalHosts
			}
		}
		if len(hosts) == 0 {
			return nil, newRPCError(connect.CodeInvalidArgument, errors.New("the selected environment has no application routes"))
		}
		doc = access.Empty()
		doc.SharedWorkspaceID = ws.WorkspaceID
		for _, host := range hosts {
			id := tailshare.SuggestedName(host)
			if host == ws.GetLocalDomain() {
				id = "api"
			} else if strings.Contains(host, "front") {
				id = "frontend"
			}
			for _, origin := range doc.Origins {
				if origin.ID == id {
					id = tailshare.SuggestedName(host)
				}
			}
			doc.Origins = append(doc.Origins, access.Origin{ID: id})
			doc.Bindings = append(doc.Bindings, access.Binding{WorkspaceID: ws.WorkspaceID, OriginID: id, LocalHost: host})
		}
		if s.publicDevices != nil {
			devices, err := s.publicDevices.Devices(ctx)
			if err == nil {
				for _, d := range devices {
					if d.Self && !d.Tagged && d.UserID != 0 {
						doc.AllowedUserIDs = []uint64{d.UserID}
					}
				}
			}
		}
	}
	var hosts []string
	for _, binding := range doc.Bindings {
		if binding.WorkspaceID == doc.SharedWorkspaceID {
			hosts = append(hosts, binding.LocalHost)
		}
	}
	names := tailshare.SuggestedNames(hosts)
	knownURLs := map[string]bool{}
	for _, publication := range s.tailscaleSharing.State().Publications {
		knownURLs[publication.Origin.URL] = true
	}
	for i, origin := range doc.Origins {
		if knownURLs[origin.URL] {
			continue
		}
		for _, binding := range doc.Bindings {
			if binding.WorkspaceID == doc.SharedWorkspaceID && binding.OriginID == origin.ID {
				doc.Origins[i].URL = "https://" + names[binding.LocalHost] + "." + suffix
				break
			}
		}
	}
	return connect.NewResponse(&rementorv1.PrepareTailscaleSharingResponse{Document: accessProto(access.Configuration{Document: doc}).Document}), nil
}
