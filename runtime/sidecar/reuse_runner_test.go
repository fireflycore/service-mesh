package sidecar

import (
	"context"
	"net"
	"testing"
	"time"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// TestSidecarReusesInvokeRuntime 验证 sidecar 模式不是空壳，
// 而是和 agent 一样真正挂载了 MeshInvokeService。
func TestSidecarReusesInvokeRuntime(t *testing.T) {
	// 先预留一个空闲端口，再交给真正的 sidecar runtime 监听。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()

	cfg := config.Default()
	cfg.Mode = model.ModeSidecar
	cfg.Runtime.Sidecar.Address = address
	cfg.Runtime.Sidecar.ServiceName = "config"
	cfg.Runtime.Sidecar.InstanceID = "config-1"
	cfg.Runtime.Sidecar.Namespace = "/microservice/lhdht"
	cfg.Runtime.Sidecar.Env = "dev"
	cfg.ControlPlane.Enabled = false
	// 这里关闭 telemetry，避免测试依赖本地 collector。
	cfg.Telemetry.TraceEnabled = false
	cfg.Telemetry.MetricEnabled = false
	cfg.Telemetry.LogEnabled = false

	runner, err := New(&cfg)
	if err != nil {
		t.Fatalf("new sidecar runner failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runner.Run(ctx)
	}()

	// 给 gRPC server 一个最小启动窗口。
	time.Sleep(150 * time.Millisecond)

	conn, err := grpc.DialContext(
		context.Background(),
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial sidecar failed: %v", err)
	}
	defer conn.Close()

	_, err = invokev1.NewMeshInvokeServiceClient(conn).UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{})
	if err == nil {
		t.Fatal("expected invalid request to fail")
	}

	statusErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error: %v", err)
	}
	if statusErr.Code() != codes.InvalidArgument {
		t.Fatalf("unexpected status code: %s", statusErr.Code())
	}

	cancel()

	// 取消 ctx 后，runner 应该能在可接受时间内优雅退出。
	select {
	case runErr := <-errCh:
		if runErr != nil {
			t.Fatalf("unexpected runner error: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("sidecar runner did not stop")
	}
}
