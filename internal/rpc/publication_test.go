package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/thiagojdb/rementor/internal/ingress"
	"github.com/thiagojdb/rementor/internal/publication"
)

type publicationStore struct{ c publication.Configuration }

func (s *publicationStore) Load() (publication.Configuration, error) { return s.c, nil }
func (s *publicationStore) Save(c publication.Configuration, _ uint64) (publication.Configuration, error) {
	s.c = c
	return c, nil
}

type publishingDevices struct{ discoveryError error }

func (publishingDevices) Devices(context.Context) ([]ingress.Device, error) {
	return []ingress.Device{
		{Name: "marcus", DNSName: "marcus.tail.example.ts.net", Self: true, Addresses: []string{"100.64.0.1"}},
		{Name: "rementor-marcus", DNSName: "rementor-marcus.tail.example.ts.net", Online: false},
	}, nil
}
func (d publishingDevices) ServePorts(context.Context) ([]string, error) {
	return []string{"443", "8443"}, d.discoveryError
}

func TestPublishingDiscoveryRecommendsUnusedNodeWithoutChangingConfiguration(t *testing.T) {
	store := &publicationStore{}
	m, err := publication.New(store, publication.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := &ControlPlaneService{tailnetPublishing: m, publicDevices: publishingDevices{}}
	state := s.publicationState(context.Background())
	if state.SuggestedNodeName != "rementor-marcus-2" || len(state.UsedServePorts) != 2 || state.Configuration.Suffix != "tail.example.ts.net" {
		t.Fatal(state)
	}
	if store.c.NodeName != "" || store.c.Enabled {
		t.Fatal("discovery connected a node or enabled publishing")
	}
	s.publicDevices = publishingDevices{discoveryError: errors.New("permission denied")}
	state = s.publicationState(context.Background())
	if state.DiscoveryError == "" || len(state.UsedServePorts) != 0 {
		t.Fatal("claimed ports were available without checking")
	}
}
