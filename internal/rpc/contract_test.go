package rpc

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSharedContractRoundTripsIdentityRouteAndOperationMetadata(t *testing.T) {
	created := timestamppb.New(time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC))
	completed := timestamppb.New(created.AsTime().Add(25 * time.Millisecond))
	want := &rementorv1.Application{
		Id:        "legacy-orders",
		AppId:     "orders",
		ServiceId: "orders-service",
		Identity: &rementorv1.CanonicalApplicationRef{
			AppId: "orders", ServiceId: "orders-service", Aliases: []string{"orders-api"}, LegacyId: "legacy-orders",
		},
		Environment: &rementorv1.WorkspaceEnvironmentRef{WorkspaceId: "desenvolvimento", Environment: "desenvolvimento", LegacyId: "dev"},
		Route: &rementorv1.RouteState{
			DesiredMode: rementorv1.RouteMode_ROUTE_MODE_LOCAL, EffectiveMode: rementorv1.RouteMode_ROUTE_MODE_LOCAL,
			Version: &rementorv1.RouteVersion{Value: 7}, OperationId: "op-7", VerifiedAt: completed,
		},
	}
	data, err := proto.Marshal(want)
	if err != nil {
		t.Fatalf("marshal application: %v", err)
	}
	var got rementorv1.Application
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal application: %v", err)
	}
	if !proto.Equal(want, &got) {
		t.Fatalf("application contract changed across serialization:\nwant %s\ngot %s", want, &got)
	}

	operation := &rementorv1.OperationMetadata{
		OperationId: "op-7", CorrelationId: "corr-7", RouteVersion: &rementorv1.RouteVersion{Value: 7},
		CreatedAt: created, CompletedAt: completed,
		Kind: rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_TOGGLE,
	}
	opData, err := proto.Marshal(operation)
	if err != nil {
		t.Fatalf("marshal operation: %v", err)
	}
	var decodedOperation rementorv1.OperationMetadata
	if err := proto.Unmarshal(opData, &decodedOperation); err != nil {
		t.Fatalf("unmarshal operation: %v", err)
	}
	if got := decodedOperation.GetRouteVersion().GetValue(); got != 7 {
		t.Fatalf("route version = %d, want 7", got)
	}
	if !decodedOperation.GetCompletedAt().AsTime().Equal(completed.AsTime()) {
		t.Fatalf("completed timestamp did not round-trip")
	}
}

func TestStructuredErrorUsesStableCodeAndHumanMessage(t *testing.T) {
	err := newRPCError(connect.CodeNotFound, errSentinel("orders not found"))
	if got, want := connect.CodeOf(err), connect.CodeNotFound; got != want {
		t.Fatalf("connect code = %s, want %s", got, want)
	}
	details := err.Details()
	if len(details) != 1 {
		t.Fatalf("got %d error details, want one", len(details))
	}
	detail, detailErr := details[0].Value()
	if detailErr != nil {
		t.Fatalf("decode error detail: %v", detailErr)
	}
	structured, ok := detail.(*rementorv1.StructuredError)
	if !ok {
		t.Fatalf("detail type = %T, want StructuredError", detail)
	}
	if structured.GetCode() != rementorv1.ErrorCode_ERROR_CODE_NOT_FOUND || structured.GetMessage() != "orders not found" {
		t.Fatalf("unexpected structured error: %s", structured)
	}
}

type errSentinel string

func (e errSentinel) Error() string { return string(e) }
