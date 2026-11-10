package rpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/publication"
	"github.com/thiagojdb/rementor/internal/tailnode"
)

func (s *ControlPlaneService) SetTailnetPublishing(m *publication.Manager) { s.tailnetPublishing = m }
func (s *ControlPlaneService) SetPublicationNetwork(n *tailnode.Network)   { s.publicationNetwork = n }
func (s *ControlPlaneService) PublicationCatalog(ctx context.Context, owner string) ([]publication.Scope, error) {
	if s.peers == nil {
		return nil, errors.New("paired computer catalog unavailable")
	}
	var result []publication.Scope
	if owner == s.peers.State().Fingerprint {
		for _, entry := range s.registry.PublicWorkspaceHosts() {
			ws := s.registry.GetWorkspaceView(entry.WorkspaceID)
			if ws != nil {
				result = append(result, publication.Scope{WorkspaceID: entry.WorkspaceID, Name: entry.Name, EnvironmentID: ws.EnvironmentID(), SessionID: ws.SessionID(), Hosts: entry.LocalHosts})
			}
		}
	} else {
		hosts, err := s.peers.PublicWorkspaceCatalog(ctx, owner)
		if err != nil {
			return nil, err
		}
		scopes, err := s.peers.PeerWorkspaces(ctx, owner)
		if err != nil {
			return nil, err
		}
		for _, entry := range hosts.Workspaces {
			for _, summary := range scopes.Workspaces {
				if summary.Scope.WorkspaceID == entry.WorkspaceID {
					result = append(result, publication.Scope{WorkspaceID: entry.WorkspaceID, Name: entry.Name, EnvironmentID: summary.Scope.EnvironmentID, SessionID: summary.Scope.SessionID, Hosts: entry.LocalHosts})
					break
				}
			}
		}
	}
	return result, nil
}
func scopeProto(s publication.Scope) *rementorv1.PublicWorkspaceHosts {
	return &rementorv1.PublicWorkspaceHosts{WorkspaceId: s.WorkspaceID, Name: s.Name, LocalHosts: append([]string(nil), s.Hosts...), EnvironmentId: s.EnvironmentID, SessionId: s.SessionID}
}
func publicationConfigurationProto(c publication.Configuration) *rementorv1.TailnetPublishingConfiguration {
	result := &rementorv1.TailnetPublishingConfiguration{Revision: c.Revision, Enabled: c.Enabled, Suffix: c.Suffix, CertificateMode: c.CertificateMode, ListenAddress: c.ListenAddress, NodeName: c.NodeName}
	for _, e := range c.Entries {
		result.Entries = append(result.Entries, &rementorv1.TailnetPublicationEntry{ComputerFingerprint: e.Computer, ComputerName: e.Name, WorkspaceId: e.WorkspaceID, IncludeSessions: e.IncludeSessions})
	}
	return result
}
func (s *ControlPlaneService) publicationState(ctx context.Context) *rementorv1.TailnetPublishingState {
	state := s.tailnetPublishing.State()
	result := &rementorv1.TailnetPublishingState{Configuration: publicationConfigurationProto(state.Configuration), DnsActive: state.DNSActive, HttpsActive: state.HTTPSActive, DnsError: state.DNSError, HttpsError: state.HTTPSError, CaCertificate: state.Certificate, CaFingerprint: publication.CertificateFingerprint(state.Certificate), ServingAddress: state.ListenAddress}
	if s.publicationNetwork != nil {
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		node := s.publicationNetwork.State(check)
		cancel()
		result.NodeDnsName, result.NodeAddresses, result.NodeStatus, result.NodeLoginUrl, result.NodeError = node.DNSName, node.Addresses, node.Status, node.LoginURL, node.Error
		if state.Configuration.NodeName != "" && node.Status == "Running" && state.HTTPSError != "" {
			result.NodeError = state.HTTPSError
		}
	}
	for _, p := range state.Scopes {
		entry := publicationConfigurationProto(publication.Configuration{Entries: []publication.Entry{p.Entry}}).Entries[0]
		scope := &rementorv1.TailnetPublishedScope{Entry: entry, Scope: scopeProto(p.Scope), Error: p.Error}
		for _, l := range p.Links {
			scope.Links = append(scope.Links, &rementorv1.TailnetPublicationLink{LocalHost: l.LocalHost, Url: l.URL})
		}
		result.Scopes = append(result.Scopes, scope)
	}
	if s.publicDevices != nil {
		check, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		devices, err := s.publicDevices.Devices(check)
		if err != nil {
			result.DeviceError = err.Error()
		} else {
			var used []string
			computer := "computer"
			for _, d := range devices {
				used = append(used, d.Name, d.DNSName)
				if d.Self {
					computer = d.Name
				}
			}
			result.SuggestedNodeName = tailnode.SuggestName(computer, used)
			for _, d := range devices {
				if !d.Self {
					continue
				}
				result.Addresses = append(result.Addresses, d.Addresses...)
				if state.Configuration.Revision == 0 {
					parts := strings.SplitN(d.DNSName, ".", 2)
					if len(parts) == 2 {
						result.Configuration.Suffix = parts[1]
					}
				}
				break
			}
		}
		if inspector, ok := s.publicDevices.(interface {
			ServePorts(context.Context) ([]string, error)
		}); ok {
			ports, err := inspector.ServePorts(check)
			if err != nil {
				result.DiscoveryError = err.Error()
			} else {
				result.UsedServePorts = ports
			}
		}
	}
	return result
}
func (s *ControlPlaneService) GetTailnetPublishing(ctx context.Context, req *connect.Request[rementorv1.GetTailnetPublishingRequest]) (*connect.Response[rementorv1.GetTailnetPublishingResponse], error) {
	if s.tailnetPublishing == nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("tailnet publisher unavailable"))
	}
	return connect.NewResponse(&rementorv1.GetTailnetPublishingResponse{State: s.publicationState(ctx)}), nil
}
func (s *ControlPlaneService) SaveTailnetPublishing(ctx context.Context, req *connect.Request[rementorv1.SaveTailnetPublishingRequest]) (*connect.Response[rementorv1.SaveTailnetPublishingResponse], error) {
	if s.tailnetPublishing == nil || req.Msg.Configuration == nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("tailnet publication settings are required"))
	}
	input := req.Msg.Configuration
	c := publication.Configuration{Enabled: input.Enabled, Suffix: input.Suffix, CertificateMode: input.CertificateMode, ListenAddress: input.ListenAddress, NodeName: input.NodeName}
	for _, e := range input.Entries {
		if e == nil {
			return nil, newRPCError(connect.CodeInvalidArgument, errors.New("publication entry is required"))
		}
		c.Entries = append(c.Entries, publication.Entry{Computer: e.ComputerFingerprint, Name: e.ComputerName, WorkspaceID: e.WorkspaceId, IncludeSessions: e.IncludeSessions})
	}
	if err := c.Validate(); err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	existing := s.tailnetPublishing.State().Configuration
	// Removals and disabling remain possible when a source is offline. New or
	// changed targets must be checked against their owning computer's catalog.
	catalogs := map[string][]publication.Scope{}
	hosts := map[string]string{}
	for _, e := range c.Entries {
		if _, ok := catalogs[e.Computer]; !ok {
			list, err := s.PublicationCatalog(ctx, e.Computer)
			if err != nil {
				unchanged := false
				for _, old := range existing.Entries {
					if old == e {
						unchanged = true
					}
				}
				if !unchanged {
					return nil, newRPCError(connect.CodeUnavailable, err)
				}
				continue
			}
			catalogs[e.Computer] = list
		}
		found := false
		for _, scope := range catalogs[e.Computer] {
			if scope.WorkspaceID == e.WorkspaceID {
				found = true
				if scope.SessionID != "" && e.IncludeSessions {
					return nil, newRPCError(connect.CodeInvalidArgument, errors.New("a session cannot include other sessions"))
				}
			}
			if scope.WorkspaceID != e.WorkspaceID && !(e.IncludeSessions && scope.SessionID != "" && scope.EnvironmentID == e.WorkspaceID) {
				continue
			}
			if len(scope.Hosts) > 32 {
				return nil, newRPCError(connect.CodeInvalidArgument, errors.New("an environment can publish at most 32 application hostnames"))
			}
			for _, host := range scope.Hosts {
				name, err := publication.Hostname(host, e.Name, c.Suffix)
				if err != nil {
					return nil, newRPCError(connect.CodeInvalidArgument, err)
				}
				if _, ok := hosts[name]; ok {
					return nil, newRPCError(connect.CodeAlreadyExists, errors.New("hostname is already included in another publication"))
				}
				hosts[name] = e.WorkspaceID
			}
		}
		if !found {
			return nil, newRPCError(connect.CodeNotFound, errors.New("selected environment is unavailable on its source computer"))
		}
	}
	if _, err := s.tailnetPublishing.Save(c, req.Msg.ExpectedRevision); err != nil {
		code := connect.CodeInternal
		if errors.Is(err, publication.ErrConflict) {
			code = connect.CodeAborted
		}
		return nil, newRPCError(code, err)
	}
	return connect.NewResponse(&rementorv1.SaveTailnetPublishingResponse{State: s.publicationState(ctx)}), nil
}
