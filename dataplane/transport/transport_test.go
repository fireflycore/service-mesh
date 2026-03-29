package transport

import (
	"context"
	"net"
	"testing"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type downstreamInvokeServer struct {
	invokev1.UnimplementedMeshInvokeServiceServer
}

// UnaryInvoke 模拟一个下游业务服务，把 payload 原样回显回来。
func (s *downstreamInvokeServer) UnaryInvoke(_ context.Context, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	return &invokev1.UnaryInvokeResponse{
		Payload: append([]byte("downstream:"), req.GetPayload()...),
		Codec:   req.GetCodec(),
	}, nil
}

// TestGRPCInvoke 验证 transport 可以透传原始 protobuf bytes。
func TestGRPCInvoke(t *testing.T) {
	server := grpc.NewServer()
	invokev1.RegisterMeshInvokeServiceServer(server, &downstreamInvokeServer{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port failed: %v", err)
	}

	portValue, err := net.LookupPort("tcp", port)
	if err != nil {
		t.Fatalf("lookup port failed: %v", err)
	}

	nestedReq, err := proto.Marshal(&invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service:   "inventory",
			Namespace: "default",
			Env:       "dev",
		},
		Method:  "/acme.inventory.v1.InventoryService/GetStock",
		Payload: []byte("payload"),
		Codec:   "json",
	})
	if err != nil {
		t.Fatalf("marshal nested request failed: %v", err)
	}

	transport := NewGRPC()
	resp, err := transport.Invoke(context.Background(), model.Endpoint{
		Address: host,
		Port:    uint32(portValue),
	}, &invokev1.UnaryInvokeRequest{
		Method:  invokev1.MeshInvokeService_UnaryInvoke_FullMethodName,
		Payload: nestedReq,
		Codec:   "proto",
	})
	if err != nil {
		t.Fatalf("transport invoke failed: %v", err)
	}

	var nestedResp invokev1.UnaryInvokeResponse
	if err := proto.Unmarshal(resp.GetPayload(), &nestedResp); err != nil {
		t.Fatalf("unmarshal nested response failed: %v", err)
	}

	if got, want := string(nestedResp.GetPayload()), "downstream:payload"; got != want {
		t.Fatalf("unexpected nested payload: got=%s want=%s", got, want)
	}
}
