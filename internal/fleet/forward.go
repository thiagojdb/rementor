package fleet

import (
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/validation"
)

// EndpointRef identifies a binding in a validated owner snapshot. It deliberately
// contains no URL or port: only the owning instance may resolve the endpoint.
type EndpointRef = models.PeerEndpoint

func ValidateEndpointRef(ref EndpointRef) error {
	return validation.PeerEndpoint(&ref)
}

func (snapshot Snapshot) Endpoint(applicationID string) EndpointRef {
	for _, app := range snapshot.Intent.Applications {
		if app.ID == applicationID && app.PeerEndpoint != nil {
			return *app.PeerEndpoint
		}
	}
	return EndpointRef{OwnerFingerprint: snapshot.OwnerFingerprint, WorkspaceID: snapshot.Intent.Scope.WorkspaceID, ApplicationID: applicationID, Version: snapshot.Version, Digest: snapshot.Digest}
}
