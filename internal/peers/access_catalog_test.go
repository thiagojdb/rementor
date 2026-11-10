package peers

import (
	"context"
	"net/http"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
)

type catalogSource struct {
	*snapshotSource
	entries []access.WorkspaceHosts
	started chan struct{}
	release chan struct{}
}

func (s *catalogSource) PublicWorkspaceHosts() []access.WorkspaceHosts {
	if s.started != nil {
		close(s.started)
		<-s.release
	}
	return s.entries
}

func TestRoutingWorkspaceCatalogUsesPinnedOwnerAndRechecksRevocation(t *testing.T) {
	owner, client, stranger := testManager(t, "routing-owner"), testManager(t, "moved-selector"), testManager(t, "stranger")
	source := &catalogSource{snapshotSource: &snapshotSource{}, entries: []access.WorkspaceHosts{{WorkspaceID: "dev", Name: "Routing Development", LocalHosts: []string{"dev.localhost"}}, {WorkspaceID: "qa", Name: "Routing Quality", LocalHosts: []string{"qa.localhost"}}}}
	owner.SetWorkspaceSource(source)
	if got := status(t, stranger, owner, publicWorkspaceCatalogPath); got != http.StatusForbidden {
		t.Fatal("unpaired catalog request accepted", got)
	}
	code, invitation := invite(t, owner)
	if _, err := client.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	catalog, err := client.PublicWorkspaceCatalog(context.Background(), invitation.Fingerprint)
	if err != nil || catalog.OwnerFingerprint != invitation.Fingerprint || len(catalog.Workspaces) != 2 || catalog.Workspaces[1].Name != "Routing Quality" {
		t.Fatal("catalog lost source namespace", catalog, err)
	}
	if got := status(t, client, owner, publicWorkspaceCatalogPath+"?workspace=dev"); got != http.StatusNotFound {
		t.Fatal("catalog accepted unexpected query", got)
	}
	if got := status(t, client, owner, "/rpc/rementor.v1.ControlPlaneService/GetRoutingWorkspaces"); got != http.StatusNotFound {
		t.Fatal("catalog exposed management", got)
	}
	source.started, source.release = make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := client.PublicWorkspaceCatalog(context.Background(), invitation.Fingerprint)
		finished <- err
	}()
	<-source.started
	if err := owner.Revoke(client.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	close(source.release)
	if err := <-finished; err == nil {
		t.Fatal("revoked caller received catalog after blocked export")
	}
}
