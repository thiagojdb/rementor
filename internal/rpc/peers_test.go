package rpc

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
)

func TestPeerEnrollmentRequiresBrowserCSRF(t *testing.T) {
	guard := NewCSRFGuard("local-management-token")
	for _, procedure := range []string{
		rementorv1connect.ControlPlaneServiceConfigurePeerConnectionProcedure,
		rementorv1connect.ControlPlaneServiceCreatePeerInvitationProcedure,
		rementorv1connect.ControlPlaneServiceCancelPeerInvitationProcedure,
		rementorv1connect.ControlPlaneServicePairComputerProcedure,
		rementorv1connect.ControlPlaneServiceUnpairComputerProcedure,
		rementorv1connect.ControlPlaneServiceCheckPeerConnectionProcedure,
	} {
		header := http.Header{"Origin": []string{"http://localhost:9300"}, "Sec-Fetch-Site": []string{"same-origin"}}
		if err := guard.validate(procedure, header); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("%s accepted missing token: %v", procedure, err)
		}
		header.Set(CSRFHeader, "local-management-token")
		if err := guard.validate(procedure, header); err != nil {
			t.Fatal(err)
		}
		header.Set("Sec-Fetch-Site", "cross-site")
		if err := guard.validate(procedure, header); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("%s accepted foreign request", procedure)
		}
	}
}
