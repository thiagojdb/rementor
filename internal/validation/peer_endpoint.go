package validation

import (
	"encoding/hex"
	"fmt"

	"github.com/thiagojdb/rementor/internal/models"
)

func PeerEndpoint(endpoint *models.PeerEndpoint) error {
	if endpoint == nil {
		return nil
	}
	for _, value := range []string{endpoint.OwnerFingerprint, endpoint.Digest} {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != value {
			return fmt.Errorf("invalid peer endpoint identity or digest")
		}
	}
	if err := Identifier("peer endpoint workspace ID", endpoint.WorkspaceID); err != nil {
		return err
	}
	return IdentityIdentifier("peer endpoint application ID", endpoint.ApplicationID)
}
