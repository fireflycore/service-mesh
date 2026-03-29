package authz

import (
	"context"
	"net"
	"testing"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/pkg/config"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
)

type fakeAuthorizationServer struct {
	authv3.UnimplementedAuthorizationServer
	response *authv3.CheckResponse
	err      error
}

func (s *fakeAuthorizationServer) Check(context.Context, *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	return s.response, s.err
}

func TestExtAuthzAllow(t *testing.T) {
	address, stop := startAuthzServer(t, &fakeAuthorizationServer{
		response: &authv3.CheckResponse{
			Status: &rpcstatus.Status{Code: int32(grpccodes.OK)},
		},
	})
	defer stop()

	authorizer, err := NewExtAuthz(config.AuthzConfig{
		Target:    address,
		TimeoutMS: 500,
		FailOpen:  false,
	})
	if err != nil {
		t.Fatalf("new ext authz failed: %v", err)
	}

	err = authorizer.Check(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
			Port:      19090,
		},
		Method: "/acme.orders.v1.OrderService/GetOrder",
	})
	if err != nil {
		t.Fatalf("expected authz allow: %v", err)
	}
}

func TestExtAuthzDeny(t *testing.T) {
	address, stop := startAuthzServer(t, &fakeAuthorizationServer{
		response: &authv3.CheckResponse{
			Status: &rpcstatus.Status{
				Code:    int32(grpccodes.PermissionDenied),
				Message: "denied by policy",
			},
		},
	})
	defer stop()

	authorizer, err := NewExtAuthz(config.AuthzConfig{
		Target:    address,
		TimeoutMS: 500,
		FailOpen:  false,
	})
	if err != nil {
		t.Fatalf("new ext authz failed: %v", err)
	}

	err = authorizer.Check(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
			Port:      19090,
		},
		Method: "/acme.orders.v1.OrderService/GetOrder",
	})
	if err == nil {
		t.Fatal("expected authz deny")
	}
}

func TestExtAuthzFailOpen(t *testing.T) {
	authorizer, err := NewExtAuthz(config.AuthzConfig{
		Target:    "127.0.0.1:1",
		TimeoutMS: 50,
		FailOpen:  true,
	})
	if err != nil {
		t.Fatalf("new ext authz failed: %v", err)
	}

	err = authorizer.Check(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
			Port:      19090,
		},
		Method: "/acme.orders.v1.OrderService/GetOrder",
	})
	if err != nil {
		t.Fatalf("expected fail-open to allow request: %v", err)
	}
}

func startAuthzServer(t *testing.T, server authv3.AuthorizationServer) (string, func()) {
	t.Helper()

	grpcServer := grpc.NewServer()
	authv3.RegisterAuthorizationServer(grpcServer, server)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	return listener.Addr().String(), func() {
		grpcServer.Stop()
		_ = listener.Close()
	}
}
